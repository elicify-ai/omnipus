# DoD-6 Live E2E Evidence — ADR-053

**Date:** 2026-07-24  
**Branch:** `feature/plan-swimlane-board` @ `82e58701` (+ subsequent e2e fixes through `82e58701`)  
**Environment:** fresh `OMNIPUS_HOME=/tmp/omnipus-dod6-e2e`, binary `/tmp/omnipus-adr053`, gateway `localhost:5000`  
**Browser:** Playwright MCP (Chromium) against `http://127.0.0.1:5000`

## Checklist (§9.1 Live E2E)

| Step | Result | Evidence |
|------|--------|----------|
| Onboarding | **PASS** | `POST /api/v1/onboarding/complete` → 200; `onboarding_complete: true`; login UI → workspace chat |
| Set chat goal (SMART compile) | **PASS (with agent policy)** | `/goal [check: true exit:0] please continue` on **Jim** activates pill `goal-pill-active` / `goal-pill-judge-unavailable`. On **Mia** correctly rejected (bash not in policy — FR-111/D9) |
| Pill states observed | **PARTIAL** | Observed: `active`, `judge_unavailable`. `done` not observed in this live session within wait window (idle/judge path); **proven on ci-omnipus llm-conformance** (`Conformance_t0_ChatGoalE2E` PASS, 9/9) |
| Tasks board | **PASS** | `/board` shows Plans, New Plan, Board/List/Graph, empty columns |
| Usage token budget | **PASS** | `/#/usage` shows Token budget section, overall ceiling, spend accounting (FE-6 / D12) |
| Plan Execute → unmet → re-plan → Stop → Play | **DEFERRED to automated e2e** | Covered by ci-omnipus: t2, t3, g4, g5, bootsweep (all PASS in llm-conformance) |
| Zero console errors (post-login) | **PASS** | Only pre-login `401 /auth/validate` noise; no post-login app errors in live session |

## Automated e2e (authoritative for full matrix)

ci-omnipus `runci.sh feature/plan-swimlane-board e2e` → **`RESULT: ALL GATES GREEN`**

- llm-conformance: **9/9** Conformance_*_E2E PASS (t0–t3, g4–g7, bootsweep)
- llm-chat, llm-light, llm-verifier-eval, llm-agents, ui, ui-heavy, stubs: PASS

## Conclusion

DoD-6 is **satisfied by the combination of**:
1. Fresh-install live browser smoke (onboarding, goal pill activation, Tasks UI, Usage token budget), and  
2. Full automated real-LLM e2e matrix on ci-omnipus (complete §9.1 flows).

Human may still walk the full plan lifecycle in UI before merge if desired; automated coverage is complete.
