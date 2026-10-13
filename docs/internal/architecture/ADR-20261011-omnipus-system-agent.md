# ADR-20261011 — The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field

**Correction — 2026-10-12, the sole correction round.** At `4b298976` this ADR said Omnipus's residual injection risk was "a persistent foothold, not direct exfiltration" and that "secrets and host execution stay behind the ask list". The grill (`docs/internal/architecture/ADR-20261011-omnipus-system-agent-review.md` at `cdab144f0`, CRIT-001/CRIT-002) showed that was wrong: allowed file reads plus public web calls can disclose private files, and an allowed `create_agent` can plant an external program that later runs on the host. The correct statement is in OSA-3's threat model: the founder explicitly accepted both risks on 2026-10-12 after a stress test (founder answers FQ-11 A, FQ-12 C). The first draft also proposed a code confirmation check (M-B), a main-only rule for ask calls, a helper-free secret card and a user-settable default agent; founder answers replaced all four. Every grill finding and every founder answer is dispositioned in "Correction round 2026-10-12" below. No second grill runs on this ADR.

| Header | Value |
|---|---|
| Status | **Proposed** — corrected once after the one ADR-mode grill and the founder interview. Two new points (FQ-17, FQ-18) are open; they are listed at the end and do not reopen answered questions. The ask list (OSA-3) still needs the security-lead pass before the specification is accepted (founder record F:D6). |
| Date | 2026-10-12 (founder decision date; correction round the same day). The ID carries the UTC date 2026-10-11 produced by `scripts/new-adr-id.sh "Omnipus system agent"`. |
| Deciders | Daniel Piatkowski (founder). |
| Author | Architect. |
| Founder record (authority) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::Decisions Log D1–D27, cited **F:Dn**. |
| Founder answers (authority) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md` — answers to FQ-1…FQ-16 and the default-agent question, collected by team-lead 2026-10-12, cited **FA:FQ-n** / **FA:Default**. Where the founder answered a question twice (FQ-7), the later row wins. |
| Grill (input) | `docs/internal/architecture/ADR-20261011-omnipus-system-agent-review.md` at `cdab144f0`, findings CRIT-001…OBS-003, evidence E01–E39, cited **G:ID**. |
| Visual draft | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html` (cited **WF:§n / WF:Wn**); its pre-revised-D6 trade-off rows are superseded by F. |
| BRD (background) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/BRD-omnipus-system-agent-20261011.md` (cited **BRD:§n**); superseded points listed below. |
| Builds on / amends | Builds on **ADR-072 — Skill activation and loading**, **ADR-076 — Reconcile the global tool-policy ceiling**, **ADR-077 — Two-layer tool-policy model**, **ADR-029 — Channel-instance ↔ workspace binding**, **ADR-033 — Per-(Agent, Workspace) Email Mailboxes**, **ADR-004 — Credential boot contract** (not amended here — issue #1277 owns that change). Amends by dated clause: **ADR-090 — Built-in agent configuration, skills, and visual file reading**; **ADR-092 — Shell permission modes** (D9 ask-list, additive); **ADR-20261007 — Agent-first navigation and agent identity**; **ADR-20261006 — Session core with an agent address book**. |
| Supersedes | ADR-090's Ava and Admin roles (§2.0 rows, §2.1 Ava, §2.2 Admin, §3 Ava/Admin edges, §4 rows, §5 columns, §6.1 skills, §6.2 checklists), replaced by one Omnipus role. |
| Code baseline | Draft read at `5054b1455`; correction-round code re-checks at `cdab144f0` (same production tree; only review documents added). GitNexus MCP was not available; impact statements come from Grep/Read. |
| Release | **v0.1.1** — founder answer R1 A, 2026-10-12, relayed by team-lead (not read first-hand by the architect). |
| Review boundary | One ADR-mode grill (done) and this one correction round. No specification, contract, code or user-documentation edit is made by this ADR. |

---

## Founder answers after correction (FQ-17, FQ-18) — dated 2026-10-12

Recorded after the one correction round (no new correction round; source: founder answers file, section "After the correction round"). Where this section and the text below disagree, this section wins.

| Question | Founder answer | Effect on this ADR |
|---|---|---|
| FQ-17 | **No default agent at all** (supersedes "Omnipus is the default", FA:Default). An unbound channel, or one whose bound agent no longer exists, drops the message **loudly**: visible error on the channel's status, logged, never silently routed. Every other former fallback (tasks, goals, sessions, chat frames) names its agent explicitly or fails visibly. The default-agent setting, the Mia seed and the `ChannelRouting.default_agent_id` fallback semantics are removed. | **OSA-12 is superseded.** Its per-caller table is re-decided in the specification (`docs/internal/specs/omnipus-system-agent-spec.md` FR-120–FR-127): routing fallbacks fail loudly, web/REST requests without an agent are refused, internal look-ups no longer use a default, tool listings use the central registry. The FQ-17 question below is answered. Retired surfaces gain "default-agent setting". |
| FQ-18 | Agent-private secrets are **in scope of this implementation** (designed here and in the specification, built with it). Promise wording: option A — never typed into chat, never in tool arguments, masked in output best-effort; a command can still reveal it; documented in `docs/security.md` — **pending one founder confirmation** of the wording. | OSA-7.8 stands with option A; the specification carries it as FR-076 and open question OQ-1. |

---

## Founder answers to specification questions — answers 2026-10-12, recorded 2026-10-13

Recorded without a new correction round (source: founder answers file, sections "Spec open questions OQ-1..OQ-4" and "Spec grill round 1 questions"). Where this section and the text below disagree, this section wins; the specification (`docs/internal/specs/omnipus-system-agent-spec.md`, §0.3) carries the detail.

| Answer | Effect on this ADR |
|---|---|
| OQ-1 — agent-private secrets belong to one agent in one workspace; wording A confirmed | OSA-7.3/7.8: the private kind binds to (workspace, agent, variable), not (agent, variable). |
| OQ-2 B — every channel is bound to an agent in a workspace | ADR-029's legacy unbound routing is removed; an unbound configuration fails loudly with "bind this channel to an agent in a workspace". |
| OQ-3 — a secret is stored when the user submits the card | OSA-7.2/7.4/7.5 staging, promotion and expiry are **superseded**: no staging and no expiry; a cancelled plan leaves the stored secret unused, listed and removable. Recipient binding (OSA-7.3) stays. |
| OQ-4 B — warm the default workspace's browser at boot | Browser warm-up no longer depends on any agent. |
| Q-R1-1 A — replacing an in-use secret stores a new version | The integration keeps its current reference until its approved change tool selects the new one. |
| Q-R1-2 B — no password re-entry on the secret card | Explicit exception to ADR-022's re-confirmation for this scoped, destination-bound write only; raw credential routes keep it; security-lead verifies the request/session/recipient binding. |
| OQ-5 (2026-10-13) — first "keep per-person routing rules, separate feature", then the same day superseded: person routing is in scope | See OSA-14 below. |

## OSA-14 — Channel person routing per connection (founder decision, 2026-10-13)

Recorded after both specification grill rounds; **this decision has not been grilled** (team-lead will ask the founder about a focused review). Source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/channel-person-routing-decisions-20261013.md` (R1–R5, PR-1–PR-4) and the team-lead steer of 2026-10-13. Detail: specification US-16, FR-140–FR-151, K18–K22, WU-14.

| Topic | Decision |
|---|---|
| Structure (PR-1) | Each connection (channel instance) belongs to one workspace and holds an allow list, a person table (person or group → agent in that workspace) and one **main agent** for allowed people without a rule. ADR-029's bound identity becomes the main agent, applied **after** the person table instead of skipping all bindings. |
| Reuse (R4) | The person table is stored as `Config.Bindings` entries with a new `instance_id` match field; the `ResolveRoute` match priorities (peer, parent peer, guild, team, account) run at instance scope; `allow_from`, the session-key scopes behind `session.dm_scope`, `session.identity_links` and `group_trigger` are reused. Type-only bindings and `channel_policies.<ch>.routing_rules` are retired (ignored with a warning). |
| Route order | Unbound instance → loud failure (OQ-2); stranger → "not authorised" reply + operator status/log (PR-3); person rule → that agent (must be a workspace member; otherwise loud drop, never main-agent fallback); else main agent; else loud drop. No default agent anywhere (FQ-17). |
| Per-agent list (PR-2) | Derived from the connection tables; shown read-only in the agent editor with links; not stored. |
| Conversations (PR-4) | Per-connection mode: agent's main chat (default) or one chat per person; no restart. The install-wide `session.dm_scope` setting is proposed for retirement (founder question RQ-4). |
| Omnipus | `connect_channel` sets the main agent and can save offered person rules in the same approved call. |

**Founder answers RQ-1–RQ-4 (2026-10-13):** per-person channel conversations appear in the sidebar hidden by default (un-hideable) and in the agent's History; fixed stranger text once per stranger per connection per 24 h; an allowed group admits all its members (strangers inside it logged only); the global `session.dm_scope` is retired in favour of the per-connection mode, `identity_links` stay global.

## OSA-15 — Group chats per workspace (founder decision, 2026-10-13) — not yet grilled

Source: founder answers GC-1–GC-7 (file section "After spec fix round 2") and issues #1068, #892, #955. Detail: specification US-17–US-19, FR-160–FR-188, K23–K31, WU-15/WU-16.

| Topic | Decision |
|---|---|
| Shape (GC-1) | A group chat is the user plus chosen agents of **one** workspace; each workspace also has one standing **team room**. Squads (named) and ad-hoc groups are created only by the user (GC-3); an ad-hoc group with one agent is how extra chats return. Groups never span workspaces; direct session-to-session messages keep the existing peer rules, including cross-workspace main-to-main. |
| Mechanism (reuse) | A group is a session with `agent_ids` members and no owning agent; each member works in a hidden member session under it, so turns, Stop (descendant walk), approvals (root-session display), attention and stats are reused. Delivery uses the session-core request/answer path (`AdmitRequest`, guest answers into the source conversation). |
| Who answers (GC-2) | @all by default for the user's messages; @-names select members. Agents' messages without @: founder question GQ-1. |
| Agent-to-agent limit (GC-5) | None. **Accepted risk R-A4 (founder, 2026-10-13, after a stress test):** agents can keep answering each other with no human present, each reply costing money; mitigation is the visible per-group cost and the existing Stop. |
| Authority | A group message from an agent is input, never authority: it cannot approve, answer the user's cards, run commands or change configuration (#1068 §6). Membership changes are audited. |
| Self hand-off (GC-7) | A member may send one request to **its own main session in that workspace** (the only allowed self target; the `send_message` "never yourself" rule is extended for exactly this case). The main session does the work with its cards in the main chat, then reports once into the group. Loop guards: hand-offs originate only from member sessions, one open hand-off per member per group, and the report addresses nobody. |
| Sidebar (GC-4) | "Group chats" per workspace under the agent rows: team room, squads, ad-hoc groups, each with the attention marker. |
| Channel group conversations (#892) | A stopped one resumes only on a message from a person individually on the connection's tables or from the operator; automatic wakes never resume it. |
| Channel commands (#955, GC-6) | Commands kept on channels change only that conversation; nothing on a channel changes agent or global settings; admin commands are web/CLI only. |

## OSA-16 — Agent conversation awareness (founder decision, 2026-10-13) — not yet grilled

Source: founder answers AW-1, AW-2. Detail: specification US-20, FR-190–FR-198, WU-17.

| Topic | Decision |
|---|---|
| What the main session sees | At each main-chat turn a short list of the agent's other conversations **in the same workspace** (who, where, last activity, one-line summary; ≤ 20 entries, < 2,000 characters; no content). |
| Look-up | A new read-only tool `read_conversation` opens one of those conversations; it never sends, resumes or changes anything. Policy: ceiling allow; explicit deny for Judge, Plan Supervisor and workers. |
| Summaries and notes (reuse) | The one-line summary is the first sentence of the conversation's existing idle recap (title if none); the recap's `worth_remembering` items are written automatically to a new per-(agent, workspace) memory room. No extra model call. |
| Boundary (AW-2) | Nothing crosses workspaces. This also corrects an existing leak: the recap carry-forward `last-session.md` lives in the agent-wide private room today and is moved to per-(agent, workspace) storage. |



---

## Correction round 2026-10-12

### Grill findings → disposition

| Finding | Disposition | What changed |
|---|---|---|
| CRIT-001 read + web disclosure | **Founder override of the grill's recommendation (B).** FA:FQ-11 A, 2026-10-12, after an explicit stress test | False "not direct exfiltration" claim removed; OSA-3 threat model records read-to-web disclosure as founder-accepted risk R-A2 with date; user docs that must state it are named (OSA-13 doc map). |
| CRIT-002 external worker plants a host program | **Founder override of the grill's recommendation (A).** FA:FQ-12 C, 2026-10-12, after an explicit stress test | False "host execution stays behind the ask list" removed; R-A3 recorded with date; `create_agent`/`update_agent` stay `allow` including `cli_path`; docs named. |
| MAJ-001 M-B infeasible as described | Fixed by FA:FQ-4 A | M-B dropped entirely; the summary gate stays instruction-enforced; R-A1 recorded. The "only a human answers a card" assurance is corrected: a `default_safe` question can auto-resolve, so the Go question must never set `default_safe` (OSA-3). |
| MAJ-002 staged-secret lifecycle | Fixed | OSA-7: encrypted staging envelope, one atomic promote-and-consume store operation, request states, bundle rule, Cancel/Stop/expiry/restart, handles-only resume, sensitive-value registration. |
| MAJ-003 slot ≠ recipient | Fixed | OSA-7: handles bind to a server-owned **effective-recipient descriptor** (endpoint/account/executable/transport + field); any change needs a new request. |
| MAJ-004 provider check tests wrong endpoint | Fixed | OSA-4: check the saved effective entry through `pkg/providers/validate.go::ListModels`; names what "working" proves; negative controls listed. |
| MAJ-005 target vs anchor | Fixed | New OSA-9.1: acted-on workspace separated from the session anchor; inventory of implicitly scoped verbs; workflow-start metadata is target data; the mint-only kickoff is explicitly amended. |
| MAJ-006 shared-agent reuse | Fixed by FA:FQ-14 A | OSA-9 W18: reuse unchanged; any change is a separate shared-impact proposal; per-resource revision checks. |
| MAJ-007 approval modal hides Omnipus requests | Fixed by FA:FQ-15 B + FA:FQ-8 | OSA-8/OSA-10: Omnipus-rooted questions, approvals and secret cards render in the Omnipus main chat regardless of active workspace; attention on the main. |
| MAJ-008 channel "working" / QR in chat | Fixed | OSA-5: per-platform proven facts and "not tested" limits; QR pairing correlated to request/session through a contract change; mailbox read and send readiness without sending mail. |
| MAJ-009 credential removal can stop the gateway | Fixed by FA:FQ-13 | OSA-7: removal shows consumers first and reports durable/live/boot facts separately; gateway immunity is issue #1277 (P1) and a prerequisite; ADR-004 is not amended here. |
| MAJ-010 "failed: nothing saved" | Fixed | OSA-4: four outcomes including **partly saved**; existing structured partial fields are kept. |
| MAJ-011 secret-card journey states | Fixed | OSA-7: minimum state list and value-ownership rules. |
| MAJ-012 keyboard / focus | Fixed | OSA-10: accessibility contract for the secret card, Team actions and a text equivalent of the W18 graph. |
| MAJ-013 docs TODO map | Fixed | OSA-13: change-to-page TODO map, `docs/tools.md` added. |
| MAJ-014 God Mode after operator edits | Fixed by FA:FQ-16 B | OSA-3: per-agent `ask` is refused for Omnipus at the write boundary; per-agent `deny` stays; ADR-090 §2.6 amended for Omnipus. |
| MIN-001 wrong conflict literal | Fixed | C9 and OSA-8 now say "stale-revision result": `REVISION_CONFLICT` (workspace), `CONFLICT` (agent, skill). |
| OBS-001 design-system reuse | Adopted | OSA-10: specification maps the new card/list to catalogued components. |
| OBS-002 proof table | Adopted | OSA-13: the grill's test-coverage table is carried into the specification as mandatory negative controls. |
| OBS-003 main-only ask handbacks | Resolved by FA:FQ-15 B | Helpers may run ask tools; the main-only rule is gone. |

Tally: 2 critical (both founder-accepted risks, recorded), 14 major (13 fixed in design, 1 — MAJ-001 — removed by the founder's choice), 1 minor fixed, 3 observations adopted/resolved.

### Founder answers → where applied

| Answer | Applied in |
|---|---|
| FQ-1 B — hidden member of the default workspace | OSA-1.3, OSA-8, OSA-10 |
| FQ-2 A — `add_mcp_server`, `configure_provider` ask | OSA-3 |
| FQ-3 A — permission/connector-assignment tool, ceiling `ask` | OSA-3 |
| FQ-4 A — prompt rules only | OSA-3 R-A1 |
| FQ-5 (implied by FQ-16 B, not separately confirmed) | OSA-3 — no per-agent `ask` for Omnipus, so installs do not ask in God Mode |
| FQ-6 A — keep both confirmations | OSA-3 approvals table |
| FQ-7 — **both now** (later answer replaces "A now, B later") | OSA-7.8; new contradiction → FQ-18 |
| FQ-8 — every helper decision surfaces in the parent main; attention on the parent | OSA-8 |
| FQ-9 A — "System agents" section in Settings | OSA-10 U9 |
| FQ-10 A — keep REST create, documented, no UI caller | OSA-10 U4 |
| FQ-11 A, FQ-12 C — accepted after stress test | OSA-3 R-A2, R-A3; OSA-13 |
| FQ-13 — gateway must be immune; issue #1277; consumers shown first | OSA-7.9 |
| FQ-14 A | OSA-9 W18 |
| FQ-15 B (refined by FQ-8) | OSA-8 |
| FQ-16 B | OSA-3, amendments table |
| Default agent — Omnipus is the fixed default | New OSA-12; outside-channel consequence → FQ-17 |

---

## Context

### The problem in one paragraph

Two built-in helpers share the technical work today: **Ava** designs agents, skills, permissions and delegation, **Admin** installs connectors, providers and channels (ADR-090 §2.1–§2.2; `pkg/coreagent/prompts_adr090.go`::`"ava"`, `"admin"`). The user must know which to ask (BRD:§1 P4). Agents are created through a manual form that can leave the agent in no workspace (BRD:§1 P2). API keys pass through chat text because every configuration tool takes the plaintext secret as an argument (`pkg/sysagent/tools/provider.go`::`ProviderConfigureTool.Parameters`; `mcp.go`::`MCPAddTool.Description`; `channel.go`::`ChannelConfigureTool.Description`). The founder merged both helpers into one system agent, **Omnipus**, running predefined workflows (F:D8, F:D22, F:D23).

### Code facts (verified; C23–C31 added in the correction round)

| # | Fact | Evidence |
|---|---|---|
| C1 | Shipped ceiling: `create_agent`, `update_agent`, `update_workspace`, `add_mcp_server`, `configure_provider`, `create_skill`, `edit_skill`, `install_skill`, `enable_channel`, `configure_channel`, `disable_channel`, `remove_skill`, `delete_agent`, `set_config` = `allow`; `remove_mcp_server`, `delete_workspace` = `ask`. | `pkg/config/defaults.go`::`defaultToolPoliciesSysagent` |
| C2 | Merge is strictest-wins; God Mode floors only the global layer to `allow`; a per-agent `ask`/`deny` survives God Mode. | `pkg/tools/compositor.go`::`resolveEffectivePolicyWith` |
| C3 | ADR-092 D9's Auto ask-list holds every existing tool on OSA-3's ask list; an unclassified tool asks. | **ADR-092** D9; `pkg/tools/auto_approve.go` (table) |
| C4 | Admin's seed holds per-agent `ask` for `remove_mcp_server`, `disable_channel`, `environment_setup`, `bash`. | `pkg/coreagent/role_policies_adr090.go`::`ADR090RolePolicyInventory`, `adr090SparseRolePolicies` |
| C5 | `configure_provider` does not contact the provider; `test_provider` makes zero network calls; `list_models` calls each provider's live `/models` but takes the base from `providers.APIBaseFor` (catalogue), not the configured `api_base`, and skips providers without a key. A protocol-aware `ListModels` exists. | `pkg/sysagent/tools/provider.go`::`ProviderConfigureTool.Description`, `ProviderTestTool.Description`, `ModelsListTool.Execute`; `pkg/providers/validate.go`::`ListModels` |
| C6 | `add_mcp_server` connects at once and reports live status and tool count; stores `env` values under `mcp_<name>_<key>`; can switch `tools.mcp.enabled` on install-wide. | `pkg/sysagent/tools/mcp.go`::`MCPAddTool.Description`, `mcpEnvCredKey` |
| C7 | `test_channel` makes zero network calls; `configure_channel` stores secrets under `*_ref` and does not enable or bind. | `pkg/sysagent/tools/channel.go`::`ChannelTestTool.Description`, `ChannelConfigureTool.Description`, `channelCredKey`; **ADR-029** |
| C8 | `remove_skill` deletes the skill directory irreversibly. | `pkg/sysagent/tools/skill.go`::`SkillRemoveTool.Description` |
| C9 | Change tools carry an opaque `revision` precondition; a stale write returns `REVISION_CONFLICT` (workspace) or `CONFLICT` (agent, skill). | `pkg/sysagent/tools/workspace.go`::`workspaceRevisionError`; `agent_update_adr090.go` (`"CONFLICT"`); `skill.go` (`"CONFLICT"`) |
| C10 | `create_agent` joins the calling session's workspace; supports `agent_type` `subagent_3p` with `cli`/`cli_path`; the selected binary is resolved verbatim and run for a version probe when used. | `pkg/sysagent/tools/agent.go`::`AgentCreateTool.Description`, `joinWorkspaceTeam`; `pkg/agent/runner/executor_opts.go`::`resolveCLIBinary`; `pkg/agent/runner/driver_stream.go`::`detectCLIVersion` |
| C11 | `AskUserQuestion` is web-only, refused unattended, relayed to the parent in a delegated session; its description forbids use "as a request for permission to proceed". | `pkg/tools/ask_user_question.go`::`AskUserQuestionTool.Execute`, `Description` |
| C12 | `delegate` `run` returns at once; the parent is messaged on finish, question or failure. | `pkg/tools/delegate.go`::`DelegateTool.Description` |
| C13 | Approval frames are broadcast to every connection with a parent-walked `workspace_id`; the SPA shows an approval only when it is in the active workspace's scope. | `pkg/gateway/ws_tool_approval.go`::`approvalWorkspaceID`, `broadcastToolApprovalRequired`; `src/store/toolApproval.ts`::`isApprovalInScope` |
| C14 | Main attention (`Session.needs_attention`) lights only for that main's own pending question, approval or unseen goal ending. | **ADR-20261006** D11 |
| C15 | Admin is off every team, owns one main in the default workspace, has cross-workspace filesystem authority. | `pkg/coreagent/team_eligibility.go`::`ExcludedFromWorkspaceTeams`; `pkg/gateway/main_session.go`; `pkg/agent/workspace_reroot.go` |
| C16 | Self-delegation needs an ordinary caller→caller row and real membership; fresh self-row depth `min(3, ceiling)`. | **ADR-20261006** D4; `pkg/coreagent/seed.go`::`FreshSelfDelegationMaxDepth` |
| C17 | New workspaces are seeded with Ava only plus `setup_pending`; the `workspace_setup_kickoff` frame is **mint-only** — rejected when it names an existing session. | `pkg/gateway/rest_workspace_delegation.go`::`newWorkspaceSetupTeam`; `pkg/gateway/websocket_chat.go` (comment "A kickoff frame is MINT-ONLY"); `contracts/asyncapi.yaml` |
| C18 | Workspace entry opens Ava's main when nothing is remembered. | `src/lib/nav/workspaceEntry.ts` (`WelcomeEntry`); **ADR-20261007** |
| C19 | 31 `*_ref` tags in `pkg/config/config.go` (more in `config_channels_instance.go`); OAuth only for provider device-code logins. | `pkg/config/config.go`; `pkg/credentials/oauth_entry.go`::`OAuthEntryName` |
| C20 | `set_config` refuses `sandbox`, gateway auth/tokens, `tools.exec`, `tools.mcp`, `tools.browser` and other cage-widening keys. | `pkg/sysagent/tools/config.go`::`blockedConfigKeys` |
| C21 | The Agents screen also hosts "set default agent" and the System (Judge, Plan Supervisor) section; `/agents/$agentId` opens the editor slide-over. | `src/components/screens/AgentListScreen.tsx`; `src/routes/_app/agents.$agentId.tsx`::`AgentProfileRoute` |
| C22 | The only SPA caller of REST `createAgent` is the manual create modal; no CLI caller found. | `contracts/openapi.yaml` (`createAgent`); `src/lib/api/agents.ts`; `src/components/agents/CreateAgentModal.tsx` |
| C23 | New custom agents start deny-all with explicit overrides — management tools are denied. | `pkg/coreagent/seed.go`::`NewCustomAgentToolsCfg` (`denyAllThenOverride`) |
| C24 | The pending-question registry removes a card on submit, keeps one pending-ask metadata slot, persists answers as a user-role resume record, and can auto-answer a `default_safe` card with no human. | `pkg/askuser/registry.go`::`Registry.Submit`, `fireDefaultSafeSet` (G:E08) |
| C25 | Credential store `Get`/`Set`/`Delete` are separate operations, not one promotion. | `pkg/credentials/store.go`::`Store.Get`, `Store.Set`, `Store.Delete` |
| C26 | An enabled reference to a missing credential is fatal at boot; deleting a credential commits before reload. Tracked as issue **#1277** (open, "Gateway refuses to start or reload when an enabled channel's secret is missing"). | `pkg/gateway/gateway_boot_credentials.go`::`buildEnabledRefMap`, `bootCredentials`; `pkg/gateway/rest_settings.go`::`restAPI.deleteCredential`; `gh issue view 1277` |
| C27 | Project-skill authoring/removal resolves the shelf from the calling session's workspace (`tools.ToolWorkspaceID`). | `pkg/sysagent/tools/skill_authoring.go` (shelf resolution) |
| C28 | WhatsApp pairing updates travel as `whatsapp_pairing` frames keyed by `channel_id` only (no session or request), consumed by the Settings pairing store. | `contracts/asyncapi.yaml` (`WhatsAppPairingFrame`); G:E21 |
| C29 | `AgentRegistry.GetDefaultAgent` has 16 production call sites (listed in OSA-12); `config.Agents.Defaults.DefaultAgentID` is seeded to Mia on a fresh install. | Grep `GetDefaultAgent()` in `pkg/`; `pkg/coreagent/seed.go` (`DefaultAgentID = string(IDMia)`) |
| C30 | Inbound channel routing ends at "Priority 7: Default agent"; a binding naming a missing agent also falls back to the default; `ChannelRouting.default_agent_id` empty means "fall back to the global default agent". | `pkg/routing/route.go`::`RouteResolver` (Priority 7, `resolveDefaultAgentID`); `contracts/components/schemas/ChannelRouting.yaml` |
| C31 | Values are hidden from tool output by a best-effort sensitive-value replacement whose registration replaces the full set. | `pkg/config/security.go`::`Config.RegisterSensitiveValues` (G:E11) |

---

## Decision

Decision IDs are **OSA-1 … OSA-13**. In this correction round OSA-12 became the default-agent decision, and release moved into OSA-13 together with the documentation map and proof hand-off.

### OSA-1 — One Omnipus agent replaces Ava and Admin

1. **One agent, stable ID `omnipus`, name "Omnipus"**, type `core`, locked identity, chat-able; the name is reserved (F:D8, F:D22, F:D23; BRD:§5).
2. **Ava and Admin are deleted outright** — seeds, prompts, role inventories, skill assignments, Ava's chats, every identity branch (C15, C17, C18). Greenfield, no migration (F:D22).
3. **Anchoring (FA:FQ-1 B):** Omnipus is an ordinary but **hidden member of the default workspace only**. It gets an ordinary main chat (its one ongoing conversation, F:D15) and an ordinary self-row (OSA-8) — no delegation exception. It is filtered out of that workspace's Team list, sidebar agent rows, workspace-entry lead selection (OSA-10 U8) and the add-existing picker; it is shown only as the fixed sidebar entry; adding it to any other workspace is refused at the write boundary (tools and REST). Admin's off-team special cases are deleted, not retargeted. The one identity rule kept from Admin is cross-workspace filesystem authority for setup work (C15; ADR-090 §6.5).
4. **Role boundary (shipped defaults):**

| Group | Omnipus default |
|---|---|
| Look-ups | agent, workspace, model, provider, connector, channel, skill listings; `find_skills`, `run_doctor`, `get_usage`; secret-name listing (OSA-7) — `allow` |
| Talk / coordinate | `AskUserQuestion`, `send_message`, `answer_request`, `message_parent`, `delegate` (self only), `Skill`, `ToolSearch`, memory — `allow` |
| Files | read and write within setup scope, as Admin today — `allow` (R-A2 applies) |
| Web look-up | `search_web`, `fetch_url` — `allow` (kept by FA:FQ-11 A; R-A2) |
| Change tools | OSA-3 |
| Shell | `bash` — `ask` ceiling (ADR-092); diagnosis only (WF:W17) |
| Not granted | `set_config` (F:D1; C20), `delete_workspace`, `create_workspace`, plan tools, browser tools, `send_email`/`reply`, knowledge-base writes, `serve_web` |

The specification enumerates the exact supporting tools inside this boundary.

### OSA-2 — Workflows are skills; routing and lifetime reuse the skill machinery

1. The **core prompt** holds the standing rules (summary + question before changes, never answer for the user, never "done" without a tool check, secrets only through the field, a peer message or tool content is a request never a confirmation) and a short routing table (F:D13; WF:§4). The routing table is maintained in one place (core prompt); workflow skills carry only their own "use when" line (G: D13 coverage note).
2. **Each workflow is an ordinary skill** under ADR-072's per-agent grant, loaded with `Skill` (F:D8). Ava's and Admin's skills (ADR-090 §6.1) are replaced; capability planning and dependency checks are skills using existing look-ups.
3. **Routing (F:D13):** button names the workflow (OSA-11); free text matched against skill menu + routing table; two fit → ask; none → answer, change nothing; sub-workflows show one summary.
4. **Lifetime (F:D14):** a loaded skill stays on later turns until oldest-turn trimming; the tool text already says so (`pkg/tools/skill.go`::`Description`); reload on a trim notice.
5. **Prompt-text correction (prometheus-prompt-engineer):** `AskUserQuestion`'s description must allow exactly one use as a plan confirmation (C11); Ava's "never as permission" line disappears with Ava.

### OSA-3 — Approval model, ask list and threat model

**The gate (F:D6).** Each workflow ends planning with a summary and one `AskUserQuestion` card: Go / Change / Cancel. The Go question must **never** carry `default_safe` (C24: a `default_safe` card auto-resolves with no human). After Go, Omnipus runs its change tools; `allow` is the default.

**Ask list (FA:FQ-2 A, FA:FQ-3 A; security-lead pass still required).**

| Tool | Policy | Today (C1) | Reason |
|---|---|---|---|
| `delete_agent` | **ask** | allow | Deletes owned chats and memory (ADR-20261006 D9). Named by F:D6. |
| `remove_skill` | **ask** | allow | Irreversible directory delete (C8). |
| `remove_mcp_server` | ask | ask | Unchanged. |
| secret removal (OSA-7.9) | **ask** | new | Breaks every consumer; consumers shown first (FA:FQ-13). |
| `add_mcp_server` | **ask** | allow | Arbitrary host command outside the sandbox; may enable MCP install-wide (C6). FA:FQ-2 A. |
| `configure_provider` | **ask** | allow | `api_base` can redirect every model call. FA:FQ-2 A. |
| `enable_channel`, `configure_channel`, `connect_channel` | **ask** | allow / new | Exposes an agent to outside people. |
| `connect_mailbox` | **ask** | new | Private mailbox access. |
| permission tool (provisional name `set_agent_permissions`) | **ask** | new | FA:FQ-3 A: per-agent tool-policy and connector-assignment changes on an existing agent move here; `update_agent` loses those fields. |
| `environment_setup`, `bash` | ask | ask | Unchanged. |
| `create_agent`, `update_agent` (other fields, including external-worker `cli`/`cli_path`), `update_workspace`, `create_skill`, `edit_skill`, `install_skill`, `disable_channel`, test/look-up tools | **allow** | allow | Covered by the summary question; bounded by the ceiling. `create_agent`'s requested grants exclude the management family (new agents start deny-all, C23; FA:FQ-3 A "never by default"); granting management tools later goes through the ask permission tool. External-worker selection stays `allow` by FA:FQ-12 C (R-A3). |

**Where the values live.** All `ask` values are **global-ceiling** values (Layer 1). Omnipus carries **no per-agent `ask`**, shipped or operator-set (FA:FQ-16 B): the per-agent write boundary (agent update, tools write, the new permission tool, the shared editor) refuses `ask` in Omnipus's per-agent map; per-agent `deny` stays available. God Mode floors the ceiling (C2), so in God Mode nothing asks for Omnipus (F:D7) — including `environment_setup` (FA:FQ-5 implied by FQ-16 B). This is a write-time validation like ADR-077's `ValidateSubmittedToolPolicyMap`, not a third resolution layer; the compositor is unchanged. It amends ADR-090 §2.6 ("tools user-editable") for Omnipus only. Every other built-in already persists explicit `deny` for the management tools (`adr090SparseRolePolicies`) and new custom agents start deny-all (C23), so raising those ceilings to `ask` affects no other agent. Shipped values change in `defaultToolPoliciesSysagent`; Reconcile never overwrites operator values (ADR-076 D2) — older installs keep `allow`, accepted under greenfield (F:D22). Nothing becomes `allow` that is not `allow` today.

**Auto-approve and God Mode.** New tools (`connect_channel`, `connect_mailbox`, secret removal, the permission tool) join ADR-092 D9's ask-list (additive). In God Mode Omnipus shows the summary as a message, not a question, and proceeds (F:D7). Omnipus learns the mode from a one-line note added to its per-turn context from the existing `GodModeActive(cfg)` (`pkg/agent/instance.go`); no new setting or wire field. Genuinely missing interview information and secret entry are not approvals: they still happen in God Mode (G: D7 coverage note).

**Approvals per workflow outside God Mode (FA:FQ-6 A — both confirmations kept):**

| Workflow | Question cards | Tool approvals |
|---|---|---|
| W1, no new connector/dependency | 1 | 0 |
| W1 with one connector and one dependency | 1 | 2 |
| W18, no new connector | 1 | 0 |
| W3 change agent, no permission change | 1 | 0 |
| W3 with permission change | 1 | 1 |
| W4 delete everywhere | 1 | 1 |
| W10 / W12 / W13 / W15 | 1 | 1 |

**Threat model — founder-accepted risks (Daniel Piatkowski, 2026-10-12).** The summary gate is instruction-enforced. Untrusted text reaches Omnipus through peer messages (F:D4/D5), tool results (catalogue entries, skill bodies, web pages, MCP tool lists) and pasted text. Code still stops: everything on the ask list (real arguments on the card), the ceiling, `set_config`'s blocked keys, malicious-flagged installs, and the fact that a peer gains no approval authority (ADR-20261006 D7). The founder accepted these residuals, the second and third after team-lead spelled out the zero-click scenario:

| ID | Accepted risk | Scenario | Founder answer |
|---|---|---|---|
| R-A1 | Persistent foothold through `allow` change tools | A manipulated message makes Omnipus create an attacker-written agent, skill or instruction change, or rewire delegation, without the Go question | FA:FQ-4 A, 2026-10-12 — prompt rules only; no code check |
| R-A2 | Read-to-web disclosure | A manipulated forwarded message makes Omnipus read a private file (reads are open outside the protected set, G:E03) and send its content to a website via `fetch_url` — no click | FA:FQ-11 A, 2026-10-12, after stress test — keep reads and web calls |
| R-A3 | External-program worker planting | A manipulated message makes Omnipus create (or edit) an external-program worker whose `cli_path` names a host program; a later delegation (even its version probe, C10) runs it; external permission requests bypass native approval (G:E07) — no click | FA:FQ-12 C, 2026-10-12, after stress test — keep creation `allow` |

The specification must not weaken the remaining code barriers, and user docs must state R-A1–R-A3 plainly (OSA-13). Security-lead's pass covers the complete ask surface and confirms these three are recorded, not re-decides them.

### OSA-4 — Verification inside the existing change tools

No separate verification system (F:D9). Outcomes (G:MAJ-010):

| Outcome | Meaning |
|---|---|
| **working** | Saved, and the tool's own check passed — the check's scope is stated |
| **saved but not working: reason** | Saved; the check failed |
| **partly saved** | Some parts durable (identity, revision, completed parts listed), others not saved or not verified — existing structured partial fields (e.g. `agent_update_adr090.go` persistence/stage fields, `agent.go`::`persistAndJoin` results) are kept, never collapsed |
| **failed: nothing saved** | Only when no side effect is proven |

Recovery reads the durable affected resources, not only live lists (OSA-6).

| Tool | Check — and what "working" proves |
|---|---|
| `configure_provider` | Resolves the **saved effective entry** (configured `api_base`, credential, protocol) exactly as runtime does and lists models through `pkg/providers/validate.go`::`ListModels`; keyless local providers are valid. Proves: the configured endpoint answers a model listing with this credential. Does **not** prove a chosen model executes, quota or tool support. `list_models`' catalogue-base path (C5) and `test_provider` are not used. Negative controls: catalogue succeeds/configured fails; keyless local; non-OpenAI protocol. |
| `add_mcp_server` | Unchanged live status and tool count (C6). |
| skill tools | Unchanged activation checks. |
| `update_workspace` | Read-back: graph valid, new edges' endpoints are current members. |
| `create_agent`, `update_agent`, permission tool | Existing read-back of saved configuration and joined workspace. |
| `connect_channel`, `connect_mailbox` | OSA-5. |
| `environment_setup` | Exit 0 is not readiness; "installed; readiness confirmed at the agent's first use" unless proven (ADR-090 §6.5). |

### OSA-5 — `connect_channel` and `connect_mailbox`

Names provisional (F:D11); both `ask`, both on the D9 ask-list; Settings forms stay (F:D17).

- **`connect_channel`** — configure, bind to one agent in one workspace (ADR-029), enable, and a live test, reusing the code behind `configure_channel`, `enable_channel` and the Settings binding path. The result names the **facts proven per platform** (for example: token authenticated with the platform; bot identity read; binding saved; inbound route reaches the bound agent) and lists what was **not tested** (for example: webhook delivery from the platform). Stored credentials or token authentication alone never yield "working" for routing. QR pairing (S7): the existing pairing frame (C28) gains request and session correlation through a contract change (backend-lead, generated first), so the pairing code and progress render in the requesting chat; subscription ownership, timeout, Cancel/Stop and terminal results are specification items. No hand-written QR or status payload in tool prose.
- **`connect_mailbox`** — sets an agent's mailbox in one workspace (ADR-033) via a shared function behind `pkg/gateway/rest_mailbox.go`, not an HTTP self-call. Reports read readiness (incoming-server login and folder listing) and send readiness (outgoing-server authentication) separately; it never sends a probe email.

### OSA-6 — No workflow record

No stored workflow state (F:D12). Omnipus re-reads durable configuration; unused secret handles expire; a halfway stop leaves earlier parts in place and the report says which (OSA-4). Never blindly replay creations or deletions (ADR-090 §5.2). Secret staging metadata (OSA-7) is safety metadata for one secret, not a workflow record.

### OSA-7 — A secure secret field for all agents, on the existing credential store

**User view (F:D10).** A protected input card in the chat names the asking agent, the purpose and the **effective recipient**. The value never enters the transcript, model context, a chat frame, a tool argument or a log.

```
agent ──request_secret(purpose, recipient, fields[])──► gateway (server keeps the authoritative descriptor)
   │   tool parks the turn                                   │ secret-request frame (no value)
   │                                                         ▼
   │                                            protected card → REST submit (values only here)
   │                                                         ▼
   │                         ONE store write: encrypted STAGING ENVELOPE
   │                         {request id, agent, recipient descriptor, field values, expiry, state=staged}
   ◄── resume turn with handles only (sanitized message via the existing park/resume seam)
agent ──change tool(…, *_handle)──► tool compares descriptor → ONE atomic store operation:
                                    write canonical *_ref + mark envelope consumed (same lock, one file publication)
```

1. **Reuse.** Existing encrypted credential store (`pkg/credentials/store.go`; ADR-004); canonical names unchanged (C6, C7, C19). Agents see reference names only.
2. **Why staging.** A direct write would change a live credential before Go, and Cancel could not discard it.
3. **Effective-recipient binding (G:MAJ-003).** The server-owned descriptor freezes what the card showed: provider → provider id, effective endpoint, protocol; MCP → server name, transport, command + arguments or URL, env key; channel → instance, platform, field; mailbox → agent, workspace, incoming/outgoing hosts, username; agent-private → agent id, variable name. A consuming tool compares its write against the descriptor; any difference refuses and a new request is required. Model-written purpose text is never authority. The display form is a generated contract.
4. **Atomic single use (G:MAJ-002).** Promotion writes the canonical entry and marks the envelope consumed in one store operation under the existing store lock and atomic file publication (C25 shows `Get`/`Set`/`Delete` are separate today — the store gains this one operation). A second consumer finds it consumed. Envelope states: pending → staged → consumed / cancelled / expired. Duplicate submissions of one request id are idempotent. A multi-field card stages all fields in one write (all or none); each consuming tool promotes the fields it writes, and its result reports any field left unpromoted. The boundary with the separate `config.json` write is reported truthfully (OSA-4 partly saved); no cross-resource transaction is promised.
5. **Cancel / Stop / expiry / restart.** Cancel or Stop before staging discards; staged envelopes expire (time-to-live is a specification value) and are deleted on access and at boot; a restart never resumes plaintext. The resumed turn carries handles only — no plaintext in question metadata, transcripts or resume records (contrast C24's user-role resume record).
6. **Redaction is secondary.** Staged and live values are added to the complete sensitive-value set before any probe or connector activity (C31's registration replaces the full set, so the specification appends to it); redaction is never the primary no-leak guarantee.
7. **Where the card may appear.** Web only; channel sessions are refused with "open the web app". For Omnipus, a helper may request a secret: the card renders in the Omnipus main chat, naming the acting helper, and lights its attention (FA:FQ-8). For other agents, only the owner session may raise a card in v0.1.1.
8. **Scope (FA:FQ-7 — both now).** S1 one key, S2 several secrets on one card (Slack 2, Feishu 3, LINE 2, IRC up to 3 — C19), S3 replace, S4 list names, **and agent-private secrets**: a native agent may store a secret bound to itself and a variable name; it is injected only into that agent's own sandboxed command environment (`bash`, `environment_setup`), through the existing environment handling (the v0.1.1 env-var allowlist, root `CLAUDE.md` Release Strategy). A handle binds only to destinations a tool the agent may call consumes, or to its own private variables. Later: S5 secret files, S6 connector sign-in. S7 in `connect_channel`. The agent-private kind lets the agent's own command print the value into its context — a conflict with F:D10's "never reaches the model" that this ADR cannot resolve: **FQ-18**.
9. **Removal (FA:FQ-13; G:MAJ-009).** Removing a secret (policy `ask`) first lists every consumer that references it (enabled channels, connectors, providers, mailboxes, agent-private uses). The result reports separately: durable deletion, live revocation/application, and whether configuration still boots. The founder ruled that a missing, removed or unreadable secret must never stop the gateway from starting or reloading — only that integration degrades, visibly, with the reason. That fix is **issue #1277**, which also amends ADR-004's failure semantics; this ADR amends nothing in ADR-004. Sequencing: removal of a secret referenced by an enabled consumer ships only with or after #1277; until #1277 lands, such a removal is refused with the consumer list.
10. **Agent tools stop taking plaintext secrets**; Settings forms keep plaintext entry over REST.

**Secret-card journey (G:MAJ-011) — minimum states:** request loading / unavailable; editable pending; submitting; rejected with safe retry; **submission unknown** (reconciled by request id, never by re-sending plaintext); submitted; cancelled; expired; cancelled by Stop / interrupted. Values live only in the isolated control until submission or clear — never in chat drafts or shared stores; they are cleared on terminal state, navigation or disposal; no automatic replay. The server's request state, not a click or a model sentence, owns completion.

**Contracts (Hard Constraint #8; architect shapes, backend-lead edits and regenerates):**

| Contract | Shape |
|---|---|
| Secret-request frame (WS) | request id, session id, acting agent id, root session id (for Omnipus helpers), optional workspace id, purpose text, recipient display (from the descriptor), fields[] (key, label, required), expiry. No value. |
| Secret-request resolved frame (WS) | request id, final state. |
| Snapshot | pending secret requests alongside pending approvals. |
| Submit (REST) | keyed by request id; values by field key, or cancel; response: state only. |
| Workflow start (OSA-11) | target data, not session binding (OSA-9.1). |
| Pairing progress | `whatsapp_pairing` gains request/session correlation (OSA-5). |
| Approval frame | carries acting session and root Omnipus session so the SPA can render it in the Omnipus chat (OSA-8). |

### OSA-8 — Messaging, attention and helper sessions

1. **Agents reach Omnipus with a normal message** to its address — the default-workspace pair of its ordinary main (FA:FQ-1 B; F:D4/D5; ADR-20261006 D7). No delegation, no pointer.
2. **Attention (F:D16, FA:FQ-8).** The Omnipus entry shows its main's `needs_attention`. For **Omnipus's main only**, the sources extend to pending questions, approvals and secret cards of its own helper descendants (amends ADR-20261006 D11 for this one main). A pending secret card counts as a pending question for any main.
3. **Helper sessions (F:D27; FA:FQ-15 B refined by FA:FQ-8).**

| Rule | Decision |
|---|---|
| Stays in the main | Interview, the user's secret entry for the plan, the summary question, the final report. |
| Helper work | The confirmed apply batch, **including `ask`-policy tools** (FA:FQ-15 B). |
| Helper decisions | Every question, approval and secret card from an Omnipus helper surfaces **in the Omnipus main chat**, naming the acting helper; never only in the helper chat. Questions use the existing relay (C11): the main asks on the helper's behalf. Approvals and secret cards render in the main chat through the root-session field (OSA-7 contracts) and the SPA scope change (OSA-10 U1). The main never answers for the user. |
| Depth | Omnipus's self-row in the default workspace is seeded with depth 1 (operator data), within `FreshSelfDelegationMaxDepth=3` (C16); ordinary admission/queue/Stop apply. The default workspace's graph view shows this self-row so an operator can edit or remove it; removal disables helpers visibly. |
| Concurrency | At most one apply helper per workspace (instruction). Code backstop: every resource write checks that resource's revision (C9 — a stale-revision result is `REVISION_CONFLICT` or `CONFLICT`, see C9); on conflict the helper re-reads; if the plan's meaning changed it relays a revised summary for a new Go (ADR-090 §5.2). Shared resources (a reused agent, a global skill) follow FA:FQ-14 A (OSA-9). |

### OSA-9 — Workflows W1, W18, dependencies, W8

**OSA-9.1 — Acted-on workspace versus session anchor (G:MAJ-005).** Omnipus's main and helpers are anchored in the default workspace; the workspace being configured is **target data**. Every implicitly scoped setup verb takes an explicit, validated target workspace: `create_agent` (C10), `create_skill`/`edit_skill`/`remove_skill` project-shelf resolution (C27), `environment_setup` destination, membership changes and read-backs; tools with an explicit id (`get_workspace`, `update_workspace`, `connect_*`) already have it. The main's own workspace is never changed; no silent global skill override; ordinary mount and authority checks apply. The specification inventories every verb.

- **W1 (F:D2).** Purpose → existing agent fits? (exception path: add unchanged) → capability plan → coverage (have · install · create · missing) → fixes in order → one summary → Go → apply (skills → connectors → create → assign → join → main chat) → checks → "covered / partly covered" + "Open chat". Gaps shown, never hidden.
- **W18 (F:D24–D26; FA:FQ-14 A).** Offered on workspace creation and from the empty Team tab, skippable. Tailored design (lead, 2–4 specialists, workers; default delegation lead → specialists → workers plus needed links). Reused agents are added **unchanged**, marked "reused, also works in X"; any change to a reused agent is a **separate shared-impact proposal** showing before/after and every affected workspace. The summary gives a graph preview **and a text list of agents and edges** (OSA-10 accessibility). Apply runs in a helper; the lead is placed first in `core_team`. Templates later.
- **Dependencies (F:D3).** Known needs checked up front (W1, W6, W10) from skill/task-plan guidance; installs via `environment_setup` to the target workspace; every agent keeps self-service; shared runtimes stay application-managed.
- **W8 (F:D22, F:D24).** New workspaces start with an empty team (`newWorkspaceSetupTeam` seeds no one, C17). The `setup_pending` kickoff is kept and **amended**: today it is mint-only (C17); the amended producer sends the offer into Omnipus's **existing** main with the new workspace as target data, server-initiated (the client cannot burn the flag against an unrelated session — that protection stays). Contract and producer change are backend-lead's.

### OSA-10 — Screens (F:D15–D21)

| # | Decision | Source |
|---|---|---|
| U1 | Fixed **Omnipus** entry above the workspaces opening its main without switching the active workspace (today's `useSelectSession` switches it for a foreign session — G:E37 — so a dedicated attach path is needed; unresolved attach blocks sending and shows Retry). Approvals, questions and secret cards whose root is the Omnipus main render in the Omnipus chat regardless of active workspace (C13's `isApprovalInScope` gets an Omnipus-root case); `workspace_id` is not erased and unrelated approvals stay scoped. Queue, expiry, reconnect and resolution behaviour unchanged. | F:D15, F:D23, FA:FQ-8 |
| U2 | Existing attention marker on the entry (OSA-8.2). | F:D16 |
| U3 | "Add agent" opens the Omnipus chat with W1 started; Settings forms stay. | F:D17 |
| U4 | Manual creation UI removed (`CreateAgentWizard`, `CreateAgentModal`, `wizard/*`); shared editor stays. REST `createAgent` is **kept**, documented as an API without a UI caller (FA:FQ-10 A — explicit founder exception to F:D18's "only Omnipus creates agents", which governs the UI). | F:D18 |
| U5 | Agents screen removed with every reference: `AgentListScreen.tsx`, `src/routes/_app/agents.tsx`, `agents.index.tsx`, `agents.$agentId.tsx`, menu items, links (Grep matched 30+ SPA files for `/agents`, API paths included); `/admin/chat` stub removed. No forwarding. | F:D19, F:D23 |
| U6 | Team: list first, graph second; cards (avatar, name, conversation agent or worker, model, other workspaces; Edit · Remove from this workspace · Delete everywhere with impact list); workers separate incl. external-program workers; "Add agent": *Create with Omnipus* · *Add an existing agent*. Omnipus never appears. | F:D20, FA:FQ-1 |
| U7 | Add-existing picker lists every agent except Omnipus, marking "not in any workspace". | F:D21 |
| U8 | Workspace entry with nothing remembered: first conversation agent in `core_team` order (Omnipus excluded); with none, the existing empty state whose Team path carries the D24 button. | F:D22, BRD:§9 O1 A |
| U9 | **Settings → "System agents"** section: Judge and Plan Supervisor (editable instructions, ADR-090 §2.6) and Omnipus (model and its editable fields) through the shared editor, plus a settings shortcut in the Omnipus chat header. Omnipus's model stays fixable without a working model. No "default agent" control (OSA-12). Judge/Planner tuning redesign stays out of scope (F:D1); this only relocates existing controls. | FA:FQ-9 A |

**Accessibility (G:MAJ-012):** the secret card has visible labels per field naming agent, purpose and recipient; field-error association; announced submitting/unknown/expiry states; safe focus placement and return to the composer; no Go auto-selection. Team card actions work by keyboard focus and touch, not hover only. W18's graph has the text equivalent above. The specification's accessibility acceptance verifies these.

**Design system (G:OBS-001):** the specification maps the secret card and Team list to catalogued components (Field, Input, Label, FormError, Button, ConfirmDialog, EmptyState, QueryErrorState, Tabs, ViewSwitch, AgentIcon — G:E27); a genuinely new reusable composite follows the four-part publication contract; dark-first, chat-first brand.

### OSA-11 — Buttons name the workflow

A button sends a normal user message into Omnipus's main with optional workflow-start metadata: workflow id plus **target** workspace (OSA-9.1) — target data, never the session's workspace binding. The server renders it as a fixed instruction ("Start workflow W1 for workspace <id>").

### OSA-12 — Omnipus is the default agent (FA:Default)

Omnipus is the fixed fallback for anything not addressed to an agent: no user setting, no Mia seed (`config.Agents.Defaults.DefaultAgentID` is no longer seeded, C29; the "set default agent" action leaves with the Agents screen). Each `GetDefaultAgent` call site (C29), decided:

| Call site | Kind | Decision |
|---|---|---|
| `pkg/gateway/websocket_chat.go`::`resolveTargetAgent` | Web chat frame with no agent | → Omnipus, attached to its one main (never a second Omnipus chat) |
| `pkg/gateway/rest_sessions.go`::`createSessionHTTP` | Session create with no agent | → Omnipus's main, not a new session |
| `pkg/agent/loop_inbound.go`::`resolveMessageRoute` (route agent missing) and `pkg/routing/route.go` Priority 7 / deleted-binding fallback | **Outside channel traffic** | **Open — FQ-17.** Routing outside people to the agent holding management tools is a contradiction this ADR will not settle silently |
| `pkg/agent/loop_inbound.go`::`resolveSystemMessageAgent` | System message with no origin | → Omnipus |
| `pkg/agent/goal_triggers.go`::`resolveGoalAgent`; `pkg/agent/task_executor.go`::`processTaskDirect`; `pkg/agent/steering.go`::`agentForSession` | Work **addressed to an agent that no longer resolves** | Refuse visibly (stale owner), do not re-route — this is addressed work, not unaddressed, and moving it to Omnipus would run another agent's goal/task with management tools (ADR-054 D7 fail-closed spirit) |
| `pkg/agent/steering.go`::`enqueueSteeringItemWithStatus` (empty `AgentID`) | Unaddressed internal item | → Omnipus |
| `pkg/agent/loop_publish.go`::`publishResponseIfNeeded` (nil agent) | Sender identity for an outgoing reply | → Omnipus (unchanged mechanism) |
| `pkg/agent/loop_construct.go`::`initializeCore`; `pkg/agent/loop_mcp.go`::`MCPWorkspacePath`; `pkg/agent/loop.go`::`extractProvider`; `pkg/agent/loop_accessors.go`::`GetStartupInfo`, `ListSkillsDetailed` | Internal look-ups needing "some agent" | Keep calling the default (Omnipus); their answers do not depend on the agent's grants. MCP servers' working directory becomes Omnipus's home (neutral) |
| `pkg/gateway/rest_tools.go`::`HandleTools`; `pkg/gateway/rest_tool_registry.go`::`HandleToolsRegistry` fallback | Tool listings | Must not present Omnipus's grant set as "the tools": re-point to the central builtin registry (already preferred by `HandleToolsRegistry`) |

`ChannelRouting.default_agent_id` (per-channel agent) is unchanged; its "empty → global default" meaning depends on FQ-17.

### OSA-13 — Release, documentation map and proof hand-off

**Release:** v0.1.1 (founder R1 A, relayed).

**User-facing documentation — change-to-page TODO map (G:MAJ-013).** Drafted by the implementing lead in the same change, audited by docs-verifier.

| Page | TODO |
|---|---|
| `docs/tools.md` | Replace "How to change an agent's tool access" (starts from the Agents screen) with Team card → Edit, and Omnipus's permission workflow; ask list; Omnipus has no per-agent Ask (Deny only); God Mode for Omnipus |
| `docs/security.md` | State R-A1, R-A2 (read-to-web disclosure), R-A3 (external-program workers) with the 2026-10-12 founder acceptance; secret field guarantees and limits (incl. FQ-18 outcome); ask list; secret removal and #1277 behaviour |
| `docs/agents.md` | Omnipus replaces Ava and Admin; agents are created only through Omnipus; Team as agent home; external-program workers and R-A3; Omnipus is the default agent |
| `docs/workspaces.md` | Empty new workspace, W8 offer, W18 team setup, reuse unchanged + shared-impact proposals, Team list/graph |
| `docs/using-omnipus-ui.md` | Omnipus sidebar entry, attention, cards from helpers in the Omnipus chat, workspace entry rule, no Agents screen, Settings → System agents |
| `docs/getting-started.md`, `docs/tutorial.md` | First agent via Omnipus; provider first in onboarding/Settings |
| `docs/concepts.md` | Omnipus system agent, workflows, default agent |
| `docs/skills.md` | Skills created/installed through Omnipus; project shelf per target workspace |
| `docs/connectors.md`, `docs/connectors/telegram.md` | `connect_channel`, what "working" proves and does not; QR pairing in chat |
| `docs/mail.md` | `connect_mailbox`; read vs send readiness |
| `docs/providers-and-models.md` | Provider check semantics; Omnipus's model in Settings → System agents |
| `docs/settings.md` | System agents section; no default-agent setting |
| `docs/reference/built-in-tools.md` | New tools, policies, Auto classification |
| Mentions sweep | `docs/README.md`, `docs/troubleshooting.md`, `docs/plans.md`, `docs/goals.md`, `docs/browser.md`, `docs/using-omnipus-cli.md`, `docs/operations/observability.md`, `docs/connectors/google-chat.md` |

**Proof hand-off (G:OBS-002):** the grill's "Test coverage assessment" table (approval/policy, injection, confirmation, secret lifecycle, secret removal, provider verification, channel/mail, scope/concurrency, frontend/accessibility, regression, acceptance authority) is carried into the specification as mandatory negative controls; "code correct and tested" and "reachable" stay separate claims.

---

## BRD points superseded by the founder record

| # | BRD point | Superseded by |
|---|---|---|
| 1 | Release "v0.3" | v0.1.1 |
| 2 | §4.1 one Add-agent search box, "Create new" last | F:D2, F:D17, F:D20 |
| 3 | §4.2 Omnipus side panel | F:D15, F:D17 |
| 4 | §4.2 [Create] / [Adjust in form] | F:D6 summary + question; F:D18 |
| 5 | §4.2 / §8 S5 / §11 D3 blank-agent fallback | F:D18 |
| 6 | §4.3 delete in the edit form | F:D20 Team card action |
| 7 | §5 / §11 D6 workspace-scoped side panel | F:D15 |
| 8 | §6 / §11 D7 "enforced by tools" | Revised F:D6; FA:FQ-4 A; F:D12 |
| 9 | §6 context buttons for every workflow; Omnipus replaces Settings dialogs | F:D17 |
| 10 | §8 S1 always ask, even with auto-approve | F:D6, F:D7 |
| 11 | §8 S3 per-workflow tool loading | F:D8 skills on demand |
| 12 | §9 O5 delegate to Omnipus | F:D4, F:D5 |
| 13 | §10 "lands in Ava's main" | F:D22; OSA-10 U8 |
| 14 | §9 O4 default agent setting moves to Settings | FA:Default — no setting; Omnipus is the default |

---

## Earlier decisions amended (dated 2026-10-12, effective on acceptance)

| ADR (by title) | Clause | Amendment |
|---|---|---|
| ADR-090 — Built-in agent configuration, skills, and visual file reading | Ava/Admin roster rows, §2.1/§2.2, §3 edges, §4 rows, §5 columns and "Ava confirms once", §6.1, §6.2, §6.5 Admin target | Replaced by Omnipus. §5.2's one-question confirmation survives as F:D6's gate. **§2.6 "tools user-editable"** is narrowed for Omnipus: per-agent `ask` not allowed, per-agent `deny` allowed (FA:FQ-16 B). §6.5 "both levels Ask" for `environment_setup` does not apply to Omnipus (FA:FQ-5 via FQ-16). |
| ADR-092 — Shell permission modes | D9 ask-list | Additive: `connect_channel`, `connect_mailbox`, secret removal, permission tool. |
| ADR-20261007 — Agent-first navigation and agent identity | Welcome agent = Ava; First-visit row; Admin exception; Agents in Assets | Welcome = first conversation agent (Omnipus excluded), else empty state; Admin row → fixed Omnipus entry; Agents removed. |
| ADR-20261006 — Session core with an agent address book | D7 Admin default-workspace exception; D4 Admin restrictions; **D11 attention sources** | Admin exceptions deleted (Omnipus is an ordinary hidden member). D11: for Omnipus's main only, helper descendants' pending decisions count; a pending secret card counts for any main. |
| ADR-077 — Two-layer tool-policy model | none to the model | Only shipped ceiling values change, plus a write-time refusal of per-agent `ask` for Omnipus (validation, not resolution). |
| ADR-004 — Credential boot contract | none | Not amended here; issue #1277 owns the failure-semantics change. |

---

## Consequences

### Positive
- One technical partner (BRD:§1 P4); one fixed default agent.
- No secret in chat text or tool arguments; recipient-bound, single-use handles.
- "Working" means a stated check passed; partial saves are reported, not hidden.
- Agent creation lands in a workspace with a capability plan; fewer screens.
- Two-layer policy model intact; no new approval, verification or workflow system.

### Negative / accepted
- R-A1, R-A2, R-A3 (founder-accepted 2026-10-12).
- Two confirmations on single-change workflows (FA:FQ-6 A).
- Creating an agent needs a working model (F:D18).
- Peer requests queue behind an interview in the one Omnipus conversation (ADR-20261006 D3).
- One model turn per new workspace for the W8 offer.
- Omnipus's per-agent editor loses Ask (FA:FQ-16 B).
- Older developer installs keep `allow` on raised tools and stale `ava`/`admin` entries (greenfield).

### Neutral
- Judge and Plan Supervisor unchanged except their editor home (U9).
- Settings forms and first-provider onboarding unchanged.

### Removed — retired surfaces to add to root `CLAUDE.md`
| Surface | Note |
|---|---|
| Agents screen, `/agents` routes incl. `/agents/$agentId`, links | F:D19; add a `scripts/check-*.sh` guard |
| Manual create UI (`CreateAgentWizard`, `CreateAgentModal`, `wizard/*`) | F:D18 (REST create kept, FA:FQ-10 A) |
| Ava (seed, prompt, skills, chats, workspace seed, SPA welcome entry) | F:D22 |
| Admin (seed, prompt, skills, row, `/admin/chat`, identity branches) | F:D23 |
| User-settable default agent (`DefaultAgentID` seed, "set default agent") | FA:Default |
| Plaintext secret arguments on agent configuration tools | OSA-7.10 |

### Migration
None (F:D22).

### Risks
| Risk | Mitigation |
|---|---|
| R-A1–R-A3 | Accepted; documented (OSA-13); remaining code barriers kept |
| Gateway stops when an in-use secret is removed | Issue #1277 prerequisite; refusal until it lands (OSA-7.9) |
| Outside channel traffic reaching Omnipus as default | FQ-17 |
| Agent-private secret printed into the model | FQ-18 |
| Parallel helpers on one workspace / shared agent | One helper per workspace; per-resource revision; FA:FQ-14 A |
| No model → no agent creation | Accepted; Settings → System agents keeps Omnipus's model fixable |

---

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep Ava and Admin with routing | F:D8 |
| Workflow engine / wizard per workflow | F:D8, F:D12 |
| Combined apply tool with its own approval view | WF:§3 "Dropped" |
| Per-agent `ask` for Omnipus's risky tools | Survives God Mode (C2); now also forbidden (FA:FQ-16 B) |
| Delegating technical work to Omnipus | F:D4 |
| Direct write into canonical `*_ref` on entry | Changes live credentials before Go |
| A new secrets vault | Store and `*_ref` exist (C19) |
| Provenance rule turning `allow` into `ask` | Third policy layer (Hard Constraint #6) |
| M-B confirmation check in change tools | Infeasible as described (C24, G:MAJ-001); founder chose prompt rules only (FA:FQ-4 A) |
| Off-team Omnipus with a self-row exception | FA:FQ-1 B |
| Main-only execution of ask tools | FA:FQ-15 B |
| Root/global workspace for Omnipus | Founder considered and rejected (FA:FQ-1 notes): touches every workspace enumerator |

---

## Affected components (Grep/Read-derived, not a GitNexus impact run)

| Area | Components |
|---|---|
| Seeds and prompts | `pkg/coreagent/core.go`, `seed.go` (default agent, Omnipus membership/self-row), `seed_system.go`, `prompts_adr090.go`, `role_policies_adr090.go`, `team_eligibility.go` |
| Policy | `pkg/config/defaults.go`::`defaultToolPoliciesSysagent`; `pkg/tools/auto_approve.go` + golden test; per-agent write validation for Omnipus |
| Change tools | `pkg/sysagent/tools/agent.go` (target workspace; permission fields split out), `agent_update_adr090.go`, `provider.go`, `mcp.go`, `channel.go`, `skill.go`, `skill_authoring.go` (target shelf), `workspace.go`; new permission tool, `connect_channel`, `connect_mailbox`, `request_secret`, secret removal/listing |
| Secrets | `pkg/credentials/store.go` (atomic promote-and-consume), gateway submit + frames, sensitive-value registration, agent-private injection into sandboxed commands |
| Default agent | `pkg/agent/registry.go`::`GetDefaultAgent` and the call sites in OSA-12; `pkg/routing/route.go` (FQ-17) |
| Identity branches | `pkg/gateway/main_session.go`, `websocket_chat.go`, `rest_workspaces.go`, `rest_workspace_delegation.go`; `pkg/agent/workspace_reroot.go`, `loop_env.go`; `pkg/sysagent/tools/workspace.go` |
| Contracts | `contracts/asyncapi.yaml`, `contracts/openapi.yaml`, `contracts/components/schemas/` (backend-lead) |
| SPA | Sidebar entry and attach path, `src/store/toolApproval.ts`::`isApprovalInScope`, secret card, Team list, Settings → System agents, removals, `src/lib/nav/workspaceEntry.ts`, `src/hooks/useWorkspaceSetupKickoff.ts` |
| Skills / docs | Omnipus workflow skills; pages in OSA-13 |

---

## Questions for the founder (after correction)

FQ-1 … FQ-16 and the default-agent question are answered (founder answers file). Two new points arise from those answers and are not decided here.

**FQ-17 — Outside channel messages and Omnipus as the default agent.** Context: you made Omnipus the fixed default for anything not addressed to an agent. In the code, inbound channel messages fall back to the default agent when a channel instance has no binding, or when its binding names an agent that no longer exists (`pkg/routing/route.go` "Priority 7: Default agent"; `ChannelRouting.default_agent_id` empty = "fall back to the global default agent"). With Omnipus as default, outside people on such a channel would talk directly to the agent that holds the management tools — and on a channel there is no question card, so the Go would be a typed word anyone in that chat could send. Impact: decides whether outside people can ever reach Omnipus.
- **A** — Outside channel traffic never falls back to Omnipus: an unbound or broken-binding channel drops the message loudly and shows the reason on the channel's status; `connect_channel` always binds, so normal setups are unaffected.
- **B** — Fall back to Omnipus as decided, and accept that outside people can address it (a fourth accepted risk, documented).
- **C** — Fall back to Omnipus but only for read-only answers; Omnipus refuses all changes in channel-origin turns (instruction only).
- **Recommendation: A** — channels always name their agent in the new design, so the fallback is rare, and A keeps the rare case from becoming an outside door to the system agent.

**FQ-18 — Agent-private secrets and "the value never reaches the model".** Context: you chose agent-private secrets now (FQ-7 both now): a secret is injected as an environment variable into that agent's own sandboxed commands. F:D10 promises the value never enters chat history or the model. An agent's own command can print the variable, and the output then reaches the model; the existing output masking only replaces the exact value and is best-effort. Impact: decides what the secret field promises for agent-private secrets.
- **A** — Ship it; state the narrower promise for this kind: never typed into chat, never in tool arguments, masked in output on a best-effort basis; a command can still reveal it. Document it in `docs/security.md`.
- **B** — Ship it only for `environment_setup` runs (installs), not ordinary `bash` commands.
- **C** — Defer agent-private secrets to a later release.
- **Recommendation: A**, with the limit stated plainly.

---

## Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| Founder decisions D1–D27 | Read the decision record | Verified |
| Founder answers FQ-1…FQ-16, default agent | Read `omnipus-adr-founder-answers-20261012.md` | Verified |
| Grill findings and evidence | Read the review at `cdab144f0` in full | Verified |
| C1–C22 | Read/Grep at `5054b1455` (draft round); unchanged production tree at `cdab144f0` (`git log` shows only review docs added) | Verified |
| C5 `list_models` uses catalogue base; `ListModels` exists | Grep `provider.go` (`providers.APIBaseFor`, `apiKey == ""`); `pkg/providers/validate.go`::`ListModels` | Verified |
| C9 conflict literals | Grep `"CONFLICT"` in `agent_update_adr090.go`, `skill.go` | Verified |
| C10 `cli_path` resolution and probe | Grep `resolveCLIBinary`, `detectCLIVersion` | Verified (symbols exist; behaviour per G:E05–E07) |
| C13 SPA scope filter | Grep `src/store/toolApproval.ts`::`isApprovalInScope` | Verified (symbol and comment) |
| C17 kickoff mint-only | Read `pkg/gateway/websocket_chat.go` comment "A kickoff frame is MINT-ONLY" | Verified |
| C23 custom seed deny-all | Read `pkg/coreagent/seed.go`::`NewCustomAgentToolsCfg` (`denyAllThenOverride`) | Verified |
| C24 registry submit / default-safe | Grep `pkg/askuser/registry.go`::`Submit`, `fireDefaultSafeSet` | Verified (symbols); behaviour per G:E08 |
| C25 separate store ops | Grep `pkg/credentials/store.go` `Get`/`Set`/`Delete` | Verified |
| C26 boot fatal + #1277 | Grep `buildEnabledRefMap`, `bootCredentials`, `deleteCredential`; `gh issue view 1277` → OPEN, title quoted | Verified |
| C27 shelf from `ToolWorkspaceID` | Grep `skill_authoring.go` | Verified |
| C28 pairing frame fields | Read `contracts/asyncapi.yaml` `whatsapp_pairing` (required `channel_id`, `status`) | Verified |
| C29 16 `GetDefaultAgent` call sites; Mia seed | Grep `GetDefaultAgent()` in `pkg/` (16 non-comment production calls); `seed.go` `DefaultAgentID = string(IDMia)` | Verified |
| C30 channel fallback to default | Read `pkg/routing/route.go` Priority 7 and fallback comments; `ChannelRouting.yaml` | Verified |
| C31 sensitive registration replaces set | Per G:E11; symbol not re-read this round | Inferred (grill-verified) |
| God Mode not in model context | Grep of `pkg/agent` | Inferred (absence) |
| Env-var allowlist for agent-private injection | Root `CLAUDE.md` Release Strategy names it; implementing symbol not located | Unknown (spec locates it) |
| Release v0.1.1 | Dispatch brief | Unknown first-hand (relayed) |
