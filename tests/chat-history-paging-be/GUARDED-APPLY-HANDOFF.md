# Guarded repair and diagnostic-fault RED handoff

**Correction:** the initial evidence-refusal matrix was described as isolating each fault. That was wrong for semantic binding mutations because they also invalidated the receipt's binding-byte hash. The corrected matrix aligns that hash for semantic faults and leaves the dedicated hash-mismatch case unchanged; this strengthens the test, not production. The initial runs stopped before this matrix, so no passing decoder-refusal claim was established.

The initial extension has **9 selected runtime RED results and3 passing instrument controls** on test commit `7493ae1ef9449015de302a85a9555a692c90ce0b`. The final source includes the isolation correction. Its additional exact re-proofs are recorded with their actual SHA/raw exits at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/final-source-receipts.json`; absent that evidence they are UNVERIFIED. This is not GREEN, a CHECK verdict, full-suite execution or a claim that guarded apply/the two fault outcomes executed successfully.

## Revisions and type-level assumptions

| Item | Verified revision / boundary |
|---|---|
| Requested feature update | Normal fast-forward from `30aac609741c4ca54248bdd2eab94d1d0c515f5c` to `57ba831b651e4b5b18668999affd597d1dbfd760`, including spec amendment `3f3bfd1b9ab85e77ba2dd1c516581003764ae13a`. |
| Founder F1=A | D13/G is check-only, with operator-enforced quiet window, full verified backup and native evidence. No new maintenance lock/startup guard, exclusion claim, live apply or service action was added. The earlier open-F1 wording in the architect artifact is superseded by the spec amendment. |
| Backend-owned declaration | `688a866136c003ff41ec5be5aeb87cdd7b04f2ff`, normally merged at `3181eec9618c2165cfa522ea298345bf59d55475`. The exact `historyPageFaultEvent`, nil `historyPageFaultHook` and `errHistoryPageProtocol` are **type-level assumptions consumed by QA**, not implemented fault behavior. Their wiring, actual validators and classification remain GREEN deliverables. |
| QA test commits | `6c8b52e2d` adds the plan/native support; `7493ae1ef9449015de302a85a9555a692c90ce0b` adds direct typed fault and guarded CLI assertions. QA did not author production declarations or fixes. |
| Published initial tested ref | `origin/red/1266-be` at `7493ae1ef9449015de302a85a9555a692c90ce0b` was pushed and independently checked with `git ls-remote`, exit0. The later isolation correction changes only the semantic-refusal matrix's test source plus notes; its final-source receipt manifest names the exact new tested SHA. The original12 rows below remain bound to7493ae1ef, not relabelled as a newer run. |

The sole new non-`_test.go` helper is explicitly test-only source at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/testdata/native-instance/main.go`, as assigned by section L. It is not a product command or a stopped-state override.

## Source coverage

There are15 new named test functions, with additional case tables. Expected values come from L/M and D13/G/B30/B33/B35/B36/B44/B45/T22/T23/T25/T30, not observed command output.

| Requirement | Actual source / assertions |
|---|---|
| Native stopped fixture | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/native_fixture_test.go`::repairStartNative / repairStopNative: build a real native Go child, real daemon PID registration, exact READY PID handshake, stdin stop/crash, actual Wait/reaping. No sleep establishes readiness/exit. Failed capture cleanup kills only the owned child and always joins it; cleanup is not a stopped-proof oracle. |
| Binding and receipt producers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/native_evidence_support_test.go`::repairCapture / repairProduceReceipt and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/native_evidence_test.go`: exact flags, v1 member sets/types, non-null/unknown/trailing data checks, positive actual PID, canonical root/executable, native identity stability, exact PID/binding-byte SHA256, UTC observation times and actual binary VCS commit. Independent hi1 codec has frozen ASCII/Unicode controls. Running receipt, wrong executable and real birth-mismatch cases refuse without source/output writes. |
| Real CLI apply and repeat | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/guarded_apply_test.go`::TestHistoryRepairCommand_GuardedCLIApplyReadsAndRepeats: dry-run first, capture while running, actual clean/crash stop, fresh count-verified complete backup/manifest after Wait, stopped producer, actual apply with binding/receipt/confirmation, CLI verify, shared index verifier and exact forty-message real reader projection. Independent source/backup inventory and file facts; repeat keeps all index bytes/modes/mtime and occurrence tokens. Publication counts are checked semantically as published/unchanged/refused; key=value and JSON field presentation are accepted because D13 does not pin one serialization. |
| Refusal classes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/guarded_refusal_test.go` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/guarded_arguments_test.go`: missing confirmation/evidence; binding-as-receipt; exact-byte hash and instance/root/PID mismatches; unknown/null/version/trailing object; duplicate/unknown/incompatible/banned flags; unsafe source output; real new child after receipt; malformed/changed PID; byte-identical replacement root; canonical drift; actual exited PID/zero rejection. A backup made while running must be invalidated by real graceful PID removal, not silently accepted via a new PID-file exclusion. |
| Actual fault validators and diagnostics | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_fault_diagnostics_test.go`::pagingPreparedFaultDiagnostic: same real endpoint must first succeed without a hook; then the void hook changes only ProjectedBytes=-1 after_weigh, or actual HasEarlier=true/NextBefore=nil before_carrier_validate. Assert genuine errors.Is classification, reached count, sanitized fixed C6 read-error500, no partial success content, exactly one failed summary, actual positive selected reads, required counters/conditional fields and no canaries. Actual generated carrier/factory/logger are not replaced. |

## What actually ran

All12 commands ran serially through `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh`, with CGO_ENABLED=0, tags goolm,stdjson, count1, verbose named results and p1. Full commands, candidate SHA and raw exits are in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/receipts.json`.

| Selected test / BDD | Actual result, not intended later coverage | Raw Go exit | Log |
|---|---|---|---|
| Native fixture / B44, clean | Actual READY PID86321; stop; Wait0; PID removed; PASS. |0| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/native-clean-control.log` |
| Native fixture / B44, crash | Actual READY PID46899; crash; Wait86; stale PID bytes preserved; PASS. |0| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/native-crash-control.log` |
| Independent instance codec control | Both frozen vectors match the test-side codec; PASS. |0| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/instance-codec-control.log` |
| T22 capture / B44 | Actual command/helper builds0 and live READY. Capture command exits1 with named not-implemented, expected0. Owned child reaped in cleanup. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T22-native-capture.log` |
| T30 clean receipt / B35,B44 | Stops at valid capture: command1/not-implemented versus0. Receipt/clean-stop assertions in this feature test are not reached. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T30-clean-receipt.log` |
| T30 crash receipt / B35,B44 | Stops at valid capture: command1/not-implemented versus0. Receipt/crash assertions in this feature test are not reached. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T30-crash-receipt.log` |
| T25 checked apply / B30,B36 | Initial required dry-run exits1/not-implemented versus0. Apply/read/repeat are not reached. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T25-guarded-apply.log` |
| T30 unknown flag / B35 | Actual tool exits1/not-implemented versus required refusal2; no parser/refusal-class pass claimed. The live helper then actually stops/Wait0. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T30-strict-arguments.log` |
| T30 actual exited PID / B44 | Actual child READY/Wait86/reaped before tool invocation. Capture tool exits1/not-implemented versus required refusal2; no native-inspector pass claimed. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T30-actual-exited-pid.log` |
| T30 no prior binding / B35 | Receipt tool exits1/not-implemented versus required refusal2. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T30-missing-native-evidence.log` |
| T23 weight fault / B33 | Real logger instrument control succeeds; no-fault endpoint still returns legacy array, not generated envelope. Fails there; after_weigh mutation/validator/outcome are not reached. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T23-projection-error.log` |
| T23 carrier fault / B33 | Same legacy-array no-fault control failure; before_carrier_validate mutation/validator/outcome are not reached. |1| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/T23-protocol.log` |

A first collector was stopped by the harness's10-minute background limit while queued, before any test log/receipt existed; no owned wrapper remained. The same collector/candidate resumed with a longer supported tool limit. That interruption is **not** a test failure/pass: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/harness-interruption.json`.

## Remaining proof and execution rules

| Boundary | Honest status / next owner |
|---|---|
| Successful binding, stopped receipt, apply/read/repeat/refusal matrices | Source assertions added; actual successful path and later branches are UNVERIFIED until GREEN. An earlier not-implemented result cannot certify them. |
| Two real diagnostic faults | Typed tests compile and have runtime RED at the mandatory control. Actual hook invocation, errors.Is refusal and failed-summary assertions are UNVERIFIED until GREEN. |
| Native platforms | Only native macOS fixture controls executed here. Linux/Windows repair/native-observation certification and existing Windows T19/T26/T36 remain required CI evidence. A claimed changed birth is not proof of actual OS PID-number reuse/access-denied behavior. |
| Broader gates / resources | No full local suite, race, lint, contracts, SPA, e2e or Fly run in this extension. Founder routing is full node/go/e2e on performance080d74db170408, serialized, no resizing; spares are targeted few-spec reruns only. Check lock/run ownership and collect logs before the starter stops a machine. The older spare-only note is superseded. |
| Implementation/docs | Backend-lead owns all command/probe/schema/CI/wiring and DOC-4 user-facing documentation. QA's change is tests/support/notes; the merged nil declaration itself changes no user-visible behavior. No feature-complete, live-safety or reachability claim is made. |
| GREEN / independent CHECK | GREEN and subsequent real successful assertions are pending. Mutation probes, restored GREEN and the skill's Proof-of-failability checklist are **deferred to an independent fresh CHECK instance**; this author does not audit its own suite. |

## Critical mutations for fresh CHECK — not executed in RED

| Mutant | Test that must die |
|---|---|
| Disable the real weight nonnegative/max validator | T23 RealWeightFaultIsProjectionError; must not emit a successful page or summary. |
| Disable actual generated/core carrier continuation consistency | T23 RealCarrierFaultIsProtocol. |
| Accept a binding/receipt hash or instance mismatch | GuardedApplyEvidenceRefusalClasses, from a verified valid producer setup. |
| Treat any missing/stale PID as stopped or use the mutating soft Status | StoppedReceiptRequiresActualReapedInstance, CaptureCannotAdoptZeroOrActualExitedPID and native-source snapshots. |
| Exclude PID files from stopped-source equality / ignore root replacement | GracefulPIDRemovalInvalidatesRunningBackup / GuardedApplyRechecksNativeRootAndSource. |
| Rewrite a matching completed generation on repeat | GuardedCLIApplyReadsAndRepeats exact whole-tree/index bytes/mode/mtime and positions. |

Code correct and tested: **not claimed; RED source and selected runtime failures established only**.

Reachable by a user/agent: **not claimed; actual repair main is still not implemented and the endpoint still emits the old array at this candidate**.

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis, github-cli, commit-messages; test-integrity-audit was loaded earlier for failure-mode prevention only, not CHECK.

## Evidence and self-check

| Claim | Evidence | Certainty |
|---|---|---|
| Exact selected RED/controls are real named runs, not compile/no-match failures | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/receipts.json` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/candidate-7493ae1ef/verified-probe-lines.log`; receipt-validation command exit0:12 runs,9 raw1,3 raw0, all named, zero compilation/discovery errors. | Verified (high), only the listed reached assertions |
| No QA production fix or altered prior pending regression | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/source-self-check.log`; source check exit0, hook blob identical to688a86613, prior pending regression diff empty. | Verified (high) |
| Manual impact fallback, not graph certification | GitNexus discovery found no connected tool; recorded known-present factory search exit0/new-hook absence search exit1 before supplied declaration, test-caller/scope sweep. | Inferred impact (medium); no detect_changes claim |
| Tested branch was pushed under human identity without AI trailer | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/guarded-apply/push-tests.log`: authorship/trailer checks, push and ls-remote exit0, remote7493ae1ef9449015de302a85a9555a692c90ce0b. | Verified (high) |
| Later positive branches, hook refusals, full CI, native Windows and mutation proof | Not reached/run by the listed logs; explicit remaining-proof table above. | Unknown / UNVERIFIED, never a silent pass |
| **Self-check** | Re-read L/M and F1 amendment against the new source, exact flags/formats, real child handshake/Wait, copied-data equality/repeat/refusals, void-hook mutation sites, real baseline/validators/logger and every actual selected failure line. Revalidated all12 receipt exits/names/SHA, and source scope/protected-regression diff. Later branches are explicitly unproved; no skipped feature assertion, production fix, fresh CHECK verdict or full-suite green is claimed. | Verified source/evidence self-check (high); unexecuted behavior remains Unknown |
