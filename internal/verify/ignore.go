package verify

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// IgnoreFileName holds one path pattern per line, optionally followed by the rules it applies to,
// such as "src/legacy.py duplicate_code".
const IgnoreFileName = ".donecheckignore"

const maxIgnoreFileBytes = 64 << 10

type ignoreRule struct {
	pattern string
	rules   map[string]bool // empty: every rule
	line    int
}

// Any problem is an error, so an ignore list is never half-applied.
func loadIgnoreFile(root string) ([]ignoreRule, error) {
	p := filepath.Join(root, IgnoreFileName)
	info, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxIgnoreFileBytes {
		return nil, fmt.Errorf("%s must be a regular file of at most %d bytes", IgnoreFileName, maxIgnoreFileBytes)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxIgnoreFileBytes+1))
	if err != nil {
		return nil, err
	}

	var out []ignoreRule
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if _, err := path.Match(fields[0], ""); err != nil {
			return nil, fmt.Errorf("%s line %d: bad pattern %q", IgnoreFileName, n, fields[0])
		}
		r := ignoreRule{pattern: fields[0], rules: map[string]bool{}, line: n}
		for _, name := range fields[1:] {
			if !knownScanRules[name] {
				return nil, fmt.Errorf("%s line %d: unknown rule %q", IgnoreFileName, n, name)
			}
			r.rules[name] = true
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func wholeFilePatterns(rules []ignoreRule) []string {
	var p []string
	for _, r := range rules {
		if len(r.rules) == 0 {
			p = append(p, r.pattern)
		}
	}
	return p
}

func dropIgnored(findings []Finding, rules []ignoreRule) ([]Finding, int) {
	kept := findings[:0:0]
	dropped := 0
	for _, f := range findings {
		hidden := false
		for _, r := range rules {
			if r.rules[f.Rule] && ignored([]string{r.pattern}, f.Path) {
				hidden = true
				break
			}
		}
		if hidden {
			dropped++
		} else {
			kept = append(kept, f)
		}
	}
	return kept, dropped
}

var patternEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)

// AppendIgnore appends a literal entry; existing content is never rewritten.
func AppendIgnore(root, rel string, rules ...string) error {
	if err := checkRelPath(rel); err != nil {
		return err
	}
	if strings.ContainsAny(rel, " \t\r\n#") {
		return fmt.Errorf("%q contains whitespace or '#', which %s cannot express", rel, IgnoreFileName)
	}
	for _, r := range rules {
		if !knownScanRules[r] {
			return fmt.Errorf("unknown rule %q", r)
		}
	}
	p := filepath.Join(root, IgnoreFileName)
	header := ""
	if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", IgnoreFileName)
	} else if err != nil {
		header = "# Findings DoneCheck's code_scan should leave out. Format: path-pattern [rule ...]\n"
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	line := patternEscaper.Replace(filepath.ToSlash(rel))
	if len(rules) > 0 {
		line += " " + strings.Join(rules, " ")
	}
	if _, err := f.WriteString(header + line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
