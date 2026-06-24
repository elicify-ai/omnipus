# Tool-System UAT Plan — v0.1.0 completion

**Date:** 2026-06-24 · **Scope:** the live v0.1.0 builtin tool surface (~78 tools across
16 domain categories) — tested by **actually prompting LLM agents to use each tool** and
verifying the outcome. NOT v0.3. **Branch:** `feat/0.1.0-uat-fixes`.

**Tool name authority:** `docs/internal/design/tool-rename-map-2026-06.md` (§7 rename).
All tool names below are post-rename verb-first snake_case (no `system.` prefix, no dots).

## Methodology

Three test modalities, chosen per tool:

1. **LLM-prompted** (primary) — send a chat message (WS `/api/v1/chat/ws`) that elicits the tool
   call from the right agent; assert the tool fired + the outcome (tool result + side-effects).
   This is the real UAT: the agent decides to use the tool.
2. **REST API** — for tools NOT LLM-exposed by default (most admin ops, conditional
   tools). Test the gateway REST endpoint that mirrors the tool. Verify via the API response.
3. **Playwright MCP** — for tools whose **effect is UI-visible** (board cards, sidebar workspaces,
   connectors, settings, agents screen). Drive the SPA + assert the visible change.

## Agent → tool map (LLM-exposed only)

Verified live (`GET /agents/{id}/tools`, default seed):

- **Mia / Jim / Ray (28 each):** `list_agents`, `browser_navigate`, `browser_click`,
  `browser_type`, `browser_get_text`, `browser_screenshot`, `browser_wait`,
  `browser_evaluate` (7 browser tools), `find_skills`, `hand_off`,
  `install_skill`, `list_directory`, `send_message`, `read_file`, `recall_memory`,
  `remember`, `run_retrospective`, `return_to_default`, `create_task`, `delete_task`,
  `list_tasks`, `update_task`, `set_todos`, `serve_web`, `workspace_shell`,
  `workspace_shell_bg`, `write_file`.
- **Ava (+6 admin = 34):** the above 28 + `create_agent`, `delete_agent`,
  `update_agent`, `list_models`, `create_skill`, `edit_skill`.

**NOT LLM-exposed by default** (→ API/Playwright, or configure-to-expose): `exec` (needs sandbox
wiring), `search_web`/`fetch_url` (need a search provider), `edit_file`/`append_file` (catalog-only,
not registered per-agent), `read_inbox`/`search_email`/`read_message`/`send_email`/`reply` (need a
mailbox), `spawn`/`run_subagent`/`check_spawn_status` (not in default seed),
`search_tools_bm25`/`search_tools_regex` (need MCP cache), and the admin tools:
`create_workspace`/`update_workspace`/`delete_workspace`/`list_workspaces`/`get_workspace`,
`enable_channel`/`disable_channel`/`configure_channel`/`list_channels`/`test_channel`,
`configure_provider`/`list_providers`/`test_provider`, `add_mcp_server`/`remove_mcp_server`/
`list_mcp_servers`, `get_config`/`set_config`, `list_skills`/`remove_skill`,
`query_cost`/`run_doctor`/`navigate`, `create_task_in_workspace`/`update_task_in_workspace`/
`list_tasks_in_workspace`/`delete_task_in_workspace`, `read_agent_metadata`/`write_agent_metadata`/
`activate_agent`/`deactivate_agent`.

**RETIRED tools (§7) — no scenarios:** `cron`, `task_add_todo`, `task_add_dependency`,
`system.agent.list` (use `list_agents`), `system.skill.install` (use `install_skill`),
`system.skill.search` (use `find_skills`), `system.pin.*`, `system.backup.create`.

## Gateway setup (per parallel subagent — each gets its own)

Each subagent boots a fresh gateway with a config that **exposes the maximum testable surface**:

```bash
export OMNIPUS_HOME=/tmp/uat-tools-$FAMILY
# onboard via /api/v1/onboarding/complete (OpenRouter + z-ai/glm-5.2)
# config.json additions to expose conditional tools:
#   sandbox.mode = "enforce"            → exec + workspace_shell wired (fail-closed otherwise)
#   tools.web.search.duckduckgo_enabled  → search_web/fetch_url register (no API key needed)
#   tools.mcp.discovery.enabled + a stdio MCP test server (e.g. mcp-everything) → mcp tools + tool_search
#   sandbox.browser_evaluate_enabled = true → browser_evaluate executable
#   gateway.dev_mode_bypass = true       → admin REST endpoints testable pre-onboarding-equivalent
#   experimental.workspace_shell_enabled = true (Jim seed forces this anyway)
# email mailbox: configure a test IMAP/SMTP (or skip email family if no test server)
```
Model: `z-ai/glm-5.2` (the v0.1.0 e2e/standard model). Bearer: onboarding token (or login).

## Parallel subagent structure

| Subagent | Owns (family) | Modality | Own gateway |
|----------|---------------|----------|-------------|
| **A — filesystem+shell** | read_file, write_file, list_directory, edit_file, append_file (API), exec, workspace_shell/workspace_shell_bg | LLM (Mia/Jim) + API | /tmp/uat-tools-A |
| **B — web+browser** | search_web, fetch_url, serve_web, browser_navigate, browser_click, browser_type, browser_get_text, browser_wait, browser_screenshot, browser_evaluate (7) | LLM (Mia) + Playwright (serve_web preview) | /tmp/uat-tools-B |
| **C — memory+communication+delegation** | remember, recall_memory, run_retrospective, send_message, hand_off, return_to_default, send_file, list_agents, email×5 (config) | LLM (Mia) | /tmp/uat-tools-C |
| **D — tasks** | create_task, update_task (broadened), list_tasks, delete_task, set_todos | LLM (Mia) + Playwright (board) | /tmp/uat-tools-D |
| **E — skills+agents+Ava-admin** | find_skills, install_skill, create_agent/delete_agent/update_agent, list_models, create_skill/edit_skill (Ava), list_skills, remove_skill | LLM (Ava) + Playwright (Agents screen) | /tmp/uat-tools-E |
| **F — admin REST (non-exposed)** | create_workspace/update_workspace/delete_workspace/list_workspaces/get_workspace, enable_channel/disable_channel/configure_channel/list_channels/test_channel, configure_provider/list_providers/test_provider, add_mcp_server/remove_mcp_server/list_mcp_servers, get_config/set_config, query_cost, run_doctor, navigate, create_task_in_workspace/update_task_in_workspace/list_tasks_in_workspace/delete_task_in_workspace, read_agent_metadata/write_agent_metadata/activate_agent/deactivate_agent | REST API + Playwright (Settings/Connectors) | /tmp/uat-tools-F |
| **G — conditional (tool_search, email if no mailbox in C)** | search_tools_bm25/search_tools_regex | LLM (after config) + API | /tmp/uat-tools-G |
| **H — UI/Playwright cross-cutting** | board, sidebar workspaces, connectors, settings tabs, agents screen, chat tool-call rendering | Playwright MCP | shares a gateway with D/E/F |

8 subagents, each own gateway (except H shares). Run in parallel; each reports pass/fail per scenario.

---

## Test scenarios

Format: **Tool** — `prompt` (what to send the agent) / `API call` → **expect** → **verify** → [modality].

### Family A — Filesystem + Shell

- **read_file** — Mia: "Read the file /tmp/uat-a/sample.txt and tell me what's in it." → read_file fires, returns content. **Verify:** content matches; edge: a .docx (decoded to text), pagination (offset/length). [LLM]
- **write_file** — Mia: "Write 'hello uat' to /tmp/uat-a/out.txt." → write_file. **Verify:** `cat` the file (via exec or REST media) = "hello uat"; edge: overwrite=true required on existing file (reject without it). [LLM]
- **list_directory** — Mia: "List the files in /tmp/uat-a." → list_directory. **Verify:** listing includes sample.txt, out.txt. [LLM]
- **edit_file / append_file** — NOT LLM-exposed (catalog-only). **API:** no direct REST (file tools are agent-only); verify via the catalog (`GET /tools` lists them) + note they're not agent-callable in v0.1.0 (documented gap). [API/catalog]
- **exec** — Jim (sandbox=enforce): "Run `echo hello-uat` and tell me the output." → exec. **Verify:** stdout "hello-uat"; edge: background session (returns sessionId), then poll/read/kill. **Deny-path:** a command blocked by the binary allowlist → denied with reason. [LLM]
- **workspace_shell** — Jim: "Run `ls -la` in the workspace." → workspace_shell. **Verify:** output is the workspace dir listing; edge: workspace_shell_bg (background) + session management. [LLM]
- **workspace_shell path-escape guard** — Jim: "Run `cat /etc/passwd`" (outside workspace) → workspace_shell → **Verify:** rejected "path escapes workspace". [LLM]

### Family B — Web + Browser

- **search_web** — Mia (DuckDuckGo enabled): "Search the web for 'Omnipus agentic core' and summarize." → search_web. **Verify:** results returned (titles/urls); the summary references them. [LLM]
- **fetch_url** — Mia: "Fetch the page at https://example.com and tell me the title." → fetch_url. **Verify:** content includes "Example Domain". [LLM]
- **serve_web** — Mia: "Serve this HTML: <h1>UAT preview</h1>." → serve_web returns a preview URL. **Verify:** [Playwright] navigate to the preview URL (port 5001/8081) → page shows "UAT preview". [LLM + Playwright]
- **browser_navigate** — Mia: "Navigate to https://example.com and tell me the page title." → browser_navigate. **Verify:** returns title "Example Domain" + final URL. [LLM]
- **browser_click / browser_type / browser_get_text / browser_wait** — Mia: a sequence — "Go to a page with a search box, type 'omnipus', click the search button, wait for results, then read the result text." → browser_navigate→browser_type→browser_click→browser_wait→browser_get_text. **Verify:** each step's result; browser_get_text returns result content. [LLM]
- **browser_screenshot** — Mia: "Take a screenshot of the current page." → browser_screenshot. **Verify:** returns a base64 JPEG (non-empty). [LLM]
- **browser_evaluate** — (browser_evaluate_enabled=true) Mia: "Evaluate `document.title` in the page." → browser_evaluate. **Verify:** returns the title. **Deny-path** (separate gateway with the flag OFF): "Evaluate `1+1`" → denied "disabled — set browser_evaluate_enabled". [LLM, both gated + ungated]
- **browser SSRF guard** — Mia: "Navigate to http://169.254.169.254/" → browser_navigate → **Verify:** rejected by SSRF (metadata IP blocked). [LLM]

### Family C — Memory + Communication + Delegation

- **remember** — Mia: "Remember that the deploy key is 'uat-12345'." → remember. **Verify:** recall returns it (next scenario). [LLM]
- **recall_memory** — Mia (new session): "What do you remember about the deploy key?" → recall_memory. **Verify:** returns 'uat-12345'. [LLM]
- **run_retrospective** — Mia: "Do a retrospective on this session." → run_retrospective. **Verify:** returns a reflection summary. [LLM]
- **send_message** — Mia: "Send a message to Jim: 'please review the docs'." → send_message. **Verify:** message delivered (Jim's session/heartbeat sees it, or the tool returns success). [LLM]
- **hand_off** — Mia: "Hand this off to Jim." → hand_off. **Verify:** turn transfers to Jim (next message routed to Jim / handoff event). [LLM]
- **return_to_default** — (after hand_off) Jim: "Return to the default agent." → return_to_default. **Verify:** control returns to Mia. [LLM]
- **send_file** — Mia: "Send the file /tmp/uat-a/sample.txt to Jim." → send_file. **Verify:** Jim receives the file ref. [LLM]
- **list_agents** — Mia: "List the available agents." → list_agents. **Verify:** returns Mia/Jim/Ava/Ray (+ subagents) with IDs. [LLM]
- **email (read_inbox/search_email/read_message/send_email/reply)** — (mailbox configured) Mia: "Read my inbox." → read_inbox → **Verify:** returns messages. "Send an email to x@y.com with subject 'uat'." → send_email → **Verify:** sent (or SMTP test server receipt). [LLM] *(Skip if no test mailbox — note as config-gated.)*

### Family D — Tasks (incl. §2 broadened update_task + new set_todos)

- **create_task** — Mia: "Create a task titled 'Review docs', assign it to Jim, priority 2." → create_task. **Verify:** [API] `GET /api/v1/workspaces/{ws}/tasks` includes it; [Playwright] board shows the card. Edge: with `blocked_by`. [LLM + API + Playwright]
- **update_task (broadened — §2)** — multiple scenarios:
  - "Rename task X to 'Review v0.1 docs'." → updates title. **Verify:** title changed (API read-back).
  - "Set task X priority to 1 and due date to tomorrow." → updates priority+due. **Verify.**
  - "Reassign task X to Ava." → updates agent_id (delegation-gated). **Verify:** reassigned (or denied if Ava not in trust set).
  - "Mark task X blocked by task Y." → sets blocked_by → task becomes `blocked` (§2 recompute fix). **Verify:** status `blocked`.
  - "Clear task X's dependencies." → blocked_by:[] → status back to `next` (§2 clear fix). **Verify.**
  - "Mark task X done with result 'reviewed'." → status done + result + **advances blocked dependents** (§2). **Verify:** dependents → `next`; `advance_warning` if a dependent write fails (inject fault if possible).
  - **Cycle rejection:** "Mark task A blocked by B and B blocked by A." → rejected (cycle). **Verify:** error, no edge persisted.
  - **Cross-workspace guard:** "Block task X with a task from another workspace." → rejected. **Verify.**
  - **Status-only back-compat:** "Mark task X in_progress." (only status) → works, other fields untouched. **Verify.**
  [LLM + API + Playwright]
- **list_tasks** — Mia: "List my tasks." → list_tasks. **Verify:** returns the created tasks (role=assignee vs delegator). [LLM]
- **delete_task** — Mia: "Delete task X." → delete_task. **Verify:** gone from board (API + Playwright). [LLM + API]
- **set_todos** — Mia: "Set my todos for this task: [step1 pending, step2 in_progress]." → set_todos with goal + items (tri-state: pending/in_progress/completed). **Verify:** [API] scratchpad task created with correct checklist (NOT a `done` boolean — tri-state status values). [Playwright] board shows the scratchpad card with checklist.
  - **Replace:** call set_todos again (same goal, new items) → verify OLD items gone, new items present (replace-semantics, not append).
  - **New goal archives prior:** call set_todos with a different goal → prior scratchpad archived, new one created. **Verify** both via API.
  - **Invalid status rejection:** call with status="done" → REJECT ("invalid status: must be pending, in_progress, or completed").
  - **No-hijack:** real user task named same as goal must NOT be overwritten by set_todos. **Verify** distinct records.
  [LLM + API + Playwright]

### Family E — Skills + Ava's admin tools

- **find_skills** — Mia: "Find a skill for 'docker compose'." → find_skills. **Verify:** returns ClawHub results (slugs/descriptions). *(Needs registry config / network.)* [LLM]
- **install_skill** — Mia: "Install the 'docker-compose' skill." → install_skill. **Verify:** skill installed in workspace (API or filesystem). [LLM]
- **create_agent** — Ava: "Create a new Main agent named 'Tester', model glm-5.2." → create_agent. **Verify:** [API] `GET /agents` includes 'tester'; [Playwright] Agents screen shows it. [LLM + API + Playwright]
- **update_agent** — Ava: "Rename agent 'tester' to 'Tester2'." → update_agent. **Verify:** renamed. [LLM + API]
- **delete_agent** — Ava: "Delete agent 'tester'." → delete_agent. **Verify:** gone from roster + Agents screen. [LLM + API + Playwright]
- **list_models** — Ava: "List the available models." → list_models. **Verify:** returns the configured provider's models. [LLM]
- **create_skill** — Ava: "Create a local skill named 'uat-skill' with body 'echo uat'." → create_skill. **Verify:** skill file exists. [LLM]
- **edit_skill** — Ava: "Edit skill 'uat-skill' body to 'echo updated'." → edit_skill. **Verify:** updated. [LLM]

### Family F — Admin REST (non-LLM-exposed admin tools)

These tools are admin/operator operations exposed via REST + UI, not agent-prompted. Test the
REST endpoints (the UI's path) + Playwright the UI screens.

- **Workspace tools (create/list/get/update/delete)** — `POST/GET/PUT/DELETE /api/v1/workspaces`. **Verify:** API responses + [Playwright] sidebar workspace switcher shows/updates. [API + Playwright]
- **Channel tools (list/enable/disable/configure/test)** — `GET /api/v1/channels`; configure/enable/disable/test via `PUT /api/v1/channels/{id}`. **Verify:** [Playwright] Connectors screen. **Note:** enable/disable/test are fully implemented (§6) — assert real success/failure response, not NOT_IMPLEMENTED. [API + Playwright]
- **Provider tools (list/configure/test)** — `GET /api/v1/providers`; `POST /providers/{id}/test`. **Verify:** provider list + test result. [API + Playwright Settings]
- **MCP tools (add/list/remove)** — `GET/POST /api/v1/mcp-servers`; `POST /mcp-servers/{id}/test`; `PATCH /mcp-servers/{id}` (§437). **Verify:** add a stdio MCP server → test connects → list shows real status+tool_count → edit (PATCH) → remove. [API + Playwright Skills/MCP tab]
- **Config tools (get/set)** — `GET/PUT /api/v1/config` (dot-path). **Verify:** get a value; set gateway.log_level → get reflects it. [API]
- **list_skills / remove_skill** — `GET/DELETE /api/v1/skills` etc. **Verify:** list/remove. [API + Playwright Skills tab]
- **query_cost** — stub. **Verify:** returns NOT_IMPLEMENTED. [API]
- **run_doctor** — `GET /api/v1/.../doctor` (or CLI). **Verify:** returns health checks. [API]
- **navigate** — (agent-callable but admin) drive the SPA. **Verify:** [Playwright] the UI navigates to the named screen. [Playwright]
- **Tasks in workspace (create/update/list/delete _in_workspace)** — `GET/POST/PUT/DELETE /api/v1/.../tasks`. **Verify:** board CRUD via REST. [API + Playwright]
- **read_agent_metadata / write_agent_metadata / activate_agent / deactivate_agent** — `GET/PUT /api/v1/agents`. **Verify:** read/write metadata (persona), activate/deactivate. [API + Playwright Agents screen]

### Family G — Conditional (search_tools)

- **search_tools_bm25 / search_tools_regex** — (MCP discovery enabled) Mia: "Search for a tool to read a file." → search_tools_bm25 or search_tools_regex. **Verify:** returns read_file's schema. [LLM]
- **Empty-pattern guard** — empty pattern → REJECT (guard against dumping all tools). [LLM]

### Family H — UI / Playwright cross-cutting

- **Board** — after create_task/update_task/delete_task (Family D): the board card appears, updates (status/column), disappears. After set_todos: scratchpad card shows tri-state checklist (pending/in_progress/completed — NOT a `done` boolean). [Playwright]
- **Sidebar workspaces** — after workspace create/delete (Family F): sidebar switcher updates. [Playwright]
- **Connectors screen** — after channel configure/enable/disable (Family F): the channel row updates. [Playwright]
- **Settings tabs** — providers, security, gateway, integrations render; config changes (set_config) reflect. [Playwright]
- **Agents screen** — after create_agent/delete_agent (Family E): roster updates. [Playwright]
- **Chat tool-call rendering** — when an agent calls a tool (any LLM scenario), the tool call renders inline (collapsible), with the result. [Playwright]
- **Delegation-denied label** — when a delegation is denied (Family D reassign to disallowed agent), the collapsed tool call shows "Delegation denied · <reason>" (G17 fix). [Playwright]

---

## Prerequisites & config-gated tools (per family)

| Tool | Prerequisite to be testable |
|------|------------------------------|
| exec, workspace_shell | `sandbox.mode=enforce` (else fail-closed unregistered) |
| search_web, fetch_url | a search provider (DuckDuckGo = no key; set `tools.web.search.duckduckgo_enabled`) |
| browser_* | Chromium present (`chromium-browser`); `browser_evaluate_enabled=true` for browser_evaluate |
| email.* | a test IMAP/SMTP mailbox (or skip — note as config-gated) |
| search_tools_bm25 / search_tools_regex | a stdio MCP test server (e.g. `mcp-everything`) + `tools.mcp.discovery.enabled` |
| find_skills/install_skill | ClawHub registry reachable (network) |
| admin tools (REST) | `dev_mode_bypass=true` or onboarded admin token |

## Pass/fail criteria

- **PASS:** the agent invokes the correct tool (verified in the WS tool-call frame / tool result),
  AND the outcome is correct (side-effect verified via API/filesystem/UI). Edge-case scenarios
  (cycle rejection, SSRF block, path-escape, deny-by-default for browser_evaluate, stubs returning
  NOT_IMPLEMENTED) must behave as specified.
- **FAIL:** wrong tool / no tool invoked, tool errors unexpectedly, side-effect missing/wrong, or
  an edge-case guard doesn't fire.
- **Coverage target:** every one of the ~78 catalog tools has ≥1 scenario (LLM, API, or Playwright).
  Stubs (query_cost) PASS by returning the honest NOT_IMPLEMENTED message. Config-gated tools
  (email without a mailbox) are marked SKIP-with-reason.
- **set_todos tri-state:** the todo status field accepts `pending`, `in_progress`, `completed` only.

## Execution

8 parallel subagents (A–H), each its own gateway (H shares). Each runs its family's scenarios,
reports pass/fail/skip per tool. Aggregated into a UAT report. Re-run any FAIL after a fix.
Playwright scenarios (B serve_web, D board, E Agents screen, F Settings/Connectors, H) need the
Playwright MCP connected to the subagent's preview URL.
