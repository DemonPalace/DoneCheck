// Command donecheck verifies work against evidence.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"donecheck/internal/scaffold"
	"donecheck/internal/tui"
	"donecheck/internal/verify"
)

const usage = `donecheck verifies work against evidence instead of trusting claims.

Usage:
  donecheck init [dir]     add agent skills (.skill/), AGENTS.md and a starter donecheck.json; never overwrites
  donecheck check [flags]  run checks and print a report (for scripts and CI)
  donecheck tui [flags]    run checks in an interactive terminal UI
  donecheck install [flags] install missing vulnerability scanners that donecheck.json needs (asks first)

Flags for check and tui:
  -config path       config file (default donecheck.json)
  -allow-commands    allow command_exit_code and test_suite checks to run commands
  -confirm id        mark a manual_review check as human-confirmed (repeatable)

Flags for install only:
  -config path       config file (default donecheck.json)
  -yes               do not ask for confirmation (the commands are still printed)

Flags for tui only:
  -watch=false       do not re-run checks automatically when files change (default: watch)

Flags for check only:
  -format fmt        text, json or markdown (default text)
  -o file            write the report to file instead of stdout

Exit codes: 0 fully verified, 1 a mandatory check failed, 2 usage or config error,
3 not fully verified (inconclusive or skipped checks).
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

type commonFlags struct {
	config  string
	allow   bool
	confirm stringList
}

func (f *commonFlags) register(flags *flag.FlagSet) {
	flags.StringVar(&f.config, "config", scaffold.ConfigName, "config file")
	flags.BoolVar(&f.allow, "allow-commands", false, "allow commands to run")
	flags.Var(&f.confirm, "confirm", "confirm a manual_review check by id")
}

func (f *commonFlags) load(stderr io.Writer) (*verify.Config, verify.Options, bool) {
	cfg, err := verify.LoadConfig(f.config)
	if err != nil {
		fmt.Fprintf(stderr, "donecheck: %v\n", err)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintln(stderr, "Run `donecheck init` to create a starter donecheck.json, or pass -config.")
		}
		return nil, verify.Options{}, false
	}
	opts := verify.Options{
		AllowCommands: f.allow,
		Confirmed:     map[string]bool{},
		SkipReview:    func(rel string) bool { return scaffold.Pristine(cfg.Root, rel) },
	}
	manual := map[string]bool{}
	for _, c := range cfg.Checks {
		if c.Type == verify.ManualReview {
			manual[c.ID] = true
		}
	}
	for _, id := range f.confirm {
		if !manual[id] {
			fmt.Fprintf(stderr, "donecheck: -confirm %q: no manual_review check with that id\n", id)
			return nil, verify.Options{}, false
		}
		opts.Confirmed[id] = true
	}
	return cfg, opts, true
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return verify.ExitUsage
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "check":
		return runCheck(ctx, args[1:], stdout, stderr)
	case "install":
		return runInstall(ctx, args[1:], stdin, stdout, stderr)
	case "tui":
		return runTUI(ctx, args[1:], stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "donecheck: unknown command %q\n\n%s", args[0], usage)
		return verify.ExitUsage
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("donecheck "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func runInit(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("init", stderr)
	if err := flags.Parse(args); err != nil {
		return parseExit(err)
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "donecheck init: at most one directory argument")
		return verify.ExitUsage
	}
	dir := "."
	if flags.NArg() == 1 {
		dir = flags.Arg(0)
	}
	actions, err := scaffold.Init(dir)
	for _, a := range actions {
		if a.Created {
			fmt.Fprintf(stdout, "created  %s\n", a.Path)
		} else {
			fmt.Fprintf(stdout, "skipped  %s (already exists, left untouched)\n", a.Path)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "donecheck init: %v\n", err)
		return verify.ExitUsage
	}
	fmt.Fprintln(stdout, "\nNext: run `donecheck check` (add --allow-commands to run the commands in donecheck.json).")
	return 0
}

func runInstall(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := newFlagSet("install", stderr)
	config := flags.String("config", scaffold.ConfigName, "config file")
	yes := flags.Bool("yes", false, "do not ask for confirmation")
	if err := flags.Parse(args); err != nil {
		return parseExit(err)
	}
	cf := commonFlags{config: *config}
	cfg, _, ok := cf.load(stderr)
	if !ok {
		return verify.ExitUsage
	}
	missing := verify.MissingTools(cfg)
	if len(missing) == 0 {
		fmt.Fprintln(stdout, "Nothing to install.")
		return 0
	}

	reader := bufio.NewReader(stdin)
	code := 0
	for _, m := range missing {
		cmdline := strings.Join(m.Install, " ")
		fmt.Fprintf(stdout, "%s (check %q) is not installed.\nInstall command: %s\n", m.Binary, m.CheckID, cmdline)
		if !*yes {
			fmt.Fprint(stdout, "This downloads and builds code from the internet. Run it? [y/N] ")
			answer, _ := reader.ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
				fmt.Fprintln(stdout, "skipped")
				code = 1
				continue
			}
		}
		out, err := verify.RunInstall(ctx, m.Install, cfg.MaxOutputBytes)
		if out != "" {
			fmt.Fprintln(stdout, out)
		}
		if err != nil {
			fmt.Fprintf(stderr, "donecheck install: %v\n", err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "installed %s\n", m.Binary)
	}
	return code
}

func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("check", stderr)
	var cf commonFlags
	cf.register(flags)
	format := flags.String("format", "text", "text, json or markdown")
	outPath := flags.String("o", "", "write report to file")
	if err := flags.Parse(args); err != nil {
		return parseExit(err)
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "donecheck check: unexpected argument %q\n", flags.Arg(0))
		return verify.ExitUsage
	}
	write := map[string]func(verify.Report, io.Writer) error{
		"text":     verify.Report.WriteText,
		"json":     verify.Report.WriteJSON,
		"markdown": verify.Report.WriteMarkdown,
	}[*format]
	if write == nil {
		fmt.Fprintf(stderr, "donecheck check: unknown -format %q (use text, json or markdown)\n", *format)
		return verify.ExitUsage
	}
	cfg, opts, ok := cf.load(stderr)
	if !ok {
		return verify.ExitUsage
	}

	report := verify.Run(ctx, cfg, opts)

	var buf bytes.Buffer
	if err := write(report, &buf); err != nil {
		fmt.Fprintf(stderr, "donecheck check: writing report: %v\n", err)
		return verify.ExitUsage
	}
	if *outPath == "" {
		if _, err := stdout.Write(buf.Bytes()); err != nil {
			fmt.Fprintf(stderr, "donecheck check: writing report: %v\n", err)
			return verify.ExitUsage
		}
		return report.ExitCode()
	}
	if err := os.WriteFile(*outPath, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "donecheck check: writing report: %v\n", err)
		return verify.ExitUsage
	}
	fmt.Fprintf(stdout, "report written to %s (%d verified, %d failed, %d inconclusive, %d skipped)\n",
		*outPath, report.Summary.Verified, report.Summary.Failed, report.Summary.Inconclusive, report.Summary.Skipped)
	return report.ExitCode()
}

func runTUI(ctx context.Context, args []string, stderr io.Writer) int {
	flags := newFlagSet("tui", stderr)
	var cf commonFlags
	cf.register(flags)
	watch := flags.Bool("watch", true, "re-run checks when files in the project change")
	if err := flags.Parse(args); err != nil {
		return parseExit(err)
	}
	cfg, opts, ok := cf.load(stderr)
	if !ok {
		return verify.ExitUsage
	}
	report, err := tui.Run(ctx, cfg, opts, *watch)
	if err != nil {
		fmt.Fprintf(stderr, "donecheck: %v\n", err)
		return verify.ExitUsage
	}
	return report.ExitCode()
}

func parseExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return verify.ExitUsage
}
