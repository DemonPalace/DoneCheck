// Package verify checks claims about a project against observable evidence.
package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type CheckType string

const (
	FileExists      CheckType = "file_exists"
	TextContains    CheckType = "text_contains"
	CommandExitCode CheckType = "command_exit_code"
	TestSuite       CheckType = "test_suite"
	ManualReview    CheckType = "manual_review"
	DependencyAudit CheckType = "dependency_audit"
	CodeScan        CheckType = "code_scan"
)

const (
	fallbackTimeoutSeconds = 60
	maxTimeoutSeconds      = 3600
	defaultMaxOutputBytes  = 16 * 1024
	maxMaxOutputBytes      = 1024 * 1024
	maxConfigBytes         = 1 << 20
)

type Check struct {
	ID          string    `json:"id"`
	Type        CheckType `json:"type"`
	Description string    `json:"description,omitempty"`
	Optional    bool      `json:"optional,omitempty"`

	Path              string   `json:"path,omitempty"`
	Paths             []string `json:"paths,omitempty"`
	Text              string   `json:"text,omitempty"`
	Command           []string `json:"command,omitempty"`
	Tool              string   `json:"tool,omitempty"`
	Rules             []string `json:"rules,omitempty"`
	IgnorePaths       []string `json:"ignore_paths,omitempty"`
	MinDuplicateLines int      `json:"min_duplicate_lines,omitempty"`
	ExpectExit        int      `json:"expect_exit,omitempty"`
	OutputContains    string   `json:"output_contains,omitempty"`
	TimeoutSeconds    int      `json:"timeout_seconds,omitempty"`
}

type Config struct {
	ProjectRoot           string  `json:"project_root,omitempty"`
	DefaultTimeoutSeconds int     `json:"default_timeout_seconds,omitempty"`
	MaxOutputBytes        int     `json:"max_output_bytes,omitempty"`
	Checks                []Check `json:"checks"`

	// Root is the resolved absolute project root, set by LoadConfig.
	Root string `json:"-"`
}

func LoadConfig(file string) (*Config, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("opening config: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", file, err)
	}
	if len(data) > maxConfigBytes {
		return nil, fmt.Errorf("config %s is larger than %d bytes", file, maxConfigBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", file, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing config %s: unexpected data after the top-level object", file)
	}

	absConfig, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("resolving config file: %w", err)
	}
	if err := cfg.validate(filepath.Dir(absConfig)); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", file, err)
	}
	return &cfg, nil
}

func (cfg *Config) validate(baseDir string) error {
	if cfg.DefaultTimeoutSeconds == 0 {
		cfg.DefaultTimeoutSeconds = fallbackTimeoutSeconds
	}
	if cfg.DefaultTimeoutSeconds < 1 || cfg.DefaultTimeoutSeconds > maxTimeoutSeconds {
		return fmt.Errorf("default_timeout_seconds must be between 1 and %d", maxTimeoutSeconds)
	}
	if cfg.MaxOutputBytes == 0 {
		cfg.MaxOutputBytes = defaultMaxOutputBytes
	}
	if cfg.MaxOutputBytes < 1 || cfg.MaxOutputBytes > maxMaxOutputBytes {
		return fmt.Errorf("max_output_bytes must be between 1 and %d", maxMaxOutputBytes)
	}

	root := cfg.ProjectRoot
	if root == "" {
		root = "."
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(baseDir, root)
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("project_root %q: %w", cfg.ProjectRoot, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return fmt.Errorf("project_root %q: %w", cfg.ProjectRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project_root %q is not a directory", cfg.ProjectRoot)
	}
	cfg.Root = real

	if len(cfg.Checks) == 0 {
		return errors.New("no checks defined")
	}
	seen := make(map[string]bool)
	mandatory := 0
	for i := range cfg.Checks {
		c := &cfg.Checks[i]
		if err := c.validate(); err != nil {
			return fmt.Errorf("check #%d (%q): %w", i+1, c.ID, err)
		}
		if seen[c.ID] {
			return fmt.Errorf("check #%d: duplicate id %q", i+1, c.ID)
		}
		seen[c.ID] = true
		if !c.Optional {
			mandatory++
		}
	}
	if mandatory == 0 {
		return errors.New("at least one check must be mandatory (not optional)")
	}
	return nil
}

func (c *Check) validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("id is required")
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > maxTimeoutSeconds {
		return fmt.Errorf("timeout_seconds must be between 1 and %d", maxTimeoutSeconds)
	}
	if c.ExpectExit < 0 || c.ExpectExit > 255 {
		return errors.New("expect_exit must be between 0 and 255")
	}

	set := map[string]bool{
		"path":                c.Path != "",
		"paths":               len(c.Paths) > 0,
		"text":                c.Text != "",
		"command":             len(c.Command) > 0,
		"tool":                c.Tool != "",
		"rules":               len(c.Rules) > 0,
		"ignore_paths":        len(c.IgnorePaths) > 0,
		"min_duplicate_lines": c.MinDuplicateLines != 0,
		"expect_exit":         c.ExpectExit != 0,
		"output_contains":     c.OutputContains != "",
		"timeout_seconds":     c.TimeoutSeconds != 0,
	}
	var allowed []string
	switch c.Type {
	case FileExists:
		if c.Path == "" {
			return errors.New("file_exists requires path")
		}
		allowed = []string{"path"}
	case TextContains:
		if c.Path == "" || c.Text == "" {
			return errors.New("text_contains requires path and text")
		}
		allowed = []string{"path", "text"}
	case CommandExitCode:
		allowed = []string{"command", "expect_exit", "output_contains", "timeout_seconds"}
	case TestSuite:
		allowed = []string{"command", "output_contains", "timeout_seconds"}
	case DependencyAudit:
		if _, ok := auditTools[c.Tool]; !ok {
			return fmt.Errorf("dependency_audit requires tool to be one of: %s", strings.Join(auditToolNames(), ", "))
		}
		allowed = []string{"tool", "path", "command", "timeout_seconds"}
	case CodeScan:
		allowed = []string{"path", "rules", "ignore_paths", "min_duplicate_lines"}
		if err := c.validateScan(); err != nil {
			return err
		}
	case ManualReview:
		if strings.TrimSpace(c.Description) == "" {
			return errors.New("manual_review requires description")
		}
		allowed = []string{"paths"}
		for _, p := range c.Paths {
			if err := checkRelPath(p); err != nil {
				return fmt.Errorf("paths: %w", err)
			}
		}
	case "":
		return errors.New("type is required")
	default:
		return fmt.Errorf("unknown type %q", c.Type)
	}
	for _, name := range allowed {
		delete(set, name)
	}
	for name, isSet := range set {
		if isSet {
			return fmt.Errorf("field %q does not apply to type %s", name, c.Type)
		}
	}

	if c.Path != "" {
		if err := checkRelPath(c.Path); err != nil {
			return err
		}
	}
	if c.Type == CommandExitCode || c.Type == TestSuite || (c.Type == DependencyAudit && len(c.Command) > 0) {
		if len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
			return fmt.Errorf("%s requires a non-empty command array", c.Type)
		}
	}
	return nil
}

func (c *Check) validateScan() error {
	for _, r := range c.Rules {
		if !knownScanRules[r] {
			return fmt.Errorf("unknown rule %q (known: duplicate_code, go_unused, go_unused_export, secret, syntax, todo_marker)", r)
		}
	}
	for _, p := range c.IgnorePaths {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("ignore_paths: bad pattern %q: %w", p, err)
		}
	}
	if c.MinDuplicateLines != 0 {
		if c.MinDuplicateLines < minDupLines {
			return fmt.Errorf("min_duplicate_lines must be at least %d", minDupLines)
		}
		if len(c.Rules) > 0 && !ruleOn(c.Rules, ruleDuplicate) {
			return errors.New("min_duplicate_lines needs the duplicate_code rule")
		}
	}
	return nil
}
