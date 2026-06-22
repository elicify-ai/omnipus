# Omnipus 0.1.0 — Full UAT Report (human-impersonation, Playwright)

**Date:** 2026-06-21 · **Target:** `hotfix/v0.1.1` (complete 0.1.0 line) · **Method:** 8 parallel subagents,
each its own isolated gateway + headless Chromium, each impersonating a named human persona, screenshotting
every step and reporting bugs + UX issues + **how it felt** (usability + ship-readiness). Plan:
`uat-plan-0.1.0-full.md`. Screenshots: `docs/internal/uat/screenshots/group-{1..8}/`.

## Readiness scores (testers' own 1–5 + ship verdict)
| G | Persona | Area | Score | Ship today? |
|---|---------|------|-------|-------------|
| 1 | Dana (first-timer) | Onboarding + first chat | **2** | no |
| 2 | Marcus (PM) | Workspace IA / 7 tabs | **4** | with-fixes |
| 3 | Priya (task-driver) | Tasks (Board/List/Graph/Calendar) | **2** | no |
| 4 | Sam (orchestration) | Delegation + Team graph | **3** | with-fixes |
| 5 | Lee (builder) | Agents + model selector | **4** | with-fixes |
| 6 | Robin (security op) | Settings + god-mode | **3** | with-fixes |
| 7 | Yuki (integrator) | Connectors + email + skills | **4** | with-fixes |
| 8 | Alex (breaker) | Edge/error states | **4** | with-fixes |

**Aggregate readiness ≈ 3.25 / 5 — "strong bones, premium shell, a few real gaps before public GA."**

## What testers consistently loved (the product's strengths)
- **The Team delegation graph (React Flow)** — rated 9/10 by two testers. Drag-to-delegate, click-edge-to-tune-modes/depth, click-node→AgentProfile, pre-populated roster. "The best agent-topology UI I've seen." The marquee feature lands.
- **Workspace-as-project IA** — the sidebar (Workspaces + Library) mental model "clicked immediately"; the 7-tab container is clear.
- **Sovereign Deep aesthetic** — "confident, distinctive, premium"; the octopus "Meet Mia" intro delighted the first-timer.
- **Graph (Task DAG) empty-state copy** — "the best empty state in the app"; the branded **404 page** ("drifted into the deep") — "legitimately premium."
- **Connectors/email secret handling** — "one of the safest-feeling credential flows in a self-hosted tool" (write-only fields, "stored encrypted, never shown", Test-before-Save, provider-dashboard links).
- **Settings security-score card**, the **agent creation wizard** (form-by-type, sandbox editable on locked core agents), and a **very low console-error count** across 11 screens.

## Cross-cutting issues (seen by multiple testers — fix first, high leverage)
1. **`UNRESOLVED` badge on the chat model selector** (G2, G4, G8) — bright/alarming, no tooltip, appears on every chat though the model works. Internal "slug not resolved against catalog" state leaking to users. *Fix: suppress when the provider is connected, or soften + tooltip.* (frontend)
2. **`401 GET /api/v1/system/cli-detect`** on every Agents-screen load (G1, G2, G4, G5, G8) — drives the persistent "Could not detect external CLIs" banner. The request isn't sending the auth token. *Fix: send auth (the S1 MIN-9 gate is incomplete) or make the endpoint optional-auth.* (frontend/gateway)
3. **List empty state "No tasks match the current filters"** on a zero-task workspace (G1, G2, G3, G8) — implies a filter is applied when none is. *Fix: show "No tasks yet" when filters are default.* (frontend)
4. **No active-workspace name visible when the sidebar is closed** (G2) — two workspaces look identical. *Fix: breadcrumb/title near the tab bar.* (frontend)

## Per-area findings

### Onboarding (G1) — score 2, the weakest first-run
- **CRITICAL:** the model dropdown auto-selects `~anthropic/claude-fable-latest` (first alias) — a **404 on OpenRouter** → the very first chat fails with a **raw JSON error blob** that also **leaks the OpenRouter `user_id`**. A new user's first message breaks. *Fix: pre-select a known-good cheap model; render LLM errors as a friendly message + "switch model" CTA; never dump raw JSON / user_id.*
- **MAJOR/UX:** Step 3 offers 15 providers with no guidance on what an API key is or where to get one; the model list has no "recommended" and opaque `~` aliases. No app value-prop before setup. *(The "Meet Mia" close screen, by contrast, was the highlight — bring that warmth earlier.)*

### Tasks (G3) — score 2
- **Real UI-completeness gaps:** the **task create form + detail panel are missing the Trigger (None/Once/Every/Recurring), Depends-on, Due-date, and Todos fields** (Detail #8 specified them) → **Graph (DAG) and Calendar can't be populated through the UI**, and there's **no UI to add todos** (the backend supports all of these). The **kanban has no drag-and-drop** between columns. *These are the highest-value Sprint-4 follow-ups.*
- *Tester-method caveats (NOT product bugs):* G3's "depends_on dropped" used the wrong field (`blocked_by` is the contract field); "todos 405" used GET/POST (the endpoint is `PUT /tasks/{id}/todos`); "/workspaces/{id}/tasks 404" — tasks are `POST /tasks` with `workspace_id` in the body. The Board/List views themselves work and look premium.

### Delegation (G4) — score 3
- **MAJOR:** asking Jim to delegate failed at the **LLM level** (the weak `gemini-2.5-flash` model said it "can only delegate to specific agents in its trust set"), and the task Jim created didn't appear on the **chat workspace's** board (the flagged **M4 workspace→turn binding gap** — tasks land in the agent's default workspace, not the active one), and no SubagentBlock rendered. *Fix: M4 binding so delegated tasks surface on the active workspace; a structured "delegation failed" block instead of LLM prose; retest delegation on a capable model (glm-5.2).* The **Team graph editing itself works end-to-end and is excellent.**

### Agents (G5) — score 4
- Solid. Gaps: **inherit toggles** not exposed in the subagent **creation wizard** (only post-create?); **no "applies everywhere" cue** on agent edit (auto-save is silent about scope); subagent-create wizard may not close after save; model-card slug overflows into "Test run"; the `~` alias prefix is unexplained. Sandbox-editable-on-core + no-`off` confirmed correct.

### Settings + god-mode (G6) — score 3
- **God-mode could not be exercised** — the UAT gateways were booted **without `--allow-god-mode`**, so availability=false and the toggle is disabled (a **UAT-setup gap, not a product bug**; the god-mode card copy was praised as "the best dangerous-toggle copy I've seen"). *Re-run god-mode with `--allow-god-mode` to validate the step-up + active-banner flow.*
- Real: the **restart-required modal re-fires on every Gateway-tab visit** while a restart is pending (want a persistent badge instead); **Performance Save gives no confirmation**; "compiled out" is developer-speak; Devices tab has no pairing entry point.

### Connectors + email + skills (G7) — score 4
- Strong, especially email-as-tool (ownership model + honest "inbox UI in v0.2" note). Nits: **`(cap-1 in 0.1)` internal notation leaked** into the email helper text; the Discord token placeholder `MTAx…` looks like a real token; Built-in Tools categories aren't drill-downable; Browse-Skills opens to a blank search (no featured list).

### Edge/error (G8) — score 4, robust under abuse
- **MAJOR:** **whitespace-only workspace name accepted (201)** → invisible sidebar entry (empty string is correctly rejected; whitespace isn't). *Fix: trim+validate.* 
- Minor: bogus-workspace page is a dead-end (no "back" affordance); agent-whitespace-name validation gap; cancel-mid-stream couldn't be tested (model too fast). Branded 404 + graceful invalid-route handling praised. **Zero JS page errors across 11 screens.**

## Verdict
0.1.0 is a **strong, distinctive, mostly-working product** with a premium shell and a genuinely novel delegation
UX — but **not yet GA-ready for a general (non-technical) audience**, primarily because of the **onboarding
first-chat failure (default model 404 + raw error)** and the **task create-form completeness gap** (triggers/
deps/todos/drag-drop) that leave Graph/Calendar unpopulatable via the UI. The cross-cutting `UNRESOLVED` badge
and `cli-detect` 401 are cheap, high-visibility fixes. Recommended next sprint: the onboarding model+error fix,
the task-form field completeness + drag-drop, the M4 workspace-binding for delegated tasks, and the 4 cross-
cutting UX papercuts. Technical-beta-ready today; public-GA-ready after those.
