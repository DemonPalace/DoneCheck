package verify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxReviewFiles    = 200
	largeRemoval      = 100 // deleted lines that make a removal worth a human look
	gitCommandTimeout = 15 * time.Second
)

type reviewItem struct{ path, reason string }

// reviewCandidates lists changed files a human should see; ordinary files a code_scan covers are only counted.
func reviewCandidates(ctx context.Context, cfg *Config, opts Options) ([]reviewItem, string) {
	if !opts.AllowCommands {
		return nil, "which files? none configured: set \"paths\" on this check, or pass --allow-commands to let DoneCheck pick the risky changed files via git"
	}
	stats := execute(ctx, cfg, []string{"git", "diff", "--numstat", "--relative", "-z", "HEAD"}, gitCommandTimeout)
	untracked := execute(ctx, cfg, []string{"git", "ls-files", "--others", "--exclude-standard", "-z"}, gitCommandTimeout)
	for _, ex := range []execution{stats, untracked} {
		if ex.problem != "" || ex.exit == nil || *ex.exit != 0 {
			return nil, "which files? could not list changed files with git (not a git repository, no commits yet, or git missing); set \"paths\" on this check"
		}
	}

	changes := parseNumstat(stats.output)
	for _, p := range strings.Split(untracked.output, "\x00") {
		if p != "" {
			changes = append(changes, change{path: p, added: -1, deleted: 0, untracked: true})
		}
	}

	var items []reviewItem
	others := 0
	seen := map[string]bool{}
	for _, ch := range changes {
		if ch.path == "" || seen[ch.path] || checkRelPath(ch.path) != nil || (opts.SkipReview != nil && opts.SkipReview(ch.path)) {
			continue
		}
		seen[ch.path] = true
		if reason := reviewReason(cfg, ch); reason != "" {
			items = append(items, reviewItem{ch.path, reason})
		} else {
			others++
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })

	note := ""
	switch {
	case stats.truncated || untracked.truncated:
		note = "the list from git was truncated"
	case len(items) == 0 && others == 0:
		note = "git reports no changed files; nothing specific to review"
	case others > 0:
		note = fmt.Sprintf("%d other changed file(s) are text files covered by the code_scan check and are not listed", others)
	}
	return items, note
}

type change struct {
	path           string
	added, deleted int // added is -1 for binary files and for untracked files
	binary         bool
	untracked      bool
}

func parseNumstat(out string) []change {
	var res []change
	toks := strings.Split(out, "\x00")
	for i := 0; i < len(toks); i++ {
		f := strings.SplitN(toks[i], "\t", 3)
		if len(f) != 3 {
			continue
		}
		ch := change{path: f[2]}
		if f[0] == "-" || f[1] == "-" {
			ch.binary, ch.added = true, -1
		} else {
			ch.added, _ = strconv.Atoi(f[0])
			ch.deleted, _ = strconv.Atoi(f[1])
		}
		if f[2] == "" && i+2 < len(toks) { // rename: old and new path follow
			ch.path = toks[i+2]
			i += 2
		}
		res = append(res, ch)
	}
	return res
}

var (
	dependencyFiles = map[string]bool{
		"go.mod": true, "go.sum": true, "package.json": true, "package-lock.json": true, "yarn.lock": true,
		"pnpm-lock.yaml": true, "Cargo.toml": true, "Cargo.lock": true, "pyproject.toml": true, "Pipfile": true,
		"Pipfile.lock": true, "poetry.lock": true, "composer.json": true, "composer.lock": true, "Gemfile": true,
		"Gemfile.lock": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "setup.py": true,
	}
	agentFiles = map[string]bool{"AGENTS.md": true, ".cursorrules": true}
)

func reviewReason(cfg *Config, ch change) string {
	p, base := ch.path, path.Base(ch.path)
	lower := strings.ToLower(base)
	switch {
	case base == IgnoreFileName:
		return "scan ignore list: a change here hides findings from the code scan"
	case base == "donecheck.json":
		return "DoneCheck configuration: a change here can weaken the checks"
	case strings.HasPrefix(p, ".skill/") || agentFiles[base] || p == ".github/copilot-instructions.md":
		return "instructions for AI agents: a change here alters how agents behave"
	case strings.HasPrefix(p, ".github/workflows/") || strings.HasPrefix(p, ".circleci/") || base == ".gitlab-ci.yml" || base == "Jenkinsfile":
		return "CI/CD pipeline definition"
	case dependencyFiles[base] || strings.HasPrefix(lower, "requirements") && strings.HasSuffix(lower, ".txt"):
		return "dependency manifest: new or upgraded dependencies"
	case strings.HasPrefix(lower, "dockerfile") || strings.HasPrefix(lower, "docker-compose") || lower == "makefile" || strings.HasSuffix(lower, ".tf"):
		return "build or infrastructure definition"
	case strings.HasSuffix(lower, ".sh") || strings.HasSuffix(lower, ".ps1") || strings.HasSuffix(lower, ".bat"):
		return "script that runs with your privileges"
	case strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12"):
		return "credentials-related file"
	case ch.binary || (ch.untracked && looksBinary(cfg.Root, ch.path)):
		return "binary file: the code scan cannot read it"
	case isTestFile(p) && ch.deleted > ch.added && ch.added >= 0:
		return fmt.Sprintf("test file lost %d net lines: tests may have been removed or weakened", ch.deleted-ch.added)
	case ch.deleted >= largeRemoval && ch.deleted > 3*max(ch.added, 0):
		return fmt.Sprintf("large removal (%d lines deleted)", ch.deleted)
	case !scanCovers(cfg, ch.path):
		return "not covered by any code_scan check (ignored, too large, unreadable or no scan configured)"
	}
	return ""
}

func isTestFile(p string) bool {
	lower := strings.ToLower(p)
	base := path.Base(lower)
	return strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") || strings.HasPrefix(base, "test_") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.Contains(lower, "/tests/") || strings.HasPrefix(lower, "tests/") || strings.Contains(lower, "/__tests__/")
}

func scanCovers(cfg *Config, rel string) bool {
	var scan *Check
	for i := range cfg.Checks {
		if cfg.Checks[i].Type == CodeScan {
			scan = &cfg.Checks[i]
			break
		}
	}
	if scan == nil {
		return false
	}
	if scope := path.Clean(filepath.ToSlash(scan.Path)); scan.Path != "" && rel != scope && !strings.HasPrefix(rel, scope+"/") {
		return false
	}
	for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
		if skipDirs[path.Base(d)] {
			return false
		}
	}
	if !scanEligible(path.Base(rel), rel, scan.IgnorePaths) {
		return false
	}
	full := filepath.Join(cfg.Root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScanFileBytes {
		return false
	}
	f, err := os.Open(full)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	return bytes.IndexByte(head[:n], 0) < 0
}

func reviewList(items []reviewItem, note string) string {
	var b strings.Builder
	if len(items) > 0 {
		b.WriteString("review these files:\n")
		for i, it := range items {
			if i == maxReviewFiles {
				fmt.Fprintf(&b, "  ... and %d more\n", len(items)-maxReviewFiles)
				break
			}
			if it.reason == "" {
				fmt.Fprintf(&b, "  %s\n", it.path)
			} else {
				fmt.Fprintf(&b, "  %s  (%s)\n", it.path, it.reason)
			}
		}
	}
	if note != "" {
		b.WriteString(note + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func looksBinary(root, rel string) bool {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8192)
	n, _ := f.Read(head)
	return bytes.IndexByte(head[:n], 0) >= 0
}
