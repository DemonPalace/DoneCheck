package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Mouse input is ignored during a y/n confirmation so a stray click cannot approve anything.
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.confirm != confirmNone || m.width < minWidth || m.height < minHeight {
		return m, nil
	}
	wheel := 0
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
		wheel = -1
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
		wheel = 1
	case msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft:
		return m, nil
	}

	l := m.layout()
	x, y := msg.X, msg.Y
	region := focusCount // none
	switch {
	case y >= topY && y < topY+l.topH && x < l.treeW:
		region = focusTree
	case y >= topY && y < topY+l.topH:
		region = focusChecks
	case y >= l.detailY && y < l.detailY+l.detailH:
		region = focusDetail
	case y >= l.cmdY && y < l.cmdY+cmdBoxHeight:
		region = focusCommand
	case y == bannerY && wheel == 0:
		return m.clickBanner(x)
	case y == agentsY && wheel == 0:
		return m.clickAgent(x), nil
	}
	if region == focusCount {
		return m, nil
	}

	if wheel != 0 {
		switch region {
		case focusTree:
			m = m.navTree(map[int]string{-1: "up", 1: "down"}[wheel])
		case focusChecks:
			m = m.navChecks(map[int]string{-1: "up", 1: "down"}[wheel])
		case focusDetail:
			m.scroll = max(0, m.scroll+3*wheel)
		}
		return m, nil
	}

	m.focus = region
	switch region {
	case focusTree:
		boxH := l.topH
		if row := y - topY - 1; row >= 0 && row < boxH-2 && len(m.tree) > 0 {
			idx := window(m.treeCursor, len(m.tree), boxH-2) + row
			if idx < len(m.tree) {
				m.treeCursor = idx
				if m.tree[idx].dir {
					return m.navTree("enter"), nil // toggle expand
				}
				m.showFile = true
				m = m.syncPreview()
			}
		}
	case focusDetail:
		ds := m.detailWindow(l.w-4, l.detailH-2)
		if row := y - l.detailY - 1; row >= 0 && row < len(ds.targets) && ds.targets[row] > 0 {
			m.jumpFile, m.jumpLine = m.pv.rel, ds.targets[row]
			m = m.scrollToLine(ds.targets[row])
		}
	case focusChecks:
		boxH := l.topH
		if row := y - topY - 1; row >= 0 && row < boxH-2 {
			if idx := window(m.cursor, len(m.cfg.Checks), boxH-2) + row; idx < len(m.cfg.Checks) {
				m.cursor, m.scroll, m.showFile = idx, 0, false
			}
		}
	}
	return m, nil
}

func (m model) clickBanner(x int) (tea.Model, tea.Cmd) {
	btn := m.button()
	if btn == "" || x < m.width-lipgloss.Width(btn) {
		return m, nil
	}
	switch m.buttonKind() {
	case buttonInstall:
		return m.askInstall()
	case buttonAllow:
		return m.askAllow()
	}
	return m.askFix()
}

func (m model) clickAgent(x int) model {
	_, spans := m.agentBar(m.width)
	for i, s := range spans {
		if x >= s[0] && x < s[1] {
			m.agentSel = i
		}
	}
	return m
}
