package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"go/scanner"
	"io"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ruleSyntax        = "syntax"
	syntaxFileTimeout = 10 * time.Second
	syntaxWorkers     = 4
	maxExternalPerExt = 400
	maxUnchecked      = 200
)

// Interpreters run in parse-only mode: the code is never executed. ./ keeps a leading - from being read as an option.
var externalSyntax = map[string]struct {
	bins []string
	args func(file string) []string
}{
	".py": {[]string{"python3", "python"}, func(f string) []string {
		return []string{"-c", "import ast,sys; ast.parse(open(sys.argv[1],'rb').read(), sys.argv[1])", f}
	}},
	".js":   {[]string{"node"}, func(f string) []string { return []string{"--check", f} }},
	".mjs":  {[]string{"node"}, func(f string) []string { return []string{"--check", f} }},
	".cjs":  {[]string{"node"}, func(f string) []string { return []string{"--check", f} }},
	".php":  {[]string{"php"}, func(f string) []string { return []string{"-l", f} }},
	".sh":   {[]string{"bash"}, func(f string) []string { return []string{"-n", f} }},
	".bash": {[]string{"bash"}, func(f string) []string { return []string{"-n", f} }},
	".rb":   {[]string{"ruby"}, func(f string) []string { return []string{"-c", f} }},
}

var (
	syntaxMessage = regexp.MustCompile(`(?i)syntax ?error|parse error|unexpected|unterminated|invalid syntax|expected`)
	lineNumber    = regexp.MustCompile(`(?i)(?:line |:)(\d+)`)
)

// jsonWithComments: these files allow comments and trailing commas, so a strict parse would be wrong.
func jsonWithComments(rel string) bool {
	base := strings.ToLower(path.Base(rel))
	return strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, "jsconfig") || strings.HasPrefix(base, ".eslintrc") ||
		base == ".babelrc" || strings.HasPrefix(rel, ".vscode/") || strings.HasPrefix(rel, ".devcontainer/")
}

func checkSyntaxBuiltin(rel string, data []byte) (line int, msg string, bad bool) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	switch strings.ToLower(path.Ext(rel)) {
	case ".json":
		if jsonWithComments(rel) {
			return 0, "", false
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			var se *json.SyntaxError
			if errors.As(err, &se) {
				return 1 + bytes.Count(data[:min(int(se.Offset), len(data))], []byte("\n")), "invalid JSON: " + se.Error(), true
			}
			return 1, "invalid JSON: " + err.Error(), true
		}
	case ".jsonl", ".ndjson":
		for i, l := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(l)) > 0 && !json.Valid(l) {
				return i + 1, "invalid JSON on this line", true
			}
		}
	case ".xml", ".svg", ".plist":
		d := xml.NewDecoder(bytes.NewReader(data))
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				var se *xml.SyntaxError
				if errors.As(err, &se) {
					return max(se.Line, 1), "invalid XML: " + se.Msg, true
				}
				return 1, "invalid XML: " + err.Error(), true
			}
		}
	}
	return 0, "", false
}

func goSyntaxFinding(rel string, err error) Finding {
	line, msg := 1, err.Error()
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		line, msg = max(list[0].Pos.Line, 1), list[0].Msg
	}
	return Finding{Path: rel, Line: line, Rule: ruleSyntax, Message: "Go syntax error: " + msg}
}

type syntaxOutcome struct {
	finding *Finding
	reason  string // set when the file could not be checked
}

// Files that cannot be checked are returned with a reason, never counted as correct.
func (s *scanState) runExternalSyntax(ctx context.Context, cfg *Config, opts Options) (unchecked map[string]string) {
	byExt := map[string][]string{}
	for _, rel := range s.scanned {
		ext := strings.ToLower(path.Ext(rel))
		if _, ok := externalSyntax[ext]; ok {
			byExt[ext] = append(byExt[ext], rel)
		}
	}
	unchecked = map[string]string{}
	type job struct{ rel, bin, ext string }
	var jobs []job
	for ext, files := range byExt {
		spec := externalSyntax[ext]
		bin := ""
		for _, b := range spec.bins {
			if p, err := exec.LookPath(b); err == nil {
				bin = p
				break
			}
		}
		for i, rel := range files {
			switch {
			case !opts.AllowCommands:
				unchecked[rel] = "needs --allow-commands to run " + spec.bins[0]
			case bin == "":
				unchecked[rel] = spec.bins[0] + " is not installed"
			case i >= maxExternalPerExt:
				unchecked[rel] = fmt.Sprintf("more than %d %s files", maxExternalPerExt, ext)
			default:
				jobs = append(jobs, job{rel, bin, ext})
			}
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].rel < jobs[j].rel })
	if len(jobs) > 0 {
		queued := make([]string, len(jobs))
		for i, j := range jobs {
			queued[i] = j.rel
		}
		s.emit(Progress{Phase: PhaseSyntax, Files: queued, Total: len(jobs)})
	}

	results := make([]syntaxOutcome, len(jobs))
	var finished atomic.Int32
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < syntaxWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = checkOneFile(ctx, cfg, jobs[i].bin, jobs[i].ext, jobs[i].rel)
				s.emit(Progress{Phase: PhaseSyntax, File: jobs[i].rel, Done: int(finished.Add(1)), Total: len(jobs)})
			}
		}()
	}
	for i := range jobs {
		next <- i
	}
	close(next)
	wg.Wait()

	for i, r := range results {
		switch {
		case r.finding != nil:
			s.add(*r.finding)
		case r.reason != "":
			unchecked[jobs[i].rel] = r.reason
		}
	}
	return unchecked
}

func checkOneFile(ctx context.Context, cfg *Config, bin, ext, rel string) syntaxOutcome {
	argv := append([]string{bin}, externalSyntax[ext].args("./"+rel)...)
	ex := execute(ctx, cfg, argv, syntaxFileTimeout)
	switch {
	case ex.problem != "":
		return syntaxOutcome{reason: ex.problem}
	case *ex.exit == 0:
		return syntaxOutcome{}
	case syntaxMessage.MatchString(ex.output):
		line := 1
		if m := lineNumber.FindStringSubmatch(ex.output); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
		first := ""
		for _, l := range strings.Split(ex.output, "\n") {
			if syntaxMessage.MatchString(l) {
				first = strings.TrimSpace(l)
				break
			}
		}
		return syntaxOutcome{finding: &Finding{Path: rel, Line: max(line, 1), Rule: ruleSyntax, Message: truncateBytes(clean(first), 200)}}
	}
	return syntaxOutcome{reason: fmt.Sprintf("%s exited with code %d without reporting a syntax problem", path.Base(bin), *ex.exit)}
}

func describeUnchecked(unchecked map[string]string) string {
	byReason := map[string]int{}
	for _, why := range unchecked {
		byReason[why]++
	}
	var parts []string
	for why, n := range byReason {
		parts = append(parts, fmt.Sprintf("%d file(s): %s", n, why))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}
