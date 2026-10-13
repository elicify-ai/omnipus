# Feature Specification: The Omnipus system agent

**Created**: 2026-10-12 · **Fix round 1**: 2026-10-13 · **Fix round 2 (final)**: 2026-10-13
**Status**: Draft — scope round 2026-10-13 added group chats and conversation awareness; two further grill rounds follow (founder exception)
**Size**: Feature (spec-driven; 5-reviewer gate; every engine/WebSocket bundle needs hands-on UAT by a tester and an independent validator on the exact landing commit — root `CLAUDE.md`, "Definition of Done")
**Release**: v0.1.1 (founder answer R1 A, 2026-10-12, relayed by team-lead)

| Source | Link | Role |
|---|---|---|
| ADR | [ADR-20261011 — The Omnipus system agent](../architecture/ADR-20261011-omnipus-system-agent.md), including its dated sections "Founder answers after correction (FQ-17, FQ-18)" and "Founder answers to specification questions" | Architectural authority; decisions **OSA-n**, code facts **C-n** |
| Founder decision record | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md` | Product authority, **F:Dn** |
| Founder answers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md` — FQ-1…FQ-18, "After the correction round", "Spec open questions OQ-1..OQ-4", "Spec grill round 1 questions" | **FA:…**; later sections win |
| Visual draft | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html` | Flows W1–W18 and screens, **WF** |
| ADR grill | [ADR review](../architecture/ADR-20261011-omnipus-system-agent-review.md) at `cdab144f0` | Negative controls owned in §9.5 |
| Spec grill round 1 | [Spec review](omnipus-system-agent-spec-review.md) at `54c38386b` | Findings dispositioned in §0.1 |
| Spec grill round 2 (final) | [Spec review round 2](omnipus-system-agent-spec-review-round2.md) at `12e73f653` | Findings dispositioned in §0.4 |
| Channel person routing | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/channel-person-routing-decisions-20261013.md` (R1–R5, PR-1–PR-4) | **In scope** by founder decision 2026-10-13 (US-16, ADR OSA-14). Not yet grilled |
| Group chats and awareness | Founder answers "After spec fix round 2" (GC-1–GC-6, RQ-1–RQ-4, AW-1, AW-2); issues #1068, #892, #955 | US-17–US-20, ADR OSA-15/OSA-16. Not yet grilled |
| Dependency | GitHub issue **#1277** (open) — "Gateway refuses to start or reload when an enabled channel's secret is missing" | Prerequisite for removing an in-use secret (FR-077). Its fix is **not** specified here |

**Reading guide.** §0 records the fix round. §1–§6 describe behaviour only. §7 onward (contracts, BDD, TDD plan, requirements, traceability, reachability, documentation, work units) may name code. IDs: **FR-nnn**, **BDD-u.n**, **SC-nnn**, **OQ-n**. The plan-spec skill's interactive gates are served in this flow by the two grill rounds and the founder interviews after each (root `CLAUDE.md`, "Spec-Driven Workflow").

---

## 0. Fix round 1 (2026-10-13)

### 0.1 Grill round 1 findings → change

| Finding | Disposition | Change |
|---|---|---|
| CRIT-001 private secrets lack the workspace boundary | Fixed (FA:OQ-1) | Scope is `(workspace, agent, variable)` for storage, listing, replacement, removal, recipient comparison and injection, taken from the trusted execution context or Omnipus's validated setup target (FR-065, FR-076); card shows the owner pair (K1/K2); two-workspace and concurrent fixtures (BDD-7.5, 7.6; dataset D6). |
| MAJ-001 superseded staging lifecycle | Fixed (FA:OQ-3, Q-R1-1 A) | Staging, promotion and expiry removed. Submit stores durably under a new versioned reference; Cancel/Stop before Submit stores nothing; after Submit the secret stays, unused, listed and removable (FR-063, FR-066, FR-067, FR-072; BDD-3.2, 6.5, 6.14). |
| MAJ-002 legacy named-agent unbound channels | Fixed (FA:OQ-2 B) | Routing writes require workspace + agent in REST and the Channel settings panel; every unbound instance, named agent or not, fails loudly "bind this channel to an agent in a workspace" (FR-121, FR-127, FR-128, FR-129; K14; D3 row 6). |
| MAJ-003 submission-unknown recovery | Fixed | Value-free request-state read endpoint plus recent terminal states in the session snapshot (FR-079; K4, K5b). |
| MAJ-004 wire omission and runtime validation | Fixed | K8 adds the "new session needs an agent" rule; REST paths/methods named; inbound mirror `pkg/gateway/inboundschemas/` listed; semantic checks hold with inbound validation on and off; malformed-payload tests through real handlers (§7.2, tests 62–64). |
| MAJ-005 per-caller replacement oracle | Fixed | §6.4 table: each of the 16 call sites plus the secondary fallbacks gets an owner and a visible failure (FR-122–FR-126, FR-131, FR-132). |
| MAJ-006 matrix presence ≠ coverage | Fixed | Singleton-chat, exclusive-authority, real `list_secrets`, D3 readiness, BDD-1.4/1.5 tests added; every negative control owned (§9.5); leak scan uses non-empty captures and a positive sentinel (test 31). |
| MAJ-007 God Mode vs missing information | Fixed | God Mode skips approvals and the Go question only; clarification questions and secret cards stay (FR-023; BDD-3.4, 3.7, 3.8). BDD-2.2 split into a genuinely ambiguous case (must ask, 2.2) and a nested-workflow case (2.5). |
| MAJ-008 screen states and keyboard | Fixed | §6.3 screen/state table; keyboard and focus contract widened to navigation, picker, tabs, editor entry and completion (FR-109, FR-112; BDD-12.13, 12.14). |
| MAJ-009 tools granted only to Omnipus | Fixed | §6.5 grant inventory (ordinary roles, custom constructor, hidden-engine and external exclusions, executable and reload wiring); real dispatch from a fresh built-in and a fresh custom agent (BDD-6.18; test 65). |
| MAJ-010 inputs exceed execution/masking | Fixed | Variable grammar, protected names, 4–16,384 byte values, NUL rejected, uppercase-only names, per-child injection (FR-069, FR-071, FR-076; dataset D6); US-6 promise qualified for the private kind. |
| MAJ-011 shared provider/connector writes | Fixed | §6.6 shared-resource table; expected-before precondition under the existing config write lock for provider, connector, channel and mailbox writes (FR-085; BDD-11.9). |
| MAJ-012 picker offers ineligible agents | Fixed | Picker = team-eligible, not-yet-member agents, excluding Omnipus and hidden engine agents (FR-104); graph keeps Omnipus's editable self-row (FR-111; BDD-12.12). |
| MAJ-013 secret endpoint trust contract | Fixed (with FA:Q-R1-2 B) | §6.7 security and user promises; submit trust checks with zero writes on refusal; value-free audit; bounds; no password step recorded as an explicit exception with a security-lead verification gate (FR-083, FR-084; BDD-6.15–6.17). |
| MIN-001 browser warm-up | Fixed (FA:OQ-4 B) | Warm the default workspace's browser (FR-126; BDD-13.11). |
| MIN-002 component mapping | Fixed | §6.8 composition table and named design-system gates (FR-110). |
| OBS-001 landing bundles | Adopted | §15: preparation commits vs landable bundles; WU-10 and WU-11 in the UAT list; WU-11 re-sized Feature; security-lead reviewer only. |

Tally: 1 critical fixed, 13 major fixed, 2 minor fixed, 1 observation adopted. No finding was overridden.

> **Correction (2026-10-13, fix round 2):** this tally said all 13 major findings and the landing observation were fixed. That was wrong: grill round 2 showed six of them (MAJ-001, 003, 006, 009, 010, 011) and OBS-001 were only partly fixed. Their residuals are closed in §0.4 (R2-MAJ-001…007, R2-MIN-001).

### 0.2 Founder answers applied in this round

| Answer | Applied |
|---|---|
| OQ-1 — private secrets per workspace + agent; wording A confirmed | FR-076, FR-065, US-7, docs TODO |
| OQ-2 B — every channel bound to an agent in a workspace; unbound mode removed | FR-121, FR-127–FR-129, K14, UI |
| OQ-3 — stored on card submit, no staging, no expiry | FR-063, FR-066, FR-067 |
| OQ-4 B — warm the default workspace's browser | FR-126 |
| Q-R1-1 A — replacing an in-use secret saves a new reference/version | FR-072, BDD-6.14 |
| Q-R1-2 B — no password step on the card; raw routes keep it; security-lead verifies | FR-083, §6.7, WU-4 gate |

### 0.3 Dated notes where founder answers supersede ADR text (2026-10-13)

| ADR text | Superseded by | Note |
|---|---|---|
| OSA-7 "staging envelope … promote on consumption … expiry" | FA:OQ-3, Q-R1-1 A | Secrets are stored durably on submit under a new reference; there is no staging or expiry. Recipient binding (OSA-7.3) stays. Recorded in the ADR's dated section "Founder answers to specification questions". |
| OSA-7.8 agent-private bound to "(agent, variable)" | FA:OQ-1 | Bound to (workspace, agent, variable). |
| OSA-12 Omnipus is the default agent | FA:FQ-17 (already in the ADR's dated section) | No default agent at all. |
| ADR-029 legacy unbound channel routing | FA:OQ-2 B | Removed; every channel is bound to an agent in a workspace. |
| ADR-022 password re-confirmation for credential writes | FA:Q-R1-2 B | Not required for the scoped secret card only; raw credential routes unchanged. |

### 0.4 Fix round 2 (2026-10-13) — final correction

| Finding | Disposition | Change |
|---|---|---|
| R2-MAJ-001 private version selection; truthful Stop/unused | Fixed | Three reference states: **stored** (not selected), **selected** (in use by its recorded consumer), **superseded**. First submit for a private variable with no selected version is selected at once (nothing is in use to keep). A replacement is stored only; the owner pair selects it through the new ask tool `select_secret_version` (scoped to the caller's own pair; Omnipus for a validated target) — no management grant to ordinary agents, no resolver exception. Cancel/Stop rule qualified: a not-yet-selected reference stays unused; selected references and completed changes stay and are reported (FR-064, FR-067, FR-072, FR-076, FR-086; BDD-6.5, 6.19–6.23, 7.8; K6). |
| R2-MAJ-002 durable Submit vs request state | Fixed | A value-free **submit receipt** (request id → reference names, time) is written in the **same atomic credential-store publication** as the values; it is the commit fact. On restart the request state is derived from the receipt, so a committed submit is always `submitted`; no receipt means nothing stored (`interrupted`). Resume carries references only and is re-dispatched once, idempotently by request id (FR-087; BDD-6.24; test 86). |
| R2-MAJ-003 shared-write boundary | Fixed | One boundary: the gateway config write lock, lock order **config lock → agent-loop lock, never the reverse**. Settings writes already hold it; tool writes to the four shared resources acquire it before entering the agent-loop mutation. Before-state is a secret-free **revision token** per resource (absent = `absent`) returned by the read tools; tools take `expected_revision` (§6.6; FR-085, FR-088; BDD-11.9 outline incl. Settings contention; tests 75, 88). |
| R2-MAJ-004 gateway environment vs integration resolution | Fixed | The gateway-environment prohibition applies to the **agent-private kind only**. Provider keys and mailbox passwords keep the existing boot/reload resolution, which places them in the gateway process environment (`credentials.InjectFromConfig`); children never inherit them (child allowlist). Card copy and docs say so (FR-062, FR-076, §6.1, §6.7). |
| R2-MAJ-005 masking across refresh/sign-in/restart | Fixed | One function computes the **complete** sensitive set (configured references, OAuth values, and every stored card secret of every kind, version and state); every full-set rebuild — boot, reload, Settings refresh, sign-in/token refresh, config clone — uses it; registrations are serialized so a stale computation cannot replace a newer one (FR-069, FR-089; BDD-6.25; tests 89, 90). |
| R2-MAJ-006 first landable bundle | Fixed | B1–B3 are now **preparation stages** on the feature branch (CI + qa-lead CHECK per stage, not landed). There is **one landing**: the complete feature after the 5-reviewer gate, same-change docs and exact-commit UAT. The permission split (FR-024/FR-025) moves into WU-2 so it is committed together with Omnipus's grants (§15). |
| R2-MAJ-007 protected names | Fixed | Protected set = child allowlist names and prefixes (`LC_`, `XDG_`, `OMNIPUS_CHILD_`), `OMNIPUS_`, `LD_`, `DYLD_`, the setup layer, and the document-runtime layer (`PATH`, `PYTHONPATH`, `NODE_PATH`, `XDG_CACHE_HOME`, `HOME`, `TMPDIR`, `OSL_SOCKET_PATH`, `LANG`, `LC_ALL`, `OMNIPUS_DOCUMENT_SKILLS`). Private variables are appended **after** all managed layers (the document layer strips names containing KEY/TOKEN/SECRET/CREDENTIAL/PASSWORD from inherited input, so earlier injection would lose them). Drift test runs the real composers on all three child paths with the document runtime enabled (D6; BDD-7.7, 7.10; tests 72, 91). |
| R2-MIN-001 inherited controls | Fixed | §9.5 now owns: listing-OK/selected-model-unavailable, shared-skill contention, multi-consumer removal with failed reload / stale live value / next boot, policy reload during helper execution, target-shelf create/remove/discovery, target `environment_setup`. §9.4 names concrete existing test files (BDD-4.8, 9.7, 11.10, 11.11, 15.6). |
| R2-OBS-001 composite evidence | Adopted | §13: the UAT evidence pack separates screen-level keyboard/focus/announcement, touch/reflow and reduced-motion checks from catalogue/manifest/Storybook checks. |

Tally: 0 critical, 7 major fixed, 1 minor fixed, 1 observation adopted. No finding overridden.

### 0.5 OQ-5 and channel person routing (founder, 2026-10-13)

The founder first answered OQ-5 "keep the per-person/per-group rules; separate feature", then the same day decided that **channel person routing is part of this Omnipus implementation** (team-lead steer, 2026-10-13; decisions PR-1–PR-4). It is specified here as **US-16** (FR-140–FR-156, BDD-16.x, K18–K22, WU-14) and recorded in the ADR as **OSA-14**. It **reuses** the bindings engine (`Config.Bindings`, `BindingMatch`, the `ResolveRoute` ladder), `allow_from`, the session-key scopes behind `session.dm_scope`, `session.identity_links` and `group_trigger`, and closes their gaps. **This scope was added after both grill rounds and has not been grilled**; team-lead will ask the founder about a focused review. "No default agent" and loud failure for unrouted messages stay.

### 0.6 Design choices made in fix round 2 (derived from founder answers; visible, non-blocking)

| Choice | Derived from |
|---|---|
| First submit of a private variable with nothing selected is selected at once | Q-R1-1 A protects an *in-use* value; with none in use there is nothing to keep |
| Private replacement is selected by an Ask tool scoped to the owner pair | Q-R1-1 A ("approved change tool selects"); FQ-3 A (no management grants to ordinary agents) |
| Private secrets reach document-runtime runs too, appended after the managed layers | FQ-18 / OQ-1 ("injected into that agent's own sandboxed commands") |

### 0.7 Scope round (2026-10-13) — group chats, channel conversation control, awareness

The founder added scope after fix round 2 and asked for two more grill rounds over the whole spec (founder exception to the two-round rule). Applied here, **not yet grilled**:

| Answer | Applied |
|---|---|
| RQ-1 — per-person channel conversations in the sidebar, hidden by default, can be un-hidden; also in the agent's History | FR-152, K29; BDD-16.14 |
| RQ-2 A — fixed stranger text, once per stranger per connection per 24 h; count and last stranger shown | FR-145; BDD-16.3 |
| RQ-3 A — an allowed group admits all members; strangers inside an allowed group are logged only; direct strangers get the reply | FR-145; BDD-16.15 |
| RQ-4 A — global `session.dm_scope` retired (ignored with warning); per-connection mode, default main chat, no restart; `identity_links` stay global | FR-147, FR-148; BDD-16.16 |
| GC-1–GC-6, #1068 — group chats per workspace: team room, squads, ad-hoc groups | US-17; FR-160–FR-176; ADR OSA-15 |
| #892 — only authorised people resume a stopped channel group conversation | US-18; FR-180–FR-183 |
| #955, GC-6 — channel commands are conversation-scoped; admin commands web/CLI only | US-19; FR-185–FR-188 |
| AW-1, AW-2 — the main session knows the agent's other conversations in the same workspace | US-20; FR-190–FR-198; ADR OSA-16 |
| GC-5 — no limit on agent-to-agent replies (stress-tested) | Accepted risk **R-A4** (§6.7, §19) |
| GC-7 — self hand-off from a group to the agent's own main session | Removed by GC-8 (§0.9) |

**Reuse, not new systems.** Group chats are rooms (§0.9, GC-8): they reuse the session-core request/answer path — `AdmitRequest` into members' main sessions, guest replies into the room via `replyIntoConversation`/`PublishGuestReply` — the request ledger, receiver admission policy, Stop, attention and session stats for cost. Awareness reuses the idle recap (`runRecap` already returns `recap` and `worth_remembering`), session metadata and the memory rooms behind `remember`/`recall_memory`. Channel commands reuse the per-chat `Session.model`.

**Found while designing (reported, fixed in scope):** the recap's carry-forward file `last-session.md` lives in the agent's private memory room, which is agent-wide (`pkg/agent/memory.go::WriteLastSession`, `ReadLastSession`), so one workspace's recap can reach the agent's next session in another workspace. AW-2 and the security rule "digests must not leak across workspaces" require scoping it per workspace (FR-197).

### 0.8 Founder answers GQ-1–GQ-4 and screen alignment (2026-10-13)

| Answer | Applied |
|---|---|
| GQ-1 A — the @all default applies to the user's messages only; agents reach others only by @-naming them or writing @all | FR-165; D7 row 7; BDD-17.19 |
| GQ-2 C (overrides the architect's recommendation A) — one notice "The assistant is paused." per stop in the group; those messages are not processed; the operator still sees them | FR-182; BDD-18.1, 18.3 |
| GQ-3 — founder's own design: a per-participant **steering flag**, off by default, enabled explicitly per participant; when on, steering works as in the web chat, conversation-scoped, except UI-only commands | FR-185–FR-189; US-19 rewritten; BDD-19.x; K32 |
| GQ-4 A — the user can mute an agent in the team room (not addressed by @all there; stays in the workspace) | FR-158; BDD-17.18; K23/K24 |
| Screen alignment (wireframe R34/R35) | Routing settings live in **the connection's Configure side panel on the Connectors page**; the derived "who can reach <Agent>" list lives in the shared agent editor slide-over (there is no Agents page). |

**Design choices made for GQ-3 (visible, non-blocking):** the flag is set per **individual** person entry (allow list or person table); a group entry admits members but never entitles them to steer; in web group chats the flag does not apply (the user steers as in any web chat; agents never steer — FR-168); `/help` is answered for every allowed participant; an un-entitled participant's command is not executed and not passed to the agent, and is answered "Commands are not enabled for you in this conversation." at most once per participant per conversation per 24 hours (logged every time).

### 0.9 Group chats redesigned as rooms (founder GC-8, GC-9, 2026-10-13)

GC-8 supersedes the "hidden working session per member" design of §0.7. A group chat is a **room**: a shared conversation the user owns; **no agent works inside it**. Each room message is fanned out over the existing agent-request path (`pkg/agent/address_router.go::AdmitRequest` appends the request to the receiver's **main** session); each agent works in its own main session in that workspace; its answer returns into the room over the existing reply path (`replyIntoConversation`, authored by the responder). GC-9 A: room traffic shows in each agent's main chat as compact, collapsed entries ("From group <name>: …", expandable), visible to the agent and the user.

| Consequence | Where |
|---|---|
| Main sessions see room traffic addressed to them automatically, so awareness (AW-1/AW-2) covers only non-room conversations | FR-190 |
| The GC-7 self hand-off is **removed**: rooms no longer need it, and the only other non-main conversations (per-person channel conversations) already show their approvals and questions to the user in the workspace, so no case needs it (FR-177–FR-179, tests 128–130 and K31 removed) | §0.9 |
| A busy agent answers later: the request is queued (or held when the receiver's message policy is `ask`) like any agent request; the room shows each addressed member's state | FR-201 |
| Stop in the room stops the fan-out and cancels pending room requests; a member turn already running is FQ-RM-1 — **superseded by §0.10** (`/pause`, running work untouched) | FR-170 |
| Room cost = the sum of the room-originated turns in members' main sessions | FR-171 |
| Team room, squads, ad-hoc rooms, mute (GQ-4 A), membership audit and the workspace boundary stay as decided | FR-158–FR-162, FR-172 |
| Approvals, questions and secret cards from a member's work appear in **that agent's main chat** (standard), and the room shows "waiting for you in <Agent>'s main chat" | FR-169 |

**Reused:** address book, request ledger, admission (receiver send policy, `ask` hold), `AdmitRequest`, `replyIntoConversation`, `PublishGuestReply`, the steering FIFO, Stop. **New:** the room entity and its transcript; the fan-out (mention resolution, per-member request with room context, state tracking, cancellation); the room UI; the collapsed main-chat rendering. **Removed requirements:** FR-163 (old), FR-167 (old), FR-177, FR-178, FR-179. **Added:** FR-199–FR-203.

### 0.10 Room commands, two-agent minimum, "Add participant" (founder, 2026-10-13)

| Decision | Applied |
|---|---|
| FQ-RM-2 B — rooms need at least two agents; separate-context "extra chats" are a later, separate feature (out of scope); no one-agent topic rooms | FR-161, FR-204; BDD-17.20 |
| FQ-RM-1 resolved — a room is a chat, not an agent-steering tool: `/pause` · `/resume`, `/mute @Agent` · `/unmute @Agent`, `/members`, `/help`; everything else refused "Rooms don't steer agents — use <Agent>'s main chat" with a link. Running main-chat work is not touched by `/pause`: its answer appears "after pause" and its @-mentions are not passed on | FR-170, FR-205, FR-206, FR-158, FR-189; BDD-17.6, 17.17, 17.21–17.23 |
| GC-10 — "Add participant" at the top-left of a chat header adds **agents only**; from an agent's main chat it opens a new room (user + both agents), the main chat stays one-to-one, the room starts empty with a link back; in a room it adds an agent within the existing membership rules; external channel people in rooms are out of scope | FR-207, FR-208; K35; BDD-17.24, 17.25 |

**Design choices (visible, non-blocking):** the team room exists only while the workspace has at least two conversation agents; removing a member that would leave a squad or ad-hoc room with fewer than two agents is refused ("a room needs at least two agents — delete the room instead"), and a workspace change that leaves a room with one agent makes it read-only until another agent joins; muted members count toward the minimum; messages written while a room is paused stay in the room transcript and are **not** delivered on `/resume` (resume affects new messages only); the refusal for other commands names every member with a link to its main chat (or only the @-named agent if the command names one); `/mute` works in every room (GQ-4 A extended by the room command set); "Add participant" is absent in Omnipus's chat; in the team room it is disabled with "Team room members are the workspace's agents — add the agent to the workspace".

---

## 1. Discovery summary

| Topic | Captured requirement | Source |
|---|---|---|
| Actors | The user (single account, web app); Omnipus; its helper sessions; ordinary native agents (Mia, Jim, General Purpose, Planner, Researcher, custom); external-program workers; outside people on chat channels; AI providers, connector (MCP) servers, chat platforms, mail servers | F:D4, D10, D11 |
| Problem | Two overlapping helpers; a blank create form; secrets typed into chat; unverified "done" claims; messages silently routed to a default agent | BRD §1; FA:FQ-17 |
| In scope | One Omnipus agent; workflows as skills; summary + question gate with a narrow ask list; secret card for all native agents including agent-private secrets per workspace + agent; self-checks in change tools; connect-channel and connect-mailbox; helper decisions in the Omnipus chat; screen changes; removal of the Agents screen, create form, Ava, Admin, the default agent and unbound channel routing; **channel person routing per connection** (person table, allow list, main agent, stranger reply, conversation mode, derived per-agent list) | F:D1–D27; FA; PR-1–PR-4 |
| Out of scope | Judge/Planner settings redesign, core security settings (F:D1); team templates (F:D25); secret files and connector sign-in (S5/S6); group chats; the #1277 fix | F:D1, D10, D25; FA:FQ-13 |
| Constraints | Single Go binary, pure Go, two-layer tool policy (Hard Constraint #6), contract-first (#8), greenfield (F:D22), Linux/macOS/Windows | root `CLAUDE.md` |
| Accepted risks | R-A1 foothold via allow tools; R-A2 read-to-web disclosure; R-A3 external-program worker planting — founder-accepted 2026-10-12 | ADR OSA-3 |
| Human evaluation | On a fresh install a user creates a working agent by talking to Omnipus, never types a key into chat text, sees every approval in the Omnipus chat, and sees a loud channel error instead of a silently routed message | F:D2, D10, D27; FA:FQ-17 |

---

## 2. Existing codebase context

GitNexus MCP tools are not available; context is **Grep-derived** at `54c38386b`. Risk levels are inferred; implementing leads run `impact` before editing (rule 9).

### 2.1 Symbols involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/config/defaults.go::defaultToolPoliciesSysagent` | modifies | Ceiling values |
| `pkg/tools/auto_approve.go` (ask-list table) | modifies | ADR-092 D9 ask-list |
| `pkg/coreagent/seed.go` (`NewCustomAgentToolsCfg`, `denyAllThenOverride`, default-agent seed), `core.go`, `prompts_adr090.go`, `role_policies_adr090.go` (`ADR090RolePolicyInventory`, `adr090SparseRolePolicies`), `team_eligibility.go` | modifies | Roster, grants, default-agent removal |
| `pkg/sysagent/tools/agent.go`, `agent_update_adr090.go`, `agent_apply_args.go::writeDefaultSingleton`, `agent_read.go`, `provider.go`, `mcp.go`, `channel.go`, `skill.go`, `skill_authoring.go`, `workspace.go` | modifies | Target workspace, permission split, references, checks |
| `pkg/providers/validate.go::ListModels` | calls | Provider check |
| `pkg/credentials/store.go::Store` | extends | One atomic multi-entry write for a card submit |
| `pkg/config/security.go::RegisterSensitiveValues`, `sensitiveReplacementPairs` | calls | Masking (values > 3 bytes only) |
| `pkg/tools/shell_env.go::composeExecutionEnv`, `pkg/tools/environment_setup_runner.go::setupEnv`, `pkg/sandbox/hardened_exec.go::allowedChildEnvKeys` | extends / reads | Per-child injection of private secrets; protected names |
| `pkg/tools/ask_user_question.go`, `pkg/askuser/registry.go` | calls / text change | Gate question; park/resume and caps pattern |
| `pkg/agent/registry.go::GetDefaultAgent`, `SetDefaultAgentOverride`; 16 call sites (§6.4) | removes | No default agent |
| `pkg/routing/route.go::RouteResolver` | modifies | Loud failure; no fallback |
| `pkg/gateway/rest_channels.go::setChannelRouting`, `validateUnbound`, `persistUnbound`, `getChannelRouting`; `src/components/skills/ChannelConfigPanel.tsx` | modifies / removes | Mandatory binding |
| `pkg/gateway/rest_inbound_validate.go::decodeAndValidate`, `ValidateInboundFrameJSON`; `src/lib/ws.ts::parseFrameSafe` | calls | Runtime validation |
| `pkg/gateway/ws_tool_approval.go`; `src/store/toolApproval.ts::isApprovalInScope`, `reconcileWithSessionState` | modifies | Root-session approvals |
| `pkg/gateway/websocket_chat.go` (kickoff, `resolveTargetAgent`), `rest_sessions.go` (`createSessionHTTP`, `firstChatTargetAgentID`) | modifies | Kickoff; no fallback; singleton |
| `pkg/gateway/gateway_boot_browser.go::pickWarmBrowserManager` | modifies | Warm default workspace |
| `pkg/gateway/rest_settings.go` (`HandleCredentials`, `requireReAuth`) | reads (unchanged) | Raw routes keep re-auth |
| `pkg/gateway/rest.go::restAPI.configMu` | calls | Expected-before precondition under the config write lock |
| SPA: `src/components/screens/AgentListScreen.tsx`, `src/routes/_app/agents*.tsx`, `admin.chat.tsx`, `src/components/agents/CreateAgent*`, `wizard/*` | removes | F:D18, D19, D23 |
| SPA: `WorkspaceTeamTab.tsx`, `team/AddAgentPicker.tsx` (`candidates`), `team/teamGraphModel.ts`, `Sidebar.tsx`, `useSelectSession.ts`, `workspaceEntry.ts`, `useWorkspaceSetupKickoff.ts` | modifies | Screens |
| `pkg/skills/embedded/*` | modifies | Omnipus workflow skills replace Ava/Admin skills |

### 2.2 Impact assessment (inferred)

| Symbol | Risk | Direct dependents (Grep) |
|---|---|---|
| `GetDefaultAgent` removal | **HIGH** | 16 call sites (§6.4) |
| `Agents.Defaults.DefaultAgentID` removal | **HIGH** | `seed.go`, `config.go`, `validate.go`, `loop_construct.go`, `loop_config.go`, `instance.go`, `registry.go`, `agent_read.go`, `agent.go`, `agent_apply_args.go`, `route.go`, `gateway.go`, `gateway_boot_roster.go`, `rest_agents.go`, `rest_agents_update.go`, `gateway_boot_browser.go`, `channel_ownership.go` |
| Unbound channel routing removal | **HIGH** | `rest_channels.go`, `route.go`, `ChannelConfigPanel.tsx`, `ChannelRouting.yaml`, e2e `channel-routing.spec.ts`, `channels-routing.spec.ts` |
| Credential store extension | **HIGH** (security) | Boot credentials, every `*_ref` reader |
| `isApprovalInScope` | MEDIUM | Approval modal, snapshot reconcile |
| Ceiling values | MEDIUM | Reconcile, seed/drift tests |

### 2.3 Relevant execution flows

| Flow | Interaction |
|---|---|
| Inbound channel message → route → agent turn | Bound pair only; loud drop otherwise (US-13) |
| Chat frame → session → turn → tool → policy → approval | Ask list; root-session display (US-3, US-11) |
| Delegate run → helper → parent inbox | Omnipus helpers (US-11) |
| Workspace create → setup kickoff | Retargeted to Omnipus's main (US-5) |
| Child command execution (bash, background, environment_setup) | Private-secret injection (US-7) |
| Boot → credentials → channels; boot → browser warm-up | #1277 dependency; default workspace warm-up |

### 2.4 Cluster placement and reference patterns

Spans `pkg/agent`, `pkg/sysagent`, config/policy, credentials, sandbox env, gateway, routing and the SPA — cross-tree, feature size, security-lead review mandatory. `docs/reference/go-implementation/` does not exist (checked); existing in-repo mechanisms are reused.

---

## 3. User stories and acceptance criteria

### US-1 — One Omnipus agent replaces Ava and Admin (P0)

The user has one technical partner. A fresh install has Omnipus and no Ava or Admin. **Why P0**: everything runs through Omnipus. **Independent test**: fresh install → agents list `omnipus`, not `ava`/`admin`; one Omnipus chat; role tools match.

1. **Given** a fresh install, **When** the gateway starts, **Then** exactly one system agent named Omnipus exists and no Ava or Admin.
2. **Given** any create or rename, **When** the name is "Omnipus" in any letter case or with surrounding spaces, **Then** it is refused with a visible reason.
3. **Given** Omnipus, **When** anyone adds it to a non-default workspace or removes it from the default one, **Then** the change is refused.
4. **Given** the default workspace, **When** its Team list, sidebar rows or add-existing picker render, **Then** Omnipus is absent (its self-row stays visible and editable in the Team graph).
5. **Given** Omnipus, **When** its effective tools are listed, **Then** they match §6.5.
6. **Given** Omnipus's one conversation, **When** any path tries to start a second Omnipus chat, **Then** it is refused and points to the existing one.
7. **Given** a workspace Omnipus is not a member of, **When** Omnipus performs setup there, **Then** it can; **and** no other agent gains that cross-workspace authority.

### US-2 — Workflows as skills, chosen reliably (P0)

**Why P0**: the workflows are the product. **Independent test**: one button start and four free-text requests (one match, a nested match, a genuine ambiguity, no match).

1. **Given** a button naming a workflow, **When** pressed, **Then** exactly that workflow starts for the named workspace.
2. **Given** a request that genuinely fits two unrelated workflows, **When** Omnipus reads it, **Then** it asks which one before loading either.
3. **Given** a request that fits no workflow, **When** Omnipus answers, **Then** no change tool is called.
4. **Given** a running workflow whose skill was trimmed, **When** the trim notice appears, **Then** Omnipus reloads that skill before its next change.
5. **Given** a request whose main workflow contains another as a sub-part (change an agent, which needs a connector), **When** Omnipus reads it, **Then** it loads the main workflow and shows one summary without asking which.

### US-3 — Approval: one summary question, a narrow ask list, God Mode (P0)

**Why P0**: F:D6, D7. **Independent test**: W1 (1 question, 0 approvals), W4 (1 + 1), the same in God Mode (summary message, 0 confirmations; clarification still asked when information is missing).

1. **Given** a workflow outside God Mode, **When** planning completes, **Then** a summary and one Go / Change / Cancel question appear before any change tool.
2. **Given** the user chose Cancel, **When** the workflow ends, **Then** no change tool ran; a secret the user already submitted on a card stays stored, unused, listed and removable.
3. **Given** a call to a tool on the ask list outside God Mode, **When** Omnipus or its helper calls it, **Then** an approval card appears even with Auto-approve on.
4. **Given** God Mode and a fully specified request, **When** Omnipus runs the workflow, **Then** the summary appears as a message and no approval card or Go question appears.
5. **Given** an operator edits Omnipus's tool permissions, **When** they set a per-agent "ask", **Then** it is refused; "deny" is accepted.
6. **Given** God Mode and a request missing required information, **When** Omnipus needs it, **Then** it still asks a clarification question.
7. **Given** God Mode and a needed secret, **When** Omnipus needs it, **Then** the secret card still appears.

### US-4 — Create an agent in this workspace (W1) (P0)

**Why P0**: F:D2, D18. **Independent test**: Add agent in Sales → Go → the agent is in Sales with a main chat and answers.

1. **Given** Sales is active, **When** the user presses Add agent, **Then** the Omnipus chat opens with W1 for Sales and the active workspace stays Sales.
2. **Given** an existing agent fits, **When** Omnipus proposes it, **Then** the proposal adds it unchanged.
3. **Given** uncovered abilities, **When** the summary appears, **Then** each gap is listed.
4. **Given** Go, **When** apply finishes, **Then** the agent is a member of Sales (not the default workspace), has a main chat there, and each part reports working / saved but not working / partly saved / failed.
5. **Given** the plan writes a project skill, **When** applied, **Then** it lands in Sales's shelf.
6. **Given** the new agent's skills need software, **When** the summary appears, **Then** the needed installs are listed up front, and after installation the report says "installed; readiness confirmed at the agent's first use" unless readiness was proven in that agent's own sandbox.

### US-5 — Whole-team setup (W18) and the welcome (W8) (P1)

**Why P1**: high value; W1 covers the minimum. **Independent test**: create "Research" → offer in Omnipus chat and on the empty Team tab → Go → lead, specialists, edges; entry opens the lead.

1. **Given** a new workspace, **When** creation completes, **Then** its team is empty and Omnipus offers "Set up a team for <name>?".
2. **Given** an empty Team tab, **When** shown, **Then** it offers the same setup button; a failed team load shows an error with Retry, never the empty-team offer.
3. **Given** a reused agent, **When** the summary appears, **Then** it is marked "reused, also works in X" and added unchanged.
4. **Given** a reused agent would need a change, **When** proposed, **Then** it is a separate proposal showing before/after and every affected workspace.
5. **Given** the W18 summary, **When** shown, **Then** it has a graph preview and a text list of agents and edges.
6. **Given** apply finished, **When** the user opens the workspace, **Then** the lead's main chat opens.

### US-6 — Secure secret card (P0)

Any native agent can ask for a secret through a protected card. The value never appears in chat, tool arguments, logs, error texts or resume records, and it can only be selected for the recipient the card showed. For the agent-private kind (US-7) the narrower promise applies: a command the user lets the agent run can still reveal it.

**Why P0**: F:D10. **Independent test**: connect a provider via Omnipus; a sentinel-value search over every captured sink (non-empty, with a positive control) finds zero hits; the provider check reports working.

1. **Given** an agent needs a key, **When** it requests one, **Then** a card shows the asking agent, the purpose, the recipient (for example the provider endpoint) and — for a private secret — the owning workspace and agent.
2. **Given** the user submits, **When** the submit completes, **Then** the values are stored durably and the agent's turn resumes with reference names only.
3. **Given** a reference stored for recipient R, **When** a tool tries to select it for R′, **Then** it is refused.
4. **Given** the user cancels the card or presses Stop before submitting, **When** the request ends, **Then** nothing is stored.
5. **Given** a channel session, **When** an agent requests a secret, **Then** it is refused with "open the web app".
6. **Given** the submit response and its notification are lost, **When** the card reconnects or a new tab opens, **Then** it shows the server's state for that request and never asks for the values again automatically.
7. **Given** a secret already in use, **When** the user submits a replacement, **Then** it is stored as a new version; the integration keeps the old one until its approved change selects the new one.
8. **Given** the card, **When** the user submits, **Then** no password re-entry is asked; the raw Settings credential routes still require it.
9. **Given** an unauthenticated, foreign, stale, stopped or changed-recipient submit, **When** it arrives, **Then** nothing is written and no turn resumes.
10. **Given** a stored replacement, **When** it is not yet selected, **Then** it is listed as stored, the current version stays selected, and Cancel or Stop leaves it unused; selected references and completed changes stay and are reported.
11. **Given** the gateway stops right after a submit was stored, **When** it restarts, **Then** the request reads as submitted with its references, never as "nothing stored".
12. **Given** a stored secret, **When** Settings are saved, the configuration reloads, a provider signs in or the gateway restarts, **Then** its exact value stays masked.

### US-7 — Agent-private secrets per workspace and agent (P1)

**Why P1**: FA:FQ-18, OQ-1. **Independent test**: Mia in Sales stores `WEATHER_TOKEN`; Mia's command in Sales sees it; Jim in Sales and Mia in Marketing do not; the exact value is masked in output.

1. **Given** Mia in Sales stored `WEATHER_TOKEN`, **When** Mia runs a sandboxed command in Sales, **Then** the variable is present.
2. **Given** that secret, **When** Jim runs a command in Sales, **Then** the variable is absent.
3. **Given** a command prints the exact value, **When** output returns, **Then** it is masked; a transformed value may not be.
4. **Given** that secret, **When** Mia runs a command in Marketing, **Then** the Sales value is absent (Marketing has its own value or none).
5. **Given** a variable name that is reserved or malformed, **When** stored, **Then** it is refused with the rule.
6. **Given** Mia replaces her Sales `WEATHER_TOKEN`, **When** she has not yet selected the new version, **Then** her commands keep the old value; after the user approves the selection, they get the new one.

### US-8 — Connect a channel or a mailbox (P1)

**Why P1**: F:D11. **Independent test**: connect Telegram to Mia in Sales → one approval → result lists proven and not-tested facts.

1. **Given** a confirmed plan, **When** connect-channel runs, **Then** it configures, binds (agent + workspace), enables and tests in one call behind one approval.
2. **Given** a part the test cannot prove, **When** the result is shown, **Then** it is "not tested" and routing is not "working".
3. **Given** a QR-paired channel, **When** pairing starts, **Then** code and progress appear in the requesting chat.
4. **Given** connect-mailbox, **When** it runs, **Then** read and send readiness are reported separately and no email is sent.

### US-9 — Truthful results from change tools (P1)

**Why P1**: F:D9. **Independent test**: valid key, dead custom endpoint → "saved but not working".

1. **Given** a custom endpoint, **When** a provider is configured, **Then** the check targets that endpoint.
2. **Given** a keyless local provider, **When** configured, **Then** the check runs without a key.
3. **Given** a change that saved some parts and not others, **When** the result is shown, **Then** it says "partly saved" and lists both.

### US-10 — Agents message Omnipus; attention (P1)

**Why P1**: F:D4, D5, D16. **Independent test**: Jim asks Omnipus for a connector; the entry lights only when Omnipus asks the user.

1. **Given** Jim, **When** he messages Omnipus, **Then** it arrives without a delegation edge.
2. **Given** a pending question, approval or secret card in the Omnipus chat (own or from its helpers), **When** the sidebar renders, **Then** the Omnipus entry shows attention.
3. **Given** a plain incoming message and no pending card, **When** the sidebar renders, **Then** no attention shows.
4. **Given** a read-only question from an agent, **When** Omnipus answers, **Then** no change tool runs.
5. **Given** attention cannot be loaded, **When** the sidebar renders, **Then** it shows "unknown", never "nothing pending".

### US-11 — Helpers keep Omnipus available; decisions stay in the Omnipus chat (P1)

**Why P1**: F:D27, FA:FQ-8, FQ-15. **Independent test**: confirm W18, keep chatting; the helper's approval shows in the Omnipus chat naming the helper while workspace B is active.

1. **Given** Go on a multi-step plan, **When** apply starts, **Then** a helper applies and the Omnipus chat accepts new messages.
2. **Given** a helper calls an ask tool, **When** pending, **Then** the card appears in the Omnipus chat naming the helper, with attention, whichever workspace is active.
3. **Given** a helper needs an answer, **When** it asks, **Then** Omnipus asks the user on its own card and never answers for the user.
4. **Given** two plans touch the same agent, skill, workspace or shared setting, **When** the second writes after the first, **Then** the stale write is refused and a changed plan returns to the user.
5. **Given** a confirmed plan for a provider, **When** the user changes that provider in Settings before the helper writes, **Then** the helper's write is refused and the plan returns to the user.

### US-12 — Screens (P0)

**Why P0**: reachability. **Independent test**: walk entry, Team, picker, graph self-row, System agents, the Connectors page's Configure side panel; old routes are gone.

1. **Given** any workspace active, **When** the Omnipus entry is opened, **Then** Omnipus's chat opens without changing the active workspace.
2. **Given** Team, **When** it opens, **Then** list first, graph second; cards show avatar, name, kind, model, other workspaces and actions.
3. **Given** "Add an existing agent", **When** the picker opens, **Then** it lists team-eligible agents not already members (never Omnipus, Judge or Plan Supervisor) and marks agents in no workspace.
4. **Given** Settings, **When** System agents opens, **Then** Judge, Plan Supervisor and Omnipus are editable there, including Omnipus's model with no working model.
5. **Given** an old Agents-screen link, **When** opened, **Then** the not-found screen shows.
6. **Given** a keyboard-only or screen-reader user, **When** they use the entry, Add choices, picker, list/graph switch, card actions, System agents navigation, secret card and W18 completion, **Then** every control is reachable, labelled and announced, and focus moves predictably.
7. **Given** any changed screen, **When** data is loading, empty, unavailable, stale, partial, or a change is uncertain, **Then** it shows the state from §6.3.
8. **Given** the default workspace's Team graph, **When** shown, **Then** Omnipus's self-row is visible and can be edited or removed.
9. **Given** the connection's Configure side panel on the Connectors page (wireframe R34), **When** routing is saved, **Then** both a workspace and an agent are required.

### US-13 — No default agent: explicit agents or loud failure (P0)

**Why P0**: FA:FQ-17, OQ-2. **Independent test**: an unbound channel receives a message → dropped, status error, log, audit; no agent sees it.

1. **Given** a channel instance not bound to an agent in a workspace (named agent or not), **When** a message arrives, **Then** it is dropped with "bind this channel to an agent in a workspace", shown on the channel status, logged and audited.
2. **Given** a binding whose agent no longer exists, is a worker or is not a member, **When** a message arrives, **Then** the same loud drop happens with that reason.
3. **Given** a chat message starting a new session or a session request with no agent, **When** received, **Then** it is refused visibly.
4. **Given** a task, goal or session whose canonical owner no longer exists, **When** it would run, **Then** it is refused visibly, even if another agent participates.
5. **Given** Settings and the editor, **When** shown, **Then** no default-agent control exists.
6. **Given** no default agent, **When** the gateway boots or reloads, **Then** skill, tool and startup listings and channel recording work.
7. **Given** boot, **When** browsers are prepared, **Then** the default workspace's browser is warmed.
8. **Given** a bound connection, **When** a message from an allowed person matches no person rule and the main agent is unavailable, **Then** it is dropped loudly — never routed elsewhere.

### US-14 — Permission changes ask (P1)

**Why P1**: FA:FQ-3 A. **Independent test**: "give Jim bash" → Go → approval for the permission change.

1. **Given** an existing agent, **When** Omnipus changes its tool permissions or connector assignments, **Then** the permission tool runs and asks.
2. **Given** a new agent, **When** created, **Then** it receives no management tool.

### US-15 — Remove a secret safely (P2)

**Why P2**: depends on #1277. **Independent test**: remove a secret used by an enabled channel → consumers listed; with #1277 landed the gateway restarts and the channel shows degraded.

1. **Given** a secret with consumers, **When** removal is requested, **Then** the consumers are listed before the approval.
2. **Given** #1277 not landed, **When** an in-use secret is to be removed, **Then** it is refused with the consumer list.
3. **Given** removal completes, **When** shown, **Then** durable deletion, live effect and boot validity are reported separately.
4. **Given** an unused older version after a replacement, **When** removed, **Then** it is deleted and the integration is unaffected.

### US-16 — Channel person routing per connection (P1)

The operator decides, per connection (channel instance), who may talk to the workspace's agents and which agent answers each person or group; everyone else allowed reaches the connection's main agent; strangers are told they are not authorised. **Why P1**: founder decision 2026-10-13 (R1–R5, PR-1–PR-4). **Independent test**: connection tg-1 in Sales: allow list [Anna, Ben]; person rule Anna → Jim; main agent Mia; Anna reaches Jim, Ben reaches Mia, Carl gets "not authorised" and appears on the status.

1. **Given** a connection bound to a workspace with a main agent, **When** an allowed person without a rule writes, **Then** the main agent answers.
2. **Given** a person or group rule to an agent in the connection's workspace, **When** that person or group writes, **Then** that agent answers.
3. **Given** a person not on the allow list, **When** they write, **Then** they get the "not authorised" reply and the operator sees it on the connection status and log.
4. **Given** two connections of the same type in one workspace, **When** each has different rules, **Then** each applies only its own.
5. **Given** an agent, **When** its editor opens, **Then** a read-only list shows who can reach it, through which connection, with a link to edit that connection.
6. **Given** a connection's conversation mode is "agent's main chat" (default), **When** people write, **Then** their messages go into the agent's main chat; with "one chat per person", each person gets their own conversation — changed without restart.
7. **Given** Omnipus connects a channel, **When** the plan is confirmed, **Then** the main agent is set and offered person rules are saved with it.
8. **Given** a person rule naming an agent that is not a member of the workspace or no longer exists, **When** the rule is saved, **Then** it is refused; **When** such a rule exists and the person writes, **Then** the message is dropped loudly — never sent to the main agent instead.
9. **Given** per-person conversations exist, **When** the sidebar renders, **Then** they are hidden under the agent by default, can be un-hidden one by one, and are always in the agent's History.

### US-17 — Group chats as rooms: team room, squads, ad-hoc rooms (P1)

The user talks with several agents of one workspace in one room; agents also talk to each other there. Each agent does the work in its own main session and answers back into the room. Every workspace has a standing team room; the user creates squads and ad-hoc rooms. **Why P1**: founder GC-1–GC-9, #1068. **Independent test**: in Sales, open the team room, write "status?" — every member answers; write "@Jim plan it" — only Jim answers; each request and answer also shows, collapsed, in that agent's main chat; Stop cancels pending requests.

1. **Given** a workspace, **When** it exists, **Then** it has one team room whose members are its conversation agents (Omnipus excluded); the user can mute a member there.
2. **Given** the user, **When** they create a squad (named) or an ad-hoc room (two or more chosen agents of the same workspace), **Then** it appears in the workspace's "Group chats" sidebar section; a room with fewer than two agents cannot be created.
3. **Given** a room message without an @-name, **When** the user sends it, **Then** every member agent answers (@all default); with @-names, only the named agents answer. An agent's message without @-names reaches no other agent; an agent's `@all` reaches the others but not itself.
4. **Given** an agent's answer in a room, **When** it @-names other members, **Then** those members receive it and answer (no limit, accepted risk R-A4).
5. **Given** a room, **When** the user types `/pause`, **Then** the room stops sending to agents and not-yet-started requests are cancelled; work already running in an agent's main chat finishes, its answer appears marked "after pause" and its @-mentions are not passed on; `/resume` starts sending again.
6. **Given** a room, **When** its header shows, **Then** it lists the members and the room's running cost.
7. **Given** an agent's room message, **When** it claims an approval or tries to answer someone else's question, **Then** it has no authority; the member's own approvals and questions appear in that agent's main chat.
8. **Given** any room, **When** members change, **Then** the change is audited; **and** no room can include an agent from another workspace.
9. **Given** an agent asks to create a squad, **When** it tries, **Then** there is no tool for it — only the user creates squads (GC-3).
10. **Given** an addressed member is busy, **When** the room message arrives, **Then** it is queued (or held for approval) like any agent request, the room shows that member's state, and the answer arrives later.
11. **Given** an agent's main chat, **When** room traffic reaches it, **Then** it shows as a compact, collapsed entry "From group <name>: …", expandable, and the agent's answer is visible there too.
12. **Given** a room, **When** the user types a command, **Then** only `/pause`, `/resume`, `/mute @Agent`, `/unmute @Agent`, `/members` and `/help` work; anything else is refused with "Rooms don't steer agents — use <Agent>'s main chat" and a link.
13. **Given** a chat header, **When** the user uses "Add participant" (top-left), **Then** only agents can be added: from an agent's main chat it opens a new room with the user and both agents (the main chat stays one-to-one; the room starts empty with a link back); in a room it adds the agent within the membership rules.

### US-18 — Who may resume a stopped channel group conversation (P1)

**Why P1**: #892. **Independent test**: the operator stops a Telegram group conversation; Ben (allowed via the group) writes — it stays stopped; Anna (individually on the allow list) writes — it resumes; a heartbeat never resumes it.

1. **Given** a channel group conversation the operator stopped, **When** a person individually on the connection's allow list (or the operator in the web app) writes, **Then** it resumes.
2. **Given** it is stopped, **When** someone admitted only through the group writes, **Then** it stays stopped, the group gets one notice "The assistant is paused." (once per stop), their message is not processed, and the operator still sees it.
3. **Given** it is stopped, **When** a heartbeat, timer or queued wake fires, **Then** it does not resume.

### US-19 — Steering on channels is per participant and per conversation (P1)

**Why P1**: #955, GC-6, GQ-3. **Independent test**: on Telegram, Anna (steering enabled on her person entry) uses `/model gpt-x` — only that conversation changes; Ben (not enabled) sends `/model gpt-x` — it is not executed and he is told once; `/channels` is refused for everyone.

1. **Given** a participant whose steering flag is on, **When** they use a steering command, **Then** it works as in the web chat and changes only that conversation.
2. **Given** a participant whose steering flag is off (the default), **When** they use a steering command, **Then** it is not executed, not passed to the agent, and they are told once per conversation per 24 hours.
3. **Given** any participant, **When** they use a UI-only command, **Then** it is refused with "available in the web app or CLI", whatever their flag.
4. **Given** the connection's Configure side panel, **When** the operator edits a person entry, **Then** the steering flag can be switched on or off for that person; group entries have no steering flag.

### US-20 — The agent knows its other conversations (P1)

The agent behaves like a person who remembers their other conversations in this workspace. **Why P1**: AW-1, AW-2. **Independent test**: Mia has a per-person Telegram chat with Anna and a squad chat in Sales; in her Sales main chat the user asks "what did Anna want?" — Mia answers from the list, opens Anna's conversation read-only for details, and knows nothing of her Marketing conversations.

1. **Given** Mia's main chat in Sales, **When** a turn starts, **Then** her context lists her other Sales conversations (who, where, last activity, one-line summary — no content).
2. **Given** that list, **When** Mia needs details, **Then** she opens one of those conversations read-only with a tool.
3. **Given** a conversation's idle recap produced items worth remembering, **When** it is written, **Then** they are noted into Mia's memory for Sales automatically.
4. **Given** Mia works in Marketing too, **When** her Sales main chat runs, **Then** no Marketing conversation, summary, note or carry-forward reaches it.
5. **Given** the read-only tool, **When** it is called on a conversation Mia is not part of or in another workspace, **Then** it is refused.

---

## 4. Behavioural contract

**Primary**
- When the user presses Add agent in a workspace, the system opens the Omnipus chat with W1 for that workspace.
- When a plan is complete outside God Mode, the system shows a summary and one Go / Change / Cancel question before any change.
- When the user submits a secret card, the system stores the values durably and gives the agent reference names only.
- When an approved change tool selects a reference whose recipient matches, the system switches the integration to it.
- When an agent messages Omnipus, the system delivers an ordinary message.

**Errors**
- When a channel message is not bound to an agent in a workspace, the system drops it loudly.
- When a reference's recipient differs, or a submit fails a trust check, the system refuses and writes nothing.
- When a shared resource changed since the plan, the system refuses the write and returns the plan to the user.
- When no agent is named for a new chat, session, task, goal or steering item, the system refuses visibly.

**Group chats and awareness**
- When a group message names no agent, every member answers; when it names agents, only they answer.
- When an agent's group message names other members, those members answer, without a limit (R-A4).
- When the user presses Stop in a group, every member's work there stops.
- When a main chat turn starts, the agent sees a short list of its other conversations in that workspace and nothing from other workspaces.
- When a channel command is used, only that conversation changes.

**Boundaries**
- When God Mode is on, the system shows no approval and no Go question, but still asks for missing information and secrets.
- When the user cancels before submitting a card, the system stores nothing; after submitting, the secret stays.
- When a replacement secret is submitted, the system keeps the integration on the old version until the change is approved.

---

## 5. Edge cases

| Case | Expected |
|---|---|
| Two helpers write the same agent | Second gets the stale-revision result (`CONFLICT`; workspaces `REVISION_CONFLICT`); changed plan returns to the user |
| Two helpers in different workspaces change the same provider | Second write refused by the expected-before check; plan returns to the user |
| Submit response and notification lost | "Submission unknown" → state read → true state; values never re-sent automatically |
| Gateway restart with a pending card | Pending request is terminated as "interrupted" (value-free); nothing was stored; the agent may request again |
| Gateway restart after submit | Stored secret intact; request state "submitted" readable |
| Omnipus's self-row removed | Helper launch refused visibly |
| No working model | Agent creation waits; provider and Omnipus's model fixable in Settings |
| `default_safe` on the Go question | Never used (prompt rule; prompt-text test) |
| Name " OMNIPUS " | Refused |
| Mia in Sales and Marketing run commands concurrently | Each run sees only its own workspace's value |
| Old config key `agents.defaults.default_agent_id` | Ignored with a warning on load and reload |
| Existing unbound channel config | Loads; every inbound message drops loudly until bound |

---

## 6. Explicit non-behaviours and safeguards

### 6.1 Qualitative prohibitions

- The system must not route any message to an agent that was not named, because FA:FQ-17.
- The system must not route channel traffic unless the instance is bound to an agent in a workspace, because FA:OQ-2.
- The system must not place a secret value in a transcript, frame, tool argument, log, error text, audit entry or resume record, because F:D10; and must not place an **agent-private** value in the gateway process environment or another agent's child, because FA:OQ-1. (Provider keys and mailbox passwords keep today's boot/reload resolution into the gateway process environment; children never inherit them.)
- The system must not let a reference be selected for a recipient other than the one the card showed, because OSA-7.3.
- The system must not let one workspace's private secret reach another workspace or another agent, because FA:OQ-1.
- The system must not overwrite an in-use secret on submit, because FA:Q-R1-1 A.
- The system must not let the secret card write arbitrary credential keys or delete any credential, because FA:Q-R1-2 B.
- The system must not add a third tool-policy layer or an Omnipus-only resolver bypass, because Hard Constraint #6.
- The system must not persist a per-agent "ask" for Omnipus, because FA:FQ-16 B.
- The system must not show an Omnipus helper's decision only in the helper's chat, because FA:FQ-8.
- The system must not reconfigure a reused agent inside the W18 summary, because FA:FQ-14 A.
- The system must not send a probe email, because OSA-5.
- The system must not create agents through any UI other than Omnipus (REST create stays, FA:FQ-10 A), because F:D18.
- The system must not forward old Agents-screen links, because F:D19.
- The system must not answer a question or approval on the user's behalf, because WF §1.
- The system must not reinvent person routing: it must reuse `Config.Bindings`/`BindingMatch` and the routing ladder at instance scope, because the founder's R4 (2026-10-13).
- The system must not route a person-rule match to an agent outside the connection's workspace, nor fall back silently to the main agent when a person rule's agent is unavailable, because PR-1 and FA:FQ-17.
- The system must not process a stranger's message, because PR-3.
- The system must not let a group chat include agents from more than one workspace, because GC-1 and #1068.
- The system must not treat an agent's group message as authority — it can never approve, answer the user's question, change configuration or run a command for another member, because #1068 §6.
- The system must not let an agent create a squad, because GC-3.
- The system must not let any channel command change agent or global settings, because GC-6 and #955.
- The system must not let a stopped channel group conversation be resumed by an automatic wake or by a person admitted only through the group, because #892.
- The system must not put another workspace's conversation, summary, memory note or carry-forward into an agent's context, because AW-2.
- The system must not activate a replacement secret before its owner or integration selects it, because FA:Q-R1-1 A.

### 6.2 Machine-verifiable constraints

**HTTP / wire**
- Per-agent tool-policy write with `ask` for Omnipus → 422 "Omnipus takes no per-agent Ask; set Ask globally or use Deny".
- Session create without `agent_id` → 400 "an agent must be named"; with `agent_id: omnipus` → 409 "Omnipus has one conversation" with its main session id.
- WS message frame without `session_id` and without `agent_id` → error frame "an agent must be named".
- Secret submit: unauthenticated → 401; request unknown or not accessible to the caller → 404; request not pending → 409 with current state; body > 65,536 bytes → 413; malformed (both values and cancel, missing field, extra field, null, value outside limits, NUL) → 400 with a value-free message.
- Channel routing write without nonblank `workspace_id` and `agent_id` → 400 "bind this channel to an agent in a workspace".
- Adding Omnipus to a non-default workspace → 422; create/rename to "Omnipus" → 409 "the name Omnipus is reserved".

**Data**
- A card holds 1–8 fields; each value 4–16,384 bytes of UTF-8 without NUL.
- Private variable names match `^[A-Z][A-Z0-9_]{0,63}$` and are not protected (D6).
- Pending secret requests: at most 1 per session and 64 install-wide; terminal request states (value-free) kept for the session's lifetime, at most the last 32 per session.
- Omnipus's self-row depth: 1.

**Scope**
- No production Go or TS file contains the agent ids `ava` or `admin` as agent ids after delivery (guard script).
- No non-test Go source contains `GetDefaultAgent`, `SetDefaultAgentOverride` or `DefaultAgentID` (except the ignore-with-warning loader).

**Performance**
- The secret card renders within 1 s of the request frame on a local gateway.
- The provider check finishes or times out within 15 s per provider.
- An unroutable channel message produces its degraded status within 5 s.

### 6.3 Screen and state contract (MAJ-008)

| Surface | Loading | True empty | Unavailable / error | Stale / partial | Mutation uncertain | Success | Disabled actions |
|---|---|---|---|---|---|---|---|
| Omnipus entry + attach | Skeleton row | — | "Omnipus unavailable" + Retry; sending blocked | Cached chat shown read-only with "reconnecting" | — | Chat opens; workspace unchanged | Send while unattached |
| Attention marker | None shown | No marker | "Unknown" marker (not zero) | Last known + stale hint | — | Marker reflects pending cards | — |
| Secret card | "Preparing request" | — | "Request unavailable" (expired/interrupted) | — | "Submission unknown — checking" then server state | "Saved" with reference name | Submit while submitting |
| Team list | Skeleton cards | "No agents yet" + Set up a team + Add agent | Error + Retry (never the empty offer) | Cached list + "may be out of date" | Membership change "not confirmed" + Retry | Toast + refreshed list | Actions during save |
| Team graph | Skeleton | Empty graph text | Error + Retry | Cached + hint | Edge edit "not confirmed" | Updated graph and text list | — |
| Add agent menu / picker | Loading list | "No other agents" | Error + Retry | Cached + hint | Add "not confirmed" + Undo/Retry | "Added · Undo" | Ineligible agents never listed |
| Delete everywhere | Loading workspaces | — | Cannot list impact → delete disabled | — | "Partly deleted" + Retry | Removed | Confirm until impact loaded |
| System agents | Loading | — | Error + Retry | Cached + hint | Save "not confirmed" + Retry | Saved | — |
| Workspace entry | Spinner | Empty state + Set up a team | Retryable error, no fallback | Remembered chat kept pending | — | Chat opens | Send until resolved |
| W18 completion | Progress per part | — | Failed parts listed | Partly done listed | — | Report + "Open chat" | — |
| Group chats sidebar section | Skeleton rows | Team room only | Error + Retry (never "no groups") | Cached list + hint | — | Rows with attention markers | — |
| Room view (header, members, cost, per-member state, @-picker) | Loading members | Team room with no agents: "No agents yet" + Add agent | Error + Retry; send disabled | Cached transcript + "reconnecting" | Message "not confirmed" + Retry | Answers stream per member | Send while unresolved |
| Squad dialog and "Add participant" picker (chat header, top-left) | Loading workspace agents | "No other agents in this workspace" (create disabled below two agents) | Error + Retry | — | Create/add "not confirmed" + Retry | Room opens (from a main chat: new room with a link back) | Create with fewer than two agents; add in the team room (explained) |
| Hidden per-person conversations | — | Control absent | Error + Retry | Cached + hint | Un-hide "not confirmed" | Row shown | — |
| Agent editor "who can reach this agent" | Loading | "Nobody yet" | Error + Retry | Cached + hint | — | List with links | — |
| Connectors page → connection Configure side panel (routing, person table, steering flags, mode, stranger status) | Loading | "Not bound" warning | Error + Retry | — | Save "not confirmed" | Bound pair shown | Save without workspace + agent |

Keyboard and focus (FR-109): every listed control is reachable by Tab/arrow keys and operable by Enter/Space; menus open on focus/Enter, not hover; the secret card takes initial focus on its first field, Enter submits only from the last field, keystrokes never reach the chat composer, errors are associated with fields and announced, terminal states are announced and focus returns to the composer; Go is never pre-selected; attention has a text/shape cue besides colour and respects reduced motion; layouts reflow at narrow widths and work by touch.

### 6.4 No-default-agent: per-caller oracle (MAJ-005)

| Call site | Replacement owner | Visible failure |
|---|---|---|
| `websocket_chat.go::resolveTargetAgent` (+ `firstChatTargetAgentID`) | The frame's `agent_id` for a new session; the session's canonical owner otherwise | Error frame "an agent must be named"; no first-agent fallback |
| `rest_sessions.go::createSessionHTTP` (+ `firstChatTargetAgentID`) | Request `agent_id` (required) | 400; Omnipus → 409 with main id |
| `loop_inbound.go::resolveMessageRoute` | The instance's workspace binding: allow list, then person table, then main agent (FR-142) | Loud drop (FR-121, FR-143); no fallback to any other agent |
| `loop_inbound.go::resolveSystemMessageAgent` | Named async origin (unchanged) | Missing origin → discarded, error log + audit "system message without an addressable agent" |
| `goal_triggers.go::resolveGoalAgent` | Canonical session owner only | Goal status error "owner agent X no longer exists"; never another participant |
| `task_executor.go::processTaskDirect` | Task's agent | Existing truthful pre-dispatch failure; zero runs |
| `steering.go::agentForSession` | Canonical session owner | Item refused with a visible disposition |
| `steering.go::enqueueSteeringItemWithStatus` | Session's canonical owner when `AgentID` empty | Refused disposition; no invented sender |
| `loop_publish.go::publishResponseIfNeeded` | None needed | Publishes (including unroutable errors) without the "already sent" check when no agent; never borrows another agent's state |
| `loop_construct.go::initializeCore` (state manager) | Install-level root `cfg.AgentHomeBasePath()` → `<base>/state/state.json` | Logged error if not writable; channel recording disabled visibly in logs |
| `loop_mcp.go::MCPWorkspacePath` | `cfg.AgentHomeBasePath()` | — |
| `loop.go::extractProvider` | Shared provider from any registered agent or the registry's own | Logged if none |
| `loop_accessors.go::GetStartupInfo` | Iteration over registered agents (existing) | Empty registry → explicit empty info, not failure |
| `loop_accessors.go::ListSkillsDetailed` | Shared skill loader (all installed skills), not an agent's grants | Loader error surfaced |
| `rest_tools.go::HandleTools` | Central builtin registry | 503 "tool registry not ready" before wiring |
| `rest_tool_registry.go::HandleToolsRegistry` fallback | Central builtin registry | Same 503; no agent proxy |
| Also removed | `SetDefaultAgentOverride`, health check, `writeDefaultSingleton`, `default` tool args, delete guard, `Agent.default`, `persistFreshInstallDefaultAgentID`, Mia seed | Old key ignored with warning |
| `gateway_boot_browser.go::pickWarmBrowserManager` | The default workspace's browsing key | No manager → warm nothing, log reason (no lexicographic choice) |

### 6.5 Grant inventory (MAJ-009)

| Tool | Ceiling | Omnipus | Mia, Jim, General Purpose, Planner, Researcher | New custom native | Judge, Plan Supervisor | External-program workers |
|---|---|---|---|---|---|---|
| `request_secret` | allow | allow | allow | allow (constructor override) | deny (fixed) | not available (no native tools) |
| `list_secrets` | allow | allow (all names) | allow (own workspace+agent names) | allow | deny | — |
| `remove_secret` | ask | allow-to-ceiling (asks) | deny | deny | deny | — |
| `select_secret_version` | ask | allow-to-ceiling (asks; own or validated target pair) | allow-to-ceiling (asks; own pair only) | allow-to-ceiling (constructor) | deny | — |
| `connect_channel`, `connect_mailbox`, `set_agent_permissions` | ask | allow-to-ceiling | deny | deny | deny | — |
| `read_conversation` | allow | allow-to-ceiling | allow-to-ceiling (Mia, Jim); deny (General Purpose, Planner, Researcher — workers) | allow-to-ceiling (main type) / deny (worker type) | deny | — |
| Management family (agent/workspace/skill/connector/provider/channel change tools) | per ADR OSA-3 | allow-to-ceiling | deny | deny | deny | — |

"allow-to-ceiling" = no per-agent entry (Omnipus never carries per-agent ask). Omnipus's exact supporting set: `list_agents`, `get_agent`, `get_agent_tools`, `read_agent_metadata`, `list_workspaces`, `get_workspace`, `list_models`, `list_providers`, `test_provider`, `list_mcp_servers`, `list_channels`, `test_channel`, `list_skills`, `find_skills`, `run_doctor`, `get_usage`, `list_secrets`, `AskUserQuestion`, `send_message`, `answer_request`, `message_parent`, `delegate`, `Skill`, `ToolSearch`, `remember`, `recall_memory`, `recall_conversation`, `run_retrospective`, `set_goal`, `goal_claim`, `read_file`, `write_file`, `edit_file`, `append_file`, `list_directory`, `grep`, `list_mounts`, `knowledge_describe`, `knowledge_find`, `knowledge_read`, `knowledge_list`, `search_web`, `fetch_url`, `create_agent`, `update_agent`, `delete_agent`, `update_workspace`, `create_skill`, `edit_skill`, `remove_skill`, `install_skill`, `add_mcp_server`, `remove_mcp_server`, `configure_provider`, `enable_channel`, `disable_channel`, `configure_channel`, `connect_channel`, `connect_mailbox`, `set_agent_permissions`, `request_secret`, `select_secret_version`, `remove_secret`, `environment_setup`, `bash`. Everything else: explicit per-agent deny. Every new tool is registered in the static name list, the shipped ceiling, the role inventories, the custom constructor, the Auto classification table, the central metadata catalogue **and** the executable per-agent registry built at boot and on reload. This is seeded role data in the existing two layers — not a per-agent coverage backfill and not a resolver exception.

### 6.6 Shared mutable setup resources (MAJ-011, R2-MAJ-003)

| Resource | Scope | Existing precondition | Added rule |
|---|---|---|---|
| Agent | Global identity | `revision` (`CONFLICT`) | — |
| Skill (global and project shelf) | Global / per workspace | `revision` (`CONFLICT`) | — |
| Workspace team and graph | Per workspace | `revision` (`REVISION_CONFLICT`) | — |
| Provider entry | Install-wide | None | Revision token + expected-revision check at the shared boundary |
| MCP server entry | Install-wide | Name-collision refusal only | Same |
| Channel instance | Install-wide | None | Same |
| Mailbox (agent, workspace) | Per pair | None | Same |
| Stored secret references | Per reference | Versioned names (FR-072) | Selection compares recipient (FR-064) |

**The one synchronization boundary.** Today Settings writes run under the gateway config write lock (`pkg/gateway/rest_config.go::safeUpdateConfigJSON` → `restAPI.configMu`) and then publish into the agent loop (`refreshConfigAndRewireServices` → `AgentLoop.SwapConfig`, agent-loop lock) — order *config lock → agent-loop lock*. System tools write through `Deps.WithConfig` → `AgentLoop.MutateConfig` (agent-loop lock only) and persist with `SaveConfigLocked`, which deliberately never takes the config lock (the documented AB-BA deadlock). Decision: for writes to the four shared resources above, the tool path **acquires the gateway config lock before entering `MutateConfig`** (order config lock → agent-loop lock, the same as Settings; never the reverse), checks the expected revision against the live configuration, writes, persists and releases. Settings writes to these resources are unchanged in locking and therefore serialize with tool writes; a Settings change bumps the revision, so a later tool write with the old token is refused.

**Before-state projection.** Read tools return a secret-free revision token per shared resource: a stable hash over its non-secret fields (provider: id, effective endpoint, protocol, reference name; MCP: name, transport, command + arguments or URL, enabled, reference names; channel: instance, platform, binding pair, enabled, reference names; mailbox: hosts, username, reference name). A missing entry has the token `absent`. Resolved secret values never enter a token, summary or precondition. `list_providers` gains endpoint, protocol, reference name and token (today it returns only name and credential presence); the MCP, channel and mailbox read paths gain the token.

On refusal the helper relays a revised summary to the main for a new Go. No workflow transaction engine, global lock framework or cross-resource atomicity is introduced.

### 6.7 Security and user promises for the secret card (MAJ-013, FA:Q-R1-2 B)

| Promise / control | Rule |
|---|---|
| Entry | Authenticated web session only (existing auth middleware); channel sessions refused |
| Ownership | Server derives session, acting agent, owning workspace + agent (private kind) and recipient from the request record; the request id is never authority by itself |
| Submit checks (all before any write) | Caller authenticated; request exists and is accessible to the caller's account; request pending; its session exists, is not stopped or deleted; acting agent still exists; recipient descriptor unchanged since the request; payload valid. Any failure → zero store writes, zero resume |
| Consent | Authenticated submit of the exact destination-bound card is consent for that scoped write only (**explicit exception to ADR-022's password re-confirmation, FA:Q-R1-2 B, 2026-10-12**). The card cannot write an arbitrary key and cannot delete. Raw Settings credential routes (`/api/v1/credentials`) keep `requireReAuth` |
| Verification gate | security-lead verifies the request/session/recipient binding and the exception before WU-4 lands |
| Selection | Integration kinds: only an approved change tool writing the recorded recipient selects a reference. Private kind: the first version is selected on submit; a replacement is selected only by `select_secret_version` (Ask) for the owner pair. Live policy applies |
| Stop / deletion | Stop or session deletion terminates pending requests (no value existed) |
| Bounds | 1 pending per session, 64 install-wide; terminal metadata as in §6.2 |
| Audit | Value-free events: requested, submitted, cancelled, stopped, interrupted, refused (with reason code), selected, removed |
| Errors | Store or validation errors never include a value; payload not logged |
| Masking | The complete sensitive set always includes every stored card value (all kinds, versions, states); every full-set rebuild uses the one complete computation; registrations are serialized (FR-069, FR-089) |
| Commit | The submit receipt is written in the same atomic store publication as the values; request state after restart derives from it (FR-087) |
| Gateway environment | Private values never enter it. Provider/mailbox values keep the existing boot/reload resolution into it; children never inherit them |
| Private kind | Promise wording A (FA:OQ-1): "never typed into the chat, never in tool arguments, hidden in output where possible; a command you let the agent run can still reveal it" — on the card and in `docs/security.md` |

### 6.8 Component composition (MIN-002)

| New or changed UI | Catalogued components |
|---|---|
| Secret card | Field, Label, Input (password mode), FormError, Button; card shell per chat message styling |
| Secret card states | EmptyState / QueryErrorState for unavailable; Skeleton for loading |
| Team list cards | AgentIcon, Button, Popover menu (actions), Tabs or ViewSwitch (list/graph) |
| Picker | Popover/Sheet, Input (search), AgentIcon |
| Delete everywhere | ConfirmDialog |
| System agents section | Existing Settings layout + shared editor Sheet |
| Omnipus entry | Existing sidebar row pattern + AgentIcon |

A genuinely new reusable composite follows the four-part publication contract (catalogue, export, style registration, manifest). Gates: the design-system CI gate (load `omnipus-design-system`; scripts under `scripts/design-system/` and `scripts/design-system-locks/`), `npm run typecheck`, and Storybook static-build evidence for any published component. Draft HTML colours/layout are not copied.

---

## 7. Contract-first wire definitions (Hard Constraint #8)

### 7.1 Schemas

Order per change: schema in `contracts/components/schemas/` → reference from `openapi.yaml`/`asyncapi.yaml` → `scripts/gen-contracts.sh` → one atomic commit with generated output → consumers. Generated artifacts that change: `pkg/api/generated/openapi_types.gen.go`, `asyncapi_types.gen.go`, `fixtures.go`; `src/lib/api/generated/openapi-types.ts`, `asyncapi-types.ts`, `_asyncapi-zod-schemas.generated.ts`, `schemas.ts`, `ws-schemas.ts`; and the inbound mirror `pkg/gateway/inboundschemas/*.yaml` (gen step 5).

| # | Schema | Change | FR |
|---|---|---|---|
| K1 | **new** `SecretRequestFrame` (WS) | request id, session id, acting agent id + name, root session id (server-derived, optional), workspace id (optional), owner pair (private kind), purpose (≤ 500 chars), recipient display, fields[1..8] (key `^[a-z][a-z0-9_]{0,31}$`, label ≤ 80 chars, required) | FR-061 |
| K2 | **new** `SecretRecipientDisplay` | kind enum `provider_key` / `mcp_env` / `channel_credential` / `mailbox_password` / `agent_private`; display lines (endpoint, transport + command/URL, instance + field, hosts + username, workspace + agent + variable) | FR-065 |
| K3 | **new** `SecretRequestResolvedFrame` (WS) | request id, state enum `submitted` / `cancelled` / `stopped` / `interrupted` / `refused` | FR-074 |
| K4 | **new** `SessionStateSecretRequest`; **changed** `SessionStateFrame`, `SessionSnapshotFrame` | `secret_requests` array (never null): pending plus terminal (bounded) entries with state | FR-079 |
| K5 | **new** `SecretSubmitRequest`, `SecretRequestState` (REST) | `POST /api/v1/secret-requests/{id}/submit` body: exactly one of `values` (map field key → string) or `cancel: true`; response `SecretRequestState`. `GET /api/v1/secret-requests/{id}` → `SecretRequestState` (id, state, reference names when submitted, updated_at) | FR-062, FR-079 |
| K6 | **new** `SecretNameEntry`; `GET /api/v1/secrets` | reference name, kind, version, recipient display, owner pair (private), consumers[], `state` enum `stored` / `selected` / `superseded` | FR-073, FR-086 |
| K7 | **changed** `ToolApprovalRequiredFrame`, `SessionStatePendingApproval` | optional `root_session_id` (server-derived), `acting_agent_name`; existing `workspace_id`, `session_id` kept; reconnect Deny-only stub semantics unchanged | FR-092 |
| K8 | **changed** `MessageFrame` | optional `workflow_start` {`workflow` enum `W1`/`W18`, `target_workspace_id`} described as target data; rule "a frame without `session_id` must carry a nonblank `agent_id`" documented and enforced semantically | FR-012, FR-122 |
| K9 | **changed** kickoff description | server-initiated into Omnipus's existing main; client kickoff with a session id stays refused | FR-047 |
| K10 | **changed** `WhatsAppPairingFrame`, `WhatsAppPairingSubscribeFrame` | optional `request_id`, `session_id` | FR-081 |
| K11 | **changed** `Agent` | remove `default` | FR-120 |
| K12 | **changed** `AgentUpdateRequest` | remove `default`; document 422 for Omnipus per-agent `ask` | FR-120, FR-022 |
| K13 | **changed** `AgentToolsUpdateRequest` | document 422 for Omnipus per-agent `ask` | FR-022 |
| K14 | **changed** `ChannelRouting` + `setChannelRouting`/`getChannelRouting` | write: `workspace_id` and agent required, nonblank; read: an existing unbound config is reported truthfully (fields empty, `bound: false`) — never an invented destination | FR-127–FR-129 |
| K15 | **changed** `ChannelEntry` | `degraded` / `degraded_reason` cover routing failures; optional `last_dropped_at`; cleared when a valid binding is saved | FR-121 |
| K16 | **changed** `SessionCreateRequest` | `agent_id` required | FR-122 |
| K18 | **new** `ChannelPersonRule` | match kind enum `person` / `group` / `guild` / `team` / `account`, match id, display label (≤ 80), agent id | FR-141 |
| K19 | **changed** `ChannelRouting` (read and write) | `agent_id` = main agent (required), `person_rules[]` (never null, ≤ 500), `conversation_mode` enum `main` / `per_person` (default `main`) | FR-140, FR-147 |
| K20 | **changed** `ChannelEntry` status | `strangers_count`, `last_stranger_label` (display label or redacted id), `last_stranger_at` | FR-145 |
| K21 | **new** `AgentReachabilityEntry`; `GET /api/v1/agents/{id}/reachability` | connection id, workspace id, via enum `main` / `person_rule`, match kind/label | FR-146 |
| K22 | **changed** `BindingMatch` config shape (not wire) and session-scope route | `instance_id`; `/api/v1/security/session-scope` per RQ-4 | FR-141, FR-148 |
| K23 | **changed** `Session` | optional `kind` enum `chat` / `room_team` / `room_squad` / `room_adhoc`; for rooms `agent_id` omitted, `agent_ids` = members, optional `room_name`, `muted_agent_ids` (team room), `room_cost` read-only | FR-199, FR-171, FR-158 |
| K24 | **new** `RoomCreateRequest`, `RoomMembersUpdateRequest`; `POST /api/v1/workspaces/{id}/rooms`, `PATCH /api/v1/rooms/{id}/members` (add, remove, mute, unmute), `DELETE /api/v1/rooms/{id}` | kind (`squad` / `adhoc`), name (squad, ≤ 80), agent ids (**≥ 2**, same workspace), optional `origin_session_id` (main chat it was started from); team room not creatable, deletable or editable through these | FR-161, FR-172, FR-204, FR-207 |
| K25 | **changed** `MessageFrame` | optional `mentions[]` (agent ids or `all`) resolved by the SPA picker; server re-resolves from text and membership | FR-164 |
| K26 | **changed** `Message` / transcript entry | group entries carry the authoring agent (existing guest author) and the resolved addressees | FR-166 |
| K27 | **new** `RoomMemberStateFrame` (WS) + snapshot list | room id, member agent id, state enum `queued` / `held` / `working` / `waiting_on_you` / `answered` / `cancelled`, request id | FR-201 |
| K28 | **changed** `ChannelEntry` / channel conversation status | `stopped_by_operator` and `received_while_stopped` count on a channel group conversation | FR-182 |
| K29 | **changed** `Session` | optional `sidebar_visible` (per-person channel conversations default `false`); `PATCH` on the existing session update route | FR-152 |
| K30 | **new** `AwarenessEntry` (read-only, for the web inspector of what the agent sees) and audit event shapes for group membership | session id, kind, who, last activity, summary | FR-190, FR-172 |
| K32 | **changed** `ChannelPersonRule` and allow-list entry shape | `steering` boolean (default false) on individual person entries only | FR-185 |
| K33 | **changed** transcript entry / `Message` in a main session | optional `room_ref` {room id, room name, request id, role `request` / `answer`} so the SPA renders the collapsed "From group <name>" entry | FR-202 |
| K34 | **changed** request ledger entry (`addressing`) — not wire | `origin: room` + room id, so answers route to the room and cancelled requests are dropped | FR-203 |
| K35 | **changed** `Session` (room) | `room_paused` boolean; `room_origin_session_id` (link back, GC-10); `room_read_only_reason` when below two agents; room transcript entries may carry `after_pause: true` | FR-170, FR-204, FR-207 |
| K17 | config | `agents.defaults.default_agent_id` ignored with warning (settings sanitiser and loader) | FR-120 |

### 7.2 Runtime validation (MAJ-004)

Generated types are not enforcement. Semantic rules (agent required for new sessions, submit exclusivity, field-key match, UTF-8 byte limits, NUL rejection, required routing pair, Omnipus per-agent Ask refusal, nil-safe arrays) are checked in the handlers and **hold with inbound schema validation on and off** (`pkg/gateway/rest_inbound_validate.go::decodeAndValidate`, `ValidateInboundFrameJSON`; WS read loop). The SPA consumes new frames through `src/lib/ws.ts::parseFrameSafe`. Tests 62–64 send malformed, missing, extra, null and oversize payloads through the real REST and WS handlers in both modes, each with a passing control. Discriminated unions, if any, are inline `oneOf` (ADR-034).

---

## 8. BDD scenarios

### Feature: Omnipus identity (US-1)

**BDD-1.1 Fresh install seeds Omnipus only** — Traces to US-1 AS-1 — Happy Path
- Given an empty data directory
- When the gateway starts
- Then the agents are listed with `omnipus` (core, locked identity) and without `ava` or `admin`

**BDD-1.2 Reserved name** — US-1 AS-2 — Error Path (Outline)
- Given an agent "Researcher"
- When it is created or renamed to `<name>` via `<path>`
- Then `<result>`

| name | path | result |
|---|---|---|
| Omnipus | REST create | 409 reserved |
| omnipus | `update_agent` | refused |
| " OMNIPUS " | REST update | 409 reserved |
| Omnipus Helper | REST create | accepted |

**BDD-1.3 Omnipus cannot join another workspace** — US-1 AS-3 — Error Path
- Given workspace Sales
- When `update_workspace` adds `omnipus`
- Then refused; team unchanged

**BDD-1.4 Omnipus cannot leave the default workspace** — US-1 AS-3 — Error Path
- Given the default workspace
- When a REST update removes `omnipus`
- Then 422; membership unchanged

**BDD-1.5 Omnipus hidden from lists, graph keeps self-row** — US-1 AS-4 — Happy Path
- Given the default workspace is active
- When the Team list, sidebar rows and picker render
- Then none shows Omnipus
- And the Team graph shows Omnipus's self-row

**BDD-1.6 Role tools** — US-1 AS-5 — Happy Path (Outline)
- Given a fresh install
- When Omnipus's effective policy for `<tool>` resolves
- Then it is `<policy>`

| tool | policy |
|---|---|
| create_agent | allow |
| delete_agent | ask |
| request_secret | allow |
| set_config | deny |
| browser_navigate | deny |
| send_email | deny |

**BDD-1.7 One Omnipus conversation** — US-1 AS-6 — Error Path (Outline)
- Given Omnipus's main exists
- When `<path>` tries to start a new Omnipus chat
- Then `<result>`

| path | result |
|---|---|
| REST session create `agent_id: omnipus` | 409 with main session id |
| WS frame without session id, `agent_id: omnipus` | error frame naming the main |
| REST session create `agent_id: mia` (control) | 201 new extra chat |

**BDD-1.8 Exclusive cross-workspace setup authority** — US-1 AS-7 — Happy Path
- Given workspace Research where Omnipus and Mia are not members
- When Omnipus writes a setup file there, and Mia tries the same from Sales
- Then Omnipus's write succeeds and Mia's is refused by the file boundary

### Feature: Workflows and routing (US-2)

**BDD-2.1 Button-named workflow** — US-2 AS-1 — Happy Path
- Given workspace Sales
- When the SPA sends workflow-start W1 targeting Sales
- Then the turn starts with "Start workflow W1 for workspace Sales" and loads W1

**BDD-2.2 Genuine ambiguity asks** — US-2 AS-2 — Alternate Path
- Given the request "set up Slack for the team"
- When Omnipus routes it (fits connect-channel and add-connector)
- Then it asks with a question card before loading either and calls no change tool

**BDD-2.3 No workflow fits** — US-2 AS-3 — Error Path
- Given "what is the capital of France"
- When Omnipus answers
- Then no change tool is called

**BDD-2.4 Reload after trim** — US-2 AS-4 — Edge Case
- Given a W18 run whose skill was trimmed
- When the trim notice appears
- Then `Skill` W18 is called again before the next change

**BDD-2.5 Nested workflow, one summary** — US-2 AS-5 — Happy Path
- Given "Mia needs to read our Google Drive"
- When Omnipus routes it
- Then it loads change-agent, runs add-connector as a sub-part, shows one summary and asks no routing question

### Feature: Approval (US-3)

**BDD-3.1 Summary question first** — US-3 AS-1 — Happy Path
- Given W1 without connectors outside God Mode
- When planning completes
- Then one Go/Change/Cancel card without `default_safe` precedes `create_agent`

**BDD-3.2 Cancel after a submitted secret** — US-3 AS-2 — Alternate Path
- Given the user submitted a provider key on a card and then sees the summary
- When the user chooses Cancel
- Then no change tool runs
- And the key is listed by `list_secrets` as unused and is removable

**BDD-3.3 Ask list prompts under Auto** — US-3 AS-3 — Happy Path (Outline)
- Given Auto-approve on, God Mode off
- When Omnipus calls `<tool>`
- Then an approval card appears

| tool |
|---|
| delete_agent |
| remove_skill |
| remove_mcp_server |
| add_mcp_server |
| configure_provider |
| enable_channel |
| configure_channel |
| connect_channel |
| connect_mailbox |
| set_agent_permissions |
| remove_secret |
| select_secret_version |
| environment_setup |

**BDD-3.4 God Mode, fully specified** — US-3 AS-4 — Alternate Path
- Given God Mode on and "add the Notion connector from npx notion-mcp with token on the card" for Mia
- When Omnipus runs W10
- Then no approval card and no Go question appear, the summary is posted as a message, and the secret card appears once

**BDD-3.5 Per-agent Ask refused for Omnipus** — US-3 AS-5 — Error Path (Outline)
- Given Omnipus
- When `<path>` sets `<tool>` to `<value>`
- Then `<result>`

| path | tool | value | result |
|---|---|---|---|
| REST tools write | bash | ask | 422 |
| REST tools write | bash | deny | accepted |
| `set_agent_permissions` | create_agent | ask | refused |
| editor | any | — | Ask not offered |

**BDD-3.6 Both confirmations kept** — US-3 AS-3 — Happy Path
- Given W4 outside God Mode
- When Go is chosen
- Then a `delete_agent` approval card follows

**BDD-3.7 God Mode still clarifies** — US-3 AS-6 — Alternate Path
- Given God Mode and "add a connector" with no server named
- When Omnipus plans
- Then it asks which server with a question card and calls no change tool before the answer

**BDD-3.8 God Mode still shows the secret card** — US-3 AS-7 — Alternate Path
- Given God Mode and a provider key is needed
- When Omnipus reaches the secrets step
- Then the secret card appears

### Feature: Create an agent (US-4)

**BDD-4.1 Add agent opens Omnipus** — US-4 AS-1 — Happy Path
- Given Sales active
- When Add agent is pressed in the sidebar
- Then the Omnipus chat is foreground with W1 for Sales, and Sales stays active

**BDD-4.2 Existing agent fits** — US-4 AS-2 — Alternate Path
- Given Jim fits
- When the summary appears
- Then it proposes only "Add Jim to Sales"

**BDD-4.3 Gaps listed** — US-4 AS-3 — Edge Case
- Given an uncovered ability
- When the summary appears
- Then it is listed under "not covered"

**BDD-4.4 Agent lands in target workspace** — US-4 AS-4 — Happy Path
- Given Go on W1 for Sales from Omnipus's main (default workspace)
- When `create_agent` runs with target Sales
- Then the agent is a member of Sales only and has a main chat in Sales

**BDD-4.5 Project skill in target shelf** — US-4 AS-5 — Happy Path
- Given Sales and Default both have project skill `crm`
- When Omnipus edits `crm` with target Sales
- Then only Sales's copy changes

**BDD-4.6 Unknown target refused** — US-4 AS-4 — Error Path
- Given an unknown target workspace
- When `create_agent` validates
- Then failed: nothing saved

**BDD-4.7 Dependencies up front, readiness honest** — US-4 AS-6 — Happy Path
- Given a document agent whose skill needs a converter
- When the summary appears and Go is given
- Then the install is listed in the summary, `environment_setup` asks, and the report says "installed; readiness confirmed at the agent's first use" unless readiness was proven in that agent's sandbox

**BDD-4.8 Other target-scoped verbs** — US-4 AS-5 — Happy Path (Outline)
- Given Omnipus's main in the default workspace and target `<target>`
- When Omnipus calls `<verb>`
- Then the effect lands in `<target>` only

| verb | target |
|---|---|
| `create_skill` (project) | Sales |
| `remove_skill` (project slug also in Default) | Sales |
| skill discovery for the plan | Sales |
| `environment_setup` | Sales |

### Feature: Team setup and welcome (US-5)

**BDD-5.1 New workspace empty + offer** — US-5 AS-1 — Happy Path
- Given the user creates Research
- When creation completes
- Then Research's team is empty and the offer appears in Omnipus's main naming Research

**BDD-5.2 Empty Team tab button** — US-5 AS-2 — Happy Path
- Given an empty Team tab
- When rendered
- Then "Set up a team" opens the Omnipus chat with W18 for that workspace

**BDD-5.3 Reuse unchanged** — US-5 AS-3 — Happy Path
- Given Mia in Sales fits a Research role
- When the W18 summary appears
- Then Mia is "reused, also works in Sales" with no change to Mia

**BDD-5.4 Shared change is separate** — US-5 AS-4 — Alternate Path
- Given Mia would need an extra skill
- When proposed
- Then a separate proposal shows before/after and lists Sales and Research

**BDD-5.5 Text equivalent of graph** — US-5 AS-5 — Happy Path
- Given a W18 summary
- When rendered
- Then a list of agents and edges accompanies the graph

**BDD-5.6 Land in lead's chat** — US-5 AS-6 — Happy Path
- Given W18 applied with lead Atlas
- When Research is opened with nothing remembered
- Then Atlas's main opens

**BDD-5.7 Skipped offer** — US-5 AS-1 — Alternate Path
- Given the offer
- When declined
- Then nothing is created; Team still offers setup

**BDD-5.8 Client kickoff into existing session refused** — US-5 AS-1 — Error Path
- Given a client frame with the kickoff marker and an existing session id
- When received
- Then refused

**BDD-5.9 Failed team load is not empty** — US-5 AS-2 — Error Path
- Given the team request fails
- When the Team tab renders
- Then an error with Retry shows and no setup offer appears

### Feature: Secret card (US-6)

**BDD-6.1 Card content** — US-6 AS-1 — Happy Path
- Given Omnipus requests an OpenAI key for `https://api.openai.com/v1`
- When the card renders
- Then it shows "Omnipus", the purpose, the endpoint and one labelled field

**BDD-6.2 Durable store, references only, no leak** — US-6 AS-2 — Happy Path
- Given a sentinel value submitted on a card
- When the turn resumes
- Then the resumed message lists the reference name only
- And captured model requests (decoded), transcript, frames, logs, audit, tool arguments, error texts and resume records — each non-empty — contain no sentinel
- And a control run that deliberately logs the sentinel is detected by the same search

**BDD-6.3 Recipient mismatch refused** — US-6 AS-3 — Error Path (Outline; dataset D1)
- Given a reference stored for `<recorded>`
- When a tool selects it for `<used>`
- Then refused

| recorded | used |
|---|---|
| openai @ api.openai.com | openai @ evil.example |
| MCP notion `npx notion-mcp` env TOKEN | MCP notion `sh -c curl…` env TOKEN |
| mailbox mia/Sales imap.a.com user x | mailbox mia/Sales imap.b.com user x |
| channel tg-1 bot_token | channel tg-2 bot_token |
| private Sales/mia WEATHER_TOKEN | private Marketing/mia WEATHER_TOKEN |
| private Sales/mia WEATHER_TOKEN | private Sales/jim WEATHER_TOKEN |

**BDD-6.4 One submit, one write** — US-6 AS-2 — Edge Case
- Given one pending request
- When two submits for it arrive concurrently
- Then exactly one store write happens and the other gets 409 with state `submitted`

**BDD-6.5 Ending before or after submit** — US-6 AS-4 — Error Path (Outline)
- Given a request
- When `<event>`
- Then `<stored>` and the state is `<state>`

| event | stored | state |
|---|---|---|
| user cancels the card | nothing | cancelled |
| Stop before submit | nothing | stopped |
| gateway restart before submit | nothing | interrupted |
| workflow Cancel after submit, before selection | secret stays, state stored (unused) | submitted |
| Stop after submit, before selection | secret stays, state stored (unused) | submitted |
| Stop after an integration selected it | secret stays, state selected; report lists the completed change | submitted |

**BDD-6.6 Channel session refused** — US-6 AS-5 — Error Path
- Given a Telegram-origin session
- When an agent requests a secret
- Then "open the web app"; no card

**BDD-6.7 Submission unknown recovered** — US-6 AS-6 — Edge Case
- Given the submit response and the resolved frame are both lost
- When a fresh tab loads the session
- Then the card reads the request state and shows "Saved"; no values requested again

**BDD-6.8 Plaintext refused by config tools** — US-6 AS-2 — Error Path
- Given `configure_provider` with a plaintext `api_key`
- When validated
- Then refused with "request a secret"

**BDD-6.9 Multi-field all-or-none** — US-6 AS-2 — Edge Case
- Given a Slack card with bot and app tokens
- When only one is submitted
- Then 400; nothing stored

**BDD-6.10 Duplicate submit** — US-6 AS-2 — Edge Case
- Given a submitted request
- When the same submit is replayed
- Then 409 with `submitted`; stored value unchanged

**BDD-6.11 Omnipus helper card in main** — US-6 AS-1 — Alternate Path
- Given an Omnipus helper requests a secret
- When the card renders
- Then it is in Omnipus's main chat naming the helper

**BDD-6.12 Ordinary helper refused** — US-6 AS-1 — Error Path
- Given a Jim helper requests a secret
- Then refused ("ask your parent")

**BDD-6.13 Masking registered before use** — US-6 AS-2 — Edge Case
- Given a stored value echoed by a connector's error
- When the result returns
- Then the value is masked

**BDD-6.14 Replacing an in-use secret** — US-6 AS-7 — Happy Path
- Given channel tg-1 uses `telegram_tg-1_bot_token.v1`
- When the user submits a replacement and then cancels the plan
- Then `.v2` is stored and listed unused, and tg-1 still uses `.v1`
- And when a later approved `connect_channel` selects `.v2`, tg-1 switches and `.v1` is listed unused

**BDD-6.15 No password step; raw route keeps it** — US-6 AS-8 — Happy Path
- Given a pending card
- When the user submits
- Then no password prompt appears
- And a raw `POST /api/v1/credentials` without re-authentication is still refused

**BDD-6.16 Untrusted submits write nothing** — US-6 AS-9 — Error Path (Outline)
- Given `<condition>`
- When a submit arrives
- Then `<status>`, zero store writes and no resume

| condition | status |
|---|---|
| no authentication | 401 |
| unknown request id | 404 |
| request already cancelled | 409 |
| session stopped | 409 |
| session deleted | 404 |
| acting agent deleted | 409 |
| recipient changed since request | 409 |
| store write fails | 500 value-free; request stays pending |

**BDD-6.17 Bounds** — US-6 AS-9 — Edge Case
- Given 64 pending requests install-wide
- When another agent requests a secret
- Then the tool returns "too many pending secret requests" and no card is created

**BDD-6.18 Ordinary and custom agents can use the card** — US-6 AS-1 — Happy Path (Outline)
- Given a fresh `<agent>` in Sales
- When it calls `request_secret` (private kind) and the user submits
- Then `<result>`

| agent | result |
|---|---|
| Mia (built-in) | stored; `list_secrets` shows it |
| a new custom native agent | stored; `list_secrets` shows it |
| Judge | tool unavailable |

**BDD-6.19 First private version is selected** — US-6 AS-10 — Happy Path
- Given Mia in Sales has no `API_KEY`
- When the user submits it on Mia's card
- Then `.v1` is in state selected and Mia's next Sales command sees it

**BDD-6.20 Private replacement waits for selection** — US-6 AS-10 — Happy Path
- Given Mia's Sales `API_KEY.v1` is selected
- When the user submits a replacement
- Then `.v2` is stored, `.v1` stays selected, and Mia's commands still see `.v1`
- And after Mia calls `select_secret_version` for `.v2` and the user approves, her next command sees `.v2` and `.v1` is superseded

**BDD-6.21 Cancelled private replacement** — US-6 AS-10 — Alternate Path
- Given `.v2` was stored for Mia's Sales `API_KEY`
- When the plan is cancelled
- Then `.v2` is listed as stored (unused) and removable; `.v1` stays selected

**BDD-6.22 Concurrent selections** — US-6 AS-10 — Edge Case
- Given `.v2` and `.v3` stored for the same private variable
- When two approved selections arrive at the same time
- Then exactly one version ends selected and the other selection is refused as stale

**BDD-6.23 Selection outside the owner pair refused** — US-6 AS-10 — Error Path (Outline)
- Given Mia's Sales `API_KEY.v2`
- When `<caller>` selects it
- Then `<result>`

| caller | result |
|---|---|
| Mia in Sales | approval card, then selected |
| Jim in Sales | refused |
| Mia in Marketing | refused |
| Omnipus with target Sales/mia | approval card, then selected |

**BDD-6.24 Submit commit across crashes** — US-6 AS-11 — Edge Case (Outline)
- Given a submit and a crash at `<cut>`
- When the gateway restarts and the card reads the request state
- Then state is `<state>`, stored references are `<stored>`, and the agent resumes `<resume>`

| cut | state | stored | resume |
|---|---|---|---|
| before the store publication | interrupted | none | not at all |
| after publication, before request metadata | submitted | the receipt's references | once, references only |
| terminal metadata write fails | submitted | the receipt's references | once, references only |
| resume dispatch fails | submitted | the receipt's references | once on next boot or attach |
| response and frame lost, no crash | submitted | the receipt's references | already resumed; not again |

**BDD-6.25 Masking survives every rebuild** — US-6 AS-12 — Edge Case (Outline)
- Given filtering on and a stored card secret (selected or not) of at least 4 bytes
- When `<transition>` happens and a command prints the exact value
- Then the value is masked

| transition |
|---|
| Settings save |
| configuration reload |
| provider sign-in / token refresh |
| gateway restart |
| two registrations at the same time |

### Feature: Agent-private secrets (US-7)

**BDD-7.1 Owner pair sees variable** — US-7 AS-1 — Happy Path
- Given Mia in Sales stored WEATHER_TOKEN
- When Mia runs `bash` in Sales printing the variable's length
- Then the length matches

**BDD-7.2 Other agent, same workspace** — US-7 AS-2 — Error Path
- Given that secret
- When Jim runs the command in Sales
- Then unset

**BDD-7.3 Masking limits** — US-7 AS-3 — Edge Case
- Given Mia's command prints the exact value and then its base64
- When output returns
- Then the exact value is masked; the base64 is not guaranteed (documented)

**BDD-7.4 External-program worker excluded** — US-7 AS-2 — Error Path
- Given an external-program worker
- When it is delegated work
- Then no private secret is injected and it has no `request_secret`

**BDD-7.5 Same agent, other workspace** — US-7 AS-4 — Error Path
- Given Mia@Sales WEATHER_TOKEN=A and Mia@Marketing WEATHER_TOKEN=B
- When Mia runs in Marketing
- Then the value is B; Sales's A never appears

**BDD-7.6 Concurrent turns** — US-7 AS-4 — Edge Case
- Given Mia runs commands in Sales and Marketing at the same time
- When both print the variable's hash
- Then each matches its own workspace's value

**BDD-7.7 Variable grammar** — US-7 AS-5 — Error Path (Outline; dataset D6)
- Given the name `<name>`
- When stored
- Then `<result>`

| name | result |
|---|---|
| WEATHER_TOKEN | accepted |
| weather_token | refused (uppercase only) |
| LC_ALL | refused (protected) |
| XDG_CONFIG_HOME | refused (protected prefix) |
| PYTHONPATH | refused (document runtime) |
| NODE_PATH | refused (document runtime) |
| OSL_SOCKET_PATH | refused (document runtime) |
| PATH | refused (protected) |
| HTTP_PROXY | refused (protected) |
| OMNIPUS_X | refused (protected prefix) |
| LD_PRELOAD | refused (protected prefix) |
| 1TOKEN | refused (grammar) |

**BDD-7.8 Omnipus sets a private secret for a target** — US-7 AS-1 — Alternate Path
- Given W1 for Sales needs Mia's token
- When Omnipus requests a private secret with target Sales/mia
- Then the card shows "Mia in Sales"; as the first version it is selected, and Mia's next Sales command sees it
- And a target where Mia is not a member is refused

**BDD-7.9 All child paths, never the gateway** — US-7 AS-1 — Edge Case
- Given Mia's Sales secret
- When Mia runs a foreground `bash`, a background `bash` and `environment_setup`
- Then each child sees it
- And the gateway process environment does not contain it

**BDD-7.10 Private names survive the document layer** — US-7 AS-1 — Edge Case
- Given Mia's Sales `WEATHER_TOKEN` is selected and the document runtime is enabled
- When Mia runs a document command
- Then `WEATHER_TOKEN` is present, and `PYTHONPATH`, `NODE_PATH` and `OSL_SOCKET_PATH` keep the runtime's values

**BDD-7.11 Replacement selection for commands** — US-7 AS-6 — Happy Path
- Given Mia's Sales `WEATHER_TOKEN.v2` stored but not selected
- When Mia runs a command, then selects `.v2` with approval, then runs again
- Then the first run sees `.v1` and the second sees `.v2`

### Feature: Connect channel and mailbox (US-8)

**BDD-8.1 One-call connect** — US-8 AS-1 — Happy Path
- Given a confirmed plan to connect Telegram to Mia in Sales
- When `connect_channel` runs after approval
- Then configured, bound to Mia in Sales, enabled; result lists proven facts

**BDD-8.2 Not-tested facts** — US-8 AS-2 — Edge Case
- Given webhook delivery cannot be proven
- Then "inbound delivery: not tested"; routing not "working"

**BDD-8.3 Bad token** — US-8 AS-1 — Error Path
- Given a revoked token
- When `connect_channel` runs
- Then "saved but not working: token rejected" or "failed: nothing saved"; never "working"

**BDD-8.4 QR in requesting chat** — US-8 AS-3 — Happy Path
- Given WhatsApp connect from Omnipus's main
- When pairing starts
- Then the code and progress render in Omnipus's chat, correlated by request id

**BDD-8.5 QR timeout** — US-8 AS-3 — Error Path
- Given pairing started
- When the timeout passes
- Then "saved but not working: pairing timed out"

**BDD-8.6 Mailbox readiness split** — US-8 AS-4 — Happy Path
- Given valid incoming, invalid outgoing credentials
- When `connect_mailbox` runs
- Then read working, send not; no email sent (SMTP capture empty)

**BDD-8.7 Valid token, wrong binding** — US-8 AS-2 — Error Path
- Given a valid token but a target agent that is not a member of the target workspace
- When `connect_channel` validates
- Then failed: nothing saved

**BDD-8.8 Pairing subscription teardown** — US-8 AS-3 — Edge Case
- Given pairing in progress
- When the user presses Stop or the chat disconnects and reconnects
- Then Stop ends pairing with "stopped"; a reconnect re-subscribes to the same request and sees current progress

### Feature: Truthful results (US-9)

**BDD-9.1 Custom endpoint checked** — US-9 AS-1 — Error Path
- Given a key valid at the catalogue endpoint and a dead custom endpoint
- Then "saved but not working"

**BDD-9.2 Keyless local** — US-9 AS-2 — Happy Path
- Given a local provider without a key
- Then the check runs and reports working

**BDD-9.3 Non-OpenAI protocol** — US-9 AS-1 — Alternate Path
- Given an Anthropic-protocol provider
- Then the protocol-aware listing is used

**BDD-9.4 Partly saved** — US-9 AS-3 — Edge Case
- Given `create_agent` saves the agent but membership publication fails
- Then "partly saved" with agent id and failed step

**BDD-9.5 Delegation read-back** — US-9 AS-3 — Error Path
- Given an edge to a non-member
- Then failed: nothing saved

**BDD-9.6 Crash between credential selection and config write** — US-9 AS-3 — Edge Case
- Given the config write fails after the reference was selected
- When the result is read back after restart
- Then the durable state is reported ("partly saved": reference stored, integration unchanged)

**BDD-9.7 Listing works, chosen model does not** — US-9 AS-1 — Edge Case
- Given a provider whose model list succeeds but whose plan-chosen model is unavailable
- When `configure_provider` reports
- Then it says "working: model listing" and names that model execution was not tested

### Feature: Messaging and attention (US-10)

**BDD-10.1 Jim messages Omnipus** — US-10 AS-1 — Happy Path
- Given no delegation edge
- When Jim sends to Omnipus's address
- Then admitted to Omnipus's main

**BDD-10.2 Attention on pending card** — US-10 AS-2 — Happy Path (Outline)
- Given Omnipus's main (or one of its helpers) has a pending `<card>`
- Then Omnipus's `needs_attention` is true

| card |
|---|
| question |
| approval |
| secret request |
| helper approval |
| helper secret request |

**BDD-10.3 No attention for a plain message** — US-10 AS-3 — Edge Case
- Given Jim's message and no pending card
- Then `needs_attention` false

**BDD-10.4 Reply to requester** — US-10 AS-1 — Happy Path
- Given Omnipus finishes Jim's request
- Then Jim receives the request-bound reply

**BDD-10.5 Read-only question** — US-10 AS-4 — Happy Path
- Given Jim asks "which connectors does Mia have?"
- Then Omnipus answers and no change tool runs

**BDD-10.6 Unknown attention** — US-10 AS-5 — Error Path
- Given the session list fails
- When the sidebar renders
- Then the entry shows "unknown", not "nothing pending"

### Feature: Helpers (US-11)

**BDD-11.1 Main stays available** — US-11 AS-1 — Happy Path
- Given Go on W18
- When the helper applies
- Then a new user message to Omnipus starts a turn in the main

**BDD-11.2 Helper approval in main, other workspace active** — US-11 AS-2 — Happy Path
- Given workspace B active, Omnipus chat foreground
- When an Omnipus helper calls `add_mcp_server`
- Then the approval renders in the Omnipus chat naming the helper; attention true

**BDD-11.3 Unrelated approvals keep their scope** — US-11 AS-2 — Alternate Path
- Given a Mia approval in workspace A while B is active
- Then not shown in B

**BDD-11.4 Helper question relayed** — US-11 AS-3 — Happy Path
- Given a helper calls `AskUserQuestion`
- Then Omnipus asks the user on its own card and passes the answer back

**BDD-11.5 Stale write** — US-11 AS-4 — Error Path
- Given helper A updated Jim to revision r2
- When helper B writes Jim with r1
- Then B gets `CONFLICT`; Jim keeps A's change

**BDD-11.6 Self-row removed** — US-11 AS-1 — Error Path
- Given the operator removed Omnipus's self-row
- When Omnipus delegates to itself
- Then refused visibly

**BDD-11.7 Depth 1** — US-11 AS-1 — Edge Case
- Given an Omnipus helper
- When it delegates to itself
- Then refused by depth

**BDD-11.8 Reload during pending helper approval** — US-11 AS-2 — Edge Case
- Given a pending helper approval
- When the browser reloads
- Then the approval is restored in the Omnipus chat from the snapshot

**BDD-11.9 Shared resource changed since the plan** — US-11 AS-5 — Error Path (Outline)
- Given a confirmed plan carrying the revision token of `<resource>`
- When `<other writer>` changes it first
- Then the helper's write is refused, nothing is overwritten, and a revised summary returns to the main

| resource | other writer |
|---|---|
| provider `openai` | Marketing's helper (tool) |
| provider `openai` | Settings → Providers save |
| MCP server `notion` | Settings → Connectors save |
| channel `tg-1` | another helper |
| mailbox mia/Sales | Settings → Mail save |
| provider `ollama` (token `absent`) | Settings adds it |

**BDD-11.10 Shared skill contended** — US-11 AS-4 — Error Path
- Given helpers for Sales and Marketing both edit the global skill `crm`
- When the second writes with the older revision
- Then it receives `CONFLICT` and returns a revised summary

**BDD-11.11 Policy reload during a helper** — US-11 AS-2 — Edge Case
- Given an Omnipus helper is applying a plan
- When the operator tightens `create_agent` to deny globally and the configuration reloads
- Then the helper's next `create_agent` is refused under the new policy and the report lists it as not done

### Feature: Screens (US-12)

**BDD-12.1 Omnipus entry keeps workspace** — US-12 AS-1 — Happy Path
- Given Sales active
- When the Omnipus entry is clicked
- Then Omnipus's chat opens; Sales stays active

**BDD-12.2 Attach failure** — US-12 AS-1 — Error Path
- Given attach fails
- Then sending is disabled and Retry is shown

**BDD-12.3 Team list first** — US-12 AS-2 — Happy Path
- Given Sales with Mia and a worker
- When Team opens
- Then list view selected; Mia's card complete; worker in a separate section

**BDD-12.4 Picker eligibility** — US-12 AS-3 — Happy Path
- Given Orphan (no workspace), Mia (already in Sales), Judge, Plan Supervisor, Omnipus
- When the picker opens in Sales
- Then only Orphan is listed, marked "not in any workspace"

**BDD-12.5 Delete everywhere impact** — US-12 AS-2 — Happy Path
- Given Mia in Sales and Research
- When Delete everywhere is chosen
- Then both are listed before confirmation

**BDD-12.6 System agents** — US-12 AS-4 — Happy Path
- Given no working provider
- When Settings → System agents → Omnipus
- Then the model can be changed and saved

**BDD-12.7 Old links** — US-12 AS-5 — Error Path (Outline)
- Given `<url>`
- Then not-found, no redirect

| url |
|---|
| /agents |
| /agents/mia |
| /admin/chat |

**BDD-12.8 No create form** — US-12 AS-5 — Error Path
- Given any screen
- Then no manual create action exists; Add agent opens Omnipus

**BDD-12.9 Keyboard secret card** — US-12 AS-6 — Edge Case
- Given a two-field Slack card
- When operated by keyboard
- Then fields labelled, errors announced, Enter does not reach the composer, Go never pre-selected, focus returns to the composer

**BDD-12.10 Team actions without hover** — US-12 AS-6 — Edge Case
- Given keyboard focus or touch on a card
- Then Edit, Remove and Delete everywhere are reachable

**BDD-12.11 Workspace entry rule** — US-12 AS-1 — Happy Path (Outline; dataset D4)
- Given `<ws>` with team `<team>` and nothing remembered
- Then `<result>`

| ws | team | result |
|---|---|---|
| Sales | [Mia, Jim] | Mia's main |
| Default | [Omnipus (hidden), Mia] | Mia's main |
| Research | [] | empty state with "Set up a team" |

**BDD-12.12 Omnipus self-row in graph** — US-12 AS-8 — Happy Path
- Given the default workspace's Team graph
- When the user sets the self-row depth to 0 and reloads, then removes it
- Then each change persists, and the next Omnipus helper launch is refused visibly

**BDD-12.13 Screen states** — US-12 AS-7 — Error Path (Outline; §6.3)
- Given `<surface>` and `<condition>`
- Then `<shown>`

| surface | condition | shown |
|---|---|---|
| Team list | first load fails | error + Retry, no setup offer |
| Team list | refresh fails with cached data | cached list + "may be out of date" |
| Picker | add result unknown | "not confirmed" + Retry |
| System agents | save result unknown | "not confirmed" + Retry |
| Omnipus entry | attach pending | send disabled |
| Connection Configure side panel | no workspace chosen | Save disabled |

**BDD-12.14 Keyboard journeys** — US-12 AS-6 — Edge Case (Outline)
- Given keyboard only
- When the user performs `<journey>`
- Then it completes without a pointer

| journey |
|---|
| open Omnipus entry from any workspace |
| Add agent → Create with Omnipus / Add an existing agent |
| switch Team list ↔ graph |
| open Settings → System agents → Omnipus editor |
| W18 report → Open chat |

**BDD-12.15 Connection routing requires a workspace** — US-12 AS-9 — Error Path
- Given the connection's Configure side panel on the Connectors page
- When the user picks an agent but no workspace
- Then Save is disabled with "choose a workspace"

### Feature: No default agent (US-13)

**BDD-13.1 Unbound channel loud drop** — US-13 AS-1 — Error Path (Outline; dataset D3)
- Given an instance `<state>`
- When a message arrives
- Then no agent turn starts; degraded with "bind this channel to an agent in a workspace"; error log; audit entry

| state |
|---|
| unbound, no named agent |
| unbound, legacy named agent Mia |

**BDD-13.2 Bound agent unavailable** — US-13 AS-2 — Error Path (Outline)
- Given bound to `<agent>` in Sales where `<problem>`
- Then the same loud drop with reason `<reason>`

| agent | problem | reason |
|---|---|---|
| mia | deleted | bound agent mia not found |
| worker | is a worker | bound agent is not a chat target |
| jim | not a Sales member | bound agent jim is not in Sales |

**BDD-13.3 New chat/session without agent** — US-13 AS-3 — Error Path (Outline)
- Given `<request>` without an agent
- Then `<result>`

| request | result |
|---|---|
| WS frame without session id | error frame "an agent must be named" |
| REST session create | 400 |

**BDD-13.4 Stale owner refused** — US-13 AS-4 — Error Path (Outline)
- Given a `<work>` whose canonical owner X was deleted and Jim also participates
- When it would run
- Then refused visibly; Jim does not run it

| work |
|---|
| task |
| goal turn |
| steering item |

**BDD-13.5 No default control** — US-13 AS-5 — Happy Path
- Given Settings and the editor
- Then no default-agent control; agent responses have no `default`

**BDD-13.6 Boot and reload without default** — US-13 AS-6 — Edge Case
- Given no default agent, a real gateway boot then a config reload
- Then `/api/v1/tools`, the tool registry, startup info and the skill list return the expected central content, and channel recording writes `<base>/state/state.json`

**BDD-13.7 Old config key ignored** — US-13 AS-5 — Edge Case
- Given `agents.defaults.default_agent_id: "mia"` on load and on reload
- Then the gateway runs, the key has no effect, and a warning names it

**BDD-13.8 Routing write requires the pair** — US-13 AS-1 — Error Path (Outline)
- Given a routing write with `<body>`
- Then `<result>`

| body | result |
|---|---|
| workspace + agent | 200 |
| agent only | 400 |
| workspace only | 400 |
| blank strings | 400 |
| null fields | 400 |

**BDD-13.9 System message without origin** — US-13 AS-3 — Error Path
- Given a system message with no async origin
- Then it is discarded with an error log and an audit entry

**BDD-13.10 Unroutable error still published** — US-13 AS-1 — Edge Case
- Given an outbound error reply with no producing agent
- Then it is published without borrowing another agent's state

**BDD-13.11 Warm the default workspace's browser** — US-13 AS-7 — Happy Path
- Given browser managers for Default and Sales
- When the gateway boots
- Then Default's browser is warmed, not Sales's
- And with no manager for Default, nothing is warmed and the reason is logged

**BDD-13.12 Degraded clears on binding** — US-13 AS-1 — Alternate Path
- Given a degraded unbound instance
- When a valid binding is saved
- Then degraded clears and the next message routes

**BDD-13.13 Unrouted stays loud with person routing** — US-13 AS-8 — Error Path
- Given connection tg-1 bound to Sales with main agent Mia, and Mia deleted
- When an allowed person with no person rule writes
- Then the message is dropped loudly ("main agent mia not found"); no other agent receives it

### Feature: Channel person routing (US-16)

**BDD-16.1 Main agent for allowed people without a rule** — Traces to US-16 AS-1 — Happy Path
- Given tg-1 bound to Sales, main agent Mia, allow list [Anna, Ben], rule Anna → Jim
- When Ben writes
- Then Mia's turn starts in Sales

**BDD-16.2 Person and group rules** — US-16 AS-2 — Happy Path (Outline)
- Given tg-1 as above plus rule group G → Researcher2
- When `<sender>` writes `<where>`
- Then `<agent>` answers

| sender | where | agent |
|---|---|---|
| Anna | direct | Jim |
| Ben | in group G | Researcher2 |
| Ben | direct | Mia |

**BDD-16.3 Stranger reply and operator visibility** — US-16 AS-3 — Error Path
- Given Carl is not on tg-1's allow list
- When Carl writes twice within an hour
- Then Carl receives "not authorised" once, no agent turn starts, the connection status shows 2 strangers and Carl as the last one, and a log + audit entry exists

**BDD-16.4 Rules are per connection** — US-16 AS-4 — Happy Path
- Given tg-1 and tg-2 in Sales, tg-1 rule Anna → Jim, tg-2 no rules (main Mia)
- When Anna writes to tg-2
- Then Mia answers

**BDD-16.5 Type-only legacy rules ignored** — US-16 AS-4 — Edge Case
- Given a legacy binding matching channel type "telegram" without an instance, and a legacy `channel_policies.telegram.routing_rules` entry
- When the gateway loads
- Then both are ignored with a warning naming them; routing uses only instance tables

**BDD-16.6 Derived per-agent list** — US-16 AS-5 — Happy Path
- Given Jim is the target of rule Anna on tg-1 and main agent of slack-1 in Marketing
- When Jim's editor opens
- Then it lists "tg-1 (Sales): Anna" and "slack-1 (Marketing): everyone allowed", read-only, each linking to that connection's Configure side panel

**BDD-16.7 Conversation mode** — US-16 AS-6 — Happy Path (Outline)
- Given tg-1 conversation mode `<mode>`
- When Anna and Ben each write to Mia
- Then `<result>`

| mode | result |
|---|---|
| agent's main chat (default) | both messages appear in Mia's Sales main chat |
| one chat per person | two separate conversations, one per person, in Mia's Sales History |

**BDD-16.8 Mode change without restart** — US-16 AS-6 — Edge Case
- Given mode main chat
- When the operator switches to one chat per person and saves
- Then the next message uses the new mode without a gateway restart

**BDD-16.9 Omnipus connect sets main agent and rules** — US-16 AS-7 — Happy Path
- Given a confirmed W13 plan "Telegram for Sales, main agent Mia, Anna → Jim"
- When `connect_channel` runs after approval
- Then tg-1 is bound to Sales with main agent Mia and the rule Anna → Jim

**BDD-16.10 Invalid rule refused on save** — US-16 AS-8 — Error Path (Outline)
- Given tg-1 in Sales
- When a rule to `<agent>` is saved
- Then `<result>`

| agent | result |
|---|---|
| Jim (Sales member) | saved |
| Ray (not in Sales) | refused "Ray is not in Sales" |
| a worker | refused "not a chat target" |
| unknown id | refused "agent not found" |

**BDD-16.11 Rule target removed later** — US-16 AS-8 — Error Path
- Given rule Anna → Jim and Jim removed from Sales
- When Anna writes
- Then the message is dropped loudly ("person rule → jim not in Sales"); Mia does not receive it

**BDD-16.12 Same person across connections** — US-16 AS-6 — Alternate Path
- Given `identity_links` link Anna's Telegram and Slack ids and both connections use one chat per person
- When Anna writes on both
- Then both land in the same per-person conversation for that agent

**BDD-16.13 Group trigger still applies** — US-16 AS-2 — Alternate Path
- Given tg-1 with mention-only groups and rule group G → Researcher2
- When a member writes in G without mentioning the bot
- Then no agent turn starts (existing behaviour)

**BDD-16.14 Per-person conversations hidden in the sidebar** — US-16 AS-9 — Happy Path
- Given tg-1 in per-person mode and Anna and Ben have written to Mia
- When the sidebar renders
- Then Mia's row shows a collapsed "2 hidden conversations" control; un-hiding Anna's keeps it visible after reload; both appear in Mia's History

**BDD-16.15 Strangers inside a group** — US-16 AS-3 — Edge Case (Outline)
- Given tg-1 allow list [Anna] and group G `<state>`
- When Carl (not on the list) writes in G
- Then `<result>`

| state | result |
|---|---|
| G in the allow list | Carl is admitted (group admits all members) |
| G not admitted | no reply in G; status count +1; log and audit entry |

**BDD-16.16 Global conversation scope retired** — US-16 AS-6 — Edge Case
- Given a config with `session.dm_scope: per-peer`
- When the gateway loads and reloads
- Then a warning names the ignored key and each connection uses its own mode

### Feature: Group chats as rooms (US-17)

**BDD-17.1 Team room exists with workspace members** — Traces to US-17 AS-1 — Happy Path
- Given Sales with members Mia, Jim and worker GP
- When the Group chats section renders
- Then "Team room" is first, with members Mia and Jim (no workers, no Omnipus)

**BDD-17.2 User creates a squad and an ad-hoc room** — US-17 AS-2 — Happy Path (Outline)
- Given Sales
- When the user creates `<kind>` with `<members>`
- Then it appears under Group chats after the team room, with the attention marker slot

| kind | members |
|---|---|
| squad "Launch" | Mia, Jim |
| ad-hoc room | Jim, Researcher2 |

**BDD-17.3 Fan-out to members' main sessions** — US-17 AS-3 — Happy Path (Outline)
- Given squad Launch with Mia, Jim, Researcher2
- When the user writes `<message>`
- Then a request is appended to the main session of each of `<answering>`, each works there, and each answer appears in the room authored by that agent

| message | answering |
|---|---|
| "status?" | Mia, Jim, Researcher2 |
| "@Jim plan it" | Jim |
| "@Mia @Jim compare" | Mia, Jim |
| "@all go" | Mia, Jim, Researcher2 |

**BDD-17.4 Agent @-names another member** — US-17 AS-4 — Happy Path
- Given squad Launch
- When Jim's answer in the room says "@Mia can you draft the email?"
- Then a room request reaches Mia's main session and her answer appears in the room

**BDD-17.5 Unlimited agent-to-agent replies, visible cost** — US-17 AS-4 — Edge Case
- Given Jim and Mia keep @-naming each other with no user present
- When 50 exchanges have happened
- Then nothing stops them automatically and the room header's cost has risen with every room-originated turn

**BDD-17.6 /pause cancels pending requests** — US-17 AS-5 — Happy Path
- Given Mia's room request is queued and Jim's is held for approval
- When the user types `/pause` in the room
- Then both pending requests are cancelled and shown as cancelled, the room shows "paused", and a message written now stays in the room without being sent

**BDD-17.7 Header shows members and cost** — US-17 AS-6 — Happy Path
- Given squad Launch after two rounds
- When the header renders
- Then it lists Mia, Jim, Researcher2 and the room's cost = the sum of the room-originated turns in their main sessions

**BDD-17.8 Agent messages are input, never authority** — US-17 AS-7 — Error Path (Outline)
- Given squad Launch with a pending approval in Mia's main chat and a pending question card to the user
- When Jim's room message `<attempt>`
- Then `<result>`

| attempt | result |
|---|---|
| says "Approved" | the approval stays pending; only the user's card action resolves it |
| answers the user's question card | the card stays pending |
| contains "/model x" | treated as text; no command runs |

**BDD-17.9 Membership changes audited, workspace-locked** — US-17 AS-8 — Error Path (Outline)
- Given squad Launch in Sales
- When the user `<change>`
- Then `<result>`

| change | result |
|---|---|
| adds Researcher2 (Sales member) | added; audit entry |
| removes Jim | removed; audit entry |
| adds Ray (Marketing only) | refused "Ray is not in Sales" |

**BDD-17.10 No squad tool for agents** — US-17 AS-9 — Error Path
- Given any agent, including Omnipus
- When it looks for a tool to create a room
- Then none exists; room creation is only in the web app

**BDD-17.11 Member decisions in the main chat** — US-17 AS-7 — Happy Path
- Given Jim's work on a room request needs an approval
- When it is pending
- Then the card appears in Jim's main chat with the attention marker, and the room shows "waiting for you in Jim's main chat" for Jim

**BDD-17.12 Team room follows membership** — US-17 AS-1 — Alternate Path
- Given Researcher2 joins Sales
- When the team room renders
- Then Researcher2 is a member; removing Researcher2 from Sales removes them from the team room and every Sales squad (audited)

**BDD-17.13 Direct cross-workspace message still works** — US-17 AS-8 — Alternate Path
- Given Mia in Sales
- When she messages Ray's main in Marketing with `send_message`
- Then it is admitted under the existing peer rules; no room is involved

**BDD-17.14 Busy member answers later** — US-17 AS-10 — Edge Case (Outline)
- Given squad Launch and `<condition>` for Mia
- When the user writes "status?"
- Then the room shows Mia as `<state>` and her answer arrives when `<when>`

| condition | state | when |
|---|---|---|
| Mia's main is mid-turn | queued | her current turn settles |
| Mia's message policy is `ask` | held for your approval | the user approves the admission |
| Mia is idle | working | at once |

**BDD-17.15 Collapsed entries in the main chat** — US-17 AS-11 — Happy Path
- Given squad Launch
- When the user writes "@Mia draft it" and Mia answers
- Then Mia's main chat shows one collapsed entry "From group Launch: draft it" that expands to the request and its room context, followed by her answer, and the room shows the same answer

**BDD-17.16 Room context travels with the request** — US-17 AS-3 — Edge Case
- Given the user wrote "@Jim plan it", Jim answered, then the user wrote "@Mia review Jim's plan"
- When Mia's request arrives in her main session
- Then it includes the room messages since Mia's last room request (bounded), so Mia can see Jim's plan

**BDD-17.17 Running work after /pause** — US-17 AS-5 — Edge Case
- Given Jim's main-chat turn for a room request was already running when the user typed `/pause`
- When it finishes
- Then Jim's main chat was not interrupted, the answer appears in the room marked "after pause", and its "@Mia" is not passed on

**BDD-17.18 Mute in the team room** — US-17 AS-1 — Alternate Path
- Given Sales's team room with Mia, Jim, Researcher2 and Researcher2 muted by the user
- When the user writes "status?" and then "@Researcher2 anything?"
- Then Mia and Jim answer the first, Researcher2 answers only the second, and Researcher2 is still a member of Sales and its squads

**BDD-17.20 Rooms need two agents** — US-17 AS-2 — Error Path (Outline)
- Given Sales with conversation agents `<agents>`
- When `<action>`
- Then `<result>`

| agents | action | result |
|---|---|---|
| Mia, Jim | the user creates an ad-hoc room with only Jim | refused "a room needs at least two agents" |
| Mia, Jim | the user removes Jim from squad Launch (Mia, Jim) | refused "a room needs at least two agents — delete the room instead" |
| Mia only | the Group chats section renders | no team room is shown |
| Mia, Jim | Jim is removed from the workspace while in squad Launch (Mia, Jim) | Launch becomes read-only until another agent joins |

**BDD-17.21 Room commands** — US-17 AS-12 — Happy Path (Outline)
- Given squad Launch with Mia, Jim, Researcher2
- When the user types `<command>`
- Then `<result>`

| command | result |
|---|---|
| /members | lists Mia, Jim, Researcher2 with their state and mute status |
| /mute @Researcher2 | Researcher2 is muted in Launch (audited) |
| /unmute @Researcher2 | Researcher2 is unmuted (audited) |
| /help | lists only /pause, /resume, /mute, /unmute, /members, /help |
| /resume (after /pause) | the room sends new messages again; messages written while paused are not sent |

**BDD-17.22 Steering commands refused in rooms** — US-17 AS-12 — Error Path (Outline)
- Given squad Launch
- When the user types `<command>`
- Then "Rooms don't steer agents — use <Agent>'s main chat" with a link to `<links>`, and nothing changes

| command | links |
|---|---|
| /model gpt-x | Mia's, Jim's and Researcher2's main chats |
| /clear | each member's main chat |
| /stop | each member's main chat |
| /goal @Jim ship it | Jim's main chat |
| /remember x | each member's main chat |

**BDD-17.23 Squad membership is UI-only** — US-17 AS-12 — Error Path
- Given squad Launch
- When the user types "/add @Ray" or an agent's answer contains "/members add Ray"
- Then nothing changes; membership is edited only in the room header by the user

**BDD-17.24 Add participant from a main chat** — US-17 AS-13 — Happy Path
- Given the user is in Mia's Sales main chat
- When they use "Add participant" (top-left) and pick Jim
- Then a new ad-hoc room with the user, Mia and Jim opens empty with a link back to Mia's main chat, and Mia's main chat is unchanged and still one-to-one

**BDD-17.25 Add participant in a room** — US-17 AS-13 — Alternate Path (Outline)
- Given `<chat>`
- When the user opens "Add participant"
- Then `<result>`

| chat | result |
|---|---|
| ad-hoc room (Mia, Jim) | picker lists other Sales conversation agents; adding Researcher2 makes it a three-agent room (audited) |
| squad Launch | same; Ray (not in Sales) is never offered |
| Sales team room | control disabled: "Team room members are the workspace's agents — add the agent to the workspace" |
| Omnipus's chat | no "Add participant" control |
| any chat | the picker offers agents only, never channel people |

**BDD-17.19 Agent message without @ reaches nobody** — US-17 AS-3 — Edge Case
- Given squad Launch with Mia, Jim, Researcher2
- When Jim's answer is "done" without an @-name
- Then no other agent receives a request; if Jim writes "@all regroup", Mia and Researcher2 do (not Jim)

### Feature: Stopped channel group conversations (US-18)

**BDD-18.1 Authorised person resumes** — US-18 AS-1 — Happy Path (Outline)
- Given a Telegram group conversation the operator stopped
- When `<who>` writes
- Then `<result>`

| who | result |
|---|---|
| Anna (individually on the allow list) | resumes |
| the operator in the web app | resumes |
| Ben (admitted only via the group) | stays stopped; the group gets "The assistant is paused." once for this stop |

**BDD-18.2 Automatic wakes never resume** — US-18 AS-3 — Error Path (Outline)
- Given the stopped group conversation
- When `<wake>` fires
- Then it stays stopped

| wake |
|---|
| heartbeat |
| timer / scheduled task |
| queued wake |

**BDD-18.3 Others are not silently dropped** — US-18 AS-2 — Error Path
- Given the conversation is stopped
- When Ben writes
- Then Ben's message is not processed, it is recorded as received-while-stopped for the operator, and the group has received "The assistant is paused." exactly once since the stop (a second message adds no second notice)

### Feature: Channel steering (US-19)

**BDD-19.1 Entitled participant steers this conversation only** — US-19 AS-1 — Happy Path (Outline)
- Given Anna's person entry on tg-1 has steering on, and Mia also has a web main chat
- When Anna sends `<command>`
- Then `<effect>` and Mia's configured settings, the web chat and other channel conversations are unchanged

| command | effect |
|---|---|
| /model gpt-x | Anna's conversation uses gpt-x |
| /effort high | Anna's conversation uses high effort (refused while no per-conversation effort setting exists) |
| /clear | Anna's conversation context is cleared |
| /stop | Anna's conversation's current turn stops |
| /goal ship the draft | a goal starts in Anna's conversation |

**BDD-19.2 UI-only commands refused for everyone** — US-19 AS-3 — Error Path (Outline)
- Given Anna (steering on) on tg-1
- When she sends `<command>`
- Then "available in the web app or CLI" and nothing changes

| command |
|---|
| /channels |
| /config |
| /skills |
| /tasks |
| /sessions |
| /switch-agent mia |

**BDD-19.3 Un-entitled participant** — US-19 AS-2 — Error Path
- Given Ben's entry has steering off (default)
- When Ben sends /model gpt-x twice within an hour
- Then nothing changes, the agent does not see the command, Ben gets "Commands are not enabled for you in this conversation." once, and both attempts are logged

**BDD-19.4 Group members cannot steer** — US-19 AS-4 — Error Path
- Given group G admitted on tg-1 and Carl admitted only via G
- When Carl sends /clear in G
- Then it is treated as an un-entitled command (BDD-19.3 handling)

**BDD-19.5 /help for every allowed participant** — US-19 AS-2 — Alternate Path
- Given Ben (steering off)
- When Ben sends /help
- Then he receives the help text, which lists only what he may use

**BDD-19.6 Flag edited in the Configure side panel** — US-19 AS-4 — Happy Path
- Given the Connectors page, tg-1's Configure side panel
- When the operator turns steering on for Anna and saves
- Then Anna's next steering command works without a restart; turning it off again stops it

**BDD-19.7 Rooms use their own command set** — US-19 AS-1 — Alternate Path
- Given squad Launch in the web app
- When the user sends /model gpt-x and Jim's room message contains "/pause"
- Then /model is refused with "Rooms don't steer agents — use <Agent>'s main chat" and links, and Jim's text is not a command (FR-168)

### Feature: Conversation awareness (US-20)

**BDD-20.1 Main chat sees the list** — US-20 AS-1 — Happy Path
- Given Mia in Sales has a per-person chat with Anna (recap: "Anna wants a refund for order 42"), squad Launch and an ad-hoc chat
- When a turn starts in Mia's Sales main chat
- Then her context contains three lines with who, where, last activity and one-line summary, and no message content

**BDD-20.2 Read-only look-up** — US-20 AS-2 — Happy Path
- Given that list
- When Mia calls `read_conversation` for Anna's chat
- Then she receives its recent messages read-only; nothing is sent, resumed or changed in that conversation

**BDD-20.3 Important outcomes noted** — US-20 AS-3 — Happy Path
- Given Anna's chat goes idle and its recap lists "Anna prefers email" as worth remembering
- When the recap is written
- Then a memory note tagged with Anna's conversation exists in Mia's Sales memory, and `recall_memory` in Sales finds it

**BDD-20.4 Other workspaces invisible** — US-20 AS-4 — Error Path (Outline)
- Given Mia also has Marketing conversations, notes and a Marketing recap
- When Mia's Sales main chat runs `<step>`
- Then nothing from Marketing appears

| step |
|---|
| turn-start conversation list |
| `recall_memory` |
| session-start carry-forward |

**BDD-20.5 Look-up refused outside scope** — US-20 AS-5 — Error Path (Outline)
- Given Mia in Sales
- When she calls `read_conversation` on `<target>`
- Then refused

| target |
|---|
| a Jim-only extra chat in Sales |
| her own Marketing chat |
| an unknown id |

**BDD-20.6 List bounds and fallback** — US-20 AS-1 — Edge Case
- Given Mia has 35 other Sales conversations, 10 never recapped
- When a turn starts
- Then at most 20 are listed (most recent activity first), a never-recapped one shows its title instead of a summary, and the block stays under 2,000 characters

**BDD-20.7 No extra model calls** — US-20 AS-1 — Edge Case
- Given recaps are on
- When the list is built for 100 turns
- Then no model call is made to build it (summaries come from existing recaps)

### Feature: Permission changes (US-14)

**BDD-14.1 Permission tool asks** — US-14 AS-1 — Happy Path
- Given Go on "give Jim bash"
- Then `set_agent_permissions` runs with an approval

**BDD-14.2 update_agent rejects permission fields** — US-14 AS-1 — Error Path
- Given `update_agent` with tool policies
- Then refused, pointing to the permission tool

**BDD-14.3 New agent no management tools** — US-14 AS-2 — Error Path
- Given `create_agent` requesting `add_mcp_server: allow`
- Then that grant is refused

**BDD-14.4 Connector assignment asks** — US-14 AS-1 — Happy Path
- Given assigning Notion to Mia
- Then via the permission tool with an approval

### Feature: Secret removal (US-15)

**BDD-15.1 Consumers listed** — US-15 AS-1 — Happy Path
- Given a secret used by an enabled channel
- When removal is requested
- Then consumers are listed before the approval

**BDD-15.2 Refused before #1277** — US-15 AS-2 — Error Path
- Given #1277 not landed, an enabled consumer
- Then refused with the consumer list

**BDD-15.3 Separate result facts** — US-15 AS-3 — Happy Path
- Given #1277 landed
- Then durable deletion, live effect, "configuration still starts: yes/no"

**BDD-15.4 Unused secret removed** — US-15 AS-1 — Alternate Path
- Given no consumers
- Then deleted after approval

**BDD-15.5 Old version after replacement** — US-15 AS-4 — Alternate Path
- Given tg-1 switched to `.v2`
- When `.v1` is removed
- Then it is deleted; tg-1 keeps working

**BDD-15.6 Multi-consumer removal with #1277** — US-15 AS-3 — Edge Case
- Given #1277 landed and one secret used by an enabled channel and a mailbox
- When removal is approved and the reload fails
- Then the result names both consumers, reports the stale live value, and the next boot starts with both integrations degraded

---

## 9. Test-driven development plan

### 9.1 Test hierarchy

| Level | Scope | Where |
|---|---|---|
| Unit | Go functions; React components/stores | `pkg/**/…_test.go`; `src/**/*.test.tsx` |
| Integration | Real gateway handlers + agent loop + stores with a scripted provider; real shell/sidebar mount for SPA journeys | `pkg/gateway`, `pkg/agent`; `src/test/integration/*` |
| E2E | Real gateway + SPA; live-model `llm-*` shards | `tests/e2e/omnipus-*.spec.ts` in `tests/e2e/shards.json` |

CI is the authority; locally at most one narrow test at a time; `npm run typecheck`. Every test is shown red before green.

### 9.2 Test implementation order

| # | Test | Level | BDD |
|---|---|---|---|
| 1 | `pkg/coreagent::TestSeed_FreshInstall_SeedsOmnipusNotAvaAdmin` | Unit | 1.1 |
| 2 | `pkg/coreagent::TestReservedName_Omnipus` | Unit | 1.2 |
| 3 | `pkg/coreagent::TestOmnipusRolePolicies_MatchGrantInventory` (independent expected table) | Unit | 1.6 |
| 4 | `pkg/config::TestDefaultCeiling_OmnipusAskList` | Unit | 3.3 |
| 5 | `pkg/tools::TestAutoApproveAskList_IncludesNewTools` | Unit | 3.3 |
| 6 | `pkg/config::TestValidateOmnipusPolicyMap_RefusesAsk` | Unit | 3.5 |
| 7 | `pkg/credentials::TestStore_WriteBundle_AllOrNone` | Unit | 6.9 |
| 8 | `pkg/credentials::TestStore_VersionedReferences_NeverOverwrite` | Unit | 6.14, 15.5 |
| 9 | `pkg/secrets::TestRequest_StateMachine` (pending/submitted/cancelled/stopped/interrupted/refused) | Unit | 6.5 |
| 10 | `pkg/secrets::TestRecipientDescriptor_Mismatch` (dataset D1) | Unit | 6.3 |
| 11 | `pkg/sysagent/tools::TestConfigureProvider_RefusesPlaintext` | Unit | 6.8 |
| 12 | `pkg/sysagent/tools::TestConfigureProvider_ChecksEffectiveEndpoint` | Unit | 9.1, 9.2, 9.3 |
| 13 | `pkg/sysagent/tools::TestCreateAgent_TargetWorkspace` | Unit | 4.4, 4.6 |
| 14 | `pkg/sysagent/tools::TestSkillEdit_TargetShelf` | Unit | 4.5 |
| 15 | `pkg/sysagent/tools::TestCreateAgent_PartlySavedOutcome` | Unit | 9.4 |
| 16 | `pkg/sysagent/tools::TestUpdateWorkspace_EdgeToNonMemberRefused` | Unit | 9.5 |
| 17 | `pkg/sysagent/tools::TestUpdateAgent_RejectsPermissionFields` | Unit | 14.2 |
| 18 | `pkg/sysagent/tools::TestCreateAgent_RefusesManagementGrant` | Unit | 14.3 |
| 19 | `pkg/sysagent/tools::TestUpdateWorkspace_OmnipusMembershipRules` + REST removal case | Unit | 1.3, 1.4 |
| 20 | `pkg/routing::TestRoute_UnboundAnyKind_Unroutable` | Unit | 13.1 |
| 21 | `pkg/routing::TestRoute_BoundAgentUnavailable` | Unit | 13.2 |
| 22 | `pkg/agent::TestRegistry_NoDefaultAgentSymbols` (source scan for retirement only) | Unit | 13.5 |
| 23 | `pkg/agent::TestStaleOwner_RefusedEvenWithParticipant` | Unit | 13.4 |
| 24 | `src/store/toolApproval.test.ts` (Omnipus root case + unrelated scope) | Unit | 11.2, 11.3 |
| 25 | `src/components/chat/SecretRequestCard.test.tsx` (states, focus, labels) | Unit | 6.1, 6.7, 12.9 |
| 26 | `src/components/workspaces/WorkspaceTeamTab.test.tsx` (list first, states, actions, Omnipus hidden from list) | Unit | 1.5, 12.3, 12.5, 12.10, 5.9 |
| 27 | `src/components/workspaces/team/AddAgentPicker.test.tsx` (eligibility) | Unit | 12.4 |
| 28 | `src/lib/nav/workspaceEntry.test.ts` (dataset D4) | Unit | 12.11 |
| 29 | `src/components/settings/SystemAgentsSection.test.tsx` | Unit | 12.6 |
| 30 | `pkg/gateway::TestSecretSubmit_StatesAndIdempotency` | Integration | 6.4, 6.10 |
| 31 | `pkg/gateway::TestSecretValue_NeverLeaks_WithSentinelControl` (decoded provider requests; non-empty sinks; positive control) | Integration | 6.2, 6.13 |
| 32 | `pkg/gateway::TestSecretRequest_ChannelSessionRefused` | Integration | 6.6 |
| 33 | `pkg/gateway::TestSecretRequest_OmnipusHelperInMain_OrdinaryHelperRefused` | Integration | 6.11, 6.12 |
| 34 | `pkg/agent::TestPrivateSecret_ScopedInjection` (D6; owner, other agent, other workspace, external worker) | Integration | 7.1, 7.2, 7.4, 7.5 |
| 35 | `pkg/agent::TestPrivateSecret_OutputMaskingLimits` | Integration | 7.3 |
| 36 | `pkg/gateway::TestConnectChannel_ProvenFacts_WrongBinding` (fake platform) | Integration | 8.1, 8.2, 8.3, 8.7 |
| 37 | `pkg/gateway::TestConnectChannel_PairingCorrelated_Teardown` | Integration | 8.4, 8.5, 8.8 |
| 38 | `pkg/gateway::TestConnectMailbox_ReadSendSplit_NoSend` | Integration | 8.6 |
| 39 | `pkg/gateway::TestApprovalFrame_RootSessionForOmnipusHelper_Snapshot` | Integration | 11.2, 11.8 |
| 40 | `pkg/gateway::TestNeedsAttention_OmnipusIncludesHelperDecisions` | Integration | 10.2, 10.3 |
| 41 | `pkg/gateway::TestPeerMessage_ToOmnipus_NoEdge_ReadOnly` | Integration | 10.1, 10.4, 10.5 |
| 42 | `pkg/agent::TestOmnipusSelfDelegation_Depth1_RowRemoval` | Integration | 11.6, 11.7 |
| 43 | `pkg/gateway::TestWorkspaceCreate_EmptyTeam_KickoffToOmnipusMain` | Integration | 5.1, 5.8 |
| 44 | `pkg/gateway::TestInboundUnroutable_DegradedAuditedAndClears` | Integration | 13.1, 13.2, 13.12 |
| 45 | `pkg/gateway::TestNewChatAndSessionCreate_NoAgentRefused` | Integration | 13.3 |
| 46 | `pkg/gateway::TestAgentResponses_NoDefault_OldKeyIgnoredLoadReload` | Integration | 13.5, 13.7 |
| 47 | `pkg/gateway::TestGodMode_OmnipusNoApprovals` | Integration | 3.4 |
| 48 | `pkg/gateway::TestSecretRemoval_ConsumersAndPre1277Refusal` | Integration | 15.1, 15.2, 15.4, 15.5 |
| 49 | `pkg/gateway::TestSecretRemoval_ResultFacts` (requires #1277) | Integration | 15.3 |
| 50 | `pkg/agent::TestRevisionConflict_TwoHelpers` | Integration | 11.5 |
| 51 | `pkg/coreagent::TestOmnipusPrompt_GateRulesAndRoutingTable` (wording only) | Unit | 2.1, 3.1 |
| 52 | `tests/e2e/omnipus-entry.spec.ts` | E2E | 12.1, 12.2, 4.1, 12.14 |
| 53 | `tests/e2e/omnipus-retired-surfaces.spec.ts` | E2E | 12.7, 12.8, 13.5 |
| 54 | `tests/e2e/omnipus-team.spec.ts` (real shell) | E2E | 1.4, 1.5, 12.3, 12.4, 12.5, 12.10, 12.12, 12.13, 5.2 |
| 55 | `tests/e2e/omnipus-secret-card.spec.ts` | E2E | 6.1, 6.5, 6.7, 6.15, 12.9 |
| 56 | `tests/e2e/omnipus-helper-approvals.spec.ts` | E2E | 11.2, 11.8 |
| 57 | `tests/e2e/llm-omnipus-w1.spec.ts` (live) | E2E | 2.1, 3.1, 3.6, 4.2, 4.3, 4.4, 4.7 |
| 58 | `tests/e2e/llm-omnipus-routing.spec.ts` (live) | E2E | 2.2, 2.3, 2.4, 2.5 |
| 59 | `tests/e2e/llm-omnipus-w18.spec.ts` (live) | E2E | 5.3–5.7, 11.1, 11.4 |
| 60 | `tests/e2e/llm-omnipus-godmode.spec.ts` (live) | E2E | 3.2, 3.4, 3.7, 3.8 |
| 61 | `tests/e2e/llm-omnipus-permissions.spec.ts` (live) | E2E | 14.1, 14.4 |
| 62 | `pkg/gateway::TestSecretEndpoints_MalformedPayloads_ValidationOnOff` | Integration | 6.9, 6.16 |
| 63 | `pkg/gateway::TestMessageFrame_AgentRule_ValidationOnOff` | Integration | 13.3, 1.7 |
| 64 | `pkg/gateway::TestChannelRoutingWrite_PairRequired_ValidationOnOff` | Integration | 13.8 |
| 65 | `pkg/gateway::TestSecretTools_RealDispatch_BuiltinCustomJudge` | Integration | 6.18 |
| 66 | `pkg/gateway::TestSessionCreate_OmnipusSingleton_OrdinaryControl` | Integration | 1.7 |
| 67 | `pkg/agent::TestCrossWorkspaceAuthority_OmnipusOnly` | Integration | 1.8 |
| 68 | `pkg/gateway::TestSecretSubmit_UntrustedNoWrites` (store write counter) | Integration | 6.16, 6.17 |
| 69 | `pkg/gateway::TestSecretSubmit_NoReAuth_RawRouteStillReAuth` | Integration | 6.15 |
| 70 | `pkg/gateway::TestSecretReplacement_IntegrationKeepsOldUntilSelected` | Integration | 6.14 |
| 71 | `pkg/agent::TestPrivateSecret_ConcurrentWorkspaces` (race) | Integration | 7.6 |
| 72 | `pkg/secrets::TestPrivateVariableGrammar` (dataset D6) | Unit | 7.7 |
| 73 | `pkg/gateway::TestPrivateSecret_OmnipusTargetValidated` | Integration | 7.8 |
| 74 | `pkg/agent::TestPrivateSecret_AllChildPaths_NotGatewayEnv` | Integration | 7.9 |
| 75 | `pkg/gateway::TestSharedProvider_ExpectedBefore` | Integration | 11.9 |
| 76 | `pkg/sysagent/tools::TestCredentialSelection_ConfigFailure_PartlySaved` | Unit | 9.6 |
| 77 | `pkg/gateway::TestSystemMessage_NoOrigin_DiscardedAudited` | Integration | 13.9 |
| 78 | `pkg/agent::TestPublishResponse_NilAgent_StillPublishes` | Unit | 13.10 |
| 79 | `pkg/gateway::TestWarmBrowser_DefaultWorkspace` | Unit | 13.11 |
| 80 | `pkg/gateway::TestBootReload_CatalogueSkillsStateWithoutDefault` | Integration | 13.6 |
| 81 | `src/components/skills/ChannelConfigPanel.test.tsx` (workspace required) | Unit | 12.15 |
| 82 | `src/components/layout/Sidebar.omnipusEntry.test.tsx` (attention unknown, keyboard) | Unit | 10.6, 12.14 |
| 83 | `pkg/coreagent::TestOmnipusPrompt_DependencyAndRoutingRules` (wording only) | Unit | 4.7, 2.5 |
| 84 | `pkg/secrets::TestPrivateVersion_FirstSelected_ReplacementStored` | Unit | 6.19, 6.20, 6.21 |
| 85 | `pkg/gateway::TestSelectSecretVersion_ScopeAndConcurrency` | Integration | 6.22, 6.23, 7.11 |
| 86 | `pkg/gateway::TestSecretSubmit_CommitReceipt_CrashCuts` (fault injection at each cut) | Integration | 6.24 |
| 87 | `pkg/gateway::TestSecretStop_AfterSelection_ReportsTruthfully` | Integration | 6.5 |
| 88 | `pkg/gateway::TestSharedResources_LockOrder_SettingsVsTool` (race; provider, MCP, channel, mailbox, absent) | Integration | 11.9 |
| 89 | `pkg/gateway::TestSensitiveSet_CompleteAcrossRebuilds` (Settings save, reload, sign-in, restart) | Integration | 6.25 |
| 90 | `pkg/config::TestRegisterSensitive_SerializedNoStaleReplace` (race) | Unit | 6.25 |
| 91 | `pkg/tools::TestPrivateEnv_ProtectedSetDrift_AllChildPaths` (real composers, document runtime on) | Integration | 7.7, 7.10 |
| 92 | `pkg/sysagent/tools::TestTargetScopedVerbs_SkillRemoveDiscoveryEnvSetup` | Integration | 4.8 |
| 93 | `pkg/sysagent/tools::TestConfigureProvider_ListingOnlyScopeStated` | Unit | 9.7 |
| 94 | `pkg/agent::TestSharedSkill_TwoHelpers_Conflict` | Integration | 11.10 |
| 95 | `pkg/gateway::TestSecretRemoval_MultiConsumer_FailedReload_NextBoot` (requires #1277) | Integration | 15.6 |
| 96 | `pkg/agent::TestHelper_PolicyReloadMidApply` | Integration | 11.11 |
| 97 | `pkg/routing::TestUnrouted_MainAgentMissing_Loud` | Unit | 13.13 |
| 98 | `pkg/routing::TestInstanceRouting_PersonGroupMain` (priorities 1–5 at instance scope, then main) | Unit | 16.1, 16.2, 16.4 |
| 99 | `pkg/config::TestLegacyTypeBindingsAndRoutingRules_IgnoredWithWarning` | Unit | 16.5 |
| 100 | `pkg/channels::TestStranger_ReplyOnceAndStatus` (rate limit; log; audit) | Integration | 16.3 |
| 101 | `pkg/gateway::TestPersonRule_SaveValidation_And_RuntimeLoudDrop` | Integration | 16.10, 16.11 |
| 102 | `pkg/routing::TestConversationMode_PerConnection_NoRestart` (incl. identity links) | Integration | 16.7, 16.8, 16.12 |
| 103 | `pkg/channels::TestGroupTrigger_UnchangedWithPersonRules` | Unit | 16.13 |
| 104 | `src/components/agents/ReachableBy.test.tsx` + `pkg/gateway::TestAgentReachability_Derived` | Integration | 16.6 |
| 105 | `tests/e2e/omnipus-channel-routing.spec.ts` (Connectors page → Configure side panel: main agent, allow list, person table, steering flags, mode, stranger status) | E2E | 16.1, 16.3, 16.7, 16.10 |
| 106 | `tests/e2e/llm-omnipus-connect-channel-rules.spec.ts` (live) | E2E | 16.9 |
| 107 | `src/components/layout/sidebar/PerPersonConversations.test.tsx` + `pkg/gateway::TestSessionSidebarVisible_DefaultHiddenForPerPerson` | Integration | 16.14 |
| 108 | `pkg/channels::TestGroupAdmission_AndGroupStrangerLoggedOnly` | Unit | 16.15 |
| 109 | `pkg/config::TestGlobalDMScope_IgnoredWithWarning_LoadReload` | Unit | 16.16 |
| 110 | `pkg/groupchat::TestTeamRoom_MembershipFollowsWorkspace` | Unit | 17.1, 17.12 |
| 111 | `pkg/gateway::TestGroupCreate_UserOnly_WorkspaceLocked_Audited` | Integration | 17.2, 17.9, 17.10 |
| 112 | `pkg/groupchat::TestMentionResolution_AllDefaultAndNamed` (dataset D7) | Unit | 17.3, 17.4 |
| 113 | `pkg/agent::TestRoomFanOut_RequestsToMains_AnswersAsGuestEntries` | Integration | 17.3, 17.4 |
| 114 | `pkg/agent::TestRoomNoLimit_CostFromRoomOriginatedTurns` | Integration | 17.5, 17.7 |
| 115 | `pkg/agent::TestRoomPause_CancelsPending_RunningWorkUntouched_AfterPauseNoFanOut` | Integration | 17.6, 17.17 |
| 116 | `pkg/agent::TestGroupAgentMessage_NoAuthority` | Integration | 17.8 |
| 117 | `pkg/gateway::TestRoomMemberDecisions_InMainChat_RoomShowsWaiting` | Integration | 17.11 |
| 118 | `pkg/gateway::TestDirectCrossWorkspaceMessage_Unchanged` | Integration | 17.13 |
| 119 | `tests/e2e/omnipus-group-chats.spec.ts` (sidebar section, header with cost and member states, @-picker incl. @all, squad creation, collapsed main-chat entries) | E2E | 17.1, 17.2, 17.3, 17.6, 17.7, 17.15 |
| 120 | `pkg/agent::TestStoppedChannelGroup_ResumeAuthority` | Integration | 18.1, 18.3 |
| 121 | `pkg/agent::TestStoppedChannelGroup_AutomaticWakesNeverResume` | Integration | 18.2 |
| 122 | `pkg/commands::TestChannelSteering_ConversationScoped` | Integration | 19.1 |
| 123 | `pkg/commands::TestChannelSteering_UIOnlyRefused_UnentitledAndGroupRefused` | Unit | 19.2, 19.3, 19.4 |
| 124 | `pkg/agent::TestAwarenessList_ContentBoundsWorkspace` | Integration | 20.1, 20.6, 20.7 |
| 125 | `pkg/tools::TestReadConversation_ReadOnlyAndScope` | Integration | 20.2, 20.5 |
| 126 | `pkg/agent::TestRecapOutcomes_ToWorkspaceMemory_NoCrossWorkspace` | Integration | 20.3, 20.4 |
| 127 | `tests/e2e/llm-omnipus-awareness.spec.ts` (live) | E2E | 20.1, 20.2 |
| 128 | `pkg/agent::TestRoomRequest_QueuedHeldWorkingStates` | Integration | 17.14 |
| 129 | `src/components/chat/RoomTrafficEntry.test.tsx` + `pkg/agent::TestRoomTraffic_CollapsedInMain` | Integration | 17.15 |
| 130 | `pkg/agent::TestRoomRequest_CarriesBoundedRoomContext` | Unit | 17.16 |
| 131 | `pkg/groupchat::TestTeamRoomMute_AllSkipsMuted_NamedStillAnswers` | Unit | 17.18 |
| 132 | `src/components/connectors/ConnectionConfigurePanel.steering.test.tsx` + `pkg/gateway::TestSteeringFlag_SaveNoRestart` | Integration | 19.6 |
| 133 | `pkg/commands::TestChannelHelp_ListsOnlyPermitted` | Unit | 19.5 |
| 134 | `pkg/agent::TestWebGroupCommands_UserOnly` | Integration | 19.7 |
| 135 | `pkg/groupchat::TestAgentMessageWithoutMention_ReachesNobody` | Unit | 17.19 |
| 136 | `pkg/groupchat::TestRoomCommands_AllowedSetAndRefusalLinks` | Unit | 17.21, 17.22 |
| 137 | `pkg/gateway::TestRoomMinimumTwoAgents_CreateRemoveWorkspaceChange` | Integration | 17.20, 17.23 |
| 138 | `src/components/chat/AddParticipant.test.tsx` + `pkg/gateway::TestAddParticipant_FromMainCreatesRoom` | Integration | 17.24 |
| 139 | `tests/e2e/omnipus-add-participant.spec.ts` (main chat → new room, room add, team room disabled, Omnipus absent, agents only) | E2E | 17.24, 17.25 |

Live-model specs use `openrouter` + `deepseek/deepseek-v4.1-flash`. Prompt-text tests prove wording only; a live pass does not prove the instruction-only Go gate cannot be bypassed (accepted R-A1).

### 9.3 Test datasets

**D1 — Recipient descriptor** (test 10)

| # | Recorded | Used | Boundary | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | openai @ https://api.openai.com/v1 | same | happy | accepted | BDD-6.3 |
| 2 | … /v1 | … /v1/ | trailing slash | accepted (normalised) | BDD-6.3 |
| 3 | https | http | scheme | refused | BDD-6.3 |
| 4 | catalogue host | evil.example | host | refused | BDD-6.3 |
| 5 | stdio `npx a` [x] | stdio `npx a` [x, y] | args | refused | BDD-6.3 |
| 6 | http https://m.example | sse https://m.example | transport | refused | BDD-6.3 |
| 7 | imap.a.com/user x | imap.a.com/user X | username case | refused | BDD-6.3 |
| 8 | tg-1 bot_token | tg-1 app_token | field | refused | BDD-6.3 |
| 9 | private Sales/mia VAR | private Marketing/mia VAR | workspace | refused | BDD-6.3 |
| 10 | private Sales/mia VAR | private Sales/jim VAR | agent | refused | BDD-6.3 |
| 11 | purpose "Clé API 🔑" | — | unicode display | rendered escaped | BDD-6.1 |

**D2 — Card values** (tests 7, 62)

| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | "" | empty | 400 | BDD-6.9 |
| 2 | 3 bytes | min−1 | 400 (cannot be masked) | BDD-6.13 |
| 3 | 4 bytes | min | accepted | BDD-6.2 |
| 4 | 16,384 bytes | max | accepted | BDD-6.2 |
| 5 | 16,385 bytes | max+1 | 400 | BDD-6.9 |
| 6 | 4-byte UTF-8 emoji × 4,096 | multibyte at max | accepted (bytes counted) | BDD-6.9 |
| 7 | value with NUL | invalid | 400 | BDD-6.9 |
| 8 | 8 fields / 9 fields | count max / max+1 | accepted / 400 | BDD-6.9 |
| 9 | body 65,537 bytes | body max+1 | 413 | BDD-6.16 |
| 10 | values + cancel | exclusivity | 400 | BDD-6.16 |
| 11 | extra field key | extra | 400 | BDD-6.16 |
| 12 | null value | null | 400 | BDD-6.16 |

**D3 — Inbound routing** (tests 20, 21, 44)

| # | Instance | Expected | Traces to |
|---|---|---|---|
| 1 | bound mia@Sales, mia member | routed to mia's main in Sales (control) | BDD-13.12 |
| 2 | unbound, no agent | drop "bind this channel to an agent in a workspace" | BDD-13.1 |
| 3 | bound mia@Sales, mia deleted | drop "bound agent mia not found" | BDD-13.2 |
| 4 | bound to a worker | drop "not a chat target" | BDD-13.2 |
| 5 | bound to entity that failed to load | drop (fail-closed) | BDD-13.2 |
| 6 | unbound, legacy named agent mia | drop "bind this channel to an agent in a workspace" | BDD-13.1 |
| 7 | bound jim@Sales, jim not a member | drop "jim is not in Sales" | BDD-13.2 |

**D4 — Workspace entry** (test 28): BDD-12.11 rows; remembered valid chat → that chat; remembered deleted chat → first conversation agent; member load failure → retryable error, no fallback.

**D5 — Ask / Auto / God Mode** (tests 4, 5, 47)

| Tool | Ask mode | Auto on | God Mode | Traces to |
|---|---|---|---|---|
| create_agent | runs | runs | runs | BDD-3.1 |
| delete_agent | asks | asks | runs | BDD-3.3, 3.4 |
| add_mcp_server | asks | asks | runs | BDD-3.3, 3.4 |
| configure_provider | asks | asks | runs | BDD-3.3 |
| connect_channel | asks | asks | runs | BDD-3.3 |
| set_agent_permissions | asks | asks | runs | BDD-14.1 |
| environment_setup | asks | asks | runs | BDD-3.4 |
| request_secret | runs (card appears) | runs | runs (card appears) | BDD-3.8 |
| select_secret_version | asks | asks | runs | BDD-6.20 |

**D6 — Private variable names and values** (tests 34, 72)

| # | Name / value | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | WEATHER_TOKEN | valid | accepted | BDD-7.7 |
| 2 | A (1 char) | min length | accepted | BDD-7.7 |
| 3 | 64-char name | max | accepted | BDD-7.7 |
| 4 | 65-char name | max+1 | refused | BDD-7.7 |
| 5 | weather_token | lowercase | refused | BDD-7.7 |
| 6 | PATH, HOME, USER, LOGNAME, SHELL, TZ, LANG, TMPDIR, TERM | child allowlist | refused | BDD-7.7 |
| 7 | HTTP_PROXY, HTTPS_PROXY, NO_PROXY | proxy | refused | BDD-7.7 |
| 8 | PIP_CACHE_DIR, npm_config_cache-style names | setup env | refused (protected) | BDD-7.7 |
| 9 | OMNIPUS_*, LD_*, DYLD_*, LC_*, XDG_* | protected prefixes | refused | BDD-7.7 |
| 9a | LC_ALL, XDG_CONFIG_HOME, PYTHONPATH, NODE_PATH, OSL_SOCKET_PATH, OMNIPUS_DOCUMENT_SKILLS | document runtime / inherited families | refused | BDD-7.7 |
| 9b | WEATHER_TOKEN with document runtime enabled | name contains TOKEN | present in the child (appended after the document layer) | BDD-7.10 |
| 10 | 1TOKEN, TO-KEN | grammar | refused | BDD-7.7 |
| 11 | Sales/mia=A, Marketing/mia=B | two workspaces | each sees own | BDD-7.5, 7.6 |
| 12 | Sales/mia, Sales/jim | two agents | jim sees none | BDD-7.2 |

The protected set is one list owned next to the environment composers: `allowedChildEnvKeys` and `allowedChildEnvPrefixes` (`pkg/sandbox/hardened_exec.go`), the setup layer (`pkg/tools/environment_setup_runner.go::setupEnv`, `OMNIPUS_ENV_*`), `composeExecutionEnv` and the document layer (`pkg/documentruntime/runtime.go::ChildEnvironment`), plus `OMNIPUS_`, `LD_`, `DYLD_`. The drift test (test 91) runs the real composers on foreground `bash`, background `bash` and `environment_setup` with the document runtime enabled and asserts every emitted key is protected — a literal scan of function bodies is not sufficient. Names are uppercase only, so platform case folding cannot merge two names.

**D7 — Mention resolution** (test 112)

| # | Message | Members | Expected addressees | Traces to |
|---|---|---|---|---|
| 1 | "status?" | Mia, Jim | Mia, Jim | BDD-17.3 |
| 2 | "@jim plan" | Mia, Jim | Jim (case-insensitive) | BDD-17.3 |
| 3 | "@Ray hi" (not a member) | Mia, Jim | nobody; "@Ray" shown unresolved | BDD-17.3 |
| 4 | "@all" | Mia, Jim | Mia, Jim | BDD-17.3 |
| 5 | "email@example.com" | Mia, Jim | treated as text, not a mention | BDD-17.3 |
| 6 | Jim: "@Mia draft" | Mia, Jim | Mia | BDD-17.4 |
| 7 | Jim: "done" (no @) | Mia, Jim | nobody (GQ-1 A) | BDD-17.19 |
| 8 | Jim: "@all regroup" | Mia, Jim, R2 | Mia, R2 (not Jim himself) | BDD-17.19 |

### 9.4 Regression requirements

| Existing behaviour | Existing tests | New regression test | Notes |
|---|---|---|---|
| Two-layer merge, God Mode floor | `pkg/tools/compositor_godmode_test.go::TestResolveEffectivePolicy_GodMode_AgentCeilingSurvives`, `…_LiftsGlobalCeiling` | No | Unchanged |
| Reconcile keeps operator values | `pkg/config/policy_ceiling_test.go` (`TestReconcileToolPolicyCeiling*`) | No | Ceiling values only |
| Approval queue, expiry, reconnect Deny-only stub, resolution | `pkg/gateway` approval tests (`ws_tool_approval*_test.go`); `src/store/toolApproval.resolution.test.ts` | Yes — BDD-11.3 | Non-Omnipus scope unchanged |
| AskUserQuestion park/resume, delegated relay | `pkg/api/generated/askuser_frames_contract_test.go`, `pkg/askuser/registry_test.go`, `registry_steer_test.go` | No | Reused |
| Kickoff mint-only protection | `pkg/gateway/workspace_setup_kickoff_test.go` | Yes — BDD-5.8 | Client path refused |
| ADR-029 bound routing and the bindings ladder | `pkg/routing/identity_route_test.go` (`TestResolveRoute*`), `channel-routing.spec.ts`, `channels-routing.spec.ts` | Yes — D3 row 1; BDD-13.13; tests 98–100 | Priority 1–5 match tests are kept and extended to instance scope; type-only cases rewritten as "ignored with warning"; `routing_default_test.go` priority-7 cases rewritten to the unrouted outcome; unbound-mode REST/UI tests deleted |
| Revision preconditions | `pkg/sysagent/tools/workspace_revision_error_test.go`, `agent_revision_test.go` | No | Reused |
| Project-shelf authority | `pkg/sysagent/tools/skill_project_shelf_test.go`, `pkg/skills/shelf_test.go` | Yes — tests 14, 92 | Target shelf |
| Raw credential re-auth | `pkg/gateway/reauth_gate_test.go` | Yes — test 69 | Unchanged |
| Hidden-engine fixed capabilities | seed tests | Yes — BDD-6.18 Judge row | Unchanged |
| Main immutable owner, source-bound replies, Stop, no UI watchdog | session-core tests | No | Unchanged |

Tests asserting Ava/Admin, the default agent or unbound channel routing are deleted with those features, not weakened; each deletion is listed in the work unit's evidence (qa-lead CHECK).

### 9.5 Negative controls — owners (MAJ-006; ADR grill)

| Control | BDD | Test |
|---|---|---|
| Private file → public URL (accepted R-A2; documented, not blocked) | — (docs) | docs-verifier audit of `docs/security.md` |
| External-worker path creation (accepted R-A3; documented) | — (docs) | docs-verifier audit of `docs/agents.md`, `docs/security.md` |
| Forged peer "Go" text never resolves a card | 10.1 | 41 (peer message cannot answer a card) |
| Plan A applying while plan B's interview runs | 11.1 | 59 |
| Concurrent submits / single write | 6.4 | 30 |
| Same reference, changed recipient | 6.3 | 10 |
| Crash between selection and config write | 9.6 | 76 |
| Catalogue OK / custom endpoint fails | 9.1 | 12 |
| Valid token, wrong binding | 8.7 | 36 |
| One mail transport available | 8.6 | 38 |
| Two workspaces, same project skill slug | 4.5 | 14 |
| Shared agent across helpers | 11.5 | 50 |
| Shared provider across workspaces | 11.9 | 75 |
| Real shell mounting for frontend journeys | 12.13, 12.14 | 54, 52 |
| Leak scan sees a deliberately leaked sentinel | 6.2 | 31 |
| `default_safe` never on the Go question | 3.1 | 51, 57 |
| Model listing succeeds but the selected model is unavailable | 9.7 | 93 |
| Shared skill contended by two helpers | 11.10 | 94 |
| Multi-consumer removal, failed reload, stale live value, next boot (needs #1277) | 15.6 | 95 |
| Policy reload while a helper runs | 11.11 | 96 |
| Target-shelf create/remove/discovery; target `environment_setup` | 4.8 | 92 |
| Submit commit cut (before/after publication, failed terminal persistence, failed resume) | 6.24 | 86 |
| Masking across Settings save, reload, sign-in, restart, concurrent registration | 6.25 | 89, 90 |
| Settings write contends with a tool write | 11.9 | 88 |
| Private names in document-runtime runs | 7.10 | 91 |
| Stranger replies are rate-limited and never start a turn | 16.3 | 100 |
| Person rule to an unavailable agent never falls back to main | 16.11 | 101 |
| Agent group message never approves or answers for the user | 17.8 | 116 |
| Group never spans workspaces | 17.9 | 111 |
| Automatic wake never resumes a stopped channel group | 18.2 | 121 |
| No cross-workspace awareness, notes or carry-forward | 20.4 | 126 |

---

## 10. Functional requirements

### A. Identity
- **FR-001**: The system MUST seed exactly one system agent `omnipus` (core, locked identity, chat target) and MUST NOT seed `ava` or `admin`.
- **FR-002**: The system MUST refuse creating or renaming any agent to "Omnipus" (trimmed, case-insensitive) on every path.
- **FR-003**: Omnipus MUST be a member of the default workspace only; adding it elsewhere or removing it from the default workspace MUST be refused.
- **FR-004**: Omnipus MUST be excluded from the Team list, sidebar agent rows, workspace-entry selection and the add-existing picker; its self-row MUST remain in the default workspace's Team graph.
- **FR-005**: Omnipus MUST have exactly one main chat; REST and WS paths that would mint a second Omnipus chat MUST be refused with the main's id; ordinary agents' extra chats are unaffected.
- **FR-006**: Omnipus MUST hold cross-workspace filesystem authority for setup work; no other agent gains it.
- **FR-007**: Omnipus's tool defaults MUST match §6.5; every other tool MUST be explicit per-agent deny.
- **FR-008**: Ava and Admin seeds, prompts, skills, identity branches and UI MUST be removed; a guard script MUST fail CI if `ava` or `admin` reappear as agent ids in production code.

### B. Workflows and routing
- **FR-010**: The system MUST ship Omnipus workflow skills (W1, W3–W7, W8/W18, W10–W17, capability planning, dependency checks) granted to Omnipus only, and MUST remove the Ava/Admin skills from the embedded set.
- **FR-011**: Omnipus's core prompt MUST hold the routing table and standing rules; each workflow skill MUST carry one "use when" line; a trimmed running skill MUST be reloaded before the next change.
- **FR-012**: A workflow-start message MUST make the server render the fixed instruction naming the workflow and target workspace.
- **FR-013**: For a genuinely ambiguous request Omnipus MUST ask; for a nested workflow it MUST load the main workflow without asking; for no match it MUST call no change tool.
- **FR-014**: `AskUserQuestion`'s description MUST permit one plan-confirmation use; the Go question MUST never set `default_safe`.
- **FR-015**: In God Mode, Omnipus's per-turn context MUST include a one-line mode note.

### C. Approval and permissions
- **FR-020**: The shipped ceiling MUST set `delete_agent`, `remove_skill`, `add_mcp_server`, `configure_provider`, `enable_channel`, `configure_channel` to `ask`.
- **FR-021**: `connect_channel`, `connect_mailbox`, `set_agent_permissions`, `remove_secret`, `select_secret_version` MUST ship `ask` and be on the Auto ask-list; `request_secret` and `list_secrets` MUST ship `allow`.
- **FR-022**: Every per-agent policy write path MUST refuse `ask` for Omnipus and accept `deny`; the editor MUST not offer Ask for Omnipus.
- **FR-023**: In God Mode, Omnipus and its helpers MUST raise no approval and no Go question; clarification questions for genuinely missing information and secret cards MUST still appear.
- **FR-024**: `set_agent_permissions` MUST be the only agent-tool path for per-agent tool policies and connector assignments of an existing agent; `update_agent` MUST refuse those fields.
- **FR-025**: `create_agent` MUST refuse non-deny grants for management-family tools.
- **FR-026**: Outside God Mode Omnipus MUST present a summary and one Go/Change/Cancel question before its first change tool; Cancel MUST run no change tool and MUST leave already-submitted secrets stored.
- **FR-027**: Outside God Mode, ask-list tools MUST still raise their approval after Go.

### D. Verification
- **FR-030**: Every change tool MUST return working / saved but not working (reason) / partly saved (durable parts + missing parts) / failed: nothing saved (only when no side effect is proven), with the scope of its check; recovery MUST read durable resources.
- **FR-031**: `configure_provider` MUST check the saved effective endpoint, credential and protocol via the protocol-aware listing; keyless local providers MUST be checkable.
- **FR-032**: `update_workspace` MUST read back graph validity and edge membership.
- **FR-033**: Omnipus MUST report "done" for a part only from that part's tool outcome.

### E. Target workspace
- **FR-040**: Setup verbs (`create_agent`, `create_skill`, `edit_skill`, `remove_skill`, `environment_setup`, private `request_secret`, membership changes, read-backs) MUST accept a validated explicit target and MUST NOT change the caller's session workspace.
- **FR-041**: `create_agent` with a target MUST join the target team and create the main chat there.
- **FR-042**: Project-skill operations MUST resolve the target's shelf.
- **FR-043**: An unknown target MUST fail with nothing saved.

### F. Create / team / welcome
- **FR-045**: Add agent MUST open the Omnipus chat with W1 for that workspace, leaving the active workspace unchanged.
- **FR-046**: W1 MUST offer a fitting existing agent unchanged, list uncovered gaps, and apply in WF order.
- **FR-047**: Workspace creation MUST post the setup offer into Omnipus's existing main (server-initiated); a client kickoff naming an existing session MUST stay refused.
- **FR-048**: New workspaces MUST be created with an empty team.
- **FR-049**: W18 MUST add reused agents unchanged; changes to them MUST be separate proposals listing before/after and every affected workspace.
- **FR-050**: W18's summary MUST include a text list of agents and edges.
- **FR-051**: W18 apply MUST place the lead first in team order.
- **FR-052**: The empty Team tab MUST offer "Set up a team"; a failed team load MUST NOT show it.
- **FR-053**: Workflows MUST check known dependencies before the summary and list needed installs in it; after installation Omnipus MUST NOT claim readiness unless proven in the target agent's sandbox (F:D3).

### G. Secrets
- **FR-060**: `request_secret` and `list_secrets` MUST be granted to every native agent per §6.5 (ordinary built-ins, custom constructor), denied to Judge and Plan Supervisor, unavailable to external-program workers, and wired into the executable registry at boot and reload.
- **FR-061**: The card MUST show acting agent, purpose, server recipient display and, for the private kind, the owning workspace and agent; web sessions only — channel sessions refused with "open the web app".
- **FR-062**: Values MUST travel only in the authenticated REST submit and MUST NOT appear in transcripts, frames, tool arguments, logs, audit entries, error texts or resume records. Agent-private values MUST NOT enter the gateway process environment; provider and mailbox values keep the existing boot/reload resolution and MUST NOT reach any child process.
- **FR-063**: Submit MUST store all fields durably in one atomic store write (all or none) under new reference names; duplicate submits MUST return 409 with the current state.
- **FR-064**: Each reference MUST be in one state — stored, selected or superseded. An integration reference MUST be selectable only by an approved change tool writing its recorded recipient; a private reference only as in FR-086.
- **FR-065**: The recipient descriptor MUST include the effective endpoint/account/executable/transport and field, and for the private kind `(workspace, agent, variable)`; any difference MUST refuse.
- **FR-066**: Stored secrets MUST NOT expire. Pending requests MUST be limited to 1 per session and 64 install-wide; terminal request states (value-free) MUST be kept for the session's lifetime, at most the last 32 per session.
- **FR-067**: Cancel, Stop or restart before submit MUST store nothing. After submit, Cancel or Stop MUST leave a not-yet-selected reference stored and unused; references already selected and changes already completed MUST stay in place and be reported truthfully.
- **FR-068**: The resumed turn MUST carry reference names only.
- **FR-069**: Every stored value MUST be in the sensitive-value set before any probe, connector or child activity can see it, and MUST stay there across every rebuild (FR-089); values under 4 bytes MUST be refused.
- **FR-070**: Agent configuration tools MUST accept references only and refuse plaintext; Settings forms keep plaintext entry.
- **FR-071**: A card MUST hold 1–8 fields; each value 4–16,384 bytes of UTF-8 without NUL; body ≤ 65,536 bytes.
- **FR-072**: Replacing a secret MUST store a new version in state stored; the integration or private owner MUST keep its selected version until an approved selection; the previous version then becomes superseded, listed and removable.
- **FR-073**: `list_secrets` MUST return names, kind, version, recipient display, consumers and in-use flag, never values; an ordinary agent MUST see only its own private names in the current workspace; Omnipus sees all.
- **FR-074**: The card MUST implement loading/unavailable, pending, submitting, rejected, submission unknown, submitted, cancelled, stopped, interrupted and refused, restored from the snapshot after reload.
- **FR-075**: Values MUST live only in the isolated control, cleared on terminal state, navigation or disposal; never in drafts or shared stores; never re-sent automatically.
- **FR-076**: Private secrets MUST be stored per `(workspace, agent, variable)` derived from the trusted execution context or Omnipus's validated target; names MUST match the grammar and avoid the protected set (D6); only the **selected** version MUST be injected, and only into that pair's own child executions (foreground and background `bash`, `environment_setup`, including document-runtime runs), appended after all managed environment layers; never into the gateway environment or other children; the protected set MUST include the child allowlist names and prefixes, `OMNIPUS_`, `LD_`, `DYLD_`, the setup layer and the document-runtime layer (D6); the card and `docs/security.md` MUST carry promise wording A.
- **FR-077**: `remove_secret` MUST list consumers before its approval, refuse while an enabled consumer exists until #1277 lands, and report durable deletion, live effect and boot validity separately.
- **FR-078**: An Omnipus helper's secret card MUST render in Omnipus's main chat naming the helper; other agents' helpers MUST be refused.
- **FR-079**: The system MUST offer a value-free request-state read and include pending and recent terminal requests in session snapshots; absence MUST NOT be read as success.
- **FR-083**: The card submit MUST NOT require password re-entry (explicit exception, FA:Q-R1-2 B); the card MUST NOT write arbitrary keys or delete; raw credential routes MUST keep re-authentication; security-lead MUST verify the binding before WU-4 lands.
- **FR-086**: The first submitted version of a private variable with no selected version MUST be selected on submit; a replacement MUST be selected only by `select_secret_version` (Ask), callable by the owner pair for itself or by Omnipus for a validated target pair; concurrent selections MUST resolve to exactly one selected version.
- **FR-087**: A value-free submit receipt (request id → reference names, time) MUST be written in the same atomic store publication as the values; after restart the request state MUST be derived from it (`submitted` if present, `interrupted` and nothing stored if absent); resume MUST carry references only and be re-dispatched at most once per request.
- **FR-089**: One computation MUST produce the complete sensitive-value set (configured references, OAuth values, every stored card value of every kind, version and state); boot, reload, Settings refresh, sign-in/token refresh and config cloning MUST use it; registrations MUST be serialized so an older computation never replaces a newer one.
- **FR-084**: Submit MUST pass all §6.7 trust checks before any write; any failure MUST produce zero store writes and no resume; value-free audit events MUST be written for every request transition and selection/removal.

### H. Connect tools and shared resources
- **FR-080**: `connect_channel` MUST configure, bind the instance's main agent in a workspace, enable and live-test in one call, consume references, list proven and not-tested facts, and validate the binding before saving.
- **FR-081**: QR pairing MUST render in the requesting chat, correlated by request and session id, with timeout, Stop and reconnect handling.
- **FR-082**: `connect_mailbox` MUST reuse the mailbox configuration logic, report read and send readiness separately, and send no email.
- **FR-085**: Tool writes to providers, MCP servers, channel instances and mailboxes MUST acquire the gateway config write lock before the agent-loop lock (never the reverse), carry the expected revision token from the confirmed summary, and be refused when the current token differs; Settings writes to these resources MUST keep serializing on the same lock.
- **FR-088**: Read tools MUST return a secret-free revision token per shared resource (`absent` for a missing entry); `list_providers` MUST also return endpoint, protocol and reference name.

### I. Messaging, attention, helpers
- **FR-090**: Agents MUST reach Omnipus by ordinary message without a delegation edge; read-only questions MUST be answered without change tools; replies use the request-bound reply.
- **FR-091**: Omnipus's main `needs_attention` MUST include pending questions, approvals and secret cards of its helpers; a pending secret card counts for any main; unknown attention MUST be shown as unknown.
- **FR-092**: Omnipus helper approvals MUST carry the root session and acting agent name and render in the Omnipus chat regardless of the active workspace, live and after reload; unrelated approvals keep today's scope.
- **FR-093**: Helper questions MUST be relayed to the main, which asks the user; the main chat MUST accept user messages while a helper applies.
- **FR-094**: Omnipus's self-row MUST be seeded in the default workspace at depth 1, editable and removable; removal MUST make helper launches fail visibly.
- **FR-095**: Omnipus MUST start at most one apply helper per workspace and re-confirm a plan whose meaning changed after a stale-revision or expected-before refusal.

### J. Screens
- **FR-100**: A fixed Omnipus sidebar entry MUST open its main without changing the active workspace; attach failure MUST disable sending and offer Retry.
- **FR-101**: The entry MUST show the attention marker of FR-091.
- **FR-102**: Team MUST show list first, graph second; cards per F:D20; workers separate.
- **FR-103**: Team's Add agent MUST offer "Create with Omnipus" and "Add an existing agent".
- **FR-104**: The picker MUST list team-eligible agents not already members, excluding Omnipus, Judge and Plan Supervisor, marking agents in no workspace.
- **FR-105**: Delete everywhere MUST list every workspace before confirmation and be disabled until that list loads.
- **FR-106**: Settings MUST offer "System agents" (Judge, Plan Supervisor, Omnipus) through the shared editor, linked from Omnipus's chat header; Omnipus's model MUST be editable without a working model.
- **FR-107**: Workspace entry MUST open the remembered chat, else the first conversation agent in team order excluding Omnipus, else the empty state with "Set up a team".
- **FR-108**: The Agents screen and routes, the create wizard/modal and `/admin/chat` MUST be removed without forwarding; REST create-agent MUST remain, documented as a non-UI API.
- **FR-109**: Every changed surface MUST meet the keyboard/focus/announcement contract in §6.3.
- **FR-110**: New UI MUST use the catalogued components of §6.8; a new reusable composite MUST follow the four-part publication contract and pass the design-system gate.
- **FR-111**: The default workspace's Team graph MUST show Omnipus's self-row with edit and remove controls.
- **FR-112**: Every changed surface MUST implement the states of §6.3.
- **FR-113**: The connection's Configure side panel on the Connectors page (wireframe R34/R35) MUST require a workspace and an agent before saving routing.

### K. No default agent and channel binding
- **FR-120**: The system MUST have no default-agent concept (setting, seed, `Agent.default`, update field, tool arguments, delete guard, registry override, health check, every `GetDefaultAgent` call); the old key MUST be ignored with a warning on load and reload.
- **FR-121**: An inbound message on an instance not bound to an available agent in a workspace MUST be dropped, logged at error level, audited and shown as the instance's `degraded` status with reason, before the routing ladder runs; a message the ladder leaves unrouted (former priority 7) MUST be dropped the same way; it MUST NOT reach any agent.
- **FR-122**: A WS frame starting a new session and a REST session create MUST name an agent; otherwise refused visibly.
- **FR-123**: A task, goal turn or steering item MUST run only under its canonical owner; if that owner no longer resolves it MUST be refused visibly, even when another agent participates.
- **FR-124**: Internal look-ups MUST use the owners in §6.4 (install-level state root, install-level MCP root, shared provider, iteration, shared skill loader).
- **FR-125**: Tool listing endpoints MUST use the central builtin registry and return 503 before it is wired.
- **FR-126**: At boot the system MUST warm the default workspace's browser; with no manager for it, warm nothing and log the reason.
- **FR-127**: `ChannelRouting` writes MUST require nonblank `workspace_id` and agent; the legacy unbound mode MUST be removed.
- **FR-128**: The routing GET MUST report an existing unbound configuration truthfully, never an invented destination.
- **FR-129**: Saving a valid binding MUST clear the routing `degraded` status.
- **FR-131**: A system message without an addressable origin MUST be discarded with an error log and audit entry.
- **FR-133**: Routing priority 7 (global default) MUST be replaced by the loud unrouted outcome; priorities 1–5 are reused at instance scope (US-16) and priority 6 (channel wildcard) is replaced by the connection's main agent.
- **FR-132**: Publishing a reply without a producing agent MUST still publish it without borrowing another agent's state.

### M. Channel person routing (US-16; PR-1–PR-4; not grilled)
- **FR-140**: Each channel instance MUST hold, in its workspace binding, a **main agent** (a member of that workspace), an allow list (existing `allow_from`), a person table and a conversation mode; all are edited in the connection's Configure side panel on the Connectors page.
- **FR-141**: The person table MUST be stored as `Config.Bindings` entries whose match names the **instance** (new `instance_id` match field) plus a person (peer), group (parent peer), guild, team or account; it MUST NOT introduce a second rules store.
- **FR-142**: For a bound instance the route MUST be: allow-list check → person table (priorities 1–5, instance-scoped) → main agent → loud unrouted drop; the bound identity no longer skips the bindings.
- **FR-143**: A person-rule target MUST be a chat-target member of the instance's workspace; saving otherwise MUST be refused; at runtime an unavailable target MUST cause a loud drop, never a main-agent fallback.
- **FR-144**: Bindings without an `instance_id` and `channel_policies.<ch>.routing_rules` MUST be ignored with a load/reload warning naming them (greenfield; the latter mapped user ids onto the bot-account match).
- **FR-145**: A sender not on the allow list MUST NOT start any turn. In a direct chat the system MUST reply "Sorry, you are not authorised to use this assistant." at most once per stranger per connection per 24 hours; a group on the allow list or person table admits all its members, and a stranger inside a non-admitted group is never answered publicly (logged only). Every stranger event MUST be recorded on the connection status (count, last stranger, time), in the log and in the audit log (RQ-2 A, RQ-3 A).
- **FR-146**: The per-agent "who can reach this agent" list MUST be derived from all connection tables (main-agent and person-rule entries) and shown read-only in the shared agent editor slide-over (there is no Agents page) with links to each connection's Configure side panel; it MUST NOT be stored separately.
- **FR-147**: Each connection MUST have a conversation mode: `main` (default — the target agent's main chat in that workspace) or `per_person` (one conversation per person, keyed with the existing per-channel-peer scope and `identity_links`), applied without a gateway restart.
- **FR-148**: The global `session.dm_scope` setting and its `/api/v1/security/session-scope` route MUST be retired (an existing value is ignored with a load warning); `session.identity_links` stays global (RQ-4 A).
- **FR-152**: Per-person channel conversations MUST appear in the sidebar under their agent, hidden by default and individually un-hideable, and in the agent's History (RQ-1).
- **FR-149**: `group_trigger` and per-instance channel settings MUST keep their behaviour.
- **FR-150**: `connect_channel` MUST set the main agent and MAY save offered person rules and a conversation mode in the same approved call; `configure_channel` (Ask) MUST be able to change them later.
- **FR-151**: The connection's Configure side panel on the Connectors page (wireframe R34/R35) MUST provide the routing settings (main agent selector limited to workspace members, allow list, person table with label + id + agent + steering flag, conversation mode, stranger status) with the §6.3 states and keyboard contract.

### N. Group chats as rooms (US-17; GC-1–GC-9, GQ-1, GQ-4; #1068; not grilled)
- **FR-158**: In every room the user MUST be able to mute and unmute a member agent (`/mute @Agent`, `/unmute @Agent`, or the header); a muted agent is not addressed by the user's default or `@all` there but still answers when @-named, stays a member of the room and the workspace, and still counts toward the two-agent minimum; mute changes are audited (GQ-4 A).
- **FR-160**: Each workspace MUST have exactly one standing team room whose members are the workspace's chat-target agents (workers, hidden engine agents and Omnipus excluded), updated when membership changes.
- **FR-161**: Only the user MUST be able to create squads (named) and ad-hoc rooms (two or more chosen agents) and edit their membership, from the web app UI; no agent tool or room command creates rooms or changes membership (GC-3).
- **FR-162**: A room MUST contain agents of exactly one workspace; adding a non-member MUST be refused; removing an agent from the workspace MUST remove it from that workspace's rooms.
- **FR-199**: A room MUST be a conversation in its workspace owned by the user, recording its members and its own transcript; no agent session works inside it and no per-member session exists (GC-8).
- **FR-164**: A user room message without @-names MUST be addressed to every unmuted member (@all default); with @-names (or `@all`) only to the named members (GC-2). @-names match member display names case-insensitively; an unknown @-name is shown unresolved and addresses nobody.
- **FR-165**: An agent's room message MUST be addressed only to the members it @-names; `@all` from an agent addresses every other member but not itself; without @-names it MUST reach no other agent (GQ-1 A). There MUST be no limit on agent-to-agent replies (GC-5; R-A4).
- **FR-200**: Each addressed member MUST receive the room message as an agent request appended to its **main session** in that workspace through the existing request path (`AdmitRequest`), subject to the existing receiver admission (message policy, `ask` hold, membership); the request MUST carry the room id, the sender and the room messages since that member's last room request (at most 20 entries, 8,192 bytes).
- **FR-166**: Each member's answer MUST return into the room through the existing reply path (`replyIntoConversation`), authored by the responder, and MUST be fanned out to the members it @-names per FR-165.
- **FR-201**: The room MUST show each addressed member's request state — queued, held for your approval, working, waiting for you in <Agent>'s main chat, answered, cancelled — updated live and restored after reload.
- **FR-168**: An agent's room message MUST be input only: it can never resolve an approval, answer a user question card, run a command or change configuration (#1068 §6).
- **FR-169**: Approvals, questions and secret cards raised while a member works on a room request MUST appear in that agent's main chat (standard behaviour, with the attention marker); the room MUST show "waiting for you in <Agent>'s main chat" for that member.
- **FR-170**: `/pause` in a room MUST stop the room from sending to agents and cancel its not-yet-started (queued or held) requests; messages written while paused stay in the room and are not delivered; `/resume` MUST start sending new messages again. Work already running in an agent's main chat MUST NOT be touched: it finishes, its answer appears in the room marked "after pause", and its @-mentions are not passed on (FQ-RM-1).
- **FR-171**: The room header MUST show the members and the room's running cost = the sum of the room-originated turns in members' main sessions; a turn whose inputs came from several sources is attributed to the room in proportion to its room-originated inputs.
- **FR-202**: In an agent's main chat, room traffic (requests and the agent's room answers) MUST appear as compact, collapsed entries "From group <name>: …", expandable, visible to the agent and the user (GC-9 A).
- **FR-204**: Every squad and ad-hoc room MUST have at least two member agents; creation with fewer MUST be refused; removing a member that would leave fewer MUST be refused; a workspace change that leaves fewer MUST make the room read-only until another agent joins; the team room MUST exist only while the workspace has at least two conversation agents (FQ-RM-2 B). Separate-context extra chats are out of scope.
- **FR-205**: A room MUST accept only `/pause`, `/resume`, `/mute @Agent`, `/unmute @Agent`, `/members` and `/help` (which lists only these); every other command MUST be refused with "Rooms don't steer agents — use <Agent>'s main chat" and a link to the main chat of each member (or of the @-named agent).
- **FR-206**: `/members` MUST list members with their request state and mute status; room commands MUST be usable only by the user (agent text is never a command, FR-168).
- **FR-207**: The chat header MUST show an "Add participant" control at the top-left that offers **agents only** (conversation agents of the chat's workspace; never channel people, workers, hidden engine agents or Omnipus). In an agent's main chat, choosing an agent MUST create a new ad-hoc room with the user and both agents, open it empty with a link back to the main chat, and leave the main chat unchanged and one-to-one (GC-10).
- **FR-208**: In a squad or ad-hoc room, "Add participant" MUST add the chosen agent (audited); in the team room it MUST be disabled with "Team room members are the workspace's agents — add the agent to the workspace"; it MUST be absent in Omnipus's chat.
- **FR-172**: Room creation, membership changes, mutes and deletion MUST be audited (who, which room, which agents, when).
- **FR-173**: Direct session-to-session messages MUST keep the existing peer rules, including cross-workspace main-to-main messages; rooms never span workspaces.
- **FR-174**: The sidebar MUST show a "Group chats" section per workspace under the agent rows: team room first, then squads, then ad-hoc rooms, each with the attention marker (GC-4).
- **FR-175**: The room view MUST provide a header with members, cost and per-member state, an @-picker offering `@all` and each member, and (squads and ad-hoc rooms) membership editing; squad creation MUST be a web dialog (name + members from the workspace).
- **FR-176**: Room message size and rate limits MUST reuse the existing session messaging limits (65,536 bytes per message; 60 admissions/minute per sender and target).
- **FR-203**: Room requests in the ledger MUST be marked as room-originated so the agent's answer is routed to the room, not to any other conversation, and a cancelled room request MUST NOT produce a room answer.

### O. Stopped channel group conversations (US-18; #892; not grilled)
- **FR-180**: A channel group conversation stopped by the operator MUST resume only on a message from a person individually on the connection's allow list or person table, or from the operator in the web app.
- **FR-181**: Automatic wakes (heartbeats, timers, scheduled tasks, queued wakes) MUST NOT resume a stopped channel group conversation.
- **FR-182**: While a channel group conversation is stopped, messages from people who may not resume it MUST NOT be processed; the group MUST receive one notice "The assistant is paused." once per stop; the messages MUST stay visible to the operator (GQ-2 C).
- **FR-183**: The resume rule MUST be decided by the server from the connection's tables, not by the model.

### P. Channel commands (US-19; #955, GC-6; not grilled)
- **FR-185**: Each individual person entry of a connection (allow list or person table) MUST carry a steering flag, **off by default**; group entries MUST NOT carry one (members admitted through a group never steer). The flag is edited in the connection's Configure side panel and applies without restart (GQ-3).
- **FR-186**: For a participant whose flag is on, steering commands MUST work as in the web chat and change only that conversation: `/model`, `/effort` (refused while no per-conversation effort setting exists), `/clear`, `/stop`, `/stop-redirect`, `/cancel`, `/status`, `/goal`, `/loop`, `/remember`, `/recall`, `/retrospective`.
- **FR-187**: UI-only commands MUST be refused on channels for every participant with "available in the web app or CLI": `/channels`, `/config`, `/skills`, `/tasks` (administration) and `/sessions`, `/switch-agent` (they open web views). No channel command MUST change agent or global settings; the agent-wide model-change path MUST be removed from the channel surface.
- **FR-188**: A steering command from a participant whose flag is off MUST NOT be executed or passed to the agent; the participant MUST be answered "Commands are not enabled for you in this conversation." at most once per participant per conversation per 24 hours; every attempt MUST be logged. `/help` MUST be answered for every allowed participant and list only what they may use.
- **FR-189**: In rooms the steering flag MUST NOT apply; rooms have their own command set (FR-205); agents never issue commands (FR-168).

### Q. Conversation awareness (US-20; AW-1, AW-2; not grilled)
- **FR-190**: At the start of every main-chat turn, the agent's context MUST include a list of its other conversations in the same workspace — per-person channel conversations and any other non-main conversations it owns (not rooms, whose traffic already reaches the main session per GC-8, and not helper or task sessions) — each with who, where, last activity and a one-line summary; at most 20 entries, most recent first, under 2,000 characters; no message content.
- **FR-191**: The one-line summary MUST be the first sentence (≤ 140 characters) of the conversation's latest idle recap; without a recap it MUST be the conversation title. Building the list MUST make no model call.
- **FR-192**: A new read-only tool `read_conversation` MUST return recent entries (text only, paged, at most 50 per page) of one of the caller's own conversations or groups in the same workspace; it MUST NOT send, resume, change or mark anything in that conversation.
- **FR-193**: `read_conversation` MUST be refused for conversations the caller is not part of, for other workspaces and for unknown ids.
- **FR-194**: "Important outcomes" MUST be the `worth_remembering` items of a conversation's idle recap (existing recap output, ≤ 5 per recap, ≤ 200 characters each); they MUST be written automatically to the agent's memory room for that workspace, tagged with the source conversation, deduplicated by text.
- **FR-195**: The agent's memory MUST gain a per-(agent, workspace) private room used for these notes; `recall_memory` in a workspace MUST search only that workspace's rooms plus the existing shared workspace room.
- **FR-196**: Cost: awareness MUST add no model calls (summaries and outcomes come from existing recaps); the list adds at most 2,000 characters per main-chat turn. With recaps turned off, summaries fall back to titles and no notes are written, stated in the docs.
- **FR-197**: The recap carry-forward (`last-session.md`) MUST be stored and read per (agent, workspace), so no workspace's recap reaches another workspace's sessions.
- **FR-198**: `read_conversation` MUST have explicit policy entries: global ceiling `allow`; per-agent `deny` for Judge, Plan Supervisor, workers and external-program workers; Omnipus and chat-target native agents ride the ceiling.

### L. Documentation
- **FR-130**: Every page in §14 MUST be updated in the same change as its behaviour and audited by docs-verifier.

---

## 11. Success criteria

- **SC-001**: On a fresh install a user creates an agent in a named workspace through Omnipus with exactly 1 question card and 0 approval cards (no connectors), and the agent answers a message there (live E2E).
- **SC-002**: The sentinel search over decoded model requests, transcripts, WS captures, logs, audit, tool arguments, error texts and resume records — each with a non-zero captured count — returns 0 matches, and the positive control returns ≥ 1.
- **SC-003**: 100% of ask-list tools raise an approval under Auto (D5) and 0 in God Mode; clarification and secret cards still appear in God Mode.
- **SC-004**: An unroutable channel message produces 0 agent turns, 1 error log line, 1 audit entry and a `degraded` status within 5 s; saving a valid binding clears it.
- **SC-005**: Source scan finds 0 occurrences of `GetDefaultAgent`, `SetDefaultAgentOverride`, `validateUnbound`, `persistUnbound`; the `ava`/`admin` guard passes.
- **SC-006**: With workspace B active, an Omnipus helper approval is actionable in the Omnipus chat in 10 of 10 runs, including 1 reload while pending.
- **SC-007**: `/agents`, `/agents/mia`, `/admin/chat` render not-found with no redirect.
- **SC-008**: The catalogue-OK/custom-fail fixture reports "saved but not working".
- **SC-009**: Every page in §14 passes docs-verifier with 0 FALSE claims.
- **SC-010**: `make verify-contracts` passes and tests 62–64 pass with inbound validation on and off.
- **SC-011**: Mia's private secret is visible in 100% of her Sales runs and in 0% of Jim's Sales runs and Mia's Marketing runs, including concurrent runs.
- **SC-012**: Untrusted submits (BDD-6.16) cause 0 store writes and 0 resumes.
- **SC-013**: At every crash cut of BDD-6.24 the recovered request state matches the store (0 "interrupted" requests with stored references; 0 duplicate versions from re-submission).
- **SC-014**: After each transition of BDD-6.25 the exact value of every stored card secret is masked in 100% of runs.
- **SC-016**: For the BDD-16.2/16.3 fixture, 100% of messages reach the specified agent, 0 stranger messages start a turn, and the stranger count on the connection status equals the strangers sent.
- **SC-017**: In a three-agent room, "status?" creates exactly 3 requests in members' main sessions and 3 room answers, "@Jim plan" exactly 1; after Stop, 0 pending room requests remain; the header cost equals the sum of room-originated turns.
- **SC-018**: Across 20 runs, 0 awareness lists contain another workspace's conversation, note or carry-forward; 0 model calls are made to build the list.
- **SC-019**: Every room request and room answer appears exactly once as a collapsed entry in the agent's main chat, and every addressed member's state in the room matches its request state after a reload.
- **SC-015**: Tool-versus-Settings contention on each of the four shared resources produces 0 silent overwrites in 100 race iterations.

---

## 12. Traceability matrix

| FR | Source | US | BDD | Tests |
|---|---|---|---|---|
| FR-001 | F:D8, D22, D23 | US-1 | 1.1 | 1 |
| FR-002 | OSA-1.1 | US-1 | 1.2 | 2 |
| FR-003 | FA:FQ-1 B | US-1 | 1.3, 1.4 | 19, 54 |
| FR-004 | FA:FQ-1 B | US-1, US-12 | 1.5, 12.4, 12.11 | 26, 27, 28 |
| FR-005 | F:D15 | US-1 | 1.7 | 66, 63 |
| FR-006 | OSA-1.3 | US-1 | 1.8 | 67 |
| FR-007 | OSA-1.4; §6.5 | US-1 | 1.6 | 3 |
| FR-008 | F:D22, D23 | US-1 | 1.1 | 1, guard |
| FR-010 | F:D8 | US-2 | 2.1 | 51, 57 |
| FR-011 | F:D13, D14 | US-2 | 2.1, 2.4 | 51, 58 |
| FR-012 | F:D13; OSA-11 | US-2, US-4 | 2.1, 4.1 | 52, 57 |
| FR-013 | F:D13 | US-2 | 2.2, 2.3, 2.5 | 58, 83 |
| FR-014 | F:D6 | US-3 | 3.1 | 51, 57 |
| FR-015 | F:D7 | US-3 | 3.4 | 47, 60 |
| FR-020 | F:D6; FA:FQ-2 A | US-3 | 3.3 | 4 |
| FR-021 | OSA-3 | US-3 | 3.3 | 4, 5 |
| FR-022 | FA:FQ-16 B | US-3 | 3.5 | 6 |
| FR-023 | F:D7; OSA-3 | US-3 | 3.4, 3.7, 3.8 | 47, 60 |
| FR-024 | FA:FQ-3 A | US-14 | 14.1, 14.2, 14.4 | 17, 61 |
| FR-025 | FA:FQ-3 A | US-14 | 14.3 | 18 |
| FR-026 | F:D6; FA:OQ-3 | US-3 | 3.1, 3.2 | 57, 60 |
| FR-027 | FA:FQ-6 A | US-3 | 3.6 | 57 |
| FR-030 | F:D9 | US-9 | 9.4, 9.6, 8.3 | 15, 76, 36 |
| FR-031 | OSA-4 | US-9 | 9.1, 9.2, 9.3, 9.7 | 12, 93 |
| FR-032 | OSA-4 | US-9 | 9.5 | 16 |
| FR-033 | F:D9 | US-9 | 9.4 | 57 |
| FR-040 | OSA-9.1 | US-4 | 4.4, 4.6, 4.8, 7.8 | 13, 73, 92 |
| FR-041 | F:D2 | US-4 | 4.4 | 13, 57 |
| FR-042 | OSA-9.1 | US-4 | 4.5 | 14 |
| FR-043 | OSA-9.1 | US-4 | 4.6 | 13 |
| FR-045 | F:D17 | US-4 | 4.1 | 52 |
| FR-046 | F:D2 | US-4 | 4.2, 4.3 | 57 |
| FR-047 | F:D24 | US-5 | 5.1, 5.8 | 43 |
| FR-048 | F:D22 | US-5 | 5.1 | 43 |
| FR-049 | F:D26; FA:FQ-14 A | US-5 | 5.3, 5.4 | 59 |
| FR-050 | OSA-10 | US-5 | 5.5 | 59 |
| FR-051 | OSA-9 | US-5 | 5.6 | 59 |
| FR-052 | F:D24 | US-5 | 5.2, 5.7, 5.9 | 26, 54, 59 |
| FR-053 | F:D3 | US-4 | 4.7 | 57, 83 |
| FR-060 | F:D10 | US-6 | 6.12, 6.18 | 33, 65 |
| FR-061 | F:D10 | US-6 | 6.1, 6.6 | 25, 32 |
| FR-062 | F:D10; R2-MAJ-004 | US-6, US-7 | 6.2, 7.9 | 31, 74 |
| FR-063 | FA:OQ-3 | US-6 | 6.4, 6.9, 6.10 | 7, 30 |
| FR-064 | OSA-7.3; FA:Q-R1-1 A | US-6 | 6.3, 6.19, 6.20 | 10, 84 |
| FR-065 | OSA-7.3; FA:OQ-1 | US-6 | 6.3 | 10 |
| FR-066 | FA:OQ-3 | US-6 | 6.17 | 68 |
| FR-067 | FA:OQ-3; R2-MAJ-001 | US-6 | 6.5, 3.2, 6.21 | 9, 55, 60, 84, 87 |
| FR-068 | OSA-7 | US-6 | 6.2 | 31 |
| FR-069 | OSA-7.6 | US-6 | 6.13, 6.25 | 31, 62, 89 |
| FR-070 | OSA-7.10 | US-6 | 6.8 | 11 |
| FR-071 | F:D10 S2 | US-6 | 6.9 | 7, 62 |
| FR-072 | FA:Q-R1-1 A | US-6 | 6.14, 6.20, 7.11 | 8, 70, 85 |
| FR-073 | F:D10 S4 | US-6 | 6.18, 3.2 | 65, 60 |
| FR-074 | OSA-7 | US-6 | 6.5, 6.7 | 25, 55 |
| FR-075 | OSA-7 | US-6 | 6.7 | 25, 55 |
| FR-076 | FA:FQ-18, OQ-1; R2-MAJ-004, R2-MAJ-007 | US-7 | 7.1–7.11 | 34, 35, 71, 72, 73, 74, 91 |
| FR-077 | FA:FQ-13; #1277 | US-15 | 15.1–15.6 | 48, 49, 95 |
| FR-078 | FA:FQ-8 | US-6 | 6.11, 6.12 | 33 |
| FR-079 | MAJ-003 | US-6 | 6.7 | 30, 55 |
| FR-083 | FA:Q-R1-2 B | US-6 | 6.15 | 69, 55 |
| FR-084 | MAJ-013 | US-6 | 6.16, 6.17 | 68, 62 |
| FR-080 | F:D11 | US-8 | 8.1, 8.2, 8.3, 8.7 | 36 |
| FR-081 | OSA-5 | US-8 | 8.4, 8.5, 8.8 | 37 |
| FR-082 | F:D11 | US-8 | 8.6 | 38 |
| FR-085 | MAJ-011; R2-MAJ-003 | US-11 | 11.9 | 75, 88 |
| FR-088 | R2-MAJ-003 | US-11 | 11.9 | 88 |
| FR-086 | FA:Q-R1-1 A; R2-MAJ-001 | US-6, US-7 | 6.19–6.23, 7.11 | 84, 85 |
| FR-087 | R2-MAJ-002 | US-6 | 6.24 | 86 |
| FR-089 | R2-MAJ-005 | US-6 | 6.25 | 89, 90 |
| FR-090 | F:D4, D5 | US-10 | 10.1, 10.4, 10.5 | 41 |
| FR-091 | F:D16; FA:FQ-8 | US-10 | 10.2, 10.3, 10.6 | 40, 82 |
| FR-092 | FA:FQ-8, FQ-15 | US-11 | 11.2, 11.3, 11.8 | 24, 39, 56 |
| FR-093 | F:D27 | US-11 | 11.1, 11.4 | 59 |
| FR-094 | FA:FQ-1 B | US-11 | 11.6, 11.7 | 42 |
| FR-095 | F:D27 | US-11 | 11.5, 11.10, 11.11 | 50, 94, 96 |
| FR-100 | F:D15 | US-12 | 12.1, 12.2 | 52 |
| FR-101 | F:D16 | US-12 | 10.2, 10.6 | 40, 82 |
| FR-102 | F:D20 | US-12 | 12.3 | 26, 54 |
| FR-103 | F:D20 | US-12 | 12.3 | 54 |
| FR-104 | F:D21; MAJ-012 | US-12 | 12.4 | 27, 54 |
| FR-105 | F:D20 | US-12 | 12.5 | 26, 54 |
| FR-106 | FA:FQ-9 A | US-12 | 12.6 | 29 |
| FR-107 | F:D22 | US-12 | 12.11, 5.6 | 28 |
| FR-108 | F:D18, D19, D23; FA:FQ-10 A | US-12 | 12.7, 12.8 | 53 |
| FR-109 | OSA-10; MAJ-008 | US-12 | 12.9, 12.10, 12.14 | 25, 26, 52, 82 |
| FR-110 | OSA-10; MIN-002 | US-12 | 12.3, 12.9 | 25, 26 |
| FR-111 | FA:FQ-1 B; MAJ-012 | US-12 | 12.12, 1.5 | 54 |
| FR-112 | MAJ-008 | US-12 | 12.13, 5.9 | 26, 54 |
| FR-113 | FA:OQ-2 B | US-12 | 12.15 | 81 |
| FR-120 | FA:FQ-17 | US-13 | 13.5, 13.7 | 22, 46, 53 |
| FR-121 | FA:FQ-17, OQ-2 | US-13 | 13.1, 13.2, 13.13 | 20, 21, 44, 97 |
| FR-133 | FA:FQ-17; PR-1 | US-13, US-16 | 13.13, 16.2 | 97, 98 |
| FR-122 | FA:FQ-17 | US-13 | 13.3 | 45, 63 |
| FR-123 | FA:FQ-17 | US-13 | 13.4 | 23 |
| FR-124 | FA:FQ-17 | US-13 | 13.6 | 80 |
| FR-125 | OSA-12 note | US-13 | 13.6 | 80 |
| FR-126 | FA:OQ-4 B | US-13 | 13.11 | 79 |
| FR-127 | FA:OQ-2 B | US-13 | 13.8, 13.1 | 64, 20 |
| FR-128 | FA:OQ-2 B | US-13 | 13.1 | 44 |
| FR-129 | FA:OQ-2 B | US-13 | 13.12 | 44 |
| FR-131 | FA:FQ-17 | US-13 | 13.9 | 77 |
| FR-132 | FA:FQ-17 | US-13 | 13.10 | 78 |
| FR-140 | PR-1 | US-16 | 16.1, 16.9 | 98, 105, 106 |
| FR-141 | PR-1; R4 | US-16 | 16.2, 16.4 | 98 |
| FR-142 | PR-1; FA:FQ-17 | US-16 | 16.1, 16.2, 16.11 | 98, 101 |
| FR-143 | PR-1 | US-16 | 16.10, 16.11 | 101 |
| FR-144 | R4 (decide merge or retire) | US-16 | 16.5 | 99 |
| FR-145 | PR-3; RQ-2 A, RQ-3 A | US-16 | 16.3, 16.15 | 100, 105, 108 |
| FR-146 | PR-2 | US-16 | 16.6 | 104 |
| FR-147 | PR-4 | US-16 | 16.7, 16.8, 16.12 | 102, 105 |
| FR-148 | PR-4; RQ-4 A | US-16 | 16.8, 16.16 | 102, 109 |
| FR-149 | R4 | US-16 | 16.13 | 103 |
| FR-150 | PR-1; team-lead steer | US-16 | 16.9 | 106 |
| FR-151 | R5 | US-16 | 16.1, 16.3, 16.7, 16.10 | 105 |
| FR-152 | RQ-1 | US-16 | 16.14 | 107, 105 |
| FR-158 | GQ-4 A; FQ-RM-1 | US-17 | 17.18, 17.21 | 131, 136 |
| FR-160 | GC-1; #1068 §3 | US-17 | 17.1, 17.12 | 110, 119 |
| FR-161 | GC-1, GC-3; FQ-RM-2 B | US-17 | 17.2, 17.10, 17.20, 17.23 | 111, 119, 137 |
| FR-162 | GC-1; #1068 §5 | US-17 | 17.9, 17.12 | 110, 111 |
| FR-199 | GC-8 | US-17 | 17.3, 17.15 | 113, 119 |
| FR-200 | GC-8; reuse | US-17 | 17.3, 17.14, 17.16 | 113, 128, 130 |
| FR-201 | GC-8 | US-17 | 17.11, 17.14 | 117, 128 |
| FR-202 | GC-9 A | US-17 | 17.15 | 129, 119 |
| FR-203 | GC-8 | US-17 | 17.6, 17.17 | 115 |
| FR-164 | GC-2 | US-17 | 17.3 | 112, 113, 119 |
| FR-165 | GC-2, GC-5; GQ-1 A | US-17 | 17.4, 17.5, 17.19 | 112, 114, 135 |
| FR-166 | GC-1; reuse | US-17 | 17.3, 17.4 | 113 |
| FR-168 | #1068 §6 | US-17 | 17.8 | 116 |
| FR-169 | GC-8 | US-17 | 17.11 | 117 |
| FR-170 | FQ-RM-1 | US-17 | 17.6, 17.17, 17.21 | 115, 136 |
| FR-171 | GC-5, GC-8 | US-17 | 17.5, 17.7 | 114, 119 |
| FR-204 | FQ-RM-2 B | US-17 | 17.20 | 137 |
| FR-205 | FQ-RM-1 | US-17 | 17.21, 17.22 | 136 |
| FR-206 | FQ-RM-1 | US-17 | 17.21, 17.23 | 136 |
| FR-207 | GC-10 | US-17 | 17.24, 17.25 | 138, 139 |
| FR-208 | GC-10 | US-17 | 17.25 | 139 |
| FR-172 | #1068 §6 | US-17 | 17.9, 17.12 | 111 |
| FR-173 | #1068 §5 | US-17 | 17.13 | 118 |
| FR-174 | GC-4 | US-17 | 17.1, 17.2 | 119 |
| FR-175 | GC-2, GC-3 | US-17 | 17.2, 17.3, 17.7 | 119 |
| FR-176 | #1068 §6 | US-17 | 17.3 | 113 |
| FR-180 | #892 | US-18 | 18.1 | 120 |
| FR-181 | #892 | US-18 | 18.2 | 121 |
| FR-182 | #892; GQ-2 C | US-18 | 18.1, 18.3 | 120 |
| FR-183 | #892 | US-18 | 18.1 | 120 |
| FR-185 | GQ-3 | US-19 | 19.4, 19.6 | 123, 132 |
| FR-186 | #955; GC-6; GQ-3 | US-19 | 19.1 | 122 |
| FR-187 | #955; GC-6 | US-19 | 19.2 | 123 |
| FR-188 | GQ-3 | US-19 | 19.3, 19.5 | 123, 133 |
| FR-189 | GQ-3; FQ-RM-1 | US-19 | 19.7 | 134 |
| FR-190 | AW-1, AW-2 | US-20 | 20.1, 20.6 | 124, 127 |
| FR-191 | AW-1 | US-20 | 20.1, 20.6, 20.7 | 124 |
| FR-192 | AW-1 | US-20 | 20.2 | 125, 127 |
| FR-193 | AW-2 | US-20 | 20.5 | 125 |
| FR-194 | AW-1 | US-20 | 20.3 | 126 |
| FR-195 | AW-2; security | US-20 | 20.3, 20.4 | 126 |
| FR-196 | AW-1 (cost) | US-20 | 20.7 | 124 |
| FR-197 | AW-2; security | US-20 | 20.4 | 126 |
| FR-198 | HC #6 | US-20 | 20.2, 20.5 | 125 |
| FR-130 | root CLAUDE.md DoD | all | — (docs-verifier) | docs-verifier report |

---

## 13. Reachability (Definition of Done)

| Capability | Registered / wired | UI reachable | Proof |
|---|---|---|---|
| Omnipus agent | Seeded; default-workspace member | Sidebar entry | E2E 52 |
| Workflow skills | Embedded; granted to Omnipus | Omnipus chat | Live 57–59 |
| `request_secret`, `list_secrets` | §6.5 for every native agent; executable registry at boot and reload | Secret card in any agent's web chat | Integration 65 (Mia, custom agent, Judge refused) |
| `connect_channel`, `connect_mailbox`, `set_agent_permissions`, `remove_secret` | Static names, ceiling, Omnipus inventory, Auto classification, executable registry | Omnipus chat | Integration 36–38, 48; live 61 |
| Private secrets | Per-pair storage; per-child injection | Secret card | Integration 34, 71, 74; UAT |
| Helper decisions | Root-session frames; SPA scope | Omnipus chat from another workspace | E2E 56 |
| Team list / picker / graph self-row / System agents / connection Configure side panel | SPA | Team tab, Settings, Connectors page | E2E 54; unit 29, 81 |
| No default / mandatory binding | Removal; routing | Channel status | Integration 44, 64; UAT |

"Code correct and tested" and "reachable by a user or agent" stay separate claims in every report.

**UAT evidence pack (R2-OBS-001).** Screen-level evidence — keyboard/focus/announcements in the real chat shell, the global entry after attach failure, touch and narrow reflow, reduced motion — is recorded separately from catalogue, manifest and static Storybook checks; a primitive-only pass does not count as screen acceptance.

**Person routing reachability (US-16).** Connection settings show the main agent, allow list, person table, conversation mode and stranger status (E2E test 105); the agent editor shows the derived "who can reach this agent" list (test 104); Omnipus's `connect_channel` sets the main agent and optional person rules (live test 106).

---

## 14. User-facing documentation TODOs

| Page | TODO | WU |
|---|---|---|
| `docs/tools.md` | Replace the Agents-screen path in "How to change an agent's tool access" with Team card → Edit and Omnipus's permission workflow; ask list; Omnipus takes no per-agent Ask (Deny only); God Mode for Omnipus | WU-3, WU-10 |
| `docs/security.md` | R-A4 (unlimited agent-to-agent replies, accepted 2026-10-13); group messages are input, never authority; channel commands conversation-scoped; awareness never crosses workspaces; R-A1, R-A2 (read-to-web disclosure), R-A3 (external-program workers), each "accepted by the founder 2026-10-12"; secret card guarantees and the no-password exception; provider/mailbox values are resolved into the gateway process (never children) while private values never are; private secrets per workspace + agent with wording A; stored/selected/superseded versions; masking across reloads; removal and #1277; unbound channels fail loudly | WU-4, WU-5, WU-6, WU-1 |
| `docs/agents.md` | Omnipus replaces Ava/Admin; only Omnipus creates agents (REST exception); Team as agent home; external-program workers and R-A3; no default agent | WU-2, WU-10, WU-11 |
| `docs/workspaces.md` | Empty new workspaces; setup offer; W18; reuse unchanged; Team list/graph incl. Omnipus self-row in the default workspace; entry rule | WU-10 |
| `docs/using-omnipus-ui.md` | Group chats sidebar section; hidden per-person conversations and how to un-hide them; Omnipus entry and attention (incl. unknown); helper cards in the Omnipus chat; secret card states; workspace entry; no Agents screen; System agents | WU-10, WU-4 |
| `docs/getting-started.md`, `docs/tutorial.md` | First agent via Omnipus; provider first | WU-10 |
| `docs/concepts.md` | Omnipus; workflows; no default agent; channels always bound | WU-2, WU-1 |
| `docs/skills.md` | Skills via Omnipus; target workspace shelf | WU-3 |
| `docs/connectors.md`, `docs/connectors/telegram.md`, `docs/connectors/google-chat.md` (incl. steering flags, UI-only commands, paused-group notice) | `connect_channel` binds the channel's main agent in a workspace; proven vs not tested; QR in chat; unbound or unrouted messages fail loudly; **person table, allow list, "not authorised" replies and stranger status, conversation mode per connection, group behaviour, legacy type-level rules ignored** | WU-7, WU-1, WU-14 |
| `docs/settings.md` | System agents; on the Connectors page each connection's Configure side panel requires a workspace and a main agent and holds the person table, allow list, steering flags and conversation mode; no default-agent or global conversation-scope setting; saved secrets list | WU-10, WU-1, WU-6, WU-14 |
| `docs/group-chats.md` (**new page**) | Rooms (at least two agents): team room, squads, ad-hoc rooms; "Add participant" from a chat header; room commands (/pause, /resume, /mute, /unmute, /members, /help) and why steering commands are refused; each agent answers from its own main chat, where room traffic shows as collapsed entries and where its approvals appear; queued/held states; Stop cancels pending requests; @all default and @-names; agent-to-agent replies with no limit and visible cost (R-A4); Stop; members and audit; one workspace only | WU-15 |
| `docs/memory.md` | Conversation awareness: what the agent sees, `read_conversation`, automatic notes per workspace, recaps-off behaviour | WU-17 |
| `docs/mail.md` | `connect_mailbox`; read vs send readiness | WU-7 |
| `docs/providers-and-models.md` | What the provider check proves; Omnipus's model in System agents | WU-3, WU-10 |
| `docs/reference/built-in-tools.md` | New tools, policies, Auto classification | WU-2 |
| Sweep | `docs/README.md`, `docs/troubleshooting.md`, `docs/plans.md`, `docs/goals.md`, `docs/browser.md`, `docs/using-omnipus-cli.md`, `docs/operations/observability.md`, `docs/connectors/google-chat.md` | WU-12 |

Pages are drafted by the implementing lead in the same change; WU-12 is a final sweep, not a deferral.

---

## 15. Work units, landing bundles and order

| WU | Content | Size | Owner (security-lead reviews, never implements) | Depends on |
|---|---|---|---|---|
| WU-0 | Contracts K1–K17 + regeneration (**preparation commits**, not independently landable — K11/K12 remove fields existing code reads) | Standard | backend-lead | — |
| WU-1 | No default agent; mandatory channel binding; loud failure; browser warm-up (FR-120–FR-132, FR-113 backend) | Feature | backend-lead; frontend-lead (remove default UI; Configure side panel requires workspace + agent) | WU-0 |
| WU-2 | Omnipus identity, Ava/Admin removal, membership, self-row, grant inventory, ceiling + ask-list, Omnipus per-agent Ask refusal, God Mode note, **and the permission split and management-grant refusal (FR-024, FR-025), committed together with the grants** | Feature | backend-lead; prometheus (prompt) | WU-0 |
| WU-3 | Change-tool adaptations: target workspace, outcomes, provider/workspace checks, shared-resource lock order and revision tokens (FR-085, FR-088) | Feature | backend-lead | WU-2 |
| WU-4 | Secret card core: request/submit/state read, atomic store with submit receipt, versioned references and states, descriptor, trust checks, audit, complete masking set across all rebuilds, card UI, references in config tools (**security-lead verification gate, FR-083**) | Feature | backend-lead, frontend-lead | WU-0, WU-3 |
| WU-5 | Private secrets per workspace + agent: selection tool, protected set, injection after managed layers | Feature | backend-lead | WU-4 |
| WU-6 | Secret listing UI hooks and removal | Feature | backend-lead | WU-4; **#1277** for in-use removal |
| WU-7 | `connect_channel`, `connect_mailbox`, pairing in chat | Feature | backend-lead, frontend-lead | WU-3, WU-4 |
| WU-8 | Helper decisions in the Omnipus main + attention | Feature | backend-lead, frontend-lead | WU-0, WU-2 |
| WU-9 | Workflow skills, core prompt, routing table, `AskUserQuestion` text | Feature | prometheus; backend-lead wiring | WU-2, WU-3, WU-4, WU-7 |
| WU-10 | Screens incl. state/keyboard contract, kickoff retarget, workspace entry | Feature | frontend-lead; backend-lead (kickoff) | WU-0, WU-2, WU-8 |
| WU-11 | Removals, guard scripts, retired-surfaces list | Feature (structural, cross-tree) | frontend-lead; backend-lead; prometheus (CLAUDE.md) | WU-10 |
| WU-14 | Channel person routing (US-16): instance-scoped bindings, allow-list stranger handling, main agent after person table, per-connection conversation mode, derived per-agent list (agent editor slide-over), routing in the Connectors Configure side panel, steering flags, `connect_channel`/`configure_channel` rule parameters (**not grilled — focused review per founder**) | Feature | backend-lead, frontend-lead; prometheus (W13 skill text) | WU-0, WU-1, WU-7 |
| WU-15 | Rooms: room entity and transcript, fan-out over the existing request path (mentions, room context, member states, cancellation, cost attribution), room UI (sidebar section, header, @-picker, squad dialog), collapsed main-chat entries (**not grilled**; smaller than before — no per-member sessions, no hand-off) | Feature | backend-lead, frontend-lead | WU-0, WU-8, WU-10 |
| WU-16 | Channel conversation control: stopped-group resume authority (#892), conversation-scoped commands (#955) (**not grilled**) | Feature | backend-lead | WU-14 |
| WU-17 | Conversation awareness: turn-start list, `read_conversation` + policy entries, recap outcomes to per-workspace memory room, per-workspace carry-forward (**not grilled**) | Feature | backend-lead; prometheus (prompt text) | WU-14, WU-15 |
| WU-12 | Docs sweep | Standard | implementing leads; docs-verifier | all |
| WU-13 | UAT campaign | Gate | qa-lead plan; uat-tester + uat-validator | all |

**Preparation stages and the one landing (R2-MAJ-006).** The stages below are commits on the feature branch, each checked by CI and a qa-lead CHECK; **none is landed on its own**. The feature lands **once**, complete, after the 5-reviewer gate, same-change user docs, docs-verifier, and exact-commit hands-on UAT by a tester and an independent validator (engine and WebSocket paths included). This avoids an interim state where Omnipus holds grants without the Ask boundary or users have no entry point.

| Stage | Units | Purpose |
|---|---|---|
| S1 Foundation | WU-0 + WU-1 + WU-2 | Contracts; no default agent; identity, grants **with** the permission split |
| S2 Tools and secrets | WU-3 + WU-4 + WU-5 + WU-6 + WU-7 + WU-14 | Change tools, secrets, connect tools, person routing |
| S3 Helpers and screens | WU-8 + WU-9 + WU-10 + WU-11 | Decisions in main, skills, screens, removals |
| S4 Conversations | WU-15 + WU-16 + WU-17 | Group chats, channel conversation control, awareness |
| Landing | S1–S3 + WU-12 + WU-13 | One gated, reachable, documented feature |

```
WU-0 ─┬─► WU-1 ─────────────────────────────┐            (S1)
      └─► WU-2 ─┬─► WU-3 ─► WU-4 ─┬─► WU-5  │            (S2)
                │                 ├─► WU-6 ◄── #1277
                │                 └─► WU-7 ─┬─► WU-14 (person routing)
                └─► WU-8 ───────────────────┼─► WU-9     (S3)
                                            └─► WU-10 ─► WU-11
                                  WU-10 + WU-8 ─► WU-15 ─► WU-17 (S4)
                                  WU-14 ─► WU-16 ─┘
all ─► WU-12 ─► WU-13 ─► one landing
```

UI development of WU-10 may start in parallel with WU-3/WU-4 against generated contracts; its live-workflow acceptance waits for WU-9.

---

## 16. Ambiguity warnings (resolved by stated assumption)

| # | Ambiguous | Assumption |
|---|---|---|
| 1 | Per-platform proven facts for `connect_channel` | At least one authenticated platform call where an API exists; otherwise "saved, not tested: <reason>"; backend-lead fills the platform table in WU-7 |
| 2 | Card and request bounds | §6.2 values |
| 3 | Reference version naming | Canonical name plus a version suffix; exact format fixed by backend-lead in WU-0/WU-4 |
| 4 | Pairing timeout | Existing platform pairing timeout |
| 5 | Omnipus's model on fresh install | The default model chosen in onboarding |
| 6 | GitNexus impact | Not run; leads run `impact` before edits |

---

## 17. Open questions for the founder

None open. RQ-1–RQ-4 (§0.7), GQ-1–GQ-4 (§0.8), FQ-RM-1 and FQ-RM-2 (§0.10) are answered; the design choices in §0.10 are listed for visibility.

---

## 18. Evaluation scenarios (holdout)

1. **Happy** — Fresh install: "I need an agent that answers support emails in Sales." One summary, Go, a mailbox secret card, one `connect_mailbox` approval; the agent appears only in Sales and reads the test inbox.
2. **Happy** — In Marketing, create "Events"; accept the offer. Lead + specialists; Events opens on the lead; graph and text list match.
3. **Happy** — Jim asks Omnipus for a Notion connector for Mia while the user is in Sales. Entry lights; approval in the Omnipus chat; Jim gets the answer.
4. **Error** — Drop the network during a card submit. "Submission unknown", then "Saved" after reconnect; values never requested again.
5. **Error** — Delete Mia while Telegram is bound to her; send a Telegram message. No reply; the channel shows the reason.
6. **Edge** — God Mode: "delete Researcher2". Summary message, no cards, deleted, report.
7. **Edge** — Keyboard only: connect Slack with two secrets.
8. **Edge** — Mia in Sales and Marketing each store `API_KEY` with different values; run both at once; each sees its own.

---

## 19. Assumptions

- Greenfield: no migration of Ava/Admin data, default-agent settings, unbound channel configs or older ceilings (F:D22).
- #1277 is fixed separately; in-use removal waits for it.
- External-program workers keep their own runtime; no secret card or private secrets.
- R-A1–R-A3 are documented, not mitigated beyond the ask list.
- **R-A4 (founder-accepted 2026-10-13, after a stress test):** no limit on agent-to-agent replies in rooms (each reply is a turn in the answering agent's main session); two agents can keep answering each other with no human present, each reply costing money. Mitigation is visibility only: cost per group in the header and the existing Stop (GC-5).
- Rooms need at least two agents (FQ-RM-2 B). Separate-context "extra chats" and external channel people in rooms are out of scope (later features).

## 20. Clarifications

### 2026-10-12
- Default agent → none (FA:FQ-17).
- Agent-private secrets → in scope (FA:FQ-18).
- Helper decisions → Omnipus main chat (FA:FQ-8, FQ-15).
- Omnipus per-agent Ask → refused, Deny allowed (FA:FQ-16 B).
- OQ-1 → per workspace + agent; wording A confirmed.
- OQ-2 → every channel bound; unbound mode removed.
- OQ-3 → stored on card submit; no staging, no expiry.
- OQ-4 → warm the default workspace's browser.
- Q-R1-1 → replacement stored as a new version; integration switches on approved change.
- Q-R1-2 → no password step on the card; raw routes keep it; security-lead verifies.

### 2026-10-13
- RQ-1 → sidebar, hidden by default, un-hideable; also History. RQ-2 A. RQ-3 A. RQ-4 A.
- GC-1–GC-6 → group chats (team room, squads, ad-hoc) in scope; @all default; user-only squads; no agent-to-agent limit (R-A4); channel commands conversation-scoped.
- AW-1, AW-2 → list + read-only look-up + automatic notes; same workspace only.
- GQ-1 A; GQ-2 C (paused notice once per stop); GQ-3 per-participant steering flag, off by default; GQ-4 A (team-room mute).
- FQ-RM-1 → reduced room command set (/pause, /resume, /mute, /unmute, /members, /help). FQ-RM-2 B → two-agent minimum. GC-10 → "Add participant" (agents only).
- GC-7 → self hand-off (later removed by GC-8). GC-8 → group chats are rooms; agents work in their main sessions. GC-9 A → collapsed room entries in main chats.
- OQ-5 → first "keep the rules, separate feature"; then the same day: channel person routing is **in scope** of this implementation (US-16, OSA-14), reusing the bindings engine.
