package verify

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Finding struct {
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	EndLine int      `json:"end_line,omitempty"` // last line of a multi-line finding
	Rule    string   `json:"rule"`
	Message string   `json:"message"`
	Snippet []string `json:"snippet,omitempty"`
}

const (
	ruleSecret       = "secret"
	ruleDuplicate    = "duplicate_code"
	ruleGoUnused     = "go_unused"
	ruleGoUnusedExp  = "go_unused_export"
	ruleTodo         = "todo_marker"
	ignoreMarker     = "donecheck:ignore"
	maxScanFiles     = 5000
	maxScanFileBytes = 1 << 20
	maxScanTotal     = 64 << 20
	maxFindings      = 1000
	maxShownFindings = 50
	maxSnippetLines  = 5
	defaultDupLines  = 20
	minDupLines      = 4
)

var defaultScanRules = []string{ruleSyntax, ruleSecret, ruleDuplicate, ruleGoUnused}

var knownScanRules = map[string]bool{ruleSyntax: true, ruleSecret: true, ruleDuplicate: true, ruleGoUnused: true, ruleGoUnusedExp: true, ruleTodo: true}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "target": true, ".venv": true, "__pycache__": true, "vendor": true,
	".skill": true, // instructions for AI agents, not project code
}

// IsSkippedDir reports directories excluded from scans and the file tree.
func IsSkippedDir(name string) bool { return skipDirs[name] }

// dataExtensions repeat structure by nature, so duplicate_code skips them.
var dataExtensions = map[string]bool{
	".json": true, ".jsonc": true, ".json5": true, ".jsonl": true, ".ndjson": true, ".geojson": true,
	".csv": true, ".tsv": true, ".sql": true, ".xml": true, ".yaml": true, ".yml": true, ".toml": true,
	".ini": true, ".plist": true, ".lock": true,
	".md": true, ".txt": true, ".rst": true, ".adoc": true,
}

// Lock files and source maps are generated, large and repetitive.
var generatedFiles = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "composer.lock": true,
	"Cargo.lock": true, "go.sum": true, "Gemfile.lock": true, "poetry.lock": true,
}

var todoRe = regexp.MustCompile(`\b(?:TODO|FIXME|XXX|HACK)\b|(?i:\bnot (?:yet )?implemented\b|\bunimplemented\b)`)

func scanEligible(name, rel string, ignore []string) bool {
	return !generatedFiles[name] && !strings.HasSuffix(name, ".min.js") && !strings.HasSuffix(name, ".map") &&
		!isBackupFile(name) && !ignored(ignore, rel)
}

// Backup copies are duplicates by nature and not part of the project.
func isBackupFile(name string) bool {
	if strings.HasSuffix(name, "~") {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".bak", ".orig", ".rej", ".swp", ".swo", ".tmp":
		return true
	}
	return false
}

func scanRules(c Check) []string {
	if len(c.Rules) > 0 {
		return c.Rules
	}
	return defaultScanRules
}

func ruleOn(rules []string, name string) bool {
	for _, r := range rules {
		if r == name {
			return true
		}
	}
	return false
}

// ignored tries a pattern against the path, the file name and every parent directory.
func ignored(patterns []string, rel string) bool {
	candidates := []string{rel, path.Base(rel)}
	for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
		candidates = append(candidates, d)
	}
	for _, p := range patterns {
		for _, c := range candidates {
			if ok, _ := path.Match(p, c); ok {
				return true
			}
		}
	}
	return false
}

type goDecl struct {
	name     string
	kind     string
	line     int
	exported bool
}

type goFile struct {
	rel, dir, pkg string
	external      bool // package x_test
	test          bool
	decls         []goDecl
	idents        map[string]int
	selectors     map[string]bool
	apiTypes      map[string]bool // types named in exported fields and signatures
}

type fileLines struct {
	rel    string
	hashes []uint64 // one per non-trivial line
	lineNo []int
	texts  []string // the normalised text of each non-trivial line, for snippets
}

type scanState struct {
	rules    []string
	findings []Finding
	scanned  []string
	dups     []fileLines
	goFiles  []goFile
	total    int
	overflow bool
	emit     func(Progress)
}

func (s *scanState) add(f Finding) {
	if len(s.findings) < maxFindings {
		s.findings = append(s.findings, f)
	} else {
		s.overflow = true
	}
}

func checkCodeScan(ctx context.Context, cfg *Config, opts Options, c Check, res *Result) {
	rules := scanRules(c)
	root, reason, err := scanDirectory(cfg, c)
	if err != nil {
		inconclusive(res, reason, err)
		return
	}
	ignoreLines, err := loadIgnoreFile(cfg.Root)
	if err != nil {
		inconclusive(res, "could not read the ignore list", err)
		return
	}
	c.IgnorePaths = append(append([]string(nil), c.IgnorePaths...), wholeFilePatterns(ignoreLines)...)

	st := &scanState{rules: rules, emit: func(p Progress) {
		if opts.Progress != nil {
			p.Check = c.ID
			opts.Progress(p)
		}
	}}
	refs, err := st.list(ctx, cfg.Root, root, c)
	if err != nil {
		inconclusive(res, "scan could not be completed", err)
		return
	}
	mapped := make([]string, len(refs))
	for i, r := range refs {
		mapped[i] = r.rel
	}
	st.emit(Progress{Phase: PhaseMapped, Files: mapped, Total: len(refs)})
	if err := st.scanAll(ctx, refs); err != nil {
		inconclusive(res, "scan could not be completed", err)
		return
	}
	st.emit(Progress{Phase: PhaseAnalysing})
	if ruleOn(rules, ruleDuplicate) {
		n := c.MinDuplicateLines
		if n == 0 {
			n = defaultDupLines
		}
		st.findDuplicates(n)
	}
	st.findGoProblems()

	var unchecked map[string]string
	if ruleOn(rules, ruleSyntax) {
		unchecked = st.runExternalSyntax(ctx, cfg, opts)
	}
	fillScanResult(res, st, unchecked, ignoreLines)
	res.NeedsCommands = !opts.AllowCommands && len(unchecked) > 0
}

func scanDirectory(cfg *Config, c Check) (root, reason string, err error) {
	if c.Path == "" {
		return cfg.Root, "", nil
	}
	p, err := resolveInRoot(cfg.Root, c.Path)
	if err != nil {
		return "", "could not safely resolve the scan directory", err
	}
	if info, statErr := os.Stat(p); statErr != nil || !info.IsDir() {
		return "", "scan path is not a directory", fmt.Errorf("%s", c.Path)
	}
	return p, "", nil
}

// Missing evidence is never a pass: unverified syntax leaves the check inconclusive.
func fillScanResult(res *Result, st *scanState, unchecked map[string]string, ignoreLines []ignoreRule) {
	sort.SliceStable(st.findings, func(i, j int) bool {
		a, b := st.findings[i], st.findings[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	for i := range st.findings {
		st.findings[i].Path = clean(st.findings[i].Path)
	}
	st.findings, res.Ignored = dropIgnored(st.findings, ignoreLines)
	res.Findings = st.findings
	res.Scanned = st.scanned

	for rel := range unchecked {
		res.Unchecked = append(res.Unchecked, rel)
	}
	sort.Strings(res.Unchecked)

	suffix := ""
	if res.Ignored > 0 {
		suffix = fmt.Sprintf("; %d finding(s) hidden by %s", res.Ignored, IgnoreFileName)
	}
	note := ""
	if len(unchecked) > 0 {
		note = "syntax not verified for " + describeUnchecked(unchecked)
		if len(res.Unchecked) > maxUnchecked {
			res.Unchecked = res.Unchecked[:maxUnchecked]
		}
	}
	switch {
	case len(st.findings) > 0:
		res.Status = Failed
		res.Reason = summarize(st.findings, st.overflow) + suffix
		res.Evidence = renderFindings(st.findings, st.overflow)
		if note != "" {
			res.Evidence += "\n" + note
		}
	case note != "":
		res.Status = Inconclusive
		res.Reason = "no findings, but " + note
		res.Error = note
	default:
		res.Status = Verified
		res.Reason = fmt.Sprintf("scanned %d files, no findings (rules: %s)%s", len(st.scanned), strings.Join(st.rules, ", "), suffix)
	}
}

func summarize(list []Finding, overflow bool) string {
	byRule := map[string]int{}
	files := map[string]bool{}
	for _, f := range list {
		byRule[f.Rule]++
		files[f.Path] = true
	}
	var parts []string
	for r, n := range byRule {
		parts = append(parts, fmt.Sprintf("%s x%d", r, n))
	}
	sort.Strings(parts)
	more := ""
	if overflow {
		more = "+"
	}
	return fmt.Sprintf("%d%s finding(s) in %d file(s): %s", len(list), more, len(files), strings.Join(parts, ", "))
}

func renderFindings(list []Finding, overflow bool) string {
	var b strings.Builder
	for i, f := range list {
		if i == maxShownFindings {
			fmt.Fprintf(&b, "... and %d more (see the JSON report for all)\n", len(list)-maxShownFindings)
			break
		}
		fmt.Fprintf(&b, "%s:%d  [%s] %s\n", f.Path, f.Line, f.Rule, f.Message)
		for _, l := range f.Snippet {
			fmt.Fprintf(&b, "    | %s\n", l)
		}
	}
	if overflow {
		fmt.Fprintf(&b, "(finding list capped at %d)\n", maxFindings)
	}
	return strings.TrimRight(b.String(), "\n")
}

type fileRef struct{ rel, full string }

func (s *scanState) list(ctx context.Context, projectRoot, root string, c Check) ([]fileRef, error) {
	var refs []fileRef
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are not scanned; they are never reported as clean
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, rerr := filepath.Rel(projectRoot, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || ignored(c.IgnorePaths, rel)) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !scanEligible(d.Name(), rel, c.IgnorePaths) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxScanFileBytes {
			return nil
		}
		if len(refs) >= maxScanFiles {
			return fmt.Errorf("more than %d files: narrow the scan with \"path\" or \"ignore_paths\"", maxScanFiles)
		}
		if s.total += int(info.Size()); s.total > maxScanTotal {
			return fmt.Errorf("more than %d MiB of source: narrow the scan with \"path\" or \"ignore_paths\"", maxScanTotal>>20)
		}
		refs = append(refs, fileRef{rel, p})
		return nil
	})
	return refs, err
}

func (s *scanState) scanAll(ctx context.Context, refs []fileRef) error {
	for i, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if data := readText(ref.full); data != nil {
			s.scanned = append(s.scanned, ref.rel)
			s.scanFile(ref.rel, data)
		}
		s.emit(Progress{Phase: PhaseScanning, File: ref.rel, Done: i + 1, Total: len(refs)})
	}
	return nil
}

func readText(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxScanFileBytes+1))
	if err != nil || len(data) > maxScanFileBytes || bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return nil
	}
	return data
}

func (s *scanState) scanFile(rel string, data []byte) {
	text := string(data)
	doSecret, doTodo := ruleOn(s.rules, ruleSecret), ruleOn(s.rules, ruleTodo)
	doDup := ruleOn(s.rules, ruleDuplicate) && !dataExtensions[strings.ToLower(path.Ext(rel))]
	fl := fileLines{rel: rel}
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, ignoreMarker) {
			continue
		}
		if doSecret && hasStrongSecret(line) {
			s.add(Finding{Path: rel, Line: i + 1, Rule: ruleSecret, Message: "looks like a credential or private key; remove it and rotate it"})
		}
		if doTodo && todoRe.MatchString(line) {
			s.add(Finding{Path: rel, Line: i + 1, Rule: ruleTodo, Message: "unfinished-work marker"})
		}
		if doDup {
			if norm, ok := normalizeLine(line); ok {
				h := fnv.New64a()
				h.Write([]byte(norm))
				fl.hashes = append(fl.hashes, h.Sum64())
				fl.lineNo = append(fl.lineNo, i+1)
				fl.texts = append(fl.texts, norm)
			}
		}
	}
	if doDup {
		s.dups = append(s.dups, fl)
	}
	if strings.HasSuffix(rel, ".go") && (ruleOn(s.rules, ruleGoUnused) || ruleOn(s.rules, ruleGoUnusedExp) || ruleOn(s.rules, ruleSyntax)) {
		s.parseGo(rel, data)
	}
	if ruleOn(s.rules, ruleSyntax) {
		if line, msg, bad := checkSyntaxBuiltin(rel, data); bad {
			s.add(Finding{Path: rel, Line: line, Rule: ruleSyntax, Message: msg})
		}
	}
}

var (
	quotedString = regexp.MustCompile("\"(?:\\\\.|[^\"\\\\])*\"|'(?:\\\\.|[^'\\\\])*'|`[^`]*`")
	identifier   = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// normalizeLine drops lines that would only create false duplicates: blanks, comments, lone punctuation, data rows and prose.
func normalizeLine(line string) (string, bool) {
	t := strings.Join(strings.Fields(line), " ")
	if len(t) < 4 {
		return "", false
	}
	for _, prefix := range []string{"//", "#", "/*", "*", "--", "<!--", ";"} {
		if strings.HasPrefix(t, prefix) {
			return "", false
		}
	}
	if strings.Trim(t, "{}()[];,") == "" {
		return "", false
	}
	withoutStrings := quotedString.ReplaceAllString(t, `""`)
	if !identifier.MatchString(strings.ReplaceAll(withoutStrings, `""`, "")) {
		return "", false // only literals and separators: data, not logic
	}
	if strings.Count(t, " ") >= 4 && !strings.ContainsAny(withoutStrings, "(){}[];=<>+*&|") {
		return "", false // prose
	}
	return t, true
}

func hasStrongSecret(line string) bool {
	for _, re := range strongSecretPatterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

type winLoc struct{ file, start int }

func (s *scanState) findDuplicates(n int) {
	windows := map[uint64][]winLoc{}
	for fi, f := range s.dups {
		for i := 0; i+n <= len(f.hashes); i++ {
			if !variedEnough(f.hashes[i : i+n]) {
				continue
			}
			h := fnv.New64a()
			var buf [8]byte
			for _, lh := range f.hashes[i : i+n] {
				for k := range buf {
					buf[k] = byte(lh >> (8 * k))
				}
				h.Write(buf[:])
			}
			windows[h.Sum64()] = append(windows[h.Sum64()], winLoc{fi, i})
		}
	}

	same := func(a, b winLoc) bool {
		x, y := s.dups[a.file].hashes[a.start:a.start+n], s.dups[b.file].hashes[b.start:b.start+n]
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
		return true
	}
	partner := map[int]map[int]winLoc{}
	for _, locs := range windows {
		if len(locs) < 2 {
			continue
		}
		for _, a := range locs {
			for _, b := range locs {
				overlap := a.file == b.file && abs(a.start-b.start) < n
				if a == b || overlap || !same(a, b) {
					continue
				}
				if partner[a.file] == nil {
					partner[a.file] = map[int]winLoc{}
				}
				if _, ok := partner[a.file][a.start]; !ok {
					partner[a.file][a.start] = b
				}
				break
			}
		}
	}

	for fi, starts := range partner {
		var idx []int
		for st := range starts {
			idx = append(idx, st)
		}
		sort.Ints(idx)
		f := s.dups[fi]
		for i := 0; i < len(idx); {
			j := i
			for j+1 < len(idx) && idx[j+1] == idx[j]+1 && starts[idx[j+1]] == (winLoc{starts[idx[j]].file, starts[idx[j]].start + 1}) {
				j++
			}
			other := starts[idx[i]]
			size := idx[j] - idx[i] + n
			var snippet []string
			for _, text := range f.texts[idx[i] : idx[i]+min(size, maxSnippetLines)] {
				snippet = append(snippet, clean(text))
			}
			if size > maxSnippetLines {
				snippet = append(snippet, fmt.Sprintf("... %d more lines", size-maxSnippetLines))
			}
			s.add(Finding{
				Path: f.rel, Line: f.lineNo[idx[i]], EndLine: f.lineNo[idx[j]+n-1], Rule: ruleDuplicate,
				Message: fmt.Sprintf("%d duplicated lines, the same code is also at %s:%d", size, s.dups[other.file].rel, s.dups[other.file].lineNo[other.start]),
				Snippet: snippet,
			})
			i = j + 1
		}
	}
}

// A line repeated many times is not a repeated block of code.
func variedEnough(hashes []uint64) bool {
	distinct := map[uint64]bool{}
	for _, h := range hashes {
		distinct[h] = true
	}
	return len(distinct)*3 >= len(hashes)*2
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (s *scanState) parseGo(rel string, data []byte) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, data, parser.SkipObjectResolution)
	if err != nil {
		if ruleOn(s.rules, ruleSyntax) {
			s.add(goSyntaxFinding(rel, err))
		} else {
			// Without the syntax rule the file still cannot be analysed; say so.
			s.add(Finding{Path: rel, Line: 1, Rule: ruleGoUnused, Message: "Go syntax error, cannot analyse: " + strings.SplitN(err.Error(), "\n", 2)[0]})
		}
		return
	}
	gf := goFile{
		rel: rel, dir: path.Dir(rel), pkg: f.Name.Name,
		external: strings.HasSuffix(f.Name.Name, "_test"), test: strings.HasSuffix(rel, "_test.go"),
		idents: map[string]int{}, selectors: map[string]bool{}, apiTypes: map[string]bool{},
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			gf.idents[n.Name]++
		case *ast.SelectorExpr:
			gf.selectors[n.Sel.Name] = true
		}
		return true
	})
	namesIn := func(n ast.Node) {
		ast.Inspect(n, func(x ast.Node) bool {
			if id, ok := x.(*ast.Ident); ok {
				gf.apiTypes[id.Name] = true
			}
			return true
		})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				namesIn(d.Type)
			}
			if d.Recv == nil {
				gf.decls = append(gf.decls, goDecl{d.Name.Name, "func", fset.Position(d.Pos()).Line, d.Name.IsExported()})
			}
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					if st, ok := sp.Type.(*ast.StructType); ok {
						for _, fld := range st.Fields.List {
							for _, nm := range fld.Names {
								if nm.IsExported() {
									namesIn(fld.Type)
								}
							}
						}
					}
					gf.decls = append(gf.decls, goDecl{sp.Name.Name, "type", fset.Position(sp.Pos()).Line, sp.Name.IsExported()})
				case *ast.ValueSpec:
					for _, nm := range sp.Names {
						gf.decls = append(gf.decls, goDecl{nm.Name, d.Tok.String(), fset.Position(nm.Pos()).Line, nm.IsExported()})
					}
				}
			}
		}
	}
	s.goFiles = append(s.goFiles, gf)
}

func (s *scanState) findGoProblems() {
	byDir := map[string][]goFile{}
	for _, g := range s.goFiles {
		byDir[g.dir] = append(byDir[g.dir], g)
	}

	if ruleOn(s.rules, ruleGoUnused) {
		for _, files := range byDir {
			refs := map[string]int{}
			for _, g := range files {
				if g.test {
					continue
				}
				for name, n := range g.idents {
					refs[name] += n
				}
			}
			for _, g := range files {
				if g.test {
					continue
				}
				for _, d := range g.decls {
					if d.exported || d.name == "_" || d.name == "main" || d.name == "init" {
						continue
					}
					if refs[d.name]-1 <= 0 {
						s.add(Finding{Path: g.rel, Line: d.line, Rule: ruleGoUnused, Message: fmt.Sprintf("unexported %s %s is never used by non-test code", d.kind, d.name)})
					}
				}
			}
		}
	}

	if ruleOn(s.rules, ruleGoUnusedExp) {
		for dir, files := range byDir {
			api := map[string]bool{}
			for _, g := range files {
				for name := range g.apiTypes {
					api[name] = true
				}
			}
			used := map[string]bool{}
			for otherDir, others := range byDir {
				for _, g := range others {
					if otherDir != dir || g.external {
						for name := range g.selectors {
							used[name] = true
						}
					}
				}
			}
			for _, g := range files {
				if g.test || g.pkg == "main" {
					continue
				}
				for _, d := range g.decls {
					if d.exported && !used[d.name] && !(d.kind == "type" && api[d.name]) {
						s.add(Finding{Path: g.rel, Line: d.line, Rule: ruleGoUnusedExp, Message: fmt.Sprintf("exported %s %s is not used outside package %s; it could be unexported", d.kind, d.name, g.pkg)})
					}
				}
			}
		}
	}
}
