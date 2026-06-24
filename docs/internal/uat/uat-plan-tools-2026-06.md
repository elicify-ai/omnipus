# Tool-System UAT Plan — v0.1.0 completion

**Date:** 2026-06-24 · **Scope:** the live v0.1.0 builtin tool surface (87 tools) — tested by
**actually prompting LLM agents to use each tool** and verifying the outcome. NOT v0.3.
**Branch:** `feat/0.1.0-uat-fixes`.

## Methodology

Three test modalities, chosen per tool:

1. **LLM-prompted** (primary) — send a chat message (WS `/api/v1/chat/ws`) that elicits the tool
   call from the right agent; assert the tool fired + the outcome (tool result + side-effects).
   This is the real UAT: the agent decides to use the tool.
2. **REST API** — for tools NOT LLM-exposed by default (most `system.*` admin ops, conditional
   tools). Test the gateway REST endpoint that mirrors the tool. Verify via the API response.
3. **Playwright MCP** — for tools whose **effect is UI-visible** (board cards, sidebar workspaces,
   connectors, settings, agents screen). Drive the SPA + assert the visible change.

## Agent → tool map (LLM-exposed only)

Verified live (`GET /agents/{id}/tools`, default seed):

- **Mia / Jim / Ray (28 each):** `agent_list`, `browser.*` (7), `find_skills`, `handoff`,
  `install_skill`, `list_dir`, `message`, `read_file`, `recall_memory`, `remember`,
  `retrospective`, `return_to_default`, `task_add_dependency`, `task_add_todo`, `task_create`,
  `task_delete`, `task_list`, `task_update`, `web_serve`, `workspace.shell`, `workspace.shell_bg`,
  `write_file`.
- **Ava (+6 `system.*` = 34):** the above 28 + `system.agent.create`, `system.agent.delete`,
  `system.agent.update`, `system.models.list`, `system.skill.create`, `system.skill.edit`.

**NOT LLM-exposed by default** (→ API/Playwright, or configure-to-expose): `exec` (needs sandbox
wiring), `web_search`/`web_fetch` (need a search provider), `edit_file`/`append_file` (catalog-only,
not registered per-agent), `read_inbox`/`search_email`/`read_message`/`send_email`/`reply` (need a
mailbox), `spawn`/`subagent`/`spawn_status` (not in default seed), `cron`, `tool_search_tool_*`
(need MCP cache), and 38 of 44 `system.*` (admin/REST-only — workspace/channel/provider/mcp/config/
skill.list+remove+install+search/pin/cost/backup/doctor/navigate/task.*/agent.list+read_metadata+
write_metadata+activate+deactivate).

## Gateway setup (per parallel subagent — each gets its own)

Each subagent boots a fresh gateway with a config that **exposes the maximum testable surface**:

```bash
export OMNIPUS_HOME=/tmp/uat-tools-$FAMILY
# onboard via /api/v1/onboarding/complete (OpenRouter + z-ai/glm-5.2)
# config.json additions to expose conditional tools:
#   sandbox.mode = "enforce"            → exec + workspace.shell wired (fail-closed otherwise)
#   tools.web.search.duckduckgo_enabled  → web_search/web_fetch register (no API key needed)
#   tools.cron.enabled = true            → cron tool (if testing cron)
#   tools.mcp.discovery.enabled + a stdio MCP test server (e.g. mcp-everything) → mcp tools + tool_search
#   sandbox.browser_evaluate_enabled = true → browser.evaluate executable
#   gateway.dev_mode_bypass = true       → admin REST endpoints testable pre-onboarding-equivalent
#   experimental.workspace_shell_enabled = true (Jim seed forces this anyway)
# email mailbox: configure a test IMAP/SMTP (or skip email family if no test server)
```
Model: `z-ai/glm-5.2` (the v0.1.0 e2e/standard model). Bearer: onboarding token (or login).

## Parallel subagent structure

| Subagent | Owns (family) | Modality | Own gateway |
|----------|---------------|----------|-------------|
| **A — filesystem+shell** | read_file, write_file, list_dir, edit_file, append_file (API), exec, workspace.shell/shell_bg | LLM (Mia/Jim) + API | /tmp/uat-tools-A |
| **B — web+browser** | web_search, web_fetch, web_serve, browser.* (7) | LLM (Mia) + Playwright (web_serve preview) | /tmp/uat-tools-B |
| **C — memory+communication+delegation** | remember, recall_memory, retrospective, message, handoff, return_to_default, send_file, agent_list, email×5 (config) | LLM (Mia) | /tmp/uat-tools-C |
| **D — tasks** | task_create, task_update (broadened), task_list, task_delete, task_add_todo, task_add_dependency | LLM (Mia) + Playwright (board) | /tmp/uat-tools-D |
| **E — skills+agents+Ava-system** | find_skills, install_skill, system.agent.create/delete/update, system.models.list, system.skill.create/edit (Ava) | LLM (Ava) + Playwright (Agents screen) | /tmp/uat-tools-E |
| **F — admin REST (non-exposed system.*)** | system.workspace.*/channel.*/provider.*/mcp.*/config.*/skill.list+remove+search+install/pin.*/cost/backup/doctor/navigate/task.*/agent.list+metadata+activate+deactivate | REST API + Playwright (Settings/Connectors) | /tmp/uat-tools-F |
| **G — conditional (cron, tool_search, email if no mailbox in C)** | cron, tool_search_tool_regex/bm25 | LLM (after config) + API | /tmp/uat-tools-G |
| **H — UI/Playwright cross-cutting** | board, sidebar workspaces, connectors, settings tabs, agents screen, chat tool-call rendering | Playwright MCP | shares a gateway with D/E/F |

8 subagents, each own gateway (except H shares). Run in parallel; each reports pass/fail per scenario.

---

## Test scenarios

Format: **Tool** — `prompt` (what to send the agent) / `API call` → **expect** → **verify** → [modality].

### Family A — Filesystem + Shell

- **read_file** — Mia: "Read the file /tmp/uat-a/sample.txt and tell me what's in it." → read_file fires, returns content. **Verify:** content matches; edge: a .docx (decoded to text), pagination (offset/length). [LLM]
- **write_file** — Mia: "Write 'hello uat' to /tmp/uat-a/out.txt." → write_file. **Verify:** `cat` the file (via exec or REST media) = "hello uat"; edge: overwrite=true required on existing file (reject without it). [LLM]
- **list_dir** — Mia: "List the files in /tmp/uat-a." → list_dir. **Verify:** listing includes sample.txt, out.txt. [LLM]
- **edit_file / append_file** — NOT LLM-exposed (catalog-only). **API:** no direct REST (file tools are agent-only); verify via the catalog (`GET /tools` lists them) + note they're not agent-callable in v0.1.0 (documented gap). [API/catalog]
- **exec** — Jim (sandbox=enforce): "Run `echo hello-uat` and tell me the output." → exec. **Verify:** stdout "hello-uat"; edge: background session (returns sessionId), then poll/read/kill. **Deny-path:** a command blocked by the binary allowlist → denied with reason. [LLM]
- **workspace.shell** — Jim: "Run `ls -la` in the workspace." → workspace.shell. **Verify:** output is the workspace dir listing; edge: workspace.shell_bg (background) + session management. [LLM]
- **workspace.shell path-escape guard** — Jim: "Run `cat /etc/passwd`" (outside workspace) → workspace.shell → **Verify:** rejected "path escapes workspace". [LLM]

### Family B — Web + Browser

- **web_search** — Mia (DuckDuckGo enabled): "Search the web for 'Omnipus agentic core' and summarize." → web_search. **Verify:** results returned (titles/urls); the summary references them. [LLM]
- **web_fetch** — Mia: "Fetch the page at https://example.com and tell me the title." → web_fetch. **Verify:** content includes "Example Domain". [LLM]
- **web_serve** — Mia: "Serve this HTML: <h1>UAT preview</h1>." → web_serve returns a preview URL. **Verify:** [Playwright] navigate to the preview URL (port 5001/8081) → page shows "UAT preview". [LLM + Playwright]
- **browser.navigate** — Mia: "Navigate to https://example.com and tell me the page title." → browser.navigate. **Verify:** returns title "Example Domain" + final URL. [LLM]
- **browser.click / type / get_text / wait** — Mia: a sequence — "Go to a page with a search box, type 'omnipus', click the search button, wait for results, then read the result text." → browser.navigate→type→click→wait→get_text. **Verify:** each step's result; get_text returns result content. [LLM]
- **browser.screenshot** — Mia: "Take a screenshot of the current page." → browser.screenshot. **Verify:** returns a base64 JPEG (non-empty). [LLM]
- **browser.evaluate** — (browser_evaluate_enabled=true) Mia: "Evaluate `document.title` in the page." → browser.evaluate. **Verify:** returns the title. **Deny-path** (separate gateway with the flag OFF): "Evaluate `1+1`" → denied "disabled — set browser_evaluate_enabled". [LLM, both gated + ungated]
- **browser SSRF guard** — Mia: "Navigate to http://169.254.169.254/" → browser.navigate → **Verify:** rejected by SSRF (metadata IP blocked). [LLM]

### Family C — Memory + Communication + Delegation

- **remember** — Mia: "Remember that the deploy key is 'uat-12345'." → remember. **Verify:** recall returns it (next scenario). [LLM]
- **recall_memory** — Mia (new session): "What do you remember about the deploy key?" → recall_memory. **Verify:** returns 'uat-12345'. [LLM]
- **retrospective** — Mia: "Do a retrospective on this session." → retrospective. **Verify:** returns a reflection summary. [LLM]
- **message** — Mia: "Send a message to Jim: 'please review the docs'." → message. **Verify:** message delivered (Jim's session/heartbeat sees it, or the tool returns success). [LLM]
- **handoff** — Mia: "Hand this off to Jim." → handoff. **Verify:** turn transfers to Jim (next message routed to Jim / handoff event). [LLM]
- **return_to_default** — (after handoff) Jim: "Return to the default agent." → return_to_default. **Verify:** control returns to Mia. [LLM]
- **send_file** — Mia: "Send the file /tmp/uat-a/sample.txt to Jim." → send_file. **Verify:** Jim receives the file ref. [LLM]
- **agent_list** — Mia: "List the available agents." → agent_list. **Verify:** returns Mia/Jim/Ava/Ray (+ subagents) with IDs. [LLM]
- **email (read_inbox/search_email/read_message/send_email/reply)** — (mailbox configured) Mia: "Read my inbox." → read_inbox → **Verify:** returns messages. "Send an email to x@y.com with subject 'uat'." → send_email → **Verify:** sent (or SMTP test server receipt). [LLM] *(Skip if no test mailbox — note as config-gated.)*

### Family D — Tasks (incl. §2 broadened task_update)

- **task_create** — Mia: "Create a task titled 'Review docs', assign it to Jim, priority 2." → task_create. **Verify:** [API] `GET /api/v1/workspaces/{ws}/tasks` includes it; [Playwright] board shows the card. Edge: with `blocked_by`. [LLM + API + Playwright]
- **task_update (broadened — §2)** — multiple scenarios:
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
- **task_list** — Mia: "List my tasks." → task_list. **Verify:** returns the created tasks (role=assignee vs delegator). [LLM]
- **task_delete** — Mia: "Delete task X." → task_delete. **Verify:** gone from board (API + Playwright). [LLM + API]
- **task_add_todo** — Mia: "Add a todo 'write tests' to task X." → task_add_todo. **Verify:** todo on the task (API read-back). [LLM]
- **task_add_dependency** — Mia: "Mark task X blocked by task Y." → task_add_dependency. **Verify:** edge + recompute; cycle rejection. [LLM] *(Deprecated per §2 but still present in v0.1.0 — test it.)*

### Family E — Skills + Ava's system.* tools

- **find_skills** — Mia: "Find a skill for 'docker compose'." → find_skills. **Verify:** returns ClawHub results (slugs/descriptions). *(Needs registry config / network.)* [LLM]
- **install_skill** — Mia: "Install the 'docker-compose' skill." → install_skill. **Verify:** skill installed in workspace (API or filesystem). [LLM]
- **system.agent.create** — Ava: "Create a new Main agent named 'Tester', model glm-5.2." → system.agent.create. **Verify:** [API] `GET /agents` includes 'tester'; [Playwright] Agents screen shows it. [LLM + API + Playwright]
- **system.agent.update** — Ava: "Rename agent 'tester' to 'Tester2'." → system.agent.update. **Verify:** renamed. [LLM + API]
- **system.agent.delete** — Ava: "Delete agent 'tester'." → system.agent.delete. **Verify:** gone from roster + Agents screen. [LLM + API + Playwright]
- **system.models.list** — Ava: "List the available models." → system.models.list. **Verify:** returns the configured provider's models. [LLM]
- **system.skill.create** — Ava: "Create a local skill named 'uat-skill' with body 'echo uat'." → system.skill.create. **Verify:** skill file exists. [LLM]
- **system.skill.edit** — Ava: "Edit skill 'uat-skill' body to 'echo updated'." → system.skill.edit. **Verify:** updated. [LLM]

### Family F — Admin REST (non-LLM-exposed system.*)

These 38 tools are admin/operator operations exposed via REST + UI, not agent-prompted. Test the
REST endpoints (the UI's path) + Playwright the UI screens.

- **system.workspace.*** — `POST/GET/PUT/DELETE /api/v1/workspaces`. Create/list/get/update/delete a workspace. **Verify:** API responses + [Playwright] sidebar workspace switcher shows/updates. [API + Playwright]
- **system.channel.*** — `GET /api/v1/channels`; configure/enable/disable/test via `PUT /api/v1/channels/{id}`. **Verify:** [Playwright] Connectors screen. **Note:** enable/disable/test are stubs (§6) — assert they return the honest NOT_IMPLEMENTED message. [API + Playwright]
- **system.provider.*** — `GET /api/v1/providers`; `POST /providers/{id}/test`. **Verify:** provider list + test result. [API + Playwright Settings]
- **system.mcp.*** — `GET/POST /api/v1/mcp-servers`; `POST /mcp-servers/{id}/test`; `PATCH /mcp-servers/{id}` (§437). **Verify:** add a stdio MCP server → test connects → list shows real status+tool_count → edit (PATCH) → remove. [API + Playwright Skills/MCP tab]
- **system.config.get/set** — `GET/PUT /api/v1/config` (dot-path). **Verify:** get a value; set gateway.log_level → get reflects it. [API]
- **system.skill.list/remove/search/install** — `GET/DELETE /api/v1/skills` etc. **Verify:** list/remove/search (ClawHub). [API + Playwright Skills tab]
- **system.pin.*** — `GET/POST/DELETE /api/v1/pins` (if a REST exists; else note no-UI per §5). **Verify:** CRUD. [API] *(pins have no UI — §5; may be retired v0.3.)*
- **system.cost.query** — stub (§6). **Verify:** returns NOT_IMPLEMENTED. [API]
- **system.backup.create** — stub (§6). **Verify:** returns NOT_IMPLEMENTED. [API]
- **system.doctor.run** — `GET /api/v1/.../doctor` (or CLI). **Verify:** returns health checks. [API]
- **system.navigate** — (agent-callable but admin) drive the SPA. **Verify:** [Playwright] the UI navigates to the named screen. [Playwright]
- **system.task.*** (admin variants) — `GET/POST/PUT/DELETE /api/v1/.../tasks`. **Verify:** board CRUD via REST. [API + Playwright]
- **system.agent.list/read_metadata/write_metadata/activate/deactivate** — `GET/PUT /api/v1/agents`. **Verify:** list, read/write metadata (persona), activate/deactivate. [API + Playwright Agents screen]

### Family G — Conditional (cron, tool_search)

- **cron** — (cron.enabled) Mia: "Remind me in 1 minute to check the build." → cron (at_seconds=60). **Verify:** cron job created (`GET /api/v1/.../cron` or jobs.json); fires after 60s. Edge: every_seconds recurring; allow_command gated. [LLM + API]
- **tool_search_tool_regex / bm25** — (MCP discovery enabled) Mia: "Search for a tool to read a file." → tool_search. **Verify:** returns read_file's schema. [LLM]

### Family H — UI / Playwright cross-cutting

- **Board** — after task_create/update/delete (Family D): the board card appears, updates (status/column), disappears. [Playwright]
- **Sidebar workspaces** — after workspace create/delete (Family F): sidebar switcher updates. [Playwright]
- **Connectors screen** — after channel configure/enable/disable (Family F): the channel row updates. [Playwright]
- **Settings tabs** — providers, security, gateway, integrations render; config changes (system.config.set) reflect. [Playwright]
- **Agents screen** — after agent create/delete (Family E): roster updates. [Playwright]
- **Chat tool-call rendering** — when an agent calls a tool (any LLM scenario), the tool call renders inline (collapsible), with the result. [Playwright]
- **Delegation-denied label** — when a delegation is denied (Family D reassign to disallowed agent), the collapsed tool call shows "Delegation denied · <reason>" (G17 fix). [Playwright]

---

## Prerequisites & config-gated tools (per family)

| Tool | Prerequisite to be testable |
|------|------------------------------|
| exec, workspace.shell | `sandbox.mode=enforce` (else fail-closed unregistered) |
| web_search, web_fetch | a search provider (DuckDuckGo = no key; set `tools.web.search.duckduckgo_enabled`) |
| browser.* | Chromium present (`chromium-browser`); `browser_evaluate_enabled=true` for browser.evaluate |
| email.* | a test IMAP/SMTP mailbox (or skip — note as config-gated) |
| mcp tools + tool_search | a stdio MCP test server (e.g. `mcp-everything`) + `tools.mcp.discovery.enabled` |
| cron | `tools.cron.enabled=true` |
| find_skills/install_skill | ClawHub registry reachable (network) |
| system.* admin (REST) | `dev_mode_bypass=true` or onboarded admin token |

## Pass/fail criteria

- **PASS:** the agent invokes the correct tool (verified in the WS tool-call frame / tool result),
  AND the outcome is correct (side-effect verified via API/filesystem/UI). Edge-case scenarios
  (cycle rejection, SSRF block, path-escape, deny-by-default for browser.evaluate, stubs returning
  NOT_IMPLEMENTED) must behave as specified.
- **FAIL:** wrong tool / no tool invoked, tool errors unexpectedly, side-effect missing/wrong, or
  an edge-case guard doesn't fire.
- **Coverage target:** every one of the 87 catalog tools has ≥1 scenario (LLM, API, or Playwright).
  Stubs (§6: channel enable/disable/test, cost, backup) PASS by returning the honest
  NOT_IMPLEMENTED message. Config-gated tools (email without a mailbox) are marked SKIP-with-reason.

## Execution

8 parallel subagents (A–H), each its own gateway (H shares). Each runs its family's scenarios,
reports pass/fail/skip per tool. Aggregated into a UAT report. Re-run any FAIL after a fix.
Playwright scenarios (B web_serve, D board, E Agents screen, F Settings/Connectors, H) need the
Playwright MCP connected to the subagent's preview URL.
