// Package agent finds AI agents in other terminal tabs of a project (tmux, Konsole) and sends them a fix request.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"donecheck/internal/verify"
)

const commandTimeout = 3 * time.Second

type Proc struct{ Comm, Cmdline, Cwd string }

type Env struct {
	Run    func(ctx context.Context, name string, args ...string) ([]byte, error)
	Getenv func(string) string
	Proc   func(pid int) (Proc, error)
}

func DefaultEnv() Env { return Env{Run: execRunner, Getenv: os.Getenv, Proc: readProc} }

var allowedPrograms = map[string]bool{"tmux": true, "busctl": true}

var errNotAllowed = errors.New("program not allowed")

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedPrograms[name] {
		return nil, fmt.Errorf("%w: %s", errNotAllowed, name)
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

func readProc(pid int) (Proc, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	comm, err := os.ReadFile(filepath.Join(dir, "comm"))
	if err != nil {
		return Proc{}, err
	}
	cmdline, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
	cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
	if err != nil {
		return Proc{}, err
	}
	return Proc{Comm: strings.TrimSpace(string(comm)), Cmdline: string(cmdline), Cwd: cwd}, nil
}

type Agent struct {
	Name    string
	Backend string // "tmux" or "konsole"
	Service string // Konsole's D-Bus service
	Target  string // tmux pane id (%3) or Konsole session path (/Sessions/5)
	Dir     string // the session's working directory
}

func (a Agent) Label() string {
	return fmt.Sprintf("%s (%s %s)", a.Name, a.Backend, strings.TrimPrefix(a.Target, "/Sessions/"))
}

var (
	paneRe    = regexp.MustCompile(`^%[0-9]+$`)
	sessionRe = regexp.MustCompile(`^/Sessions/[0-9]+$`)
	serviceRe = regexp.MustCompile(`^(?::[0-9]+\.[0-9]+|org\.kde\.konsole(?:-[0-9]+)?)$`)
	extraRe   = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)
	idRe      = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

	knownAgents  = []string{"codex", "gemini", "aider", "opencode", "goose", "cursor-agent", "amp"}
	interpreters = map[string]bool{"node": true, "nodejs": true, "bun": true, "deno": true, "python": true, "python3": true, "npx": true}
)

// $DONECHECK_AGENTS adds process names, for aliases and other agents.
func agentNames(env Env) []string {
	names := append([]string(nil), knownAgents...)
	for _, n := range strings.Split(env.Getenv("DONECHECK_AGENTS"), ",") {
		if n = strings.ToLower(strings.TrimSpace(n)); extraRe.MatchString(n) {
			names = append(names, n)
		}
	}
	return names
}

func matchAgent(names []string, comm, cmdline, title string) string {
	comm = strings.ToLower(comm)
	for _, n := range names {
		if comm == n {
			return n
		}
	}
	if interpreters[comm] {
		args := strings.Split(strings.TrimRight(cmdline, "\x00"), "\x00")
		if len(args) > 1 {
			for _, seg := range strings.FieldsFunc(strings.ToLower(args[1]), func(r rune) bool { return r == '/' || r == '\\' }) {
				for _, n := range names {
					if seg == n || strings.HasPrefix(seg, n+"-") {
						return n
					}
				}
			}
		}
	}
	title = strings.ToLower(title)
	for _, n := range names {
		if title != "" && strings.Contains(title, n) {
			return n
		}
	}
	return ""
}

func inside(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func Detect(ctx context.Context, env Env, root string) []Agent {
	var found []Agent
	if env.Getenv("TMUX") != "" {
		found = append(found, detectTmux(ctx, env, root)...)
	}
	if env.Getenv("KONSOLE_DBUS_SERVICE") != "" {
		found = append(found, detectKonsole(ctx, env, root)...)
	}
	return found
}

func detectTmux(ctx context.Context, env Env, root string) []Agent {
	out, err := env.Run(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_id}\t#{pane_current_command}\t#{pane_title}\t#{pane_current_path}")
	if err != nil {
		return nil
	}
	return parsePanes(string(out), root, agentNames(env))
}

func parsePanes(out, root string, names []string) []Agent {
	var found []Agent
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || !paneRe.MatchString(f[0]) || !inside(root, f[3]) {
			continue
		}
		if name := matchAgent(names, f[1], "", f[2]); name != "" {
			found = append(found, Agent{Name: name, Backend: "tmux", Target: f[0], Dir: f[3]})
		}
	}
	return found
}

var sessionLine = regexp.MustCompile(`(/Sessions/[0-9]+)\s*$`)

func detectKonsole(ctx context.Context, env Env, root string) []Agent {
	service := env.Getenv("KONSOLE_DBUS_SERVICE")
	if !serviceRe.MatchString(service) {
		return nil
	}
	own := env.Getenv("KONSOLE_DBUS_SESSION") // the tab DoneCheck itself runs in
	tree, err := env.Run(ctx, "busctl", "--user", "tree", service)
	if err != nil {
		return nil
	}
	var sessions []string
	for _, line := range strings.Split(string(tree), "\n") {
		if m := sessionLine.FindStringSubmatch(line); m != nil && m[1] != own {
			sessions = append(sessions, m[1])
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessionNumber(sessions[i]) < sessionNumber(sessions[j]) })

	names := agentNames(env)
	var found []Agent
	for _, s := range sessions {
		out, callErr := env.Run(ctx, "busctl", "--user", "call", service, s, "org.kde.konsole.Session", "foregroundProcessId")
		if callErr != nil {
			continue
		}
		pid, ok := parseBusInt(string(out))
		if !ok || pid <= 0 {
			continue
		}
		p, err := env.Proc(pid)
		if err != nil || !inside(root, p.Cwd) {
			continue
		}
		if name := matchAgent(names, p.Comm, p.Cmdline, ""); name != "" {
			found = append(found, Agent{Name: name, Backend: "konsole", Service: service, Target: s, Dir: p.Cwd})
		}
	}
	return found
}

func sessionNumber(path string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(path, "/Sessions/"))
	return n
}

func parseBusInt(out string) (int, bool) {
	f := strings.Fields(out)
	if len(f) != 2 || f[0] != "i" {
		return 0, false
	}
	n, err := strconv.Atoi(f[1])
	return n, err == nil
}

// Prompt carries only check IDs and statuses: command output and file contents are untrusted.
func Prompt(r verify.Report) (string, bool) {
	var items []string
	for i, c := range r.Checks {
		if !c.Mandatory || c.Status == verify.Verified || c.Type == verify.ManualReview {
			continue
		}
		id := c.ID
		if !idRe.MatchString(id) {
			id = fmt.Sprintf("check-%d", i+1)
		}
		items = append(items, fmt.Sprintf("%s (%s)", id, c.Status))
	}
	if len(items) == 0 {
		return "", false
	}
	return "DoneCheck reports that this project is not fully verified. Mandatory checks not passing: " +
		strings.Join(items, ", ") +
		". Run `donecheck check --allow-commands` to see the details, fix the underlying problems in the code, and re-run until it exits 0." +
		" Do not edit donecheck.json to weaken or remove checks, and do not pass --confirm for manual_review checks.", true
}

func Send(ctx context.Context, env Env, a Agent, prompt string) error {
	if strings.ContainsAny(prompt, "\r\n") {
		return errors.New("prompt must be a single line")
	}
	switch a.Backend {
	case "tmux":
		if !paneRe.MatchString(a.Target) {
			return fmt.Errorf("invalid tmux pane %q", a.Target)
		}
		if _, err := env.Run(ctx, "tmux", "send-keys", "-t", a.Target, "-l", prompt); err != nil {
			return err
		}
		_, err := env.Run(ctx, "tmux", "send-keys", "-t", a.Target, "Enter")
		return err
	case "konsole":
		if !serviceRe.MatchString(a.Service) || !sessionRe.MatchString(a.Target) {
			return fmt.Errorf("invalid Konsole session %q %q", a.Service, a.Target)
		}
		send := func(text string) error {
			_, err := env.Run(ctx, "busctl", "--user", "call", a.Service, a.Target, "org.kde.konsole.Session", "sendText", "s", text)
			return err
		}
		if err := send(prompt); err != nil {
			return err
		}
		return send("\r")
	}
	return fmt.Errorf("unsupported terminal %q", a.Backend)
}
