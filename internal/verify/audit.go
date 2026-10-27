package verify

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Exit 0 means nothing found; vulnExit means findings; anything else is a scanner error (INCONCLUSIVE).
type auditTool struct {
	argv     []string
	vulnExit int
	marker   string
	// install is fixed here, never read from config; the user approves it first.
	install []string
}

var auditTools = map[string]auditTool{
	// govulncheck: exit 3 means vulnerabilities.
	"govulncheck": {argv: []string{"govulncheck", "./..."}, vulnExit: 3,
		install: []string{"go", "install", "golang.org/x/vuln/cmd/govulncheck@latest"}},
	// osv-scanner: exit 1 means vulnerabilities; 127 and 128 are errors or no packages.
	"osv-scanner": {argv: []string{"osv-scanner", "-r", "."}, vulnExit: 1,
		install: []string{"go", "install", "github.com/google/osv-scanner/v2/cmd/osv-scanner@latest"}},
	// npm exits 1 for findings and registry errors alike, so the output must mention vulnerabilities.
	"npm": {argv: []string{"npm", "audit", "--audit-level=high"}, vulnExit: 1, marker: "vulnerabilit"},
}

func auditToolNames() []string {
	names := make([]string, 0, len(auditTools))
	for n := range auditTools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var advisoryID = regexp.MustCompile(`\b(?:CVE-\d{4}-\d{4,}|GHSA-[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4}|GO-\d{4}-\d{4,}|RUSTSEC-\d{4}-\d{4}|PYSEC-\d{4}-\d+)\b`)

func classifyAudit(c Check, ex execution, res *Result) {
	t := auditTools[c.Tool]
	code := *ex.exit
	switch {
	case code == 0:
		res.Status = Verified
		res.Reason = fmt.Sprintf("%s reported no known vulnerabilities", c.Tool)
	case code == t.vulnExit && strings.Contains(ex.output, t.marker):
		res.Status = Failed
		res.Reason = fmt.Sprintf("%s reported vulnerabilities%s", c.Tool, advisorySummary(ex.output))
	default:
		res.Status = Inconclusive
		res.Reason = fmt.Sprintf("%s exited with code %d, which is not a vulnerability result", c.Tool, code)
		res.Error = "the scanner may have failed (not installed correctly, offline, or no supported manifest); see evidence"
	}
}

func advisorySummary(output string) string {
	seen := map[string]bool{}
	var ids []string
	for _, id := range advisoryID.FindAllString(output, -1) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return " (see evidence)"
	}
	const show = 8
	shown := ids
	if len(ids) > show {
		shown = ids[:show]
	}
	s := ": " + strings.Join(shown, ", ")
	if len(ids) > show {
		s += fmt.Sprintf(" and %d more", len(ids)-show)
	}
	return s
}
