package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Status string

const (
	Verified     Status = "VERIFIED"
	Failed       Status = "FAILED"
	Inconclusive Status = "INCONCLUSIVE"
	Skipped      Status = "SKIPPED"
)

const (
	maxSearchFileBytes = 10 << 20
	maxSnippetBytes    = 200
	waitDelay          = 2 * time.Second
)

type Result struct {
	ID          string    `json:"id"`
	Type        CheckType `json:"type"`
	Description string    `json:"description,omitempty"`
	Mandatory   bool      `json:"mandatory"`
	Status      Status    `json:"status"`
	Reason      string    `json:"reason"`
	Evidence    string    `json:"evidence,omitempty"`
	// Files are the project files this result concerns, relative to the root.
	Files []string `json:"files,omitempty"`
	// Findings are the problems a code_scan located.
	Findings []Finding `json:"findings,omitempty"`
	// Scanned is every file a code_scan read; for the TUI only.
	Scanned []string `json:"-"`
	// Ignored counts findings hidden by the ignore file.
	Ignored int `json:"ignored_findings,omitempty"`
	// Unchecked are scanned files whose syntax could not be verified.
	Unchecked []string `json:"unchecked_files,omitempty"`
	// NeedsCommands is true when --allow-commands would add evidence.
	NeedsCommands bool   `json:"needs_commands,omitempty"`
	Error         string `json:"error,omitempty"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Truncated     bool   `json:"output_truncated,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
}

type Options struct {
	AllowCommands bool
	Confirmed     map[string]bool
	// SkipReview, if set, leaves a file out of the automatic review list; explicit paths are never filtered.
	SkipReview func(rel string) bool
	// Progress receives updates from long checks; it must be safe for concurrent use and not block.
	Progress func(Progress)
}

// RunCheck never returns VERIFIED without evidence gathered now (or an explicit confirmation).
func RunCheck(ctx context.Context, cfg *Config, opts Options, c Check) Result {
	res := Result{ID: c.ID, Type: c.Type, Description: c.Description, Mandatory: !c.Optional}
	start := time.Now()

	switch {
	case ctx.Err() != nil:
		res.Status, res.Reason = Skipped, "run cancelled before this check started"
	case c.Type == FileExists:
		checkFileExists(cfg, c, &res)
	case c.Type == TextContains:
		checkTextContains(cfg, c, &res)
	case c.Type == CommandExitCode || c.Type == TestSuite || c.Type == DependencyAudit:
		checkCommand(ctx, cfg, opts, c, &res)
	case c.Type == CodeScan:
		checkCodeScan(ctx, cfg, opts, c, &res)
	case c.Type == ManualReview:
		checkManual(ctx, cfg, opts, c, &res)
	default:
		res.Status, res.Reason, res.Error = Inconclusive, "unsupported check type", fmt.Sprintf("unknown type %q", c.Type)
	}

	res.DurationMS = time.Since(start).Milliseconds()
	res.Reason = clean(res.Reason)
	res.Evidence = clean(res.Evidence)
	res.Error = clean(res.Error)
	return res
}

func inconclusive(res *Result, reason string, err error) {
	res.Status, res.Reason, res.Error = Inconclusive, reason, err.Error()
}

func checkFileExists(cfg *Config, c Check, res *Result) {
	p, err := resolveInRoot(cfg.Root, c.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		res.Status, res.Reason = Failed, fmt.Sprintf("%s does not exist", c.Path)
		return
	case err != nil:
		inconclusive(res, "could not safely check path", err)
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		inconclusive(res, "could not stat path", err)
		return
	}
	kind := "file"
	if info.IsDir() {
		kind = "directory"
	}
	res.Status, res.Reason = Verified, fmt.Sprintf("%s exists", c.Path)
	res.Evidence = fmt.Sprintf("%s, %d bytes, modified %s", kind, info.Size(), info.ModTime().UTC().Format(time.RFC3339))
}

func checkTextContains(cfg *Config, c Check, res *Result) {
	p, err := resolveInRoot(cfg.Root, c.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		res.Status, res.Reason = Failed, fmt.Sprintf("%s does not exist", c.Path)
		return
	case err != nil:
		inconclusive(res, "could not safely check path", err)
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		inconclusive(res, "could not stat file", err)
		return
	}
	if !info.Mode().IsRegular() {
		inconclusive(res, "path is not a regular file", fmt.Errorf("%s has mode %s", c.Path, info.Mode()))
		return
	}
	if info.Size() > maxSearchFileBytes {
		inconclusive(res, "file too large to search", fmt.Errorf("%s is %d bytes; limit is %d", c.Path, info.Size(), maxSearchFileBytes))
		return
	}
	f, err := os.Open(p)
	if err != nil {
		inconclusive(res, "could not open file", err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSearchFileBytes+1))
	if err != nil {
		inconclusive(res, "could not read file", err)
		return
	}
	if len(data) > maxSearchFileBytes {
		inconclusive(res, "file too large to search", fmt.Errorf("%s grew beyond %d bytes while reading", c.Path, maxSearchFileBytes))
		return
	}

	idx := bytes.Index(data, []byte(c.Text))
	if idx < 0 {
		res.Status, res.Reason = Failed, fmt.Sprintf("text not found in %s", c.Path)
		res.Evidence = fmt.Sprintf("searched %d bytes", len(data))
		return
	}
	lineNo := bytes.Count(data[:idx], []byte("\n")) + 1
	lineStart := bytes.LastIndexByte(data[:idx], '\n') + 1
	lineEnd := len(data)
	if n := bytes.IndexByte(data[idx:], '\n'); n >= 0 {
		lineEnd = idx + n
	}
	res.Status, res.Reason = Verified, fmt.Sprintf("text found in %s at line %d", c.Path, lineNo)
	res.Evidence = fmt.Sprintf("line %d: %s", lineNo, truncateBytes(string(data[lineStart:lineEnd]), maxSnippetBytes))
}

func checkManual(ctx context.Context, cfg *Config, opts Options, c Check, res *Result) {
	var items []reviewItem
	note := ""
	if len(c.Paths) > 0 {
		for _, p := range c.Paths {
			items = append(items, reviewItem{p, "listed in this check's paths"})
		}
	} else {
		items, note = reviewCandidates(ctx, cfg, opts)
	}
	for _, it := range items {
		res.Files = append(res.Files, it.path)
	}
	what := strings.Join(strings.Fields(c.Description), " ")
	list := reviewList(items, note)

	if opts.Confirmed[c.ID] {
		res.Status, res.Reason = Verified, "confirmed by a human reviewer: "+truncateBytes(what, 120)
		res.Evidence = "confirmation supplied by the operator for this run; DoneCheck did not verify the claim itself\nconfirmed: " + what
		if list != "" {
			res.Evidence += "\n" + list
		}
		return
	}
	res.NeedsCommands = len(c.Paths) == 0 && !opts.AllowCommands
	res.Status, res.Reason = Inconclusive, "awaiting human confirmation: "+truncateBytes(what, 120)
	if len(items) > 0 {
		res.Reason += fmt.Sprintf(" (%d file(s) to review)", len(items))
	}
	res.Evidence = "why a human is needed: automated checks cannot judge this, so DoneCheck leaves it unverified until a person confirms it\nwhat to confirm: " + what
	if list != "" {
		res.Evidence += "\n" + list
	}
}

// If problem is set the command did not complete and exit is nil.
type execution struct {
	output    string
	truncated bool
	exit      *int
	problem   string
	err       error
}

func execute(ctx context.Context, cfg *Config, argv []string, timeout time.Duration) execution {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out := &tailBuffer{max: cfg.MaxOutputBytes}
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = cfg.Root
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = waitDelay
	runErr := cmd.Run()

	ex := execution{output: out.String(), truncated: out.truncated()}
	var exitErr *exec.ExitError
	isExit := errors.As(runErr, &exitErr)
	switch {
	case runErr != nil && cctx.Err() != nil && (!isExit || exitErr.ExitCode() == -1):
		if ctx.Err() != nil {
			ex.problem, ex.err = "command cancelled", ctx.Err()
		} else {
			ex.problem, ex.err = fmt.Sprintf("command timed out after %s", timeout), cctx.Err()
		}
	case isExit && exitErr.ExitCode() >= 0:
		code := exitErr.ExitCode()
		ex.exit = &code
	case isExit:
		ex.problem, ex.err = "command was terminated by a signal", runErr
	case errors.Is(runErr, exec.ErrWaitDelay):
		ex.problem, ex.err = "command exited but a child process kept its output open", runErr
	case runErr != nil:
		ex.problem, ex.err = "command could not be run", runErr
	default:
		zero := 0
		ex.exit = &zero
	}
	return ex
}

func checkCommand(ctx context.Context, cfg *Config, opts Options, c Check, res *Result) {
	if !opts.AllowCommands {
		res.Status, res.Reason = Skipped, "command execution is not enabled (pass --allow-commands)"
		res.NeedsCommands = true
		return
	}
	timeout := time.Duration(cfg.DefaultTimeoutSeconds) * time.Second
	if c.TimeoutSeconds > 0 {
		timeout = time.Duration(c.TimeoutSeconds) * time.Second
	}
	argv := c.Command
	if c.Type == DependencyAudit && len(argv) == 0 {
		argv = append([]string(nil), auditTools[c.Tool].argv...)
		if p, ok := resolveTool(argv[0]); ok {
			argv[0] = p
		}
	}

	ex := execute(ctx, cfg, argv, timeout)
	res.Evidence, res.Truncated, res.ExitCode = ex.output, ex.truncated, ex.exit
	if ex.truncated {
		res.Evidence = fmt.Sprintf("[output truncated: showing last %d bytes]\n%s", cfg.MaxOutputBytes, ex.output)
	}
	if ex.problem != "" {
		inconclusive(res, ex.problem, ex.err)
		if c.Type == DependencyAudit && errors.Is(ex.err, exec.ErrNotFound) {
			res.Reason = fmt.Sprintf("%s is not installed", argv[0])
			if auditTools[c.Tool].install != nil && len(c.Command) == 0 {
				res.Error += "; run `donecheck install` to install it"
			}
		}
		return
	}
	if c.Type == DependencyAudit {
		classifyAudit(c, ex, res)
		return
	}

	expect := 0
	if c.Type == CommandExitCode {
		expect = c.ExpectExit
	}
	if *ex.exit != expect {
		if why := notApplicable(ex); why != "" {
			// Nothing to run: a misconfigured check, not a failing test.
			inconclusive(res, "this check does not apply to the project", errors.New(why))
			return
		}
		res.Status = Failed
		res.Reason = fmt.Sprintf("exit code %d, expected %d", *ex.exit, expect)
		return
	}
	if c.OutputContains != "" && !strings.Contains(ex.output, c.OutputContains) {
		if ex.truncated {
			res.Status = Inconclusive
			res.Reason = "expected output not seen, but output was truncated"
			res.Error = fmt.Sprintf("%q may have been in the discarded part of the output", c.OutputContains)
			return
		}
		res.Status = Failed
		res.Reason = fmt.Sprintf("exit code %d as expected, but output did not contain %q", expect, c.OutputContains)
		return
	}
	res.Status = Verified
	res.Reason = fmt.Sprintf("exit code %d as expected", expect)
}

var (
	npmMissingScript = regexp.MustCompile(`(?i)\bnpm (?:error|err!) missing script: `)
	pytestNoTests    = regexp.MustCompile(`(?i)no tests ran|collected 0 items`)
)

func notApplicable(ex execution) string {
	switch {
	case npmMissingScript.MatchString(ex.output):
		return "package.json defines no such script, so there is nothing to run: remove this check or add the script"
	case *ex.exit == 5 && pytestNoTests.MatchString(ex.output):
		return "pytest found no tests, so there is nothing to run: remove this check or add tests"
	}
	return ""
}

// tailBuffer keeps only the last max bytes written to it.
type tailBuffer struct {
	max     int
	buf     []byte
	dropped bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.trim()
	}
	return len(p), nil
}

func (t *tailBuffer) trim() {
	if len(t.buf) > t.max {
		n := copy(t.buf, t.buf[len(t.buf)-t.max:])
		t.buf = t.buf[:n]
		t.dropped = true
	}
}

func (t *tailBuffer) String() string  { t.trim(); return string(t.buf) }
func (t *tailBuffer) truncated() bool { t.trim(); return t.dropped }

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s + "…"
}
