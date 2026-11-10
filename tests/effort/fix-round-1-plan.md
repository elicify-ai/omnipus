# Effort RED repair — round 1

Source: `/workspace/omnipus-coordination/designs-20261029/effort-red-check-report-1.md` and `/workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md`.

One author, test-only repair; no production fixes, no push. All runs foreground, serial, plain `/usr/local/bin/heavy`, Go `CGO_ENABLED=0`, tags `goolm,stdjson`, `-p 1`, named tests only. GREEN and mutation evidence are **deferred to CHECK**. Full suites are CI-only.

## Case and boundary plan

| Findings | Real boundary / process-edge fixture | Independent expectations and fault to catch |
|---|---|---|
| 01, 12 | Real session archive, reopened store, REST history and WS replay | S-21/S-95 exact effort object and stored 120/zero/absent snapshots, paging omits contributor; dropping fields must fail |
| 02, 03, 04, 05 | Production config codec and candidate selection; HTTP recording endpoints; shared D2 | Versioned isolated fixtures; seed own fallback/default; nested OpenRouter encoding; valid Off switch; exact blocked result |
| 06 | Catalog parse/served pipeline, encoder table and real adapter bodies | All S-17/18/19 route forms; stream + nonstream; D3 filter rows a–f; no budget/control metadata; provider default omits fields |
| 07 | Provider decode, message, private session codec/durable restart and request serialization | Signed/opaque carrier preserved; populated cross-route control; Off survives both requests of loop; unsupported Bedrock guard |
| 08 | Failed attempt accounting, real transcript append and cell accumulation | S-88 A keeps 120, B absent, sum 120 not 240; zero vs absent; history snapshot separate from statistics |
| 09, 10, 14 | Real picker/Memory components and real browser transport; mock HTTP only | Multiple radios valid, DeepSeek no Medium; old/new pair cancel/escape/rollback; correct WS metadata, entries page and turn note; held first fallback autosave then `[A′, B′]` |
| 11, S-65 | Real tools registry, persisting config callback, real policy compositor | Offered on/off on switch-only model; capture post-seed revision; required fields; disk/reload equal; deny means byte-identical config/entity |
| 13, S-83, S-84 | Real recap generation with isolated loaded config, recording provider | Ten runtime effects; defaults, output fields, prompt/input/fallback/retries/deadlines/spacing; clamp preserves file and warns once; ages 12/8 at idle 10 and 31/29 at default 30 |
| 15 | Commands plus real loop/store restart | Never reuse runtime as restart. Missing capability/storage hooks are loud BLOCKED tests, not guessed API or green claims |
| 16 | Exact error identity and independent literal strings | First error survives distinct second error; strict sign-in decoder exact sentence with validation off; sentinel independent of production constant |

A missing product seam is asserted as `BLOCKED: … not implemented — required by …`, never skipped and never called full behavioral coverage. Each repair must be re-read and its named run recorded before commit. Existing valid assertions stay; contradictory oracles are corrected against the spec, not current output.

## Impact

GitNexus MCP lookup (`gitnexus impact detect_changes`) returned no tool in this session, including final discovery. Fallback: repository caller/reference sweeps of edited test symbols and helpers, plus final diff/scope inspection instead of unavailable `detect_changes`. This is **Inferred**, not a GitNexus impact result. No production symbol is edited.

## Round-1 outcome and known limits

- CHECK-01 through CHECK-16 each have a test-side repair in `/workspace/wt-effort/docs/internal/specs/effort-levels-red-traceability.md::Repair resolutions`; these are author resolutions, never an independent verdict.
- The trace has 97 unique scenario rows, including S-17/S-18/S-19/S-73/S-83, with only S-16/S-27/S-47/S-96 marked N/A. A row with downstream assertions blocked is not complete acceptance coverage.
- Actual Go receipts: 133 distinct top-level named tests, 16 passing baseline controls and 117 failures including loud missing prerequisites. Component files: 36 failures. Browser: twelve executed, eleven failures and one passing envelope control. Commands, raw exits, named results, source fingerprints and saved-log digests are indexed at `/workspace/wt-effort/tests/effort/fix-round-1-receipts.json`.
- The added REST matrix drives seven slot boundaries in both validation modes and seven input classes (98 subcases), plus five mutable stored-High omission/clear workflows in both modes (ten subcases). Twelve omitted-effort controls pass. Two otherwise-baseline Memory validation-on controls are BLOCKED: the production handler asks for an absent inbound schema alias and returns HTTP 500. No test validator alias or production change masks it.
- Separate production blockers remain loud: T8 `get_agent` fields/model-chain/options, `/effort`, UI effort controls, effort storage/encoders/records, recap settings and multi-candidate streaming. Actual Bedrock factory/profile and loop request IDs also disagree; reported for implementation triage, not fixed here.
- Residual breadth stays explicit: completed Provider-default records on every adapter route, default-settings/provider-removal UI parent integrations, and a full actual OpenRouter streaming gateway-restart campaign are not all independently driven. Fragmented-stream codec tests and the actual nonstream Router two-tool durable restart are separate evidence, not interchangeable claims.
- Onboarding's config self-write registry counts distinct config hashes, not identical repeated filesystem writes. The wizard's no-follow-up-default-write assertion is coded but unreached behind its absent Change now control.
- Scoped ESLint, targeted Go vet and size budgets pass; typecheck and design-system publication gates remain red on production scaffolds. No suppressions, skipped tests, assertion weakening, production repairs or full suites were used to make those gates pass.
- GREEN, scratch-clone mutations, proof-of-failability completion, independent CHECK, full CI and exact-commit UAT remain deferred to their proper later phases. User-facing documentation and contract-order proof are implementation obligations, not met by this RED repair.

## Evidence and final self-check

| Claim | Evidence | Certainty |
|---|---|---|
| Repairs trace to the independent input and specification | The two absolute input paths above; `/workspace/wt-effort/docs/internal/specs/effort-levels-red-traceability.md::Repair resolutions` | Verified |
| Targeted runs and passing baseline controls are distinguished from feature acceptance | `/workspace/wt-effort/tests/effort/fix-round-1-receipts.json`; saved logs retain commands, raw exits and actual assertions | Verified |
| No graph-analysis success is claimed | Final tool discovery returned no matches; fallback caller/scope review as described above | Inferred impact only |
| Self-check | `/workspace/logs/effort-red/fix1-precommit-scope-and-receipts.log`, exit 0: all 16 repairs, 97 rows, named runs, raw exits, source fingerprints, formatting and test-only scope reconciled; remaining limits retained. The final handoff supplies staged-diff/commit evidence. | Verified author self-check, not independent CHECK |
