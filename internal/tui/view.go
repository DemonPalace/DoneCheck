package tui

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"donecheck/internal/verify"
)

const (
	spinnerFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"
	cmdBoxHeight  = 4
)

var (
	bold     = lipgloss.NewStyle().Bold(true)
	dim      = lipgloss.NewStyle().Faint(true)
	focusBox = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	colors   = map[verify.Status]lipgloss.Style{
		verify.Verified:     lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		verify.Failed:       lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true),
		verify.Inconclusive: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		verify.Skipped:      lipgloss.NewStyle().Faint(true),
	}
	badgeColors = map[verify.Status]string{verify.Verified: "2", verify.Failed: "1", verify.Inconclusive: "3", verify.Skipped: "8"}
	icons       = map[verify.Status]string{verify.Verified: "✓", verify.Failed: "✗", verify.Inconclusive: "?", verify.Skipped: "–"}
)

func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

func box(title string, w, h int, focused bool, lines []string) string {
	bc, tc := dim, bold
	if focused {
		bc, tc = focusBox, focusBox
	}
	inner := w - 2
	t := ansi.Truncate(" "+title+" ", inner, "")
	var b strings.Builder
	b.WriteString(bc.Render("┌") + tc.Render(t) + bc.Render(strings.Repeat("─", inner-lipgloss.Width(t))+"┐") + "\n")
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString(bc.Render("│") + " " + fit(line, inner-2) + " " + bc.Render("│") + "\n")
	}
	b.WriteString(bc.Render("└" + strings.Repeat("─", inner) + "┘"))
	return b.String()
}

// layout is shared by drawing and mouse hit-testing.
type layout struct {
	w, h, topH, treeW, detailY, detailH, cmdY int
}

const (
	agentsY = 1
	bannerY = 2
	topY    = 3
)

func (m model) layout() layout {
	rest := m.height - topY - cmdBoxHeight - 1
	topH := max(5, rest*45/100)
	return layout{
		w: m.width, h: m.height, topH: topH, treeW: max(22, m.width*35/100),
		detailY: topY + topH, detailH: rest - topH, cmdY: topY + rest,
	}
}

func window(cursor, n, h int) int {
	if n <= h {
		return 0
	}
	return max(0, min(cursor-h/2, n-h))
}

func (m model) View() string {
	if m.width < minWidth || m.height < minHeight {
		return fmt.Sprintf("Terminal too small: need at least %dx%d, have %dx%d.\nResize the window, or press q to quit.\n",
			minWidth, minHeight, m.width, m.height)
	}
	l := m.layout()
	w := l.w
	top := lipgloss.JoinHorizontal(lipgloss.Top,
		m.treeBox(l.treeW, l.topH),
		m.checksBox(w-l.treeW, l.topH),
	)
	agentLine, _ := m.agentBar(w)
	parts := []string{m.header(w), agentLine, m.banner(w), top, m.detailBox(w, l.detailH), m.commandBox(w), m.footer(w)}
	return strings.Join(parts, "\n")
}

func (m model) header(w int) string {
	left := bold.Render("DoneCheck") + "  " + dim.Render(m.cfg.Root)
	right := dim.Render("○ watch off")
	if m.watch {
		right = colors[verify.Verified].Render("● watching for changes")
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fit(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func badge(text, color string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color(color)).Padding(0, 1).Render(text)
}

type buttonKind int

const (
	buttonNone buttonKind = iota
	buttonInstall
	buttonAllow
	buttonFix
)

func (m model) buttonKind() buttonKind {
	switch {
	case !m.running && len(m.missing) > 0:
		return buttonInstall
	case !m.running && !m.opts.AllowCommands && m.needsCommands():
		return buttonAllow
	case m.canFix():
		return buttonFix
	}
	return buttonNone
}

func (m model) button() string {
	switch m.buttonKind() {
	case buttonInstall:
		return badge("I  Install "+m.missing[0].Binary, "6")
	case buttonAllow:
		return badge("A  Allow commands", "3")
	case buttonFix:
		return badge("F  Fix all with "+m.agents[m.agentSel].Name, "5")
	}
	return ""
}

func (m model) agentBar(w int) (string, [][2]int) {
	label := bold.Render("Agents") + " "
	if len(m.agents) == 0 {
		hint := "none found in this project: open codex, gemini, aider, ... in another Konsole tab or tmux pane in this folder"
		return fit(label+dim.Render(hint), w), nil
	}
	line, x := label, lipgloss.Width(label)
	var spans [][2]int
	for i, a := range m.agents {
		dir, _ := filepath.Rel(m.cfg.Root, a.Dir)
		if dir == "." {
			dir = "this folder"
		}
		text := fmt.Sprintf(" %d %s · %s ", i+1, a.Label(), dir)
		chip := dim.Render(text)
		if i == m.agentSel {
			chip = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("5")).Render(text)
		}
		spans = append(spans, [2]int{x, x + lipgloss.Width(chip)})
		line += chip + " "
		x += lipgloss.Width(chip) + 1
	}
	return fit(line, w), spans
}

func (m model) banner(w int) string {
	left, short := m.bannerLeft()
	btn := m.button()
	if btn == "" {
		return fit(left, w)
	}
	for _, l := range []string{left, short} {
		if gap := w - lipgloss.Width(l) - lipgloss.Width(btn); gap >= 1 {
			return l + strings.Repeat(" ", gap) + btn
		}
	}
	return fit(btn, w)
}

func (m model) bannerLeft() (full, short string) {
	finished := 0
	for _, d := range m.done {
		if d {
			finished++
		}
	}
	total := len(m.done)
	if m.running {
		barW := 16
		filled := barW * finished / max(1, total)
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barW-filled)
		full = badge("CHECKING", "4") + " " + bar + fmt.Sprintf(" %d/%d", finished, total)
		if m.scan.active {
			full += "  " + dim.Render(m.scan.label())
		}
		return full, badge("CHECKING", "4") + " " + bar + fmt.Sprintf(" %d/%d", finished, total)
	}
	r := m.report()
	counts := fmt.Sprintf("  %d verified · %d failed · %d inconclusive · %d skipped",
		r.Summary.Verified, r.Summary.Failed, r.Summary.Inconclusive, r.Summary.Skipped)
	switch {
	case r.Summary.FullyVerified:
		short = badge("FULLY VERIFIED", "2")
	case r.Summary.MandatoryFailed > 0:
		short = badge("NOT VERIFIED", "1")
	default:
		short = badge("NOT FULLY VERIFIED", "3")
	}
	return short + counts, short
}

func (m model) treeBox(w, h int) string {
	ih, cw := h-2, w-4
	status := m.pathStatus()
	var scanMarks map[string]markState
	if m.scan.active {
		scanMarks = m.scan.marks()
	}
	top := window(m.treeCursor, len(m.tree), ih)
	var lines []string
	for i := top; i < len(m.tree) && i < top+ih; i++ {
		r := m.tree[i]
		glyph := "  "
		name := r.name
		switch {
		case r.link:
			glyph = "↪ "
		case r.dir && r.expanded:
			glyph, name = "▾ ", name+"/"
		case r.dir:
			glyph, name = "▸ ", name+"/"
		}
		left := strings.Repeat("  ", r.depth) + glyph + name
		st := status[r.rel]
		icon := " "
		if st != "" {
			icon = icons[st]
		}
		style := lipgloss.NewStyle()
		if st != "" {
			style = colors[st]
		}
		if mark, scanning := scanMarks[r.rel]; scanning {
			style, icon = m.markLook(mark)
		}
		row := fit(left, cw-2) + " " + icon
		switch {
		case i == m.treeCursor && m.focus == focusTree:
			lines = append(lines, style.Reverse(true).Bold(true).Render(row))
		case i == m.treeCursor:
			lines = append(lines, style.Bold(true).Render(row))
		default:
			lines = append(lines, style.Render(row))
		}
	}
	if len(m.tree) == 0 {
		lines = []string{dim.Render("(empty)")}
	}
	return box("Files", w, h, m.focus == focusTree, lines)
}

func (m model) markLook(s markState) (lipgloss.Style, string) {
	switch s {
	case markDone:
		return colors[verify.Verified], "✓"
	case markCurrent:
		frames := []rune(spinnerFrames)
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")), string(frames[m.frame%len(frames)])
	}
	return dim, "·"
}

func (m model) checkParts(i int) (icon, word string, st lipgloss.Style, dur string) {
	switch {
	case m.done[i]:
		r := m.results[i]
		return icons[r.Status], string(r.Status), colors[r.Status], fmtDur(r.DurationMS)
	case m.running && i == firstUnfinished(m.done):
		spin := []rune(spinnerFrames)[m.frame%len([]rune(spinnerFrames))]
		return string(spin), "RUNNING", bold, fmt.Sprintf("%.1fs", time.Since(m.checkStart).Seconds())
	case m.prev[i]:
		r := m.results[i]
		return icons[r.Status], string(r.Status), dim, ""
	}
	return "·", "PENDING", dim, ""
}

func firstUnfinished(done []bool) int {
	for i, d := range done {
		if !d {
			return i
		}
	}
	return -1
}

func fmtDur(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func (m model) checksBox(w, h int) string {
	ih, cw := h-2, w-4
	top := window(m.cursor, len(m.cfg.Checks), ih)
	var lines []string
	for i := top; i < len(m.cfg.Checks) && i < top+ih; i++ {
		c := m.cfg.Checks[i]
		icon, word, st, dur := m.checkParts(i)
		id := c.ID
		if c.Optional {
			id += " (optional)"
		}
		if m.scan.active && m.scan.check == c.ID && !m.done[i] {
			id += "  " + m.scan.label()
		}
		plainLeft := fmt.Sprintf("%s %-12s %s", icon, word, id)
		row := fit(plainLeft, cw-len(dur)-1) + " " + dur
		switch {
		case i == m.cursor && m.focus == focusChecks:
			lines = append(lines, st.Reverse(true).Bold(true).Render(row))
		case i == m.cursor:
			lines = append(lines, st.Bold(true).Render(row))
		default:
			lines = append(lines, st.Render(row))
		}
	}
	return box("Checks", w, h, m.focus == focusChecks, lines)
}

func (m model) detailBox(w, h int) string {
	ih := h - 2
	ds := m.detailWindow(w-4, ih)
	title := ds.title
	if ds.total > ih {
		title += fmt.Sprintf("  (%d-%d of %d)", ds.start+1, ds.start+len(ds.lines), ds.total)
	}
	return box(title, w, h, m.focus == focusDetail, ds.lines)
}

// targets[i], when non-zero, is the file line that clicking lines[i] jumps to.
type detailSlice struct {
	title   string
	lines   []string
	targets []int
	start   int
	total   int
}

// detailWindow builds only the rows in view so large files stay cheap to draw.
func (m model) detailWindow(cw, ih int) detailSlice {
	if !m.showFile {
		title, all := m.checkDetail(cw)
		start := min(m.scroll, max(0, len(all)-ih))
		end := min(len(all), start+ih)
		return detailSlice{title: title, lines: all[start:end], start: start, total: len(all)}
	}

	head, headTargets := m.buildConnections()
	if m.pv.note != "" {
		head, headTargets = append(head, dim.Render(m.pv.note)), append(headTargets, 0)
	}
	total := len(head) + len(m.pv.lines)
	start := min(m.scroll, max(0, total-ih))
	end := min(total, start+ih)
	ds := detailSlice{title: "Preview: " + m.pv.rel, start: start, total: total}

	if start < len(head) {
		hi := min(end, len(head))
		ds.lines, ds.targets = head[start:hi], headTargets[start:hi]
	}
	from := max(start, len(head)) - len(head)
	to := end - len(head)
	if to > from {
		m.pv.ensureChunks(from, to)
		flagged := m.flaggedLines(m.pv.rel)
		red := colors[verify.Failed]
		for i := from; i < to; i++ {
			if flagged[i+1] {
				ds.lines = append(ds.lines, red.Render(fmt.Sprintf("%4d ▌ ", i+1)+m.pv.plainLine(i)))
			} else {
				ds.lines = append(ds.lines, dim.Render(fmt.Sprintf("%4d │ ", i+1))+m.pv.line(i))
			}
			ds.targets = append(ds.targets, 0)
		}
	}
	return ds
}

func (m model) checkDetail(cw int) (string, []string) {
	c := m.cfg.Checks[m.cursor]
	title := "Detail: " + c.ID
	if !m.done[m.cursor] && !m.prev[m.cursor] {
		return title, []string{dim.Render("Waiting for this check to run...")}
	}
	r := m.results[m.cursor]
	if !m.done[m.cursor] {
		title += " (previous run)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  [%s]\n%s\n", badge(string(r.Status), badgeColors[r.Status]), r.Type, r.Reason)
	if r.Description != "" {
		fmt.Fprintf(&b, "%s\n", r.Description)
	}
	if r.ExitCode != nil {
		fmt.Fprintf(&b, "exit code: %d\n", *r.ExitCode)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", r.Error)
	}
	if r.Evidence != "" {
		fmt.Fprintf(&b, "\nevidence:\n%s\n", r.Evidence)
	}
	wrapped := lipgloss.NewStyle().Width(cw).Render(strings.TrimRight(b.String(), "\n"))
	return title, strings.Split(wrapped, "\n")
}

func (m model) flaggedLines(rel string) map[int]bool {
	out := map[int]bool{}
	for i, c := range m.cfg.Checks {
		if c.Type != verify.CodeScan || !(m.done[i] || m.prev[i]) {
			continue
		}
		for _, f := range m.results[i].Findings {
			if f.Path == rel {
				for l := f.Line; l <= max(f.Line, f.EndLine); l++ {
					out[l] = true
				}
			}
		}
	}
	return out
}

func (m model) buildConnections() ([]string, []int) {
	if len(m.tree) == 0 || m.pv.rel == "" {
		return nil, nil
	}
	rel := m.pv.rel
	var lines []string
	var targets []int
	add := func(line string, target int) {
		lines, targets = append(lines, line), append(targets, target)
	}
	for i, c := range m.cfg.Checks {
		if !(m.done[i] || m.prev[i]) {
			continue
		}
		named := c.Path != "" && path.Clean(filepath.ToSlash(c.Path)) == rel
		for _, f := range m.results[i].Files {
			named = named || path.Clean(filepath.ToSlash(f)) == rel
		}
		if named {
			st := m.results[i].Status
			verb := "checked by"
			if c.Type == verify.ManualReview {
				verb = "needs human review for"
			}
			add(colors[st].Render(fmt.Sprintf("  %s %s %q (%s)", icons[st], verb, c.ID, st)), 0)
		}
	}
	for i, c := range m.cfg.Checks {
		if c.Type != verify.CodeScan || !(m.done[i] || m.prev[i]) {
			continue
		}
		shown := 0
		for _, f := range m.results[i].Findings {
			if f.Path != rel {
				continue
			}
			if shown++; shown > 6 {
				add(colors[verify.Failed].Render("  ✗ … more findings in this file (press x to step through all)"), 0)
				break
			}
			add(colors[verify.Failed].Render(fmt.Sprintf("  ✗ line %d  [%s] %s", f.Line, f.Rule, f.Message)), f.Line)
			for _, sn := range f.Snippet {
				add(dim.Render("      | "+sn), f.Line)
			}
		}
	}
	const maxLinks = 6
	for _, l := range []struct {
		arrow string
		names []string
	}{{"──▶", m.graph.out[rel]}, {"◀──", m.graph.in[rel]}} {
		for i, n := range l.names {
			if i == maxLinks {
				add(dim.Render(fmt.Sprintf("  … %d more", len(l.names)-maxLinks)), 0)
				break
			}
			what := "mentions"
			if l.arrow == "◀──" {
				what = "mentioned by"
			}
			add(fmt.Sprintf("  %s %s %s", l.arrow, n, dim.Render("("+what+")")), 0)
		}
	}
	if len(lines) == 0 {
		return nil, nil
	}
	return append(append([]string{bold.Render("Connections")}, lines...), ""), append(append([]int{0}, targets...), 0)
}

func (m model) commandBox(w int) string {
	msg := dim.Render("type 'help' for commands")
	switch {
	case m.msg != "" && m.msgErr:
		msg = errStyle.Render(m.msg)
	case m.msg != "":
		msg = m.msg
	}
	prompt := bold.Render("> ") + m.input
	switch {
	case m.focus == focusCommand:
		prompt += focusBox.Render("█")
	case m.input == "":
		prompt = bold.Render("> ") + dim.Render("press : or Tab to type a command")
	}
	return box("Command", w, cmdBoxHeight, m.focus == focusCommand, []string{msg, prompt})
}

func (m model) footer(w int) string {
	hints := "Tab pane · ↑↓ move · Enter expand · x next problem · e edit · i ignore · r rerun · w watch · : command · q quit"
	switch {
	case m.focus == focusCommand:
		hints = "Enter run · Esc leave · Tab switch pane · try: help"
	case m.running:
		hints = "q / Ctrl+C cancel run · Tab switch pane · ↑↓ move"
	case !m.running && len(m.missing) > 0:
		hints = "I install " + m.missing[0].Binary + " · " + hints
	case m.buttonKind() == buttonAllow:
		hints = "A allow commands · " + hints
	case m.canFix():
		hints = "F fix all with " + m.agents[m.agentSel].Name + " · " + hints
	}
	return dim.Render(fit(hints, w))
}
