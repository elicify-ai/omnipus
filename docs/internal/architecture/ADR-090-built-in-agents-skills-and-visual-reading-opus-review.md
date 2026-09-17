# Independent Claude Code Opus review — ADR-090 and specifications

Executed 2026-09-17 using Claude Code 2.1.274 with `--model opus`, high effort, and only Read/Glob/Grep tools. CLI result: success, no permission denials; model usage identifies `claude-opus-5` (and the CLI's `claude-haiku-4-5-20251001` auxiliary usage). Duration 629,904 ms, 86 turns. This is source/document review, not tests or runtime execution.

The original review below is retained verbatim as the findings record. Its “all findings open” ending describes that review snapshot; author corrections and final closure are recorded in the [disposition report](ADR-090-built-in-agents-skills-and-visual-reading-opus-dispositions.md). Two suggested founder questions are resolved from already-confirmed requirements: Admin installation ships Allow as the accepted matrix states, with explicit other-role restrictions; connector execution requires assignment, consistent with the configured-assignment model. The separate, verified document-skill license choice L1 remains pending and must not be conflated with those engineering clarifications.

---

# ADR-090 review: built-in agents, skills, and visual file reading

I checked the ADR and both specs against the source at `3463b2d36`. I only read code: no tests, shell or runtime. I found 2 critical, 11 major and 12 minor issues, plus 3 observations.

## Verdicts

| Document | Verdict | Why |
|---|---|---|
| ADR-090 | **BLOCK** | Its connector-exclusion design relies on a per-agent MCP binding that nothing enforces at runtime (C-2). Its same-role helper and fork routes contradict an existing hard ban on self-delegation (C-1). Admin's identity and type aren't defined, and the global ceiling blocks Admin's install route (M-1, M-2). |
| Agent configuration and skills spec | **BLOCK** | Same two critical issues, plus undefined concurrency and soul-file behaviour (M-4), sparse vs complete policy maps (M-5), 15 unspecified role skills (M-8), and the Ava confirmation route (M-3). |
| Visual file reading spec | **REVISE** | No critical issues. Replay and revocation are underspecified and overbuilt (M-10). The Judge's per-task evidence scope doesn't exist (M-6). The provider adapter inventory is incomplete (m-7). |

## The 37 upfront tools

I counted them myself rather than trusting the documents. The ADR §5.4 table and the spec FR-008 list are the same **37 unique names**. Each one is in `pkg/coreagent/seed.go::allStaticToolNames` and has a real registration: `get_workspace` is a sysagent tool, and the other 36 come from `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata`. The spec also plans to update `previewedLazyToolNames` and `fullManifestToolNames`. `ToolSearch` stays in its separate infrastructure list, as the ADR allows.

## Findings table

| ID | Severity | Lens | Document / section | Source evidence | Failure | Smallest correction |
|---|---|---|---|---|---|---|
| **C-1** | Critical | Incorrectness | ADR §2.3, §3 ("Jim → self", "GP → GP helpers"), §8. Config spec FR-006 ("follows canonical permitted edge rules"), FR-009, D18, D25, BDD-15 | `pkg/agent/loop_delegation.go::buildDelegationDenyChecker` always refuses a delegate call to itself ("self-delegation is never permitted"). `pkg/gateway/rest_workspace_delegation.go` rejects self-edges in the graph. | The rules the spec cites forbid exactly what it requires. As written, General Purpose can never create a helper, Jim can never fork, and D18/D25 can't pass. An implementer would have to loosen a security guard with no stated limits. | State the mechanism. For example: allow `delegate` to self only through an explicit self-edge in the workspace graph, with the existing modes and depth cap. Exempt self-edges from cycle detection and seed them for Jim and GP. List the guard tests to replace. |
| **C-2** | Critical | Insecurity / Incorrectness | ADR §5 ("existing explicit empty/disabled binding form"), §5.1 Connectors. Config spec FR-003 (`mcp_servers`, binding `tools`, "runtime selection MUST honor it"), FR-005, D06, BDD-04 | `pkg/agent/loop_mcp.go` registers every MCP tool into every agent (`registry.ListAgentIDs()` loop). `pkg/agent/instance.go::agentToolsCfgToPolicy` never reads `Tools.MCP.Servers`. `config.AgentMCPServerBinding` treats an empty list and `["*"]` both as "all tools", and `omitempty` makes `[]` and a missing list look the same. | Per-agent connector assignment is stored but never enforced. Ava "removes" a connector, the readback confirms it, and the agent still calls the tool. Every agent can reach every installed connector. | Add a requirement that runtime tool registration or policy follows the bindings. Define what an omitted binding means for built-in and custom agents (see founder decision 2). Add an integration test: after an unbind, calling the connector tool is refused. Alternatively, define assignment as per-agent `mcp_<server>_*` policy overrides, which is the mechanism that works today. |
| **M-1** | Major | Incompleteness | ADR §2.2 ("Admin (system, but chat-able)"), §2.3, §10.1. Config spec FR-001 ("implement §§2–4 exactly") | `pkg/coreagent/core.go` has no Admin and no General Purpose ID; the roster is Mia/Jim/Ava/Ray/Worker/Planner/Explorer/Researcher. `config.AgentConfig::IsChatTarget` returns `!IsWorker() && !IsSystem()`. `coreagent.init` panics if a base agent has no compiled prompt. Explorer is seeded but the ADR never mentions it. | A system-type Admin can never be chatted with. Two engineers would choose different IDs and types. Nothing says whether Explorer stays, or whether General Purpose is `worker` renamed. | Add a roster identity table (ID, persisted type, wire type, chat target yes/no, locked). Keep "no existing type changes" by making Admin `core`, General Purpose `worker` with its display name changed, and Explorer explicitly not seeded. |
| **M-2** | Major | Incorrectness / reachability | ADR §5 matrix (Admin connector installation: **Allow**), §5.2 ("global restrictions remain authoritative") | `pkg/config/defaults.go` sets `"add_mcp_server": "deny"` in the global ceiling, with a written anti-injection reason. | Under strictest-wins, Admin's local Allow can't beat a global Deny. Admin's main job is unreachable on a fresh install. Neither document changes the ceiling. | State the shipped ceiling for `add_mcp_server` and the per-role denies it requires (founder decision 1). Add a fresh-install reachability test for Admin. |
| **M-3** | Major | Incompleteness | ADR §3 (Mia → Ava, Jim → Ava handover), §5.2. Config spec FR-007 ("exactly one `AskUserQuestion`") | `pkg/tools/ask_user_question.go::Execute` rejects any call from a delegated session ("owner-session-only"). | If Ava is reached through delegate or send_message, she can't ask the one confirmation question. The workflow then stalls or gets rebuilt around `message_parent`. | Require the route to Ava to be `switch_agent`, so the user owns the session. Define what Ava does when invoked in a delegated session (return the proposal and make no writes). Add a BDD scenario and a dataset row. |
| **M-4** | Major | Inconsistency / concurrency | Config spec FR-002 ("zero … instruction-file writes"), FR-007 (token covers entity plus instructions, compared under lock), BDD-09, D13 | `pkg/gateway/rest_agents_update.go::persistAndReload` writes `SOUL.md` after the entity-store lock is released. `updateRecord` checks `updated_at` only when the caller sends it, and Settings autosave uses `updated_at`. | The soul edit can't be compared and written under the same lock. If the entity save succeeds and the soul write fails, the request returns 500 with the entity already changed. The spec adds an opaque token but doesn't say whether it replaces `updated_at` or is required on REST. | Specify one token (replace `updated_at`, or define how the two relate). Make it required on every mutation. Move the soul compare and write into the same critical section as the entity update, and define the write order when one of them fails. |
| **M-5** | Major | Ambiguity / inconsistency | ADR §5 ("keep overrides sparse", "omission alone is not denial", "—" = not granted). Config spec FR-001, FR-003 ("empty sparse override map"), FR-008, D17 | `pkg/coreagent/seed.go`: most roles are seeded with `denyAllThenOverride` (an explicit entry for every tool). The dedicated tools endpoint (`restAPIUpdateAgentTools.normalizeBuiltinPolicies` → `config.ValidateSubmittedToolPolicyMap`) rejects any map missing a catalog tool. Shipped ceilings include `set_config`, `configure_provider`, `send_email` and `browser_evaluate` at allow. | One engineer keeps full deny maps, so new tools such as `get_agent` are silently denied everywhere and "inherit the ceiling" is almost never reachable. Another builds sparse maps, so every "—" cell sitting over an allow ceiling is granted unless it gets an explicit deny. The dedicated endpoint then turns a sparse agent back into a full map. | Decide the seed shape. Say whether every "—" over a non-deny ceiling needs an explicit deny. Add a test that computes each role's effective policy for every catalog tool and compares it with the ADR matrix. |
| **M-6** | Major | Incorrectness | ADR §2.4 ("relevant output files"). Config spec FR-009 ("no … unrelated task evidence"). Visual spec US-2, B10 ("Judge requests unrelated task image → refusal"), "grants no new permissions" | Only `inspect_session` is session-scoped (`pkg/tools/base.go::VerifierSessionScopeAllows`). The Judge's `read_file`, `list_directory` and `grep` are rooted at the reviewed work's whole workspace (`pkg/workspace/find_for_agent.go`, verifier reroot). No per-task file scope exists. | B10 can't pass without new scoping machinery, but the spec calls this existing behaviour. FR-009's "only" also leaves unclear whether the Judge keeps `list_directory`, `grep`, `ToolSearch` and `Skill`. | Either say the evidence scope is the reviewed workspace (and reword B10 to cover paths outside it), or specify per-task scoping from declared task files. List the Judge's exact tool set. Note in ADR §14 that this supersedes ADR-084's JUDGE-FR-059 empty skill allowlist. |
| **M-7** | Major | Inconsistency | ADR §6.1 (`define-goal` row excludes Plan Supervisor). Config spec FR-009 ("retains its fixed assignments") | `pkg/coreagent/seed_system.go::systemAgentSkills` pins `plan` and `define-goal`, and `PlanSupervisorDefaultRubric` has a rule that it must not drift from `define-goal`. | Following the ADR table removes `define-goal`, which breaks the Supervisor's criteria-authoring rule. "Retains" points the other way, so the two documents disagree. | Add Plan Supervisor to the `define-goal` row. |
| **M-8** | Major | Incompleteness | ADR §6.1, §6.2, §10.4. Config spec FR-009, FR-010 | `pkg/skills/embedded/` holds only `daily-briefing`, `define-goal`, `plan`, `skill-authoring` and `summarize`. | 15 new role skills have no content contract, packaging location, acceptance criteria or tests: interview, handoff, orchestrate, deep-research, agent-authoring, tool-mapping, skill-mapping, delegation-graph, workspace-team, mcp-install, provider-setup, channel-setup, doctor, verify, inbox-triage. Only the four document skills have a requirement (FR-010). The ADR's own rule is that a skill file alone doesn't prove delivery. Nothing checks that prompts and skills name real tools the role is allowed to use. | Add an FR for each role skill: required sections, embedded location, and how `interview` selects one of its four checklists. Add a lint test: every tool name in a prompt or skill is in the catalog and is not denied for at least one assigned role. |
| **M-9** | Major | Inconsistency / infeasibility | ADR §6.5. Config spec FR-010, FR-011, D20 | CLAUDE.md Hard Constraint #1 ("No new runtime deps"). `pkg/sandbox/hardened_exec.go`: Landlock limits file access and the npm cache is per agent. | Python, Node and converter dependencies need a recorded exception to the constraint. There is no shared install location, so Admin can install into a place Mia's or GP's sandbox can't see and D20 fails forever. | Record the exception in ADR §14 (external prerequisites, not bundled). Specify one install prefix that every agent sandbox can read, how PATH is set, and the exact probe command. |
| **M-10** | Major | Ambiguity / overcomplexity | Visual spec FR-005, FR-012, BDD-06, BDD-15, B13, B14, implementation design items 2, 3 and 8 | `pkg/agent/loop_media.go::attachToolResultMedia` stores data URLs in `msg.Media`, which is saved to session history. Workspace files have no grant object that could be "revoked" (mount grants are the only one). | Nothing defines "current scope", which component rechecks it on replay, or what happens to base64 images already in history. The spec also adds snapshot identity, retention and re-normalization across turns, while the ADR asks to reuse existing media handling. | Don't store image bytes from inspection reads in history. Store a text marker (path, snapshot hash, dimensions); on replay show "not retained — re-read to view", which US-5 acceptance scenario 3 already allows. That removes revocation checks and snapshot retention. Keep per-candidate normalization inside a single request. |
| **M-11** | Major | Incompleteness | ADR §10 acceptance table. Config spec TDD plan and datasets | (Coverage gap.) | There is no test for C-1, C-2, M-2 or M-3 (self-delegation limits, MCP unbinding enforced at runtime, Admin reachability under the shipped ceiling, Ava confirmation route), nor for M-4's soul/entity race or the M-8 prompt lint. Every one of these could pass CI while the feature is broken. | Add the BDD scenarios and datasets named in the fixes above. |
| m-1 | Minor | Inconsistency | Config spec FR-002 (REST 400 for malformed input) | `rest_tools.go` returns 422 for a bad policy value or an unconfigured MCP server; `rest_agents_update.go` returns 422 for an empty name. | Existing tests expect 422. | Say whether 422 stays or moves to 400. |
| m-2 | Minor | Incorrectness | ADR §5 ("Send … is Ask"). Config spec FR-008 ("retains Ask") | Ceiling has `send_email` and `reply` at allow. | Nothing currently makes sending Ask, so "retains" is wrong. "Channels" could also be read as `send_message`, which would break "send back". | Name the tools (`send_email`, `reply`) and say an explicit Ask override is added. Exclude `send_message`. |
| m-3 | Minor | Incompleteness | Config spec FR-002 field matrix | `memory_enabled` is accepted on the Judge by PUT but reset to false at every boot (`seedSystemAgents`). `timeout_seconds` and `rate_limits` are silently dropped today. | `memory_enabled`, `default`, `voice`, heartbeat, `executor`, `timeout_seconds` and `rate_limits` aren't classified, which conflicts with "no silently dropped fields" and "survives restart". | Add rows: reject `memory_enabled` for hidden agents; reject or define the dropped fields. |
| m-4 | Minor | Ambiguity | ADR §5.2. Config spec FR-007 | Ceiling `delete_agent` is ask; Ava's seed has `delete_agent`, `install_skill` and `create_skill` at ask. | A confirmed proposal that includes a deletion or install still triggers a second prompt. FR-007 lists only five tools. | Name the intended default for delete, install and remove. |
| m-5 | Minor | Incompleteness | Config spec FR-008 and regression table | `manifest.go`: the previewed tier is pinned by `TestVisibility_PreviewedSetIsExactlyNine` and `TestVisibility_TierArithmetic`. | The fate of the previewed tier (only `serve_web` would be left) and the uncompressed-manifest mode aren't stated, and the pinned tests aren't in the regression list. | State both and add those tests. |
| m-6 | Minor | Inconsistency | ADR §5 (Jim: no bash) | CLAUDE.md Constraint #6 says "Jim's seed grants `bash: allow`". | The repo rules become stale. | Update CLAUDE.md in the same change. |
| m-7 | Minor | Incompleteness | Visual spec C1 ("four serializer families") | `pkg/providers/bedrock/provider_bedrock.go::convertMessages` also drops tool-result images (behind the `bedrock` build tag). The Codex, Copilot and Claude CLI providers can't take images. | The adapter scope is implicit. | List every adapter as in scope, out of scope, or treated as non-vision. |
| m-8 | Minor | Overcomplexity | Visual spec B15, B16, design item 2 | `ReadFileTool.Execute` already handles non-seekable sources. | Cancellable, non-seekable acquisition is more machinery than local image files need. | Refuse non-regular files in the image branch before reading. |
| m-9 | Minor | Incompleteness | Config spec FR-005, SC-004 | `persistAndReload` returns 500 when the save works but activation fails. | The REST shape for "saved but not active" is undefined; only the tool result is. | Define the REST response in OpenAPI. |
| m-10 | Minor | Ambiguity | Config spec FR-004 (management scope permission) | — | "Existing effective permission to author or configure" doesn't name a tool. | Name the gating tool(s). |
| m-11 | Minor | Incompleteness | Config spec D24 | — | Boundary values are postponed until implementation. | Record the values now. |
| m-12 | Minor | Incompleteness | ADR §14 | — | It doesn't record what it overrides: the self-delegation ban, the `add_mcp_server` deny reasoning, or JUDGE-FR-059. | Add them. |
| o-1 | Observation | Overcomplexity | Config spec FR-004 | — | `get_agent` and `get_agent_tools` both return overrides and effective policies. | Put the catalog view in one tool only. |
| o-2 | Observation | Feasibility | Visual spec B5 | — | P−1 = 4095×4097 and P+1 = 2¹⁴×2¹⁰+1 can be constructed. Decoding 4096×4096 needs about 64 MB, which matters in constrained test environments. | Fine for CI; note the memory cost. |
| o-3 | Observation | — | Config spec §5 | — | Its claims about `validateTarget`, the locked-skills rejection, the workspace lock, the fresh-install-only skill seed and `list_models` all match the source. | — |

## Structural check

Both specs have the full plan-spec structure (numbered requirements, BDD scenarios, a traceability matrix and success criteria). The ADR is prose, so I checked it for completeness instead.

| Check | Config spec | Visual spec |
|---|---|---|
| Every user story has acceptance scenarios | PASS | PASS |
| Every acceptance scenario has a BDD scenario | PASS | PASS |
| Every BDD scenario has `Traces to:` | PASS | PASS |
| Every BDD scenario has a planned test | PASS | PASS |
| Every FR is in the matrix | PASS | PASS |
| Every BDD scenario is in the matrix | PASS | PASS |
| Datasets cover boundaries and errors | FAIL (D24 postponed; no roster, MCP-enforcement or self-delegation data) | PASS, apart from M-10 semantics |
| Regression impact addressed | PASS (misses the pinned manifest tests, m-5) | PASS |
| Success criteria measurable | PASS (SC-003, SC-005 and SC-006 depend on C-1, C-2 and M-1) | PASS |

**ADR completeness:** scope and exclusions are clear. The actors aren't fully defined (Admin, General Purpose and Explorer identities). Failure modes are covered in prose. There isn't enough detail to start on roster seeding, connector enforcement or self-delegation. It doesn't record the constraints it overrides (m-12, M-9).

## Test coverage gaps

| Category | Gap | Affects |
|---|---|---|
| Authorization at runtime | An unbound MCP tool is still callable (C-2) | BDD-04, BDD-07, D06 |
| Role boundary | Self-delegation allowed only through a self-edge, within the depth cap; custom agent refused (C-1) | BDD-15, D18, D25 |
| Reachability | Admin's `add_mcp_server` on a fresh install under the shipped ceiling (M-2) | Missing |
| Session route | Ava invoked in a delegated session makes no writes and returns the proposal (M-3) | BDD-07, BDD-08 |
| Concurrency | A pause between the entity write and the soul write; a competing soul edit (M-4) | BDD-09, D13 |
| Policy matrix | Effective policy for each role across the whole catalog compared with the ADR table (M-5) | TestADR090_RolePoliciesAndGPIdentity |
| Drift | Tool names in prompts and skills are real and permitted (M-8) | Missing |
| Evidence scope | Judge reads inside vs outside the reviewed workspace (M-6) | B10 |
| History | No base64 kept from inspection reads; replay marker (M-10) | BDD-15, B14 |

## Threat summary (STRIDE)

| Component | Spoofing | Tampering | Repudiation | Info disclosure | Denial of service | Privilege escalation | Key concern |
|---|---|---|---|---|---|---|---|
| Connector assignment | ok | ok | ok | **risk** | ok | **risk** | Bindings aren't enforced, so every agent reaches every connector (C-2) |
| Self-delegation | ok | ok | ok | ok | **risk** | **risk** | Loosening the guard without stated limits risks fork loops (C-1) |
| Agent PUT / Ava writes | ok | **risk** | ok | ok | ok | ok | Soul write outside the lock; token contract unclear (M-4) |
| Policy seeding | ok | ok | ok | ok | ok | **risk** | Sparse maps over allow ceilings grant "—" tools (M-5) |
| MCP install (Admin) | ok | ok | ok | ok | ok | **risk** | Loosening the ceiling reopens the injection path the deny was there for (M-2) |
| Judge evidence | ok | ok | ok | **risk** | ok | ok | Workspace-wide reads, not per task (M-6) |
| Image reader | ok | ok | ok | ok | ok | ok | The byte limit (M) and pixel limit (P) match the source (`GetMaxMediaSize`, `maxImagePixels`); reading through the authorized handle closes the file-swap race |
| History and replay | ok | ok | ok | **risk** | ok | ok | Base64 is kept in history; revocation semantics undefined (M-10) |
| Provider adapters | ok | ok | ok | ok | ok | ok | Adapter scope gap only (m-7) |

## Founder decisions actually needed

1. **How Admin gets to install MCP servers.** Two options:
   - **Ask:** make the global ceiling ask. Admin then sees one approval prompt per install.
   - **Allow:** make the ceiling allow and add an explicit deny for every other role, including new custom agents. This removes the protection the current deny gives against injected install requests.

   I recommend Ask, because it keeps that protection.
2. **What agents get from a newly installed connector before Ava assigns it.** Today the code gives it to every agent. The ADR assumes a way to "enforce a shipped exclusion" but never says which roles are excluded. I recommend no access until assigned, except where a role's default says otherwise.

Everything else can be settled by engineers within the decisions already made.

## What this review couldn't see

- I didn't trace the internals of the Anthropic SDK and Anthropic Messages adapters, the chat-completions request shape, the fallback candidate loop, `AgentProfile.tsx` / `ToolsAndPermissions.tsx`, or the exact paths Landlock lets an agent write to. My checks there rely on the specs' own claims, apart from the Responses translator and Bedrock, which I read.
- I didn't check whether the manifest compressed mode already defers MCP tools, so I don't report a visibility defect there.

## Findings still open

All of them: **C-1, C-2, M-1 to M-11, m-1 to m-12, o-1 and o-2**. Founder decisions 1 and 2 need answers before M-2 and C-2 can be closed.
