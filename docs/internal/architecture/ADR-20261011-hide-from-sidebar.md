# ADR-20261011 — Hide from sidebar: contract shape, and what the row menu's Remove calls

| Header | Value |
|---|---|
| Status | Accepted — implemented on the session-core integration branch; landed in git on 2026-10-12 (combined-review row S2-10) because code comments and tests cite it. |
| Date | 2026-10-11 (decision), landed 2026-10-12 |
| Author | Architect (squad nav-agent-row-menu-20261011); addendum by the squad lead |
| Where the rule lives now | `pkg/workspace/member_config.go::MergeMemberConfig` and `IncomingMemberConfig` (S2-08 moved the merge out of the REST handler); the D8 rule is `pkg/workspace/builtin_roster.go::RefuseBuiltinRemoval`. |
| Source decisions | D1-D9 of the squad's `DECISIONS.md` (founder, 2026-10-11): ⋯ menu with History, Hide from sidebar, Edit, Remove; D7 removes every UI entry for a new chat while keeping the capability dormant; D8 built-in agents cannot leave the default workspace. |

The original architect note follows unchanged, apart from this header.

Squad nav-agent-row-menu-20261011 · architect · 2026-10-11 · Status: decided (founder questions: §7)

**Correction (evidence duty):** team-lead's steer attributed the removed-member main
unreachability to commit `b00ea0fd3`. That commit is the **agent-delete cascade** fix
(`git show b00ea0fd3` — "the agent-delete cascade uses the live session store and stops
sessions first", DEL-10). The M4/M7 removed-member behaviour itself is real and verified —
it lives in `pkg/gateway/rest_gw_gate_test.go::TestGW_M4_*` / `TestGW_M7_*`. The behaviour
the steer describes stands; only the commit attribution was off.

## Context

D5 (DECISIONS.md): Hide from sidebar is per workspace, saved on the server, display-only —
the agent keeps working (tasks, @ mentions); the workspace group shows a quiet
"N hidden · Show" line. D4/D8: Remove takes the agent off this workspace's team only, and
must respect built-in/default-member refusal rules, disabled with a reason. Hard
Constraint #8: the wire shape is decided before any Go/TS code.

Three facts pin the design space:

1. The sidebar roster is built from `workspace.member_configs`, not `core_team`:
   `src/components/layout/sidebar/workspaceRoster.ts::memberRoster` iterates
   `Object.entries(workspace.member_configs)`. The server projects an entry for **every**
   team member whose main resolves, even with no stored settings
   (`pkg/gateway/rest_workspaces.go::workspaceToWireFrom`, C-MAIN block).
2. `member_configs` writes already carry merge semantics with team validation and GC:
   per-agent replace + GC of off-team entries (`rest_workspaces.go::applyUpdate`,
   `pkg/workspace/member_config.go::GCMemberConfigs`), unknown-member reject 422
   (`member_config.go::ValidateMemberConfigs`), all under the revision lock
   (`rest_workspaces.go::loadAndValidateTeam` → `workspace.CheckRevisionLocked`, 409 on
   conflict).
3. Admin is not a member and never can be (`rest_workspace_team.go::validateCoreTeamMembers`
   rejects Admin and System Agents; `contracts/components/schemas/Workspace.yaml::
   admin_main_session_id` — "Admin is not a workspace member; this is not a membership
   entry"). Admin's sidebar row rides `Workspace.admin_main_session_id`, only on the
   default workspace.

## Decision

**D5 shape — `hidden_from_sidebar` (optional boolean) on `WorkspaceMemberConfig`**
(`contracts/components/schemas/WorkspaceMemberConfig.yaml`), stored inside the workspace's
existing `member_configs` map. Update path: the existing `PUT /workspaces/{id}`
(`contracts/openapi.yaml::updateWorkspace` → `WorkspaceUpdateRequest`); **no new field on
Workspace, no new endpoint.**

One-click hide is a minimal legal body — `WorkspaceUpdateRequest.yaml` requires only
`revision` and `minProperties: 2`:

```json
PUT /api/v1/workspaces/{id}
{ "revision": <n>, "member_configs": { "<agentId>": { "hidden_from_sidebar": true } } }
```

**One documented semantics exception (required):** inside an incoming member config,
`hidden_from_sidebar` **absent ⇒ preserve the stored value** (wire type `*bool`/`boolean`,
tri-state; store as `bool` in `pkg/workspace/workspace.go::MemberConfig`). Reason: the
existing per-agent semantics are wholesale replace
(`rest_workspaces.go::applyUpdate`: `rw.ws.MemberConfigs[agentID] = mc`) — a minimal hide
body without the exception would silently wipe the member's stored heartbeat
(`rest_workspace_team.go::workspaceMemberConfigsFromWire` maps only present pointer
fields). Heartbeat keeps its documented replace semantics unchanged; only the new field
gets preserve-when-absent, stated in both schema descriptions. Backend-lead must extend the
`member_configs` rewrite in `scripts/gen-go-fixup.go` for the new field (flagged as the
translation hook by `rest_workspaces.go::workspaceToWireFrom`'s comment).

**Remove — reuse the Team-panel path, no second removal mechanism.** The row menu calls
the same mutation the Team tab saves with
(`src/components/workspaces/WorkspaceTeamTab.tsx::saveFn` →
`src/lib/api/workspaces.ts::updateWorkspace` with `{revision, core_team: members-minus-id,
delegation: edges}`, keeping the load-bearing core_team-before-edges order). Server side
this is the same `handleWorkspacePut` — GC of the member_configs entry is automatic
(FIX-4a branch of `applyUpdate` + `GCMemberConfigs`).

### Why per-member field, not the alternatives

| Option | Verdict | Reason (evidence) |
|---|---|---|
| (a) `hidden_from_sidebar` on `WorkspaceMemberConfig` | **Chosen** | GC free: off-team entries are pruned on every relevant PUT (`applyUpdate` both the `mcPresent` and FIX-4a branches). Validation free: non-member key → 422 (`ValidateMemberConfigs`). Read path free: the roster already iterates exactly these entries (`memberRoster`). Stored flag defaults to false on old workspace JSONs — no migration. |
| (b) top-level `Workspace.hidden_agent_ids` array | Rejected | Creates a second roster-like array that must be kept a subset of `core_team` by hand — new prune logic in `applyUpdate` plus a new membership validator. The codebase already paid for exactly this class of drift once (`rest_workspace_team.go::RepairDanglingCoreTeamMembers` exists because dangling `core_team` ids accumulated). |
| (c) new endpoint (`POST …/hidden-agents` etc.) | Rejected | Duplicates a revision-guarded mutation surface that already validates, GCs, audits and locks; more contract and handler area for zero capability. |

### Rules (Q3)

- **Hidden ids must be members:** free — any `member_configs` key not on the effective
  core team 422s (`ValidateMemberConfigs`; effective team = request value when the same
  PUT also edits `core_team`, `rest_workspaces.go::prepareWorkspacePutMembers`).
- **Agent leaves later:** the whole entry — flag included — is GC'd
  (`GCMemberConfigs`). Re-adding the agent starts **visible**; the main itself is the
  retained identity and re-add reveals the same main (`applyUpdate` GC comment).
- **Minor, recommended:** mirror the worker-heartbeat rule — reject
  `hidden_from_sidebar: true` for a non-main-eligible member (worker) with 422; the
  `isWorker` predicate is already injected (`ValidateMemberConfigs`). Sidebar rows only
  exist for main-eligible colleagues (`src/lib/nav/eligibleMains.ts::isEligibleColleague`),
  so the flag is inert elsewhere; the check just keeps the API honest. Backend-lead may
  drop this without re-review.
- **D8 — who cannot be Removed:** (i) **Admin** — not a member, no Remove at all;
  (ii) **built-in roster members on the default workspace** — `rest_workspace_team.go::
  ensureBuiltinRosterPresent` re-adds missing built-ins to the default workspace at every
  boot, so a successful remove would be silently resurrected on restart. The row menu
  disables Remove there with that reason (`Agent.locked` is on the wire,
  `contracts/components/schemas/Agent.yaml` + `rest_agents.go`: `ag.Locked = ac.Locked`);
  recommended server-side guard: 409 "built-in roster member cannot leave the default
  workspace" so the API never lies about a removal the next boot undoes. This executes
  D8; it is not a new founder question. Built-ins on **non-default** workspaces are
  removable (nothing re-adds them there). (iii) System Agents — never members, never
  rendered.
- **Who can be Hidden:** every member row, **including locked/built-in members** — D8's
  refusals govern membership and deletion, not display; hiding is reversible in one click
  via the "N hidden · Show" line, which would be unable to restore a locked agent if it
  could not be hidden. **Admin's row: not hideable** in this shape (no member entry to
  carry the flag) — see §7.

### Conflict behaviour (Q2)

`PUT /workspaces/{id}` suffices; no contract change needed for the update path. Revision
stale → 409 via `CheckRevisionLocked` (`rest_workspaces.go::loadAndValidateTeam`). SPA
behaviour on 409 from the row menu: refetch the workspace detail, recompute the flag from
the fresh record, retry once; a second conflict surfaces an error toast. This matches the
existing expectation that mutation helpers reject with `status: 409`
(`src/lib/api/configuration-revisions.test.ts`). No new wire types.

### "Remove" and the main chat (Q4)

Removal never deletes the agent's main chat — the founder ruling is already how the code
behaves: the `core_team` PUT GCs the member_config but releases **no session** — "a main
is a RETAINED identity … re-adding the same pair reveals the same id"
(`rest_workspaces.go::applyUpdate`). The retained main of a removed member is unserved
(list, rename, per-agent session list, WS attach+send) while kept
(`rest_gw_gate_test.go::TestGW_M4_*`), and is released only when the workspace itself is
deleted (`TestGW_M7_WorkspaceDeleteReleasesMainsOfEarlierRemovedMembers`). Only real agent
deletion deletes the main, and that is the separate Delete flow, refused for locked agents
with 403 `agent_locked` (`rest_agents.go` delete handler). Main-session projection stays
consistent with no extra code: off-team ⇒ no projected `member_configs` entry
(`workspaceToWireFrom` C-MAIN loop iterates `w.CoreTeam`) ⇒ no sidebar row. Removal also
clears the hidden flag for free under this decision (GC drops the entry) — under option
(b) it would have needed manual pruning; one more reason for (a).

### SPA read path (Q5)

Today: `memberRoster` (pairs each member_configs entry with its agent) →
`src/lib/nav/eligibleMains.ts::eligibleMainAgents` (keeps `core`/`Main` colleagues with a
validated `main_session_id`; workers and Admin excluded) → rows. Hide slots in at
`src/components/layout/sidebar/workspaceRoster.ts::workspaceMains`: split `result.rows`
into visible rows and hidden rows by `member.hidden_from_sidebar === true`; the
"N hidden · Show" line takes its count and ids from the hidden rows (names come from the
already-loaded agents map). **Attention recommendation (decided):** compute
`signals`/`onCount`/`unknown` over **all** rows, hidden included, so a hidden agent's
attention still counts on the collapsed workspace row's dot/label
(`WorkspaceAgentList.tsx` consumes `mains.onCount`). Hiding removes a row, it does not
mute the "a main chat needs you" signal (`docs/workspaces.md`, "Chats" section) — D5
promises the agent keeps working; silently dropping its attention would break that
promise. Admin's default-workspace row is outside this shape (no member entry) and is
never hidden. Un-hide ("Show") is the same PUT with `hidden_from_sidebar: false` (bulk:
list every id in one `member_configs` body).

## Consequences

**Positive.** One stored flag riding an existing, revision-guarded, GC'd, validated map;
no migration; no second removal path; generated types change by one optional field
(`src/lib/api/generated/` + `pkg/api/generated/`); the SPA filter is local to the roster
module.

**Negative.** Mixed merge semantics inside one object (heartbeat replaces wholesale,
`hidden_from_sidebar` preserves when absent) — must be stated verbatim in
`WorkspaceUpdateRequest.yaml` and `WorkspaceMemberConfig.yaml` or the next client
reintroduces the heartbeat-wipe bug. Locked-member hiding is a product-visible permission
the founder has not worded himself (derived from D5+D8, flagged below).

**Neutral.** Core is concurrently editing contracts (AgentColor palette, session titles) —
expect gen-contracts conflicts; resolve by regenerating after merging, never hand-editing
generated files. E2E fallout (D7 button removal, `tests/e2e/fixtures/selectors.ts::
startNewChat`) is qa-lead's listed work, unchanged by this shape.

## Documentation pages to update (Q6, docs/ outside docs/internal/)

| Page | Section | Change |
|---|---|---|
| `docs/workspaces.md` | "Each agent's own chat, per workspace" | ⋯ menu replaces the two icon buttons; Hide/Show line; Remove = leaves this workspace's team, history kept, agent still reachable |
| `docs/using-omnipus-ui.md` | Sidebar section (Workspaces table row + agent-row controls paragraph, "two quiet icon buttons") | Rewrite for the ⋯ menu, hidden rows, "N hidden · Show", removed-agent rows |
| `docs/getting-started.md` | Mentions "Past sessions"/"New chat with" | Same row-control rewording |
| `docs/agents.md` | Locked-agent rules / team membership | Remove refusals (built-ins on the default workspace), locked agents hideable, deletion still the only main-chat delete |

## Founder questions (Q7)

One genuine gap; everything else is settled by D1–D9 plus the code cited above.

- **Q: May Admin's default-workspace sidebar row be hidden (and what does its ⋯ menu
  offer)?** Admin is not a member, so the per-member flag has no surface for it; hiding
  Admin would need a new top-level field — a scope extension D5 does not describe.
  Options: (A) Admin's row not hideable, not removable — menu shows History, with
  Edit/Delete disabled per existing locked-agent rules (**recommended**, zero new
  contract area); (B) add `admin_hidden` or widen the shape to non-members — new contract
  surface, new founder decision. If the founder wants A, no further input needed.

— architect, squad nav-agent-row-menu-20261011

## Addendum 2026-10-11 (squad-lead, after gate round 1 and founder ruling "hiding is only hiding, keep it as simple as possible")
Supersedes the "preserve every omitted field" behaviour that landed in b4b573f4e. Merge semantics for PUT /workspaces/{id} member_configs are EXACTLY the pre-feature semantics (an incoming member config REPLACES the stored one; omitted heartbeat clears it, with the disable transition), with ONE narrow exception pair, both about the new display flag only:
1. `hidden_from_sidebar` absent in an incoming config => the stored flag is preserved (so a heartbeat save never un-hides).
2. A "pure hide/show" entry - `hidden_from_sidebar` present AND `heartbeat` absent - preserves the stored heartbeat (so hide/show never changes any other setting). An entry with neither field ({}) keeps the old replace behaviour (clears the heartbeat; the flag is preserved by rule 1).
No new field, no new endpoint. Contract descriptions (WorkspaceMemberConfig, WorkspaceUpdateRequest, Workspace) must state exactly this.
Founder rulings same day: (a) the built-in-removal refusal runs before ANY side effect (no plan pause/resume before the 409); (b) the REST PUT and the agent tool pkg/sysagent/tools/workspace.go update_workspace share ONE rule/helper: built-ins cannot be removed from the default workspace by anyone, and every removal cleans up the member's config including hidden_from_sidebar; (c) the 409 carries a documented error code and the user-facing text "Built-in agents can't be removed from this workspace".
