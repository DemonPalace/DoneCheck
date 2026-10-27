# Security model and limitations

- Commands run as an argument array through `exec.CommandContext`, never a shell,
  each with a timeout and an output limit. DoneCheck runs only the programs a
  feature needs: configured commands, `git` (read-only), the interpreters in
  parse-only mode, `go install` for supported scanners, `tmux` and `busctl`.
- Commands run only with `-allow-commands` (or `A` in the TUI), a flag and not a
  config setting, so a config in an untrusted repository cannot turn execution on.
  Read `donecheck.json` first: `["npm", "test"]` runs whatever the project defines.
- Paths must be relative; `..` and symlink escapes are rejected or INCONCLUSIVE.
- Reports and previews redact common secret shapes; this is best-effort pattern
  matching and misses unknown formats. Output and file text are untrusted: control
  characters are stripped before display.
- Inspecting the repository is read-only. DoneCheck writes only the report you name
  with `-o`, new files from `init`, and a line appended to `.donecheckignore` after you
  confirm.
- Sending a fix request types into another process. It asks every time, targets only
  sessions inside the project, validates the session identifiers, and sends a fixed
  template with sanitized check IDs. The agent can still act on the request as it likes.

Known limitations:

- A path can be swapped for a symlink between being resolved and opened (TOCTOU).
- Commands inherit DoneCheck's environment and run with your privileges. There is no sandbox.
- On timeout the started process is killed; processes it spawned may keep running.
- Duplicate detection compares normalised lines, not syntax trees; renamed
  variables are not seen as duplicates. `go_unused` and `go_unused_export` work from
  names without type information, so reflection, `go:linkname` and generated
  registration can hide or fake a use.
- The `secret` rule knows token formats, not every credential.
- `-o` overwrites an existing file. Checks run one after another. The watcher polls.
- Agent features need Linux with Konsole or tmux; they are not available on Windows.
