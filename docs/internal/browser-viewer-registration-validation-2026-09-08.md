# Original attachment ownership in viewer registration and command results

The request-aware viewer registry entry retains the original attachment context,
capture pointer, and public capture ID. Registration verifies the original chat
route and committed capture against the dispatch epoch under attachment then
WebRTC state locks. Invalid registration cannot replace an existing route. A
capture stop racing publication removes only the entry just installed.

Input failures, command parse failures, control outcomes, and tab-action errors
now retain the original attachment context through outbound delivery. Operation
completion does not cancel a current result; replacement or clearing of its
attachment does.

## Verification

- Root `72062` exited 1 with nine intended registration failures against the
  original registration behavior: lost original identity and eight rejected-origin
  cases were reproduced. Package time was 5.580 seconds.
- Root `20870` exited 1 after the registration fix. Registration cases passed;
  four actual command-handler paths reproduced errors still deliverable after
  attachment replacement. Package time was 3.704 seconds.
- The relevant initial green in driver `32316` passed 16 tests/subtests with no
  failures or skips, 6.900 seconds, after command-result wiring.
- Driver `32316` exited 0. Both deliberate faults were caught: allowing an old
  offer to overwrite registration, and replacing original command-result contexts
  with a connection-independent context (all four handler regressions failed).
- Restored relevant tests passed with `-race -shuffle=on`: 16 tests/subtests,
  zero failures, zero skips, no race warnings, 10.774 seconds. No unrelated suite
  or full CI was run for this change.

The registration API received a bounded independent review of its identity and
lock ordering. GitNexus rated the registry entry type LOW (one direct caller,
three affected symbols, no indexed process). New context handlers and the new
registration helper were absent from the index; their direct wrappers, queue,
and intended request-aware handler caller were traced manually.

These checks cover registry admission and actual command-handler output. The
request-aware WebRTC handler and health fan-out consume the registry API in
subsequent integration work; this commit alone does not prove that production
integration or end-to-end browser input.

Only relevant tests run locally. Full CI remains a final integrated-branch gate.
Retained driver and output:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-viewer-and-command-mutations.py`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-viewer-and-command-mutations.log`
