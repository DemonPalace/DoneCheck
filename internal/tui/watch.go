package tui

import (
	"fmt"
	"hash/fnv"
	"io/fs"
	"path/filepath"

	"donecheck/internal/verify"
)

const maxWatchEntries = 50000

// fingerprint hashes path, size and mtime; it detects change, it is not a verification result.
func fingerprint(root string) uint64 {
	h := fnv.New64a()
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && verify.IsSkippedDir(d.Name()) {
			return fs.SkipDir
		}
		if n++; n > maxWatchEntries {
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s|%d|%d\n", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return h.Sum64()
}
