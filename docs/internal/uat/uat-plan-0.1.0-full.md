# Omnipus 0.1.0 — Full UAT Plan (human-impersonation, Playwright, fan-out)

**Target:** the complete 0.1.0 line on `hotfix/v0.1.1` (workspace-as-project IA, unified tasks, delegation,
roster, god-mode, email-as-tool). **Goal:** full feature coverage by parallel subagents impersonating human
testers, each reporting **bugs**, **UX issues**, **coverage gaps**, AND **how it FEELS** (usability +
readiness-to-ship impression, 1–5 + prose). Outcome → a consolidated readiness report.

## Method
- **Real parallelism:** each tester subagent gets its **own isolated gateway** (unique `OMNIPUS_HOME` + ports)
  and its **own headless Chromium** driven by a self-contained Playwright script via the harness
  (`docs/internal/uat/harness/lib.mjs`, `mkauth.mjs`). No shared browser → true fan-out. (The lead may use the
  Playwright MCP for a spot smoke-check.)
- **Persona:** each subagent adopts a named human persona (non-technical → power user) and narrates first-person
  reactions ("I expected X, I felt lost when Y, this felt premium / janky").
- **Auth:** storageState (localStorage `omnipus_auth_token` + role/username + CSRF cookie) per `mkauth.mjs`.
  Real-LLM groups use OpenRouter (`z-ai/glm-5.2` or `google/gemini-2.5-flash` — tool-capable).
- **Evidence:** screenshot every step; capture console errors + failed network requests; iterate reactively.
- **Output contract (per subagent):** `{persona, journeys:[{step, screenshot, observation, feeling}],
  bugs:[{severity, title, repro, screenshot}], ux_issues:[...], coverage_gaps:[...],
  readiness:{score_1to5, would_i_ship, prose}}`.

## Topology — 8 parallel tester groups
| G | Persona | Journeys (feature coverage) | LLM |
|---|---------|------------------------------|-----|
| 1 | "Dana", first-timer | **Onboarding** (fresh, un-onboarded gateway) → first **Chat** in My Workspace → does it explain itself? | yes |
| 2 | "Marcus", PM | **Workspace IA**: sidebar (Workspaces + libraries), create a workspace, the **7 tabs** (Chat/Board/List/Graph/Calendar/Team/Settings), switching workspaces, deep-link a tab | no |
| 3 | "Priya", task-driver | **Tasks**: Board (7-state, create form, Create&Run, drag, quick-capture→inbox, partial-can't-advance), **List** filters, **Graph (Task DAG)** render+pan/zoom, **Calendar** (once/every/recurring), todos vs subtasks, **delegation roll-ups** + altitude toggle | yes |
| 4 | "Sam", orchestration nerd | **Delegation in action** (real LLM): ask Jim/Planner to decompose+delegate, watch the **SubagentBlock** in chat, the roll-ups on Board, the live tree in Graph; **Team tab** = delegation graph editor (add/remove agent, draw an edge, edit modes/depth, click node→AgentProfile), bounded delegation | yes |
| 5 | "Lee", admin/builder | **Agents**: library (filter All/by workspace) + **Workspace Teams** index; create Main / Subagent / subagent_3p (form differs by type); **model selector** ({model,provider}, grouped/sorted); inherit toggles; **sandbox profile** (editable incl. locked core, no `off`); edit agent applies-everywhere | yes |
| 6 | "Robin", security-conscious op | **God-mode** (Settings→Gateway): the danger toggle → **password step-up** modal → active banner; what it says it disables; **restart UX** (restart-gated setting → modal → reattach); **Settings** all tabs + **autosave** indicators | no |
| 7 | "Yuki", integrator | **Connectors**: channels config (a channel's Configure slide-over, secret handling), **email mailbox account** (IMAP/SMTP, agent/workspace ownership, write-only secret); **Skills & Tools** tabs | no |
| 8 | "Alex", breaker | **Edge/error states**: bad inputs, empty states (no tasks/agents/workspaces), invalid routes, network errors, restart-required flows, cancel mid-stream, console-error sweep across every screen | mixed |

Ports: gateways 6071–6078, preview 6171–6178. RAM-gate before fan-out (`free -m`); if tight, run 4+4.

## Per-group assessment questions (every group answers)
1. Did the feature work as a human would expect? Where did it break or surprise you?
2. **Usability:** what felt premium? what felt janky/confusing/unfinished? (be specific, name the screen)
3. **Readiness:** would you ship this to a real user today? score 1–5 + why.
4. Visual: Sovereign Deep consistency, motion quality, empty/loading/error states, no emoji in chrome.
5. Console errors / failed requests (WS reconnect warnings OK).

## Phase 0 — lead setup
Build SPA → embed → binary once; boot 8 isolated gateways (group 1 left un-onboarded); mint auth states for
2–8; seed each with the canonical config + OpenRouter cred (real-LLM groups). Screenshot dirs per group.

## Phase 2 — synthesis
Lead consolidates all 8 reports → `uat-report-0.1.0-2026-06-21.md`: feature-by-feature pass/fail, bug list by
severity, UX issues, the **readiness scores + the testers' felt impressions**, and a go/no-go readiness verdict.
Any Major+ bug gets a fix; re-test. Present the plan + a summary to the operator in chat.
