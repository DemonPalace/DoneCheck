package tui

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"donecheck/internal/verify"
)

const (
	maxGraphFiles     = 3000
	maxGraphFileBytes = 256 * 1024
)

var pathToken = regexp.MustCompile(`[A-Za-z0-9_@~+\-./]+`)

type graph struct {
	out map[string][]string // file -> files it mentions
	in  map[string][]string // file -> files that mention it
}

// buildGraph only sees path mentions between text files; language imports are not parsed.
func buildGraph(root string) graph {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are simply not part of the graph
		}
		if d.IsDir() {
			if p != root && verify.IsSkippedDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if rel, err := filepath.Rel(root, p); err == nil {
				files = append(files, filepath.ToSlash(rel))
			}
		}
		if len(files) >= maxGraphFiles {
			return fs.SkipAll
		}
		return nil
	})
	known := make(map[string]bool, len(files))
	for _, f := range files {
		known[f] = true
	}

	out, in := map[string]map[string]bool{}, map[string]map[string]bool{}
	for _, f := range files {
		data := readHead(filepath.Join(root, filepath.FromSlash(f)))
		if data == nil || bytes.IndexByte(data, 0) >= 0 {
			continue
		}
		dir := path.Dir(f)
		for _, tok := range pathToken.FindAllString(string(data), -1) {
			tok = strings.TrimRight(tok, ".,-")
			for _, cand := range []string{path.Clean(tok), path.Join(dir, tok)} {
				if cand != f && known[cand] {
					addEdge(out, f, cand)
					addEdge(in, cand, f)
				}
			}
		}
	}
	return graph{out: flatten(out), in: flatten(in)}
}

func readHead(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxGraphFileBytes))
	if err != nil {
		return nil
	}
	return data
}

func addEdge(m map[string]map[string]bool, k, v string) {
	if m[k] == nil {
		m[k] = map[string]bool{}
	}
	m[k][v] = true
}

func flatten(m map[string]map[string]bool) map[string][]string {
	res := make(map[string][]string, len(m))
	for k, set := range m {
		for v := range set {
			res[k] = append(res[k], v)
		}
		sort.Strings(res[k])
	}
	return res
}
