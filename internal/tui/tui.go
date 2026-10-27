// Package tui is an interactive view over verify results.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"donecheck/internal/agent"
	"donecheck/internal/verify"
)

const (
	minWidth  = 50
	minHeight = 17
	tickEvery = 120 * time.Millisecond
	pollEvery = time.Second
	maxInput  = 200
)

type focus int

const (
	focusTree focus = iota
	focusChecks
	focusDetail
	focusCommand
	focusCount
)

type resultMsg struct {
	run, idx int
	res      verify.Result
}
type tickMsg struct{ run int }
type pollMsg struct{}
type graphMsg struct{ g graph }
type agentMsg struct{ agents []agent.Agent }
type fixSentMsg struct{ err error }

type ignoreRequest struct {
	rel   string
	rules []string
}
type installDoneMsg struct {
	tool string
	out  string
	err  error
}

type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmFix
	confirmInstall
	confirmIgnore
	confirmAllow
)

type fpMsg struct {
	run int
	fp  uint64
}

type model struct {
	cfg  *verify.Config
	opts verify.Options

	results []verify.Result // latest result per check; valid where done[i] or prev[i]
	done    []bool          // check i has finished in the current run
	prev    []bool          // results[i] is from an earlier run
	cursor  int             // selected check

	tree       []treeRow
	expanded   map[string]bool
	treeCursor int
	pv         preview
	showFile   bool // detail pane shows the file preview instead of a check

	focus  focus
	scroll int // first visible line of the detail pane
	input  string
	msg    string
	msgErr bool

	running    bool
	runID      int
	checkStart time.Time
	frame      int
	ctx        context.Context
	cancel     context.CancelFunc
	parent     context.Context

	watch     bool
	baselined bool
	baseline  uint64

	graph    graph
	sys      agent.Env
	agents   []agent.Agent
	agentSel int
	confirm  confirmKind // waiting for y before acting on another process or the network
	missing  []verify.Missing
	ignoring ignoreRequest
	findIdx  int // index of the finding last jumped to with x
	progress chan progressEnv
	scan     scanView
	jumpFile string // file and line of that finding, so e opens the editor there
	jumpLine int
	install  func(ctx context.Context, argv []string, maxOutput int) (string, error)

	width, height int
}

func newModel(parent context.Context, cfg *verify.Config, opts verify.Options, watch bool, sys agent.Env) model {
	confirmed := make(map[string]bool, len(opts.Confirmed))
	for k, v := range opts.Confirmed {
		confirmed[k] = v
	}
	opts.Confirmed = confirmed
	m := model{cfg: cfg, opts: opts, parent: parent, watch: watch, sys: sys, install: verify.RunInstall, focus: focusChecks, expanded: map[string]bool{}, findIdx: -1, progress: make(chan progressEnv, 2048)}
	m.missing = verify.MissingTools(cfg)
	m = m.begin()
	return m.rebuildTree()
}

// Results from the previous run stay visible (dimmed) until each check reports again.
func (m model) begin() model {
	if m.cancel != nil {
		m.cancel()
	}
	n := len(m.cfg.Checks)
	prev := make([]bool, n)
	for i := range prev {
		prev[i] = m.done != nil && (m.done[i] || m.prev[i])
	}
	m.runID++
	m.ctx, m.cancel = context.WithCancel(m.parent)
	if m.results == nil {
		m.results = make([]verify.Result, n)
	}
	m.scan = scanView{}
	m.done, m.prev = make([]bool, n), prev
	m.running, m.baselined = true, false
	m.checkStart = time.Now()
	return m
}

func (m model) rebuildTree() model {
	sel := ""
	if m.treeCursor < len(m.tree) {
		sel = m.tree[m.treeCursor].rel
	}
	m.tree = buildTree(m.cfg.Root, m.expanded)
	for i, r := range m.tree {
		if r.rel == sel {
			m.treeCursor = i
			break
		}
	}
	m.treeCursor = max(0, min(m.treeCursor, len(m.tree)-1))
	return m.syncPreview()
}

func (m model) syncPreview() model {
	if len(m.tree) == 0 {
		m.pv = preview{note: "this directory is empty"}
		return m
	}
	r := m.tree[m.treeCursor]
	if r.rel != m.pv.rel {
		m.scroll = 0
	}
	m.pv = loadPreview(m.cfg.Root, r)
	return m
}

func (m model) start() tea.Cmd { return tea.Batch(m.checkCmd(0), m.tickCmd()) }

func (m model) checkCmd(idx int) tea.Cmd {
	ctx, run, c, cfg, opts := m.ctx, m.runID, m.cfg.Checks[idx], m.cfg, m.opts
	ch := m.progress
	opts.Progress = func(p verify.Progress) {
		select {
		case ch <- progressEnv{run, p}:
		default: // never slow a scan down for the display
		}
	}
	return func() tea.Msg {
		return resultMsg{run: run, idx: idx, res: verify.RunCheck(ctx, cfg, opts, c)}
	}
}

func (m model) tickCmd() tea.Cmd {
	run := m.runID
	return tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{run} })
}

func pollCmd() tea.Cmd {
	return tea.Tick(pollEvery, func(time.Time) tea.Msg { return pollMsg{} })
}

func (m model) fpCmd() tea.Cmd {
	run, root := m.runID, m.cfg.Root
	return func() tea.Msg { return fpMsg{run: run, fp: fingerprint(root)} }
}

func (m model) graphCmd() tea.Cmd {
	root := m.cfg.Root
	return func() tea.Msg { return graphMsg{g: buildGraph(root)} }
}

func (m model) agentCmd() tea.Cmd {
	if m.sys.Run == nil {
		return nil
	}
	ctx, sys, root := m.parent, m.sys, m.cfg.Root
	return func() tea.Msg { return agentMsg{agents: agent.Detect(ctx, sys, root)} }
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.start(), pollCmd(), m.graphCmd(), m.agentCmd(), m.progressCmd())
}

func (m model) fixPrompt() (string, bool) {
	if m.running {
		return "", false
	}
	return agent.Prompt(m.report())
}

func (m model) canFix() bool {
	_, ok := m.fixPrompt()
	return len(m.agents) > 0 && ok
}

func (m model) sendFix() tea.Cmd {
	prompt, ok := m.fixPrompt()
	if !ok || len(m.agents) == 0 {
		return nil
	}
	ctx, sys, a := m.parent, m.sys, m.agents[m.agentSel]
	return func() tea.Msg { return fixSentMsg{err: agent.Send(ctx, sys, a, prompt)} }
}

func (m model) report() verify.Report {
	results := make([]verify.Result, 0, len(m.results))
	for i := range m.results {
		r := m.results[i]
		if !m.done[i] {
			c := m.cfg.Checks[i]
			r = verify.Result{ID: c.ID, Type: c.Type, Mandatory: !c.Optional, Status: verify.Skipped, Reason: "did not run"}
		}
		results = append(results, r)
	}
	return verify.NewReport(m.cfg, results)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case resultMsg:
		if msg.run != m.runID {
			return m, nil
		}
		m.results[msg.idx], m.done[msg.idx] = msg.res, true
		if m.scan.check == m.cfg.Checks[msg.idx].ID {
			m.scan = scanView{}
		}
		m.checkStart = time.Now()
		if next := msg.idx + 1; next < len(m.cfg.Checks) {
			return m, m.checkCmd(next)
		}
		m.running = false
		m.missing = verify.MissingTools(m.cfg)
		// Baseline after the run so files the checks write do not trigger another run.
		return m.rebuildTree(), tea.Batch(m.fpCmd(), m.graphCmd())
	case tickMsg:
		if msg.run != m.runID || !m.running {
			return m, nil
		}
		m.frame++
		return m, m.tickCmd()
	case pollMsg:
		if m.watch && !m.running {
			return m, tea.Batch(pollCmd(), m.fpCmd(), m.agentCmd())
		}
		return m, tea.Batch(pollCmd(), m.agentCmd())
	case progressMsg:
		m = m.applyProgress(msg.batch)
		return m, m.progressCmd()
	case graphMsg:
		m.graph = msg.g
	case agentMsg:
		m = m.setAgents(msg.agents)
	case editorDoneMsg:
		if msg.err != nil {
			m.msg, m.msgErr = "editor exited with an error: "+msg.err.Error(), true
		} else {
			m.msg, m.msgErr = "edited "+msg.file+"; re-running checks", false
		}
		if !m.running {
			m = m.begin()
			return m.rebuildTree(), m.start()
		}
	case installDoneMsg:
		m.missing = verify.MissingTools(m.cfg)
		if msg.err != nil {
			m.msg, m.msgErr = "install failed: "+lastLine(msg.out, msg.err.Error()), true
			return m, nil
		}
		m.msg, m.msgErr = "installed "+msg.tool+"; re-running checks", false
		if !m.running {
			m = m.begin()
			return m, m.start()
		}
	case fixSentMsg:
		if msg.err != nil {
			m.msg, m.msgErr = "could not send to the agent: "+msg.err.Error(), true
		} else {
			m.msg, m.msgErr = "fix request sent to "+m.agents[m.agentSel].Label()+"; watching for changes", false
		}
	case fpMsg:
		if msg.run != m.runID || m.running {
			return m, nil
		}
		if !m.baselined {
			m.baseline, m.baselined = msg.fp, true
		} else if m.watch && msg.fp != m.baseline {
			m = m.begin()
			m.msg, m.msgErr = "files changed: re-running checks", false
			return m, m.start()
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	}
	return m, nil
}

func (m model) quitOrCancel() (tea.Model, tea.Cmd) {
	if m.running {
		m.cancel() // remaining checks come back SKIPPED
		m.msg, m.msgErr = "run cancelled", false
		return m, nil
	}
	return m, tea.Quit
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm != confirmNone {
		kind := m.confirm
		m.confirm = confirmNone
		switch {
		case msg.String() != "y":
			m.msg, m.msgErr = "cancelled", false
		case kind == confirmFix && m.canFix():
			m.msg, m.msgErr = "sending fix request...", false
			return m, m.sendFix()
		case kind == confirmAllow:
			m.opts.AllowCommands = true
			m.msg, m.msgErr = "commands allowed for this session; re-running checks", false
			m = m.begin()
			return m, m.start()
		case kind == confirmIgnore:
			if err := verify.AppendIgnore(m.cfg.Root, m.ignoring.rel, m.ignoring.rules...); err != nil {
				return m.fail("could not update %s: %v", verify.IgnoreFileName, err)
			}
			m.msg, m.msgErr = "added to "+verify.IgnoreFileName+": "+m.ignoring.rel+"; re-running checks", false
			m = m.begin()
			return m, m.start()
		case kind == confirmInstall && len(m.missing) > 0:
			m.msg, m.msgErr = "installing "+m.missing[0].Binary+" (this can take a minute)...", false
			return m, m.installCmd()
		}
		return m, nil
	}
	if m.focus == focusCommand {
		return m.handleInput(msg)
	}
	switch msg.String() {
	case "F":
		return m.askFix()
	case "I":
		return m.askInstall()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(msg.String()[0] - '1'); i < len(m.agents) {
			m.agentSel = i
		}
	case "A":
		return m.askAllow()
	case "x":
		return m.jumpToFinding(1)
	case "X":
		return m.jumpToFinding(-1)
	case "e":
		return m.editFile()
	case "i":
		return m.askIgnore()
	case "ctrl+c", "q", "esc":
		return m.quitOrCancel()
	case "tab":
		m.focus = (m.focus + 1) % focusCount
	case "shift+tab":
		m.focus = (m.focus + focusCount - 1) % focusCount
	case ":", "/":
		m.focus = focusCommand
	case "w":
		m.watch = !m.watch
	case "r":
		if !m.running {
			m = m.begin()
			return m, m.start()
		}
	case "c":
		m = m.setConfirmed(m.cfg.Checks[m.cursor].ID, !m.opts.Confirmed[m.cfg.Checks[m.cursor].ID])
	default:
		switch m.focus {
		case focusTree:
			m = m.navTree(msg.String())
		case focusChecks:
			m = m.navChecks(msg.String())
		case focusDetail:
			m = m.navDetail(msg.String())
		}
	}
	return m, nil
}

func (m model) setConfirmed(id string, on bool) model {
	for i, c := range m.cfg.Checks {
		if c.ID != id || c.Type != verify.ManualReview || m.running {
			continue
		}
		m.opts.Confirmed[id] = on
		m.results[i] = verify.RunCheck(m.ctx, m.cfg, m.opts, c)
		m.done[i] = true
	}
	return m
}

func (m model) navChecks(key string) model {
	prev := m.cursor
	switch key {
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.cfg.Checks) - 1
	}
	m.cursor = max(0, min(m.cursor, len(m.cfg.Checks)-1))
	if m.cursor != prev || key == "enter" {
		m.scroll = 0
	}
	m.showFile = false
	return m
}

func (m model) navDetail(key string) model {
	switch key {
	case "up", "k":
		m.scroll = max(0, m.scroll-1)
	case "down", "j":
		m.scroll++
	case "pgup", "ctrl+u":
		m.scroll = max(0, m.scroll-8)
	case "pgdown", "ctrl+d":
		m.scroll += 8
	case "home", "g":
		m.scroll = 0
	}
	return m
}

func (m model) navTree(key string) model {
	if len(m.tree) == 0 {
		return m
	}
	m.showFile = true
	r := m.tree[m.treeCursor]
	switch key {
	case "up", "k":
		m.treeCursor--
	case "down", "j":
		m.treeCursor++
	case "home", "g":
		m.treeCursor = 0
	case "end", "G":
		m.treeCursor = len(m.tree) - 1
	case "pgup":
		m.treeCursor -= 8
	case "pgdown":
		m.treeCursor += 8
	case "enter", "right", "l":
		if r.dir && r.err == "" {
			if key == "enter" {
				m.expanded[r.rel] = !m.expanded[r.rel]
			} else {
				m.expanded[r.rel] = true
			}
			return m.rebuildTree()
		}
	case "left", "h":
		if r.dir && r.expanded {
			m.expanded[r.rel] = false
			return m.rebuildTree()
		}
		for i := m.treeCursor - 1; i >= 0; i-- {
			if m.tree[i].depth < r.depth {
				m.treeCursor = i
				break
			}
		}
	}
	m.treeCursor = max(0, min(m.treeCursor, len(m.tree)-1))
	return m.syncPreview()
}

func (m model) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.quitOrCancel()
	case tea.KeyEsc:
		m.focus = focusChecks
	case tea.KeyTab:
		m.focus = focusTree
	case tea.KeyShiftTab:
		m.focus = focusDetail
	case tea.KeyEnter:
		line := strings.TrimSpace(m.input)
		m.input = ""
		return m.runCommand(line)
	case tea.KeyBackspace:
		if r := []rune(m.input); len(r) > 0 {
			m.input = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		m.input = ""
	case tea.KeySpace:
		m.input += " "
	case tea.KeyRunes:
		for _, r := range msg.Runes {
			if !unicode.IsControl(r) && len([]rune(m.input)) < maxInput {
				m.input += string(r)
			}
		}
	}
	return m, nil
}

const helpText = "commands: check | fix | install | allow | next | edit | ignore <path> [rule] | cancel | watch [on|off] | confirm <id> | unconfirm <id> | clear | quit   (no shell: only DoneCheck commands)"

func (m model) askFix() (tea.Model, tea.Cmd) {
	switch {
	case len(m.agents) == 0:
		return m.fail("no AI agent found in this project: run codex, gemini, aider, ... in another Konsole tab or tmux pane in this folder (other names: set DONECHECK_AGENTS)")
	case !m.canFix():
		return m.fail("nothing for an agent to fix right now")
	}
	m.confirm = confirmFix
	m.msg, m.msgErr = fmt.Sprintf("Send the fix request to %s?  y = send, other = cancel", m.agents[m.agentSel].Label()), false
	return m, nil
}

func (m model) fail(format string, a ...any) (tea.Model, tea.Cmd) {
	m.msg, m.msgErr = fmt.Sprintf(format, a...), true
	return m, nil
}

func (m model) runCommand(line string) (tea.Model, tea.Cmd) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return m, nil
	}
	m.msg, m.msgErr = "", false
	switch f[0] {
	case "help", "?":
		m.msg = helpText
	case "clear":
	case "check", "run", "rerun":
		if m.running {
			return m.fail("a run is already in progress (use: cancel)")
		}
		m = m.begin()
		m.msg = "running checks"
		return m, m.start()
	case "cancel":
		if !m.running {
			return m.fail("nothing is running")
		}
		m.cancel()
		m.msg = "run cancelled"
	case "watch":
		switch {
		case len(f) == 1:
			m.watch = !m.watch
		case f[1] == "on":
			m.watch = true
		case f[1] == "off":
			m.watch = false
		default:
			return m.fail("usage: watch [on|off]")
		}
		m.msg = fmt.Sprintf("watch %s", map[bool]string{true: "on", false: "off"}[m.watch])
	case "confirm", "unconfirm":
		if len(f) != 2 {
			return m.fail("usage: %s <manual_review id>", f[0])
		}
		found := false
		for _, c := range m.cfg.Checks {
			found = found || (c.ID == f[1] && c.Type == verify.ManualReview)
		}
		switch {
		case !found:
			return m.fail("no manual_review check with id %q", f[1])
		case m.running:
			return m.fail("wait for the run to finish first")
		}
		m = m.setConfirmed(f[1], f[0] == "confirm")
		m.msg = map[string]string{"confirm": "confirmed ", "unconfirm": "unconfirmed "}[f[0]] + f[1]
	case "fix":
		return m.askFix()
	case "install":
		return m.askInstall()
	case "allow":
		return m.askAllow()
	case "ignore":
		if len(f) < 2 {
			return m.fail("usage: ignore <path> [rule ...]")
		}
		if err := verify.AppendIgnore(m.cfg.Root, f[1], f[2:]...); err != nil {
			return m.fail("%v", err)
		}
		m = m.begin()
		m.msg = "added to " + verify.IgnoreFileName + ": " + strings.Join(f[1:], " ")
		return m, m.start()
	case "edit":
		return m.editFile()
	case "next", "problem":
		return m.jumpToFinding(1)
	case "quit", "exit":
		return m, tea.Quit
	default:
		return m.fail("unknown command %q (try: help)", f[0])
	}
	return m, nil
}

func Run(ctx context.Context, cfg *verify.Config, opts verify.Options, watch bool) (verify.Report, error) {
	p := tea.NewProgram(newModel(ctx, cfg, opts, watch, agent.DefaultEnv()), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return verify.Report{}, fmt.Errorf("running TUI: %w", err)
	}
	m := final.(model)
	m.cancel()
	return m.report(), nil
}

func lastLine(out, fallback string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return fallback
}

func (m model) askInstall() (tea.Model, tea.Cmd) {
	if len(m.missing) == 0 {
		return m.fail("nothing to install")
	}
	if m.running {
		return m.fail("wait for the run to finish first")
	}
	m.confirm = confirmInstall
	m.msg, m.msgErr = "Install "+m.missing[0].Binary+" with: "+strings.Join(m.missing[0].Install, " ")+" ?  y = install, other = cancel", false
	return m, nil
}

func (m model) installCmd() tea.Cmd {
	ctx, install, mi, max := m.parent, m.install, m.missing[0], m.cfg.MaxOutputBytes
	return func() tea.Msg {
		out, err := install(ctx, mi.Install, max)
		return installDoneMsg{tool: mi.Binary, out: out, err: err}
	}
}

func (m model) needsCommands() bool {
	for i := range m.results {
		if (m.done[i] || m.prev[i]) && m.results[i].NeedsCommands {
			return true
		}
	}
	return false
}

func (m model) askAllow() (tea.Model, tea.Cmd) {
	switch {
	case m.opts.AllowCommands:
		return m.fail("commands are already allowed")
	case m.running:
		return m.fail("wait for the run to finish first")
	case !m.needsCommands():
		return m.fail("nothing needs commands right now")
	}
	m.confirm = confirmAllow
	m.msg, m.msgErr = "Allow DoneCheck to run commands from donecheck.json (tests, scanners), syntax checkers and git in this project?  y = allow, other = cancel", false
	return m, nil
}

func (m model) setAgents(found []agent.Agent) model {
	sel := 0
	if m.agentSel < len(m.agents) {
		prev := m.agents[m.agentSel]
		for i, a := range found {
			if a.Backend == prev.Backend && a.Service == prev.Service && a.Target == prev.Target {
				sel = i
			}
		}
	}
	m.agents, m.agentSel = found, sel
	if len(found) > 0 && strings.HasPrefix(m.msg, "no AI agent") {
		m.msg, m.msgErr = "", false
	}
	if m.confirm == confirmFix && !m.canFix() {
		m.confirm = confirmNone
	}
	return m
}
