# Agent instructions

This project uses DoneCheck. Before you start, read:

- `.skill/anti-ai-slop/SKILL.md` for the engineering rules.
- `.skill/donecheck/SKILL.md` for how to verify your work.
- `.skill/test-first/SKILL.md` when adding features or fixing bugs.
- `.skill/systematic-debugging/SKILL.md` when something fails.
- `.skill/self-review/SKILL.md` before you declare work done.

Do not tell the user a task is done until `donecheck check` exits 0. If it does
not, report exactly what failed, was inconclusive, or was skipped. Never edit
`donecheck.json` to make a check pass.
