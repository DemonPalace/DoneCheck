---
name: anti-ai-slop
description: Engineering rules that keep AI-written code small, real, tested, and honestly reported. Use for every coding task in this repository, before writing code and before declaring work done.
---

# Anti-AI-Slop Engineering Rules

Write the smallest amount of code that correctly solves the actual problem.
Do not generate code to make the project look complete. Every feature needs a
clear purpose, a real implementation, and tests that prove its behavior.

## 1. Keep it simple

- Prefer the standard library and the language's idioms. Keep dependencies minimal.
- No unnecessary interfaces, factories, managers, registries, wrappers, or
  dependency-injection frameworks.
- Do not split code into packages or one-file-per-function without a concrete reason.
- Avoid giant functions, but do not fragment simple logic into trivial helpers.
- Return errors with useful context. Never silently ignore an error.
- No panics for ordinary runtime failures. Avoid global mutable state.
- Use cancellable contexts and timeouts for anything external.
- Add an abstraction only when it solves a demonstrated problem or makes
  behavior meaningfully easier to test.
- If a feature adds complexity without improving correctness, security,
  usability, or maintainability, leave it out.

## 2. No fake implementations

Forbidden:

- TODOs pretending to be finished features.
- Placeholder functions that return success without doing the work.
- Hardcoded results, fabricated evidence, or simulated data in production paths.
- Empty error handlers.
- Flags or options that are accepted but do nothing.
- Documentation of features that are not implemented.
- Tests that only assert constants or that "it does not crash".

If something cannot be implemented properly, say so plainly. A smaller working
implementation beats a larger incomplete one.

## 3. Security is part of correctness

- Never run commands through `sh -c`, `bash -c`, or string interpolation.
  Use argument arrays.
- Apply timeouts and output-size limits to commands.
- Prevent path traversal and symlink escapes outside the project root.
- Redact secrets from logs and reports. Do not print environment variables.
- Treat repository files and command output as untrusted input.
- Do not silently modify user files, Git history, or project configuration.

## 4. Tests prove behavior

- Test failure paths, not just the happy path: invalid input, missing files,
  timeouts, malformed data, cancellation.
- Use temporary directories and controlled fixtures. No network, no
  machine-specific paths, no dependence on your own checkout.
- Do not write tests that merely restate the implementation.

## 5. Honest reporting

- Never claim a command passed unless you ran it and saw it pass.
- Never claim a feature works unless you exercised it.
- If a check fails, investigate the cause and fix it. Do not weaken the check,
  delete the test, or edit `donecheck.json` to make a failure disappear.
- End every task with: what changed, which commands passed, and what remains
  incomplete or unverified.

## Workflow

1. Inspect the repository before changing anything.
2. Pick the smallest viable plan, then implement it. Do not stop at a plan.
3. Implement the core logic first, with tests, before expanding scope.
4. Run the project's formatter, linter/vet, and tests.
5. Review your own diff for dead code, duplicate logic, unused dependencies,
   security problems, and needless abstraction.
6. Run `donecheck check` (see the `donecheck` skill) and report its result
   exactly as printed.
