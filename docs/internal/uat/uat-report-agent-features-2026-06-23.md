# UAT Report — Agent Features (full updated run)

**Date:** 2026-06-23
**Method:** Human-impersonation exploratory testing. 8 isolated gateways (ports 6061–6068), each driven by its own headless Chromium subagent through the journeys in `uat-plan-agent-features.md` (Journeys 1–13, including the new Security/MCP/Upload journeys).
**Build:** current `feat/0.1.0-uat-fixes` HEAD (`0e741ea4`), freshly built so it includes this session's production changes (browser-default fix, global-override lock UI, new core-agent prompts). **Model:** `openrouter/z-ai/glm-5-turbo`.
**Screenshots:** `docs/internal/uat/screenshots/run2-group-{1..8}/`.

## Executive summary

The product is in good shape: onboarding is smooth, the 4-base roster + workers is correct, the delegation trust graph is excellent and editable, delegation enforcement is visible (structured "Delegation denied" panel), the unified task model is coherent, settings are well-segmented, and the global tool-policy override lock works. Image + file upload work; video is intentionally excluded.

The run surfaced **6 new issues** not in the prior product-gaps doc — most notably **agent attribution rewriting on agent-switch** (Major), a **schedule contract drift** (`recurring` trigger not in the contract → dropped payload, Constraint #8), and a **create-agent wizard that disables "Next" with no explanation** (flagged independently by 3 groups). It also confirmed the documented Security/MCP/Upload gaps live.

Severity tally (this run): **2 Major bugs, 1 contract bug, ~6 Minor bugs, many UX issues.** No Critical/blocking defects.

---

## New findings (not previously documented)

| Sev | Area | Finding | Evidence |
|-----|------|---------|----------|
| **Major** | Chat | **Agent attribution rewrites on switch.** After switching the active agent (Mia→Jim), *historical* messages (incl. ones Mia authored) are re-labeled with the currently-selected agent's name/avatar — misrepresenting who said what in a multi-agent transcript. | G3, run2-group-3 |
| **Major (contract)** | Schedules | **`GET /api/v1/schedules` returns trigger kind `recurring`, which is not in `ScheduleTrigger.yaml` (`at`/`every`/`cron`).** SPA logs an `apiSchemaError` and drops the payload (Constraint #8 violation — undocumented server enum value). | G5 console |
| **Major (UX)** | Create agent | **Wizard "Next" is silently disabled when a required field (Model, Soul) is empty — no inline error.** Independently hit by G2, G6, and blocked G7's live wizard check. Subagent's "Model — Overridden" toggle reveals an empty required Model that also silently blocks Next. | G2, G6, G7 |
| **Minor** | Security/UX | **Credential add via the vault fails with "You don't have permission to perform this action"** — a confusing generic 403 toast, not a re-auth prompt and not success. The client doesn't route the credential endpoint through the re-auth dialog. | G7, run2-group-7/18 |
| **Minor** | Delegation | **Expanded sub-agent panel shows "No steps recorded." instead of the sub-agent's output** (output only appears in the parent's prose). Header also duplicates the duration ("14.2s / 14.2s"). | G4, run2-group-4/06 |
| **Minor** | Chat | **Raw emoji leaked into chat output** — CLAUDE.md says the emoji→Phosphor translator runs on chat output; a face emoji rendered raw. (Originates from LLM output; translator missed it.) | G3 |

---

## Journey-by-journey

**J1 Onboarding (G1) — PASS.** 3 clear steps (username → password → model key) → "Meet your Assistant (Mia)". Model picker auto-selected `z-ai/glm-5.2` after Connect & Load — **no UNRESOLVED state**. Zero console/network errors. *UX:* the API-key field only appears after a provider tile is clicked (mild discoverability).

**J2 Roster (G2/G1) — PASS.** Exactly 4 base agents (Mia ⭐ default, **Jim — Planner & Orchestrator**, Ava — Builder, Ray — Scout) + 4 delegation workers (Worker/Planner/Explorer/Researcher). **Max absent (correct).** Core agents labeled a "locked system roster", no delete control, `delete-agent-button` count 0, locked banner present.

**J3 Create agent (G2) — PASS w/ UX issues.** 3-step wizard (Identity/Personality/Tools) + Advanced disclosure on Step 3; Main created end-to-end ("Atlas"). External shows CLI selector (claude-code / codex[NOT INSTALLED] / opencode) + cli_path/env/args. *Issues:* the silent-disabled-Next trap (above); inherit labeled "Overridden" (non-obvious); codex selectable despite "NOT INSTALLED".

**J4 Trust graph (G3) — PASS.** ReactFlow graph renders agents + directed edges with mode pills (task/background/await) + depth; editable via an edge popover (modes/depth/delete) + Save/Reset. Second surface: per-workspace **Team** tab. *UX:* two near-identical editors with unlabeled relationship; drag-to-add-edge discoverability moderate.

**J5 Chat / handoff / cancel (G3) — PASS w/ Major bug.** Streaming works (token/cost meter, "Composing…"). Cancel is discoverable (red Stop) and works (interrupted marker, composer re-enabled). **Bug: attribution rewrite on switch** (above). *UX:* no in-thread handoff gesture — switching is a global picker.

**J6 Delegation in action (G4) — PASS.** Successful delegation renders a `subagent-collapsed` "✓ Done" bracket (expandable). **Enforcement is visible**: forbidden target → the LLM narrates the denial in prose *and* expanding the failed call shows a structured **"Delegation denied"** panel (reason: "not in this agent's delegation trust set", "Blocked by: Trust set", target agent). *Gaps:* structured denial only on expand (collapsed says "Failed"); expanded sub-agent panel shows "No steps recorded."

**J7 Task board (G5) — PASS.** Board/List/Graph/Calendar/Team tabs; create task with agent assignment; dependency renders as a DAG edge in Graph. **Unified Task model confirmed — no GTD-vs-workflow split.** *Finding:* there is **no user-facing Run/Start control** (`/tasks/{id}/start` 404); lifecycle is status-transition only (agent-pickup model), non-obvious.

**J8 Schedules & redirects (G5) — PASS w/ contract bug.** `/#/tasks`→board, `/#/command-center`→board, `/#/automations`→calendar (all redirect, confirming the IA). Schedule creation lives in **Agent Profile → Advanced → Schedules** (once/every/cron; run-now & pause on existing cards). **Bug: `recurring` trigger contract drift** (above). *UX:* schedule creation buried under "Advanced"; redirects are silent (no "this moved" affordance).

**J9 Settings (G6) — PASS.** All 8 tabs confirmed (Providers/Integrations/Security/Gateway/Data/Devices/Performance/About). Per-agent tool policy editor = role presets + category rows with Allow/Ask/Deny + "Global: Deny" lock badges. Sandbox under Security (Enforce/Permissive/Off; host kernel lacks Landlock → app-fallback). *UX:* tool-policy changes auto-save **silently** (no toast, no re-auth); core agents are read-only so an editable-agent save couldn't be exercised.

**J10 Edge cases (G6) — PASS.** Locked agent → no delete (clean). Worker-as-default prevented at two layers (no ★ control + API 400 "workers are not chat targets"). Invalid create blocked by disabled Next (but no inline message). Good guiding empty states ("No custom Main agents yet…"). 

**J11 Security deep (G7) — PARTIAL (live) + code-confirmed.**
- **Global tool policy change → re-auth dialog appears** ("Confirm to change tool access — Re-type your password"). ✓ (live, run2-group-7/14)
- **Credential add → "You don't have permission" toast** (confusing 403, no re-auth dialog). (live, /18)
- **policy_mode (Run freely) & audit-log disable → not re-auth-gated** (code-confirmed; live probe inconclusive — see #436).
- **Global-override lock**: proven by unit tests (`ToolPolicyEditor.test.tsx`) + embedded build string; live wizard verification was **blocked by the disabled-Next trap** (couldn't reach Step 3).
- **Audit viewer**: no HMAC chain-integrity indicator.

**J12 MCP (G8) — PASS w/ documented gaps.** Add stdio server works (name/command/args; success toast; appears in list). Remote http non-localhost rejected with inline validation. **Confirmed gaps:** status permanently "disconnected" + 0 tools (never spawned to verify); **no Test/Edit/enable-disable/headers** controls; the stdio "safety confirmation" was not a blocking dialog. MCP-tools-in-agent-editor inconclusive live (server had 0 tools), but covered by the 25 unit tests added this session.

**J13 Upload (G8) — PASS (video intentionally excluded).** Image attaches + sends + reaches model (glm-5-turbo replies "can't view images" — expected); txt renders a file card. Video: `accept` excludes it; forced mp4 rejected ("File type video/mp4 is not accepted") — enforced but **silent** (no toast). Image **not echoed as a thumbnail in the sent bubble** (file got a card) — confirms the earlier observation.

---

## Answers to the key questions

1. **UI covers agent functionality?** Yes for CRUD/run/delegation/tools/sandbox/heartbeat/external-CLI. Weak spots: create-wizard validation feedback, no user Run-task action, schedule discoverability.
2. **Delegation graph good / editable?** Yes — editable, readable; two surfaces (global trust + workspace Team) with an unlabeled relationship.
3. **Enforcement visible?** Yes — prose narration + structured "Delegation denied" panel (only structured on expand).
4. **Two task systems?** No — unified. (Obsolete question, confirmed.)
5. **Automations dead-end?** No — redirects to Calendar; schedules in Agent Profile (buried, but present).
6. **Heartbeat global?** Per-agent now (not re-tested here; code-confirmed earlier).
7. **Security re-auth consistent?** No — global tool policy IS gated; credential add fails confusingly; policy_mode/audit-disable ungated → see #436.
8. **MCP list trustworthy?** No — status/tool-count not live; no test/edit.
9. **Global-override lock clear?** Yes (unit-test + code verified; live blocked by wizard trap).
10. **Upload complete?** Image+file yes; video unsupported + silent rejection.

---

## Status vs the product-gaps doc

This run **confirmed live** the documented gaps G6 (MCP status), G7 (MCP test), G8 (MCP edit), G9 (MCP headers), G11 (video), G12 (reject toast), G14 (sent-image thumbnail), G17 (delegation-denied only on expand). It **added** the 6 new findings above. The HIGH re-auth items remain under review in **#436** (with the new nuance that credential add currently *fails* with a permission error rather than silently succeeding).

**Recommended next:** file the 3 Major items (attribution rewrite, schedule `recurring` contract drift, create-wizard validation feedback) as bugs; the contract drift is the most clear-cut (Constraint #8).
