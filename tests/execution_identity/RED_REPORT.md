# Execution-identity RED — blocked acceptance, partial test pack

**Correction:** said `TestSteerCanceller_Revive_NewGeneration_OldMarkerInert` in the draft; wrong because the source declares `TestRevive_NewGeneration_OldMarkerInert`; correct is the latter. Impact: citation only; test sources and results are unchanged.

**Instrument correction:** the first final skip scan used an over-broad `t\.Skip` expression and matched `report.SkippedTerminal`, a result field. A corrected call-pattern scan detected its positive controls and found no skips, global-hook names, manual generation assignments or raw lifecycle-persistence calls in either new test file (exit 0). This was a static-check false positive, not another Go-test failure; no test source changed.

**The requested same-generation stale-Stop corruption proof is BLOCKED.** The locked narrow run compiled and executed six top-level tests: five failed, one newer-Stop positive control passed. The failures establish missing production prerequisites and an actual retained cascade lock, not delayed old effects corrupting a same-generation replacement. Production code is unchanged; GREEN remains held.

Code correct and tested: **not established for T27**; prerequisites/lock behaviour were exercised on the pre-change production code.

Reachable by a user or agent: **not established for the new workflow**; the real RESUME used here still changes generation, so it cannot construct T27's required same-generation replacement.

## Source and execution receipt

| Item | Value |
|---|---|
| Baseline/work branch | `6aa93802b923692d10a22b5ff3906ee0e4b14229`, `fix/execution-identity` |
| Spec | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md`, `cd20cf8b365e7bc8a010c731a8f6830e14397c23`, D2/D4/D5/D7/T27 |
| Test cases | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/execution_identity_t27_test.go` |
| Fixture | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/execution_identity_t27_fixture_test.go` |
| Plan written before assertions | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/tests/execution_identity/RED_PLAN.md` |
| Raw run log | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/tests/execution_identity/evidence/t27-red-6aa93802b.log` |
| Direct exit receipt | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/tests/execution_identity/evidence/t27-red-6aa93802b.exit` = `1` |
| Source hashes, unchanged after run | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/tests/execution_identity/evidence/t27-red-source.sha256` |
| Runtime | Named package result `FAIL github.com/elicify-ai/omnipus/pkg/agent 19.892s`; six root tests plus two nested stop assertions executed |

One local test process ran. The runner was `lockf -t 1800 /tmp/omnipus-gotest.lock env CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -run '^TestExecutionIdentityT27_' -count=1 -v -timeout 3m`; its one selected package resolves to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/`. Output was redirected and `$?` captured directly before any parsing. Named test failures, not a compiler/setup error, produced exit 1. No local full suite, race-detector run, GREEN implementation or mutation probe was performed.

## Observed cases

All test symbols below are in the test-cases file listed above.

| Test symbol | Expected from spec | Observed on baseline | Result |
|---|---|---|---|
| `TestExecutionIdentityT27_DurableIdentityBeforeQueuedAdmission` | Persist complete execution identity before queued admission | Real queued lifecycle journal has no `execution_id`; loud `BLOCKED` fatal | RED |
| `TestExecutionIdentityT27_DurableIdentityBeforeActiveAdmission` | Persist complete execution identity before real active provider call | Real running lifecycle journal has no `execution_id`; loud `BLOCKED` fatal | RED |
| `TestExecutionIdentityT27_NeverRanStopLandsStoppedWithCauseNote` | Non-terminal `stopped`, spent fence, durable cascade note | Independent subtests observe terminal `cancelled` and missing `stop_note` | RED (both subtests) |
| `TestExecutionIdentityT27_ExplicitResumeKeepsGenerationAndReplacesRun` | Same-generation RESUME; distinct run, same boot, cleared note/effect | Real RESUME changes generation `1 -> 2`; loud `BLOCKED` fatal before later identity assertions | RED |
| `TestExecutionIdentityT27_CascadeReleasesLockBeforeRealLiveCancel` | Release owning cascade/lifecycle locks before live callback | Cascade lock still held at forwarding callback; lifecycle lock free; real provider observes `context.Canceled` | Behavioural RED |
| `TestExecutionIdentityT27_NewerStopCancelsReplacementPositiveControl` | A genuinely newer Stop still reaches current replacement | Real resumed instruction reaches provider; newer Stop fences generation 2, cancels provider, hard-aborts handle and releases admission after joined disposal | PASS, generation-changing control only |

The positive control was not observed RED first. It is an existing-behaviour control, not an implementation GREEN claim; its mutation proof is deferred to fresh CHECK. Distinct run/unchanged boot/cleared effect assertions exist but were **not reached** after the generation mismatch, and are not reported verified.

The identity assertions inspect real journal snapshots after admission/provider entry. They detect absent metadata on this baseline; they do not independently prove the future persist-before-admission locking order.

## Effect-boundary coverage

| Required boundary/property | What this pack actually exercises | Stale same-generation replacement isolation |
|---|---|---|
| `SteerGenerationCancel` queue removal | Real Stop removes selected old queued entry; real RESUME queues replacement separately | **Not covered** — no delayed old callback released after replacement |
| `cancelDelegatedSubtree` second queue-removal caller | Not invoked by this pack | **Not covered** |
| Graceful `Interrupt` | Not invoked by this pack | **Not covered** |
| Hard escalation timer / `InterruptSessionHard` | Immediate generation-cancel callback reaches real provider/turn in lock test and positive control; timer and hard API not invoked | **Not covered** |
| Never-ran Stop finalization | Real cascade/deliverer/store land the wrong terminal state and omit note | **Not covered** for stale finalization; prerequisites RED |
| Late live-turn completion/Stop mutation | Real disposal is joined during cleanup/control; no replacement-overlapping finalization barrier | **Not covered** |
| Second-pass callback retry | No deliberately failed real dependency followed by second-pass retry | **Not covered** |
| Pinned immutable execution handle | Actual current live turn is observed in control, but there is no old pinned-handle/replacement interleaving | **Not covered** |
| No locks across interrupt/wait | Real cascade-lock probe fails; lifecycle lock is free at that one callback entry | **Partial** — registry synchronization and lock retention across interrupt/wait not proved |
| Distinct run/current boot; replacement state/note/receipt; historical-notice dedup | Identity prerequisites fail; no historical notice or control receipt assertions | **Not covered** |
| Genuinely newer Stop | Effective current replacement cancellation observed, generation `1 -> 2` | **Partial positive control**, not same-generation acceptance |

This pack does **not** meet the requested minimum of a stale queue-removal race plus graceful interruption plus the positive control. It preserves the blocked requirements as loud tests instead of forcing lifecycle state or fabricating an execution identity.

## Findings and implementation needs

| Severity / certainty | Failure scenario | Evidence |
|---|---|---|
| High / Verified (high confidence) | Real stopped-child RESUME cannot create a same-generation replacement: old Stop lands terminal `cancelled`; RESUME increments generation; no durable execution id or stop note exists | Five `BLOCKED` failure sites in raw receipt; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/steer_cancel.go::SteerCanceller.Revive`, `::AgentLoop.terminaliseNeverRanStop`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/session/lifecycle.go::LifecycleRecord` |
| High / Verified (high confidence) | The real live-cancel callback is invoked while the cascade lock is retained, violating D2/D7. If the callback blocks during interruption/wait, concurrent cascade operations cannot acquire that lock | Named behavioural RED; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/steer_cancel.go::SteerCanceller.cascade` retains `defer lock.Unlock()` across `fireLiveCancels` |
| Testability need / Verified static (high confidence) | Exact stale never-ran/late-turn proof cannot wrap the proposed ordinary shared lifecycle mutation dependency on this baseline: both owners directly use the concrete LifecycleStore | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/steer_cancel.go::SteerCanceller`, `::AgentLoop.reportSteeredSessionTerminalUpward`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/steer_completion.go::AgentLoop.deliverSteeredCompletion` |
| Integration note / Inferred (high confidence) | Implementing new stopped/same-generation semantics will conflict with existing tests pinning terminal cancellation and generation-bumping revival; reconcile against the ADR, never weaken tests merely for green | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/pkg/agent/steer_cancel_test.go::TestSteerGenerationCancel_NeverRanChildUnblocksParent`, `::TestRevive_NewGeneration_OldMarkerInert` |

No production or existing test symbol was edited. GitNexus cannot resolve this checkout; the pre-commit detect-changes attempt exited 1 with `Repository ... execution-identity ... not found`, not a clean graph verdict. Receipt: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/execution-identity/tests/execution_identity/evidence/t27-gitnexus-detect-changes.txt`. Direct caller search matches only the two new test files, so expected impact is test-only **Inferred**, not graph-verified.

GREEN remains held per dispatch. Completing T27 needs the production stopped/same-generation identity transitions and ordinary real-store/cancellation seams, then a production-wired delayed-effect pack. Fresh independent CHECK owns skill step 4 items 2/3 and the proof-of-failability mutation checklist. No CHECK verdict is issued by this RED author.

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| No competing matching process at original preflight | `pgrep -fl execution-identity` -> exit 1/no output; later matching monitor PID 90091 had cwd outside assigned worktree, verified with `lsof -a -p 90091 -d cwd` -> exit 0 | Verified (high) |
| Runnable RED, no setup/compiler masquerade | Locked tagged named group -> raw exit 1; six root RUN lines, five root FAIL lines, one root PASS line; five BLOCKED messages plus retained-cascade-lock assertion | Verified (high) |
| Real current-replacement Stop still effective | Raw log's named positive-control PASS and `actual generation 1 -> 2`; test checks exact RESUME instruction, provider cancellation, live hard-abort, durable current fence and joined slot release | Verified (high); same-generation claim not made |
| Source tested is unchanged | SHA-256 fixture `7b1bbf4a17db1910cb93dc5d8889796d49cf901d1c60c315076af81171531b93`, test `b7f68c9ca2d431d480c5936549be824aee1f40909ccb09b2efd051f5a9e09d75`; compared after run -> both UNCHANGED | Verified (high) |
| Test-only scope and narrow size | Staged name-status contains only new test-owned files; new-symbol caller sweep -> exit 0, two test files only; test sources 244/205 lines and function-span upper bounds 50/45 lines; staged diff check -> exit 0 | Verified (high) for scope; impact Inferred |
| Final static instrument corrected | Call-pattern scan -> exit 0; each positive control detected, zero skip/global-hook/manual-generation/raw-persistence matches in both files; initial broad pattern had falsely matched `report.SkippedTerminal` | Verified (high), scoped static check only |
| Human identity available | `gh api user` -> exit 0, Daniel Piatkowski, `daniel-piatkowski-ai`, id 10800669; configured no-reply email matches that id/login; `git var GIT_AUTHOR_IDENT` and `git var GIT_COMMITTER_IDENT` both confirm it | Verified (high) |
| Exact T27/remaining gates not verified | Boundary table records missing stale interleavings; no full suite, race-detector, implementation GREEN or mutations run | Unknown for unexecuted requirements |
| Self-check | Re-read the staged fixture/test/plan/report diff against D2/D4/D5/D7/T27; checked the corrected legacy-test citation, named raw outcomes and source hashes (`shasum -c`, exit 0); repeated scoped caller search, corrected static instrument controls and staged whitespace check (exit 0). Outside-pack diff exits 0. The requested stale-effect acceptance and its minimum coverage are unmet and remain BLOCKED; GREEN/mutations/CHECK are deferred | Verified (high) |
