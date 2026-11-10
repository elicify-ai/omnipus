# WU-10 server setup-offer migration and function-budget repair

QA parent: 80225e097ec783d8a65d50e62666d2f8d5b81116; own branch work/omnipus-l1-ui-red-wu10. Budget reproducer: eada779c6fd05779ec4bd8330aa46e5690517458. Healthy UI target: ba1d4ff428fde402e3d5e67db3c9b40080edd334. Server authority: 3bbb4adf3e0aa91c9774020d307562fe115a86b6. No production mutations, configuration edits or budget-allowance changes.

## Authority and independent expectations

| Source read | Requirement |
|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/docs/internal/specs/omnipus-system-agent-spec.md::FR-047, K9, BDD-5.1/5.2/5.8 | Server owns the initial setup offer into Omnipus's existing main; new workspace team is empty; client must not initiate the kickoff; the explicit Team setup action opens W18 for that workspace |
| Same spec in frozen ba1d target::BDD-5.8 chief amendment 2026-10-14 | EVERY client kickoff marker is refused, with or without session id, recipient or workspace id. Deleted client machinery is superseded, not a missing implementation to restore |
| Corrected team-lead dispatch, 2026-10-14 | Creation/open of empty workspace sends zero client kickoff frames; creation itself sends no chat frame; exact server system-entry offer/failure copy remains visible from history/replay; Set up a team retains workflow_start without kickoff metadata. setup_target_workspace_id is server-internal, NOT a wire oracle |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/scripts/check-function-budget.sh::documented rule | Test functions share 240-line ceiling; listed grandfather allowance only shrinks. Split oversized describe without changing any expectation, threshold or scanner |

## Plan fixed before edits

| Area | Tests / exact expected observation | Boundary and negative control |
|---|---|---|
| Budget | Reproduce listed 254 > 247 on immutable eada; split the pending-card describe into bounded sibling groups. Preserve every existing test title, case body and matcher expression byte-for-byte; re-run unchanged function-budget gate to exit 0 | No budget/config/scanner edits. Calibrate assertion comparator by removing/changing one matcher in memory; must detect it |
| Workspace create/open | Real sidebar create and empty-workspace open through real router/query/stores; inspect all captured outgoing generated frames. No metadata.workspace_setup_kickoff on any frame; creation sends no type=message frame. Positive UI/cache/HTTP-created workspace proves the path actually ran | Raw HTTP/socket process edges only, no kickoff hook/message-send/workspace component mock. Empty team/setup_pending fixture ensures old kickoff preconditions are genuinely exercised |
| Offer history/replay | Real Omnipus main chat rebuilt independently from HTTP history and public replay frames. Exact system text: Workspace setup offered for Research. Failure text: The setup offer for Research could not be started. Open its Team tab to set up its team. | Real ChatScreen/runtime and frame consumption; no direct DOM insertion, formatter mock or invented setup-target wire key; preserve same Omnipus main |
| Manual Team setup | Real Set up a team button sends workflow_start {workflow:W18,target_workspace_id:Research id} to existing Omnipus main, no client kickoff marker or target workspace rebinding | Active workspace remains Research; Omnipus main stays Default. Failed/empty roster distinction retained in existing coverage |
| Legacy cleanup | Verify every supplied classification against actual target source and the QA source, then delete only retired client-hook/machine cases; drop only absent-field preconditions/seeds elsewhere | Maintain per-case old→new ledger including every retired title and reason. Preserve unrelated no-replay, first-message, precedence, mail-discard, connection and clear-state assertions |

The read-only legacy inventory is delegated to one tier-1 qa-lead helper; it may not edit, run tests or give an independent CHECK verdict. The parent is the sole test writer and verifies the helper's cited evidence before acting. GitNexus MCP is unavailable, so direct caller/source plus diff sweeps are the fallback; impact is Inferred, not a claimed graph result.

One explicit test file/process at a time. Budget scanners run serially and are not a full Go test/build. All output remains under /Users/danielpiatkowski/AI-Agent-Workspace. No live instance, browser, Fly, remote gate or Documents access. The ba1d lock differs from QA, so its own npm ci installation is required and must be source-bound; no silently shared stale dependencies.

## Proof / completion boundaries

Existing implementation makes the new no-kickoff/offer predicates healthy controls, not observed RED or mutation proof. Propose each old-client/missing-offer mutation for fresh CHECK; author performs no production mutation. Skill step 4 items 2–3 and Proof-of-failability remain deferred to CHECK. Local runs cover named files only, never complete CI or live server-offer delivery/persistence. Actual create → server offer → manual W18 recovery remains tester/independent-validator UAT with separate accounts/sessions, badges, step screenshots and redacted snapshots.

## Delivered author-side artifacts and controls

| Artifact / control | Observed result |
|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/tests/omnipus-l1-ui-red/WU10-KICKOFF-ORACLE-LEDGER.md | Complete changed-case old→new ledger, including reasons for every retirement and the preserved ordinary/mixed invariants |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/tests/omnipus-l1-ui-red/KICKOFF-RETIREMENTS.json | 44 explicit obsolete-case retirements: hook 18, pure client producer 23, wiring two, synthetic rejection one; exact old titles/hashes and independent authority |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/tests/omnipus-l1-ui-red/KICKOFF-CASE-ORACLE-CHANGES.json | Index of per-case UI/reconnect and store/delivery ledgers; all required original matcher expressions retained; real removed-expression/negative-change calibration detected |
| Budget reproducer → split | eada original exit 1: 254 > listed 247; identical gate/budget after split exit 0, callback 171. All 14 test bodies and 51 matcher expressions byte-identical; final QA function gate exit 0 |
| Named legacy controls | Thirteen serial one-file processes on ba1d: 105 named passes, exits 0. Five ordinary store cases and three mixed generic guarantees are preserved/retargeted rather than blanket-deleted |
| New actual UI controls | Workspace.serverSetupOffer on ba1d: nine named passes, exit 0. Real create/open/empty-Team/W18/history/replay paths; raw network/socket/browser geometry boundaries only |
| Split card controls | AskUserQuestionCard on ba1d: 14 named passes, exit 0 |
| Target-only seed, team-lead Option C | Separate worktree /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-wu10-target and branch work/omnipus-l1-ui-red-wu10-target, cut at ba1d. Published a887c2190e2217031d144129a2883bd180564249 removes only one pendingKickoff:null fragment; every other byte unchanged. Its one file passes six cases; no target-only production/test file imported into the QA branch |
| Final static gates | QA typecheck and design scanner exit 0. Exact ba1d plus both authorized test overlays also typechecks exit 0. Complete CI/live server execution remains unverified |

The original classification needed correction: the 31-case kickoff file included surviving ordinary-message coverage; the provider's shared kickoff fixture affected three cases, not one. The parent read and retained those invariants; team-lead accepted both corrections. Ordinary fresh-send fixtures now explicitly name Mia under FR-122, not an implicit/default Ava. Queue survival is retargeted to an actual offline/reconnect/drain flow, not a fabricated replacement kickoff API.

One execution-integrity error is disclosed: the first target-only pre-push verifier required an empty status and failed on the declared dependency symlink; the shell wrongly continued to the normal push. The temporary symlink was then removed after test completion, and independent verification confirmed the exact one-fragment commit, unchanged assertion bytes, human authorship, clean tree and remote=HEAD. The rejected receipt is retained; it is not reported as a passing pre-push check.

No test is skipped or silently removed; retirement is explicit under the server-owned contract. Fresh CHECK still owns mutation/restored-control proof and the formal integrity verdict.
