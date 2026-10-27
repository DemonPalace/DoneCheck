package tui

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"donecheck/internal/verify"
)

const (
	maxTreeRows  = 5000
	previewBytes = 2 << 20
	previewLines = 100000
	hlChunk      = 256
)

type treeRow struct {
	rel      string // slash-separated, relative to the project root
	name     string
	depth    int
	dir      bool
	link     bool // symlinks are listed but never followed
	expanded bool
	err      string
}

// Only expanded directories are read.
func buildTree(root string, expanded map[string]bool) []treeRow {
	var rows []treeRow
	var walk func(rel string, depth int)
	walk = func(rel string, depth int) {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			rows = append(rows, treeRow{rel: rel, name: "(unreadable)", depth: depth, err: err.Error()})
			return
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].IsDir() && !entries[j].IsDir() })
		for _, e := range entries {
			if len(rows) >= maxTreeRows {
				return
			}
			if e.IsDir() && verify.IsSkippedDir(e.Name()) {
				continue
			}
			row := treeRow{
				rel: path.Join(rel, e.Name()), name: e.Name(), depth: depth,
				dir: e.IsDir(), link: e.Type()&fs.ModeSymlink != 0,
			}
			row.expanded = row.dir && expanded[row.rel]
			rows = append(rows, row)
			if row.expanded {
				walk(row.rel, depth+1)
			}
		}
	}
	walk("", 0)
	return rows
}

type preview struct {
	rel   string
	lines []string // control characters removed; secrets are masked per chunk when shown
	note  string
	// plain and hl are filled per chunk when it comes into view.
	plain map[int][]string
	hl    map[int][]string
}

func (p preview) ensureChunks(from, to int) {
	if p.plain == nil || to <= from {
		return
	}
	for chunk := from / hlChunk; chunk <= (to-1)/hlChunk; chunk++ {
		if _, done := p.plain[chunk]; done {
			continue
		}
		lo, hi := chunk*hlChunk, min(len(p.lines), (chunk+1)*hlChunk)
		masked := make([]string, hi-lo)
		for i := range masked {
			masked[i] = verify.SafeText(p.lines[lo+i])
		}
		p.plain[chunk] = masked
		p.hl[chunk] = highlight(path.Base(p.rel), strings.Join(masked, "\n"), hi-lo)
	}
}

func (p preview) plainLine(i int) string {
	if c := p.plain[i/hlChunk]; i%hlChunk < len(c) {
		return c[i%hlChunk]
	}
	return p.lines[i]
}

func (p preview) line(i int) string {
	if h := p.hl[i/hlChunk]; i%hlChunk < len(h) {
		return h[i%hlChunk]
	}
	return p.plainLine(i)
}

// A PEM block is masked header to footer so the key body never shows, even across chunks.
func maskPrivateKeys(lines []string) {
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], "-----BEGIN") || !strings.Contains(lines[i], "PRIVATE KEY-----") {
			continue
		}
		end := i
		for end < len(lines) && !strings.Contains(lines[end], "-----END") && end-i < 200 {
			end++
		}
		for j := i; j <= min(end, len(lines)-1); j++ {
			lines[j] = "[REDACTED PRIVATE KEY]"
		}
		i = end
	}
}

// The path comes from the tree listing, never user input, and symlinks are not followed.
func loadPreview(root string, r treeRow) preview {
	p := preview{rel: r.rel}
	switch {
	case r.err != "":
		p.note = "cannot read directory: " + r.err
		return p
	case r.link:
		p.note = "symlink (not followed)"
		return p
	case r.dir:
		p.note = "directory (Enter or Right to expand)"
		return p
	}

	full := filepath.Join(root, filepath.FromSlash(r.rel))
	info, err := os.Lstat(full)
	if err != nil {
		p.note = err.Error()
		return p
	}
	if !info.Mode().IsRegular() {
		p.note = "not a regular file"
		return p
	}
	f, err := os.Open(full)
	if err != nil {
		p.note = err.Error()
		return p
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, previewBytes+1))
	if err != nil {
		p.note = err.Error()
		return p
	}
	truncated := len(data) > previewBytes
	if truncated {
		data = data[:previewBytes]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		p.note = fmt.Sprintf("binary file, %d bytes", info.Size())
		return p
	}
	text := strings.ReplaceAll(verify.Sanitize(string(data)), "\t", "    ")
	p.lines = strings.Split(strings.TrimRight(text, "\n"), "\n")
	maskPrivateKeys(p.lines)
	if len(p.lines) > previewLines {
		p.lines, truncated = p.lines[:previewLines], true
	}
	if truncated {
		p.note = fmt.Sprintf("preview truncated (file is %d bytes)", info.Size())
	}
	p.plain, p.hl = map[int][]string{}, map[int][]string{}
	return p
}

var statusRank = map[verify.Status]int{verify.Verified: 1, verify.Skipped: 2, verify.Inconclusive: 3, verify.Failed: 4}

func (m model) pathStatus() map[string]verify.Status {
	out := map[string]verify.Status{}
	for i, c := range m.cfg.Checks {
		if !(m.done[i] || m.prev[i]) {
			continue
		}
		st := m.results[i].Status
		paths := append([]string{c.Path}, m.results[i].Files...)
		if c.Type == verify.CodeScan {
			for _, f := range m.results[i].Scanned {
				mark(out, f, verify.Verified)
			}
			for _, f := range m.results[i].Unchecked {
				mark(out, f, verify.Skipped) // syntax not checked: neutral, not a warning
			}
			for _, f := range m.results[i].Findings {
				mark(out, f.Path, verify.Failed)
			}
			paths = []string{c.Path}
			if c.Path != "" {
				st = verify.Verified
				if m.results[i].Status != verify.Verified {
					st = m.results[i].Status
				}
			}
		}
		for _, file := range paths {
			if file == "" {
				continue
			}
			mark(out, file, st)
		}
	}
	return out
}

// mark records st for a file and each ancestor directory.
func mark(out map[string]verify.Status, file string, st verify.Status) {
	for p := path.Clean(filepath.ToSlash(file)); p != "." && p != "/"; p = path.Dir(p) {
		if statusRank[st] > statusRank[out[p]] {
			out[p] = st
		}
	}
}
