# Browser stability candidate

The maintainer approved a bounded stability candidate on 2026-09-09. Work remains on `browser-improvements`, targeting `release/v0.1.1` through draft PR 685. No merge, release, or installed-app replacement is authorized by this candidate checkpoint.

## Candidate scope

Complete the remaining new-tab reporting and input-cancellation corrections, and repair the failing test cleanup and agent test where their causes are established. Preserve input ordering, current-picture/current-tab safeguards, owned key/button releases, capture retirement, and recovery behavior. No tool-policy or discovery source changes. Testing-agent configuration remains allowed.

Defer further video-speed experiments until the candidate is available. This does not waive the existing latency target or declare speed acceptance: variable video delay remains documented in the input timing report. A stability candidate is for controlled hands-on testing, not release readiness.

## Validation

Use relevant tests per correction and independent review, then one consolidated automated check of the combined candidate. Validate the same frozen source revision on Amsterdam Linux and the isolated Mac gateway. Record platform-specific binary hashes; binaries cannot be byte-identical across operating systems.

Exercise sustained exact ordered input, tab changes, resize, reconnect/recovery, and audio/video continuity. Report every unsupported or unverified scenario. Record latency separately without weakening its assertions or relabeling a failed speed test as a complete pass. The installed Mac instance on port 10994 stays untouched; isolated testing uses port 11094.

## Initial blockers from revision 1dcd32ca9

- Two real-Chrome click tests omit the expected new-tab report despite a successful click. Investigate the ordering between passive tab adoption and click reconciliation.
- The input wait-budget test dispatched zero commands but reported cancellation instead of the caller's deadline. Preserve the correct caller reason without weakening the original deadline or cancellation safety.
- The catalog refresh test failed while removing its temporary directory. Its background refresh loops use an uncancelled context; test cleanup must cancel and join them before filesystem removal. Runtime catalog behavior is outside this correction.
- The agent end-to-end test passed only after retry. The failed trace must establish the cause before changing an assertion; the retry guard remains intact.

Detailed timing evidence and measurement limitations: [input timing report](https://github.com/elicify-ai/omnipus/blob/browser-improvements/docs/internal/browser-input-timing-2026-09-09.md).

## Focused correction evidence

The click correction snapshots the original session, opener, and known targets before input. It reports a newly adopted popup even when passive adoption finishes before reconciliation, while excluding existing or unrelated targets. The public reconciliation behavior remains unchanged. Both passive schedules were reproduced deterministically. After three deliberate guard faults were caught and restored, 15 related test groups passed with race detection (29 pass records, no skips or race warnings), including both originally failing real-Chrome cases.

Caller deadline expiry is now preserved when its cancellation relay wins the target-derived timer. Unrelated browser errors remain intact, and the original deadline assertion is unchanged. The restored affected tests recorded 60 passes, with a further 20 race-tested repetitions of the original failing case; no skips or races. Deliberate faults exercised caller reason, unrelated-error preservation, and live-caller behavior.

Four test-owned catalog refresh loops now cancel and join before temporary-file cleanup. The four affected groups passed three repetitions with race detection. This is a fixture lifecycle correction, not a runtime catalog change.

The delegation visibility test now requires a matching successful child completion and parent completion, and checks the same completed card after toggling verbose output. It no longer assumes exactly one prose message. Sixteen focused cases, three fault checks, TypeScript checking, and independent review passed. Separate live attempts stopped during authentication or agent-picker setup before delegation; they do not verify this integration. The consolidated automated run must establish that result.

Independent reviews found no remaining high-confidence issue in these corrections. Evidence is retained under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/stability-candidate/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/stability-candidate/`.

## Post-freeze test-harness correction

The consolidated run on runtime candidate `32de1e610` passed all checks except the hot-reload end-to-end job and the aggregate gate reflecting that failure. The previously failing browser, caller-budget, catalog, delegation, and Settings checks passed. The remaining helper attempted to read a Fetch response twice when JSON parsing failed, hiding the original status/body. Its original non-JSON response cause was not retained in CI and is not claimed as diagnosed.

Both hot-reload helpers now read the body once, then parse JSON or retain the raw text. Status checks, response assertions, timeouts, and retries are unchanged. Eight real HTTP response cases and three deliberate faults passed their intended checks; both actual hot-reload tests passed against the frozen Mac candidate without retries. Independent reviews passed. This follow-up changes test code only; the runtime candidate remains the same on both platforms.

The Mac candidate passed ten short live checkpoints, including exact input state, held-key release across tabs, resize, socket reconnect, and input after reconnect. It recorded no page errors and a live audio track; audible content is unverified. The first Mac soak attempt stopped before measurement on a `localhost` versus configured `127.0.0.1` preview-origin mismatch; the corrected configuration entered the timed idle phase. Both full 20-minute runs and the Linux short UI check are pending at this checkpoint.
