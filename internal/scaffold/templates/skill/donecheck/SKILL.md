---
name: donecheck
description: How to verify that a task is actually done using the DoneCheck CLI. Use before saying a task is complete, finished, fixed, or passing, and whenever the user asks whether work is done.
---

# DoneCheck: verify, do not assume

DoneCheck checks claims about this project against observable evidence. Your
own statement that work is done is not evidence. Only DoneCheck output is.

The checks live in `donecheck.json` at the project root. See `CHECKS.md` in
this folder for check types, statuses, and exit codes.

## Required procedure

1. Finish the work and run the project's own formatter, vet/lint, and tests.
2. Run DoneCheck from the project root:

   ```
   donecheck check
   ```

   Commands in `donecheck.json` (`command_exit_code`, `test_suite`) are
   SKIPPED unless you pass `--allow-commands`. Read `donecheck.json` first;
   pass `--allow-commands` only if you have confirmed every command in it is
   one you are comfortable running in this repository.

   ```
   donecheck check --allow-commands
   ```

3. Read the whole output. Act on the exit code:

   | Exit | Meaning | What you may say |
   |------|---------|------------------|
   | 0 | Every mandatory check VERIFIED | "DoneCheck: fully verified." |
   | 1 | A mandatory check FAILED | The work is not done. Fix the cause, run again. |
   | 2 | Usage or config error | Fix the invocation or config; nothing was verified. |
   | 3 | Nothing failed, but not everything is VERIFIED | The work is not verified. Say which checks are INCONCLUSIVE or SKIPPED and why. |

4. In your final message, quote the status line and list every check that is
   not VERIFIED. Never summarize an exit code 3 as "passed".

## Rules you must not break

- Do not declare a task done unless `donecheck check` exits 0, or you state
  clearly that it did not and why.
- Do not edit `donecheck.json` to make a failing, inconclusive, or skipped
  check go away: no deleting checks, loosening `text`, raising timeouts to hide
  a hang, or marking a check `optional` just to pass. You may add checks, or
  change a check only when the user asked for it or the check itself is wrong;
  say so explicitly when you do.
- Never pass `--confirm` for a `manual_review` check. Human review exists
  because it needs a human. Leave it INCONCLUSIVE and tell the user what to
  review.
- Fix a `code_scan` finding (a leaked key, a syntax error, duplicated code, unused
  code) in the code. Do not hide it with `donecheck:ignore`, `ignore_paths` or
  `.donecheckignore` unless the user asked for that exact ignore.
- A vulnerable dependency reported by a `dependency_audit` check is a real
  failure: upgrade or replace the dependency. Do not add an ignore, delete the
  check, or mark it `optional` to get past it.
- A timeout is not a pass. A skipped check is not a pass. Missing evidence is
  not a pass.
- Do not fabricate or paraphrase evidence. If you quote output, quote it as
  printed.
- If you cannot run DoneCheck (not installed, no permission), say that; do not
  substitute your own judgment for its result.

## Useful commands

```
donecheck check                       # text report, exit code per table above
donecheck check --format json         # machine-readable report
donecheck check --format markdown -o report.md
donecheck tui --allow-commands        # interactive view for a human
```
