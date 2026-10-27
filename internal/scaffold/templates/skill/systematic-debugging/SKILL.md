---
name: systematic-debugging
description: Find the root cause of a bug or failing check before changing code. Use when a test, build, command, or DoneCheck check fails or behaves unexpectedly.
---

# Systematic debugging

Do not guess and edit. Work in this order.

1. **Reproduce.** Get the exact failing command and its full output. If you
   cannot reproduce it, you cannot claim to have fixed it.
2. **Read the evidence.** Read the whole error, not the first line. Note the
   file, line, and values involved.
3. **Form one hypothesis** that explains all of the evidence. Say what you
   would observe if it were true and what if it were false.
4. **Test the hypothesis** with the smallest probe: a print, a focused test, a
   narrower command. Change one thing at a time.
5. **Fix the cause, not the symptom.** Trace the bad value back to where it was
   produced. Patching where it surfaced hides the bug.
6. **Prove the fix.** Re-run the original failing command and see it pass. Add
   a regression test (see `test-first`). Run the wider suite for collateral damage.

## If it is still failing

After two failed fixes, stop and re-read the evidence; your model of the
problem is probably wrong. After three, tell the user what you have ruled out
and what you still do not understand. Do not keep stacking speculative changes.

## Never

- Delete or weaken a check, test, or assertion to make a failure disappear.
- Add retries or sleeps to cover a race you have not understood.
- Swallow an error so a run looks clean.
- Say "fixed" without having re-run the thing that failed.
