# Gate C1 / I3 — backend QA coverage plan (2026-10-23)

## Source and ownership

Gate input: pr-test-analyzer final result in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/logs/gate-tests2-claudec-t1.stream.jsonl, reviewed98844ce862e0d227b1a28fd0144f268c32d0407d. Current feature input7c4bf181a0c1a26ecaf69ea2bcb09ef0e8c85ed9 contains that candidate (actual ancestry check exit0) and was normally merged asfa436bb75c1b3368de43b2df53b93e32c3f3906b on red/1266-be. Tree was clean before/after merge; no incoming instruction-file change found. Imported implementation is authorized merge input, not a QA production fix.

Spec: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md::D13/G, B30/B35/B36/B44/B45, FR-019; ::D4/D4a/D4b, B46, T32, FR-024/SC-014. The founder's F1=A check-only design is preserved: no new exclusion lock, automatic restore or automatic live retry. Gate findings are coverage claims until our tests distinguish the failure.

Owned edits: Go test/support files and this test plan/evidence only. No production, generated, contract or runner edit. GitNexus tools are not exposed (ToolSearch found no GitNexus schema); existing test-symbol caller sweeps and exact diffs provide an Inferred impact assessment. No Fly execution. Local tests use one exact tagged top-level symbol at a time through /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh, saved log plus raw exit. No foreign lease cleanup.

## C1 — guarded CLI failure AFTER actual publication

Real unit: command parser/guard/manifest verifier, native process inspection, actual complete copied backup, shared session index construction/publication and real bounded reader. Controlled edges only: test-owned canonical files/native child lifecycle and deterministic output/publication rendezvous. No fake native probe, backup verifier, repair engine, successful publication or CLI return code.

| Case | Independent expected result | Source |
|---|---|---|
| Two real closed canonical sessions, missing only disposable indexes, real native capture/stop and full count-verified copy | Valid apply reaches and publishes the first real generation; first shared reader/index verifier succeeds before a late-fault claim | D13/G, B30/B44 |
| Deterministic barrier after first publication, then canonical/source drift | Exact source_changed exit2, actual published1, unchanged0, explicit actual refusal count, second index not published; no automatic rollback/repair or false zero-publication report | D13 aggregate outcomes, B35/B45, FR-019 |
| Same boundary, then a newly registered real native process or changed native binding/PID evidence | Exact instance_running/stopped_evidence_unknown exit2 as appropriate, actual partial summary, no next publication, no tool-driven kill/PID cleanup | D13/G fresh checks, B35/B44 |
| Actual interruption after one publication | Exact interruption exit1 and partial publication/refusal summary; first completed generation retained, second absent, all owned processes/work joined | D13 idempotence/interruption, B36 |
| Deliberate stopped recovery with renewed/revalidated producer evidence and dry-run | Matching first generation is unchanged byte/mode/mtime; remaining session publishes once, readable stable positions, no canonical duplication or automatic live retry | D13/G, B36 |
| Deliberately damaged post-check, operator restoration rehearsal | Preserve failed-run summary and immutable backup; manually restore the FULL verified copy, independently compare complete counts/bytes/mode/mtime, rerun dry-run, re-establish native capture/stop/binding/receipt and fresh manifest where invalidated, then deliberate apply/verify/reader | D13 F1=A damage/recovery steps, B36/B44/B45 |

Q1 is now independently pinned by the architect's published /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md::V, read in full with U as a present search control BEFORE freezing assertions: after one actual publication plus one triggering late failure, published=1, unchanged=0, refused=1. Unattempted sessions add no refusal or invented status; inspect the second session's generation/checkpoint before/after separately. Source_changed and stopped_evidence_unknown remain exit2; an otherwise-valid inventory interruption remains interrupted/1. Q2=A is approved by squad lead/user: the observing context rendezvous must prove a real shared-verifier-confirmed committed root, not infer publication from elapsed time or a filesystem race. No production seam/flag/mock/lock. Missing required instrumentation/API is a named fatal BLOCKED test plus a testability finding, never skip.

## I3 — carrier-dependent selection, not merely encoded-size accounting

Real unit: canonical writer/index, shared gateway preparation/meter and selector, actual REST messages/detail and WebSocket snapshot/replay. No injected counting weigher, fake replay sink, output-derived golden or REST-only reference standing in for the unit.

| Case | Independent expected result | Source |
|---|---|---|
| Frozen valid tool-heavy/REST-heavy and WS-heavy inputs with row target100 and floor20 | Independently derive two carrier byte bounds and different carrier-only cut points strictly between20 and100, before running production; shared selection uses the larger carrier | D4a/D4b, B46/T32 |
| Actual REST, detail and socket snapshot at same unchanged read cut | Exact expected source IDs/independent position tokens, whole groups, stop_reason=byte_budget, exact row/source counts, truthful continuation and projected bytes covering the larger actual encoded carrier | D4/D4b parity, FR-024 |
| Frozen envelope/context/encoding headroom controls | Fixture validation proves both boundary and boundary+1 math using independent generated-schema encoding/bounds; no page-sum/last-output oracle, floor=target masking or fixed narrow byte estimate | D4a/D4b, SC-014 |
| REST-only selection mutation in independent scratch checkout | Strengthened WS-heavy case must fail its expected source boundary (not merely an unrelated schema/setup assertion) | Gate I3; fresh CHECK owner |

### Frozen I3 vectors — derived before execution

Both fixtures have80 one-row canonical groups, target100/floor20, budget4194304. Opaque tool-call IDs have no maximum in the read contracts (ToolCall/ToolCallStartFrame/ToolCallResultFrame); use48KiB ASCII IDs with fixed three-digit unique suffixes, not invalid oversized tool names. All canonical records remain far below8MiB. The special source is a valid judge_verdict with one64KiB ASCII criterion reason: Message retains canonical content plus its declared typed verdict; the typed WS verdict emits that reason once. This avoids the declared provider-fallback parity hold.

| Fixture | Independent per-group payload + maximum framing headroom | Carrier-only cut before100 | Shared expected boundary |
|---|---|---|---|
| Tool-only: call ID48KiB + result text16KiB | REST64KiB+512B; WS112KiB+1024B because ID appears in start AND result. Fixed batch envelope/context <8192B. | REST63; WS36 |36 groups, exact source indices45..80 |
| Judge special: one64KiB reason | REST128KiB+1024B (content JSON + typed verdict); WS64KiB+768B. Fixed batch envelope/context <8192B. | REST31; WS63 |31 groups, exact source indices50..80 |

For each carrier, the frozen chosen count times(payload+headroom)+8192 is below4194304; the next count times payload plus one required JSON byte exceeds4194304. Thus the boundary is strictly above20/below100 and the two carriers genuinely disagree. Reference generated-schema encodings and real-carrier bounds validate the fixture class, but NEVER choose expected counts from production output. The default40-row detail also byte-cuts at the same36/31 positions (both below40 and above20); compare it to default40 REST, while explicit100 REST compares to an explicit100 actual snapshot. Metadata limits are not falsely equated across different options.

Existing tests' useful ownership/side-effect/encoded-size assertions remain untouched; new named T32 coverage adds exact byte-driven selection, continuation, row/group and immutable-source oracles.

## Proof and handoff

One narrow local process at a time; final source and logs bound by SHA/file hashes. Read every failing assertion and separate setup/compile/control failure from behavioral RED. If current implementation already satisfies a strengthened test, report actual GREEN with the previous suite's inability to distinguish the required defect stated, not invented historical RED. Any genuine implementation failure routes to GREEN-BE with the exact assertion and D13/D4b requirement; QA never fixes production.

GREEN verification/mutation/Proof-of-failability belong to a DIFFERENT qa-lead instance. Team-lead acknowledged this boundary and will dispatch the required REST-only scratch-copy mutation as independent CHECK once this pack is pushed. RED author does not self-CHECK or claim that probe was run. Final report includes per-item RED/GREEN/UNVERIFIED, commit/remote SHA, saved raw exits, limitations and an evidence table ending Self-check.
