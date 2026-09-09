# UAT report — judgment-first criteria + AskUserQuestion (Playwright-MCP, 2026-09-06)

- **Build under test:** `release/v0.1.1` @ `3b0e4d9b` (waves 1+2 + all 10 code-review fixes + the UAT-found window-feed fix), binary with embedded SPA, fresh `$OMNIPUS_HOME`, port 5050 (5000 squatted by macOS ControlCenter — noted for local UAT docs), real OpenRouter provider (`z-ai/glm-5-turbo`), driven via the Playwright MCP tools.
- **Verdict: PASS** — every functional flow verified end-to-end with real LLM turns; **one CRITICAL defect found live and fixed during the UAT** (below); three cosmetic defects filed as [#675](https://github.com/elicify-ai/omnipus/issues/675).

## Defect found and fixed during UAT (the reason UAT exists)

**The goal Judge was silently starved of its transcript window.** First end-to-end `/goal` run: the Judge returned `met=false` on every criterion with "no diff, transcript, machine-check, or worker summary was provided" against a session whose transcript plainly held the satisfying reply — judgment-first holdout **H-7's exact failure mode** (fail-closed-by-starvation). Root cause: both FR-032 verifier window feeds (goal + task scope) read the **legacy per-agent** session store while live sessions are written to the **shared** store, and `ReadTranscript` returns empty-with-no-error for a session missing from the store it's asked — silent empty window, no log. Fixed (shared-store-first with legacy fallback + WARN logs on the fallback paths + 2 regression tests), rebuilt, re-run live: **post-fix verdict `met=true` with real per-criterion `evidence_quote` text lifted from the transcript window.** Commit `3b0e4d9b`.

## Functional results

| Area | Result | Evidence |
|------|--------|----------|
| Onboarding (3 steps, provider key, admin) | PASS | UI steps 1-2; step 3 via REST (key kept out of transcript); login works |
| Chat + real LLM turns | PASS | Mia, multi-turn, token accounting live |
| **Prose-first criteria editor** | PASS | Plain-language field is primary, no kind selector; "+ Add technical check" and "+ Add action-count check" expanders; `verifies via: go test ./... -> exit 0` chip renders; all three kinds authored in one task |
| **Server-side kind inference** | PASS | REST readback of the created task: `prose` (no payload), `check` (payload), `behavior` (`min_count:3, tool:search_web, scope:task_session`) — kinds inferred, never sent by the UI |
| **`/goal` two-phase, clarification** | PASS | Mixed marker+prose goal → real clarifying question ("Answer in chat, or /goal clear") → answer → resumed compile |
| **Feasibility rejection, plain language** | PASS | Check criterion vs Mia's no-bash policy → "The goal was rejected at compile time… No criterion was saved. Please restate…" (no marker-syntax lecture) |
| **Prose goal happy path** | PASS | Skill-governed decomposition into 2 plain-language criteria; itemized chat echo with confirm taxonomy; GoalEchoCard "Done when" breakdown (the new wire surface); queued pill (previously-dead state, first real occupant); `confirm` → activation → round 1 |
| **Judge with evidence (post-fix)** | PASS | `met=true`; per-criterion `evidence_quote` quoting the transcript (`[tool_call] send_message -> success`, the reply text); D7 field live end-to-end |
| Judge unavailability semantics | PASS (observed) | Two OpenRouter timeouts → `judge: unavailable, backing off` — no round consumed (D7), retry succeeded |
| Deterministic-fallback observability | PASS (observed) | `goal: LLM compile fell back to the deterministic parser` WARN + `goal_compile_fallbacks_total:1` fired when the repaired compile was gate-rejected |
| **AskUserQuestion full round-trip** | PASS | Agent-invoked tool → parked turn → card: tab header, Recommended badge first & NOT pre-selected, free-text field, Answer disabled at 0/1, Cancel present, composer locked with explanation → select + Answer → collapsed "Answered" record → resume turn where Mia demonstrably received the choice → composer unlocked |
| Cold history reload | PASS | Restart mid-session: transcript re-rendered, no raw JSON bubbles |
| Agents screen | PASS | Built-in roster + System agents (Judge "not a chat target, not delegable") render |
| Settings (10 tabs) | PASS | All tabs render; Providers populated |
| Console errors | PASS | Only WS `ERR_CONNECTION_REFUSED` from deliberate gateway restarts (the checklist's allowed exception) |

## Cosmetic defects (filed, not blocking) — [#675](https://github.com/elicify-ai/omnipus/issues/675)
1. Duplicate goal pill (queued + active simultaneously after confirm; queued shown post-restart for an active goal).
2. GoalEchoCard lingers with "Reply to confirm" after confirmation.
3. Tool badge renders "Askuserquestion" instead of the ruled `AskUserQuestion` casing.

## Environment notes
- Local port 5000 is squatted by macOS ControlCenter (AirPlay) — UAT ran on 5050 via `config.json` (`{"version":1,"gateway":{"port":5050}}` — the `version` field is required or boot panics).
- The judge's OpenRouter calls hit a transient latency spike mid-UAT (two timeouts, correct backoff behavior observed) — environmental, not a defect.
