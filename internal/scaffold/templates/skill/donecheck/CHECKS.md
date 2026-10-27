# DoneCheck reference

## Statuses

| Status | Meaning |
|--------|---------|
| VERIFIED | Evidence gathered during this run supports the claim. |
| FAILED | The evidence contradicts the claim (file missing, text absent, wrong exit code). |
| INCONCLUSIVE | The check could not be completed: timeout, cancellation, unreadable or oversized file, command that could not start, path that escapes the project root, or a `manual_review` nobody has confirmed. The `error` field says why. |
| SKIPPED | The check did not run (commands not enabled, or the run was cancelled). |

Only VERIFIED counts toward "fully verified". The project is fully verified
only when there is at least one mandatory check and every mandatory check is
VERIFIED. Checks marked `"optional": true` are reported but do not affect the
result.

## Check types

| Type | Fields | Verifies |
|------|--------|----------|
| `file_exists` | `path` | The path exists inside the project root (file or directory). |
| `text_contains` | `path`, `text` | The file contains `text` (case-sensitive, literal). Files over 10 MiB are INCONCLUSIVE. |
| `command_exit_code` | `command`, `expect_exit` (default 0), `output_contains`, `timeout_seconds` | The command exits with the expected code. |
| `test_suite` | `command`, `output_contains`, `timeout_seconds` | The test command exits 0. Use `output_contains` to guard against a run that executed no tests. |
| `dependency_audit` | `tool` (`govulncheck`, `osv-scanner` or `npm`), optional `path` (the manifest, so it is highlighted in the TUI tree), `command` (override), `timeout_seconds` | The scanner reports no known vulnerabilities. Findings are FAILED (advisory IDs in the reason); a scanner that errors, is missing or is offline is INCONCLUSIVE. Needs `--allow-commands`; the scanner usually needs network access. |
| `code_scan` | `rules`, `ignore_paths`, `min_duplicate_lines`, `path` | No findings in the project's files. Default rules: `syntax`, `secret`, `duplicate_code`, `go_unused`; opt-in: `go_unused_export`, `todo_marker`. Unverifiable syntax leaves it INCONCLUSIVE. |
| `manual_review` | `description`, `paths` | Stays INCONCLUSIVE until a human confirms it (`--confirm <id>` or `c` in the TUI). |

Common fields: `id` (required, unique), `type` (required), `description`,
`optional`. Fields that do not apply to a check's type are rejected.

`command` is an argument array, never a shell string. It runs in the project
root. Example: `["go", "test", "./..."]`.

Top-level settings: `project_root` (relative to the config file, default `.`),
`default_timeout_seconds` (default 60), `max_output_bytes` (default 16384; the
last bytes of output are kept).

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Fully verified |
| 1 | At least one mandatory check FAILED |
| 2 | Usage or configuration error |
| 3 | No mandatory failure, but not fully verified |

## Example

```json
{
  "checks": [
    {"id": "readme", "type": "file_exists", "path": "README.md"},
    {"id": "tests", "type": "test_suite", "command": ["go", "test", "./..."], "output_contains": "ok"},
    {"id": "review", "type": "manual_review", "description": "A human has read the diff"}
  ]
}
```

## Ignoring findings

A finding can be hidden with `donecheck:ignore` on its line, `ignore_paths` on
the check, or an entry in `.donecheckignore` (`path-pattern [rule ...]`). Hidden
findings are counted in the result, and a change to `.donecheckignore` is sent to
a human for review.

