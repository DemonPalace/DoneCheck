---
name: test-first
description: Write a failing test before the code that makes it pass. Use when adding a feature, fixing a bug, or changing behavior.
---

# Test first

1. **Red.** Write one test that describes the behavior you want. Run it. It must
   fail, and for the right reason (the behavior is missing, not a typo or a
   compile error). A test you never saw fail proves nothing.
2. **Green.** Write the least code that makes that test pass. Run it. Run the
   rest of the suite too.
3. **Refactor.** Clean up with the tests green. Do not add behavior here.

For a bug fix, first write a test that reproduces the bug and fails. Then fix it.
The test stays as a regression guard.

## What makes a test worth keeping

- It asserts observable behavior, not how the code is written internally.
- It covers a failure path: bad input, missing file, timeout, cancellation.
- It uses temporary directories and controlled fixtures, never the network or
  paths that exist only on your machine.
- It would fail if the feature were broken or deleted. If you cannot say how it
  could fail, delete it.

## Do not

- Write the implementation first and then tests that mirror it.
- Assert constants, or only that a call does not crash.
- Change an assertion to match wrong output instead of fixing the code.
- Skip, comment out, or loosen a failing test to get a green run.
