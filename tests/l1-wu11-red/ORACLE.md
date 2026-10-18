# WU-11 SPA RED pack — ORACLE note

Branch `work/omnipus-l1-wu11-spa` (from release/v0.1.1 288bc9b4b). Unit: WU-11 SPA half
(Agents screen + manual create form + the SPA UI of removed surfaces), from
`docs/internal/specs/omnipus-system-agent-spec.md`.

Spec sources for every oracle: §15 "Work units, landings and order" (WU-11 row), FR-008,
FR-045, FR-103, FR-108, FR-148, FR-253 (landing-1 items), BDD-4.1, BDD-12.7, BDD-12.8,
K22, plus the team-lead steer items SC-007 (no redirect) and the keep-green guard.

RED evidence: each run below has a saved log under `tests/l1-wu11-red/logs/` with its
exit code. Frontend runs obeyed the one-file-at-a-time rule (`npx vitest run <file>`).

## 1. RED tests — failing on today's code (288bc9b4b)

### `src/routes/_app/-wu11-retired-routes.test.tsx` — 4/4 RED (`logs/red-retired-routes.log`, exit 1)

| Test | Spec oracle | Today's code it fails on | RED reason |
|---|---|---|---|
| `/agents` renders root not-found, keeps URL | FR-108; BDD-12.7 (row `/agents`); BDD-12.8; SC-007 | `src/routes/_app/agents.index.tsx::Route` → `src/components/screens/AgentListScreen.tsx::AgentListScreen` ("New agent" action at the header) | The Agents screen still renders at `/agents`; the branded 404 (`src/routes/__root.tsx::NotFoundPage`, "This page drifted into the deep.") never renders. Failure element observed: `<button>New agent</button>` |
| `/agents/mia` renders root not-found, keeps URL | FR-108; BDD-12.7 (row `/agents/mia`); SC-007 | `src/routes/_app/agents.$agentId.tsx::AgentProfileRoute` (opens `openEditAgentSlideOver`, then `navigate({ to: '/agents', replace: true })`) | The deep link redirects to the list (URL changes) instead of 404 at the same URL. Failure element observed: the same `New agent` button after the redirect |
| `/admin/chat` renders not-found, keeps URL even when Admin resolves | FR-108; BDD-12.7 (row `/admin/chat`); SC-007 | `src/routes/_app/admin.chat.tsx::AdminChatRedirect` (`ADMIN_AGENT_ID` line 26; enters Admin's default-workspace main via `useSelectSession`) | The route is a redirect stub — with Admin fully resolvable it navigates away instead of showing the 404. Failure element observed: "Restoring your conversation…" |
| sidebar carries no "Agents" asset link | FR-108; BDD-12.8 | `src/components/layout/Sidebar.tsx::ASSET_ITEMS` — `{ to: '/agents', label: 'Agents', Icon: Robot }` | The fixed nav still links the removed screen. Failure element observed: `<a aria-label="Agents" href="/agents">` |

### `src/routes/_app/-wu11-no-manual-create.test.ts` — 5/5 RED (`logs/red-no-manual-create.log`, exit 1)

| Test | Spec oracle | Today's code it fails on | RED reason |
|---|---|---|---|
| screen/wizard/modal/wizard-dir/WorkerCard files deleted | FR-108 ("create wizard/modal MUST be removed") | `src/components/screens/AgentListScreen.tsx`, `src/components/agents/CreateAgentModal.tsx`, `CreateAgentWizard.tsx`, `wizard/`, `WorkerCard.tsx` | All five still exist; the failure lists them |
| no source imports the retired components | FR-108 | AgentListScreen route files (`agents.index.tsx`), CreateAgentModal importers (`AgentListScreen.tsx`), WorkerCard importer (`AgentListScreen.tsx`), wizard importers | Importers remain. Deliberate: comment-only mentions also count — a removal must not leave dangling references to deleted components (the invariant's failure list is the GREEN cleanup checklist) |
| no create-modal store wiring | FR-108; BDD-12.8 | `src/store/ui.ts` (`createAgentModalOpen`/`openCreateAgentModal`, lines 58–74), `AgentListScreen.tsx` call sites (lines 295, 358, 403, 441) | The manual-create modal's store keys and openers remain |
| no component outside `src/lib/api` calls `createAgent` | FR-108 ("REST create-agent MUST remain, documented as a non-UI API") | `src/components/agents/CreateAgentModal.tsx` (imports and calls `src/lib/api/agents.ts::createAgent`) | The UI still drives the manual agent-create REST flow |
| sidebar ASSET_ITEMS carries no `/agents` entry | FR-108; BDD-12.8 | `src/components/layout/Sidebar.tsx::ASSET_ITEMS` | The literal `{ to: '/agents', … }` is still in the nav table |

### `src/routes/_app/-wu11-ava-admin-removed.test.tsx` — 3/3 RED (`logs/red-ava-admin.log`, exit 1)

| Test | Spec oracle | Today's code it fails on | RED reason |
|---|---|---|---|
| AddAgentPicker offers neither Ava nor Admin even when the cache carries them | FR-008 (Ava/Admin UI removed) | `src/components/workspaces/team/AddAgentPicker.tsx::AddAgentPicker` — the only retired-id filter is `.filter((a) => a.id !== 'admin')` (line 115); `ava` passes `isTeamEligible` (`src/lib/omnipus.ts::teamKindOf`, type `core`) | `ava` renders as a clickable candidate. Failure element observed: `data-testid="team-add-agent-option-ava"` |
| default-workspace roster renders no Admin row | FR-008 | `src/components/layout/sidebar/workspaceRoster.ts::adminDefault` (`ADMIN_AGENT_ID` line 13; row pushed from `Workspace.admin_main_session_id` via `eligibleMains.ts` input) | The Admin row is projected. Failure detail observed: `[{ agentId: 'admin', … }]` in rows |
| no branch site carries an `ava`/`admin` agent-id literal | FR-008 (identity branches AND UI removed) | All seven steer-named sites + the route: `workspaceRoster.ts:13`, `eligibleMains.ts:57`, `useChatAgents.ts:59` (`isStandaloneChatOperator`), `teamGraphModel.ts:568` (`NON_TEAM_AGENT_IDS`), `AddAgentPicker.tsx:115`, `agentKind.ts:81` (`CHAT_BUILTIN_IDS`), `admin.chat.tsx:26`, plus `AgentRowMenu.tsx`'s branch via the imported constant (`isAdminRow`, line 50) | The scan returns the exact surviving lines (10 hits observed). Scope is file-scoped to the named sites — the login/onboarding `placeholder="admin"` (the HUMAN administrator) and task-fixture `owner: 'admin'` are out of scope per the steer |

### `src/lib/api/-wu11-session-scope-client.test.ts` — 4/4 RED (`logs/red-session-scope.log`, exit 1)

| Test | Spec oracle | Today's code it fails on | RED reason |
|---|---|---|---|
| `src/lib/api.ts` carries no SessionScope types | FR-148; K22 | `src/lib/api.ts` imports `SessionScopeRequest`/`SessionScopeResponse` (lines 91–92), `SessionScopeUpdateResponse` (line 158, re-exported at 354; also 288–289) | The SPA client's type plumbing remains |
| `src/lib/api/config.ts` carries no session-scope remnants | FR-148; K22 ("the SPA client in `src/lib/api/config.ts`") | `src/lib/api/config.ts` lines 340–347: re-export notes + the "remain available for a future Settings surface" reservation | The client was functionally removed as zero-consumer, but the reservation contradicts FR-148's "MUST be retired" |
| `src/lib/api/security.ts` hand-writes no DMScope | FR-148 | `src/lib/api/security.ts:237` — `export type DMScope = 'main' \| 'per-peer' \| …` | A hand-written dm_scope type remains in the SPA client |
| no SPA-authored source references the route or wire types | FR-148 | The three files above | Whole-`src` sweep (generated dir excluded — see scope note below) |

**FR-148 scope note (team-lead decision recorded):** the SPA-client removal is a WU-11
preparation commit even though the spec traces FR-148 to US-16/landing 2 via K22 —
removing the client first avoids a compile break when the backend regenerates. These
tests deliberately EXCLUDE `src/lib/api/generated/**` (and `contracts/**`): the OpenAPI
path/schema removal is the backend lane's K22/WU-0b work, and no SPA-lane change may
demand a generated-artifact diff.

## 2. KEEP-GREEN guards — NOT RED (labelled per team-lead steer)

These PASS today (WU-10 landed the wiring) and MUST still pass after GREEN. They are in
the pack so the removals cannot cut the surviving entry points.

### `src/routes/_app/-wu11-add-agent-entrypoints.test.tsx` — 2/2 PASS (`logs/keepegreen-entrypoints.log`, exit 0)

| Guard | Spec oracle | Today's code that already satisfies it |
|---|---|---|
| sidebar "Add agent" starts W1 in the Omnipus main, navigates `/omnipus`, workspace unchanged | FR-045; BDD-4.1; BDD-12.8 | `src/components/layout/sidebar/WorkspaceAgentList.tsx::AddAgentRow` (`data-testid="sidebar-add-agent-*"`, calls `openOmnipus({ workflow: 'W1', … })`) — no prior test pinned this path |
| Graph view picker offers "Create with Omnipus instead" and it starts W1 | FR-103; FR-045; BDD-12.8 | `src/components/workspaces/team/TeamGraphView.tsx` (picker render sites, lines 524 and 579, wired `onCreateWithOmnipus={() => startWorkflow('W1')}` in `WorkspaceTeamTab.tsx`) — no prior test pinned this path |

The LIST-view Team menu entries are already pinned by
`src/components/workspaces/WorkspaceTeamTab.omnipus.test.tsx` (FR-103 two choices,
BDD-4.1 W1, BDD-12.14 keyboard) and are not duplicated here.

### `src/components/workspaces/team/-wu11-agent-profile-reachable.test.tsx` — 3/3 PASS (`logs/keepegreen-profile-reachable.log`, exit 0)

| Guard | Spec oracle | Today's code |
|---|---|---|
| Team card menu Edit opens the editor for the agent in its workspace | US-12 (Team as the agent home); BDD-12.10 | `src/components/workspaces/team/TeamListView.tsx` card actions (`team-card-actions-*` → `Edit <name>`) |
| sidebar row menu Edit opens the editor with workspace context | US-12; BDD-12.10 | `src/components/layout/sidebar/AgentRowMenu.tsx::openEditor` |
| Settings → System agents Edit opens the same editor | BDD-12.6; US-12 AS-4 | `src/components/settings/SystemAgentsSection.tsx` (`Edit <name>` → `openEditAgentSlideOver(id)`) |

## 3. E2E — CI only (never run locally)

`tests/e2e/omnipus-retired-surfaces.spec.ts` (4 tests) — BDD-12.7 ×3 (each of
`/#/agents`, `/#/agents/mia`, `/#/admin/chat`: branded 404 visible AND URL unchanged,
per SC-007) + BDD-12.8 shell sweep (no Agents nav link, no "New agent"/"New Main"/
"New Subagent"/"Add Subagent (External)" controls). Registered in the `stubs-1` shard of
`tests/e2e/shards.json` (`scripts/e2e-shards.sh check` → OK, 89 specs, exit 0). RED
evidence at unit level is section 1; the e2e file's own first execution happens in CI —
marked UNVERIFIED-locally by design (the brief: e2e runs in CI, not locally).

## 4. Old tests deleted or retargeted (separate commit, per spec §9.4 last line:
"Tests asserting Ava/Admin, the default agent or unbound channel routing are deleted
with those features, not weakened; each deletion is listed")

| File | Action | Retiring spec line |
|---|---|---|
| `src/components/screens/AgentListScreen.test.tsx` | deleted | FR-108 (Agents screen removed) |
| `src/components/screens/AgentListScreen.auth.test.tsx` | deleted | FR-108 |
| `src/components/screens/AgentListScreen.systemSection.test.tsx` | deleted | FR-108; FR-008 (the "Built-in colleagues — Mia, Jim, Ava and Admin" section it pins is retired UI) |
| `src/components/agents/CreateAgentModal.test.tsx` | deleted | FR-108 (create wizard/modal removed) |
| `src/components/agents/CreateAgentWizard.test.tsx` | deleted | FR-108 |
| `src/components/agents/wizard/Advanced.toolIterationLimit.test.tsx` | deleted | FR-108 (imports only the deleted `wizard/Advanced`) |
| `src/components/agents/wizard/Step1Identity.commandPreview.test.tsx` | deleted | FR-108 (imports only the deleted wizard modules) |
| `src/components/agents/wizard/Step1Identity.envFocus.test.tsx` | deleted | FR-108 (imports only the deleted wizard modules) |
| `src/components/agents/wizard/Step1Identity.prefillValidate.test.tsx` | deleted | FR-108 (imports only the deleted wizard modules) |
| `src/components/agents/wizard/Step1Identity.test.tsx` | deleted | FR-108 (imports only the deleted wizard modules) |
| `src/components/agents/wizard/Step2Personality.test.tsx` | deleted | FR-108 (imports only the deleted `./Step2Personality` / `../CreateAgentWizard`) |

The six `wizard/` rows above were deleted by commit 97040691e (frontend lane, under
team-lead decision Q3 = B: the wizard test directory is retired with the directory),
between this pack's commits and the dev's GREEN; CHECK finding F1 required them listed
here. Each imports only deleted modules, so deletion was the only disposition and no
surviving coverage was lost (verified by CHECK at base).
| `src/components/agents/WorkerCard.test.tsx` | deleted | FR-108 (WorkerCard rendered only on the retired screen) |
| `src/components/agents/identityPreview.wave1.test.tsx` | deleted | FR-108 (wholly renders `CreateAgentWizard`) |
| `src/test/integration/agent-list.test.tsx` | deleted | FR-108 (test #26 pins AgentListScreen's fetch/grid) |
| `src/test/integration/create-agent.test.tsx` | deleted | FR-108 (CreateAgentModal create flow) |
| `src/routes/_app/-admin.chat.test.tsx` | deleted | FR-108 + BDD-12.7 — the route is removed WITHOUT forwarding, so the redirect-stub pin dies with the stub (replaced by the not-found oracle in `-wu11-retired-routes.test.tsx`) |
| `tests/e2e/agents.spec.ts` | deleted (+ removed from shard `ui-1`) | FR-108/FR-008 (pins the Agents screen: built-in roster accordion, Ava/Admin cards) |
| `tests/e2e/create-agent.spec.ts` | deleted (+ removed from shard `ui-2`) | FR-108 (walks the create wizard for each tier) |
| `tests/e2e/tool-iteration-limit.spec.ts` | retargeted | FR-108 + BDD-12.7 — the spec's BEHAVIOUR is kept (tool-iteration-limit-spec US-2/US-6; the field survives in AgentProfile); only the retired navigation changed: create+add-to-default-team via the same REST the Team tab saves, profile opened from the Team panel card actions instead of the `/#/agents/{id}` deep link |
| `src/components/agents/avatarSurfaceSweep.test.tsx` | partial edit | FR-108 — removed the WorkerCard sweep case; AgentCard case kept (Team cards survive) |
| `src/components/agents/AgentTools.test.tsx` | partial edit | FR-108 — dropped the create-modal store keys from the ui mock + stale header line; the US-1/B-2 ToolsAndPermissions tests are kept behaviour |
| `src/components/agents/ToolIterationLimitField.component.test.tsx` | partial edit | FR-108 — removed the `wizard/Advanced` describe + imports; the field's own 15 tests kept (AgentProfile hosts it) |
| `src/components/agents/toolIterationLimit.noLiteral.test.ts` | partial edit | FR-108 — removed the CreateAgentWizard/wizard-Advanced entries from the guard's file list; all other surfaces still guarded (10/10 pass) |
| `src/test/screens.test.tsx` | partial edit | FR-108 — removed the "Agents screen — empty state" describe (8/8 remaining pass) |

Kept after verification (steer's do-not-delete list, each checked): `CommandPreview.test.tsx`,
`AgentProfile.commandPreview.test.tsx`, `AgentProfile.cliPathValidate.test.tsx`
(`useCliPathValidation`), the `useCliDetect` coverage inside the AgentProfile files,
`ToolsAndPermissions*.test.tsx` (toolPolicyPresets), `ExecutorSelector.test.tsx`
(ExecutorSelector is hosted by the kept AgentProfile), `model-selector.test.tsx`,
`src/lib/api/sessions.create-agent-required.test.ts` (REST-layer session contract, not
the UI form), `tests/e2e/fixtures/selectors.ts` + `selectors.test.ts` (generic URL
helpers; `/#/agents` appears only in a doc-comment example and a synthetic URL, never as
live navigation). WU-10's Team suite (`WorkspaceTeamTab.omnipus.test.tsx`,
`AddAgentPicker*.test.tsx`, `TeamAddAgent.disclosure.test.tsx`, …) tests surviving
surfaces and stays.

### GREEN follow-up — round 2 retirement (stream C, after 88c63303e + 9871cbc66)

The dev's from-scratch gate (`coordination/wu11-spa/gates-r2/`) left typecheck red in 4
test files and 12 unit files failing at runtime, plus 9 e2e sites navigating retired
routes. Disposition below; receipts in `tests/l1-wu11-red/retire2-logs/`.

Deleted (spec §9.4 — the subject is the removed feature itself):

| File/case | Spec line that retires it |
|---|---|
| `src/lib/nav/adminMainSession.test.ts` (whole file: `adminMainSessionIdOfWorkspace`, the Admin roster row, the Admin named-gap) | FR-008 (Admin UI + identity branches removed); the seam reader is deleted |
| `src/hooks/useChatAgents.adminStandalone.test.ts` (whole file: the `isStandaloneChatOperator` predicate) | FR-008 (Admin identity branch removed with GREEN) |
| `adminMainSession` describe's Admin cases inside it — see file deletion above | FR-008 |
| `eligibleMains.test.ts` "default-workspace Admin without a seam id is a named gap" | FR-008 (no adminDefault input exists; no gap can be produced) |
| `eligibleMains.test.ts` "N10 other workspace hides Admin even when the roster lists Admin" | FR-008 (Admin workspace-scoping was an identity branch of the retired entry; retirement is enforced once at eligibility — `lib/omnipus.ts::isTeamEligible` → `lib/agentKind.ts::isChatEligibleAgent` — and the roster renders membership) |
| `WorkspaceAgentList.rowMenu.test.tsx` "the Admin row gets History and Edit but no Hide…" | FR-008 (no Admin row renders; the shared member-menu contract stays pinned by the D1/D2/D3/D5 and locked-default cases) |
| `WorkspaceAgentList.rowMenu.test.tsx` "opens Admin's GLOBAL profile…" | FR-008 (the `isAdminRow` global-profile branch is removed; every surviving row opens the editor with its workspace) |
| `workspaceRoster.hidden.test.ts` "Admin's default-workspace row is never hidden…" | FR-008 (no Admin row exists in the visible/hidden split) |
| e2e `uat-shell-and-screens.spec.ts` Agents row of the SCREENS walk | FR-108 (screen retired; the walk covers surviving screens) |

Retargeted (a still-valid rule buried in a retired subject — same strength, new pole):

| File/case | Before → After | Spec line |
|---|---|---|
| `-wu11-add-agent-entrypoints.test.tsx` (keep-green guard) | seeded/asserted `createAgentModalOpen: false` → asserts `Object.hasOwn(useUiStore.getState(), 'createAgentModalOpen')` is false | FR-108 (key removed; same "no manual create opens" behaviour) |
| `WorkspaceTeamTab.omnipus.test.tsx` | `createAgentModalOpen` seeded ×1, asserted ×2 → same `Object.hasOwn` absence assertions | FR-108 |
| `sessionCoreSeam.test.ts` | passed `adminDefault: null` in two `EligibleInput` literals → key dropped (property removed from the wire-of-tests type) | FR-008 |
| `avatarSurfaceSweep.test.tsx` "WorkspaceAgentList draws the Admin mark from the workspace-carried main" → "…draws the member mark from the agent figure" | Admin-row avatar pin → the same figure-not-initial-circle rule on an ordinary member row (real roster: core_team + validated main) | FR-008; avatar rule unchanged |
| `Sidebar.agentRows.test.tsx` "N10 Admin is a row in the default workspace and not in Product launch" → "N10 a retired Admin agent in the cache is never a row in any workspace" | default-workspace Admin row present, absent elsewhere → Admin absent from BOTH workspaces even on a stale cache (kept Judge/worker exclusions; stale fixture deliberately kept) | FR-008 |
| `Sidebar.m5.test.tsx` "Agents library link has aria-label" → "the surviving global-library links carry accessible names and the retired Agents entry is gone" | Agents link name pin → Skills & Tools + Connectors names, Agents link `null` | FR-108 |
| `Sidebar.m5.test.tsx` "library links are NOT active at /" | Agents + Connectors aria-current null → Skills & Tools + Connectors aria-current null | FR-108 (route-accuracy rule pinned on survivors) |
| `Sidebar.test.tsx` "renders the Workspaces section + Assets" | `getByText('Agents')` present → `queryByText('Agents')` null, Skills & Tools + Connectors present | FR-108 |
| `WorkspaceAgentList.rowMenu.test.tsx` Remove-dialog copy | `toContain('Agents list')` + `toContain('chat history')` → the exact new sentence pinned: "Mia leaves only this workspace's team; the agent still exists, stays in its other workspaces and keeps its chat history." (matches `docs/using-omnipus-ui.md`'s Remove paragraph; commit 9871cbc66) | FR-108; docs match |
| `teamGraphModel.test.ts` "refuses Admin and hidden engine agents" → "refuses hidden engine agents" | admin refusal (id can no longer exist) dropped; judge/plansupervisor refusal kept | FR-008 |
| `useChatAgents.test.ts` "includes Admin but excludes a retired locked Ray record" → "excludes retired built-in records (Admin, Ray) from chatAgents by identity" | `chatAgents === ['admin']` → `chatAgents === []` on the same stale cache | FR-008 |
| `useSlashMenu.mentionAdmin.test.tsx` "still lists Admin in the DEFAULT workspace" → "lists no Admin in the DEFAULT workspace either (FR-008)" | `['admin', 'mia']` → `['mia']` + `@admin` matches nothing | FR-008 |
| `eligibleMains.test.ts` "BDD-01.2 / N10 default workspace keeps Mia and validated Admin only" → "…keeps Mia; a cached Admin never becomes a row" | expected rows dropped the Admin entry; `adminDefault` input removed from the local type + all literals | FR-008 |
| e2e `accessibility.spec.ts` ROUTES | `'/#/agents'` removed from the sweep | FR-108 |
| e2e `nav-wave1.spec.ts` "a built-in agent shows locked figure…" | `/#/agents` → agent-card click replaced by default-workspace Team panel → card actions → Edit (same locked-identity editor assertions unchanged) | FR-108 |
| e2e `sidebar-agent-row-menu.spec.ts` D4 survival check | Agents-list visibility check → REST read `GET /api/v1/agents/{id}` returns the agent (D4: removal is not deletion) | FR-108; D4 unchanged |
| e2e `bug-regression.spec.ts` ×2 navigate-away-and-back replay tests | away surface `/#/agents` → `/#/skills` | FR-108 |
| e2e `csp-assumptions.spec.ts` inline-style sweep | `/#/agents` → `/#/skills` | FR-108 |
| e2e `auth.spec.ts` dev-mode banner persistence | navigation target `/#/agents` → `/#/skills` | FR-108 |
| e2e `sidebar.spec.ts` LIBRARY_NAV_ITEMS | Agents entry removed; every surviving nav item still routes | FR-108 |

Kept after verification: `src/lib/api/configuration.test.ts`'s `createAgent` case — the
REST client stays (FR-108's "documented as a non-UI API"); 36/36 green.

## 5. Notes for GREEN and CHECK

### CHECK round (stream-c/wu11-check-report.md — WARN, TWS 0) — fixes applied

- **F1 (fixed):** the six `wizard/` test files deleted by 97040691e are now listed in
  §4 with their retiring line (FR-108) and the Q3 = B decision noted.
- **F2 (fixed, truthful-rename option):** the eligibleMains case is renamed
  "…keeps Mia (validated members only)". The strengthening option (a real cached-Admin
  roster member) was tried first and exposed a layering fact, recorded here for the
  next CHECK: `eligibleMainAgents` has no retired-id gate in the roster path — a
  cached Admin LISTED ON A TEAM renders as an ordinary member row on any workspace.
  Reaching it requires the server to re-seed Admin AND keep it on a core_team (a
  double regression); retirement is enforced once at eligibility
  (`lib/omnipus.ts::isTeamEligible` → `isChatEligibleAgent`), which is where the
  stale-cache guards live (Sidebar.agentRows N10, the add-existing picker,
  useChatAgents, useSlashMenu). If CHECK wants belt-and-braces at the roster path
  too, `lib/omnipus.ts::teamKindOf` is the single-line place; the pin to restore is
  the fixture probe described above.
- **F3 (fixed):** the stale "adminMainSessionIdOfWorkspace: the Admin-main rule stays
  real" comment is corrected in all 10 surviving files that carried it
  (Sidebar.agentRows' copy was already rewritten in round 2 — 11 total per CHECK).
- **F4 (fixed, cheap form):** the profile-reachable keep-green guard gains a
  source-mount pin — `AppShell.tsx` imports `AgentProfile` and hosts
  `<AgentProfile />` exactly once — so a future change dropping the shell mount dies
  with the three open-path cases above it.



- `src/components/agents/CLAUDE.md` still documents "Creation has one path" via
  `CreateAgentWizard` and the built-in Mia/Jim/Ava/Admin roster — the module-doc update
  belongs to frontend-lead's GREEN (module CLAUDE.md is not a test file; listed here so
  it is not missed, together with its byte-identical AGENTS.md twin per
  `scripts/check-agents-md-sync.sh`). UPDATE round 2: the dev's GREEN (88c63303e)
  rewrote this module doc — the note is satisfied; retirement is now documented as
  enforced once at eligibility (`lib/omnipus.ts::isTeamEligible` →
  `lib/agentKind.ts::isChatEligibleAgent`), which is the design the round-2
  eligibleMains deletion above relies on.
- File B's no-importer invariant counts comment mentions on purpose (e.g.
  `src/components/ui/sheet.tsx:76`, `src/hooks/useCliDetect.ts:22`,
  `src/components/shared/AdvancedDisclosure.tsx:9`, `AgentProfile.tsx:355,2421`) — the
  GREEN cleanup should sweep dangling references to the deleted components.
- Mutation probes on the critical tests (route removals, picker eligibility) belong to
  CHECK, not RED — deferred per the elicify-test-writing RED boundary.
- Pre-existing, not this pack's: the `check-vitest-coverage.mjs` warning that the
  `tests/design-system/` CI pattern matches no test file (unchanged by this work).
