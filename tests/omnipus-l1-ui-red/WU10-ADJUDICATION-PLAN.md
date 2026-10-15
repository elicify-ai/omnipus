# WU-10 author-side adjudication plan — five assertions

This is a correction of the RED author's own test harness, **not independent CHECK**. Production code is never edited. Work branch: work/omnipus-l1-ui-red-wu10 at f37fb27aa81e63606aaab69408024d1b95605ffb. Frozen implementation under investigation: 397a6d690c473a05fc1f1241b5db936bda735f47. Implementation and QA dependency manifests match.

## Independent oracle

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/docs/internal/specs/omnipus-system-agent-spec.md::§6.3, FR-111, FR-112, BDD-12.12, BDD-12.13.

The stale-state contract requires a retained view plus its stale cue. It does not specify a same-event-loop-tick render. The self-row contract requires visibility **and edit/remove controls**, persistence after a reload, and a visibly refused next helper after disabling/removing the row. A limitation of jsdom does not remove that product requirement.

## Case plan

| Case | Unchanged expected result | Investigation / proposed correction, conditional on evidence |
|---|---|---|
| 1 Sidebar failed session refresh | Omnipus accessible name matches stale / out of date / last known | Reproduce original synchronous assertion; read installed query scheduling; compare actual error state vs DOM; await the IDENTICAL assertion through waitFor only if delayed delivery is demonstrated |
| 2 System agents failed roster refresh | Cached Omnipus edit action visible plus stale cue | Same; retain cached-role assertion and all first-load/Retry/uncertain-save negative controls |
| 3 Team list failed roster refresh | Cached Mia/model visible, may-be-out-of-date cue, no setup offer | Same; preserve every exact locator/expected value and negative setup-offer assertion |
| 4 Team graph failed delegation refresh | Cached graph visible plus stale cue | Same; preserve cached graph assertion and original cue regex |
| 5 Default graph self-row | Omnipus node visible; edge-chip opens enabled depth and remove controls | Reproduce original graph path; compare a Mia→Mia self-edge in the same harness; inspect installed React Flow measurement gate; try a measuring environment adapter as a diagnostic. The counterprobe passed for both identities. Team-lead approved the stronger correction: add a reusable measuring observer/dimension-getter helper and keep ALL THREE original edge assertions unchanged. Real-browser persistence/refusal remains mandatory; no graph/component mock |

## browser/UAT rows for WU-10 — updated mandatory U4

All previously required WU-10 browser/UAT rows remain mandatory, including U1 disabled composer, U2 header-to-System-agents navigation, U3 routed empty/error state, U5 W8/W18 completion, U6 real-screen accessibility/reflow/motion, U7 malformed-row visible resilience and U8 #215 freshness without navigation. This correction does not mark any of them accepted.

| Row | Spec | Steps / expected observable | Independent validator critical path |
|---|---|---|---|
| U4 — real self-edge controls, persistence and helper refusal | FR-111 / BDD-12.12 / FR-109 | On the exact merged candidate, open Default Team → Graph. Omnipus remains a node; open its Omnipus→Omnipus edge chip using keyboard or touch, not hover alone. Verify the actual depth input and edge-delete control are enabled. Set depth to 0, reload and verify 0 persists and a helper launch is visibly refused. Then remove the SELF-EDGE (not Omnipus team membership), reload and verify the edge is gone, Omnipus is still present, and the next helper launch is visibly refused | Re-drive chip opening, enabled controls, save→reload at depth 0, edge removal→reload, and refused helper launches with a separate validator account/session. Evidence shows the tester/validator account and role badge plus workspace; never reuse the tester's cookie |

The measuring unit adapter preserves automated node/chip/depth/delete coverage. It is NOT proof of real browser geometry, input modality, persistence after reload or backend refusal. Team-lead dispatches tester and independent validator on the exact candidate; account identities and UAT execution receipts are still unknown in this author lane.

## Reproduction boundary

Commands are launched from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red. A disposable archive of the frozen implementation lives at /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-wu10/adjudication/reference-397a6d690. Only test files may be changed in that snapshot; production source hashes are compared before and after. Existing matching dependencies are linked, not reinstalled. All local runs select exactly one test file and are serial. Logs capture raw exit without a pipe.

## Verification / invariants

| Item | Evidence to collect |
|---|---|
| Baseline | Three unchanged component files on frozen implementation, named outcomes and raw exits |
| Scheduling instrument | Installed notifyManager/defaultScheduler/systemSetTimeoutZero/useBaseQuery source; per-case query-error vs DOM settlement receipt. No scheduler override or timeout widening in committed tests |
| Graph instrument | Real result for Omnipus plus ordinary-agent control; measurement/edge-render source; optional geometry-only adapter counterprobe |
| Final revised tests | Same three files on same production snapshot after test-only copy; exact assertions preserved in waitFor; browser obligation explicitly retained |
| Prior RED remains RED | Revised files on QA's pre-implementation production base, one file at a time. Missing implementation is not repaired here |
| Git scope | Only three test files, the reusable test geometry helper and this test plan; no skips/expected-fail/suppressions; human authorship; normal commit/push |

Implementation mutations and a test-integrity verdict remain **deferred to a fresh CHECK instance**. The author-side diagnostic probes are not CHECK or a product-acceptance claim. Required browser/UAT evidence is not silently called verified.
