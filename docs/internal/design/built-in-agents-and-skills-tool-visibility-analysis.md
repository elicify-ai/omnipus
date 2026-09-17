# Built-in agents — default tool visibility analysis

Date: 2026-09-17. Status: analysis with subsequent founder decision; runtime implementation pending.

Scope: analysis of the existing Omnipus tool harness and the tool surface exposed in this Codex session, informed by the confirmed agents-and-skills requirements. Source baseline: worktree code at `a0b36050b`; requirements contain subsequent local interview revisions. No runtime tests were run. A separate read-only agent traced definition assembly and the native Judge/Supervisor paths. GitNexus has no index for this worktree, so the evidence comes from source inspection.

## Accepted direction — supersedes the initial proposal

The founder chose the simpler existing model: **one global hardcoded upfront tool set**, filtered by per-agent permissions. No per-agent visibility setting or role-based definition selection is to be added. The exact accepted 37-name set and role-prompt discovery rule are authoritative in §5.4 of `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/design/built-in-agents-and-skills-2026-09.md`.

Role-specific standard tools are named and explained in each built-in prompt. Tools outside the global upfront set are loaded through ToolSearch by exact name before use. The initial role-specific upfront tables below are retained as analysis context and role-tool inventories only; they are not the accepted visibility requirements. In particular, the initial browser and knowledge-retrieval promotions below are not additions to the accepted global set.

A short text preview or prompt mention is not a callable definition. Permission, registration, and session applicability remain separate from global visibility. Skill is upfront, while skill bodies remain on demand.

## What exists today

Compressed mode is enabled by default. Its global full-definition list has 17 names:

`read_file`, `write_file`, `edit_file`, `list_directory`, `list_mounts`, `search_web`, `fetch_url`, `send_message`, `switch_agent`, `send_file`, `message_parent`, `remember`, `recall_memory`, `recall_conversation`, `set_todos`, `list_tasks`, `delegate`.

`ToolSearch` is also directly offered as infrastructure, subject to the filtered runtime surface. Nine tools receive text previews but still require loading: `list_agents`, `list_jobs`, `serve_web`, `get_workspace`, `bash`, `create_task`, `update_task`, `create_plan`, `execute_plan`.

`Skill`, `AskUserQuestion`, `set_goal`, `goal_claim`, `grep`, `inspect_session`, and `plan_correct` default to search-only on the ordinary compressed path. There is a narrow exception for an active goal lacking criteria: goal forcing exposes `set_goal` and an eligible user question. That does not make either tool generally visible.

Allowed but unloaded calls are refused by the offered-tool check. Once loaded, static tools remain available in the agent/session's in-memory loaded set; switching agents or starting a child session does not automatically share that set. The native Judge and Plan Supervisor use the same normal turn loop, so their central tools are deferred too. Disabling compression offers all policy-filtered tools, but that is too coarse a solution for a large connector catalog.

## Reference harness

In this session the assistant receives the command execution and process follow-up interfaces, file patching, image viewing, user questions, goal controls, and collaboration controls without searching for them. These are capabilities observed in this session, not a claim about every Codex configuration. The session still has deferred connector tooling.

The relevant design principle is that execution, clarification, coordination, progress, and inspection should not require discovering how to do the agent's core work. Omnipus can retain its own tools: its `bash` already combines run/poll/read/kill, so copying separate process tools from this harness is unnecessary. Omnipus's `set_goal` and `goal_claim` also have their own scope rules; do not transplant this harness's goal semantics.

Official reference: [OpenAI tool search documentation](https://developers.openai.com/api/docs/guides/tools-tool-search). It distinguishes immediately callable functions from deferred ones and describes the discovery step and context tradeoff. It supports the loading distinction, not a particular Omnipus role list.

## Initial proposal — shared capabilities (superseded as visibility policy)

These are shared building blocks, applied only to roles permitted to use them. The Judge and Supervisor retain their fixed smaller sets.

| Purpose | Direct tools | Applicable roles/context |
|---|---|---|
| Skills and long-tail discovery | `Skill`, `ToolSearch` | Registered, permitted agents. Skill bodies remain loaded on demand; making the loader visible does not preload every skill. |
| Ask or route a question | `AskUserQuestion`, `message_parent` | User-facing owner sessions use AskUserQuestion; delegated sessions use message_parent. Do not advertise an always-refused direct user question to a child. |
| Find teammates and hand off | `list_agents`, `switch_agent`, `send_message` | Working agents whose roles permit the action. Listing a teammate does not grant delegation trust. |
| Preserve and recall context | `remember`, `recall_memory`, `recall_conversation` | Memory-enabled working agents, not Judge/Supervisor. |
| Track work | `set_todos`, `list_tasks`, `list_jobs` | Roles that use these operations; avoid hiding the follow-up tool after starting background work. |
| Goal lifecycle | `set_goal`, `goal_claim` | Expose when legal for the session: editable goal record for set_goal; valid completion-claim scope for goal_claim. Not every child or goalless chat qualifies. |
| Read inputs and find files | `read_file`, `list_directory`, `grep`, `list_mounts`, `library_list`, `library_read` | File-reading working roles. Admin gets the setup-relevant subset; Judge only its fixed task-evidence subset. |
| Workspace and knowledge context | `get_workspace`, `list_workspaces`; `knowledge_describe`, `knowledge_find`, `knowledge_read`, `knowledge_list` | Agents granted those capabilities. Do not grant identity-sensitive knowledge tools to shared worker roles merely to make the list uniform. |
| Write and return artifacts | `write_file`, `edit_file`, `append_file`, `send_file` | Mia and General Purpose; Admin's setup files as needed. File-writing visibility is not permission to edit protected agent metadata. |

## Initial proposal — role inventories (use for prompts, not visibility)

This table complements the shared baseline; it is a proposed minimum working set, not a limit on permission grants.

| Role | Directly visible working tools |
|---|---|
| Mia | `bash`; `create_task`, `update_task`; configured email tools `read_inbox`, `search_email`, `read_message`, `reply`, `send_email`; `search_web`, `fetch_url`; file read/write/return tools and document skills through visible Skill. |
| Jim | `delegate`, `list_agents`, `list_jobs`; `create_task`, `update_task`, `list_tasks`; `create_plan`, `execute_plan`, **`stop_plan`**; `get_workspace`, `list_workspaces`, `create_workspace`, `update_workspace` where granted; `search_web`, `fetch_url`. Stop must be as discoverable as start. |
| Ava | `list_agents`, `read_agent_metadata`, `create_agent`, `update_agent`, `list_models`; `list_skills`, `find_skills`, `create_skill`, `edit_skill`, `install_skill`; `list_mcp_servers`; `list_workspaces`, `get_workspace`, `update_workspace`; AskUserQuestion for her combined proposal. The completed capability-read/configuration and workspace delegation operations must also be direct; some required operations still lack a tool surface. |
| Admin | `run_doctor`, `get_usage`; `list_mcp_servers`, `add_mcp_server`; `list_providers`, `list_models`, `configure_provider`, `test_provider`; `list_channels`, `configure_channel`, `enable_channel`, `test_channel`; execution and setup file tools. List/configure/test form one workflow. Runtime registration must actually make the chat-able Admin setup route usable outside workspace teams. |
| Planner | `create_task`, `update_task`, `list_tasks`, `list_agents`, `delegate`, `message_parent`; file discovery/reading; `search_web`, `fetch_url`; Skill for plan and define-goal. Planning must not require searching for task creation. |
| Researcher | `search_web`, `fetch_url`, file/library read and discovery tools, `message_parent` and permitted result messaging; Skill for deep-research. No shell or extra delegation is implied. |
| General Purpose | `bash`, file read/write/discovery/return tools, `update_task`, `list_tasks`, `set_todos`, `list_jobs`, `delegate` for same-role helpers, `message_parent`, `goal_claim` in valid task scope; `search_web`, `fetch_url` when granted. Bash already exposes process polling and cancellation through its action argument. |
| Judge | Offer its **entire fixed permitted set** directly, particularly `inspect_session`, `Skill`, and the approved read-only evidence tools. No shell or connectors. No tool-discovery step should be required to judge a task. |
| Plan Supervisor | Offer its **entire fixed permitted set** directly, particularly `plan_correct` and `Skill`. This includes any existing fixed read utility retained by the role; do not silently expand or shrink its permissions while changing visibility. |

For Mia, Jim, and Ava's agreed live-browser role, also offer the everyday browser controls directly: `browser_navigate`, `browser_snapshot`, `browser_click`, `browser_type`, `browser_press_key`, `browser_screenshot`, `browser_list_tabs`, `browser_open_tab`, `browser_switch_tab`, and `browser_handover`. Less frequent browser operations can remain discoverable. Availability still depends on the actual registered browser tools and permissions. Browser snapshot is a view of the browser, not a general local-image viewer.

Visibility of a mutating tool does not skip Ava's proposal confirmation or email/channel consent. Those are execution rules, independent of the definition's presence.

## Keep on demand

- The broad connector catalog and unrelated specialist tools. A future installed specialist role may explicitly promote its essential connector tools without exposing every connected server to every agent.
- Occasional deletion/removal operations, infrequent diagnostics, and advanced browser actions. A low-use destructive tool need not occupy the first-turn surface; this is not a substitute for authorization.
- Full skill bodies. The Skill tool and granted-skill summaries should be visible; instructions load when the relevant skill is selected.

Do not hide ordinary Admin installation/configuration operations merely because they are administrative: they are Admin's main job. Likewise, do not leave Jim's stop_plan in the obscure tail because it changes state.

## Gaps visibility cannot fix

1. Ava still needs complete readback and write operations for permissions, skill/connector bindings, and workspace delegation. `list_agents` only lists identity/type, and `read_agent_metadata` is not a complete live capability editor. Do not invent new tool names in the specification before settling the actual operations.
2. `read_file` and `library_read` decode supported Office/PDF files to text and reject other detected binary files. They do not deliver local images to model vision. Follow-up source verification found existing image-return routes through `send_file` and `serve_web` plus browser screenshots, but incomplete role permissions and provider adapters that omit tool-message images prevent treating those routes as a verified solution. See [visual inspection verification](built-in-agents-and-skills-visual-inspection-verification.md). Execution alone does not prove visual validation works; a separate document engine is unnecessary.
3. A tool name in a seed or global metadata catalog does not prove its runtime registration, permissions, input schema, or callbacks work. Configured mailboxes/connectors and session scope still matter.

## Implementation guardrails and verification

Retain the existing tool-name classification and expand the upfront set to the exact accepted list in requirements §5.4. ToolSearch stays directly callable infrastructure. Do not introduce role-specific classification, per-agent visibility fields, or a new loading mechanism. Apply the existing permission filter first and keep provider definitions, text previews, offered-call validation, and tools metadata consistent.

Verify fresh sessions, not sessions that already loaded the tools. Mia can call Skill and bash directly when permitted; Ava can ask a question directly and discovers deferred create/update tools named in her prompt; Jim can start and stop plans without discovery; Judge can inspect and Supervisor can correct without discovery. A new child starts with the global upfront set filtered by its own registration, permissions, and scope. Verify denied tools stay unavailable and a permitted tool outside the set still loads through ToolSearch. Measure definition size and workflow behavior before claiming token savings or latency improvement.

## Code evidence

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/manifest.go` — `ToolManifestTier`, `fullManifestToolNames`, `previewedLazyToolNames`.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/tool_manifest.go` — `buildCompressedToolDefs`, agent/session loaded-tool bucket, goal-door exception.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/loop_run_turn.go` — `prepareToolSurface`, policy filtering and compressed/uncompressed assembly.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/tool_offer_gate.go` — rejection of calls not offered or loaded.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/goal_loop_forcing.go` — `evaluateGoalForcing`, narrow empty-goal criteria exception.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/verifier_adjudication.go` — native Judge's normal `runTurn` path.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/plan_engine_supervise.go` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/agent/plan_engine.go` — Supervisor dispatch through `processTaskDirect`.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/filesystem.go` — `ReadFileTool.Execute` and document-text extraction; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/library_tool.go` wraps that same reader.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/tools/shell.go` — `ExecTool.Parameters` includes run/poll/read/kill.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/config/defaults.go` — compressed mode enabled by default.
