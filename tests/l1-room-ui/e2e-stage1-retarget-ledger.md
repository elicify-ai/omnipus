# Stage-1 E2E fixture and selector retarget ledger

## Outcome and execution limit

Three requested retargets are included, in two E2E spec files only. Typecheck and targeted lint both exit 0. Static whole-file and TypeScript AST checks retain all 162 original assertion expressions and all case/configuration declarations, normalizing only the explicitly authorized Team control selector. No assertion was deleted or weakened; no timeout, retry, skip/quarantine, error policy or production code changed.

**E2E was not run locally**, including no Playwright collection command, browser launch, authenticated API setup or global setup. Post-change runtime success is **UNVERIFIED until CI**. Source reasoning is not a browser PASS. Independent GREEN verification and mutation are deferred to fresh CHECK/CI.

## Accepted head and independent before evidence

| Item | Evidence |
|---|---|
| Task source | Team-lead's accepted CI retarget dispatch, 2026-10-15; tests only, no local E2E |
| Required UI head | 4cafd099b922a72d1a06ec75ba85b17bd99231ff, fetched and merged before editing |
| Merge/baseline commit | df8b88fc2cca77c9e15de6e2a6c46269e2776766; conflict-free merge into work/omnipus-l1-ui-red-stage1 |
| PR and CI run | PR #1316 head read first-hand as 4cafd099b; run https://github.com/elicify-ai/omnipus/actions/runs/38683771987 |
| Docking before evidence | ui-3 job https://github.com/elicify-ai/omnipus/actions/runs/38683771987/job/116255252597 — 22 distinct native docking failures, all reaching openNativeDock's chat-input visibility wait |
| UAT before evidence | ui-4 job https://github.com/elicify-ai/omnipus/actions/runs/38683771987/job/116255252485 — UAT-47 (a)/(b) fail on the controlled Team path's stale team-add-agent locator; Settings fails on the stale subtitle prefix, including retries |

**Triage precision:** the docking CI failures are `expect(chat-input).toBeVisible()` timeouts. `BLOCKED: real chat did not render` appears in the following source context; it was not the thrown error in those cases. This does not change the fixture diagnosis: the source establishes an empty team when creation omits core_team, and the real chat tab renders EmptyTeamEntry for that state. No trace/screenshot-based claim about the original page state is made.

## Before → after ledger

| Case/file | Before | Test-only after | Oracle retained / certainty |
|---|---|---|---|
| Docking fixture: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/e2e/side-panel-expand-multitab.spec.ts::createWorkspace | Workspace POST sends name only; L1 FR-048 starts it with an empty team. FR-107 then selects the empty-team screen, contrary to this suite's real-chat precondition. | Same isolated workspace and cleanup, but explicit core_team ['mia']; the request literal satisfies generated WorkspaceCreateRequest. No shared default workspace is mutated. | All 129 original assertion expressions and 19 case/config declaration nodes unchanged. Fixture correction Verified by code; successful browser outcome Inferred, not tested. |
| UAT-47 Team entry: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/e2e/uat-shell-and-screens.spec.ts::openTeamPanel / openAddAgentPicker | Waits/clicks the standalone team-add-agent trigger, which is not the trigger in Team's controlled WU-10 menu path. | Wait/click team-add-agent-menu, then exact menuitem “Add an existing agent”; await the same real picker popover. | All visible-before-confirmation, timeout, plain-text/no-tooltip and placement checks retained. No hover, force-click or confirmation added. |
| UAT-47 disclosure locator, same file | First text matches using “this workspace” / “act as whoever”. | Both cases scope their original text locator inside team-add-agent-disclosure within the actual picker, retaining the meaning-bearing text as the visibility/tooltip/box target rather than only its wrapper. | All four independent semantic regexes remain byte-identical: browser, signed-in/logins, act-as consequence, unattended/background/scheduled turns. Bounding box, no-tooltip and above-candidate checks unchanged. |
| Settings screen walk, same file::SCREENS | /Configure providers, integrations/i does not match the accepted new wording. | /Configure providers, system agents, integrations/i — exact current prefix, not an optional/widened match. | Same visible screen and console-error assertions, same pattern flags and timeout. Only the explicitly requested stale copy literal changes. |

The UAT spec retains all 33 original assertion expressions and 5 case/configuration declaration nodes after the exact control-selector normalization. The only additional interaction opens the existing-agent menu choice; no agent is selected/added by the disclosure tests. The existing quarantine remains exactly as before; it was neither added nor extended.

## Code read before changing fixtures or selectors

| Claim | First-hand source |
|---|---|
| Explicit team is a supported create request | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/contracts/components/schemas/WorkspaceCreateRequest.yaml::core_team |
| Omitted team starts empty; explicit team is honored | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/pkg/gateway/rest_workspaces.go::handleWorkspacePost — validates explicit membership, uses provided CoreTeam, otherwise sets nil/SetupPending |
| Empty conversation team does not render ChatScreen | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/workspaces/WorkspaceChatTab.tsx::WorkspaceChatTab / EmptyTeamEntry |
| Mia is the compiled conversation-core identity, not a guessed custom agent | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/pkg/coreagent/core.go::IDMia / Mia; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/pkg/coreagent/seed.go::SeedConfig |
| Team has a menu and an existing-agent choice | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/workspaces/WorkspaceTeamTab.tsx::TeamAddAgentMenu — menu closes before opening the controlled picker |
| Disclosure is plain content before search and candidates | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/workspaces/team/AddAgentPicker.tsx::AddAgentPicker — role note, stable disclosure test id, first content child |
| Disclosure semantics are independent of current implementation copy | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/docs/internal/specs/browser-rework-uat-plan.md::UAT-47 — live-login consequence, unattended turns, before confirmation, not a tooltip |
| Settings includes system agents | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/src/components/screens/SettingsScreen.tsx::SettingsScreen |
| Setup still uses shared session and CSRF, not a new login | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/e2e/fixtures/admin-api.ts::newAdminApiContext; unchanged by this task |

## Saved verification receipts

| Check | Command / exit / key output | Absolute log path |
|---|---|---|
| Prior ui-3 CI read | gh run view 38683771987 --job 116255252597 --log-failed; reader exit=0; remote job FAILURE, 22 relevant docking failures | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/e2e-ui3-failed-ci.log |
| Prior ui-4 CI read | gh run view 38683771987 --job 116255252485 --log-failed; reader exit=0; remote job FAILURE, UAT-47 a/b and Settings stale locators | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/e2e-ui4-failed-ci.log |
| Typecheck | npm run typecheck; exit=0; tsc -b --noEmit | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/e2e-retarget-typecheck.log |
| Touched-spec lint | npx eslint with the two touched spec paths; exit=0 | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/e2e-retarget-lint.log |
| Static preservation | Node whole-file allow-listed diff + TypeScript AST comparison; exit=0; 129 + 33 original assertion expressions retained; all case/config declarations unchanged | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red-s1/tests/.l1-ui-red-stage1-evidence/e2e-retarget-preservation.log |
| Instrument can detect weakening | In-memory assertion deletion and timeout widening both rejected before trusting preservation. No test or production file changed by those controls. | Same preservation log |
| Post-change E2E / independent CHECK | NOT EXECUTED locally, not claimed passing; requires CI and fresh CHECK. | No runtime receipt fabricated |

GitNexus impact attempts resolved a stale sibling index. Their returned caller counts are not trusted. The direct caller sweep shows createWorkspace is this suite's beforeAll fixture; openTeamPanel/openAddAgentPicker serve the two UAT-47 rows; SCREENS changes only the Settings entry. Impact is **Inferred (high confidence): these test paths only**, not an indexed execution-flow claim.

The PR check list also contains unrelated failed gates. This task does not diagnose or close them and does not claim a fully green branch. Likewise it does not establish that all 22 docking cases now pass: fixture preconditions and selectors are code-verified, browser behaviour awaits CI.

No new user-facing product behaviour was authored, so no user-facing documentation update is needed. No production change, assertion weakening, skip, quarantine extension, mock, forced interaction or test-timeout increase is included.

Code correct and tested: **typecheck/lint/static preservation verified; post-change E2E and independent CHECK outstanding**.
Reachable by a user/agent: **real menu and fixture preconditions traced through current code; live browser execution unverified**.

Skills: omnipus-shared-rules, elicify-test-writing, github-cli, gitnexus-impact-analysis, commit-messages (the latter two loaded earlier in this session).

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Requested UI head merged before editing | 4cafd099b922a72d1a06ec75ba85b17bd99231ff is ancestor of merge df8b88fc2cca77c9e15de6e2a6c46269e2776766; conflict-free merge log | Verified (high) |
| All three retargets preserve their original behaviour oracles | Reviewed two-spec diff; static preservation exit=0, all 162 assertion expressions retained modulo the authorized selector; exact copy prefix update and one menu interaction only | Verified (high) |
| The before failures are real CI observations | First-hand ui-3/ui-4 logs above, reader exits=0; 22 docking failures and 3 UAT/screen failures (plus retries) | Verified (high) |
| Typecheck and touched-spec lint succeed | Saved commands above, both raw exits=0 | Verified (high) |
| The changes should restore the intended fixture/control path | Current create handler, chat entry, Team menu, picker and Settings code read against the dispatch | Inferred (high), not a runtime PASS |
| Post-change E2E outcome | No local E2E or collection run; CI after the patch and fresh CHECK still needed | Unknown / UNVERIFIED |
| **Self-check** | Re-read the final two-spec diff against every dispatch item; compared all assertions/case/config nodes and whole-file allowed edits, calibrated the instrument, ran typecheck/lint, and verified tests-only scope before commit/push. No local browser/global setup ran. | Verified (high); runtime still unclaimed |
