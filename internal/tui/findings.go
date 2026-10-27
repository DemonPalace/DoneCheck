package tui

import (
	"fmt"
	"path"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"donecheck/internal/verify"
)

func (m model) allFindings() []verify.Finding {
	var all []verify.Finding
	for i, c := range m.cfg.Checks {
		if c.Type == verify.CodeScan && (m.done[i] || m.prev[i]) {
			all = append(all, m.results[i].Findings...)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})
	return all
}

func (m model) jumpToFinding(dir int) (tea.Model, tea.Cmd) {
	all := m.allFindings()
	if len(all) == 0 {
		return m.fail("no problems found by code_scan")
	}
	idx := ((m.findIdx+dir)%len(all) + len(all)) % len(all)
	f := all[idx]

	for p := path.Dir(f.Path); p != "." && p != "/"; p = path.Dir(p) {
		m.expanded[p] = true
	}
	m = m.rebuildTree()
	row := -1
	for i, r := range m.tree {
		if r.rel == f.Path {
			row = i
		}
	}
	if row < 0 {
		return m.fail("%s:%d is not shown in the file tree", f.Path, f.Line)
	}
	m.treeCursor, m.showFile, m.focus = row, true, focusDetail
	m = m.syncPreview()
	m.findIdx, m.jumpFile, m.jumpLine = idx, f.Path, f.Line
	m = m.scrollToLine(f.Line)
	m.msg, m.msgErr = fmt.Sprintf("problem %d/%d  %s:%d  [%s] %s   (x next, X previous, e edit, i ignore)", idx+1, len(all), f.Path, f.Line, f.Rule, f.Message), false
	return m, nil
}

func (m model) scrollToLine(line int) model {
	conn, _ := m.buildConnections()
	before := len(conn)
	if m.pv.note != "" {
		before++
	}
	l := m.layout()
	m.scroll = max(0, before+line-1-(l.detailH-2)/3)
	return m
}

func (m model) askIgnore() (tea.Model, tea.Cmd) {
	if !m.showFile || m.pv.rel == "" {
		return m.fail("select a file with problems in the Files box first")
	}
	seen := map[string]bool{}
	var rules []string
	for _, f := range m.allFindings() {
		if f.Path == m.pv.rel && !seen[f.Rule] {
			seen[f.Rule] = true
			rules = append(rules, f.Rule)
		}
	}
	if len(rules) == 0 {
		return m.fail("%s has no problems to ignore", m.pv.rel)
	}
	sort.Strings(rules)
	m.ignoring = ignoreRequest{rel: m.pv.rel, rules: rules}
	m.confirm = confirmIgnore
	m.msg, m.msgErr = fmt.Sprintf("Add %s %v to %s? This hides these findings from the scan.  y = add, other = cancel", m.pv.rel, rules, verify.IgnoreFileName), false
	return m, nil
}
