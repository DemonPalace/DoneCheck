# Reference

Commands, configuration, check types, statuses and exit codes.

## Install

There are no prebuilt binaries; the scripts build from this checkout and need Go 1.25 or newer.

```
./install.sh                                               # Linux/macOS, installs to ~/.local/bin
DONECHECK_INSTALL_DIR=/usr/local/bin ./install.sh          # a different directory
powershell -ExecutionPolicy Bypass -File .\install.ps1     # Windows
.\install.ps1 -NoPath                                       # Windows, without editing the user PATH
go install .                                               # without the scripts
```

The scripts run `donecheck help` to confirm the build works and tell you if the install
directory is not on your `PATH`. `install.sh` prints the line to add to your shell profile;
`install.ps1` adds the directory to your user `PATH` unless you pass `-NoPath`.
`install.sh` was tested on Linux only; `install.ps1` has not been run on Windows.

## Commands

```
donecheck init [dir]       add .skill/, AGENTS.md and a starter donecheck.json
donecheck check [flags]    run checks, print a report (scripts and CI)
donecheck tui [flags]      run checks in an interactive terminal UI
donecheck install [flags]  install missing vulnerability scanners that donecheck.json needs
```

| Flag | Applies to | Meaning |
|------|-----------|---------|
| `-config path` | check, tui, install | Config file. Default `donecheck.json`. |
| `-allow-commands` | check, tui | Allow checks to run commands (tests, scanners, syntax checkers, git). Without it they are SKIPPED or unverified. |
| `-confirm id` | check, tui | Record that a human confirmed a `manual_review` check. Repeatable. |
| `-format text\|json\|markdown`, `-o file` | check | Report format, and a file to write it to. |
| `-watch=false` | tui | Do not re-run when files change. |
| `-yes` | install | Do not ask before running the install commands. |

`donecheck init` creates, only if they do not already exist, `.skill/` with
short skills for agents (anti-AI-slop rules, how to verify with DoneCheck, test
first, systematic debugging, self-review), `AGENTS.md` and a
starter `donecheck.json`. It never overwrites a file. The starter config always
has `code-scan` and `human-review`, plus checks that can give a real result for
this project: a test command only if tests exist (a real `scripts.test`, `*_test.go`
files, pytest files), and a dependency audit only if the lockfile it reads is
there (Go: `govulncheck`; Node with `package-lock.json`: `npm audit`; Rust,
Python, Maven: `osv-scanner`). Other ecosystems get nothing. The skills are
instructions only; nothing forces an agent to follow them except that
`donecheck check` reports the truth when run.

## Configuration

JSON. Unknown fields, and fields that do not apply to a check's type, are
rejected. `project_root` is relative to the config file.

```json
{
  "project_root": ".",
  "default_timeout_seconds": 60,
  "max_output_bytes": 16384,
  "checks": [
    {"id": "readme", "type": "file_exists", "path": "README.md"},
    {"id": "tests", "type": "test_suite", "command": ["go", "test", "./..."], "output_contains": "ok", "timeout_seconds": 300},
    {"id": "code-scan", "type": "code_scan", "ignore_paths": ["*_test.go", "testdata"]},
    {"id": "deps", "type": "dependency_audit", "tool": "govulncheck", "path": "go.mod"},
    {"id": "review", "type": "manual_review", "description": "A person read the diff", "paths": ["internal/auth"]},
    {"id": "changelog", "type": "file_exists", "path": "CHANGELOG.md", "optional": true}
  ]
}
```

## Check types

| Type | Fields | VERIFIED when |
|------|--------|---------------|
| `file_exists` | `path` | The path exists inside the project root. |
| `text_contains` | `path`, `text` | The file contains `text` (literal, case-sensitive; files over 10 MiB are INCONCLUSIVE). |
| `command_exit_code` | `command`, `expect_exit`, `output_contains`, `timeout_seconds` | The command exits with `expect_exit` (default 0). Needs `-allow-commands`. |
| `test_suite` | `command`, `output_contains`, `timeout_seconds` | The command exits 0. Needs `-allow-commands`. A runner that reports there is nothing to run (npm `Missing script`, pytest "no tests ran") is INCONCLUSIVE with advice, not FAILED. |
| `dependency_audit` | `tool`, `path`, `command`, `timeout_seconds` | The scanner reports no known vulnerabilities. Needs `-allow-commands`. |
| `code_scan` | `rules`, `ignore_paths`, `min_duplicate_lines`, `path` | The scan of the project's files finds nothing. |
| `manual_review` | `description`, `paths` | A person passes `-confirm id` (or presses `c`). |

`command` is an argument array, never a shell string, and runs in the project root.

### `code_scan`

Reads every text file under the project (or under `path`) and applies rules.
Default rules: `syntax`, `secret`, `duplicate_code`, `go_unused`. Opt-in:
`go_unused_export`, `todo_marker`.

- **`syntax`**: files that do not parse. DoneCheck parses Go, JSON, JSONL/NDJSON and
  XML/SVG itself (`tsconfig` and `.vscode/` files, which allow comments, are skipped).
  Python, JavaScript, PHP, shell and Ruby use the interpreter's parse-only mode
  (`python3 ast.parse`, `node --check`, `php -l`, `bash -n`, `ruby -c`), which needs
  `-allow-commands`; the code is never run. A file whose syntax could not be checked
  is reported as unverified and the check is INCONCLUSIVE, never a pass. YAML and TOML
  are not checked.
- **`secret`**: private key headers and tokens of known formats (AWS access key IDs,
  GitHub, Slack, `sk-` keys, JWTs). Generic `password = ...` assignments are not
  flagged. Test fixtures with realistic-looking tokens are flagged too; ignore them
  (below). The secret itself is never written to a report.
- **`duplicate_code`**: runs of at least `min_duplicate_lines` (default 20) meaningful
  lines that appear in more than one place, shown with the first lines of the copy and
  where else it is. Blank lines, comments, lone punctuation, data rows (only strings and
  numbers) and prose are not counted, and one line repeated many times is not a block.
  Data, config and documentation files (`.json .csv .sql .xml .yaml .toml .md .txt` and
  similar) are skipped for this rule.
- **`go_unused`**: unexported Go functions, types, constants and variables that no
  non-test code in the package uses.
- **`go_unused_export`** (opt-in): exported Go identifiers that no other package uses,
  so they could be private. Meant for applications, not libraries.
- **`todo_marker`** (opt-in): `TODO`, `FIXME`, `XXX`, `HACK`, "not implemented".

Not scanned: `.git`, `node_modules`, `target`, `.venv`, `__pycache__`, `vendor`,
`.skill`, lock files, minified files and source maps, backup files (`.bak`,
`.orig`, `.rej`, `.swp`, `.tmp`, `file~`), binary files, files over 1 MiB, and
symlinks. A scan of more than 5,000 files or 64 MiB is INCONCLUSIVE with advice
to narrow it, never a partial pass. Findings are capped at 1,000.

**Ignoring.** Three ways, from narrowest to widest: put `donecheck:ignore` on a
line; add `ignore_paths` patterns to the check (matched against the path, the
file name and every parent directory); or list entries in `.donecheckignore`,
one per line, `path-pattern [rule ...]`, for example `src/legacy.py duplicate_code`.
In the TUI, `i` on a file with findings adds it after you confirm. Findings
hidden by `.donecheckignore` are counted in the result, and a change to that
file is flagged for human review, so an ignore cannot be added silently.

### `manual_review`

Stays INCONCLUSIVE until a person confirms it. The result says why a human is
needed and what to confirm. With `paths` it lists those files. Without, and with
`-allow-commands`, DoneCheck asks `git` (read-only) which files changed and lists
only those a machine cannot vouch for, each with the reason: dependency
manifests, CI definitions, DoneCheck's own config and the ignore file, agent
instructions, build scripts, credential files, binary files, test files that
shrank, large deletions, and files no `code_scan` covers. Ordinary source files a
`code_scan` covers are only counted. Untouched `donecheck init` files are left out;
edited ones are listed.

### `dependency_audit`

DoneCheck has no vulnerability database and makes no network calls itself; it
runs a scanner and reads its exit code.

| `tool` | Default command | FAILED when | Notes |
|--------|-----------------|-------------|-------|
| `govulncheck` | `govulncheck ./...` | exit 3 | Only vulnerabilities your code can reach fail; others are in the evidence. |
| `osv-scanner` | `osv-scanner -r .` | exit 1 | Exit 128 (no packages) is INCONCLUSIVE. Syntax differs between versions; override with `command`. |
| `npm` | `npm audit --audit-level=high` | exit 1 and the output mentions vulnerabilities | Needs a lockfile. |

A scanner that is missing, offline, times out or exits unexpectedly is
INCONCLUSIVE. FAILED lists advisory IDs (CVE, GHSA, GO, RUSTSEC, PYSEC). If a
supported scanner is missing, `donecheck install` (or `I` in the TUI) shows the
exact `go install` command and runs it only after you agree; `~/go/bin` is searched
even when it is not on `PATH`. `govulncheck` and `npm` were run against real
projects; `osv-scanner` has never been run against a real project.

## Statuses and exit codes

- `VERIFIED`: evidence gathered in this run supports the claim.
- `FAILED`: the evidence contradicts the claim.
- `INCONCLUSIVE`: the check could not complete (timeout, cancellation, scanner
  missing, unverified syntax, unconfirmed manual review). The `error` field says why.
- `SKIPPED`: the check did not run (commands not allowed, run cancelled).

Only VERIFIED counts. The project is **fully verified** only if there is at least
one mandatory check and every mandatory check is VERIFIED; `"optional": true`
checks are reported but do not affect the result.

| Exit code | Meaning |
|-----------|---------|
| 0 | Fully verified |
| 1 | A mandatory check FAILED |
| 2 | Usage error, or missing or invalid configuration |
| 3 | No mandatory failure, but not fully verified |
