# UAT Plan: Human-Impersonated Exploratory Testing via Playwright

**Date:** 2026-06-20
**Scope:** All agent-related features — agent CRUD, running/chat, delegation, task management, boards, scheduled tasks, automations
**Method:** LLM impersonates a human user; drives the SPA through Playwright MCP; takes screenshots at every step; reports bugs, UX issues, and coverage gaps
**Provider:** OpenRouter (`z-ai/glm-5-turbo`) — real LLM responses for agent running/delegation tests

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

## Key Questions to Answer (Explicitly in Report)

1. **Does the UI cover all agent functionality?** — CRUD, running, delegation, tools, sandbox, heartbeat, external CLI. What's missing?
2. **Is the delegation graph good?** — Does it render? Is it readable? Can I modify trust from the UI or only view it?
3. **Is delegation enforcement visible?** — When a delegation is denied, does the user understand why? Or is it a silent failure?
4. **Two task systems** — GTD board tasks vs workflow tasks. Is this distinction clear? Which should a user use? Is it confusing?
5. **Automations read-only dead-end** — The Automations screen says "manage from Command Center" but that redirects to /tasks. Can a user actually find where to create schedules?
6. **Heartbeat is global** — The UI shows it per-agent but it's actually global. Is this misleading?

---

## Known Issues to Verify (from codebase analysis)

These are suspected bugs/gaps the UAT should confirm:

1. **`cli_path`/`env_overrides`/`cli_args` not consumed by dispatch** — drivers hardcode binary names. Setting custom paths round-trips through the API but doesn't take effect at runtime.
2. **GTD board tasks have no WS frame** — only workflow tasks emit `task_status_changed`. SPA uses polling for GTD. The WS handler invalidates `['tasks']` (workflow key), not `['board-tasks']`.
3. **Heartbeat is global, not per-agent** — the wire fields `heartbeat_enabled`/`heartbeat_interval` surface on every agent but write to a single global config. Setting it on one agent changes it for all.
4. **`/command-center` redirects to `/tasks`** — but the actual schedule creation UI is in Agent Profile → Schedules tab. The Automations screen says "manage rules from the Command Center" but Command Center no longer exists as a route.
5. **GTD `active` status only via `/start`** — PUT with `status:"active"` returns 403. Is this discoverable in the UI?

---

## Report Format

Output: `docs/internal/uat/uat-report-agent-features-2026-06-20.md`

Structure:
- **Executive summary** — overall impression, key findings
- **Journey-by-journey walkthrough** — screenshots + commentary per step
- **Bug list** — severity, description, screenshot, steps to reproduce
- **UX issues** — what's confusing/wired, with screenshots and recommendations
- **Coverage gaps** — API functionality not exposed in UI
- **Answers to the 6 key questions** above

**Severity levels:** Critical (blocks core function), Major (feature broken but workaround exists), Minor (cosmetic/edge case), UX (usability concern).

---

## Execution Approach

1. **Phase 0 (bash):** Boot gateway, configure OpenRouter, confirm reachable — ~2 min
2. **Journeys 1-10 (Playwright MCP):** Sequential — one human clicking through. Screenshots at every step. ~45-60 min total.
3. **Report:** Compile findings + screenshots into markdown — ~10 min

No parallel subagents for the Playwright journeys (one browser, one human).
