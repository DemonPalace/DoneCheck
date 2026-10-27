package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type editorDoneMsg struct {
	file string
	err  error
}

func editorArgs(editor []string, file string, line int) []string {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(editor[0])), ".exe")
	at := fmt.Sprintf("%s:%d", file, line)
	var extra []string
	switch name {
	case "vim", "vi", "nvim", "nano", "emacs", "micro", "pico", "joe", "ex", "view", "kak":
		extra = []string{fmt.Sprintf("+%d", line), file}
	case "code", "codium", "code-insiders", "cursor", "windsurf":
		extra = []string{"-g", at}
	case "hx", "helix", "subl", "zed", "mate":
		extra = []string{at}
	default:
		extra = []string{file}
	}
	return append(append([]string(nil), editor...), extra...)
}

func userEditor() ([]string, bool) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(env)); len(f) > 0 {
			return f, true
		}
	}
	for _, name := range []string{"nano", "vim", "vi", "micro"} {
		if p, err := exec.LookPath(name); err == nil {
			return []string{p}, true
		}
	}
	return nil, false
}

// The terminal is handed to the editor while it runs.
func (m model) editFile() (tea.Model, tea.Cmd) {
	switch {
	case m.running:
		return m.fail("wait for the run to finish before editing")
	case !m.showFile || m.pv.rel == "" || len(m.tree) == 0 || m.tree[m.treeCursor].dir || m.tree[m.treeCursor].link || m.pv.lines == nil && m.pv.note != "":
		return m.fail("select a text file in the Files box first")
	}
	editor, ok := userEditor()
	if !ok {
		return m.fail("no editor found: set $EDITOR (or $VISUAL)")
	}
	line := 1
	if m.jumpFile == m.pv.rel && m.jumpLine > 0 {
		line = m.jumpLine
	}
	argv := editorArgs(editor, m.pv.rel, line)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = m.cfg.Root
	rel := m.pv.rel
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return editorDoneMsg{file: rel, err: err} })
}
