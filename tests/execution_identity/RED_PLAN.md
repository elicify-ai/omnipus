# Execution-identity RED plan

## Authority and scope

- Work branch: `fix/execution-identity`, baseline `6aa93802b923692d10a22b5ff3906ee0e4b14229`.
- Spec: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md`, revision `cd20cf8b365e7bc8a010c731a8f6830e14397c23`, D2/D4/D5/D7/T27. Every T27 mention was located and read before any test edits.
- Behaviour: an older Stop must never remove or cancel replacement work, even when session, generation and boot are identical; a newer Stop must still work.
- Ownership: new test files and test-plan/report/evidence artifacts under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/tests/execution_identity/` only. No production edits, global test hooks, fake lifecycle store, artificial generation reset or manually fabricated stopped/resumed records.

## Production boundary

Real AgentLoop, SteerLauncher, SteerCanceller, admission gate, lifecycle journal, unified session/transcript store, inbox and SteerUpwardDeliverer. A controlled provider is the only mock: the paid network edge is held until released or cancelled. Construction-injected GenerationCancelFunc may be wrapped to pause and then forward to the real AgentLoop.SteerGenerationCancel; it must not replace that operation with a fake result.

## Confirmed prerequisites missing on the baseline

`SteerCanceller.Revive` increments the generation for every stopped/terminal resume. `terminaliseNeverRanStop` writes terminal `cancelled`, not non-terminal `stopped`. Lifecycle/admission/turn records have no execution identity, boot identity or stop-effect metadata. The shared ordinary lifecycle mutation dependency described by the ADR is not present: SteerCanceller and AgentLoop use a concrete LifecycleStore directly. The exact T27 interleaving therefore cannot currently be constructed through a production same-generation RESUME.

A dispatcher question was raised before proceeding with any altered race scenario: permit current-generation-changing-path races as explicitly partial evidence, or stop at loud prerequisite tests. No scope choice was approved. The conservative pack contains only independent prerequisites, the requested cascade-lock boundary assertion and the requested newer-Stop positive control. It does not substitute a generation-changing stale-effect race or claim complete T27 proof.

## Case derivation

| Case | Input/order | Expected value/invariant | Oracle | Baseline disposition |
|---|---|---|---|---|
| Durable identity, queued | One real active provider occupies the sole slot; dispatch a child | Journal stores `execution_id {session_id, generation, boot_seq, run_id}` before the child is queued | D2/D4/T27 | Loud BLOCKED if absent |
| Durable identity, active | Dispatch a child and wait for its real provider call | Journal stores the complete execution identity for this admission | D2/D4/T27 | Loud BLOCKED if absent |
| Landed stop | Dispatch a queued child; invoke real Stop cascade and upward delivery | State `stopped`, non-terminal, no current Stop fence, durable `stop_note` | D2/D7/T27 | Loud BLOCKED if absent/wrong |
| Same-generation explicit resume | Stop a queued child through production; invoke real ReviveStoppedSession with a new instruction | Same session and generation; replacement queued, old fence/note cleared, distinct run identity | D2/D5/T27 | Loud BLOCKED if generation changes |
| No cascade lock at the live callback | Dispatch a real running child; wrap the construction-injected callback to probe the actual cascade lock and then forward to real SteerGenerationCancel | Cascade lock can be acquired at callback entry; real provider observes cancellation | D2/D7/T27 | Expected behavioural RED if the live callback retains the cascade lock; not proof of replacement isolation |
| Newer Stop positive control | Stop old child, explicitly resume it, admit replacement to a real provider, issue a newer Stop | Replacement provider cancelled, replacement turn hard-aborted, its admission released and current Stop fence effective | D5/T27 | Expected PASS; not proof of the missing same-generation race |

Boundary classes are ordering/state classes (queued versus active, before versus after replacement), not numeric-limit changes. Most cases are negative/prerequisite cases; the positive control prevents an always-no-op cancellation implementation from looking correct.

## Exact-race coverage still required

The eventual T27 pack must gate the actual production dependency after child fence commit and before each effect; land the selected child stopped; explicitly resume it in the same generation/boot; then verify fresh run identity, queued entry/active immutable handle, provider, note/state and receipts survive. Cover SteerGenerationCancel queue removal, cancelDelegatedSubtree queue removal, graceful Interrupt, hard escalation/InterruptSessionHard, never-ran/late-turn mutation, second-pass callback retry, historical-notice dedup and no lifecycle/cascade/registry lock across interrupt/wait. No prerequisite failure is proof that a delayed effect corrupted a replacement.

## What would break these assertions

Deferred to fresh CHECK, not run in RED: omit one execution-identity field; retain the old run identity on resume; increment a stopped session's generation; leave a current Stop fence on landed stopped state; discard its stop note; retain the cascade lock at the live callback; make cancellation a no-op on the current replacement. The positive control observes the provider's exact cancellation error and the real turn/admission state, so callback counts alone cannot pass it.

## Instrument and execution

- No timing-based logic assertions: channel signals for provider entry/cancellation and completion; deadlines are deadlock safeguards only.
- Directly captured local receipt, one process at a time, under `/tmp/omnipus-gotest.lock` using `lockf -t 1800`, `CGO_ENABLED=0`, tags `goolm,stdjson`, `-p 1`, one named execution-identity test group and one package.
- Verify named PASS/FAIL lines, not merely package exit status. Record setup/compiler errors separately; they do not establish RED.
- Full suite and GREEN are not run here. Skill step 4 items 2/3 and the proof-of-failability mutation checklist are deferred to independent CHECK.

## Impact

GitNexus is not indexed for this worktree; `gitnexus impact ReviveStoppedSession --direction upstream --include-tests` exits 1 and reports unrelated ambiguous registered repositories. Direct caller sweep is the fallback, not a graph result. No existing symbol is edited; changes are confined to new tests and their test-owned plan/report/evidence artifacts. Expected impact is test-only (Inferred).
