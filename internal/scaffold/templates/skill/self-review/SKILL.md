---
name: self-review
description: Review your own diff before declaring work done. Use after implementing a change and before running the final DoneCheck.
---

# Self-review

Read your full diff as if someone else wrote it. For each item, either it is
fine or you fix it.

**Scope**
- Does every changed line serve the request? Remove drive-by refactors,
  unrelated formatting, and speculative features.
- Is anything accepted but not implemented (flags, options, config fields,
  documented behavior)?

**Correctness**
- Is every error returned or handled with context? None ignored?
- What happens with empty input, a missing file, a timeout, cancellation?
- Could a failed or missing result be reported as success?

**Simplicity**
- Dead code, unused parameters, unused dependencies, duplicated logic?
- An abstraction with only one caller and no test benefit? Inline it.
- Comments that describe what the code does rather than why?

**Security**
- Commands built from strings, or run through a shell?
- Paths from input that could escape the project root?
- Secrets, tokens, or environment values that could reach logs or output?
- Untrusted text printed without sanitizing?

**Evidence**
- Did you run the formatter, vet/lint, and tests, and read the output?
- Do the docs describe what the code actually does now?

Then run `donecheck check` and report its result as printed.
