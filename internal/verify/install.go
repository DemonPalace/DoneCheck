package verify

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const installTimeout = 5 * time.Minute

type Missing struct {
	CheckID string
	Tool    string
	Binary  string
	Install []string
}

// `go install` puts binaries in directories that are often not on PATH.
func goBinDirs() []string {
	var dirs []string
	if d := os.Getenv("GOBIN"); d != "" {
		dirs = append(dirs, d)
	}
	for _, p := range filepath.SplitList(os.Getenv("GOPATH")) {
		dirs = append(dirs, filepath.Join(p, "bin"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	return dirs
}

func resolveTool(name string) (string, bool) {
	if strings.ContainsRune(name, filepath.Separator) {
		return name, true // a path; the command itself will report if it is wrong
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, d := range goBinDirs() {
		if p, err := exec.LookPath(filepath.Join(d, name)); err == nil {
			return p, true
		}
	}
	return "", false
}

// Only scanners DoneCheck knows how to install are reported.
func MissingTools(cfg *Config) []Missing {
	var out []Missing
	seen := map[string]bool{}
	for _, c := range cfg.Checks {
		if c.Type != DependencyAudit || len(c.Command) > 0 || auditTools[c.Tool].install == nil {
			continue
		}
		binary := auditTools[c.Tool].argv[0]
		if _, found := resolveTool(binary); found || seen[binary] {
			continue
		}
		seen[binary] = true
		out = append(out, Missing{CheckID: c.ID, Tool: c.Tool, Binary: binary, Install: auditTools[c.Tool].install})
	}
	return out
}

// Runs in an empty temp dir so the project's go.mod cannot influence the install.
func RunInstall(ctx context.Context, argv []string, maxOutput int) (string, error) {
	if len(argv) == 0 {
		return "", fmt.Errorf("no install command")
	}
	dir, err := os.MkdirTemp("", "donecheck-install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	out := &tailBuffer{max: maxOutput}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = waitDelay
	err = cmd.Run()
	text := clean(out.String())
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%v)", err, ctx.Err())
		}
		return text, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return text, nil
}
