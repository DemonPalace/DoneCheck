package tui

import (
	"fmt"
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"donecheck/internal/verify"
)

type progressEnv struct {
	run int
	p   verify.Progress
}

type progressMsg struct{ batch []progressEnv }

type markState int

const (
	markPending markState = iota
	markCurrent
	markDone
)

type scanView struct {
	active       bool
	check        string
	phase        string
	files        []string
	done         map[string]bool
	current      string
	doneN, total int
}

func (m model) progressCmd() tea.Cmd {
	ch, ctx := m.progress, m.parent
	return func() tea.Msg {
		select {
		case first := <-ch:
			batch := []progressEnv{first}
			for len(batch) < 512 {
				select {
				case e := <-ch:
					batch = append(batch, e)
				default:
					return progressMsg{batch}
				}
			}
			return progressMsg{batch}
		case <-ctx.Done():
			return nil
		}
	}
}

func (m model) applyProgress(batch []progressEnv) model {
	for _, e := range batch {
		if e.run != m.runID {
			continue
		}
		p := e.p
		switch p.Phase {
		case verify.PhaseMapped:
			m.scan = scanView{active: true, check: p.Check, phase: p.Phase, files: p.Files, done: map[string]bool{}, total: p.Total}
		case verify.PhaseSyntax:
			if !m.scan.active || m.scan.check != p.Check {
				continue
			}
			if len(p.Files) > 0 {
				for _, f := range p.Files {
					delete(m.scan.done, f)
				}
				m.scan.phase, m.scan.current, m.scan.doneN, m.scan.total = p.Phase, "", 0, p.Total
				continue
			}
			m.scan.phase, m.scan.current, m.scan.doneN, m.scan.total = p.Phase, p.File, p.Done, p.Total
			m.scan.done[p.File] = true
		case verify.PhaseScanning:
			if m.scan.active && m.scan.check == p.Check {
				m.scan.phase, m.scan.current, m.scan.doneN, m.scan.total = p.Phase, p.File, p.Done, p.Total
				m.scan.done[p.File] = true
			}
		case verify.PhaseAnalysing:
			if m.scan.active && m.scan.check == p.Check {
				m.scan.phase, m.scan.current = p.Phase, ""
			}
		}
	}
	return m
}

// marks: a directory is current if the file being checked is inside, pending while anything inside is left.
func (s scanView) marks() map[string]markState {
	out := map[string]markState{}
	set := func(p string, st markState) {
		for ; p != "." && p != "/" && p != ""; p = path.Dir(p) {
			prev, seen := out[p]
			switch {
			case !seen:
				out[p] = st
			case st == markCurrent || prev == markCurrent:
				out[p] = markCurrent
			case st == markPending || prev == markPending:
				out[p] = markPending
			}
		}
	}
	for _, f := range s.files {
		switch {
		case f == s.current:
			set(f, markCurrent)
		case s.done[f]:
			set(f, markDone)
		default:
			set(f, markPending)
		}
	}
	return out
}

func (s scanView) label() string {
	switch s.phase {
	case verify.PhaseScanning:
		return fmt.Sprintf("scanning %d/%d  %s", s.doneN, s.total, path.Base(s.current))
	case verify.PhaseSyntax:
		return fmt.Sprintf("checking syntax %d/%d  %s", s.doneN, s.total, path.Base(s.current))
	case verify.PhaseAnalysing:
		return "comparing code"
	case verify.PhaseMapped:
		return fmt.Sprintf("mapped %d files", s.total)
	}
	return strings.TrimSpace(s.phase)
}
