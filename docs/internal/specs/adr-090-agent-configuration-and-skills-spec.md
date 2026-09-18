# ADR-090 — Agent configuration and skills specification

- **Status:** Grill-spec and independent Claude Code Opus reviews passed after corrections; L1 selects original Elicify skills; implementation delivered with verification recorded in the [implementation and verification ledger](adr-090-implementation-status.md) and the [generic environment setup verification record](adr-090-environment-setup-verification.md); release review pending; native Windows explicitly deferred; Linux Office rendering pending UAT.
- **Date:** 2026-09-17
- **Authority:** [ADR-090 — Built-in agent configuration, skills, and visual file reading](../architecture/ADR-090-built-in-agents-skills-and-visual-reading.md), decisions D1–D5 and D7.
- **Companion:** [Visual file reading specification](adr-090-visual-file-reading-spec.md), D6. Its acceptance is a release dependency for document workflows here.
- **Scope:** Greenfield roster, field protection, Ava's configuration operations, global visibility, prompts, skills, document execution and dependency setup. No runtime changes have been delivered by this document.
- **Requirement identifiers:** FR/SC/US/BDD identifiers are local to this specification; qualify cross-document references by title.

## 1. Confirmed requirements and boundaries

The founder has already confirmed the requirements through the interview and approved ADR preparation. No additional permission round is required for these decisions. Ordinary built-in colleagues and staff retain protected identity and base instructions while users can configure their capabilities. Judge and Plan Supervisor retain fixed capabilities and editable instructions. Ava must complete configuration work, including discovery, one combined confirmation, effective readback, and workspace delegation. The default roles and their boundaries are exactly ADR-090 §§2–5, not a second roster defined here.

Actors are the user, Ava, working built-ins, hidden reviewers, and Admin. Integrations are existing Settings, configuration storage, runtime publication, tool discovery, installed connectors, skills, and document execution. Success requires real reachable workflows in addition to passing code checks. Priority is P0 for coherent permissions and configuration, P1 for document workflows and packaging.

No old-roster migration, old-install compatibility layer, isolation engine, native Office engine, automatic model switching, global delegation field, per-agent visibility setting, connector credential disclosure, or third permission layer is included. Existing unrelated runtime restrictions remain in force. Preserve ordinary user choices on reload/restart; greenfield does not mean reset-on-boot.

## 2. User stories

### US-1 — Configure built-ins without changing their identity (P0)

The user changes a built-in's capabilities through Settings or Ava without changing its product identity. Hidden reviewers accept revised review instructions while keeping their capabilities fixed.

**Why this priority:** Whole-record locking currently blocks legitimate changes; broad unlocking would expose protected fields.

**Independent test:** Exercise every field class through Settings, both configuration endpoints and Ava, then reload/restart.

**Acceptance Scenarios:**
1. **Given** an ordinary built-in, **When** a valid capability change is saved, **Then** the effective change persists and its protected instructions remain unchanged.
2. **Given** Judge or Plan Supervisor, **When** valid instructions are saved, **Then** the next review uses them and its fixed capabilities remain unchanged.
3. **Given** a request mixing allowed and forbidden fields, **When** it is submitted, **Then** the complete request fails and none of its fields change.
4. **Given** existing assignments, **When** the user explicitly clears them and restarts, **Then** they remain empty; omission preserves them and malformed nulls are rejected.
5. **Given** a custom or external agent, **When** supported settings are saved, **Then** existing runtime restrictions remain enforced and its type cannot change.

### US-2 — Ava configures a usable teammate (P0)

The user asks Ava to create or change a teammate. Ava inspects actual available configuration, proposes complete changes, obtains one normal confirmation, applies the proposal and verifies usability.

**Why this priority:** A saved agent that cannot receive work or access its assigned tools does not satisfy the request.

**Independent test:** Start with an installed connector and an unassigned skill, have Ava configure a custom native worker and prove Jim can delegate a task to it.

**Acceptance Scenarios:**
1. **Given** available agents, models, tools, connectors and skills, **When** Ava inspects configuration, **Then** she sees management inventory and effective settings independently of her own execution assignments, without secrets.
2. **Given** a complete proposal, **When** the user chooses Apply, **Then** Ava applies exactly those changes after one user question and verifies effective configuration and workspace usability.
3. **Given** a proposal awaiting consent, **When** the user chooses Cancel or Change, **Then** no proposal mutation occurs; Change returns to preparation.
4. **Given** concurrent edits or interruption, **When** Ava resumes application, **Then** stale assumptions are rejected, completed work is not duplicated, and material changes require a revised proposal.
5. **Given** unavailable discovery, persistence, or runtime activation, **When** Ava attempts the workflow, **Then** she distinguishes the failed stage and any completed changes without claiming completion.
6. **Given** a configuration request routed by another colleague, **When** Ava receives it, **Then** Ava can perform permitted configuration work in either session; confirmation is governed by the conversation and instructions, not a special session-type write gate.

### US-3 — Predictable tool access and role boundaries (P0)

Every agent receives permitted common tools immediately and discovers permitted specialist tools by name. Global policy and workspace delegation still limit actual access.

**Why this priority:** Prompt descriptions alone cannot grant tools or enforce role boundaries.

**Independent test:** Capture a fresh session's offered tools and perform one discovered specialist operation, with denied-operation controls.

**Acceptance Scenarios:**
1. **Given** a fresh eligible session using compressed manifests, **When** tool definitions are assembled, **Then** exactly the eligible permitted members of the global upfront set are callable without discovery; other tools remain deferred.
2. **Given** a role prompt naming a deferred permitted tool, **When** the agent needs it, **Then** it discovers the exact tool name and uses the loaded definition.
3. **Given** global restrictions or an unchanged shipped role exclusion, **When** an agent attempts the operation, **Then** the effective policy applies: a prompt cannot grant access, a shipped local Deny restricts access until edited, and global Ask/Deny still wins over a local Allow.
4. **Given** General Purpose or Jim requests a self helper, **When** the helper request is validated, **Then** the explicit self-edge, identity, modes and depth rules apply; General Purpose cannot target other worker roles merely because their runtime matches.

### US-4 — Deliver documents with packaged skills (P1)

Mia and General Purpose use complete document skills and their actual execution environments to deliver valid files. Any permitted native agent requests user-approved application setup, including custom agents without delegation edges.

**Why this priority:** Skill text without scripts, dependencies or accessible execution produces misleading capability claims.

**Independent test:** Generate and open one document of each required format, including a deliberate missing dependency and subsequent supported setup.

**Acceptance Scenarios:**
1. **Given** a fresh installation with packaged skills, **When** Mia or General Purpose completes a document request, **Then** it delivers a valid file using the selected workflow, with required visual validation complete.
2. **Given** a missing dependency, **When** the worker requests setup, **Then** the actual user approves the environment_setup call through the existing Ask mechanism, the application performs supported setup, and the requesting worker verifies its own environment before resuming.
3. **Given** unsupported setup or incomplete visual validation, **When** the workflow reaches that limitation, **Then** it remains incomplete and reports the limitation without fabricated success.
4. **Given** a release candidate, **When** its skill package is verified, **Then** all selected skill versions, permissions to redistribute, dependencies and referenced helpers are evidenced, and retired defaults are absent.
5. **Given** the assigned role skills, **When** each role performs their specified workflow, **Then** real tools produce the stated output and denied/missing prerequisites produce the specified limitation.
6. **Given** fresh Admin defaults, **When** Admin installs an authorized test connector, **Then** setup succeeds under the actual ceiling while other default roles cannot install or automatically execute its tools.

### Edge cases

An empty assignment is intentional; null is not an alternate spelling of empty. Removing the last override restores global inheritance, not automatic denial. An operator's global restriction can block an otherwise confirmed proposal. A connector may disappear between discovery and save. Two editors may change different fields concurrently. A proposal may lose Ava's own configuration capability partway through execution. Successful persistence can precede failed activation. Hidden agents remain non-chat targets even when their instructions are edited. Skill management discovery must not grant execution. Custom native workers must not impersonate the General Purpose role by choosing the same runtime.

## 3. Behavioral contract and integration boundaries

When an allowed edit is saved, the system preserves unrelated fields and makes its activation status visible. When a protected, malformed or stale request is submitted, it rejects that request before changing state. When a confirmed multi-resource workflow partially fails, it reports resource-by-resource outcomes; it does not imply a cross-resource transaction or silently undo unrelated edits. When a required capability is removed, the agent reports the limitation rather than restoring its defaults.

### Explicit safeguards

The system must not override operator global policy; explicit fresh-default changes below are intentional. It must not infer permission from a prompt, expose connector secrets to Ava, mutate an existing runtime type, expand General Purpose delegation by runtime equivalence, or claim successful document validation from text extraction alone. A cancelled proposal must produce zero configuration writes. A forbidden-field request must produce zero record or instruction-file writes. Saving an empty assignment must remain empty after two reloads and one process restart. All hidden-agent operations retain their existing review/correction scopes.

### Integrations

| Boundary | Data and expected contract | Failure behavior | Development/evaluation approach |
|---|---|---|---|
| Settings and configuration tools | Typed changes and current saved/effective readback | Validation/permission/conflict failures identify the field or resource; no silent dropped fields | Real stores and API integration; browser evaluation for Settings |
| Provider catalogs | Configured provider IDs and available model IDs; no keys | Unavailable or failed fetch is disclosed; no invented model choices | Stub network failures in tests; live configured provider for usability |
| Installed connectors | IDs, tools, connection status and assignments; secrets excluded | Unknown/disconnected connector is distinguished from denied access | Controlled installed connector plus disconnected fixture |
| Runtime publication | Saved revision and active revision/status | Saved-but-inactive is explicit and incomplete | Fault-injected publish plus real next-turn task |
| Skill package | Skill text, referenced helpers/assets, provenance and dependencies | Missing, unreadable or unlicensed content blocks release/workflow as applicable | Package inspection plus real execution |
| Document dependencies | Runtime-specific availability, setup result and output files | Pending setup; unsupported setup visible | Isolated execution environment; real format validators |
| Visual reading | Existing readers supply visual verification via companion spec | Missing vision capability or failed inspection remains incomplete | Companion automated and live-model acceptance |

## 4. Detailed design and functional requirements

### FR-001 — Roster, fixed identity and startup

Implement ADR-090 §§2–4 and §6.1 exactly, including chat/sidebar eligibility, default assignment, hidden roles and role-specific instructions. Do not seed retired Ray/Explorer/Max/default skills or domain packs. Do not delete pre-existing custom records as a migration. Ordinary built-in identity fields (`id`, `name`, `description`, `color`, `icon`, `locked`, built-in role classification) and base `soul` are fixed. Custom creation accepts only existing engine-facing types `Main`, `Subagent`, `subagent_3p`; reject `core`, `system`, built-in role IDs and caller-supplied `locked`. Type is immutable after creation for every agent. Do not expose a writable role identifier that lets custom agents impersonate General Purpose.

For hidden Judge/Supervisor, fixed identity and capability seeds are re-enforced; user-authored soul content is preserved. For ordinary built-ins, tool/MCP/skill defaults apply only on record creation; intentional empty selections and removed overrides survive reload and restart. The existing global ceiling reconciliation remains; do not introduce per-agent policy backfill. The fresh-install-only skill rule already exists and must be preserved. Future built-in updates must preserve saved user settings unless the user explicitly chooses a reset, and must never automatically add new capability defaults to existing ordinary agents. This does not make hidden Judge/Supervisor capabilities editable or introduce upgrade migrations into scope.

Fresh records use this exact identity mapping; no rename/type migration is included:

| Persisted ID | Display name | Persisted type | Wire type | Chat target / team | Locked |
|---|---|---|---|---|---|
| mia | Mia | core | core | Yes / yes; fresh default | true |
| jim | Jim | core | core | Yes / yes | true |
| ava | Ava | core | core | Yes / yes | true |
| admin | Admin | core | core | Yes / no team membership | true |
| planner | Planner | worker | Subagent | No / staff membership | true |
| researcher | Researcher | worker | Subagent | No / staff membership | true |
| worker | General Purpose | worker | Subagent | No / staff membership | true |
| judge | Judge | system | system | No / engine-owned only | true |
| plansupervisor | Plan Supervisor | system | system | No / engine-owned only | true |

Admin's system-operator label is its job, not persisted type. Add its compiled prompt before seeding. General Purpose reuses the existing worker ID. Ray, Explorer and Max are absent from fresh roster/default-team seeds; no existing record is silently deleted.

### FR-002 — Shared field matrix and atomic validation

All mutation surfaces MUST call the same field-policy validation rules before any entity, soul-file or workspace mutation. This includes main agent PUT, dedicated tools PUT and Ava tools. Field applicability remains runtime-aware. Trusted initialization uses the existing seeding path governed by FR-001 to establish fixed fields and re-enforce hidden capabilities; it is not a user-request validation path, is never caller-selectable, and must preserve the mutable user values described in FR-001.

| Field class | Ordinary built-ins | Judge/Supervisor | Custom native | External CLI worker |
|---|---|---|---|---|
| Identity and runtime type | Fixed | Fixed | Identity editable where existing contract permits; type fixed | Same, retaining CLI-kind immutability |
| Soul/instructions | Fixed | Editable, nonblank | Editable, nonblank | Existing external configuration contract only |
| Builtin tool policies, MCP assignments, skills | Editable | Fixed | Editable | Reject Omnipus tools/skills unsupported by runtime |
| Model/provider/fallbacks/context-window/model parameters/max tool iterations | Preserve existing editability and validation | Preserve existing editability and validation | Existing contract | Existing external contract |
| Shell security overrides | Existing runtime-supported editable settings; cannot override global restrictions | Reject changes: would alter fixed capability boundaries | Existing contract | Existing external contract |
| Workspace membership and delegation | ADR role eligibility and workspace graph | Cannot be made chat/team members to bypass hidden scope | Existing membership/delegation eligibility | Existing delegation-only eligibility |

Additional field classification: `memory_enabled` stays editable where supported for ordinary/custom agents, but is fixed false and rejected on hidden roles. `default` retains existing global chat-target eligibility: chat-capable core agents and custom Main may be selected; staff, external and hidden roles cannot. This field does not add workspace membership or make Admin a workspace-team member; Mia remains the fresh default. `voice` retains existing persisted custom/ordinary persona behavior but must report inactive voice functionality; hidden/external unsupported voice edits reject. Built-in/hidden `executor` stays native and fixed; custom/external applicability remains, including immutable CLI kind and supported editable `cli_path`. Reject `heartbeat`, `heartbeat_enabled`, `heartbeat_interval` (workspace-scoped), and unimplemented per-agent `timeout_seconds`/`rate_limits` rather than silently accepting them.

Any supplied protected field is rejected, including a same-value echo. Mixed allowed/forbidden requests fail in full before side effects. Unknown fields and unsupported runtime fields fail before side effects. Settings only submits changed editable fields; it does not silently discard a requested change. REST uses 403 for protected fields and 400 for malformed/unknown/type-inapplicable input, preserving existing 422 semantic errors for invalid policy values, unconfigured connector IDs and empty names; tool responses use `PROTECTED_FIELD` and `INVALID_INPUT`, naming offending fields. Validation/conflict rejection is zero-write. Storage faults after successful validation may produce the explicitly reported partial state in FR-007; no whole-proposal transaction is implied.

### FR-003 — Presence, empty values and strictest-wins policy

Contract-first changes belong in OpenAPI components before generated Go/TypeScript code. Preserve existing explicit nullable semantics such as context-window clearing; for newly exposed capability fields use this exact contract:

| Field | Omitted | Empty value | Null |
|---|---|---|---|
| `skills` assignment | Preserve on update; role/default create seed | `[]` means no skills | Reject |
| `mcp_servers` assignment | Preserve on update; no custom grants on create | `[]` removes all bindings | Reject |
| Binding `tools` | All tools of that explicitly assigned server, subject to policies | `[]` means no tools, never all tools | Reject |
| `tool_policy_changes.set` | No set operations | `{}` no set operations | Reject |
| `tool_policy_changes.remove` | No removals | `[]` no removals | Reject |
| `soul` | Preserve on update | Empty/whitespace rejected | Reject |
| `provider` | Preserve | `""` clears explicit pin | Reject |
| `context_window_override` | Preserve | Positive integer only | Clear per existing contract |
| Workspace `core_team`, `delegation` | Preserve | `[]` explicitly clears that collection, subject to graph validation | Reject |

Use `tool_policy_changes` with explicit `set` map of tool→allow/ask/deny and `remove` list of override names on `update_agent`; allow the same optional patch field on the main agent PUT. Reject a tool named in both, duplicate removals, unknown tools, invalid values, and simultaneous `tools_cfg` replacement plus `tool_policy_changes`. Removing a nonexistent override is an idempotent no-op. Removing the last override yields an empty sparse override map and inherits the global ceiling. Do not substitute global values into persisted agent overrides. Resolve effective policy using the existing strictest-wins compositor.

Retain the dedicated tools PUT's complete-map input validation, but require explicit sparse intent. The request carries the complete `builtin.policies` map, `revision`, and `override_names: string[]`. `override_names` lists exactly which entries to persist as local overrides. Unlisted values are inherited echoes and must equal the current global ceiling; mismatch returns 409/CONFLICT so Settings re-reads the changed ceiling and revises the proposal if needed. Listed entries remain explicit even when equal to the ceiling; equality never implies removal. Duplicate/unknown names reject 400. Empty override_names removes all local overrides. GET returns override_names from stored keys. Settings preserves it and uses set/remove patches for individual edits. This intentionally replaces full-map persistence, not complete-input validation; no old-client migration. Connector omission preserves and explicit empty clears. Existing connector binding representation may encode omitted-all versus explicit-empty-none differently internally; its serialized contract MUST preserve that distinction and runtime selection MUST honor it.

Connector bindings MUST become a runtime assignment allow-list intersected with existing strictest-wins policy. An absent server binding means no access for every role, including custom agents; a newly installed connector grants nobody execution access until assigned. Admin may configure/test connection through management tools without execution access to all exposed tools. Preserve binding-tools presence internally: omitted means all tools of this assigned server, non-nil empty means none; remove serialization behavior that collapses these. Existing wildcard-only binding means all, but reject wildcard mixed with exact names. MCP policy wildcards continue to tighten grants.

Apply assignment filtering to offered/discovered MCP tools and immediately before execution, including stale loaded definitions. Central registration may stay shared, but `pkg/agent/loop_mcp.go` and actual dispatch must not treat registration as authorization. Unbind/reconnect/restart cannot restore access. Management inventory remains sanitized and does not grant execution. Test a previously callable tool is actually refused after unbinding, not merely absent from readback.

### FR-004 — Management discovery and actual tool surface

Add a registered deferred `get_agent` tool returning sanitized configuration, applicable soul, editable-field descriptors, stored overrides/assignments, model/provider, revision token and activation status. Put the full effective tool catalog only in `get_agent_tools`, not both reads. `list_agents` keeps its lightweight routing list. Reuse `list_models`; no duplicate provider-model catalog. Model selection references a configured provider and an ID returned by its successful catalog lookup; catalog failure is a limitation, not proof a model is invalid.

Extend `list_skills` with `scope: "usable" | "management"` (default usable) and optional `name` for sanitized content/metadata inspection. Management scope requires effective `list_skills` plus at least one of `create_agent`, `update_agent`, `create_skill`, `edit_skill` to be non-Deny, checked through the compositor at execution; Ask permits this read-only discovery but does not authorize a later write; it lists installed skills independently of execution grants. Normal usable discovery and `Skill` execution remain assignment-filtered. Project/user/builtin origin and shared impact are visible; secrets and arbitrary filesystem content are not returned.

Add a deferred `get_agent_tools` management tool returning static tool catalog, installed connector tools, stored overrides/bindings and target effective policies; it does not require Ava to execute the target tool. It requires effective `get_agent_tools` permission and no broader credential access. Reuse `list_mcp_servers` for sanitized installed-server status; provide no environment secrets or credential values. Register every added tool in the static catalog, global ceiling and chosen role policy seed; do not silently grant management discovery to all roles through an Allow ceiling. Management tools' global ceiling permits explicit role grants; explicit ordinary-role denials enforce ADR exclusions under strictest-wins.

Creation preflight: `get_agent_tools` accepts exactly one of an existing `id` or `new_agent_type` (`Main` or `Subagent`). The latter is a read-only preview built from the same custom-agent defaults used by creation, combined with the current global ceiling. It returns the complete default overrides/effective policies and empty connector assignments, explicitly marked `preview: true`, without a persisted agent ID or revision. It creates no files, entities or live instances. External CLI and built-in types are refused. Preview and existing-agent readback must report non-deniable ToolSearch as available. The preview is a snapshot, not a reservation; changed settings before apply still require normal readback/conflict handling. Ava must distinguish preserved defaults from proposed sparse changes rather than describe a sparse patch as an exclusive allowlist.

### FR-005 — Complete mutation and readback

Extend `create_agent` with native `skills`, `mcp_servers`, `tool_policy_changes` and existing model/type inputs; its initial override set starts from the supported custom-agent defaults and applies explicit patch changes before publication. Reject capability fields on unsupported external runtimes. Extend `update_agent` with those fields, `revision`, and the shared field rules. Validate skill IDs, connector IDs and connector tool names against current inventory before committing. Denied effective grants are reported as restrictions in proposal/readback; no grant bypasses the ceiling.

A successful tool result includes agent ID, saved revision, effective configuration summary and activation status, following the state envelope in FR-007. Persistence failure yields `SAVE_FAILED` without invented success. Publication failure returns saved state with `activation_status: "failed"` and diagnostic; Ava keeps the configuration task incomplete. Reuse existing fast publication; prove the next eligible turn sees changed instructions/skills/tools. No broad restart is required for each write.

### FR-006 — Workspace team and delegation operations

Extend `get_workspace` to include sanitized authoritative delegation edges and `revision` token covering both membership and graph. Extend `update_workspace` with optional `delegation` edge replacement and required `revision` for every workspace mutation, including description-only changes. Edges use the existing REST workspace-delegation shape and canonical validator. No global per-agent graph is introduced.

Compute the candidate membership and graph together. If membership changes without an explicit graph, preserve valid existing edges, remove edges incident to removed members, and apply only the existing newly-added-member seed rule; return all resulting edge changes. Ava's proposal must include these effects. Explicit graph input replaces the graph and disables implicit seeding for that request. Reject unknown members, disallowed endpoints, forbidden cycles/depth, or edges outside candidate team before writing anything. Replace the existing unconditional self-delegation ban narrowly: only built-in jim and worker may have self-edges, and an explicit authoritative workspace self-edge is required for delegate-to-self. Seed jim→jim and worker→worker on fresh workspace membership. Self edges permit existing direct mode (await/background execution); task self-assignment retains its existing non-delegation behavior. Custom records and other roles cannot obtain self-edges. Validate identity, team, mode and depth before spawn. Exempt permitted self-edges alone from cycle detection; other cycles remain forbidden. Gateway/shape validation and runtime buildDelegationDenyChecker must change together. Missing/removed edge denies. Each helper consumes existing delegation depth; the effective maximum is the minimum of the edge and configured global depth. Fresh self-edges explicitly set max_depth 3 (or the lower configured ceiling). No unlimited fork engine or inheritance across different roles.

Use the existing workspace lock for read/compare/validate/write. Graph persistence remains in its existing separate authoritative store. Because the workspace and graph are separate records, a write failure after one succeeds is reported as partial with both actual states; block completion, re-read and repair through a newly validated remaining proposal. Never claim cross-file transactional rollback. Admin and hidden agents cannot be added as ordinary teammates to bypass role boundaries.

### FR-007 — One proposal, confirmation and conflict recovery

Ava may receive work through switch_agent or delegation, including Jim's requests for team specialists and skills. The founder explicitly removed Ava-specific hardcoded write restrictions based on delegation depth, unattended status or missing user-session identity. Keep confirmation conversational and prompt-governed, with no approval token or session-type mutation gate. AskUserQuestion retains its general owner-session applicability: when delegated Ava needs confirmation, she sends the proposal through message_parent so Jim can ask the user and relay the answer. A parent's own approval is not user confirmation. Once the user's approval is conveyed, Ava can apply the proposal in the delegated run, subject to ordinary permissions, revision checks and protected-field rules.

Ava's fixed prompt and authoring skills require read-before-propose, one combined before/after proposal including model, grants/removals, team/graph effects and shared skill changes, then exactly one `AskUserQuestion` with Apply/Change/Cancel. Read-only discovery precedes confirmation. Reuse the shipped global Allow defaults already present for `create_agent`, `update_agent`, `create_skill`, `edit_skill` and `update_workspace`; align Ava’s per-agent defaults to Allow for those configuration operations. A local Allow cannot override global Ask. For the same confirmed Ava workflow, fresh global and Ava defaults are Allow also for delete_agent, install_skill and remove_skill; keep destructive confirm flags supplied from the already-confirmed proposal. Other built-ins and custom defaults get explicit Deny where these management actions are unassigned. Never overwrite operator Ask/Deny on reload. New management reads get explicit catalog/global/role policies. Do not introduce an approval-token mechanism or separate approval tool. Preserve explicit operator Ask/Deny: if the operator changes a required mutation to Ask, the existing policy prompt can still occur and Ava must explain that this additional policy requirement overrides the default one-question experience. The confirmation authorizes that exact proposal, not later material changes. Destructive tool `confirm` flags may be supplied from this already-confirmed proposal; they do not require another ordinary user question.

Use one concurrency precondition named `revision`: opaque SHA-256 of canonical relevant state, returned by management/REST reads and required on every existing agent/tools/workspace/skill mutation, including delete/remove. It replaces updated_at as a write precondition; updated_at remains read-only display metadata and supplying it on update is rejected 400. All Settings autosaves and configuration tools send revision. Creation uses absent-target protection under the store lock, not a revision of a nonexistent record; duplicate identity returns 409/CONFLICT. Installation replacing an existing skill requires its revision; new installation requires target absence. Agent tokens include entity and applicable soul bytes, skill tokens resolved source/content, workspace tokens team/graph. Missing/malformed revision rejects 400/INVALID_INPUT; mismatch rejects 409/CONFLICT with zero resource writes.

Compare and commit under one authoritative resource critical section shared by all mutation surfaces. Extend the entity-store locked operation to include soul read, validation, staging, entity replacement and soul replacement; do not release entity/sidecar lock before SOUL.md writes or reacquire the same lock recursively. Readback uses the same lock so readers do not observe intermediate pairs during a successful commit; after a partial failure the reported pair is actual partial state. Stage both candidate files before replacing either. Replace entity first, then soul; a staging or comparison failure changes neither. If I/O fails after entity replacement, return persistence_status partial, exact changed components, activation_status not_attempted and fresh actual revision. Do not publish the partial agent or blindly roll back unrelated state. Repair re-reads actual entity/soul and makes a new validated write before activation. Cancellation before commit changes nothing; once replacements start, finish bounded write/result accounting before releasing the lock. Reuse existing entity/file-atomic-write facilities, not a new cross-resource transaction framework. Skill/workspace changes compare source or graph/team under their authoritative locks. Validation/conflict zero-write guarantees do not incorrectly extend to a disk fault after the first replacement.

Define the REST state envelope in OpenAPI: persistence_status complete|partial|none, activation_status active|failed|not_attempted, revision, changed_fields, error_stage and sanitized message. Complete active save returns 200. Complete save followed by publication failure returns 200 with activation_status failed and a visible incomplete/error result; autosave must not show normal green success. Storage partial/none failure returns 500 with actual state. Validation/permission/conflict statuses remain unchanged and zero-write. Tools expose the same state distinctions.
A conflict stops the remaining proposal. Ava re-reads; unrelated changes can be preserved in a refreshed proposal, but no stale write is automatically retried. A material change requires new confirmation. Apply independent operations in deterministic resource order; self-permission/team removals that would block remaining work occur last. Before interruption recovery, inspect actual resource state and match already-created IDs/content to the confirmed work; never blindly replay create/delete. No new durable cross-resource transaction framework is required. Existing task/conversation records preserve the proposal and completed-operation identifiers; loss of sufficient evidence requires a fresh proposal, not guessed completion.

### FR-008 — Global tool visibility and seed policy

Use exactly ADR-090 §5.4's 37 names globally:

`ToolSearch, Skill, AskUserQuestion, bash, read_file, write_file, edit_file, append_file, list_directory, grep, list_mounts, library_list, library_read, get_workspace, search_web, fetch_url, list_agents, send_message, switch_agent, message_parent, send_file, remember, recall_memory, recall_conversation, set_todos, list_tasks, list_jobs, create_task, update_task, set_goal, goal_claim, delegate, create_plan, execute_plan, stop_plan, inspect_session, plan_correct`.

Registration, permissions, session applicability and goal forcing still filter this set. `ToolSearch` retains infrastructure classification and is intentionally always available on compressed turns — operator Deny of `ToolSearch` does not remove the discovery door (user clarification, 2026-09-18). Permissions still govern which *target* tools discovery may reveal, load, or execute. Goal forcing may still withhold `ToolSearch` on a narrowed first-move request. Full schema assembly, previews and offered-call validation MUST agree for every target tool. New management tools above stay deferred. No per-agent visibility setting or full skill-body preload. Every role prompt names its standard tools and exact-name discovery rule from ADR §5.4.

Ship a catalog-derived role inventory mapping every tool to the approved group in ADR §5, effective seeded policy and prompt/skill workflow. It is generated as implementation evidence, reviewed against the catalog, and checked for no unclassified static tools; it must not generate a per-agent deny-all backfill. Explicit ADR exclusions require authored deny overrides wherever global defaults would permit them. Add explicit Ask overrides for send_email and reply on working roles granted those outbound operations; these are newly authored Ask defaults over current Allow ceilings. Exclude internal send_message/message_parent/handback from this send gate; send_file retains its existing delivery policy. Channel/email sending resolves Ask for the granted roles; Ava configuration writes default Allow under the one-question workflow, subject to the global ceiling.

Knowledge-base defaults follow the founder clarification in ADR §5: all seven ordinary built-ins, including Admin, receive Allow for `knowledge_describe`, `knowledge_find`, `knowledge_read` and `knowledge_list`. Mia, Jim and General Purpose receive Ask for `knowledge_edit`, `knowledge_restructure`, `knowledge_configure` and `knowledge_base_create`; Ava, Admin, Planner and Researcher receive Deny for those writes. Judge and Plan Supervisor remain denied all eight. Preserve global restrictions and user edits. These tools remain deferred; role prompts name their supported knowledge operations and use ToolSearch when needed. Verify both the seed and the actual turn's resolved policy, including denied writes and a tighter global ceiling.

Fresh global add_mcp_server becomes Allow so Admin's approved installation role is reachable. Admin default is Allow; every other built-in and every new custom-agent default gets explicit Deny. Later authorized capability edits may change ordinary overrides within global restrictions. This intentionally supersedes the former anti-injection global Deny for a fresh install; preserve operator-configured Ask/Deny and never overwrite it on reload. Implementation must update root CLAUDE.md/AGENTS.md's stale Jim-bash Allow example and MCP-install ceiling explanation to cite ADR-090.

Canonical ordinary agent policy storage is sparse: author role exclusions over a non-Deny ceiling and deliberate Ask/Allow overrides. Every ADR matrix dash means effective Deny, not omission; classify the entire static catalog. Remove blanket denyAllThenOverride seeding for ordinary roles. Hidden agents may retain complete maps because their capabilities are explicitly fixed. New custom agents retain their existing complete explicitly authored default map, including installer Deny; this is distinct from ordinary built-in sparse seeding. Test each role's effective result against every catalog entry and the reviewed inventory. New tools are individually classified, not granted by omission.

With compressed manifests enabled, the exact upfront set applies and preview-only is exactly serve_web after the other eight names move upfront. A preview is not callable. With compressed manifests disabled, preserve existing all-eligible-permitted definitions, including assignment-filtered MCP; the exact-37 requirement applies to compressed mode only. Applicability and permission checks are the same in both modes. Update pinned tier arithmetic and preview-set tests.

### FR-009 — Role-complete prompts and hidden-agent boundaries

Founder clarification: Jim may delegate to Ava to identify or propose new team specialists and skills. Seed the Jim-to-Ava edge alongside his staff/self edges. Delegated Ava may inspect configuration, prepare the proposal, obtain any needed user confirmation through Jim, and apply the approved changes without a mandatory owner-session handoff. Do not silently abandon an approved team/skill need after returning the proposal.

Seed all ADR §6.1 skill assignments and interview checklists §6.2 exactly. Retarget `skill-authoring` to real current tool names. General Purpose delegation compares stable built-in role identity, not `AgentTypeWorker`; custom workers cannot acquire that identity through create/update. Jim is the shipped plan runner; Ava and General Purpose ship with explicit plan-tool Deny overrides. These ordinary capability defaults remain user-editable under FR-002; they are not a third immutable policy layer. Judge's exact tools are ToolSearch, Skill, inspect_session, read_file, list_directory and grep; its sole skill is verify. inspect_session stays reviewed-session scoped; file tools are rooted in the reviewed work's workspace under existing filesystem/mount policy. No per-task file allow-list exists or is added here. Instructions prioritize relevant outputs but do not falsely promise runtime denial of other authorized workspace files. Outside-workspace access is denied. No shell/connectors/messaging. This supersedes ADR-084's empty registry-skill allow-list, retaining verdict contracts. Supervisor's exact tools are ToolSearch, Skill, grep and plan_correct; fixed skills are plan and define-goal, with one plan_correct per wake. Changing either soul does not remove engine-enforced verdict/correction contracts. Future isolation instructions remain gated, as ADR §7 states.

### FR-013 — Role-skill content and packaging

Package every role skill below at `pkg/skills/embedded/<skill-id>/SKILL.md` through the existing embedded loader. Each requires valid name/description frontmatter, trigger/scope, prerequisites, numbered steps, expected output, and failure/stop/handoff behavior. Prompts name assigned skills and when to load them. Tool references name actual shipped operations; prohibitions/handoffs are explicitly distinguished from invocation steps. Assignment never grants tool permission. The single interview skill selects its branch by invoking built-in ID jim/mia/ava/admin; other callers receive a general clarification checklist without invented role privileges.

| Skill | Minimum procedure and output | Real workflow acceptance |
|---|---|---|
| interview | Four ADR §6.2 checklists; skip answered constraints; project/PA/config/setup distinction | Each role asks relevant missing questions and produces its specified brief/proposal/setup request |
| handoff | Mia routes projects to Jim and configuration to Ava with owner-session switch and context/progress | Correct active colleague with context; no duplicate task |
| orchestrate | Jim assigns Planner/Researcher/GP, approves/executes plan and monitors/corrects through engine tools | Real multi-step plan and delegated result; Jim does not do execution labour |
| deep-research | Question, sources, evidence comparison, citations, uncertainty and handback | Cited evidence bundle; missing source is disclosed; no shell |
| agent-authoring | Read matrix/model/type, propose settings, confirm, mutate, read back | Usable agent; protected fields/type refused |
| tool-mapping | Target catalog/effective policy, global constraints, explicit set/remove | Override changed/removed with verified runtime effect |
| skill-mapping | Management inventory, origin/shared impact, assignment vs shared deletion | Assignment change with no unintended shared deletion; revoked execution blocked |
| delegation-graph | Candidate team/edges, self/depth/mode constraints, proposed effects | Usable valid graph; invalid endpoint/self/depth refused |
| workspace-team | Preserve unrelated members, include edge additions/removals | Exactly proposed team and graph changes |
| mcp-install | Admin chooses transport/server, approved secret handling, installation and connection probe | Fresh Admin connects controlled server; no automatic execution grants; failed connection stays incomplete |
| provider-setup | Provider credential reference and real model-catalog check | Actual catalog or accurate failure; key presence is not verification |
| channel-setup | Channel credential/configuration and connection probe | Usable channel under send policy or incomplete setup |
| doctor | Relevant diagnostics, cause, supported repair/setup, repeat probe | Injected dependency/config fault is repaired and verified, not merely listed |
| verify | Reviewed-session/workspace evidence, each criterion, met/unmet verdict | Verdict cites observed evidence; no execution or unsupported success |
| inbox-triage | Permitted inbox, classification, tasks/drafts, send Ask | Fixture inbox yields intended tasks/drafts; denied or unconfirmed send never occurs |

Retarget existing plan, define-goal and skill-authoring to current tools and approved assignments, including Supervisor define-goal. Add catalog lint for explicitly marked tool references in every prompt/skill; all exact names must exist. Capability lint checks every invocation branch against the assigned role's effective seeded grants; limitation/prohibition references are allowed without execution permission. For shared skills, at least one assigned role must permit each applicable invocation and every role-specific branch must permit its own required operations. Execute the table's real registered-tool workflows and negative controls; successful Markdown loading is not delivery.

### FR-010 — Document skills, provenance and dependencies

Package the four original Elicify skills (`elicify-docx`, `elicify-xlsx`, `elicify-pptx`, `elicify-pdf`) from elicify-ai/elicify-Skills, including their referenced helpers and declared conversion/rendering/validation dependencies. Use these canonical IDs in seeded assignments and prompt references; the earlier author-* labels are superseded. Reuse existing skill loading and actual `bash` execution; don't claim a Markdown-only copy is equivalent. Mia and General Purpose must have supporting file/library/execution/delivery permissions and instructions that permit the workflow. Completion depends on companion visual-reading acceptance.

Before release, commit a dependency/provenance inventory with upstream repository URL, immutable commit for each selected skill, original path, redistribution license/terms evidence, local adaptation diff, helper/asset checksums, required runtime/library/converter versions, platform support and successful command evidence in each supported execution environment. An upstream URL or assumed open-source license is insufficient. Selecting revisions is engineering work; absence of redistribution permission or dependencies is a release blocker requiring a legally distributable alternative under the same capability contract, not silently bundled unlicensed content. No selected revision is fabricated by this spec. **L1 is resolved to original Elicify skills.** Public package distribution terms, immutable Elicify source revisions, dependency evidence and the intensive validation gates still must be recorded before release; source selection is not a claim of complete runtime readiness.

### FR-011 — Dependency ownership and honest completion

The [environment setup specification](adr-090-environment-setup-spec.md) supersedes the former Admin installation/handoff contract. Add environment_setup as a normal deferred tool with **Ask** permission. The existing tool-approval mechanism is the approval; no separate plan/token/expiry workflow, second confirmation or special God-mode exception. Existing policy resolution and user overrides remain authoritative. Mia, General Purpose and Admin default to Ask; Jim, Ava, Planner and Researcher default to Deny; Judge/Supervisor remain locked Deny. Founder clarification: the existing global and per-agent levels must both be Ask for Mia, General Purpose, Admin and new custom native agents; do not prune the explicit Ask posture during sparse-policy reconciliation. There is no execution-dependent default logic. Creation and permission previews agree; skills grant no permissions and saved overrides remain preserved. Keep the global upfront set unchanged.

Any permitted native agent, including custom Main/Subagent agents without delegation edges, requests dependencies through this tool. Workspace-local packages/caches are the default; shared runtime/native installations must be explicit in the normal approved call. The application manages shared installations, agents receive read/execute access, and skill assignment grants no setup, execution, network or file permissions. Replace role-ID wiring and Admin-only finalization; no Admin handoff is needed. Admin has cross-workspace filesystem access and can target a workspace without membership; each installation stays confined to its selected destination. Installation runs in the background with the existing Bash-style session_id and poll/read/kill lifecycle. Unattended Ask retains existing automatic denial, without a new approval queue. The pure-Go binary remains standalone.

The document skills’ dependency request covers Python and authoring libraries, LibreOffice, a supported PDF rasterizer and fonts; these are skill-provided requirements, not hardcoded tool recipes. The setup tool executes agent-supplied installation commands/scripts through existing Ask approval and must accept dependencies unknown to Omnipus at build time; Node is conditional on actual helper requirements. Use existing bounded installation/execution infrastructure, not an unrestricted host shell. After setup, the requesting agent checks actual generation, format validation, Office conversion and image rendering inside its own sandbox. Inspect real images through read_file and the companion visual-reading contract. A host install or --version check does not establish readiness; image creation does not prove visual inspection. Denial/failure preserves a resumable task and honest incomplete validation, without hidden permission grants or automatic approval retries.

### FR-012 — Observable outcomes and contract discipline

All new REST request/response/token fields are defined in OpenAPI components before generated types; no hand-written cross-boundary types. Record proposal resource IDs, mutations, conflict, persistence and activation outcomes using existing tool/audit records, excluding credentials and full sensitive skill/connector payloads. Error results must distinguish `INVALID_INPUT`, `PROTECTED_FIELD`, `CONFLICT`, `NOT_FOUND`, `SAVE_FAILED`, and activation failure; preserve REST authentication 401 and authorization 403. Clear field/state diagnostics accompany these codes. Tests and release reports separately state automated correctness and observed user/agent reachability. A saved object, passing prompt snapshot or unexecuted test plan proves neither complete delivery nor activation.

## 5. Existing codebase context and reference patterns

GitNexus query was attempted for this exact worktree and returned repository-not-found. No graph context, impact depth, process IDs, or risk ratings are claimed. The following is manual source evidence at baseline `3463b2d36`, not a complete graph-derived blast radius. The optional `docs/reference/go-implementation/00-overview.md` directory is absent; reuse repository-local entity/contract patterns rather than inventing replacement infrastructure.

| Source symbol | Planned role and flow |
|---|---|
| `pkg/gateway/rest_agents_update.go::restAPIUpdateAgentFlow.validateTarget` | Replace blanket skill restriction; retain identity/runtime validation |
| `pkg/gateway/rest_agents_update.go::restAPIUpdateAgentPersistAgent.updateRecord` | Existing locked update and optimistic check; extend expected state/patch semantics |
| `pkg/gateway/rest_tools.go::restAPIUpdateAgentTools.validateRequest`, `normalizeBuiltinPolicies`, `persistPolicy` | Field lock, complete-map contract and connector empty/preserve fixes |
| `pkg/gateway/rest_agents.go::getAgent`, `listAgents`, `fastAgentUpsert` | Sanitized readback and publication |
| `pkg/sysagent/tools/agent.go::AgentCreateTool`, `AgentUpdateTool` | Real capability operations; currently absent parameters and blanket locked rejection |
| `pkg/tools/task_query.go::AgentListTool`, `AgentInfo` | Existing lightweight ID/name/type list; retain while adding detailed management read |
| `pkg/sysagent/tools/provider.go::ModelsListTool` | Reuse live model discovery |
| `pkg/sysagent/tools/skill.go::SkillListTool`, `grantPredicateFor` | Preserve usable list; separate authorized management list |
| `pkg/sysagent/tools/workspace.go::WorkspaceGetTool`, `WorkspaceUpdateTool` | Currently omits graph read/general graph update; extend under workspace lock |
| `pkg/gateway/rest_workspace_delegation.go::buildWorkspaceDelegationEdges` and `pkg/workspace/delegationstore.go` | Canonical validator and graph store boundary |
| `pkg/coreagent/seed.go::SeedConfig`; `pkg/coreagent/seed_system.go::seedSystemAgents` | Fresh ordinary defaults, fixed hidden capabilities, preserved edited souls |
| `pkg/tools/compositor.go::resolveEffectivePolicyWith`; `pkg/tools/manifest.go` | Existing two-layer permissions and global visibility |
| `src/components/agents/AgentProfile.tsx::AgentProfile`; `ToolsAndPermissions.tsx::ToolsAndPermissions` | Replace UI blanket guards/stripping, preserve autosave conflict behavior |

Manual impact areas: REST clients/generated schemas, agent Settings, tool registry/catalog/policies, seed/runtime publication, native review loops, workspace trust graph, skill loader/writer, packaging/execution. Implementation must inspect current callers and run the relevant existing suites in CI; this list is not a claim of exhaustive impact.

## 6. BDD scenarios

Each scenario has one triggering action. Error scenarios deliberately test failure acceptance criteria; a negative criterion does not become a happy-path test by relabeling it.

### BDD-01 — Ordinary capability change
**Traces to:** US-1, Acceptance Scenario 1. **Category:** Happy Path.
- **Given** each ordinary built-in and an allowed capability patch through each mutation surface,
- **When** the patch is applied,
- **Then** its effective capabilities change and protected identity/soul do not.

### BDD-02 — Hidden instructions and preserved tuning
**Traces to:** US-1, Acceptance Scenario 2. **Category:** Happy Path.
- **Given** Judge and Supervisor with fixed capabilities,
- **When** valid new instructions and a supported model tuning change are saved,
- **Then** the next native review uses them while verdict/correction scope and fixed capability sets hold.

### BDD-03 — Whole-request refusal
**Traces to:** US-1, Acceptance Scenario 3. **Category:** Error Path.
- **Given** a valid field mixed with a forbidden, unknown, malformed or runtime-inapplicable field,
- **When** the request is submitted,
- **Then** the specified 400/403 or tool error identifies it and entity, instruction files and active configuration remain unchanged.

### BDD-04 — Clear, omit and restart
**Traces to:** US-1, Acceptance Scenario 4. **Category:** Edge Case.
- **Given** existing skills, connectors and tool overrides,
- **When** the presence-semantics dataset is applied,
- **Then** omissions preserve, explicit empties/removals clear as defined, null errors change nothing, and successful states survive two reloads and a restart.

### BDD-05 — Custom runtime contract
**Traces to:** US-1, Acceptance Scenario 5. **Category:** Alternate Path.
- **Given** valid native and external custom configurations,
- **When** the runtime-type dataset is submitted,
- **Then** supported updates succeed and forbidden engine types/type changes/external capability fields fail without mutation.

### BDD-06 — Management inventory
**Traces to:** US-2, Acceptance Scenario 1. **Category:** Happy Path.
- **Given** Ava can manage configuration but lacks the target skill/tool execution grants,
- **When** she reads management inventory,
- **Then** sanitized target settings, installed skills/connectors/tools, model catalog, revisions and editable fields are available without granting execution; an unauthorized management caller is refused.

### BDD-07 — Confirmed teammate works
**Traces to:** US-2, Acceptance Scenario 2. **Category:** Happy Path.
- **Given** Ava's combined proposal for an agent, skill/tool/connector assignment and workspace membership/graph,
- **When** the user chooses Apply,
- **Then** exactly one ordinary question covers the unchanged proposal, effective readback matches, and Jim can assign a task that uses the configured capability.

### BDD-08 — Cancel or revise
**Traces to:** US-2, Acceptance Scenario 3. **Category:** Alternate Path.
- **Given** a proposal awaiting confirmation,
- **When** the user chooses Cancel or Change,
- **Then** zero proposal mutations occur and Change produces revised preparation before any new confirmation.

### BDD-09 — Atomic stale-state conflict
**Traces to:** US-2, Acceptance Scenario 4. **Category:** Edge Case.
- **Given** another writer changes an agent, soul, skill, team or graph after the read,
- **When** the stale write reaches its locked comparison,
- **Then** it returns CONFLICT/409 with no writes for that resource and Ava stops the remainder for readback/reproposal.

### BDD-10 — Resume partial operations
**Traces to:** US-2, Acceptance Scenario 4. **Category:** Edge Case.
- **Given** persisted creation followed by interruption or one workspace-file write failure,
- **When** Ava resumes,
- **Then** actual states determine remaining work, completed records are not duplicated, partial graph/team state is disclosed, and material changes receive a revised confirmation.

### BDD-11 — Discovery, persistence and publication failures
**Traces to:** US-2, Acceptance Scenario 5. **Category:** Error Path.
- **Given** the failure-stage dataset,
- **When** Ava attempts its configuration operation,
- **Then** the failed stage and actual saved/active state are reported without a whole-proposal success claim or secret disclosure.

### BDD-12 — Exact initial tool set
**Traces to:** US-3, Acceptance Scenario 1. **Category:** Happy Path.
- **Given** every built-in role in its valid fresh-session scope with compressed manifests enabled,
- **When** the offered definitions are assembled,
- **Then** the eligible permitted intersection with the exact 37-name set is upfront and deferred schemas are absent; previews and offered-call validation agree.

### BDD-13 — Discover a standard deferred tool
**Traces to:** US-3, Acceptance Scenario 2. **Category:** Happy Path.
- **Given** Ava's prompt names a permitted deferred configuration tool,
- **When** Ava needs that operation,
- **Then** exact-name ToolSearch loads it and a real successful operation follows; skill bodies load only when selected.

### BDD-14 — Effective policy and editable defaults
**Traces to:** US-3, Acceptance Scenario 3. **Category:** Error Path.
- **Given** global Ask/Deny with local Allow, or global Allow with an ordinary seeded Deny either retained or explicitly removed by the user,
- **When** the agent attempts the operation,
- **Then** global Ask/Deny remains authoritative, a retained local Deny rejects, and the removed ordinary Deny permits execution under global Allow; no hidden regrant or immutable role-policy layer is introduced.

### BDD-15 — General Purpose helper identity
**Traces to:** US-3, Acceptance Scenario 4. **Category:** Edge Case.
- **Given** Jim/GP with valid self-edges, no self-edge, removed edge or exhausted depth, plus Researcher/custom same-runtime targets,
- **When** each attempts the helper request,
- **Then** only explicit permitted Jim/GP self helpers below the minimum edge/global/3 depth ceiling succeed; no other role/self/runtime bypass succeeds and task self-assignment remains unchanged.

### BDD-16 — Four usable documents
**Traces to:** US-4, Acceptance Scenario 1. **Category:** Happy Path.
- **Given** each of Mia and GP with assigned packaged document skills,
- **When** it completes a request for each of Word, spreadsheet, presentation and PDF,
- **Then** the eight outputs open in independent format validators, satisfy content assertions, pass required visual workflow and are actually delivered.

### BDD-17 — Approved dependency setup without delegation
**Traces to:** US-4, Acceptance Scenario 2. **Category:** Happy Path.
- **Given** one missing dependency in the worker's actual environment,
- **When** the worker requests setup,
- **Then** the requesting agent uses environment_setup, the authenticated user approves the tool call through existing Ask, and the application installs it; the agent's own sandbox probe and rendering check pass before work resumes. Repeat with a custom agent having no delegation edges.

### BDD-18 — Unsupported setup or inspection
**Traces to:** US-4, Acceptance Scenario 3. **Category:** Error Path.
- **Given** an unavailable installer, denied setup, unsupported platform or missing visual capability,
- **When** the document workflow reaches that prerequisite,
- **Then** it identifies the limitation and keeps work/validation incomplete without routing setup to Admin or claiming visual inspection.

### BDD-19 — Release package inventory
**Traces to:** US-4, Acceptance Scenario 4. **Category:** Happy Path.
- **Given** the release package and its provenance inventory,
- **When** package verification runs,
- **Then** every assigned skill and helper resolves with verified provenance/dependencies, all retired defaults are absent, and missing evidence blocks release.

### BDD-20 — Runtime connector assignment enforcement
**Traces to:** US-1, Acceptance Scenario 4. **Category:** Edge Case.
- **Given** a connected server, assigned/all/subset/empty/unassigned bindings and an already loaded definition,
- **When** an agent attempts its tool after assignment changes,
- **Then** both offered definitions and actual execution follow the current assignment-policy intersection; unbound/stale calls are refused across reconnect/restart and no secrets are disclosed.

### BDD-21 — Ava confirmation without a session-type write gate
**Traces to:** US-2, Acceptance Scenario 6. **Category:** Alternate Path.
- **Given** the same request delivered by user-owned switch_agent or delegated invocation,
- **When** Ava prepares the proposal,
- **Then** Ava obtains the one conversational confirmation directly or through her parent and applies the approved proposal; delegation, unattended status or absence of a user-session ID alone does not cause a configuration-write refusal. A cancelled proposal still produces no writes, as required by her instructions.

### BDD-22 — Shared soul/entity commit and partial storage failure
**Traces to:** US-2, Acceptance Scenarios 4 and 5. **Category:** Error Path.
- **Given** concurrent soul/entity writers or injected failure before staging, after entity replacement or at activation,
- **When** an agent edit commits,
- **Then** the required revision is checked under the shared lock, competing writers cannot interleave, validation/staging refusal changes nothing, and post-replacement failures report exact partial/saved state without activation or false success.

### BDD-23 — Admin fresh-install reachability
**Traces to:** US-4, Acceptance Scenario 6. **Category:** Happy Path.
- **Given** fresh defaults and an authorized controlled connector installation task,
- **When** Admin invokes add_mcp_server,
- **Then** the actual global and local policies permit setup, other default roles/customs are denied that tool, no agent automatically receives execution access, and operator Ask/Deny controls remain honored.

### BDD-24 — Role skills perform real work
**Traces to:** US-4, Acceptance Scenario 5. **Category:** Happy Path.
- **Given** every FR-013 role/skill branch, embedded package and controlled workflow input,
- **When** the role executes the selected skill,
- **Then** the table's output is observed through real tools, catalogue/capability lint passes, and denied/missing prerequisites follow its error/handoff contract.

### BDD-25 — Exact roster, policy and manifest modes
**Traces to:** US-1, Acceptance Scenario 1; US-3, Acceptance Scenarios 1 and 3. **Category:** Edge Case.
- **Given** a fresh install and both compressed/uncompressed manifest modes,
- **When** roster and tool access are assembled,
- **Then** exact identity/type/chat eligibility matches FR-001, each catalog tool resolves to its approved per-role policy, compressed upfront/preview sets are exact, and uncompressed mode exposes only eligible permitted tools.

## 7. TDD plan (planned, not executed)

Write failing tests first, run a meaningful positive and negative oracle, implement minimally, then rerun. Use test-plan-and-write before implementing tests and demonstrate each test detects a targeted regression; no expected values derived from the implementation under test. Unit → integration → real workflows. Go suite/build authority is CI; do not run full Go suites locally. Follow root AGENTS build tags and scoped-test restriction. E2E observations must record the actual question count, mutation events, saved and active states, and artifact validation.

| Order | Planned test name | Level | BDD | Independent oracle |
|---|---|---|---|---|
| 1 | TestADR090_FieldMatrix | Unit | 01,02,03,05 | This document's explicit role/field matrix |
| 2 | TestADR090_PresenceAndOverridePatch | Unit | 04 | Literal before/after fixture and null/error table |
| 3 | TestADR090_ManagementInventoryAuthorization | Unit | 06 | Separate inventory versus execution grant fixture |
| 4 | TestADR090_GlobalVisibility37 | Unit | 12 | Literal approved name set, not imported manifest |
| 5 | TestADR090_RolePoliciesAndGPIdentity | Unit | 14,15 | ADR exclusions and stable seeded role identity |
| 6 | TestADR090_AgentMutationParity | Integration | 01,02,03,04,05 | Both REST endpoints, tools, real stores and next-turn runtime |
| 7 | TestADR090_ExpectedStateUnderLock | Integration | 09 | Barriers force change between discovery and commit |
| 8 | TestADR090_WorkspaceGraphAndPartialFailure | Integration | 07,09,10,11 | Literal candidate graph, injected second-write failure |
| 9 | TestADR090_DiscoveryAndPublicationFailure | Integration | 06,11 | Controlled missing catalogs/save/publish failure and actual disk |
| 10 | TestADR090_SkillPackageProvenance | Integration | 19 | Committed selected-source inventory and package bytes |
| 11 | ADR090 Settings field editing | E2E | 01,02,03,04,05 | Real Settings edit plus API and restart readback |
| 12 | ADR090 Ava single proposal | E2E | 07,08,09,10,11 | Captured questions, mutation trace and delegated result |
| 13 | ADR090 upfront and discovered tools | E2E | 12,13,14,15 | Captured offered definitions and actual tool result |
| 14 | ADR090 document generation and setup | E2E | 16,17,18,19 | Independent format validators, worker probe and companion visual proof |

Additional planned tests, ordered at the appropriate level before E2E completion:

| Order | Planned test | Level | BDD | Independent oracle |
|---|---|---|---|---|
| U1 | TestADR090_ExactRosterAndPolicyInventory | Unit | 25 | Literal roster table and reviewed role/catalog policy inventory |
| U2 | TestADR090_PromptSkillToolReferences | Unit | 24 | Catalog names plus separate invocation/prohibition annotations |
| I1 | TestADR090_MCPAssignmentEnforcedAtCall | Integration | 20 | Real registry/dispatch plus assigned/unbound literal fixture |
| I2 | TestADR090_SoulEntityCommitConcurrency | Integration | 22 | Barriers around staging/replacements and real saved bytes/revisions |
| I3 | TestADR090_AdminMCPFreshCeiling | Integration | 23 | Fresh effective policy and controlled connector subprocess/service |
| I4 | TestADR090_SelfHelperGraphAndDepth | Integration | 15 | Actual gateway graph + runtime deny/depth path at cap−1/cap/cap+1 |
| E1 | ADR090 Ava conversational confirmation | E2E | 21 | Real switch/delegated sessions, relayed user confirmation and mutation counts |
| E2 | ADR090 role skill workflows | E2E | 24 | Every FR-013 row's observable outputs with real tools and fault controls |

### Test datasets

| ID | Input/state | Expected result | Traces to |
|---|---|---|---|
| D01 | All seven ordinary built-ins × skills/tools/MCP edit | Allowed with unchanged identity/soul | BDD-01 |
| D02 | Both hidden roles × new nonblank soul/model tuning | Used next review; fixed capabilities unchanged | BDD-02 |
| D03 | Valid model plus forbidden soul/name/type/skills for applicable role | Whole request 403/PROTECTED_FIELD; zero writes | BDD-03 |
| D04 | Unknown key, wrong types, malformed token, null assignments | 400/INVALID_INPUT; zero writes | BDD-03,04 |
| D05 | Existing one skill/connector/override; omit / explicit empty | Preserve / clear, respectively, across restart | BDD-04 |
| D06 | Connector bound with tools omitted / [] / one valid tool | All permitted / none / selected only | BDD-04 |
| D07 | Override set+remove same key; duplicate removal; missing override removal | Reject / reject / no-op | BDD-04 |
| D08 | Custom Main/Subagent/subagent_3p; core/system; existing type change | Supported create / reject reserved / reject change | BDD-05 |
| D09 | Ava denied target execution but allowed management; unauthorized management caller | Inventory without execution grant / refuse | BDD-06 |
| D10 | Unicode custom name and skill title, including spaces; path-traversal skill ID | Valid bounded text roundtrips; traversal rejected | BDD-03,06 |
| D11 | Provider catalog timeout/disconnected connector/skill deleted after proposal | Stage-specific incomplete result; invalid references not persisted | BDD-11 |
| D12 | Apply/Change/Cancel | One confirmation plus exact writes / zero writes / zero writes | BDD-07,08 |
| D13 | Parallel agent/soul/skill/team/graph edits; stale expected token | Conflict before resource mutation | BDD-09 |
| D14 | Creation persisted, publish failed, retry; second workspace file write failed | No duplicate; partial state disclosed and revalidated | BDD-10,11 |
| D15 | Every approved name × allowed/denied/unregistered/out-of-scope | Exact eligible upfront intersection | BDD-12 |
| D16 | Deferred get_agent; unloaded Skill body | Exact-name load then use; no body preload | BDD-13 |
| D17 | Global Deny/Ask + local Allow; global Allow + user removes/changes the last ordinary seeded Deny | Global wins; authorized removal/change of an ordinary seeded Deny with global Allow yields effective Allow | BDD-14 |
| D18 | GP→GP / GP→Researcher / GP→custom same runtime | Allowed if graph permits / denied / denied | BDD-15 |
| D19 | 2 workers × 4 formats, non-ASCII content and multi-page/sheet fixtures | Eight valid content-checked files with visual acceptance | BDD-16 |
| D20 | Missing renderer; setup on host but absent in worker; actual worker setup | Pending until worker probe succeeds | BDD-17 |
| D21 | Install denied, unsupported OS/setup, no image-capable model | Explicit incomplete state, no bypass/model switch | BDD-18 |
| D22 | Missing helper, mismatched checksum, missing license evidence, retired default | Release verification fails | BDD-19 |
| D23 | Existing numeric constraints: context window 0/1; fallback count 0/2/3; max iterations -1/0/1 | Existing minimum/max constraints retained, zero iterations inherits | BDD-03,05 |
| D24 | Provider routing key lengths 63/64/65; icon lengths 49/50/51; soul empty/one-character/whitespace; skill ID 0/1/63/64/65 bytes; description 0/1/1023/1024/1025 bytes; skill Markdown 262143/262144/262145 bytes | Existing limits: provider64, icon50, nonblank soul, skill ID1–64 slug bytes, description1–1024, Markdown≤262144; exact boundary accepts, outside rejects | BDD-03,19,24 |
| D25 | Empty core team and graph; stale edge to removed member; unknown endpoint; allowed self-helper edge | Clear/preserve-prune rules; invalid graph rejected; valid self rule retained | BDD-07,09 |

D24 is fixed from the inspected OpenAPI AgentUpdate/Create components and skills loader/authoring limits. ASCII fixtures give unambiguous byte counts; separate Unicode fixtures test valid text without confusing byte versus character limits. No finite agent-soul maximum is invented where the existing contract defines only nonblank content.

| ID | Input/state | Expected result | Traces to |
|---|---|---|---|
| D26 | None/all/empty/subset connector bindings; stale loaded call; disconnect/reconnect/restart | Assignment-policy intersection enforced at actual dispatch | BDD-20 |
| D27 | Ava via switch_agent vs delegated session | Direct or parent-relayed user confirmation; approved writes allowed in both | BDD-21 |
| D28 | Missing/stale revision; updated_at supplied; soul writer during entity commit; injected second-file failure | Invalid/conflict zero-write, no interleaving, explicit storage partial and no activation | BDD-22 |
| D29 | Fresh Admin vs all other roles/customs; global operator Ask/Deny; newly installed server | Reachable authorized setup; other default installers denied; no unassigned tool execution | BDD-23 |
| D30 | All 15 FR-013 skills and four interview branches; misspelled tool reference; denied invocation vs prohibition | Real outputs per table; lint rejects wrong/missing invocation; limitation reference allowed | BDD-24 |
| D31 | Exact nine built-ins, no Ray/Explorer/Max; compressed and uncompressed modes | Exact ids/types/chat rules; exact mode-specific schemas and preview serve_web | BDD-25 |
| D32 | Jim/worker self-edge absent/present/removed; global/edge cap at 2/3/4 and attempt depth cap−1/cap/cap+1; other/custom self-edge | Spawn only permitted identity+edge+mode below cap; all bypass cases refuse | BDD-15 |
| D33 | Sparse override equal to global and explicitly listed; absent override echoed; ceiling changed after GET so unlisted echo differs | Preserve explicit intent / inherit / 409 CONFLICT and re-read current ceiling | BDD-04,25 |

### Regression requirements

Run these existing tests in their appropriate CI suites. Preserve their behavior except where the identified blanket-lock assertion is intentionally replaced by the new role matrix:

| Existing test | Treatment and protected behavior |
|---|---|
| TestUpdateAgent_LockedCoreAgentSoulStillForbidden | Unchanged protected ordinary soul |
| TestUpdateAgent_LockedRejectsIdentityChange | Unchanged fixed identity |
| TestUpdateAgent_LockedRejectsSkills | Replace ordinary rejection with acceptance; retain hidden rejection |
| TestUpdateAgentTools_LockedAgentForbidden | Split ordinary acceptance / hidden rejection |
| TestUpdateAgent_SkillsClear | Preserve explicit-empty behavior |
| TestUpdateAgent_SkillsOnlyChange_UpdatesLiveAllowlistWithoutRestart | Preserve next-turn activation |
| TestUpdateAgent_SkillsOnlyChange_ClearingGrantsTakesEffectImmediately | Preserve revocation activation |
| TestUpdateAgentTools_PolicyOnlyUpdatePreservesMCPBindings | Preserve omission; add explicit-empty complement |
| TestUpdateAgentTools_IncompletePolicyMap_Rejected400 | Preserve dedicated replacement validation |
| TestSeedConfig_TwiceWithEmptiedListStaysEmpty | Preserve fresh-only ordinary seeds |
| TestSeedSystemAgents_ReEnforcesJudgeSkillAllowlist | Update pinned expected set to verify only; preserve enforcement |
| TestCreateAgent_DuringInFlightReload_IsImmediatelyTaskAssignable | Preserve real task assignability |
| TestWorkspaceUpdate_PreservesDelegationGraph | Preserve unrelated graph edges |
| TestWorkspaceUpdate_NoCoreTeamArg_DelegationUntouched | Preserve omission when graph also omitted |
| TestWorkspaceDelegation_PutSelfEdge_400 | Replace blanket ban with only Jim/worker explicit-edge exception; other identities remain refused |
| TestDelegationDenyChecker_SelfTargetDeniedForBackgroundDelegate; TestDelegationDenyChecker_SelfTargetDeniedForAwaitDelegate | Retain missing-edge refusal; add bounded allowed self-edge controls |
| TestDelegateTool_SelfTargetDeniedAtExecute; TestDelegateTool_SelfTargetDeniedBeforeDepthResolver | Preserve authorization-before-depth ordering and missing-edge refusal |
| TestTaskCreate_SelfAssignmentAllowedThroughRegisterSharedTools | Preserve non-delegation self-assignment |
| TestVisibility_PreviewedSetIsExactlyNine; TestVisibility_TierArithmetic | Replace expected preview nine with serve_web-only and revised full/deferred counts |
| ToolsAndPermissions.test.tsx / AgentTools.test.tsx locked-agent cases | Replace blanket lock expectation with field matrix; preserve denied writes |

| Regression dataset | Old behavior to preserve | Traces to |
|---|---|---|
| Ordinary locked soul and identity edits | Rejected before write | BDD-03 |
| Supported custom model/provider clearing | Existing runtime semantics retained | BDD-05 |
| Tools PUT without MCP field | Existing MCP bindings preserved | BDD-04 |
| Incomplete replacement tools map | Rejected; old policy unchanged | BDD-03 |
| Removed ordinary skill after reload | Not silently re-seeded | BDD-04 |
| Existing graph, workspace description-only update | Graph unchanged | BDD-09 |
| Global Deny and explicit local Allow | Deny | BDD-14 |

## 8. Success criteria and total traceability

- **SC-001:** Every role/field/surface matrix case passes; forbidden mixed requests produce zero persisted or active changes.
- **SC-002:** Every presence dataset passes after two reloads and one restart; no removed ordinary grants return.
- **SC-003:** One real Ava create/configure workflow uses exactly one ordinary confirmation for an unchanged proposal and ends in a successful Jim-assigned task using an assigned capability; cancellation performs zero writes.
- **SC-004:** Forced conflicts at each authoritative resource boundary produce no stale writes; interrupted creates produce zero duplicates; publication failures are reported as saved-but-inactive.
- **SC-005:** With compressed manifests enabled, fresh-session upfront names equal the eligible permitted intersection with exactly 37 approved names for every role; at least one deferred role tool is discovered and actually used.
- **SC-006:** All explicit role exclusions and global ceiling tests pass; GP cannot delegate to either a different built-in role or custom same-runtime worker.
- **SC-007:** Eight real document outputs (two roles × four formats) pass independent format/content checks and companion visual acceptance; approved dependency recovery is observed for Mia, GP and a custom agent without delegation edges, including actual sandbox rendering; environment setup ES-SC-01 through ES-SC-04 also pass.
- **SC-008:** Every shipped skill/helper has immutable provenance and redistribution evidence; zero retired defaults or missing package dependencies remain in the release evidence.
- **SC-009:** New contracts regenerate without drift and automated correctness is reported separately from real workflow reachability.
- **SC-010:** All 15 role-skill workflows and four interview branches pass real-tool evaluations; fresh Admin connector installation succeeds; delegated Ava can apply user-confirmed changes without a session-type gate; an unbound MCP tool is refused even with an already loaded definition.

| Requirement | User story / AS | BDD | Planned test(s) | Success |
|---|---|---|---|---|
| FR-001 | US-1 AS1,2,4; US-4 AS4 | 01,02,04,19 | FieldMatrix; AgentMutationParity; SkillPackageProvenance | SC-001,002,008 |
| FR-002 | US-1 AS1,2,3,5 | 01,02,03,05 | FieldMatrix; AgentMutationParity; Settings field editing | SC-001 |
| FR-003 | US-1 AS3,4,5; US-3 AS3 | 03,04,05,14 | PresenceAndOverridePatch; AgentMutationParity; RolePoliciesAndGPIdentity | SC-002,006 |
| FR-004 | US-2 AS1,5 | 06,11 | ManagementInventoryAuthorization; DiscoveryAndPublicationFailure | SC-003,004 |
| FR-005 | US-2 AS2,5 | 07,11 | AgentMutationParity; DiscoveryAndPublicationFailure; Ava single proposal | SC-003,004 |
| FR-006 | US-2 AS2,4,5; US-3 AS4 | 07,09,10,11,15 | WorkspaceGraphAndPartialFailure; ExpectedStateUnderLock; SelfHelperGraphAndDepth (I4, D32) | SC-003,004,006 |
| FR-007 | US-2 AS2,3,4,5 | 07,08,09,10,11 | ExpectedStateUnderLock; Ava single proposal | SC-003,004 |
| FR-008 | US-3 AS1,2,3 | 12,13,14 | GlobalVisibility37; upfront and discovered tools | SC-005,006 |
| FR-009 | US-1 AS2; US-3 AS3,4; US-4 AS4 | 02,14,15,19 | FieldMatrix; RolePoliciesAndGPIdentity; SkillPackageProvenance | SC-001,006,008 |
| FR-010 | US-4 AS1,4 | 16,19 | SkillPackageProvenance; document generation and setup | SC-007,008 |
| FR-011 | US-4 AS2,3 | 17,18 | document generation and setup | SC-007 |
| FR-012 | US-1 AS3; US-2 AS5; US-4 AS4 | 03,11,19,22 | AgentMutationParity; DiscoveryAndPublicationFailure; SkillPackageProvenance; SoulEntityCommitConcurrency | SC-001,004,009 |
| FR-013 | US-4 AS5 | 24 | PromptSkillToolReferences; role skill workflows | SC-010 |
| FR-001,FR-008 | US-1 AS1; US-3 AS1,3 | 25 | ExactRosterAndPolicyInventory | SC-001,005,006 |
| FR-003,FR-005 | US-1 AS4 | 20 | MCPAssignmentEnforcedAtCall | SC-006,010 |
| FR-007 | US-2 AS6 | 21 | Ava conversational confirmation | SC-003,010 |
| FR-007 | US-2 AS4,5 | 22 | SoulEntityCommitConcurrency | SC-004 |
| FR-008 | US-4 AS6 | 23 | AdminMCPFreshCeiling | SC-010 |

Test short names refer to the uniquely named full entries in §7. Contract generation/verification is an additional CI gate for SC-009, not a substitute for scenario tests. Every AS and every BDD, including revision scenarios 20–25, is represented; holdouts below are intentionally excluded from TDD traceability.

## 9. Holdout evaluation — do not use for TDD

These are public evaluation protocols, not secret fixtures. After implementation is frozen, an independent evaluator chooses unseen names, content, ordering and failure timing and keeps its concrete inputs outside development fixtures. Capture external behavior through the running app and real agents; do not copy these into unit tests.

| Holdout | Category | External evaluation |
|---|---|---|
| H1 | Happy | Ask Ava to configure a new teammate with evaluator-selected installed skills/connector, then have Jim request an unseen task requiring them. |
| H2 | Happy | Edit ordinary built-in assignments and hidden reviewer instructions through Settings, restart, and observe different intended working/review behavior. |
| H3 | Happy | Request a mixed-format document bundle with unseen content; open every output and inspect its corrected final layout. |
| H4 | Error | Revoke a required capability after discovery but before applying a proposal; observe an honest conflict/limitation without permission bypass. |
| H5 | Error | Disable a required document dependency in the worker environment; observe pending work, existing Ask approval and application-managed recovery, or an explicit unsupported result. |
| H6 | Edge | Interrupt immediately after a teammate is created; resume and verify there is one teammate and a correct remaining-work proposal. |
| H7 | Edge | Have two people edit an agent and its workspace graph concurrently using independently chosen changes; observe preserved unrelated edits and explicit conflicts. |

## 10. Open questions, assumptions and release prerequisites

L1 below is resolved by the founder to original Elicify document skills; no package-source choice remains pending. Engineering choices made explicit here are management read tools, patch versus replacement semantics, presence-aware connector lists, locked expected-state validation and partial-result reporting; these implement the confirmed workflow without a new transaction framework.

Selected upstream revisions, legal redistribution evidence, exact dependency versions and the final catalog-derived supporting-tool inventory remain mandatory implementation artifacts. They are not permission to defer capabilities or mark the release complete. If a selected upstream skill cannot legally be shipped or a required workflow cannot run on a supported platform, report the concrete blocking evidence and proposed alternative to the founder before changing scope. Visual-reading completion is owned by the companion spec. No runtime tests or workflow evaluations have been executed as part of writing this document.

### Resolved decision L1 — original Elicify document skills

During source verification on 2026-09-17, the four upstream `skills/docx`, `skills/xlsx`, `skills/pptx` and `skills/pdf` LICENSE.txt files at Anthropic skills commit `34040c9c568585f6929bedeaad110ad08f079624` all explicitly restrict copying, derivative works and redistribution. Their presence in a public repository is not evidence that Omnipus may bundle them. [Pinned license evidence](https://github.com/anthropics/skills/blob/34040c9c568585f6929bedeaad110ad08f079624/skills/docx/LICENSE.txt).

Daniel resolved L1 on 2026-09-17: author original Elicify document skills after online comparison, add them to [elicify-ai/elicify-Skills](https://github.com/elicify-ai/elicify-Skills), and plan intensive testing. Select `elicify-docx`, `elicify-xlsx`, `elicify-pptx` and `elicify-pdf`; do not copy or adapt Anthropic prompts, scripts or assets. Python is the initial authoring route; Node remains available where a workflow needs it. The source choice is closed. Package pinning, explicit Elicify-approved public distribution terms, dependency provisioning and full Omnipus acceptance remain release requirements. The private skill repository’s existing license is not silently changed by this decision.

The original package implementation, comparative source research and intensive test plan are available in [Elicify Skills PR 1](https://github.com/elicify-ai/elicify-Skills/pull/1). Its isolated document pilot is package evidence, not evidence that Omnipus provisioning or runtime integration has shipped.
