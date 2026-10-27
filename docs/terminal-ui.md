# Terminal UI

`donecheck tui` needs a terminal of at least 50x17. From the top: a header, a bar
of **Agents** found in other terminal tabs, a status banner, then **Files** and
**Checks** side by side, a **Detail/Preview** pane and a **Command** line.

- **Realtime.** Checks run on start and again when files change (polled each
  second, `-watch=false` or `w` turns it off). Files the checks write themselves
  do not trigger a rerun. While `code_scan` runs, the tree first shows every
  mapped file as `·`, then checks them one by one (spinner on the current file, `✓`
  when done); folders summarise their contents. This follows real progress
  events from the scan, not a timer.
- **Files.** Folders expand and collapse. Names are green, yellow or red by the
  checks that concern them; folders take the worst status inside, so you can follow
  red down to the file. Files whose syntax was not checked are dim. Symlinks are
  listed, never followed.
- **Preview.** Up to 2 MiB and 100,000 lines per file, drawn only for the rows in
  view. Syntax-coloured (`NO_COLOR` turns it off), control characters stripped, secrets
  masked, PEM private keys hidden. Offending lines are red with a `▌` marker. A
  **Connections** block lists the checks that name the file, its findings (with the
  duplicated code), and the files it mentions or that mention it. A mention is a
  token equal to another file's path, from the root or relative to the file; language
  imports are not parsed.
- **Jump and edit.** `x` / `X` jump to the next or previous problem, opening the file
  at that line. Clicking a finding does the same. `e` opens the file in `$VISUAL` or
  `$EDITOR` at that line (vim, nano, emacs, micro: `+N`; VS Code and forks: `-g`;
  helix, zed, sublime: `file:N`; no shell is involved) and re-runs the checks afterwards.
- **Buttons** in the banner, each asking for confirmation with `y`: **A** allow
  commands for this session, **I** install a missing scanner, **F** send a fix request
  to an agent. Mouse clicks can open the prompt but never answer it.
- **Command line** (`:`): `check`, `fix`, `install`, `allow`, `next`, `edit`,
  `ignore <path> [rule]`, `cancel`, `watch [on|off]`, `confirm <id>`, `unconfirm <id>`,
  `clear`, `help`, `quit`. These are DoneCheck commands; it is not a shell, and there
  is no embedded terminal.

Keys: Tab / Shift+Tab switch pane, arrows or j/k move, PgUp/PgDn scroll, Enter or
Right expand, `1`-`9` pick an agent, `r` rerun, `w` watch, `c` confirm a review,
`q` cancel a run or quit. Mouse: click to select, expand and focus; wheel to move or
scroll; click an agent chip to select it. The process exit code follows the table above.

## Fixing with an AI agent

DoneCheck looks for coding agents in other terminal tabs of the same project:
**KDE Konsole** (through its D-Bus interface, using `busctl`) and **tmux**. A session
counts when its foreground process is `codex`, `gemini`, `aider`,
`opencode`, `goose`, `cursor-agent` or `amp` (also when run through node or python),
and its working directory is inside the project. Set `DONECHECK_AGENTS=my-agent,other-tool` for
other process names. The tab DoneCheck runs in is never a target. After you confirm,
DoneCheck types one line of English into the chosen tab and presses Enter. The line
names only check IDs and statuses; command output and file contents are never
forwarded, because they are untrusted. Other terminals offer no safe way to type into
another session, so no button appears there. Detection was checked read-only against
a live Konsole; sending has not been tried on a live tab.
