# Feature Specification: The Omnipus system agent

**Created**: 2026-10-12
**Status**: Draft
**Size**: Feature (spec-driven; 5-reviewer gate; engine-touching parts need hands-on UAT on the exact landing commit — root `CLAUDE.md`, "Definition of Done")
**Release**: v0.1.1 (founder answer R1 A, 2026-10-12, relayed by team-lead)

| Source | Link | Role |
|---|---|---|
| ADR | [ADR-20261011 — The Omnipus system agent](../architecture/ADR-20261011-omnipus-system-agent.md) at `bc2da87d1` plus its dated "Founder answers after correction (FQ-17, FQ-18)" section | Architectural authority; decisions cited **OSA-n**, code facts **C-n** |
| Founder decision record | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md` | Product authority, cited **F:Dn** |
| Founder answers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md` (incl. "After the correction round") | Cited **FA:FQ-n**; FQ-17 (no default agent) supersedes ADR OSA-12 |
| Visual draft | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html` | Flows W1–W18 and screens, cited **WF** |
| Grill of the ADR | [ADR review](../architecture/ADR-20261011-omnipus-system-agent-review.md) at `cdab144f0` | Its test-coverage table is carried into §9 as mandatory negative controls |
| Dependency | GitHub issue **#1277** — "Gateway refuses to start or reload when an enabled channel's secret is missing" (open) | Prerequisite for removing an in-use secret (FR-077). Its fix is **not** specified here |

**Reading guide.** §1–§6 describe behaviour only (no implementation detail). §7 onward (contracts, BDD, TDD plan, requirements, traceability, reachability, documentation, work units) may name code. Requirement IDs are **FR-nnn**, scenarios **BDD-u.n** (u = user story), success criteria **SC-nnn**, open questions **OQ-n**.

**Process note.** The plan-spec skill's interactive gates (Phase 1 confirmation, Phase 5.5 ambiguity review) are served in this feature flow by the two grill rounds and the founder interview after each (root `CLAUDE.md`, "Spec-Driven Workflow"). Ambiguities are listed in §16 and founder questions in §17; nothing here was guessed silently.

---

## 1. Discovery summary

| Topic | Captured requirement | Source |
|---|---|---|
| Actors | The user (single account, web app); Omnipus (system agent); its helper sessions; ordinary native agents (Mia, Jim, custom); external-program workers; outside people on chat channels; AI providers, connector (MCP) servers, chat platforms, mail servers | F:D4, D10, D11; ADR Context |
| Problem | Two overlapping helpers (Ava, Admin); a blank create form that can leave an agent in no workspace; secrets typed into chat; "done" claims without checks | BRD §1; ADR Context |
| In scope | One Omnipus agent; workflows as skills; summary + question gate with a narrow ask list; secret field for all agents including agent-private secrets; self-checks in change tools; connect-channel and connect-mailbox; helpers whose decisions surface in the Omnipus chat; screens (sidebar entry, Team list, picker, Settings → System agents); removal of the Agents screen, the create form, Ava and Admin; **no default agent anywhere** | F:D1–D27; FA:FQ-1…FQ-18 |
| Out of scope | Judge/Planner settings redesign and core security settings (F:D1); team templates (F:D25); secret files and connector sign-in (S5/S6, F:D10); group chats; the #1277 boot-immunity fix | F:D1, D10, D25; FA:FQ-13 |
| Constraints | Single Go binary, pure Go, two-layer tool policy (Hard Constraint #6), contract-first wire types (#8), greenfield (no migration, F:D22), Linux/macOS/Windows | root `CLAUDE.md` |
| Accepted risks | R-A1 foothold via allow tools; R-A2 read-to-web disclosure; R-A3 external-program worker planting — founder-accepted 2026-10-12 | ADR OSA-3 |
| Human evaluation | A user with a fresh install creates a working agent in a workspace by talking to Omnipus, never types a key into chat text, sees every approval in the Omnipus chat, and sees a loud channel error instead of a silently re-routed message | F:D2, D10, D27; FA:FQ-17 |

---

## 2. Existing codebase context

GitNexus MCP tools are not available in this session; the context below comes from Grep/Read at `bc2da87d1` and is labelled **Grep-derived**. Risk levels are the architect's inference, not graph output; implementing leads must run `impact` before editing (rule 9).

### 2.1 Symbols involved

| Symbol | Role | Context |
|---|---|---|
| `pkg/config/defaults.go::defaultToolPoliciesSysagent` | modifies | Shipped ceiling values (C1) |
| `pkg/tools/auto_approve.go` (ask-list table) | modifies | ADR-092 D9 ask-list (C3) |
| `pkg/coreagent/seed.go`, `core.go`, `prompts_adr090.go`, `role_policies_adr090.go`, `team_eligibility.go` | modifies | Roster, prompts, role policies, default-agent seed (C29) |
| `pkg/sysagent/tools/agent.go` (`AgentCreateTool`, `AgentUpdateTool`, `AgentDeleteTool`), `agent_update_adr090.go`, `agent_apply_args.go::writeDefaultSingleton`, `agent_read.go` | modifies | Target workspace, permission split, default removal |
| `pkg/sysagent/tools/provider.go`, `mcp.go`, `channel.go`, `skill.go`, `skill_authoring.go`, `workspace.go` | modifies | Handles, checks, target shelf |
| `pkg/providers/validate.go::ListModels` | calls | Protocol-aware provider check (C5) |
| `pkg/credentials/store.go::Store` | extends | Staging envelope and atomic promote-and-consume (C25) |
| `pkg/tools/ask_user_question.go`, `pkg/askuser/registry.go` | calls / text change | Gate question; park/resume seam (C11, C24) |
| `pkg/agent/registry.go::GetDefaultAgent`, `SetDefaultAgentOverride` and 16 call sites | modifies / removes | No default agent (C29) |
| `pkg/routing/route.go::RouteResolver` (Priority 7, `resolveDefaultAgentID`) | modifies | Loud channel failure (C30) |
| `pkg/gateway/rest_channels.go::setChannelRouting`, `getChannelRouting` | modifies | `default_agent_id` semantics |
| `pkg/gateway/ws_tool_approval.go`, `src/store/toolApproval.ts::isApprovalInScope` | modifies | Root-session routing of approvals (C13) |
| `pkg/gateway/websocket_chat.go` (kickoff, `resolveTargetAgent`), `rest_sessions.go::createSessionHTTP` | modifies | Kickoff amendment; no default fallback |
| `pkg/gateway/main_session.go`, `rest_workspaces.go`, `rest_workspace_delegation.go::newWorkspaceSetupTeam`, `rest_mailbox.go` | modifies / calls | Admin removal; empty new teams; mailbox reuse |
| `pkg/gateway/gateway_boot_browser.go::pickWarmBrowserManager` | modifies | Uses the default agent today |
| `pkg/gateway/rest_agents.go`, `rest_agents_update.go` | modifies | `Agent.default` computation and write |
| `src/components/screens/AgentListScreen.tsx`, `src/routes/_app/agents*.tsx`, `admin.chat.tsx`, `src/components/agents/CreateAgent*`, `wizard/*` | removes | F:D18, D19, D23 |
| `src/components/workspaces/WorkspaceTeamTab.tsx`, `team/AddAgentPicker` | modifies | Team list first, picker |
| `src/components/layout/Sidebar.tsx`, `src/components/chat/useSelectSession.ts`, `src/lib/nav/workspaceEntry.ts`, `src/hooks/useWorkspaceSetupKickoff.ts` | modifies | Omnipus entry, entry rule, kickoff |
| `pkg/skills/embedded/*` | modifies | Omnipus workflow skills replace Ava/Admin skills |

### 2.2 Impact assessment (inferred)

| Symbol modified | Risk | Direct dependents (Grep) | Notes |
|---|---|---|---|
| `GetDefaultAgent` (removal) | **HIGH** | 16 production call sites (FR-120–FR-126) | Routing, tasks, goals, steering, tool listings, browser warm-boot |
| `Agents.Defaults.DefaultAgentID` (removal) | **HIGH** | `seed.go`, `config.go`, `validate.go`, `loop_construct.go`, `loop_config.go`, `instance.go`, `registry.go`, `agent_read.go`, `agent.go`, `agent_apply_args.go`, `route.go`, `gateway.go`, `rest_agents.go`, `rest_agents_update.go`, `gateway_boot_browser.go`, `channel_ownership.go` | Config field, REST, tools, boot |
| `defaultToolPoliciesSysagent` | MEDIUM | Reconcile, seed tests, drift tests | Ceiling value changes only |
| `isApprovalInScope` | MEDIUM | Approval modal, reconcile with snapshot | Must keep all non-Omnipus behaviour |
| `Store` (credential store) | **HIGH** (security) | Boot credentials, every `*_ref` reader | Adds one operation; existing ones unchanged |
| Admin/Ava identity branches | MEDIUM | `main_session.go`, `websocket_chat.go`, `workspace_reroot.go`, `loop_env.go`, `workspace.go`, `rest_workspaces.go` | Deleted, except cross-workspace filesystem authority, retargeted to Omnipus |

### 2.3 Relevant execution flows

| Flow | Interaction |
|---|---|
| Inbound channel message → route → agent turn | No default fallback; loud drop (US-13) |
| Chat frame → session → turn → tool call → policy → approval | Ask list, root-session approval display (US-3, US-11) |
| Delegate run → helper session → report to parent inbox | Omnipus helpers (US-11) |
| Workspace create → setup kickoff | Retargeted to Omnipus main (US-5) |
| Boot → credentials → channels | #1277 dependency (US-15) |

### 2.4 Cluster placement

Spans agent engine (`pkg/agent`), system tools (`pkg/sysagent`), config/policy, credentials, gateway, routing, and the SPA. Cross-tree → feature size, security-lead review mandatory (credentials, policy, gateway auth paths).

### 2.5 Reference patterns

`docs/reference/go-implementation/` does not exist in this repository (checked). No reference patterns apply; existing in-repo mechanisms are reused instead (pending-question registry, approval queue, credential store, mailbox REST logic).

---

## 3. User stories and acceptance criteria

### US-1 — One Omnipus agent replaces Ava and Admin (P0)

The user has one technical partner. A fresh install has Omnipus and no Ava or Admin; Omnipus can do everything both did, from one conversation, across workspaces.

**Why P0**: every other story runs through Omnipus.
**Independent test**: fresh install → agent list contains `omnipus`, not `ava`/`admin`; Omnipus's main chat opens; its tool set matches the role table.

1. **Given** a fresh install, **When** the gateway starts, **Then** exactly one system agent named Omnipus exists, and no agent named Ava or Admin exists.
2. **Given** any create or rename, **When** the requested name is "Omnipus" in any letter case, **Then** it is refused with a visible reason.
3. **Given** Omnipus, **When** anyone tries to add it to a workspace other than the default one, or remove it from the default one, **Then** the change is refused.
4. **Given** the default workspace, **When** its Team list, sidebar agent rows or the add-existing picker are shown, **Then** Omnipus does not appear in them.
5. **Given** Omnipus, **When** its effective tools are listed, **Then** they match the role table (ADR OSA-1.4), and `set_config`, browser, plan, email-send, knowledge-write and `serve_web` tools are denied.

### US-2 — Workflows as skills, chosen reliably (P0)

The user asks for something technical; Omnipus picks the right workflow, asks when two fit, and changes nothing when none fits.

**Why P0**: the workflows are the product.
**Independent test**: send a button start and three free-text requests (one match, two matches, no match) and observe the loaded skill and calls.

1. **Given** a button that names a workflow, **When** it is pressed, **Then** Omnipus starts exactly that workflow for the named workspace.
2. **Given** a free-text request that fits two workflows, **When** Omnipus reads it, **Then** it asks the user which one with a question card before loading either.
3. **Given** a request that fits no workflow, **When** Omnipus answers, **Then** no change tool is called.
4. **Given** a running workflow whose skill was trimmed from the context, **When** a trim notice appears, **Then** Omnipus reloads that skill before continuing.

### US-3 — Approval: one summary question, a narrow ask list, God Mode (P0)

The user confirms a plan once; only risky changes ask again; in God Mode nothing asks.

**Why P0**: safety and approval fatigue (F:D6, D7).
**Independent test**: run W1 without connectors (one question, zero approvals), W4 delete (one question, one approval), then the same in God Mode (summary message, zero prompts).

1. **Given** any workflow outside God Mode, **When** planning is complete, **Then** a summary and one Go / Change / Cancel question appear before any change tool runs.
2. **Given** the user chose Cancel, **When** the workflow ends, **Then** nothing was written and any staged secret was discarded.
3. **Given** a call to a tool on the ask list outside God Mode, **When** Omnipus or its helper calls it, **Then** an approval card appears even with Auto-approve on.
4. **Given** God Mode is on, **When** Omnipus runs a workflow, **Then** the summary appears as a message and no question card or approval card appears.
5. **Given** an operator edits Omnipus's tool permissions, **When** they try to set a per-agent "ask", **Then** it is refused; a per-agent "deny" is accepted.

### US-4 — Create an agent in this workspace (W1) (P0)

From "Add agent" in workspace X, Omnipus interviews, plans capabilities, shows gaps, and after Go creates a working agent in X with its main chat.

**Why P0**: the founder's core use case (F:D2) and the only way to create agents (F:D18).
**Independent test**: Add agent in workspace "Sales" → Go → the new agent is a member of Sales, has a main chat, and answers a message.

1. **Given** workspace Sales is active, **When** the user presses Add agent, **Then** the Omnipus chat opens with W1 started for Sales and the active workspace stays Sales.
2. **Given** an existing agent fits the purpose, **When** Omnipus proposes it, **Then** the proposal is to add it unchanged, as one change.
3. **Given** the plan needs abilities nothing covers, **When** the summary appears, **Then** each gap is listed, never hidden.
4. **Given** Go, **When** apply finishes, **Then** the agent is a member of Sales (not the default workspace), has a main chat there, and the report lists each part as working, saved but not working, partly saved, or failed.
5. **Given** the plan includes writing a project skill, **When** it is applied, **Then** the skill lands in Sales's project shelf, not the default workspace's.

### US-5 — Whole-team setup (W18) and the welcome (W8) (P1)

Creating a workspace offers a tailored team; the user can skip it.

**Why P1**: high value, but W1 covers the minimum.
**Independent test**: create workspace "Research" → offer appears in the Omnipus chat and on the empty Team tab → Go → lead, specialists and delegation edges exist; landing opens the lead's main chat.

1. **Given** a new workspace is created, **When** creation completes, **Then** its team is empty and Omnipus offers "Set up a team for <name>?" in its chat.
2. **Given** an empty Team tab, **When** it is shown, **Then** it offers the same setup button.
3. **Given** a design that reuses an agent from another workspace, **When** the summary appears, **Then** that agent is marked "reused, also works in X" and is added unchanged.
4. **Given** a reused agent would need a change, **When** Omnipus proposes it, **Then** that change is a separate proposal showing before/after and every affected workspace.
5. **Given** the W18 summary, **When** it is shown, **Then** it includes a graph preview and a text list of agents and delegation edges.
6. **Given** apply finished, **When** the user opens the workspace, **Then** the lead agent's main chat opens.

### US-6 — Secure secret field (P0)

Any agent can ask for a secret through a protected card; the value never appears in chat, tool arguments, logs or the model's context, and it can only be used for the recipient the card showed.

**Why P0**: F:D10 safety.
**Independent test**: connect a provider via Omnipus; search transcript, logs, frames and tool arguments for the key: zero hits; the provider check reports working.

1. **Given** an agent needs a key, **When** it requests one, **Then** a card shows the asking agent, the purpose and the recipient (for example the provider endpoint), with labelled fields.
2. **Given** the user submits, **When** the turn resumes, **Then** the agent sees only reference names.
3. **Given** a handle for provider X at endpoint E, **When** a tool tries to use it for endpoint E′, **Then** the use is refused and a new request is required.
4. **Given** a handle already used once, **When** any tool uses it again, **Then** it is refused.
5. **Given** the user cancels, presses Stop, or the handle expires unused, **When** the request ends, **Then** the staged value is gone.
6. **Given** a channel session (for example Telegram), **When** an agent requests a secret, **Then** the request is refused with "open the web app".
7. **Given** the submit response is lost, **When** the card reconnects, **Then** it shows the server's state for that request without asking for the values again.

### US-7 — Agent-private secrets (P1)

An agent can keep a secret for its own commands (for example an API token a skill's script needs).

**Why P1**: FA:FQ-18 puts it in scope; narrower promise.
**Independent test**: Mia stores `WEATHER_TOKEN`; Mia's command sees it as an environment variable; Jim's command does not; the value is masked in Mia's command output.

1. **Given** an agent stores a private secret under a variable name, **When** that agent runs a sandboxed command, **Then** the variable is present in that command's environment.
2. **Given** another agent, **When** it runs a command, **Then** the variable is absent.
3. **Given** a command prints the exact value, **When** the output reaches the agent, **Then** the exact value is masked (best effort; the promise in docs says a command can still reveal it).

### US-8 — Connect a channel or a mailbox (P1)

The user connects Telegram (or another channel) to an agent in a workspace, or an agent's mailbox, in one approved step, and learns exactly what was proven.

**Why P1**: W13/W15 cannot finish without it (F:D11).
**Independent test**: connect Telegram to Mia in Sales → one approval → result lists "token authenticated", "binding saved", and what was not tested.

1. **Given** a confirmed plan, **When** connect-channel runs, **Then** it configures, binds to the named agent and workspace, enables and tests in one call, behind one approval.
2. **Given** the test cannot prove inbound delivery, **When** the result is shown, **Then** that part is listed as "not tested" and the result is not "working" for routing.
3. **Given** a channel that pairs by QR code, **When** pairing starts, **Then** the code and progress appear in the chat that requested it.
4. **Given** connect-mailbox, **When** it runs, **Then** read readiness and send readiness are reported separately and no email is sent.

### US-9 — Truthful results from change tools (P1)

**Why P1**: F:D9 — never report "done" for something that does not work.
**Independent test**: configure a provider with a working key but a dead custom endpoint → "saved but not working".

1. **Given** a provider with a custom endpoint, **When** it is configured, **Then** the check targets that endpoint, not the catalogue one.
2. **Given** a keyless local provider, **When** it is configured, **Then** the check runs without a key.
3. **Given** a change that saved some parts and failed others, **When** the result is shown, **Then** it says "partly saved" and lists both.

### US-10 — Agents message Omnipus; attention (P1)

**Why P1**: F:D4, D5, D16.
**Independent test**: Jim sends "Mia needs a Notion connector" → Omnipus's entry shows attention when its question appears; Jim receives the answer when done.

1. **Given** Jim, **When** he sends Omnipus a message, **Then** it arrives as an ordinary message without any delegation edge.
2. **Given** Omnipus needs the user, **When** a question, approval or secret card is pending in its chat, **Then** the Omnipus sidebar entry shows the attention marker.
3. **Given** a plain incoming agent message, **When** Omnipus has not asked the user anything, **Then** no attention marker appears.

### US-11 — Helpers keep Omnipus available; decisions stay in the Omnipus chat (P1)

**Why P1**: F:D27, FA:FQ-8, FQ-15.
**Independent test**: confirm a W18 plan, keep chatting with Omnipus while the helper applies; the helper's approval appears in the Omnipus chat naming the helper, while workspace B is active.

1. **Given** Go on a multi-step plan, **When** apply starts, **Then** a helper does it and the Omnipus chat accepts new messages.
2. **Given** a helper calls an ask tool, **When** the approval is pending, **Then** the card appears in the Omnipus chat, names the helper, and the Omnipus entry shows attention — whichever workspace is active.
3. **Given** a helper needs the user's answer, **When** it asks, **Then** Omnipus asks the user on its own card; Omnipus never answers for the user.
4. **Given** two plans touch the same workspace, **When** the second writes after the first, **Then** a stale write is refused and a changed plan goes back to the user.

### US-12 — Screens (P0)

**Why P0**: reachability of everything above (F:D15–D21, FA:FQ-9, FQ-10).
**Independent test**: walk the sidebar entry, Team list, picker and Settings → System agents; confirm the Agents screen and create form are gone.

1. **Given** any workspace is active, **When** the user opens the Omnipus entry, **Then** Omnipus's chat opens and the active workspace does not change.
2. **Given** the Team tab, **When** it opens, **Then** the list view is first, the graph second; each card shows avatar, name, kind, model, other workspaces and actions.
3. **Given** "Add an existing agent", **When** the picker opens, **Then** it lists every agent except Omnipus and marks agents in no workspace.
4. **Given** Settings, **When** the user opens System agents, **Then** Judge, Plan Supervisor and Omnipus can be edited there, including Omnipus's model, even when no model works.
5. **Given** an old link to the Agents screen, **When** it is opened, **Then** the app shows its not-found screen (no forwarding).
6. **Given** a keyboard-only user, **When** they use the secret card or a Team card action, **Then** every control is reachable and labelled, and focus returns sensibly.

### US-13 — No default agent: explicit agents or loud failure (P0)

**Why P0**: FA:FQ-17 — nothing is silently routed.
**Independent test**: an unbound channel receives a message → it is dropped, the channel shows an error with the reason, the log has an error; no agent sees it.

1. **Given** a channel instance with no binding and no named agent, **When** a message arrives, **Then** it is dropped, the channel's status shows an error with the reason, and the event is logged.
2. **Given** a binding whose agent no longer exists, **When** a message arrives, **Then** the same loud drop happens.
3. **Given** a chat message or session request with no agent named, **When** the server receives it, **Then** it is refused with a visible error.
4. **Given** a task, goal or session whose agent no longer exists, **When** it would run, **Then** it is refused visibly and no other agent runs it.
5. **Given** Settings and the agent editor, **When** they are shown, **Then** there is no "default agent" control anywhere.

### US-14 — Permission changes ask (P1)

**Why P1**: FA:FQ-3 A.
**Independent test**: ask Omnipus to give Jim `bash` → summary Go → an approval card for the permission change appears.

1. **Given** an existing agent, **When** Omnipus changes its tool permissions or connector assignments, **Then** that change goes through the permission tool and asks.
2. **Given** a new agent, **When** it is created, **Then** it receives no management tool.

### US-15 — Remove a secret safely (P2)

**Why P2**: depends on #1277; low frequency.
**Independent test**: remove a secret used by an enabled channel → consumers listed first; with #1277 landed, the gateway still restarts and the channel shows degraded.

1. **Given** a secret referenced by consumers, **When** removal is requested, **Then** the consumers are listed before the approval.
2. **Given** #1277 is not landed, **When** removal of an in-use secret is requested, **Then** it is refused with the consumer list.
3. **Given** removal completes, **When** the result is shown, **Then** it reports durable deletion, live effect and whether the configuration still starts, separately.

---

## 4. Behavioural contract

**Primary flows**
- When the user presses Add agent in a workspace, the system opens the Omnipus chat with the create-agent workflow started for that workspace.
- When a workflow's plan is complete, the system shows a summary and one Go / Change / Cancel question before any change.
- When the user answers Go, the system applies the plan (in a helper for multi-step work) and reports each part's outcome.
- When an agent needs a secret, the system shows a protected card and gives the agent only a reference name.
- When an agent messages Omnipus, the system delivers it as an ordinary message.

**Error flows**
- When a channel message has no route, the system drops it, shows an error on the channel and logs it.
- When a handle's recipient differs from the card's, the system refuses the use.
- When a stale revision is written, the system refuses the write; a changed plan returns to the user.
- When a check fails after saving, the system reports "saved but not working" with the reason.
- When no agent is named for a chat, session, task, goal or steering item, the system refuses visibly.

**Boundary conditions**
- When God Mode is on, the system shows no question or approval card for Omnipus or its helpers.
- When a handle is used a second time or after expiry, the system refuses it.
- When the user picks Cancel, the system writes nothing and discards staged secrets.

---

## 5. Edge cases

| Case | Expected |
|---|---|
| Two Omnipus helpers write the same agent | Second write gets the stale-revision result; plan returns to the user if its meaning changed |
| Submit response lost on the secret card | "Submission unknown" state; reconciled by request id; no values re-sent |
| Browser tab closed with a pending secret card | Request stays pending until expiry; reopening the chat shows it |
| Gateway restarts with a staged secret | Expired envelopes deleted at boot; unexpired ones remain usable until expiry; the turn resumes only with handles |
| Omnipus's self-row removed by the operator | Helper launch refused visibly; Omnipus applies in the main chat or reports the limitation |
| No working model on a fresh install | Agent creation waits; provider and Omnipus's model remain fixable in Settings |
| A `default_safe` question | Never used for the Go question (instruction + prompt-text test) |
| Name "OMNIPUS " with trailing space | Refused (trimmed, case-insensitive) |
| Unbound channel instance with an explicitly named agent | Open — OQ-2 |
| Peer agent message arrives mid-interview | Queued in Omnipus's one conversation; handled after the current step |

---

## 6. Explicit non-behaviours and safeguards

### 6.1 Qualitative prohibitions

- The system must not route any message to an agent that was not named, because FA:FQ-17 removed the default agent.
- The system must not place a secret value in the transcript, a chat frame, a tool argument, a log line, a resume record or an error message, because F:D10.
- The system must not let a handle be used for a different recipient or twice, because OSA-7.
- The system must not add a third tool-policy layer or an Omnipus-only resolver bypass, because Hard Constraint #6.
- The system must not persist a per-agent "ask" for Omnipus, because FA:FQ-16 B.
- The system must not show an Omnipus approval only in a helper's chat, because FA:FQ-8.
- The system must not reconfigure a reused agent inside W18's summary, because FA:FQ-14 A.
- The system must not send a probe email when testing a mailbox, because OSA-5.
- The system must not create an agent through any UI path other than Omnipus, because F:D18 (the REST create endpoint stays, FA:FQ-10 A).
- The system must not forward old Agents-screen links, because F:D19.
- The system must not reintroduce Ava, Admin, the Agents screen or the create form, because they are retired surfaces.
- The system must not answer a question or approval on the user's behalf, because WF §1.

### 6.2 Machine-verifiable constraints

**HTTP / wire**
- Per-agent tool-policy write with `ask` for Omnipus → HTTP 422, body error text "Omnipus takes no per-agent Ask; set Ask globally or use Deny".
- Session create without an agent → HTTP 400 "an agent must be named".
- Secret submit for an unknown or foreign request id → HTTP 404; for an already-resolved request → HTTP 409 with the current state; body size over 64 KiB → HTTP 413.
- Adding Omnipus to a non-default workspace via REST → HTTP 422.
- Creating or renaming an agent to "Omnipus" (case-insensitive, trimmed) → HTTP 409 "the name Omnipus is reserved".

**Data**
- A secret card holds 1–8 fields; a field value is 1–16,384 bytes of UTF-8.
- One pending secret card per session at a time (same rule as the question card).
- Staged-secret expiry: 30 minutes from staging (proposed; OQ-3).
- Omnipus's self-row depth: 1.

**Scope**
- The agent-private secret kind is visible only to its owning agent's sandboxed command environment.
- No production Go or TS file contains the agent ids `ava` or `admin` after delivery (guard script).

**Performance**
- The secret card renders within 1 s of the request frame on a local gateway.
- The provider check finishes or times out within 15 s per provider (existing listing budget, C5).

---

## 7. Contract-first wire definitions (Hard Constraint #8)

All changes land in `contracts/` first (backend-lead), regenerated with `scripts/gen-contracts.sh`, committed with the generated diff, then consumed. Generated outputs that change: `pkg/api/generated/openapi_types.gen.go`, `pkg/api/generated/asyncapi_types.gen.go`, `pkg/api/generated/fixtures.go`, `src/lib/api/generated/openapi-types.ts`, `src/lib/api/generated/asyncapi-types.ts`, `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts`, `src/lib/api/generated/schemas.ts`, `src/lib/api/generated/ws-schemas.ts`.

| # | Schema | Change | Spec |
|---|---|---|---|
| K1 | **new** `SecretRequestFrame.yaml` (asyncapi) | request id, session id, acting agent id, root session id (optional), workspace id (optional), purpose, recipient display, fields[] (key, label, required), expires-in | FR-061 |
| K2 | **new** `SecretRecipientDisplay.yaml` | kind enum (`provider_key`, `mcp_env`, `channel_credential`, `mailbox_password`, `agent_private`) + display lines; no secret material | FR-065 |
| K3 | **new** `SecretRequestResolvedFrame.yaml` | request id, state enum (`submitted`, `cancelled`, `expired`, `stopped`) | FR-074 |
| K4 | **new** `SessionStatePendingSecretRequest.yaml`; **changed** `SessionStateFrame.yaml`, `SessionSnapshotFrame.yaml` | pending secret requests listed with pending approvals | FR-074 |
| K5 | **new** `SecretSubmitRequest.yaml`, `SecretSubmitResponse.yaml` (openapi; path keyed by request id) | values by field key, or `cancel: true`; response state only | FR-062 |
| K6 | **new** `SecretNameEntry.yaml`; openapi list endpoint | names, kind, recipient display, consumers count — never values | FR-073 |
| K7 | **changed** `ToolApprovalRequiredFrame.yaml`, `SessionStatePendingApproval.yaml` | optional `root_session_id`, `acting_agent_name` | FR-092 |
| K8 | **changed** `MessageFrame.yaml` metadata | optional `workflow_start` {workflow enum `W1`/`W18`, `target_workspace_id`}; described as target data, not session binding | FR-012, FR-045 |
| K9 | **changed** `MessageFrame.yaml` / asyncapi `workspace_setup_kickoff` description | kickoff becomes server-initiated into Omnipus's existing main; client-sent kickoff with a session id stays refused | FR-047 |
| K10 | **changed** `WhatsAppPairingFrame.yaml` (+ subscribe frame) | optional `request_id`, `session_id` for chat correlation | FR-081 |
| K11 | **changed** `Agent.yaml` | remove `default` | FR-120 |
| K12 | **changed** `AgentUpdateRequest.yaml` | remove `default`; document 422 for Omnipus per-agent `ask` in `tools` | FR-120, FR-022 |
| K13 | **changed** `AgentToolsUpdateRequest.yaml` | document 422 for Omnipus per-agent `ask` | FR-022 |
| K14 | **changed** `ChannelRouting.yaml` + openapi `setChannelRouting` text | empty `default_agent_id` no longer means "global default"; see OQ-2 for the unbound-with-agent case | FR-127 |
| K15 | **changed** `ChannelEntry.yaml` | reuse `degraded` / `degraded_reason` for "unroutable: …" (description update; add `last_dropped_at` optional) | FR-121 |
| K16 | **changed** `SessionCreateRequest.yaml` | `agent_id` required | FR-122 |
| K17 | **removed** settings surface for `agents.defaults.default_agent_id` (config schema / blocked-path note) | key ignored if present (greenfield) | FR-120 |

No hand-written wire type may be added (lint: `scripts/check-no-handwritten-wire-types.sh`). Discriminated unions, if any, follow the inline `oneOf` rule (root `CLAUDE.md`, ADR-034).

---

## 8. BDD scenarios

### Feature: Omnipus identity (US-1)

**BDD-1.1 Fresh install seeds Omnipus only** — Traces to US-1 AS-1 — Happy Path
- Given an empty data directory
- When the gateway starts
- Then the agent list contains `omnipus` (type core, locked identity)
- And it contains neither `ava` nor `admin`

**BDD-1.2 Reserved name** — Traces to US-1 AS-2 — Error Path (Outline)
- Given an existing agent "Researcher"
- When it is created or renamed to `<name>` via `<path>`
- Then the result is `<result>`

| name | path | result |
|---|---|---|
| Omnipus | REST create | 409 reserved |
| omnipus | `update_agent` tool | refused, reserved |
| "OMNIPUS " | REST update | 409 reserved |
| Omnipus Helper | REST create | accepted (not an exact match) |

**BDD-1.3 Omnipus cannot join another workspace** — Traces to US-1 AS-3 — Error Path
- Given workspace Sales
- When `update_workspace` adds `omnipus` to Sales's team
- Then the call is refused and Sales's team is unchanged

**BDD-1.4 Omnipus cannot leave the default workspace** — Traces to US-1 AS-3 — Error Path
- Given the default workspace
- When a REST update removes `omnipus` from its team
- Then HTTP 422 is returned and membership is unchanged

**BDD-1.5 Omnipus hidden from lists** — Traces to US-1 AS-4 — Happy Path
- Given the default workspace is active
- When the Team list, sidebar rows and add-existing picker render
- Then none shows Omnipus

**BDD-1.6 Role tools** — Traces to US-1 AS-5 — Happy Path
- Given a fresh install
- When Omnipus's effective tool policies are resolved
- Then `create_agent` is allow, `delete_agent` is ask, `set_config` is deny, `browser_navigate` is deny

### Feature: Workflows and routing (US-2)

**BDD-2.1 Button-named workflow** — US-2 AS-1 — Happy Path
- Given workspace Sales
- When the SPA sends a message with workflow-start W1 targeting Sales
- Then Omnipus's turn begins with the fixed instruction "Start workflow W1 for workspace Sales" and loads the W1 skill

**BDD-2.2 Two workflows fit** — US-2 AS-2 — Alternate Path
- Given the request "Mia needs to read our Google Drive"
- When Omnipus routes it
- Then it either loads the change-agent workflow which runs connector setup as a sub-part, or asks with a question card; it never calls a change tool before the summary

**BDD-2.3 No workflow fits** — US-2 AS-3 — Error Path
- Given the request "what is the capital of France"
- When Omnipus answers
- Then no change tool is called

**BDD-2.4 Reload after trim** — US-2 AS-4 — Edge Case
- Given a W18 run whose skill message was trimmed
- When the trim notice appears
- Then Omnipus calls `Skill` for W18 again before its next change

### Feature: Approval (US-3)

**BDD-3.1 Summary question before change** — US-3 AS-1 — Happy Path
- Given W1 with no connectors outside God Mode
- When planning completes
- Then exactly one question card with Go / Change / Cancel appears before `create_agent`
- And it has no `default_safe`

**BDD-3.2 Cancel writes nothing** — US-3 AS-2 — Error Path
- Given a staged secret and a summary
- When the user chooses Cancel
- Then no change tool runs and the staged secret is gone

**BDD-3.3 Ask list prompts under Auto** (Outline) — US-3 AS-3 — Happy Path
- Given Auto-approve on and God Mode off
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
| environment_setup |

**BDD-3.4 God Mode: nothing asks** — US-3 AS-4 — Alternate Path
- Given God Mode on
- When Omnipus runs W10 (add connector) end to end
- Then no question card and no approval card appear
- And the summary is posted as a message

**BDD-3.5 Per-agent Ask refused for Omnipus** (Outline) — US-3 AS-5 — Error Path
- Given Omnipus
- When `<path>` sets `<tool>` to `<value>` for Omnipus
- Then `<result>`

| path | tool | value | result |
|---|---|---|---|
| REST tools write | bash | ask | 422 |
| REST tools write | bash | deny | accepted |
| `set_agent_permissions` | create_agent | ask | refused |
| editor | any | — | Ask option not offered |

**BDD-3.6 Both confirmations kept** — US-3 AS-3 — Happy Path
- Given W4 delete outside God Mode
- When Go is chosen
- Then a `delete_agent` approval card follows

### Feature: Create an agent (US-4)

**BDD-4.1 Add agent opens Omnipus** — US-4 AS-1 — Happy Path
- Given workspace Sales active
- When the user presses Add agent in the sidebar
- Then the Omnipus chat is foreground, W1 starts for Sales, and the active workspace is still Sales

**BDD-4.2 Existing agent fits** — US-4 AS-2 — Alternate Path
- Given Jim fits the stated purpose
- When the summary appears
- Then it proposes "Add Jim to Sales" with no other change

**BDD-4.3 Gaps listed** — US-4 AS-3 — Edge Case
- Given an ability no tool, skill or connector covers
- When the summary appears
- Then the gap is listed under "not covered"

**BDD-4.4 Agent lands in target workspace** — US-4 AS-4 — Happy Path
- Given Go on W1 for Sales from Omnipus's main (default workspace)
- When `create_agent` runs with target Sales
- Then the agent is a member of Sales, not of the default workspace, and has a main chat in Sales

**BDD-4.5 Project skill lands in target shelf** — US-4 AS-5 — Happy Path
- Given workspaces Sales and Default both have a project skill slug `crm`
- When Omnipus edits `crm` with target Sales
- Then Sales's copy changes and Default's does not

**BDD-4.6 Missing target refused** — US-4 AS-4 — Error Path
- Given Omnipus calls `create_agent` with an unknown target workspace
- When the tool validates
- Then it fails with nothing saved

### Feature: Team setup and welcome (US-5)

**BDD-5.1 New workspace empty + offer** — US-5 AS-1 — Happy Path
- Given the user creates workspace Research
- When creation completes
- Then Research's team is empty and an offer appears in Omnipus's main chat naming Research

**BDD-5.2 Empty Team tab button** — US-5 AS-2 — Happy Path
- Given an empty Team tab
- When it renders
- Then a "Set up a team" button opens the Omnipus chat with W18 for that workspace

**BDD-5.3 Reuse unchanged** — US-5 AS-3 — Happy Path
- Given Mia works in Sales and fits a role in Research
- When the W18 summary appears
- Then Mia is "reused, also works in Sales" and no change to Mia is in the plan

**BDD-5.4 Shared change is a separate proposal** — US-5 AS-4 — Alternate Path
- Given Mia would need an extra skill
- When Omnipus proposes it
- Then a separate proposal shows Mia's before/after and lists Sales and Research

**BDD-5.5 Text equivalent of graph** — US-5 AS-5 — Happy Path
- Given a W18 summary
- When rendered
- Then a list of agents and edges ("Lead → Analyst") accompanies the graph preview

**BDD-5.6 Land in lead's chat** — US-5 AS-6 — Happy Path
- Given W18 applied with lead "Atlas"
- When the user opens Research with nothing remembered
- Then Atlas's main chat opens

**BDD-5.7 Skipped offer** — US-5 AS-1 — Alternate Path
- Given the offer
- When the user declines
- Then nothing is created and the Team tab still offers setup

**BDD-5.8 Client kickoff into an existing session refused** — US-5 AS-1 — Error Path
- Given a client frame with the kickoff marker and an existing session id
- When the gateway receives it
- Then it is refused (existing protection kept)

### Feature: Secret field (US-6)

**BDD-6.1 Card content** — US-6 AS-1 — Happy Path
- Given Omnipus requests an OpenAI key for endpoint `https://api.openai.com/v1`
- When the card renders
- Then it shows "Omnipus", the purpose, the endpoint, and one labelled field

**BDD-6.2 Handles only after submit** — US-6 AS-2 — Happy Path
- Given the user submits a value
- When the turn resumes
- Then the resumed message lists the handle name only
- And transcript, frames, logs, tool arguments and resume records contain no value (byte search)

**BDD-6.3 Recipient mismatch** (Outline) — US-6 AS-3 — Error Path
- Given a handle staged for `<staged>`
- When a tool consumes it for `<used>`
- Then it is refused

| staged | used |
|---|---|
| provider openai @ api.openai.com | provider openai @ evil.example |
| MCP notion: `npx notion-mcp` env TOKEN | MCP notion: `sh -c curl…` env TOKEN |
| mailbox mia/Sales imap.a.com user x | mailbox mia/Sales imap.b.com user x |
| channel tg-1 bot_token | channel tg-2 bot_token |
| agent-private mia WEATHER_TOKEN | agent-private jim WEATHER_TOKEN |

**BDD-6.4 Single use, concurrent consumers** — US-6 AS-4 — Edge Case
- Given one staged handle
- When two tools consume it at the same time
- Then exactly one succeeds and the other is refused as consumed

**BDD-6.5 Cancel / Stop / expiry** (Outline) — US-6 AS-5 — Error Path
- Given a pending or staged request
- When `<event>`
- Then the staged value is deleted and the card shows `<state>`

| event | state |
|---|---|
| user Cancel | cancelled |
| Stop | stopped |
| 30 min pass unused | expired |
| gateway restart after expiry | expired (deleted at boot) |

**BDD-6.6 Channel session refused** — US-6 AS-6 — Error Path
- Given a Telegram-origin session
- When an agent requests a secret
- Then the tool result says to open the web app and no card is created

**BDD-6.7 Submission unknown** — US-6 AS-7 — Edge Case
- Given the submit response is lost
- When the connection returns
- Then the card shows the server state for that request id and does not request values again

**BDD-6.8 Plaintext refused by config tools** — US-6 AS-2 — Error Path
- Given `configure_provider` receives a plaintext `api_key`
- When it validates
- Then it refuses and instructs to request a secret

**BDD-6.9 Multi-field card all-or-none** — US-6 AS-1 — Edge Case
- Given a Slack card with bot and app tokens
- When the user submits only one
- Then submit is rejected and nothing is staged

**BDD-6.10 Duplicate submit idempotent** — US-6 AS-7 — Edge Case
- Given a submitted request
- When the same submit is replayed
- Then HTTP 409 with state `submitted`, and the staged value is unchanged

**BDD-6.11 Omnipus helper card in main** — US-6 AS-1 / US-11 AS-2 — Alternate Path
- Given an Omnipus helper requests a secret
- When the card renders
- Then it appears in Omnipus's main chat naming the helper

**BDD-6.12 Ordinary helper refused** — US-6 AS-1 — Error Path
- Given a Jim helper requests a secret
- When the tool runs
- Then it is refused ("ask your parent")

**BDD-6.13 Redaction registration** — US-6 AS-2 — Edge Case
- Given a staged value echoed in an upstream error by a connector
- When the error reaches the tool result
- Then the value is masked

### Feature: Agent-private secrets (US-7)

**BDD-7.1 Owner sees variable** — US-7 AS-1 — Happy Path
- Given Mia stored WEATHER_TOKEN
- When Mia runs `bash` printing the variable's length
- Then the length matches

**BDD-7.2 Other agent does not** — US-7 AS-2 — Error Path
- Given Mia's WEATHER_TOKEN
- When Jim runs the same command
- Then the variable is unset

**BDD-7.3 Masking best-effort** — US-7 AS-3 — Edge Case
- Given Mia's command prints the exact value
- When the output returns
- Then the value is masked
- And a reversed or base64 value is **not** guaranteed masked (documented limit)

**BDD-7.4 External CLI worker excluded** — US-7 AS-2 — Error Path
- Given an external-program worker
- When it requests an agent-private secret
- Then it is refused (no sandboxed command environment under Omnipus control)

### Feature: Connect channel and mailbox (US-8)

**BDD-8.1 One-call connect** — US-8 AS-1 — Happy Path
- Given a confirmed plan to connect Telegram to Mia in Sales
- When `connect_channel` runs after its approval
- Then the channel is configured, bound to Mia in Sales, enabled, and the result lists proven facts

**BDD-8.2 Not-tested facts** — US-8 AS-2 — Edge Case
- Given the platform offers no way to prove webhook delivery
- When the result is shown
- Then "inbound delivery: not tested" is listed and routing is not "working"

**BDD-8.3 Bad token** — US-8 AS-1 — Error Path
- Given a revoked token
- When `connect_channel` runs
- Then the result is "saved but not working: token rejected", or "failed: nothing saved" if it validates before saving — never "working"

**BDD-8.4 QR in requesting chat** — US-8 AS-3 — Happy Path
- Given a WhatsApp connect from Omnipus's main
- When pairing starts
- Then the QR code and progress render in Omnipus's chat, correlated by request id

**BDD-8.5 QR timeout** — US-8 AS-3 — Error Path
- Given pairing started
- When the pairing timeout passes
- Then the result is "saved but not working: pairing timed out"

**BDD-8.6 Mailbox readiness split** — US-8 AS-4 — Happy Path
- Given valid incoming and invalid outgoing credentials
- When `connect_mailbox` runs
- Then read readiness is working, send readiness is not, and no email was sent

### Feature: Truthful results (US-9)

**BDD-9.1 Custom endpoint checked** — US-9 AS-1 — Error Path
- Given a key valid at the catalogue endpoint and a dead custom endpoint
- When `configure_provider` runs
- Then the result is "saved but not working"

**BDD-9.2 Keyless local** — US-9 AS-2 — Happy Path
- Given a local provider with no key
- When `configure_provider` runs
- Then the check runs without a key and reports working

**BDD-9.3 Non-OpenAI protocol** — US-9 AS-1 — Alternate Path
- Given an Anthropic-protocol provider
- When checked
- Then the protocol-aware listing is used

**BDD-9.4 Partly saved** — US-9 AS-3 — Edge Case
- Given `create_agent` saves the agent but membership publication fails
- When the result is shown
- Then it reads "partly saved" with the saved agent id and the failed step

**BDD-9.5 Delegation read-back** — US-9 AS-3 — Error Path
- Given `update_workspace` adds an edge to a non-member
- When it runs
- Then it fails with nothing saved

### Feature: Messaging and attention (US-10)

**BDD-10.1 Jim messages Omnipus** — US-10 AS-1 — Happy Path
- Given no delegation edge from Jim to Omnipus
- When Jim sends a message to Omnipus's address
- Then it is admitted to Omnipus's main

**BDD-10.2 Attention lights on a pending card** (Outline) — US-10 AS-2 — Happy Path
- Given Omnipus's main has a pending `<card>`
- When the session list loads
- Then Omnipus's `needs_attention` is true

| card |
|---|
| question |
| approval |
| secret request |

**BDD-10.3 No attention for a plain message** — US-10 AS-3 — Edge Case
- Given Jim's message arrived and Omnipus asked nothing
- When the session list loads
- Then `needs_attention` is false

**BDD-10.4 Reply to requester** — US-10 AS-1 — Happy Path
- Given Omnipus finishes Jim's request
- When it replies
- Then Jim receives it through the request-bound reply

### Feature: Helpers (US-11)

**BDD-11.1 Main stays available** — US-11 AS-1 — Happy Path
- Given Go on W18
- When the helper is applying
- Then a new user message to Omnipus starts a turn in the main

**BDD-11.2 Helper approval in main, other workspace active** — US-11 AS-2 — Happy Path
- Given workspace B active and Omnipus chat foreground
- When an Omnipus helper calls `add_mcp_server`
- Then the approval renders in the Omnipus chat naming the helper
- And Omnipus's `needs_attention` is true

**BDD-11.3 Unrelated approvals stay scoped** — US-11 AS-2 — Alternate Path (regression guard)
- Given a Mia approval in workspace A while workspace B is active
- When approvals render
- Then it is not shown in B (existing behaviour)

**BDD-11.4 Helper question relayed** — US-11 AS-3 — Happy Path
- Given a helper calls `AskUserQuestion`
- When it is relayed
- Then Omnipus asks the user on its own card and passes the answer back

**BDD-11.5 Stale write** — US-11 AS-4 — Error Path
- Given helper A updated Jim (revision r2)
- When helper B writes Jim with revision r1
- Then B receives the stale-revision result (`CONFLICT`) and Jim keeps A's change

**BDD-11.6 Self-row removed** — US-11 AS-1 — Error Path
- Given the operator removed Omnipus's self-row
- When Omnipus delegates to itself
- Then the launch is refused visibly

**BDD-11.7 Depth 1** — US-11 AS-1 — Edge Case
- Given an Omnipus helper
- When it delegates to itself
- Then the launch is refused by depth

**BDD-11.8 Reload during pending helper approval** — US-11 AS-2 — Edge Case
- Given a pending helper approval
- When the browser reloads
- Then the approval is restored in the Omnipus chat from the snapshot

### Feature: Screens (US-12)

**BDD-12.1 Omnipus entry keeps workspace** — US-12 AS-1 — Happy Path
- Given workspace Sales active
- When the user clicks the Omnipus entry
- Then Omnipus's chat opens and the active workspace is still Sales

**BDD-12.2 Attach failure** — US-12 AS-1 — Error Path
- Given Omnipus's main cannot be attached
- When the entry is clicked
- Then sending is disabled and a Retry action is shown

**BDD-12.3 Team list first** — US-12 AS-2 — Happy Path
- Given workspace Sales with Mia and a worker
- When Team opens
- Then the list view is selected, Mia's card shows avatar, name, "conversation agent", model, other workspaces and actions, and the worker is in a separate section

**BDD-12.4 Picker** — US-12 AS-3 — Happy Path
- Given an agent "Orphan" in no workspace
- When the picker opens
- Then Orphan is marked "not in any workspace" and Omnipus is absent

**BDD-12.5 Delete everywhere impact** — US-12 AS-2 — Happy Path
- Given Mia in Sales and Research
- When Delete everywhere is chosen
- Then both workspaces are listed before confirmation

**BDD-12.6 System agents** — US-12 AS-4 — Happy Path
- Given no working provider
- When the user opens Settings → System agents → Omnipus
- Then the model can be changed and saved

**BDD-12.7 Old Agents link** (Outline) — US-12 AS-5 — Error Path
- Given the user opens `<url>`
- Then the not-found screen shows and no redirect happens

| url |
|---|
| /agents |
| /agents/mia |
| /admin/chat |

**BDD-12.8 No create form** — US-12 AS-5 — Error Path
- Given any screen
- When the user looks for a manual create action
- Then none exists (no "New agent" button; Add agent opens Omnipus)

**BDD-12.9 Keyboard secret card** — US-12 AS-6 — Edge Case
- Given a two-field Slack card
- When navigated by keyboard
- Then both fields have labels, errors are announced, Go is not pre-selected, and focus returns to the composer after resolution

**BDD-12.10 Team actions without hover** — US-12 AS-6 — Edge Case
- Given touch or keyboard focus on a card
- When the actions menu is opened
- Then Edit, Remove and Delete everywhere are reachable

**BDD-12.11 Workspace entry rule** (Outline) — US-12 AS-1 — Happy Path
- Given workspace `<ws>` with team `<team>` and nothing remembered
- When opened
- Then `<result>`

| ws | team | result |
|---|---|---|
| Sales | [Mia, Jim] | Mia's main |
| Default | [Omnipus(hidden), Mia] | Mia's main |
| Research | [] | empty state with "Set up a team" |

### Feature: No default agent (US-13)

**BDD-13.1 Unbound channel loud drop** — US-13 AS-1 — Error Path
- Given a Telegram instance with no binding and no named agent
- When a message arrives
- Then no agent turn starts, the instance shows `degraded` with reason "unroutable: no agent bound", an error is logged and an audit entry is written

**BDD-13.2 Bound agent deleted** — US-13 AS-2 — Error Path
- Given an instance bound to Mia in Sales and Mia deleted
- When a message arrives
- Then the same loud drop with reason "unroutable: bound agent mia not found"

**BDD-13.3 Chat/session without agent** (Outline) — US-13 AS-3 — Error Path
- Given `<request>` with no agent
- Then `<result>`

| request | result |
|---|---|
| WS chat frame | error frame "an agent must be named" |
| REST session create | 400 |

**BDD-13.4 Stale owner refused** (Outline) — US-13 AS-4 — Error Path
- Given a `<work>` owned by deleted agent X
- When it would run
- Then it is refused visibly and no other agent runs it

| work |
|---|
| task |
| goal turn |
| steering item for a session |

**BDD-13.5 No default control** — US-13 AS-5 — Happy Path
- Given Settings and the agent editor
- Then no "default agent" control exists and agent responses carry no `default` field

**BDD-13.6 Internal look-ups without default** — US-13 AS-4 — Edge Case (regression guard)
- Given no default agent exists
- When the gateway boots and lists skills, tools and startup info
- Then each succeeds without naming any agent

**BDD-13.7 Old config key ignored** — US-13 AS-5 — Edge Case
- Given a config containing `agents.defaults.default_agent_id: "mia"`
- When the gateway starts
- Then it starts, the key has no effect, and a warning names the ignored key

### Feature: Permission changes (US-14)

**BDD-14.1 Permission tool asks** — US-14 AS-1 — Happy Path
- Given Go on "give Jim bash"
- When Omnipus applies it
- Then `set_agent_permissions` is called and an approval card appears

**BDD-14.2 update_agent rejects permission fields** — US-14 AS-1 — Error Path
- Given `update_agent` receives tool policies
- When it validates
- Then it refuses and points to the permission tool

**BDD-14.3 New agent no management tools** — US-14 AS-2 — Error Path
- Given `create_agent` requests `add_mcp_server: allow`
- When it validates
- Then it refuses that grant

**BDD-14.4 Connector assignment asks** — US-14 AS-1 — Happy Path
- Given assigning connector Notion to Mia
- When applied
- Then it goes through the permission tool with an approval

### Feature: Secret removal (US-15)

**BDD-15.1 Consumers listed** — US-15 AS-1 — Happy Path
- Given secret `telegram_tg-1_bot_token` used by an enabled channel
- When removal is requested
- Then the consumer list is shown before the approval

**BDD-15.2 Refused before #1277** — US-15 AS-2 — Error Path
- Given #1277 not landed and an enabled consumer
- When removal is requested
- Then it is refused with the consumer list

**BDD-15.3 Separate result facts** — US-15 AS-3 — Happy Path
- Given #1277 landed
- When removal completes
- Then the result states durable deletion, live effect and "configuration still starts: yes/no"

**BDD-15.4 Unused secret removed** — US-15 AS-1 — Alternate Path
- Given a secret with no consumers
- When removed after approval
- Then it is deleted

---

## 9. Test-driven development plan

### 9.1 Test hierarchy

| Level | Scope | Where |
|---|---|---|
| Unit | Go functions in `pkg/…`; React components and stores | `pkg/**/…_test.go`; `src/**/*.test.tsx` |
| Integration | Gateway + agent loop + stores with a scripted provider; SPA with mocked transport | `pkg/gateway/…_test.go`, `pkg/agent/…_test.go`; `src/test/integration/*` |
| E2E | Real gateway + SPA in Playwright; live-model scenarios in the `llm-*` shards | `tests/e2e/omnipus-*.spec.ts`, added to `tests/e2e/shards.json` |

CI is the authority; locally at most one narrow tagged `go test` or one `npx vitest run <file>` at a time; `npm run typecheck` is the only TypeScript gate.

### 9.2 Test implementation order

| # | Test | Level | BDD |
|---|---|---|---|
| 1 | `pkg/coreagent::TestSeed_FreshInstall_SeedsOmnipusNotAvaAdmin` | Unit | 1.1 |
| 2 | `pkg/coreagent::TestReservedName_Omnipus` (table) | Unit | 1.2 |
| 3 | `pkg/coreagent::TestOmnipusRolePolicies_MatchRoleTable` | Unit | 1.6 |
| 4 | `pkg/config::TestDefaultCeiling_OmnipusAskList` | Unit | 3.3 |
| 5 | `pkg/tools::TestAutoApproveAskList_IncludesNewTools` (golden) | Unit | 3.3 |
| 6 | `pkg/config::TestValidateOmnipusPolicyMap_RefusesAsk` | Unit | 3.5 |
| 7 | `pkg/credentials::TestStore_StageEnvelope_AllOrNone` | Unit | 6.9 |
| 8 | `pkg/credentials::TestStore_PromoteAndConsume_SingleUseConcurrent` (race) | Unit | 6.4 |
| 9 | `pkg/credentials::TestStore_ExpirySweep_AccessAndBoot` | Unit | 6.5 |
| 10 | `pkg/secrets::TestRecipientDescriptor_MismatchRefused` (table, dataset D1) | Unit | 6.3 |
| 11 | `pkg/sysagent/tools::TestConfigureProvider_RefusesPlaintext` | Unit | 6.8 |
| 12 | `pkg/sysagent/tools::TestConfigureProvider_ChecksEffectiveEndpoint` | Unit | 9.1, 9.2, 9.3 |
| 13 | `pkg/sysagent/tools::TestCreateAgent_TargetWorkspace` | Unit | 4.4, 4.6 |
| 14 | `pkg/sysagent/tools::TestSkillEdit_TargetShelf` | Unit | 4.5 |
| 15 | `pkg/sysagent/tools::TestCreateAgent_PartlySavedOutcome` | Unit | 9.4 |
| 16 | `pkg/sysagent/tools::TestUpdateWorkspace_EdgeToNonMemberRefused` | Unit | 9.5 |
| 17 | `pkg/sysagent/tools::TestUpdateAgent_RejectsPermissionFields` | Unit | 14.2 |
| 18 | `pkg/sysagent/tools::TestCreateAgent_RefusesManagementGrant` | Unit | 14.3 |
| 19 | `pkg/sysagent/tools::TestUpdateWorkspace_OmnipusMembershipRules` | Unit | 1.3 |
| 20 | `pkg/routing::TestRoute_NoDefault_UnboundIsUnroutable` | Unit | 13.1 |
| 21 | `pkg/routing::TestRoute_BoundAgentMissing_Unroutable` | Unit | 13.2 |
| 22 | `pkg/agent::TestRegistry_HasNoDefaultAgent` (symbol-absence + behaviour) | Unit | 13.6 |
| 23 | `pkg/agent::TestStaleOwner_TaskGoalSteeringRefused` (table) | Unit | 13.4 |
| 24 | `src/store/toolApproval.test.ts::isApprovalInScope Omnipus root` | Unit | 11.2, 11.3 |
| 25 | `src/components/chat/SecretRequestCard.test.tsx` (states, a11y) | Unit | 6.1, 6.7, 12.9 |
| 26 | `src/components/workspaces/WorkspaceTeamTab.test.tsx` (list first, cards, worker section) | Unit | 12.3, 12.5, 12.10 |
| 27 | `src/components/workspaces/team/AddAgentPicker.test.tsx` | Unit | 12.4 |
| 28 | `src/lib/nav/workspaceEntry.test.ts` (dataset D4) | Unit | 12.11 |
| 29 | `src/components/settings/SystemAgentsSection.test.tsx` | Unit | 12.6 |
| 30 | `pkg/gateway::TestSecretSubmit_StatesAndIdempotency` | Integration | 6.2, 6.10 |
| 31 | `pkg/gateway::TestSecretValue_NeverInTranscriptFramesLogs` (byte search) | Integration | 6.2, 6.13 |
| 32 | `pkg/gateway::TestSecretRequest_ChannelSessionRefused` | Integration | 6.6 |
| 33 | `pkg/gateway::TestSecretRequest_OmnipusHelperCardInMain_OrdinaryHelperRefused` | Integration | 6.11, 6.12 |
| 34 | `pkg/agent::TestAgentPrivateSecret_InjectedOnlyForOwner` | Integration | 7.1, 7.2, 7.4 |
| 35 | `pkg/agent::TestAgentPrivateSecret_OutputMasked` | Integration | 7.3 |
| 36 | `pkg/gateway::TestConnectChannel_ProvenAndNotTestedFacts` (fake platform) | Integration | 8.1, 8.2, 8.3 |
| 37 | `pkg/gateway::TestConnectChannel_PairingCorrelated` | Integration | 8.4, 8.5 |
| 38 | `pkg/gateway::TestConnectMailbox_ReadSendSplit_NoSend` (fake IMAP/SMTP) | Integration | 8.6 |
| 39 | `pkg/gateway::TestApprovalFrame_RootSessionForOmnipusHelper` | Integration | 11.2, 11.8 |
| 40 | `pkg/gateway::TestNeedsAttention_OmnipusIncludesHelperDecisions` | Integration | 10.2, 10.3 |
| 41 | `pkg/gateway::TestPeerMessage_ToOmnipus_NoEdgeNeeded` | Integration | 10.1, 10.4 |
| 42 | `pkg/agent::TestOmnipusSelfDelegation_Depth1_RowRemoval` | Integration | 11.6, 11.7 |
| 43 | `pkg/gateway::TestWorkspaceCreate_EmptyTeam_KickoffToOmnipusMain` | Integration | 5.1, 5.8 |
| 44 | `pkg/gateway::TestInboundUnroutable_ChannelDegradedAndAudited` | Integration | 13.1, 13.2 |
| 45 | `pkg/gateway::TestChatFrameAndSessionCreate_NoAgentRefused` | Integration | 13.3 |
| 46 | `pkg/gateway::TestAgentResponses_NoDefaultField_OldKeyIgnored` | Integration | 13.5, 13.7 |
| 47 | `pkg/gateway::TestGodMode_OmnipusNoPrompts` | Integration | 3.4 |
| 48 | `pkg/gateway::TestSecretRemoval_ConsumersAndPre1277Refusal` | Integration | 15.1, 15.2, 15.4 |
| 49 | `pkg/gateway::TestSecretRemoval_ResultFacts` (requires #1277) | Integration | 15.3 |
| 50 | `pkg/agent::TestRevisionConflict_TwoHelpers` | Integration | 11.5 |
| 51 | `pkg/coreagent::TestOmnipusPrompt_GateRulesAndRoutingTable` (prompt text) | Unit | 2.1, 3.1 |
| 52 | `tests/e2e/omnipus-entry.spec.ts` | E2E | 12.1, 12.2, 4.1 |
| 53 | `tests/e2e/omnipus-retired-surfaces.spec.ts` | E2E | 12.7, 12.8, 13.5 |
| 54 | `tests/e2e/omnipus-team.spec.ts` | E2E | 12.3–12.5, 12.10, 5.2 |
| 55 | `tests/e2e/omnipus-secret-card.spec.ts` | E2E | 6.1, 6.5, 6.7, 12.9 |
| 56 | `tests/e2e/omnipus-helper-approvals.spec.ts` | E2E | 11.2, 11.8 |
| 57 | `tests/e2e/llm-omnipus-w1.spec.ts` (live model) | E2E | 2.1, 3.1, 4.2, 4.3, 4.4, 3.6 |
| 58 | `tests/e2e/llm-omnipus-routing.spec.ts` (live model) | E2E | 2.2, 2.3, 2.4 |
| 59 | `tests/e2e/llm-omnipus-w18.spec.ts` (live model) | E2E | 5.3–5.7, 11.1, 11.4 |
| 60 | `tests/e2e/llm-omnipus-godmode.spec.ts` (live model) | E2E | 3.4, 3.2 |
| 61 | `tests/e2e/llm-omnipus-permissions.spec.ts` (live model) | E2E | 14.1, 14.4 |

Every test is shown red before green (CI on a tests-only commit or one narrow local run). Live-model specs use the UAT provider/model `openrouter` + `deepseek/deepseek-v4.1-flash` (root `CLAUDE.md`).

### 9.3 Test datasets

**D1 — Recipient descriptor comparison** (test 10)

| # | Staged | Used | Boundary | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | openai @ https://api.openai.com/v1 | same | happy | accepted | BDD-6.3 |
| 2 | openai @ https://api.openai.com/v1 | openai @ https://api.openai.com/v1/ | trailing slash | accepted (normalised) | BDD-6.3 |
| 3 | openai @ https://api.openai.com/v1 | openai @ http://api.openai.com/v1 | scheme change | refused | BDD-6.3 |
| 4 | openai @ catalogue | openai @ https://evil.example | host change | refused | BDD-6.3 |
| 5 | MCP stdio `npx a` args [x] | stdio `npx a` args [x, y] | args change | refused | BDD-6.3 |
| 6 | MCP http https://m.example | sse https://m.example | transport change | refused | BDD-6.3 |
| 7 | mailbox imap.a.com/user x | imap.a.com/user X | case of user | refused (exact) | BDD-6.3 |
| 8 | channel tg-1 bot_token | tg-1 app_token | field change | refused | BDD-6.3 |
| 9 | agent-private mia VAR | mia VAR (2nd use) | reuse | refused (consumed) | BDD-6.4 |
| 10 | any | any, after 30 min | expiry | refused (expired) | BDD-6.5 |
| 11 | unicode purpose "Clé API 🔑" | — | unicode display | rendered escaped, accepted | BDD-6.1 |

**D2 — Secret field values** (tests 7, 30)

| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | "" | empty | 400, field required | BDD-6.9 |
| 2 | 1 byte | min | accepted | BDD-6.2 |
| 3 | 16,384 bytes | max | accepted | BDD-6.2 |
| 4 | 16,385 bytes | max+1 | 400 | BDD-6.9 |
| 5 | 9 fields | count max+1 | 400 | BDD-6.9 |
| 6 | 8 fields | count max | accepted | BDD-6.9 |
| 7 | body 64 KiB + 1 | body max+1 | 413 | BDD-6.9 |
| 8 | value with NUL byte | special | accepted as bytes, never logged | BDD-6.13 |
| 9 | unknown request id | invalid | 404 | BDD-6.10 |
| 10 | resolved request id | repeat | 409 + state | BDD-6.10 |

**D3 — Inbound routing without default** (tests 20, 21, 44)

| # | Instance state | Expected | Traces to |
|---|---|---|---|
| 1 | bound to mia@Sales, mia exists | routed to mia's main in Sales | BDD-13.1 (control) |
| 2 | unbound, no named agent | dropped, degraded "unroutable: no agent bound" | BDD-13.1 |
| 3 | bound to mia@Sales, mia deleted | dropped, degraded "…bound agent mia not found" | BDD-13.2 |
| 4 | bound to a worker | dropped, degraded "…not a chat target" | BDD-13.2 |
| 5 | bound to entity that failed to load | dropped (existing fail-closed), degraded | BDD-13.2 |
| 6 | unbound, explicit agent (legacy wildcard) | per OQ-2 | BDD-13.1 |

**D4 — Workspace entry** (test 28) — rows as in BDD-12.11 plus: remembered valid chat → that chat; remembered deleted chat → first conversation agent; member list load failure → retryable error, no fallback.

**D5 — Ask-list / God Mode / Auto matrix** (tests 4, 5, 47)

| Tool | Ask mode | Auto on | God Mode | Traces to |
|---|---|---|---|---|
| create_agent | runs | runs | runs | BDD-3.1 |
| delete_agent | asks | asks | runs | BDD-3.3, 3.4 |
| add_mcp_server | asks | asks | runs | BDD-3.3, 3.4 |
| configure_provider | asks | asks | runs | BDD-3.3 |
| connect_channel | asks | asks | runs | BDD-3.3 |
| set_agent_permissions | asks | asks | runs | BDD-14.1 |
| environment_setup | asks | asks | runs (no per-agent ask for Omnipus) | BDD-3.4 |
| bash | asks | ADR-092 rules | runs | BDD-3.4 |

### 9.4 Regression requirements

| Existing behaviour | Existing test (family) | New regression test | Notes |
|---|---|---|---|
| Two-layer merge, God Mode floor | `pkg/tools` compositor tests | No | Must stay unchanged |
| Reconcile never overwrites operator values | `TestReconcile*` | No | Ceiling value changes only |
| Approval queue, expiry, reconnect stub, resolution | `pkg/gateway` approval tests; `src/store/toolApproval` tests | Yes — BDD-11.3 | Non-Omnipus scope unchanged |
| AskUserQuestion park/resume and delegated relay | `askuser_frames_contract_test.go`, registry tests | No | Reused |
| Kickoff mint-only protection | websocket kickoff tests | Yes — BDD-5.8 | Client path still refused |
| ADR-029 bound routing | `channel-routing.spec.ts`, routing tests | Yes — D3 row 1 | Control row |
| Revision preconditions | sysagent revision tests | No | Reused |
| Project-shelf authority | `skill_project_shelf_test.go` | Yes — test 14 | Target shelf |
| Shared editor field protections | ADR-090 config tests | No | Unchanged |
| `ValidateSubmittedToolPolicyMap` completeness | policy write tests | No | New Omnipus rule is additive |

Tests asserting Ava/Admin/default-agent behaviour are deleted with their features, not weakened; each deletion is listed in the work unit's evidence (qa-lead CHECK).

### 9.5 Mandatory negative controls carried from the ADR grill

Private-file read → public URL (documented accepted risk R-A2: test only that it is documented, not blocked); external-worker path creation (R-A3, same); forged peer "Go" text never resolves a card; plan A applying while plan B's interview runs; concurrent handle consumers; same-id changed recipient; crash between promotion and config write → "partly saved"; catalogue-OK/custom-endpoint-fail; valid token with wrong binding; one mail transport available; two workspaces with the same project skill slug; shared agent across two helpers; real shell/sidebar mounting for frontend tests (no isolated handler mocks for reachability claims).

---

## 10. Functional requirements

### A. Identity
- **FR-001**: The system MUST seed exactly one system agent `omnipus` (core, locked identity, chat target) on a fresh install and MUST NOT seed `ava` or `admin`.
- **FR-002**: The system MUST refuse creating or renaming any agent to "Omnipus" (trimmed, case-insensitive) on every path (REST, tools).
- **FR-003**: Omnipus MUST be a member of the default workspace only; adding it elsewhere or removing it from the default workspace MUST be refused (REST 422, tool error).
- **FR-004**: Omnipus MUST be excluded from the Team list, sidebar agent rows, workspace-entry selection and the add-existing picker.
- **FR-005**: Omnipus MUST have exactly one main chat; no path creates a second Omnipus chat.
- **FR-006**: Omnipus MUST hold cross-workspace filesystem authority for setup work; no other agent gains it.
- **FR-007**: Omnipus's shipped tool defaults MUST match ADR OSA-1.4; not-granted tools MUST be explicit per-agent `deny`.
- **FR-008**: All Ava and Admin seeds, prompts, skills, identity branches and UI MUST be removed; a guard script MUST fail CI if the ids `ava` or `admin` reappear as agent ids in production code.

### B. Workflows and routing
- **FR-010**: The system MUST ship Omnipus workflow skills (W1, W3–W7, W8/W18, W10–W17, capability planning, dependency checks) granted to Omnipus only, and MUST remove the Ava/Admin skills (`agent-authoring`, `skill-mapping`, `tool-mapping`, `delegation-graph`, `workspace-team`, `mcp-install`, `provider-setup`, `channel-setup`, `doctor`, Ava/Admin `interview` checklists) from the embedded set.
- **FR-011**: Omnipus's core prompt MUST contain the routing table and standing rules (ADR OSA-2.1); each workflow skill MUST carry one "use when" line.
- **FR-012**: A workflow-start message MUST make the server render the fixed instruction naming the workflow and target workspace.
- **FR-013**: For an ambiguous request Omnipus MUST ask with a question card; for no match it MUST call no change tool.
- **FR-014**: `AskUserQuestion`'s description MUST permit one plan-confirmation use (Go/Change/Cancel); Omnipus's prompt MUST forbid `default_safe` on the Go question.
- **FR-015**: When God Mode is active, Omnipus's per-turn context MUST include a one-line mode note.

### C. Approval and permissions
- **FR-020**: The shipped ceiling MUST set `delete_agent`, `remove_skill`, `add_mcp_server`, `configure_provider`, `enable_channel`, `configure_channel` to `ask` (others unchanged).
- **FR-021**: New tools `connect_channel`, `connect_mailbox`, `set_agent_permissions`, `remove_secret` MUST ship `ask` in the ceiling and be on the Auto ask-list; `request_secret` and `list_secrets` MUST ship `allow`.
- **FR-022**: Every per-agent policy write path MUST refuse `ask` for Omnipus and accept `deny`; the editor MUST not offer Ask for Omnipus.
- **FR-023**: In God Mode, no tool call by Omnipus or its helpers may raise an approval.
- **FR-024**: `set_agent_permissions` MUST be the only agent-tool path that changes per-agent tool policies and connector assignments of an existing agent; `update_agent` MUST refuse those fields.
- **FR-025**: `create_agent` MUST refuse non-`deny` grants for management-family tools.
- **FR-026**: Omnipus MUST present a summary and one Go/Change/Cancel question before its first change tool in every workflow outside God Mode; Cancel MUST leave no writes.
- **FR-027**: Outside God Mode, ask-list tools MUST still raise their approval after Go (both confirmations).

### D. Verification
- **FR-030**: Every change tool MUST return one of: working, saved but not working (reason), partly saved (saved parts + unsaved parts), failed: nothing saved (only when no side effect is proven), with the scope of its check.
- **FR-031**: `configure_provider` MUST check the saved effective endpoint, credential and protocol using the protocol-aware listing; keyless local providers MUST be checkable.
- **FR-032**: `update_workspace` MUST read back graph validity and that new edge endpoints are members.
- **FR-033**: Omnipus MUST report "done" for a part only from that part's tool outcome.

### E. Target workspace
- **FR-040**: Setup verbs (`create_agent`, `create_skill`, `edit_skill`, `remove_skill`, `environment_setup`, membership changes, read-backs) MUST accept a validated explicit target workspace and MUST NOT change the caller's session workspace.
- **FR-041**: `create_agent` with a target MUST join the target team and create the main chat there.
- **FR-042**: Project-skill operations MUST resolve the target workspace's shelf.
- **FR-043**: An unknown target MUST fail with nothing saved.

### F. Create / team / welcome
- **FR-045**: Add agent (sidebar, Team) MUST open the Omnipus chat with W1 for that workspace, leaving the active workspace unchanged.
- **FR-046**: W1 MUST offer an existing fitting agent as "add unchanged", list uncovered gaps, and apply in the WF order.
- **FR-047**: Workspace creation MUST post the setup offer into Omnipus's existing main (server-initiated); a client kickoff naming an existing session MUST stay refused.
- **FR-048**: New workspaces MUST be created with an empty team.
- **FR-049**: W18 MUST add reused agents unchanged; any change to a reused agent MUST be a separate proposal listing before/after and every affected workspace.
- **FR-050**: W18's summary MUST include a text list of agents and edges besides the graph preview.
- **FR-051**: W18 apply MUST place the lead first in the team order.
- **FR-052**: The empty Team tab MUST offer "Set up a team".

### G. Secrets
- **FR-060**: `request_secret` MUST be available to every native agent (not external-program workers).
- **FR-061**: The card MUST show acting agent, purpose and the server's recipient display; web sessions only — channel sessions MUST be refused with "open the web app".
- **FR-062**: Values MUST travel only in the REST submit; they MUST NOT appear in transcripts, frames, tool arguments, logs, error texts or resume records.
- **FR-063**: Submit MUST stage all fields in one encrypted write (all or none); duplicate submits MUST be idempotent (409 + state).
- **FR-064**: Consumption MUST write the canonical reference and mark the envelope consumed in one atomic store operation; a second consumption MUST be refused.
- **FR-065**: Consumption MUST compare the full recipient descriptor (dataset D1); any difference MUST refuse.
- **FR-066**: Staged envelopes MUST expire (OQ-3; proposed 30 min) and be deleted on access and at boot.
- **FR-067**: Cancel and Stop MUST discard pending/staged values.
- **FR-068**: The resumed turn MUST carry handle names only.
- **FR-069**: Staged and promoted values MUST be added to the sensitive-value set before any probe or connector activity, without replacing other entries.
- **FR-070**: Agent configuration tools MUST accept handles only and refuse plaintext secrets; Settings forms keep plaintext entry.
- **FR-071**: A card MUST support 1–8 fields; each value 1–16,384 bytes; one pending card per session.
- **FR-072**: Replacing a secret MUST use a new handle consumed by the writing tool.
- **FR-073**: `list_secrets` MUST return names, kinds, recipient displays and consumer counts, never values.
- **FR-074**: The card MUST implement the states loading/unavailable, pending, submitting, rejected, submission unknown, submitted, cancelled, expired, stopped, restored from the snapshot after reload.
- **FR-075**: Values MUST live only in the isolated control and be cleared on terminal state, navigation or disposal; never in drafts or shared stores.
- **FR-076**: Agent-private secrets MUST be stored bound to (agent, variable name), injected only into that agent's own sandboxed `bash`/`environment_setup` environment, masked in output best-effort, and documented with the narrower promise (option A — pending founder confirmation, OQ-1).
- **FR-077**: `remove_secret` MUST list consumers before its approval, refuse while an enabled consumer exists until #1277 lands, and report durable deletion, live effect and boot validity separately.
- **FR-078**: An Omnipus helper's secret card MUST render in Omnipus's main chat naming the helper; other agents' helpers MUST be refused.

### H. Connect tools
- **FR-080**: `connect_channel` MUST configure, bind (agent + workspace), enable and live-test in one call, consume handles, and list proven and not-tested facts; stored or authenticated credentials alone MUST NOT yield "working" for routing.
- **FR-081**: QR pairing MUST render in the requesting chat, correlated by request and session id, with timeout, Cancel and Stop handling.
- **FR-082**: `connect_mailbox` MUST reuse the mailbox configuration logic, report read and send readiness separately, and send no email.

### I. Messaging, attention, helpers
- **FR-090**: Agents MUST reach Omnipus by ordinary message to its address without a delegation edge; Omnipus MUST reply through the request-bound reply.
- **FR-091**: Omnipus's main `needs_attention` MUST include pending questions, approvals and secret cards of its own helper descendants; a pending secret card MUST count for any main.
- **FR-092**: Approvals from Omnipus helpers MUST carry the root session and acting agent name, and MUST render in the Omnipus chat regardless of the active workspace; unrelated approvals keep today's scope.
- **FR-093**: Helper questions MUST be relayed to the main, which asks the user on its own card.
- **FR-094**: Omnipus's self-row MUST be seeded in the default workspace at depth 1, be editable and removable; removal MUST make helper launches fail visibly.
- **FR-095**: Omnipus MUST start at most one apply helper per workspace and re-confirm a plan whose meaning changed after a stale-revision result.

### J. Screens
- **FR-100**: The sidebar MUST show a fixed Omnipus entry above workspaces that opens Omnipus's main without changing the active workspace; an attach failure MUST disable sending and offer Retry.
- **FR-101**: The entry MUST show the attention marker from FR-091.
- **FR-102**: Team MUST show the list view first and the graph second; cards per F:D20; workers in a separate section including external-program workers.
- **FR-103**: Team's Add agent MUST offer "Create with Omnipus" and "Add an existing agent".
- **FR-104**: The picker MUST list every agent except Omnipus and mark agents in no workspace.
- **FR-105**: Delete everywhere MUST list every workspace the agent is in before confirmation.
- **FR-106**: Settings MUST offer "System agents" (Judge, Plan Supervisor, Omnipus) through the shared editor; Omnipus's chat header MUST link there; Omnipus's model MUST be editable without a working model.
- **FR-107**: Workspace entry MUST open the remembered chat, else the first conversation agent in team order excluding Omnipus, else the empty state with "Set up a team".
- **FR-108**: The Agents screen and routes (`/agents`, `/agents/$agentId`), the create wizard/modal and `/admin/chat` MUST be removed without forwarding; REST create-agent MUST remain and be documented as a non-UI API.
- **FR-109**: The secret card and Team actions MUST meet the accessibility contract (labels, error association, announced states, focus return, no Go pre-selection, keyboard/touch reachable actions).
- **FR-110**: The secret card and Team list MUST use catalogued design-system components; a new reusable composite MUST follow the four-part publication contract.

### K. No default agent
- **FR-120**: The system MUST have no default-agent concept: the setting, its seed, `Agent.default`, the `default` update field and tool arguments, the delete guard for the default, the registry override and every `GetDefaultAgent` call MUST be removed; an old config key MUST be ignored with a warning.
- **FR-121**: An inbound channel message with no route (no binding and no named agent; bound agent missing, a worker, or failed to load) MUST be dropped, logged at error level, audited, and shown as the instance's `degraded` status with reason; it MUST NOT reach any agent.
- **FR-122**: A chat frame or session-create request without an agent MUST be refused with a visible error.
- **FR-123**: A task, goal turn or steering item whose agent no longer resolves MUST be refused visibly and not run by another agent; a steering item without an agent MUST be refused.
- **FR-124**: Internal look-ups (state manager setup, MCP working directory, provider extraction, startup info, skill listing) MUST work without a default agent.
- **FR-125**: Tool listing endpoints MUST use the central builtin registry.
- **FR-126**: Browser warm-up at boot MUST NOT depend on a default agent (behaviour per OQ-4; proposed: warm nothing).
- **FR-127**: `ChannelRouting.default_agent_id` empty MUST mean "no agent named" (unroutable), never a global default.

### L. Documentation
- **FR-130**: Every user-facing page in §13 MUST be updated in the same change as its behaviour and audited by docs-verifier.

---

## 11. Success criteria

- **SC-001**: On a fresh install, a user creates an agent in a named workspace through Omnipus with exactly 1 question card and 0 approval cards (no connectors), and the agent answers a message in that workspace — observed in the live E2E W1 spec.
- **SC-002**: A byte search for every submitted test secret over transcripts, WS captures, gateway logs, audit log, tool-call arguments and resume records returns 0 matches.
- **SC-003**: 100% of ask-list tools raise an approval under Auto (dataset D5) and 0 do in God Mode.
- **SC-004**: An inbound message on an unroutable instance produces 0 agent turns, 1 error log line, 1 audit entry and a `degraded` instance status within 5 s.
- **SC-005**: `grep` for `GetDefaultAgent`, `DefaultAgentID` and `SetDefaultAgentOverride` in non-test Go source returns 0 matches; the guard script for `ava`/`admin` ids passes.
- **SC-006**: With workspace B active, an Omnipus helper approval is actionable in the Omnipus chat in 100% of 10 runs, including 1 reload during the pending approval.
- **SC-007**: `/agents`, `/agents/mia` and `/admin/chat` render not-found with no redirect.
- **SC-008**: The configured-endpoint provider check reports "saved but not working" for the catalogue-OK/custom-fail fixture.
- **SC-009**: All user-facing pages in §13 pass docs-verifier with 0 FALSE claims.
- **SC-010**: `make verify-contracts` passes with the K1–K17 changes and no hand-written wire types (lint passes).

---

## 12. Traceability matrix

| FR | Source | US | BDD | Tests |
|---|---|---|---|---|
| FR-001 | F:D8, D22, D23; OSA-1 | US-1 | 1.1 | 1 |
| FR-002 | OSA-1.1; BRD §5 | US-1 | 1.2 | 2 |
| FR-003 | FA:FQ-1 B; OSA-1.3 | US-1 | 1.3, 1.4 | 19 |
| FR-004 | FA:FQ-1 B | US-1, US-12 | 1.5, 12.4, 12.11 | 26, 27, 28 |
| FR-005 | F:D15; OSA-1.3 | US-1 | 13.3 | 45 |
| FR-006 | OSA-1.3 | US-1 | 4.5 | 14 |
| FR-007 | OSA-1.4 | US-1 | 1.6 | 3 |
| FR-008 | F:D22, D23 | US-1 | 1.1 | 1, guard script |
| FR-010 | F:D8; OSA-2.2 | US-2 | 2.1 | 51, 57 |
| FR-011 | F:D13, D14; OSA-2.1, OSA-2.4 | US-2 | 2.1, 2.2, 2.4 | 51, 58 |
| FR-012 | F:D13; OSA-11 | US-2, US-4 | 2.1, 4.1 | 52, 57 |
| FR-013 | F:D13 | US-2 | 2.2, 2.3 | 58 |
| FR-014 | F:D6; OSA-2.5, OSA-3 | US-3 | 3.1 | 51, 57 |
| FR-015 | F:D7; OSA-3 | US-3 | 3.4 | 47, 60 |
| FR-020 | F:D6; FA:FQ-2 A | US-3 | 3.3 | 4 |
| FR-021 | OSA-3, OSA-5, OSA-7 | US-3 | 3.3 | 4, 5 |
| FR-022 | FA:FQ-16 B | US-3 | 3.5 | 6 |
| FR-023 | F:D7; FA:FQ-5/16 | US-3 | 3.4 | 47, 60 |
| FR-024 | FA:FQ-3 A | US-14 | 14.1, 14.2, 14.4 | 17, 61 |
| FR-025 | FA:FQ-3 A | US-14 | 14.3 | 18 |
| FR-026 | F:D6 | US-3 | 3.1, 3.2 | 57, 60 |
| FR-027 | FA:FQ-6 A | US-3 | 3.6 | 57 |
| FR-030 | F:D9; OSA-4 | US-9 | 9.4, 8.3 | 15, 36 |
| FR-031 | OSA-4 | US-9 | 9.1, 9.2, 9.3 | 12 |
| FR-032 | OSA-4 | US-9 | 9.5 | 16 |
| FR-033 | F:D9 | US-9 | 9.4 | 57 |
| FR-040 | OSA-9.1 | US-4 | 4.4, 4.6 | 13 |
| FR-041 | F:D2; OSA-9 | US-4 | 4.4 | 13, 57 |
| FR-042 | OSA-9.1 | US-4 | 4.5 | 14 |
| FR-043 | OSA-9.1 | US-4 | 4.6 | 13 |
| FR-045 | F:D17 | US-4 | 4.1 | 52 |
| FR-046 | F:D2 | US-4 | 4.2, 4.3 | 57 |
| FR-047 | F:D24; OSA-9 W8 | US-5 | 5.1, 5.8 | 43 |
| FR-048 | F:D22; OSA-9 W8 | US-5 | 5.1 | 43 |
| FR-049 | F:D26; FA:FQ-14 A | US-5 | 5.3, 5.4 | 59 |
| FR-050 | OSA-10 a11y | US-5 | 5.5 | 59 |
| FR-051 | OSA-9 W18 | US-5 | 5.6 | 59 |
| FR-052 | F:D24 | US-5 | 5.2, 5.7 | 54, 59 |
| FR-060 | F:D10 | US-6 | 6.1, 6.12 | 33 |
| FR-061 | F:D10; OSA-7 | US-6 | 6.1, 6.6 | 25, 32 |
| FR-062 | F:D10 | US-6 | 6.2 | 31 |
| FR-063 | OSA-7.4 | US-6 | 6.9, 6.10 | 7, 30 |
| FR-064 | OSA-7.4 | US-6 | 6.4 | 8 |
| FR-065 | OSA-7.3 | US-6 | 6.3 | 10 |
| FR-066 | OSA-7.5; F:D12 | US-6 | 6.5 | 9 |
| FR-067 | OSA-7.5 | US-6 | 6.5, 3.2 | 9, 55, 60 |
| FR-068 | OSA-7.5 | US-6 | 6.2 | 31 |
| FR-069 | OSA-7.6 | US-6 | 6.13 | 31 |
| FR-070 | OSA-7.10 | US-6 | 6.8 | 11 |
| FR-071 | F:D10 S2 | US-6 | 6.9 | 7, 30 |
| FR-072 | F:D10 S3 | US-6 | 6.3 | 10 |
| FR-073 | F:D10 S4 | US-6 | 15.1 | 48 |
| FR-074 | OSA-7 journey | US-6 | 6.5, 6.7, 11.8 | 25, 55 |
| FR-075 | OSA-7 journey | US-6 | 6.7 | 25, 55 |
| FR-076 | FA:FQ-7, FQ-18 | US-7 | 7.1–7.4 | 34, 35 |
| FR-077 | FA:FQ-13; #1277 | US-15 | 15.1–15.4 | 48, 49 |
| FR-078 | FA:FQ-8 | US-6, US-11 | 6.11, 6.12 | 33 |
| FR-080 | F:D11; OSA-5 | US-8 | 8.1, 8.2, 8.3 | 36 |
| FR-081 | OSA-5 | US-8 | 8.4, 8.5 | 37 |
| FR-082 | F:D11; OSA-5 | US-8 | 8.6 | 38 |
| FR-090 | F:D4, D5 | US-10 | 10.1, 10.4 | 41 |
| FR-091 | F:D16; FA:FQ-8 | US-10 | 10.2, 10.3 | 40 |
| FR-092 | FA:FQ-8, FQ-15 | US-11 | 11.2, 11.3, 11.8 | 24, 39, 56 |
| FR-093 | F:D27 | US-11 | 11.1, 11.4 | 59 |
| FR-094 | FA:FQ-1 B; OSA-8 | US-11 | 11.6, 11.7 | 42 |
| FR-095 | F:D27; OSA-8 | US-11 | 11.5 | 50 |
| FR-100 | F:D15 | US-12 | 12.1, 12.2 | 52 |
| FR-101 | F:D16 | US-12 | 10.2 | 40, 52 |
| FR-102 | F:D20 | US-12 | 12.3 | 26, 54 |
| FR-103 | F:D20 | US-12 | 12.3 | 54 |
| FR-104 | F:D21 | US-12 | 12.4 | 27 |
| FR-105 | F:D20 | US-12 | 12.5 | 26, 54 |
| FR-106 | FA:FQ-9 A | US-12 | 12.6 | 29 |
| FR-107 | F:D22; OSA-10 U8 | US-12 | 12.11, 5.6 | 28 |
| FR-108 | F:D18, D19, D23; FA:FQ-10 A | US-12 | 12.7, 12.8 | 53 |
| FR-109 | OSA-10 a11y | US-12 | 12.9, 12.10 | 25, 26, 55 |
| FR-110 | OSA-10 design system | US-12 | 12.3, 12.9 | 25, 26 |
| FR-120 | FA:FQ-17 | US-13 | 13.5, 13.7 | 22, 46, 53 |
| FR-121 | FA:FQ-17 | US-13 | 13.1, 13.2 | 20, 21, 44 |
| FR-122 | FA:FQ-17 | US-13 | 13.3 | 45 |
| FR-123 | FA:FQ-17; OSA-12 | US-13 | 13.4 | 23 |
| FR-124 | FA:FQ-17 | US-13 | 13.6 | 22 |
| FR-125 | OSA-12 | US-13 | 13.6 | 46 |
| FR-126 | FA:FQ-17 | US-13 | 13.6 | 22 |
| FR-127 | FA:FQ-17 | US-13 | 13.1 | 20, 44 |
| FR-130 | root CLAUDE.md DoD | all | — (docs-verifier audit) | docs-verifier report |

Every BDD scenario 1.1–15.4 appears in at least one row (checked by script before commit).

---

## 13. Reachability (Definition of Done)

| Capability | Registered / wired | UI reachable | Proof |
|---|---|---|---|
| Omnipus agent | Seeded; in static catalog roster | Sidebar entry | E2E 52 |
| Workflow skills | Embedded, granted to Omnipus (ADR-072 grant) | Via Omnipus chat | Live E2E 57–59 |
| `connect_channel`, `connect_mailbox`, `set_agent_permissions`, `request_secret`, `list_secrets`, `remove_secret` | `pkg/coreagent` static names; `pkg/config/defaults.go` ceiling; Omnipus per-agent seed; `pkg/tools/auto_approve.go` classification; central builtin registry | Omnipus chat; secret card for every native agent | `grep -rl '"connect_channel"' pkg/coreagent/ pkg/config/ pkg/tools/` ≥ 3 files per tool; live E2E 61 |
| Agent-private secrets | `request_secret` kind; injection in sandboxed command env | Secret card in any agent's web chat | Integration 34, hands-on UAT |
| Helper decisions in main | Root-session frames; SPA scope | Omnipus chat while another workspace is active | E2E 56 |
| Team list / picker / System agents | SPA components | Team tab; Settings | E2E 54; unit 29 |
| No default agent | Removal | Channel status shows unroutable | Integration 44; UAT |

Two claims stay separate in every report: "code correct and tested" and "reachable by a user or agent". Engine-touching units (WU-1, WU-2, WU-3, WU-4, WU-5, WU-8) require `uat-tester` + `uat-validator` on the exact landing commit.

---

## 14. User-facing documentation TODOs (page by page)

| Page | TODO | Work unit |
|---|---|---|
| `docs/tools.md` | Replace "How to change an agent's tool access" (Agents screen) with Team card → Edit and Omnipus's permission workflow; ask list; Omnipus has no per-agent Ask (Deny only); God Mode for Omnipus | WU-3, WU-10 |
| `docs/security.md` | R-A1, R-A2 (read-to-web disclosure), R-A3 (external-program workers), each "accepted by the founder 2026-10-12"; secret field guarantees; agent-private secret narrower promise (OQ-1); secret removal and #1277; no default agent — unroutable channel messages are dropped | WU-4, WU-5, WU-6, WU-1 |
| `docs/agents.md` | Omnipus replaces Ava/Admin; only Omnipus creates agents (REST API exception); Team as agent home; external-program workers and R-A3; no default agent | WU-2, WU-10, WU-11 |
| `docs/workspaces.md` | New workspaces start empty; setup offer; W18; reuse unchanged and shared-impact proposals; Team list/graph; entry rule | WU-10 |
| `docs/using-omnipus-ui.md` | Omnipus sidebar entry and attention; helper cards in the Omnipus chat; workspace entry rule; no Agents screen; Settings → System agents; secret card states | WU-10, WU-4 |
| `docs/getting-started.md`, `docs/tutorial.md` | First agent via Omnipus; provider first | WU-10 |
| `docs/concepts.md` | Omnipus system agent; workflows; no default agent | WU-2 |
| `docs/skills.md` | Skills via Omnipus; project shelf of the target workspace | WU-3 |
| `docs/connectors.md`, `docs/connectors/telegram.md` | `connect_channel`; proven vs not-tested facts; QR pairing in chat; unbound channels fail loudly | WU-7, WU-1 |
| `docs/mail.md` | `connect_mailbox`; read vs send readiness; no probe email | WU-7 |
| `docs/providers-and-models.md` | What the provider check proves; Omnipus's model in System agents | WU-3, WU-10 |
| `docs/settings.md` | System agents; no default-agent setting | WU-10, WU-1 |
| `docs/reference/built-in-tools.md` | New tools, policies, Auto classification | WU-2 |
| Sweep (mentions) | `docs/README.md`, `docs/troubleshooting.md`, `docs/plans.md`, `docs/goals.md`, `docs/browser.md`, `docs/using-omnipus-cli.md`, `docs/operations/observability.md`, `docs/connectors/google-chat.md` | WU-12 |

Each page is drafted by the implementing lead in the same change and audited by docs-verifier (FR-130); WU-12 is the final sweep and consistency pass.

---

## 15. Work units, sizes and dependency order

| WU | Content | Size | Owner(s) | Depends on |
|---|---|---|---|---|
| WU-0 | Contracts K1–K17 + regeneration | Standard | architect (shape, done here) → backend-lead | — |
| WU-1 | No default agent (FR-120–FR-127), loud channel failure | Feature | backend-lead; frontend-lead (remove default UI) | WU-0 |
| WU-2 | Omnipus identity, Ava/Admin removal, membership rules, self-row, role policies, ceiling + ask-list, Omnipus per-agent Ask refusal, God Mode note (FR-001–FR-008, FR-015, FR-020–FR-023, FR-094) | Feature | backend-lead; prometheus (prompt) | WU-0 |
| WU-3 | Change-tool adaptations: target workspace, permission tool split, management-grant refusal, outcome vocabulary, provider and workspace checks (FR-024, FR-025, FR-030–FR-033, FR-040–FR-043) | Feature | backend-lead | WU-2 |
| WU-4 | Secret field core: envelope, atomic promotion, descriptor, request/submit/frames, card UI, handles-only config tools, redaction (FR-060–FR-075, FR-078) | Feature | backend-lead, frontend-lead; security-lead review | WU-0, WU-3 |
| WU-5 | Agent-private secrets (FR-076) | Feature | backend-lead; security-lead | WU-4 |
| WU-6 | Secret listing and removal (FR-073, FR-077) | Feature | backend-lead | WU-4; **issue #1277** for in-use removal |
| WU-7 | `connect_channel`, `connect_mailbox`, pairing in chat (FR-080–FR-082) | Feature | backend-lead, frontend-lead | WU-3, WU-4 |
| WU-8 | Helper decisions in Omnipus main + attention (FR-091–FR-093, FR-095) | Feature | backend-lead, frontend-lead | WU-0, WU-2 |
| WU-9 | Omnipus workflow skills, core prompt, routing table, `AskUserQuestion` text (FR-010–FR-014, FR-026, FR-027, FR-033, FR-046, FR-049–FR-051) | Feature | prometheus-prompt-engineer; backend-lead wiring | WU-2, WU-3, WU-4, WU-7 |
| WU-10 | Screens: entry + attach path, Add agent → Omnipus, workflow start, Team list/picker, System agents, workspace entry, empty-team offer, kickoff retarget, accessibility (FR-004, FR-045, FR-047, FR-048, FR-052, FR-100–FR-107, FR-109, FR-110) | Feature | frontend-lead; backend-lead (kickoff) | WU-0, WU-2, WU-8 |
| WU-11 | Removals: Agents screen, routes, create UI, `/admin/chat`, guard scripts, retired-surfaces list in root `CLAUDE.md` (FR-108, FR-008 guard) | Standard | frontend-lead; prometheus (CLAUDE.md text) | WU-10 |
| WU-12 | User docs sweep and consistency (FR-130) | Standard | implementing leads; docs-verifier | all above |
| WU-13 | UAT campaign (hands-on, exact commit) | — (gate) | qa-lead plan; uat-tester/validator | all above |

```
WU-0 ──┬─► WU-1 ─────────────────────────────────────────────┐
       ├─► WU-2 ─┬─► WU-3 ─► WU-4 ─┬─► WU-5                   │
       │         │                 ├─► WU-6 (◄─ #1277)        ├─► WU-12 ─► WU-13
       │         │                 └─► WU-7 ─┐                │
       │         └─► WU-8 ──────────────────┼─► WU-9 ─┐       │
       └────────────────────────────────────┴─► WU-10 ┴─► WU-11┘
```

Parallel-safe waves: {WU-1, WU-2}; {WU-3, WU-8}; {WU-4}; {WU-5, WU-6, WU-7}; {WU-9, WU-10}; {WU-11}; {WU-12}; {WU-13}.

---

## 16. Ambiguity warnings (spec-level, resolved by stated assumption unless a grill objects)

| # | Ambiguous | Assumption taken | Question |
|---|---|---|---|
| 1 | Per-platform "proven facts" for `connect_channel` | Minimum: one authenticated platform API call where one exists; otherwise "saved, not tested: <reason>"; backend-lead fills the per-platform table in WU-7 | Grill to confirm the list is enough |
| 2 | Secret card limits | 1–8 fields; value ≤ 16 KiB; body ≤ 64 KiB; one pending card per session | — |
| 3 | Agent-private secrets for which agents | All native agents with `bash` or `environment_setup`; external-program workers excluded | — |
| 4 | Pairing timeout | Existing platform pairing timeout reused | — |
| 5 | Omnipus's model on fresh install | Uses the install's default model selected in onboarding | — |
| 6 | `list_secrets` scope for ordinary agents | Own agent-private names only; Omnipus sees all names | Grill to confirm |
| 7 | GitNexus impact | Not run (tools unavailable); leads must run `impact` before edits | — |

---

## 17. Open questions for the founder

**OQ-1 — Confirm the promise wording for agent-private secrets (FQ-18).** Context: you put agent-private secrets in scope; team-lead is to confirm the wording. The spec is written with option A. Impact: the text in `docs/security.md` and the card.
- **A** — "Never typed into chat, never in tool arguments, masked in output on a best-effort basis; a command you allow the agent to run can still reveal it." *(written into this spec)*
- **B** — A stricter rule (for example only `environment_setup` runs get the variable).
- **Recommendation: A** (your stated default unless you object).

**OQ-2 — A channel with no workspace binding but an explicitly named agent.** Context: FQ-17 says an unbound channel fails loudly. Today a channel can also be "unbound" (no workspace) while still naming one agent for all its messages (`ChannelRouting.default_agent_id` with no `workspace_id`). That names its agent, but has no workspace, and agent main chats live per workspace. Impact: whether such existing setups keep working.
- **A** — Treat it as routed: it names its agent; messages go to that agent as today.
- **B** — Treat it as unbound: only workspace-bound channels route; everything else fails loudly with "bind this channel to an agent in a workspace".
- **Recommendation: B** — one rule ("bound to an agent in a workspace, or loud failure"), consistent with main chats being per workspace and with `connect_channel`, which always binds.

**OQ-3 — How long an unused secret stays usable.** Context: a secret entered on the card waits for the confirmed plan; unused ones expire (D12). Impact: too short loses the user's input during a long interview; too long keeps secrets staged.
- **A** — 30 minutes. **B** — 2 hours. **C** — until the conversation's next restart.
- **Recommendation: A** — matches the existing 30-minute question timeout.

**OQ-4 — Browser warm-up at startup without a default agent.** Context: today the gateway pre-starts one browser at boot for the default agent's workspace, so the first browser panel opens faster. With no default agent there is no workspace to pick. Impact: first-panel speed only.
- **A** — Warm nothing; the first panel opens cold (one-time delay).
- **B** — Warm the default workspace's browser.
- **Recommendation: A** — no implicit choice of whose logged-in browser profile to start.

---

## 18. Evaluation scenarios (holdout — not referenced by the TDD plan or traceability)

1. **Happy** — Fresh install, user says "I need an agent that answers support emails in Sales". Expected: one summary, Go, a mailbox secret card, one `connect_mailbox` approval; the new agent appears only in Sales and can read the test inbox.
2. **Happy** — In workspace Marketing, create workspace "Events"; accept the team offer. Expected: lead + specialists created, Events opens on the lead's chat, the graph and text list match.
3. **Happy** — Jim asks Omnipus for a Notion connector for Mia while the user is in Sales. Expected: Omnipus's entry lights; approval in the Omnipus chat; Jim gets the answer.
4. **Error** — Disconnect the network during the secret submit. Expected: "submission unknown", then the true state after reconnect; values never re-requested if staged.
5. **Error** — Delete Mia while a Telegram instance is bound to her and send a Telegram message. Expected: no reply from anyone; the channel shows "unroutable" with the reason.
6. **Edge** — In God Mode, ask Omnipus to delete an agent. Expected: summary message, no cards, agent deleted, report.
7. **Edge** — Keyboard-only: complete a two-secret Slack connect. Expected: every step reachable and announced.

---

## 19. Assumptions

- Greenfield: no migration of Ava/Admin data, default-agent settings or older ceilings (F:D22).
- #1277 is fixed by a separate change; WU-6's in-use removal waits for it.
- External-program workers keep their own runtime; they get no secret field or agent-private secrets.
- R-A1–R-A3 are not mitigated beyond the ask list; they are documented.

## 20. Clarifications

### 2026-10-12
- Q: Default agent? → A: None at all (FA:FQ-17); supersedes ADR OSA-12.
- Q: Agent-private secrets now or later? → A: In scope of this implementation, promise option A pending confirmation (FA:FQ-18; OQ-1).
- Q: Helper decisions? → A: Always in the Omnipus main chat, attention there (FA:FQ-8, FQ-15).
- Q: Per-agent Ask for Omnipus? → A: Refused; Deny allowed (FA:FQ-16 B).
