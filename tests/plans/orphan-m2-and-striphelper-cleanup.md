# M2 hint precision and public recovery test migration

## Scope and independent oracles

This is a test-only correction and test-entry migration on baseline `72b1b191dab7cd9f3348a97055b9f2b59668a8d5`. No production behavior, hint wording, contracts or user-facing documentation changes are permitted. Keep both legacy recovery test names so existing selections still collect them. Keep every existing behavioral assertion and every other M2 mark assertion.

| Behavior | Independent source |
|---|---|
| Recall hint accepts an exact integer address followed by whitespace, the ruled punctuation, or end-of-string; rejects another numeric/alphanumeric token | Architect Option A in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/logs/context-window/orphan-m2-ruling.log`, result record; dispatch's exact pattern and nine examples |
| The interrupted assistant is absent from recovered model history; original user is preserved | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/cw-orphan-m2-cleanup/docs/internal/specs/tool-registry-redesign-spec.md` — FR-088; existing first legacy test |
| A bound cancellation is appended without deleting original archive messages | Same specification — FR-069; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-mechanism-ruling-20261001/ruling.md` — Shared pure model view and Unchanged constraints |
| Marker uses one-l `turn_canceled_restart` and the existing record shape, not a new alias | Same architect mechanism ruling — Position-scoped recovery idempotency |
| No unresolved call means no change to recovered history or stored transcript | Dispatch Task 2; existing second legacy test; mechanism ruling's preservation of completed groups |

Expectations are written from these sources before execution. Production signatures and neighboring fixture construction are consulted only to reach the real entry point. The public signature read is `RecoverOrphanedToolCalls(store session.SessionStore, sessionKey string, auditLog *audit.Logger) []providers.Message`.

## Unit boundary

REAL: mark assertion helper and its JSON decoding/ID/name/address checks; production projection/checkpoint M2 path; public RecoverOrphanedToolCalls and real temp-directory session store.

MOCK: none in the recovery tests. The mark-helper tests use an in-process recording assertion sink to observe a deliberate rejected hint without a second test process or a duplicate pattern. Dispatcher session `omnipus-ee` approved Option A: a minimal parameter interface embedding `require.TestingT` plus `Helper()`, with every existing assertion body unchanged. The sink records assertion failure and preserves fatal short-circuiting; the test asserts the exact failure count, fatal status and address-assertion diagnostic.

## Case table

| Case | Input | Expected outcome |
|---|---|---|
| M2 real product hint | Existing projection fixture, retained result address 7 | Exact mark schema, quoted ID, recall tool name, integer address and preserved structure all pass; old hint regex must fail |
| Address / whitespace + parenthesis | `archive_line=7 (offset, length)` | Accept |
| Address / whitespace + prose | `archive_line=7 to read…` | Accept |
| Address / end-of-string | `archive_line=7` | Accept |
| Address / JSON punctuation | `"archive_line":7}` | Accept |
| Address / immediate parenthesis | `archive_line=7(` | Accept |
| Address / tens suffix | `archive_line=70` | Reject specifically at the hint-address assertion |
| Address / hundreds suffix | `archive_line=700` | Reject specifically at the hint-address assertion |
| Address / letter suffix | `archive_line=7x` | Reject specifically at the hint-address assertion |
| Address / fractional suffix | `archive_line=7.5` | Reject specifically at the hint-address assertion |
| Legacy first recovery test | Real seeded/saved session: one user and one unresolved assistant tool call | Recovered view is exactly the original user; stored transcript preserves both original messages and exactly one schema-correct cancellation record |
| Legacy second recovery test / no calls | Real seeded/saved session: user and ordinary assistant | Full recovered and stored messages equal the original sequence |
| Legacy second recovery test / resolved call control | Real seeded/saved session: user, assistant tool call, matching result | Full recovered and stored messages equal the original sequence; a direct unconditional strip is not valid public recovery |

Correction: the initial plan counted 12 executable leaves. That omitted the existing M2 case; the correct total is 13 (M2, nine hint controls, three recovery leaves). The hint-specific negative ratio is 4/9 (44.4%); the no-op recovery controls are separate preservation cases. There is no new numeric product limit: line 7 is the architect's representative address, not a capacity boundary. Empty/malformed mark schemas and unrelated recovery groups are covered by the existing acceptance pack and are outside this dispatch.

## Serial execution and RED meaning

| Stage | Required evidence |
|---|---|
| M2 baseline RED | Run only `TestOrphanRecoveryAcceptance/archive_mapping_and_projection/M2_projection_uses_live_original_composite_address` with the old regex; the failure must print the real hint and failed address assertion |
| M2 corrected GREEN | Same narrow command on the corrected regex, no product changes |
| Hint boundary controls | One narrow top-level boundary test; expected rejection must identify the address assertion, not another schema/ID/name failure |
| Recovery migration RED | Add real-store and public-contract assertions while the old direct strip invocation remains. First test must fail because that seam appends no cancellation; completed-call control must fail because the unconditional strip discards a resolved group |
| Recovery migrated GREEN | Replace the two direct invocations with the public entry point; run each unchanged legacy top-level name serially |

Recovery RED demonstrates the old test seam cannot exercise the public recovery contract. It is not a claim that baseline public recovery is broken: the dispatch explicitly requires passing against unchanged production. No production mutation is authorized or performed by this author.

Use the shared retained-file `/tmp/omnipus-gotest.lock`, build tags `goolm,stdjson`, `CGO_ENABLED=0`, `-p 1`, `-count=1`, verbose named collection and direct inner exit receipts. No full package, full suite, parallel test process, private build cache or production edit.

## Predicted breakages for fresh CHECK

| Fault | Control that should catch it |
|---|---|
| Restore the old punctuation-constrained regex | Real M2 and whitespace/immediate-parenthesis positives |
| Remove the integer-token boundary or accept every hint | All four rejected address tokens |
| Recover without appending a bound durable marker | First migrated test's exact stored marker assertions |
| Return unresolved assistant unchanged | First migrated test's original length/role and strengthened full-user equality |
| Strip a complete group or alter an ordinary assistant | Second migrated test's full-view and stored-sequence equality |

These are predictions, not mutation receipts. Independent CHECK and the Proof-of-failability checklist remain deferred to a fresh instance. The explicit dispatch separately requests serial local red/green execution receipts for this test-only maintenance work; those receipts are not a CHECK verdict.

## Task 1 execution receipts

Evidence directory: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-m2-cleanup-20261002-86c190`.

| Run | Direct Go exit | Named result | Receipt |
|---|---|---|---|
| Original M2 | 1 | FAIL at the hint-address assertion; actual producer text includes `archive_line=7 (offset, length)` | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-m2-cleanup-20261002-86c190/m2-old-regex-red-receipt.json` |
| Nine controls, old regex | 1 | Three ruled positives FAIL; two positives and all four negatives PASS | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-m2-cleanup-20261002-86c190/m2-hint-controls-old-regex-red-receipt.json` |
| Corrected M2 | 0 | Named M2 PASS; schema, exact ID/name/address and structural assertions all complete | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-m2-cleanup-20261002-86c190/m2-ruled-regex-green-receipt.json` |
| Nine controls, corrected regex | 0 | All nine named controls PASS, including each invalid token rejected by the actual helper's address assertion | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/cw-orphan-m2-cleanup-20261002-86c190/m2-hint-controls-ruled-regex-green-receipt.json` |

Each receipt has its exact command/tree/source manifest, raw log and inner exit file alongside it. Only the M2 case and the three previously rejected valid-hint controls showed RED; the baseline-passing negatives are not described as red-first or mutation-certified. Task 2 execution is pending at this plan update. Independent CHECK remains deferred.

## Known gaps

Full-suite, race, cross-platform and live reachability results are UNVERIFIED and belong to CI/the feature dispatcher. Audit-event behavior is unchanged and remains covered by neighboring tests; this migration passes the optional nil logger. No user-facing documentation update is needed because this change alters tests only. GitNexus tools are unavailable in this session; pre-edit caller sweeps and final diff/scope checks are the inferred fallback, not a claimed graph result.
