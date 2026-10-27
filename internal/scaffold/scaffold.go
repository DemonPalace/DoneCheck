// Package scaffold implements `donecheck init`, which never overwrites an existing file.
package scaffold

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"donecheck/internal/verify"
)

//go:embed all:templates
var templates embed.FS

const (
	ConfigName = "donecheck.json"
	osvScanID  = "osv-scan"
)

var files = []struct{ dest, src string }{
	{"AGENTS.md", "templates/AGENTS.md"},
	{".skill/README.md", "templates/skill/README.md"},
	{".skill/anti-ai-slop/SKILL.md", "templates/skill/anti-ai-slop/SKILL.md"},
	{".skill/donecheck/SKILL.md", "templates/skill/donecheck/SKILL.md"},
	{".skill/donecheck/CHECKS.md", "templates/skill/donecheck/CHECKS.md"},
	{".skill/test-first/SKILL.md", "templates/skill/test-first/SKILL.md"},
	{".skill/systematic-debugging/SKILL.md", "templates/skill/systematic-debugging/SKILL.md"},
	{".skill/self-review/SKILL.md", "templates/skill/self-review/SKILL.md"},
}

// Starter checks are added only when they can give a real result here: tests must exist, audits need a lockfile.
var ecosystems = []func(root string) []verify.Check{goChecks, nodeChecks, rustChecks, pythonChecks, mavenChecks}

func goChecks(root string) []verify.Check {
	if !exists(root, "go.mod") {
		return nil
	}
	checks := []verify.Check{{ID: "go-vet", Type: verify.CommandExitCode, Command: []string{"go", "vet", "./..."}}}
	if anyFile(root, func(name string) bool { return strings.HasSuffix(name, "_test.go") }) {
		checks = append(checks, verify.Check{ID: "go-test", Type: verify.TestSuite, Command: []string{"go", "test", "./..."}, OutputContains: "ok"})
	}
	return append(checks, verify.Check{ID: "go-vulncheck", Type: verify.DependencyAudit, Tool: "govulncheck", Path: "go.mod"})
}

func nodeChecks(root string) []verify.Check {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	var checks []verify.Check
	// npm's generated default script ("Error: no test specified") is not a test.
	if json.Unmarshal(data, &pkg) == nil {
		if t := strings.TrimSpace(pkg.Scripts["test"]); t != "" && !strings.Contains(t, "no test specified") {
			checks = append(checks, verify.Check{ID: "npm-test", Type: verify.TestSuite, Command: []string{"npm", "test"}})
		}
	}
	if exists(root, "package-lock.json", "npm-shrinkwrap.json") {
		checks = append(checks, verify.Check{ID: "npm-audit", Type: verify.DependencyAudit, Tool: "npm", Path: "package.json"})
	}
	return checks
}

func rustChecks(root string) []verify.Check {
	if !exists(root, "Cargo.toml") {
		return nil
	}
	checks := []verify.Check{{ID: "cargo-test", Type: verify.TestSuite, Command: []string{"cargo", "test"}}}
	if exists(root, "Cargo.lock") {
		checks = append(checks, osvScan("Cargo.lock"))
	}
	return checks
}

func pythonChecks(root string) []verify.Check {
	if !exists(root, "pyproject.toml", "setup.py", "requirements.txt", "Pipfile", "poetry.lock") {
		return nil
	}
	var checks []verify.Check
	if anyFile(root, func(name string) bool {
		return strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") || strings.HasSuffix(name, "_test.py")
	}) {
		checks = append(checks, verify.Check{ID: "pytest", Type: verify.TestSuite, Command: []string{"python3", "-m", "pytest"}})
	}
	for _, lock := range []string{"poetry.lock", "Pipfile.lock", "uv.lock", "requirements.txt"} {
		if exists(root, lock) {
			return append(checks, osvScan(lock))
		}
	}
	return checks
}

func mavenChecks(root string) []verify.Check {
	if !exists(root, "pom.xml") {
		return nil
	}
	return []verify.Check{
		{ID: "maven-test", Type: verify.TestSuite, Command: []string{"mvn", "test"}},
		osvScan("pom.xml"),
	}
}

func osvScan(manifest string) verify.Check {
	return verify.Check{ID: osvScanID, Type: verify.DependencyAudit, Tool: "osv-scanner", Path: manifest}
}

func exists(root string, names ...string) bool {
	for _, n := range names {
		if info, err := os.Stat(filepath.Join(root, n)); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func anyFile(root string, match func(name string) bool) bool {
	found, seen := false, 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case found || seen > 20000:
			return fs.SkipAll
		case d.IsDir() && p != root && verify.IsSkippedDir(d.Name()):
			return fs.SkipDir
		}
		seen++
		found = !d.IsDir() && match(d.Name())
		return nil
	})
	return found
}

type Action struct {
	Path    string
	Created bool // false: the path already existed and was left untouched
}

func Init(root string) ([]Action, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolving project directory: %w", err)
	}

	var actions []Action
	for _, f := range files {
		content, readErr := templates.ReadFile(f.src)
		if readErr != nil {
			return actions, fmt.Errorf("reading embedded %s: %w", f.src, readErr)
		}
		a, writeErr := writeNew(realRoot, f.dest, content)
		if writeErr != nil {
			return actions, writeErr
		}
		actions = append(actions, a)
	}

	cfg, err := starterConfig(realRoot)
	if err != nil {
		return actions, err
	}
	a, err := writeNew(realRoot, ConfigName, cfg)
	if err != nil {
		return actions, err
	}
	return append(actions, a), nil
}

func writeNew(root, rel string, content []byte) (Action, error) {
	act := Action{Path: rel}
	full := filepath.Join(root, filepath.FromSlash(rel))
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return act, fmt.Errorf("creating %s: %w", filepath.Dir(rel), err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return act, fmt.Errorf("resolving %s: %w", filepath.Dir(rel), err)
	}
	if relDir, err := filepath.Rel(root, realDir); err != nil || relDir == ".." || strings.HasPrefix(relDir, ".."+string(filepath.Separator)) {
		return act, fmt.Errorf("refusing to write %s: directory resolves outside the project", rel)
	}

	// O_EXCL fails if anything exists at the path, including a symlink.
	f, err := os.OpenFile(filepath.Join(realDir, filepath.Base(full)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return act, nil
	}
	if err != nil {
		return act, fmt.Errorf("creating %s: %w", rel, err)
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return act, fmt.Errorf("writing %s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return act, fmt.Errorf("writing %s: %w", rel, err)
	}
	act.Created = true
	return act, nil
}

func starterConfig(root string) ([]byte, error) {
	var checks []verify.Check
	seen := map[string]bool{}
	for _, detect := range ecosystems {
		for _, c := range detect(root) {
			if !seen[c.ID] { // several ecosystems share the osv-scan check
				seen[c.ID] = true
				checks = append(checks, c)
			}
		}
	}
	checks = append(checks, verify.Check{ID: "code-scan", Type: verify.CodeScan})
	checks = append(checks, verify.Check{
		ID: "human-review", Type: verify.ManualReview, Description: "A person must read the diff and confirm the change is the one that was asked for: tests and scans only show the code runs and is clean, not that it is the right change",
	})
	data, err := json.MarshalIndent(verify.Config{Checks: checks}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("building starter config: %w", err)
	}
	return append(data, '\n'), nil
}

// Pristine reports whether rel is a file init writes and is still exactly as written.
func Pristine(root, rel string) bool {
	var want []byte
	if rel == ConfigName {
		cfg, err := starterConfig(root)
		if err != nil {
			return false
		}
		want = cfg
	} else {
		for _, f := range files {
			if f.dest == rel {
				want, _ = templates.ReadFile(f.src)
			}
		}
	}
	if want == nil {
		return false
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	if info, err := os.Lstat(full); err != nil || !info.Mode().IsRegular() {
		return false
	}
	got, err := os.ReadFile(full)
	return err == nil && bytes.Equal(got, want)
}
