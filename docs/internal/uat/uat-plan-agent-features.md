# UAT Plan: Human-Impersonated Exploratory Testing via Playwright

**Date:** 2026-06-20 · **Revised:** 2026-06-23
**Scope:** All agent-related features — agent CRUD, running/chat, delegation, task management, boards, scheduled tasks, automations — **plus (2026-06-23 revision):** Settings → Security (deep), MCP server configuration, and chat file/image/video upload.
**Method:** LLM impersonates a human user; drives the SPA through Playwright MCP; takes screenshots at every step; reports bugs, UX issues, and coverage gaps
**Provider:** OpenRouter (`z-ai/glm-5-turbo`) — real LLM responses for agent running/delegation tests

> **2026-06-23 revision note.** Journeys 11–13 (Security, MCP, Upload) were added after a code-level validation pass + a live smoke against a booted gateway. Three things to keep in mind when running the *original* Journeys 1–10:
> - **Roster re-cast:** the seed roster is now **4 base agents** (Mia · Assistant ⭐, Jim · **Planner & Orchestrator**, Ava · Builder, Ray · Scout) **+ 4 delegation-only workers** (Worker, Planner, Explorer, Researcher). **Max is retired.** (Journey 2 still says "5 agents incl. Worker" — stale.)
> - **Workspaces IA:** chat/tasks/schedules are **workspace-scoped**. `/tasks`, `/command-center`, `/automations` now **redirect** into workspace tabs (Board/Calendar); there is **no global Tasks screen** and **no "Channels" screen** (it's **Connectors**). Journeys 7–8 describe a pre-Workspaces product and are largely obsolete — the board lives at `/workspaces/$id/board` with tabs Board/List/Graph/Calendar/Team (no "Execution"), and the "two task systems" + "automations dead-end" framings no longer apply (unified Task entity; schedules in Agent Profile → Schedules + workspace Calendar).
> - The 5 original "Known Issues" (cli_path, GTD WS frame, heartbeat global, command-center dead-end, GTD `/start` 403) are all **fixed or superseded** — verify-and-close rather than reproduce.

---

## Goal

I (the LLM) impersonate a human user. I boot the gateway, complete onboarding through the browser, then **use the app like a real person would** — clicking through every screen, creating agents, chatting, delegating, managing tasks, scheduling automations. At each step I take screenshots and note:

- **Bugs** — things that break or don't work
- **UX issues** — things that are confusing, unintuitive, "wired"
- **Coverage gaps** — functionality that exists in the API but the UI doesn't expose, or exposes poorly

**The key question that must be answered:** Does the UI cover all functionality in a good way, including the delegation graph and enforcement?

---

## Pre-requisites (executed before Playwright)

1. Boot gateway on `0.0.0.0:8080` with OpenRouter key configured
2. Confirm `$DEVPOD_PREVIEW_URL` is reachable

---

## User Journey (Sequential — one human clicking through)

### Journey 1: First Launch & Onboarding

- Navigate to the app → screenshot
- Walk through onboarding (name, password, model key) → screenshot each step
- Land on the "Meet your Assistant" screen → screenshot
- **Assess:** Is the onboarding flow smooth? Are instructions clear? Any friction?

### Journey 2: Exploring the Default Roster

- Land on chat with Mia → screenshot
- Navigate to Agents screen → screenshot the 5 seed agents
- Click into each agent profile (Mia, Jim, Ava, Ray, Worker) → screenshot each
- **Assess:** Can I tell the difference between agent types? Is the lock on core agents clear? What can I edit vs not?

### Journey 3: Creating a Custom Agent

- Open the create-agent wizard → screenshot each step
- Fill in identity, personality (SOUL.md), tools → screenshot
- Create a Main agent → screenshot result
- Create a Subagent → screenshot (does it ask for description? is the type distinction clear?)
- Try creating a subagent_3p (external CLI) → screenshot (is the executor selector clear? does CLI detect work?)
- **Assess:** Is the wizard intuitive? Are required fields obvious? Does the type selector explain what each type does?

### Journey 4: Configuring Agent Delegation

- Go to Jim's profile → find delegation settings → screenshot
- Navigate to `/agents/trust` (delegation graph) → screenshot
- **Key question:** Does the graph render? Is it readable? Can I see who can delegate to whom?
- Try editing Jim's delegation policy from the UI → screenshot (can I add/remove trusted agents? set modes? set depth?)
- Try editing from the agent profile → screenshot
- **Assess:** Is the delegation graph understandable? Can I modify trust relationships from the UI? Is enforcement visible (does the UI tell me when delegation is denied and why)?

### Journey 5: Chatting & Handoff

- Start a chat with Mia → send a message → screenshot streaming response
- Ask Mia to hand off to Jim → screenshot the transition
- **Key question:** Is the handoff obvious? Does the UI clearly show which agent is active?
- Try sending to a worker (if UI allows) → screenshot (does it prevent this?)
- Cancel a response mid-stream → screenshot
- **Assess:** Streaming UX, tool call visibility, cancel discoverability, agent switching clarity

### Journey 6: Delegation in Action

- Ask Jim to delegate a task to Ava → screenshot
- **Key question:** Do I see `subagent_start`/`subagent_end` brackets? Is it clear a sub-agent ran?
- Ask Jim to delegate to an agent he CAN'T delegate to → screenshot
- **Key question:** Does the UI show a delegation-denied message? Is it understandable?
- Try to trigger depth-limit exceeded → screenshot
- **Assess:** Is delegation enforcement visible to the user? Or does it silently fail?

### Journey 7: Task Board

- Navigate to tasks/board → screenshot the kanban
- Create a task → screenshot the form
- Assign it to an agent → screenshot
- Set up a blocked_by dependency → screenshot (can I do this from the UI? is the DAG visible?)
- Start a task → screenshot (does it run the agent? is that clear?)
- Switch between Board/List/Execution views → screenshot each
- **Assess:** Are GTD column names clear? Is the two-task-systems (GTD vs workflow) distinction confusing? Can I see dependencies? Is "Start" obviously "run an agent"?

### Journey 8: Schedules & Automations

- Navigate to Automations screen → screenshot
- **Key question:** It says "read-only — manage from Command Center" — but Command Center redirects to /tasks. Can I actually find where to create a schedule?
- Find the schedule form (Agent Profile → Schedules) → screenshot
- Create a one-shot schedule → screenshot
- Create a cron schedule → screenshot (is the cron builder intuitive or scary?)
- Run a schedule now → screenshot
- Pause/resume → screenshot
- **Assess:** Schedule creation discoverability, trigger builder UX, session mode clarity, failure notification visibility

### Journey 9: Settings & Tool Registry

- Navigate to Settings → screenshot each tab
- Find the per-agent tool policy UI → screenshot (allow/ask/deny per tool)
- Try changing a tool policy → screenshot (does it require re-auth? is that clear?)
- Check sandbox settings → screenshot
- **Assess:** Are settings overwhelming? Is the tool policy UI understandable? Is re-auth flow smooth?

### Journey 10: Edge Cases & Error States

- Try to delete a locked agent → screenshot (what's the error message?)
- Try to set a worker as default → screenshot
- Try to create an agent with invalid data → screenshot (validation messages)
- Try to put a bad cron expression → screenshot
- Check empty states (no tasks, no schedules, no notifications) → screenshot
- **Assess:** Are error messages helpful? Are empty states guiding?

---

## (2026-06-23 revision) New Journeys 11–13

These cover surfaces the original plan never tested. Each lists the route, the
exact controls to exercise, the **expected** behaviour, and the **known gaps to
confirm** (from a code-level validation pass — cite `file:line` in the report).

### Journey 11: Settings → Security (deep)

Route: `/settings` → **Security** tab. (Sandbox / God-mode / Global tool policy /
Credential vault live under the **Advanced / technical details** disclosure further
down the tab.)

- Screenshot the Security tab: **Security Health** score, **Agent tool access**
  (Must ask first / Run freely), **Shell command approval**, **Daily spending
  limit**, **Skill Trust**, **Prompt Guard** → screenshot each.
- Expand **Advanced / technical details** → screenshot **Process Sandbox**
  (mode enforce/permissive/off, default profile, allowed paths [read-only],
  **SSRF / internal-CIDR** policy, shell deny-patterns), **Global tool policies**
  (default + per-tool allow/ask/deny), **God-mode** toggle, **Credential vault**,
  **Audit log** (View).
- **Re-auth (password step-up) probe — the core of this journey.** Change each of
  the following and record whether a password step-up dialog appears:
  - God-mode toggle · Sandbox config · Global tool policies → **expect re-auth**.
  - **Switch policy_mode (Deny → Allow)** → **expect re-auth, but currently NONE** (gap).
  - **Disable audit logging** → **expect re-auth, but currently NONE** (gap).
  - **Add / delete a credential** → **expect re-auth, but currently NONE** (gap).
- Open the **Audit log** viewer → confirm events render; **note the absence of any
  HMAC chain-integrity indicator** (gap).
- **Global-override interaction (links to Journey 3/9):** set a tool to **Deny** (or
  **Ask**) in Global tool policies, then open an agent's **Tools** (create wizard
  Step 3 *and* edit slide-over) → confirm the contradicting **Allow** button is
  **disabled** with a lock badge "Global: Deny/Ask" linking to Settings → Security.
- **Assess / Known gaps to confirm:** (KI-6) policy_mode change not re-auth-gated;
  (KI-7) audit-log disable not re-auth-gated; (KI-8) credential add/delete not
  re-auth-gated; (KI-9) no audit chain-integrity surfaced; (KI-10) master-key /
  credential **rotation is CLI-only**, not in the UI. God-mode requires the
  `--allow-god-mode` server flag (toggle is disabled otherwise) — verify the
  disabled-state copy is clear.

### Journey 12: MCP Server Configuration

Route: `/skills` → **MCP Servers** tab.

- Screenshot the empty state ("No MCP servers connected") + **Add Server**.
- **Add a local (stdio) server:** switch to "Local program", clear the **stdio
  safety confirmation** dialog ("runs a program on your server"), fill name +
  command (`npx`) + args + env → submit → screenshot. Confirm it appears in the list.
- **Add a remote (http/sse) server:** name + **https** URL → submit. Confirm
  **http non-localhost is rejected** and **RFC1918 addresses show an SSRF caution**.
- **Duplicate name** → expect 409 conflict toast. **Delete** → confirm dialog →
  removed.
- **MCP tools reach BOTH tool lists (must verify):**
  - **Per-agent:** open an agent's Tools editor (create wizard **Step 3** *and* the
    edit slide-over) → confirm the configured server's tools appear grouped under
    **"MCP server tools"** by server, each with allow/ask/deny.
  - **Global:** Settings → Security → Advanced → **Global tool policies** → confirm
    the same MCP tools appear there too (they should — `GET /api/v1/tools` returns
    builtin **and** MCP; the editor's `builtinTools` var is a misleading legacy name).
- **allow / ask / deny mechanism works for MCP tools (must verify end-to-end):**
  - Set an MCP tool to **Deny** globally → confirm the per-agent **Allow** control
    for it is **locked** (global deny > per-agent), and confirm the agent **cannot
    call it** (denied/invisible — filtered before the LLM sees it; check it does
    not run when asked).
  - Set it to **Ask** → confirm a permission prompt on use.
  - Set it back to **Allow** (per-agent, no global floor) → confirm the agent can use it.
  - Use an **exact tool name** to deny — a `mcp_*`/`mcp.*` **wildcard will NOT work**
    (KI-22): MCP names are underscore single-segments and the matcher only does
    dot-segment wildcards, so there is no bulk-deny-by-server.
  - This exercises the deny>ask>allow resolution (`pkg/tools/compositor.go`) for an
    MCP-namespaced tool and the global-override lock for MCP rows.
- **Assess / Known gaps to confirm:** (KI-11) list **status is always
  "disconnected"** and **tool_count always 0** — the REST list never queries the
  live MCP manager; (KI-12) **no connection-test** endpoint (bad configs fail
  silently at agent-loop start); (KI-13) **no edit/PATCH and no enable/disable** —
  must delete + re-add; (KI-14) **no UI for HTTP headers** (can't configure
  header-auth remote servers), env-file, or per-tool admin-ask; (KI-15) **no
  per-agent MCP-server scoping** (all agents see all MCP tools); (KI-16) tool
  namespace collision if a server name contains an underscore.
- **Automated-test backing (validate + close):** the production path works, but the
  **MCP-specific unit coverage is thin** — see the "Unit-test coverage" appendix.
  Add: backend MCP-tool policy-resolution + enforcement tests
  (`pkg/tools/compositor_test.go`, `pkg/agent/*`), `GET /api/v1/tools` includes-MCP
  assertion (`pkg/gateway/rest_tool_registry_test.go`), and frontend allow/deny +
  global-override-lock tests for MCP rows + the global editor
  (`ToolPolicyEditor.test.tsx`, `SecuritySection.test.tsx`).

### Journey 13: Chat Upload — image / file / video

Route: chat (`/` → default workspace Chat). Use the composer **Attach** button
(`data-testid="add-attachment"`); the file chooser's `accept` list is
`image/*, .pdf, .docx, .pptx, .xlsx, .txt, .md, .csv, .json, .log, .yaml, .yml`.

- **Image (.png/.jpg):** attach → confirm thumbnail chip in composer → send →
  confirm it uploads (`POST /api/v1/upload` → `uploads/<session>/…`) and **reaches
  the model**. With a non-vision model (e.g. glm-5-turbo) the agent should reply
  with a clear "I can't view images with the current model — switch to one that
  supports image input" (verified live). **Also confirm the image renders as a
  thumbnail in the *sent* user bubble** (a degenerate 2×2 test image did **not**
  render visibly while a file card did — re-confirm with a normal image; possible
  sent-message render gap, KI-19).
- **File (.txt/.pdf):** attach → confirm **file card** ("name / TYPE") in composer
  and in the sent message → send → confirm persisted to disk.
- **Video (.mp4):** attempt to attach. **Expected (current): NOT supported** — the
  `accept` list excludes all video, so a real user cannot pick one; forcing an mp4
  through triggers an adapter rejection ("File type video/mp4 is not accepted") that
  surfaces as an **uncaught error, not a user-facing toast** (verified live).
- **Assess / Known gaps to confirm:** (KI-17) **video upload entirely unsupported**
  in chat (no `accept` entry, no affordance) — decide intended vs. gap; (KI-18)
  **disallowed/forced file types reject without a graceful toast** (drag-drop a
  blocked type → no readable feedback); (KI-19) image thumbnail may not render in
  the sent user bubble (file cards do); (KI-20) **audio** uploads + renders as a
  file card but is **never passed to the model as audio**; (KI-21) **no client-side
  upload progress / retry UI** (100 MB/file server limit only).

---

## Key Questions to Answer (Explicitly in Report)

1. **Does the UI cover all agent functionality?** — CRUD, running, delegation, tools, sandbox, heartbeat, external CLI. What's missing?
2. **Is the delegation graph good?** — Does it render? Is it readable? Can I modify trust from the UI or only view it? (Note: there are now **two** delegation surfaces — `/agents/trust` and the per-workspace **Team** tab.)
3. **Is delegation enforcement visible?** — When a delegation is denied, does the user understand why? Or is it a silent failure?
4. ~~**Two task systems** (GTD vs workflow)~~ — **OBSOLETE**: unified into one Task entity. Instead ask: is the single 7-state lifecycle clear across Board/List/Graph/Calendar?
5. ~~**Automations read-only dead-end**~~ — **OBSOLETE**: Automations redirects to the workspace Calendar; schedules live in Agent Profile → Schedules. Ask instead: is schedule creation discoverable now?
6. ~~**Heartbeat is global**~~ — **OBSOLETE**: heartbeat is genuinely per-agent now. Ask instead: is the per-agent heartbeat clear, and correctly hidden for workers?

**New key questions (2026-06-23):**

7. **Is security re-auth gating consistent?** — Which sensitive changes prompt for a password and which don't? (Expect gaps on policy_mode, audit-disable, credential add/delete — KI-6/7/8.)
8. **Can a user trust the MCP server list?** — Does it show *real* connection status and tool counts, or always "disconnected / 0"? Can they test/edit a server, or only add/delete? (KI-11–13.)
9. **Does the global-override lock read clearly?** — When a tool is globally Deny/Ask, do the per-agent Allow controls visibly lock with an explanation + link?
10. **Is chat upload complete?** — Image and file work; is **video** supported, and when a file type is rejected does the user get a clear, graceful message (not a silent error)? (KI-17/18.)

---

## Known Issues to Verify (from codebase analysis)

These are suspected bugs/gaps the UAT should confirm.

**Original set (1–5) — now FIXED or SUPERSEDED (2026-06-23). Verify-and-close, do not expect to reproduce:**

1. ~~`cli_path`/`env_overrides`/`cli_args` not consumed by dispatch~~ — **FIXED**: consumed end-to-end (`pkg/agent/external_dispatch.go:169-171`).
2. ~~GTD board tasks have no WS frame~~ — **FIXED**: unified Task store; `task_status_changed` invalidates `['tasks']`.
3. ~~Heartbeat is global, not per-agent~~ — **FIXED**: genuinely per-agent (`AgentConfig.HeartbeatEnabled/Interval` + migration).
4. ~~`/command-center` redirects to `/tasks` / Automations dead-end~~ — **SUPERSEDED**: redirects into workspace Board/Calendar; schedules in Agent Profile → Schedules.
5. ~~GTD `active` only via `/start` (403)~~ — **FIXED**: 7-state lifecycle; `in_progress` set via PATCH/drag, no `/start` gate.

**New set (6–21) — from the 2026-06-23 validation; confirm live and cite `file:line`:**

*Security (Journey 11):*
6. **policy_mode change not re-auth-gated** (HIGH) — `PUT /api/v1/config` for Deny→Allow needs no password step-up (`SecuritySection.tsx:334`).
7. **Audit-log disable not re-auth-gated** (HIGH) — `PUT /api/v1/security/audit-log` has no `requireReAuth` (`rest_audit_log.go`).
8. **Credential add/delete not re-auth-gated** (MED) — `POST/DELETE /api/v1/credentials`.
9. **No audit chain-integrity indicator** in the viewer (HMAC chain not surfaced).
10. **Master-key / credential rotation is CLI-only** (not exposed in the vault UI).

*MCP (Journey 12):*
11. **MCP list status always "disconnected" + tool_count always 0** (`rest.go:5065-66`) — live state never queried.
12. **No MCP connection-test endpoint** — POST doesn't validate; bad configs fail silently.
13. **No MCP edit/PATCH and no enable/disable** — delete + re-add only.
14. **No UI for MCP HTTP headers / env-file / per-tool admin-ask.**
15. **No per-agent MCP-server scoping** — all agents see all MCP tools.
16. **MCP tool namespace collision** if a server name contains an underscore (`mcp_<server>_<tool>` parsing).
22. **MCP tools can't be wildcard-denied** (MED) — runtime names are underscore single-segments (`mcp_<server>_<tool>`); the policy matcher only does dot-segment `.*` wildcards, so no `mcp_*`/`mcp.*` ever matches. Only exact-key deny works → no bulk-deny of a server's tools. Characterized by `compositor_mcp_policy_test.go::TestFilterToolsByPolicy_MCPTool_WildcardDoesNotMatch_ExactKeyRequired`.

*Upload (Journey 13):*
17. **Video upload entirely unsupported** in chat (excluded from `accept`; no affordance) — intended vs. gap?
18. **Disallowed/forced file types reject without a graceful toast** (uncaught error instead).
19. **Image thumbnail may not render in the sent user bubble** (file cards do) — re-confirm with a normal image.
20. **Audio uploads/renders as a file card but is never passed to the model as audio.**
21. **No client-side upload progress / retry UI** (100 MB/file server limit only).

---

## Report Format

Output: `docs/internal/uat/uat-report-agent-features-2026-06-23.md`

Structure:
- **Executive summary** — overall impression, key findings
- **Journey-by-journey walkthrough** — screenshots + commentary per step
- **Bug list** — severity, description, screenshot, steps to reproduce
- **UX issues** — what's confusing/wired, with screenshots and recommendations
- **Coverage gaps** — API functionality not exposed in UI
- **Answers to the key questions** above (note items 4–6 are now obsolete; cover 1–3 and the new 7–10)

**Severity levels:** Critical (blocks core function), Major (feature broken but workaround exists), Minor (cosmetic/edge case), UX (usability concern).

---

## Execution Approach

1. **Phase 0 (bash):** Boot gateway, configure OpenRouter, confirm reachable — ~2 min
2. **Journeys 1–13 (Playwright MCP):** Sequential — one human clicking through. Screenshots at every step. ~60–75 min total. Journeys 7–8 are largely obsolete (Workspaces re-cast) — run them only to confirm the redirects/IA, not the old flows.
3. **Report:** Compile findings + screenshots into markdown — ~10 min

No parallel subagents for the Playwright journeys (one browser, one human).

**Harness note (2026-06-23):** for upload + cancel-style journeys, do **not** rely on the URL for the session id — the workspace-scoped chat IA keeps the page at `/#/workspaces/<id>/chat`. Discover the session by diffing `OMNIPUS_HOME/sessions/` before/after a turn (see `tests/e2e/cancel-cross-channel.spec.ts::listSessionDirs`). The composer's file input is opened via a `filechooser` event (AssistantUI `AddAttachment`), not a persistent `input[type=file]`.

---

## Appendix: Unit-test coverage for MCP tools → policy (validated 2026-06-23)

The production path works end-to-end: a configured MCP server's tools land in the
tool registry with `source=mcp`, `GET /api/v1/tools` returns them (so they show in
BOTH the per-agent editor and the global Settings→Security editor), and the
source-agnostic compositor (`pkg/tools/compositor.go`, deny>ask>allow) resolves
allow/ask/deny for them and filters denied tools before the LLM. The MCP-specific
unit coverage gaps below were **closed on 2026-06-23** (25 new tests).

**KI-22 (finding from writing these tests): MCP tools cannot be wildcard-denied.**
Runtime MCP tool names are underscore-delimited single segments
(`mcp_<server>_<tool>`, via `MCPTool.Name()` — `sanitizeIdentifierComponent` strips
dots), but the policy matcher (`resolveFromMap`) only supports trailing `.*` keys
matched on **dot** segments. So no `.*` wildcard can ever match an MCP tool — the
**only** way to deny one (globally or per-agent) is an **exact key**. Consequence:
you can't bulk-deny all of a server's MCP tools with a single wildcard; each tool
must be denied individually. (Whether to make the matcher support `mcp_<server>_*`
is a product decision — currently characterized, not "fixed".)

**Backend (Go) — now COVERED:**
- ✅ MCP registry add/remove/collision/rename/admin-ask — `pkg/tools/mcp_registry_test.go`.
- ✅ MCP-tool policy resolution (exact deny, global-deny-over-agent-allow, admin-ask fence, allowed control, scope gate) + the **wildcard-does-not-match characterization** — `pkg/tools/compositor_mcp_policy_test.go` (7 tests).
- ✅ MCP-tool deny enforcement at `FilterToolsByPolicy` (the loop's LLM-assembly gate) incl. a registry round-trip — `pkg/tools/mcp_policy_test.go` (2 tests).
- ✅ `GET /api/v1/tools` includes MCP entries with `source=mcp`, and builtin-first dedup — `pkg/gateway/rest_tool_registry_mcp_test.go` (2 tests).

**Frontend (vitest) — now COVERED:**
- ✅ MCP tools render grouped by server — `ToolPolicyEditor.test.tsx` (pre-existing).
- ✅ allow/ask/deny on an MCP row fires `onChange` with the MCP tool name; **exact-key** global deny/ask **locks** the MCP row's controls; lock doesn't bleed to siblings — `ToolPolicyEditor.test.tsx` (new `describe('MCP tool policy controls')`, 8 tests).
- ✅ The **global** Settings→Security editor renders MCP tools in the `mcp-tools-section` and changing one fires `updateGlobalToolPolicies` through the re-auth gate — `SecuritySection.test.tsx` (new `describe('MCP tools in GlobalToolPoliciesSection')`, 6 tests).

All 25 added tests pass (backend scoped runs with `-tags goolm,stdjson`; frontend
`vitest run` — 68 in the two files). No production code changed.
