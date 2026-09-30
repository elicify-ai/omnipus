# Short independent check — round-four control-plane fixes

**R4-MAJ-001: SOUND. R4-MAJ-002: SOUND — as written design.** The two corrections supply the missing execution targeting and legal post-terminal delivery path. No correction-introduced contradiction was found in the relevant founder rulings or the checked ADR sections. This is not an implementation pass or another full review round.

**Date:** 2026-09-30

**Status:** founder-ordered two-fix check complete; runtime proof remains unverified

**Reviewed HEAD:** `cd20cf8b365e7bc8a010c731a8f6830e14397c23`, branch `docs/adr-cp-grill-r4`

**Correction commits:** `0c6d11d5ff080864898804f2823d4e1e32bb7fad` and `3bd153edae4f6bdab13b3abbb8cc3c2be1166005`

## Scope and references

| Reference | Artifact / scope |
|---|---|
| ADR | [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md)::D2, D4, D5, D7, D8, Impact, T11 and T27; directly related founder, vocabulary and D6 consistency checks |
| Original findings | [Round-four independent review — The sub-agent control plane](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/docs/internal/architecture/ADR-20260928-sub-agent-control-plane-review-round4.md)::R4-MAJ-001 and R4-MAJ-002, including both Required proof paragraphs |
| Founder authority | [Founder plan](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-2026-09-28.md)::2026-09-29 session states/stop model and round-two answers; 2026-09-30 train/spec authority |
| Method | Read both correction commits with `git show`, the current scoped requirements and the source symbols below. GitNexus exposed no tools/resources; direct source reads and scoped searches were used. No graph impact result, runtime test, build or CI result is claimed. |

## R4-MAJ-001 — SOUND

| Required field | Assessment |
|---|---|
| Failure scenario checked | An old Stop/Stop-all/timeout effect is delayed after its fence. The selected turn lands stopped; explicit RESUME queues or starts replacement work in the same generation and boot. The old effect must not remove that admission, cancel its provider call or land the replacement stopped. |
| Evidence | ADR::D2 execution identity/effect-boundary paragraphs, D4 internal targeting, D5 newer-action ordering, D7 corrected mechanism, Impact cancellation row and T27; source-to-requirement mapping below. |
| Severity | Original **MAJOR** design gap resolved by the written correction; no remaining scoped finding. |
| Certainty | **Verified (high confidence):** source behavior and explicit requirements. **Inferred (high confidence):** the specified mechanism excludes the interleaving. Runtime behavior is **Unknown**; not exercised here. |

The identity is defined, not merely requested: `(session_id, generation, boot_seq, run_id)`. D2 persists it before admission and copies it into the queue entry and immutable live handle. Every replacement gets a fresh identity; queue promotion retains that admission's identity. Stop intent/fence records the selected identity plus the selecting Stop's `control_id`. Timeout is explicitly included in this same rule.

| Checked source site | Closing requirement |
|---|---|
| [Cancel source](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_cancel.go)::`SteerCanceller.cascade`, `cancelStamped` | D2/D7 replace generation-only callbacks with each node's stamped pair. Second-pass/retry work cannot retarget a replacement; traversal/stamping stays serialized, but the cascade lock is released before live effects. |
| [Cancel source](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_cancel.go)::`SteerCanceller.Revive` | D2/Impact replace the existing generation-bump protection for stopped RESUME with an atomic same-generation queued transition, fresh execution identity and clearing of old stop-effect metadata. Safety does not depend on Revive taking the cascade lock. |
| [Cancel source](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_cancel.go)::`AgentLoop.SteerGenerationCancel`; [Admission](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/admission.go)::`steerAdmission.removeQueuedSession`; [Delegate cancellation](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_delegate_cancel.go)::`AgentLoop.cancelDelegatedSubtree` | Both removal callers must compare the complete execution identity under the queue lock. D2 expressly forbids the existing preliminary session-wide removal. |
| [Turn registry](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/turn.go)::`AgentLoop.requestCancelForGeneration`; [Interrupts](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steering.go)::`Interrupt`, `InterruptSessionHard` | Match and pin only the immutable selected execution handle, then invoke it after releasing synchronization. No replacement turn-state/provider-cancel slot reuse or later session lookup; graceful and delayed hard cancellation obey the same boundary. |
| [Cancel source](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_cancel.go)::`terminaliseNeverRanStop`; [Completion](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_completion.go)::`AgentLoop.completeSteeredTurn` | D2 compares the producing execution inside the lifecycle mutation; stop finalization additionally matches the still-current stop control. A stale completion cannot commit against the replacement. Historical stop notices may finish delivery without rewriting its state/note/receipt. |

**T27 supplies an adequate proof prescription, not executed proof.** It fixes generation and boot, changes only the run identity, and requires separate queued and active replacement cases. It releases the delayed callback, graceful interrupt, hard escalation and never-ran/late-turn finalization, including both removal callers and second-pass retry. The negative instrument control deliberately blocks/fails the **real production dependency** before normal assertions; the newer-Stop positive control prevents passing by disabling cancellation altogether. A generation-only implementation would fail the stated replacement-survival assertions. T27 also requires pinned-handle targeting and no lifecycle/cascade/registry lock across interrupt or wait. No executed negative-control or mutation receipt is claimed.

## R4-MAJ-002 — SOUND

| Required field | Assessment |
|---|---|
| Failure scenario checked | Done/failed G commits with its final outbox. A publisher then tries to persist progress/retirement through another same-generation lifecycle mutation, which the real terminal guard rejects. If G+1 starts first, tail-only recovery could also hide G's pending final. |
| Evidence | [Lifecycle store](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/session/lifecycle.go)::`LifecycleStore.persistLocked`, `Mutate`, `tail`, `Load`; ADR::D2 legal post-terminal writes/protected commit/retirement/discovery, D4 storage row, D8.1/5/9, Impact real-store dependency and T11. |
| Severity | Original **MAJOR** design gap resolved by the written correction; no remaining scoped finding. |
| Certainty | **Verified (high confidence):** the current guard rejects same-generation terminal appends and the correction defines a different, bounded writer. **Inferred (high confidence):** that protocol preserves the committed winner while allowing delivery progress. Implementation and crash behavior are **Unknown**. |

| Check | Why the correction closes it |
|---|---|
| Legal writer | The proposed `LifecycleStore.UpdateFinalDelivery` appends typed `final_delivery_update` envelopes under the same store/session lock, **not** `LifecycleRecord` values through `persistLocked`. Ordinary same-generation terminal `Mutate`/`Persist` stay prohibited. Tail/list readers must distinguish the types. This is a specified new operation, not an assertion that the current store already supports it. |
| Immutable winner | Only `advance_progress` and `retire_payload` commands are accepted; callers cannot submit a replacement lifecycle record/outcome/payload. Historical commit identity, final ID, generation, producer, destination and payload hash remain protected. Progress records durable receipts, is monotonic and uses an expected revision to reject stale concurrent updates. |
| Safe retirement | Durable inbox append plus recorded frames/wake or matching durable acknowledgment precede retirement. A durable marker precedes atomic compaction. Only the internal outbox payload copy/redundant delivery revisions retire; outcome, commit/hash/replay identity and other generations remain. Failed writes/compaction stay visible and retryable. |
| G remains discoverable during G+1 | `ListPendingFinalDeliveries` scans every committed generation. Updating G never appends an old lifecycle state behind G+1. Boot retries G's delivery independently while stopping uncommitted working G+1; it does not run the child. |
| T11 reaches the real post-terminal phase | The shared production dependency explicitly includes the new update/discovery operations and real retirement/compaction. T11 requires terminal commit → durable progress → permitted retirement → reopen/retry at crash cuts, plus pending G during admitted G+1 and restart. It rejects changed outcome/failed reason/final identity/producer/destination/payload, missing commit, regression, stale revision and premature retirement, with protected commit/current tail unchanged. Deliberate failures must reach the **real terminal and post-terminal writers**, not a fake progress store or only the first commit. |

The existing [Lifecycle bridge](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/session/lifecycle_bridge.go)::`LifecycleMutator` alone is insufficient: it exposes only `Mutate`, while the real completion and Stop paths read above use the concrete store. Impact explicitly requires refactoring those production callers onto the shared dependency backed by that same real store. The extended T11 therefore addresses the review's required proof, rather than assuming a permissive fake makes the terminal guard disappear. Its execution remains outstanding.

## Founder and ADR consistency

**No contradiction introduced by either correction was found in the checked scope.** The founder plan's self-only Stop, downward Stop all, retained goals/questions, same plan-step sessions and fresh explicit post-boot RESUME remain intact in ADR::D2/D5/D6/D7/D8. Internal `run_id` does not create a new user-visible generation; retrying an old committed final does not resume a child. Internal duplicate-payload retirement does not delete a session or its transcript. The corrections do not introduce held-claim machinery into D6's immediate-refusal rule.

[An open conversation must keep the ability to delegate](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/docs/internal/architecture/ADR-093-open-conversation-must-keep-delegation.md)::“Invariants this ADR will not break” used the broader terminal-generation immutability wording. The corrected ADR expressly identifies and dates its bounded clarification in **Amends** and D2: the terminal outcome remains immutable; typed delivery metadata and restricted outbox retirement are separate. This is an explicit amendment, not a silent permission for generic terminal mutation. D4, D8 and Impact consistently use that operation.

No new product decision or founder question is needed for these two fixes. This check authorizes neither another review round nor landing.

## Limits

**Code correct and tested:** not established; T11/T27 were assessed as requirements, not executed tests.

**Reachable by a user/agent:** not established; no integrated control-plane invocation was performed.

Skills: `omnipus-shared-rules`, `gitnexus-exploring`, `commit-messages`.

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Fixed review baseline and both corrections inspected | `git rev-parse HEAD` exit 0 → `cd20cf8b365e7bc8a010c731a8f6830e14397c23`; `git show` for `0c6d11d5f` and `3bd153eda` each exit 0 → execution-fence and delivery-journal ADR diffs. Initial `git status --short` exit 0 → empty. | **Verified — high** |
| All listed delayed-effect sites are addressed | First-hand source symbols in the R4-MAJ-001 mapping; ADR::D2/D4/D5/D7/Impact. Scoped `rg` exit 0 finds the removal definition and both callers. Source-pin diff exit 0 has no changes in these cancellation/store files; positive-control diff finds the changed ADR. | **Verified requirements/source — high**; mechanism sufficiency **Inferred — high** |
| T27 contains meaningful negative and positive controls | ADR::T27 and original finding::Required proof: real-dependency fault control, same-generation/same-boot queued and active replacements, delayed effects, newer Stop, lock discipline. Acceptance-row lookup exit 0 → `acceptance_row_lookup=2/2` for T11/T27. | **Verified prescription — high**; execution **Unknown** |
| New delivery path is bounded without weakening generic terminal guard | Lifecycle store::`persistLocked` rejects `prev.Terminal() && rec.Generation == prev.Generation`; `Mutate` calls it. ADR::D2/D4 defines separate typed envelopes, protected identity, retirement and all-generation discovery; D8/Impact agree. | **Verified source/design — high**; implementation **Unknown** |
| T11 explicitly covers real-store post-terminal legality and recovery | ADR::T11/Impact and original finding::Required proof; Lifecycle bridge::`LifecycleMutator`, Completion::`AgentLoop.completeSteeredTurn`, Cancel source::`SteerCanceller.stampStop`, and [Upward deliverer](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-cp-grill-r4/pkg/agent/steer_audience.go)::`SteerUpwardDeliverer.Deliver` read first-hand. | **Verified prescription/source — high**; execution **Unknown** |
| Relevant product decisions remain unchanged; prior invariant clarification is explicit | Founder plan::session states/stop model/round-two answers; ADR::Founder decisions, Amends, D2/D4/D5/D6/D7/D8; prior delegation ADR::terminal-generation invariant. | **Verified text — high**; consistency conclusion **Inferred — high** |
| Commit identity is the human's GitHub identity | `gh api user` exit 0 → Daniel Piatkowski, `daniel-piatkowski-ai`, ID `10800669`; configured no-reply email matches `10800669+daniel-piatkowski-ai@users.noreply.github.com`. | **Verified — high** |
| **Self-check** | Reread the staged report against both findings/Required proof, every named effect site, founder consistency and design-only limits. Python inventory exit 0 → two complete four-part verdicts, existing absolute evidence links, skills and final Self-check; negative fixtures missing a verdict, failure scenario or Self-check were all detected. `git diff --cached --check` exit 0; staged name/status exit 0 → only this new report. Target-ADR diff against `cd20cf8b3` exit 0 → unchanged. These are document/scope checks, not runtime acceptance. | **Verified — high**, report only |
