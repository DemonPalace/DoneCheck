package verify

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var errOutsideRoot = errors.New("path is outside the project root")

func checkRelPath(p string) error {
	if p == "" || strings.ContainsRune(p, 0) {
		return errors.New("path is empty or contains a NUL byte")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) || filepath.VolumeName(p) != "" {
		return fmt.Errorf("%w: %q is absolute", errOutsideRoot, p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %q escapes via ..", errOutsideRoot, p)
	}
	return nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// A not-exist error is returned only after the deepest existing ancestor is confirmed inside root.
func resolveInRoot(root, rel string) (string, error) {
	if err := checkRelPath(rel); err != nil {
		return "", err
	}
	full := filepath.Join(root, rel)

	existing := full
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", err
		}
		existing = parent
	}

	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", rel, err)
	}
	if !within(root, real) {
		return "", fmt.Errorf("%w: %q resolves through a symlink to %s", errOutsideRoot, rel, real)
	}
	if existing != full {
		return "", fmt.Errorf("%q: %w", rel, fs.ErrNotExist)
	}
	return real, nil
}
