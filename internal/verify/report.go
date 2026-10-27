package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	ExitVerified    = 0 // every mandatory check VERIFIED
	ExitFailed      = 1 // at least one mandatory check FAILED
	ExitUsage       = 2 // bad flags, unreadable or invalid config
	ExitNotVerified = 3 // no mandatory failure, but not everything VERIFIED
)

type Summary struct {
	Verified        int  `json:"verified"`
	Failed          int  `json:"failed"`
	Inconclusive    int  `json:"inconclusive"`
	Skipped         int  `json:"skipped"`
	FullyVerified   bool `json:"fully_verified"`
	MandatoryFailed int  `json:"mandatory_failed"`
}

type Report struct {
	ProjectRoot string    `json:"project_root"`
	GeneratedAt time.Time `json:"generated_at"`
	Summary     Summary   `json:"summary"`
	Checks      []Result  `json:"checks"`
}

func Run(ctx context.Context, cfg *Config, opts Options) Report {
	results := make([]Result, 0, len(cfg.Checks))
	for _, c := range cfg.Checks {
		results = append(results, RunCheck(ctx, cfg, opts, c))
	}
	return NewReport(cfg, results)
}

// NewReport marks the project fully verified only with at least one mandatory check, all VERIFIED.
func NewReport(cfg *Config, results []Result) Report {
	r := Report{ProjectRoot: cfg.Root, GeneratedAt: time.Now().UTC(), Checks: results}
	mandatory, mandatoryVerified := 0, 0
	for _, res := range results {
		switch res.Status {
		case Verified:
			r.Summary.Verified++
		case Failed:
			r.Summary.Failed++
		case Skipped:
			r.Summary.Skipped++
		default:
			r.Summary.Inconclusive++
		}
		if res.Mandatory {
			mandatory++
			if res.Status == Verified {
				mandatoryVerified++
			}
			if res.Status == Failed {
				r.Summary.MandatoryFailed++
			}
		}
	}
	r.Summary.FullyVerified = mandatory > 0 && mandatory == mandatoryVerified && len(results) == len(cfg.Checks)
	return r
}

func (r Report) ExitCode() int {
	switch {
	case r.Summary.MandatoryFailed > 0:
		return ExitFailed
	case r.Summary.FullyVerified:
		return ExitVerified
	default:
		return ExitNotVerified
	}
}

func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func overall(r Report) string {
	if r.Summary.FullyVerified {
		return "FULLY VERIFIED"
	}
	if r.Summary.MandatoryFailed > 0 {
		return "NOT VERIFIED (mandatory checks failed)"
	}
	return "NOT FULLY VERIFIED (some mandatory checks are inconclusive, skipped or missing)"
}

func (r Report) counts() string {
	return fmt.Sprintf("%d verified, %d failed, %d inconclusive, %d skipped",
		r.Summary.Verified, r.Summary.Failed, r.Summary.Inconclusive, r.Summary.Skipped)
}

func label(res Result) string {
	if res.Mandatory {
		return string(res.Type)
	}
	return string(res.Type) + ", optional"
}

func (r Report) WriteText(w io.Writer) error {
	var b strings.Builder
	for _, res := range r.Checks {
		fmt.Fprintf(&b, "%-12s %s (%s): %s\n", res.Status, res.ID, label(res), res.Reason)
		if res.Error != "" {
			fmt.Fprintf(&b, "    error: %s\n", indent(res.Error))
		}
		if res.Evidence != "" {
			fmt.Fprintf(&b, "    evidence:\n%s\n", indentBlock(res.Evidence))
		}
	}
	fmt.Fprintf(&b, "\n%s: %s\n", overall(r), r.counts())
	_, err := io.WriteString(w, b.String())
	return err
}

func (r Report) WriteMarkdown(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# DoneCheck report\n\n**Result: %s**\n\n%s\n\nGenerated %s\n\n",
		overall(r), r.counts(), r.GeneratedAt.Format(time.RFC3339))
	for _, res := range r.Checks {
		fmt.Fprintf(&b, "## %s `%s`\n\n- Type: %s\n- Mandatory: %t\n- Reason: %s\n",
			res.Status, mdInline(res.ID), res.Type, res.Mandatory, mdInline(res.Reason))
		if res.ExitCode != nil {
			fmt.Fprintf(&b, "- Exit code: %d\n", *res.ExitCode)
		}
		if res.Error != "" {
			fmt.Fprintf(&b, "- Error: %s\n", mdInline(res.Error))
		}
		if res.Evidence != "" {
			fence := fenceFor(res.Evidence)
			fmt.Fprintf(&b, "\n%s text\n%s\n%s\n", fence, strings.TrimRight(res.Evidence, "\n"), fence)
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// fenceFor returns a backtick fence longer than any run in s, so evidence cannot close the block.
func fenceFor(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := longest + 1
	if n < 3 {
		n = 3
	}
	return strings.Repeat("`", n)
}

func mdInline(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "`", "'")
}

func indent(s string) string { return strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ") }

func indentBlock(s string) string {
	return "      " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n      ")
}
