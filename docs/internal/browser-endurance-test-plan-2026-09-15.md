# Browser endurance acceptance — 2026-09-15

User requirement: continuous mixed mouse movement, scrolling, clicks and keyboard input for at least 20 minutes, with failures investigated, corrected and retested. Work remains on browser-improvements targeting release/v0.1.1; no main, Mac installed runtime, location/resource or tool-policy changes.

The principal oracle is an authored interactive website whose trusted DOM events update exact counters and text, encoded visibly in its canvas. The viewer reads those counters from the received video. Expected values derive from the authored gestures, not transport acknowledgements. Mouse hover may coalesce, but clicks, key actions, scroll deltas and final released state must remain exact. No automatic retries, reconnects, Resume clicks, skipped failed rounds or shorter-duration pass are allowed in the acceptance run.

Run one 20-minute workload with a mix of mouse sweeps, wheel bursts in both directions, clicks, repeated arrow key down/up, native keyboard typing and committed Unicode, plus drags. Keep text bounded by explicitly deleting the previous known text. Assert exact state after every round, monitor all viewer alerts/paused states and page errors during the run, and retain timestamps, counts, route encoding, peer states and screenshots. Require all four user-requested event categories throughout all 20 one-minute intervals. Periodically change viewer size and revalidate geometry. Setup time is excluded from the 20-minute interval; a failed run must remain failed even if retried later.

Complement the endurance run with targeted regressions: slow new-tab attach must not block an existing tab, cancellation/session replacement must fence publication, compatible slow scroll continuation must preserve exact deltas within the active dispatch deadline, and meaningful action/held-state boundaries must preserve safe ordering. Fault variants must demonstrate that the assertions can fail. The tests do not certify arbitrary websites or guarantee zero future faults.

Use the existing Amsterdam test agent and authorized static preview fixture. New runtime candidates must be built from committed source and installed/running binary hashes verified before final acceptance. Focused relevant suites only, per user instruction; no repeated full CI.

## Baseline and candidate evidence

The baseline on 3f978959c failed after 104 active seconds at the first resize: four repeated arrow-key presses were suppressed before transport. This remains a regression case. Candidate changes defer resizing during held input/composition, move first popup attachment outside the existing-tab command gate, and preserve one compatible scroll continuation within the existing active dispatch budget. Focused frontend 21 tests, popup admission 4 tests, queue/wheel tests and gateway dedicated-input tests pass. Three deliberate fault variants for each fix were caught, with restored source passing. Independent frontend and backend review found no blockers. Full 1200-second live acceptance remains required.

## Amsterdam candidate 14e00bc17

Installed/running binary and unchanged machine configuration verified. Slow-wheel live regression passed: 21 authored events produced exactly 210 scroll units; the server completed one merged 5-event dispatch in 1231.5 ms and the remaining 16 in 15.52 ms. Input stayed ready; subsequent click and Unicode text matched exactly, with no held state or fixture errors. Initial attempt failed before scrolling because the test checked transport readiness without checking the visible waiting-picture status; the corrected test uses the same full readiness condition as endurance and retains the failed attempt. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/wheel-continuation-14e00bc17-ready/`. The 1200-second endurance run failed after 99.27 active seconds; no acceptance claim. All 8 key repeats survived the first resize boundary, but the deferred resize started just before the next wheel burst and suppressed every wheel before transport. The viewer advertised ready before that scheduled transition. Next fix makes this readiness handover explicit and waits for fresh matching geometry before reporting ready, retaining the existing test.

## Explicit resize handover

A pending resize now withdraws input readiness as soon as the held gesture finishes, before the scheduled viewport operation can race with the next gesture. It shows a resizing status until the exact accepted control acknowledgement identifies a fresh capture that has actually been displayed. This reuses the backend geometry validation, including legitimate scrollbar differences. Old frames and unrelated acknowledgements cannot restore readiness. Real input failures remain visible, and an unfinished handover produces an error after 15 seconds. Paste and composition finish admission before the handover starts.

The 23 focused frontend tests pass. Three mutations—removing acknowledgement correlation, removing immediate handover, and extending the timeout—were caught. Full live endurance must be repeated on the next verified build; the test is unchanged.

## Candidate 9b372a50b running verification

The installed and running binary hash is862291c07764f876f2e7ea253f370d7825f1872def48d00ed4db129334127b93. Amsterdam machine configuration is preserved. The 20-minute run is underway and has passed its first resize plus subsequent key/scroll rounds. This is progress, not acceptance until the entire run and its assertions finish.

Related dedicated-input/recovery tests passed (18 tests); frame-generation tests passed (14 tests) after the negotiated-transport test stub gained the existing cancelAutomaticRecovery method. No production behavior or assertions were changed for that test-fixture repair.

## 9b372a50b failure and next diagnosis

The run failed at 527.56 active seconds, after 61 complete rounds. All 30 final wheel inputs reached the server and completed by00:02:39.255 UTC. The video was still advancing through older scroll states around00:02:43, ending60 units short at the unchanged five-second assertion. This proves a failed visible-response check; it does not prove permanent input loss. Input queue wait was at most161ms for that burst. The delayed stage after Chrome acknowledgement remains unproven.

A separate confirmed defect caused a400ms resizing notice after ordinary focus changes with unchanged geometry. The next frontend fix deduplicates geometry before deferral and again before releasing a held gesture. Its26 tests and mutation check pass.

The next run records receiver buffer/decode/RTT statistics per round and a bounded recent frame-presentation timeline. Continuous host CPU/steal/cgroup samples will cover the whole run. A120-second diagnostic with resizing every two rounds can test whether repeated capture replacement reproduces the slowdown sooner; this cannot satisfy the20-minute acceptance requirement. Default acceptance remains1200 seconds with resizing every12 rounds and unchanged exact-result/five-second checks.
