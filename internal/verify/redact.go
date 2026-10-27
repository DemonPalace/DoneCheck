package verify

import (
	"regexp"
	"strings"
	"unicode"
)

const redacted = "[REDACTED]"

var (
	rePrivateKey = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	reAWSKey     = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	reGitHub     = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)
	reGitHubPAT  = regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`)
	reSlack      = regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)
	reSK         = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)
	reJWT        = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+`)
)

// strongSecretPatterns are token formats specific enough to flag in source without false positives.
var strongSecretPatterns = []*regexp.Regexp{rePrivateKey, reAWSKey, reGitHub, reGitHubPAT, reSlack, reSK, reJWT}

var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{rePrivateKey, "[REDACTED PRIVATE KEY]"},
	{reAWSKey, redacted},
	{reGitHub, redacted},
	{reGitHubPAT, redacted},
	{reSlack, redacted},
	{reSK, redacted},
	{reJWT, redacted},
	{regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{16,}`), "${1}" + redacted},
	{regexp.MustCompile(`(://[^/\s:@]+:)[^@\s/]+@`), "${1}" + redacted + "@"},
	{regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*)("?\s*[:=]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`), "${1}${2}" + redacted},
}

// redact is best-effort: unknown secret formats pass through.
func redact(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// sanitize drops control characters and invalid UTF-8 so text is safe to print.
func sanitize(s string) string {
	s = strings.ToValidUTF8(s, "�")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func clean(s string) string { return sanitize(redact(s)) }

// Sanitize is sanitize without redaction, cheap enough for whole files.
func Sanitize(s string) string { return sanitize(s) }

func SafeText(s string) string { return clean(s) }
