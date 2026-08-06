# UAT Execution Plan — Delegation + Browser (human-impersonation, Playwright MCP, parallel)

**Status:** Ready for operator go  
**Date:** 2026-07-13  
**Matrix (case catalog):** [`uat-matrix-delegation-subagent-tasks-2026-07-13.md`](./uat-matrix-delegation-subagent-tasks-2026-07-13.md) (v2)  
**Branch tip:** `hotfix/v0.1.1` (after sibling CI green enough to trust)  
**Method:** Parallel **subagents impersonating human UAT testers**, each driving the **real SPA** with **Playwright browser tools (Playwright MCP)** where possible, plus design/usability narrative.

---

## 1. Confirmed method (yes — this is the plan)

| Rule | Meaning |
|------|---------|
| **Who tests** | Multiple subagents, each with a **named human persona** (not “run the matrix like a script”) |
| **How they drive UI** | **Playwright MCP** browser tools: navigate, click, type, snapshot, screenshot — real Chromium against the SPA |
| **What they cover** | **Delegation / multi-agent** rows **and** **live browser control** rows, in **parallel** as far as isolation allows |
| **What they report** | Bugs + matrix PASS/FAIL **plus** usability/design feelings (1–5 readiness, “would I ship”, jank vs premium) |
| **What they do not do** | Replace unit CI; invent results without screenshots; share one gateway across browser groups |

This matches prior Omnipus UAT style (`uat-plan-0.1.0-full.md`, agent-features reports) updated for the **live** delegation + browser model.

---

## 2. How parallelism actually works (verified 2026-07-13)

Three different “browsers / sessions” get conflated. Separate them:

| Layer | What it is | Parallel? |
|-------|------------|-----------|
| **A. SPA tester browser** (Playwright MCP / library) | Human-impersonation: click Omnipus UI | **Yes**, if each tester has an **isolated browser context** (see §2.1) |
| **B. Omnipus gateway user + chat sessions** | Auth tokens + many `session_id`s + WS | **Yes** — product supports concurrent sessions (see §2.2) |
| **C. Omnipus agent live browser** (chromedp) | Agent tools + live panel CDP | **No per gateway** — fixed debug port **9223**, one Chromium process (see §2.3) |

### 2.1 Playwright MCP — parallel agents

**Installed here:** `@playwright/mcp` via `playwright-mcp --headless --browser chromium --no-sandbox`.  
This Claude session has **one** playwright MCP server attached to the **lead** process.

Official package rule ([`@playwright/mcp` README](https://www.npmjs.com/package/@playwright/mcp)):

> A persistent profile can only be used by one browser instance at a time, so **concurrent MCP clients sharing the same workspace will conflict**. To run several clients in parallel, start each additional client with **`--isolated`** or a distinct **`--user-data-dir`**.

Also relevant:

| Flag / tool | Role |
|-------------|------|
| `--isolated` | In-memory profile; safe for parallel clients |
| `--user-data-dir <path>` | Distinct on-disk profile per client |
| `--storage-state <path>` | Seed cookies/localStorage into an isolated context |
| `--shared-browser-context` | **Opposite** of isolation — one context for all HTTP clients (do **not** use for parallel UAT) |
| `browser_tabs` MCP tool | Multiple **tabs** inside **one** MCP browser — useful for one tester, **not** multi-tester isolation |

**What subagents inherit today**

- Claude Code subagents typically **share the parent session’s MCP servers** (one Playwright MCP → one default profile → **contention** if two agents call `browser_*` at once).
- So “spawn 6 Agent tools that all call Playwright MCP” **without** isolation **will** fight over the same page.

**Workable patterns (pick one):**

| Pattern | How | Parallel SPA testers? | Notes |
|---------|-----|------------------------|-------|
| **P1 — Isolated MCP per tester** | Each UAT subagent/process starts its own `playwright-mcp` with `--isolated` (or unique `--user-data-dir=/tmp/pw-uat-<group>`) | **Yes** | Best match to “use Playwright MCP”. Requires MCP config per agent or separate Claude sessions / MCP endpoints. |
| **P2 — Staggered single MCP** | One MCP; only one tester group drives it at a time | **No** (sequential waves) | Simplest with current single attached MCP. |
| **P3 — Playwright library** | Each subagent runs Node `chromium.launch({args:['--no-sandbox']})` + own context | **Yes** | Proven in ADR-041 UAT when MCP was down; still “Playwright browser tools”, not the MCP protocol. |
| **P4 — Hybrid (recommended default)** | Lead smoke + flaky repro: **MCP**. Parallel fan-out: **P1 if available, else P3**. | **Yes** | Document `driver: mcp-isolated \| mcp-shared \| library` per report. |

**Auth into SPA (any pattern):** inject `localStorage.omnipus_auth_token` (+ username); seed `csrf` cookie for SPA-preflight POSTs (library: `addInitScript` + `addCookies`; MCP: init script / storage-state).

### 2.2 Omnipus gateway — multiple user sessions (**supported**)

You are correct that the **gateway is built for concurrent sessions**. Evidence on this tree:

| Capability | Behaviour |
|------------|-----------|
| **Many chat sessions** | Each WS conversation can mint/attach a distinct `session_id` (`store.NewSession`); sessions are first-class on disk under `sessions/<id>/`. |
| **Concurrent WS turns** | Cancel/stream paths are **session-scoped** (`session_id` required on cancel); multiple sessions can stream without sharing one turn ID. |
| **Multiple bearer tokens per user** | Login **appends** tokens (`UserConfig.Tokens`), cap `MaxUserTokens = 10`; logout revokes **only** the presented token so **other tabs/devices stay logged in** (SEC-1 / UAT #399). |
| **Same user, many SPA tabs** | Intended: one human account, many concurrent browser tabs / tokens. |
| **Product posture** | Moving toward **single-user** (Users admin API retired), but **single-user ≠ single-session**. One operator account + many sessions is the normal webchat model. |

**UAT implication**

- Prefer **one gateway per isolation domain** still (clean data, independent config, independent agent-browser), **or**
- **One shared gateway** with **many sessions** for pure SPA/delegation UI tests that do **not** open the agent live browser — multiple Playwright contexts can log in (same user, multiple tokens or same token) and each open a **different chat session**.

**Do not assume** multi-user RBAC isolation for UAT (Users API removed); assume **one admin user, many sessions**.

### 2.3 Omnipus **agent** browser (live panel) — **not** multi-session on one process

Separate from SPA sessions:

| Fact | Implication for UAT |
|------|---------------------|
| Live Chromium uses fixed **`browserDebugPort = 9223`** | Only **one** agent Chromium per gateway process |
| `BrowserManager` is per-agent map, but one debug port | Second launch fights zombies on 9223 |
| Live panel + agent tools share that Chromium | Two UAT groups both calling “Open browser” / agent `browser_*` on **one** gateway will collide |

So:

- **Many SPA sessions on one gateway:** OK  
- **Many simultaneous agent-live-browser UATs on one gateway:** **Not OK**  
- **G-Br-1 / G-Br-2 / G-Br-3 in parallel:** need **3 gateways** (or serialize browser groups)

### 2.4 Other hard constraints

| Constraint | Implication |
|------------|-------------|
| **Devpod RAM / cores** | Prefer waves (3-wide) over 9 full stacks |
| **Preview port 8080** | UAT gateways use dedicated ports (6081+); 8080 may host artifact or one demo SPA |
| **Sibling CI** | Fan out only on trusted tip SHA |
| **glm-5.2** | Tool-capable; no vision — BN-05 needs vision model |
| **Browse agents** | Prefer **Ray / Jim** |

### 2.5 Parallelism model (recommended)

```
                    ┌─────────────────────────────────────┐
                    │  Playwright SPA drivers (parallel)  │
                    │  MCP --isolated  OR  library launch │
                    └─────────────────────────────────────┘
                                      │
          ┌───────────────────────────┼───────────────────────────┐
          ▼                           ▼                           ▼
   Gateway A (del)             Gateway B (del)             Gateway C (browser)
   many chat sessions          many chat sessions          ONE agent Chromium :9223
   no agent-browser needed     optional                    G-Br-* only
```

**Waves**

```
Wave A — G-Del-1,2,3 parallel
  • 1–3 gateways (can share one gateway with 3 chat sessions if RAM tight)
  • SPA driver: isolated MCP or library
  • Product LLM: Jim/Mia — force-tool prompts

Wave B — G-Del-4,5,6 parallel
  • Same as A
  • Nested/parallel may stress MaxParallelAgents — still no agent live panel required

Wave C — G-Br-1,2,3 parallel
  • THREE gateways mandatory (one agent Chromium each)
  • Each: SPA driver (isolated) + product Ray + live panel UAT
  • Preflight: kill zombies on 9223 per machine (only one chrome per gateway anyway)
```

**Minimum viable parallel (tight RAM)**

1. One gateway for all **G-Del-*** (many sessions, sequential or parallel SPA drivers).  
2. Browser groups **serialized** on a second gateway (or same machine one-at-a-time).  
3. Lead uses attached Playwright MCP for smoke between groups.

### 2.6 Decision: default driver for this pod

| Default | **P4 Hybrid** |
|---------|----------------|
| Lead / debug | Attached Playwright **MCP** (current process) |
| Parallel subagents | Prefer **Playwright library** per subagent **unless** we stand up **per-agent MCP with `--isolated`** |
| Report field | `driver: mcp | mcp-isolated | library` |

If operator insists “MCP only”: we must run **multiple playwright-mcp processes with `--isolated`** (or unique user-data-dir), one per concurrent tester — not one shared MCP.

---

## 3. Goals per tester (every subagent prompt must require this)

Each human-impersonation subagent returns:

1. **Matrix results** — table of case IDs → `PASS` / `FAIL` / `BLOCKED` / `SKIP` + one-line evidence  
2. **Bugs** — severity, title, repro, screenshot path  
3. **UX / design notes** — what felt premium, janky, confusing; Sovereign Deep consistency; empty/loading/error states  
4. **Usability score** — readiness **1–5**, “would I ship this to a friend?”, short prose in first person  
5. **Console / network** — failed requests, unexpected errors (WS reconnect OK)  
6. **Screenshots** — every major step under  
   `docs/internal/uat/screenshots/delegation-browser-<runid>/<group>/`

Persona voice: *“I expected X… I felt lost when Y… this felt polished / half-finished.”*

---

## 4. Workstreams ↔ groups (from matrix)

### 4.1 Delegation (multi-agent)

| Group | Persona | Focus | Matrix IDs | LLM agent in product |
|-------|---------|-------|------------|----------------------|
| **G-Del-1** | Sam — orchestration nerd | Direct `delegate`, status, cancel | D-01–D-12, U-01, U-03 | Jim |
| **G-Del-2** | Priya — trust enforcer | Denials, Team graph, modes | D-13–D-17, G-*, T-02–T-03 | Mia/Jim |
| **G-Del-3** | Lee — task board | create_task, board, reassignment | T-01, T-04–T-08, T-11–12, P-07 | Jim |
| **G-Del-4** | Ray — parallel scout | Fan-out, dual async | P-01–P-06, N-01–N-02 | Ray/Jim |
| **G-Del-5** | Nest — depth | Nested chains, depth caps, approvals | N-03–N-08, A-*, U-08 | Jim/Ray |
| **G-Del-6** | Ext — breaker | 3p (if CLI present), identity, handoff, chaos | X-*, I-*, H-*, E-*, T-09–10 | mix |

### 4.2 Browser control

| Group | Persona | Focus | Matrix IDs | Product agent |
|-------|---------|-------|------------|---------------|
| **G-Br-1** | Dana — surfaces | **Overlay / pinned / pop-out**, keyboard (esp. pop-out type), URL/SSRF | BH-*, BU-*, BK-*, BC-01–02, BN-06 | Ray |
| **G-Br-2** | Sam — agent driver | Agent multi-step job, **target=_blank**, take-over during drive | BA-*, BT-01–07, BT-14, BC-02–03, BC-07 | Ray |
| **G-Br-3** | Alex — co-pilot | Take control full, **annotate**, multi-viewer, chaos | BC-*, BN-* (vision model for BN-05), BK-08–09, BH-17, BE-* | Ray/Jim |

**Browser host checklist (G-Br-1 owns proof):** every critical path proven on:

1. Overlay sidebar  
2. Pinned sidebar  
3. Pop-out browser tab  

---

## 5. Phase plan

### Phase 0 — Lead setup (blocking)

1. Confirm **CI tip** SHA on `hotfix/v0.1.1` (sibling instance).  
2. Build binary + SPA embed once; copy to a shared path for gateways.  
3. For each group:  
   - unique `OMNIPUS_HOME=/tmp/uat-<group>`  
   - unique gateway port + preview port  
   - seed OpenRouter (or equiv.) + tool-capable model (`z-ai/glm-5.2`)  
   - browser groups: Chromium wrapper on PATH (`--no-sandbox --remote-allow-origins=*`); **kill zombies on 9223**  
4. Mint auth (token + csrf) per home.  
5. Screenshot root: `docs/internal/uat/screenshots/delegation-browser-<runid>/`.  
6. Free public **8080** if UAT needs it for a demo gateway; otherwise keep matrix artifact elsewhere and use private ports for tests.  
7. Preflight: login → chat → (browser groups) Open browser → one frame.

### Phase 1 — Wave A (G-Del-1…3) parallel

- Spawn 3 subagents with **full persona prompts** (template §7).  
- Driver: Playwright library preferred for true parallel; MCP OK if staggered.  
- Collect reports → lead triage P0 bugs (fix now vs continue).

### Phase 2 — Wave B (G-Del-4…6) parallel

- Same pattern.  
- External CLI rows: SKIP with note if binaries missing (operator scope).

### Phase 3 — Wave C (G-Br-1…3) parallel

- **Three separate gateways** (mandatory).  
- G-Br-3: configure **vision** model for annotate vision case.  
- Multi-tab fixture: serve a tiny local HTML with `target=_blank` + form fields (or known live site).  
- Explicitly retest: **pop-out typing**, **blank-link adopt**, **one-click take-over**.

### Phase 4 — Synthesis

- `uat-report-delegation-matrix-<date>.md`  
- `uat-report-browser-control-matrix-<date>.md`  
- Combined scorecard: P0 open list, UX themes, ship/no-ship per subsystem.  
- Refresh project-artifact status page with PASS/FAIL pills (delta).

### Phase 5 — Fix wave (if needed)

- P0 fails → fix agents → re-run **only** failed IDs + regression neighbors.  
- Second 7-reviewer gate only if code changes land (per project rules).

---

## 6. Subagent prompt contract (every tester)

Each Agent spawn prompt must include:

1. **Persona name + goals** (first-person human tester).  
2. **Exact matrix IDs** in scope (copy from §4).  
3. **Gateway URL**, credentials, agent to use (Jim/Ray/…).  
4. **Driver:** “Use Playwright MCP tools if you are the sole browser user; if parallel, use Playwright library script pattern from ADR-041 notes.”  
5. **Force-tool kit** for LLM product agents (from matrix §17).  
6. **Definition of done:** filled result table + bugs + UX prose + screenshots + readiness score.  
7. **graphify note** if they explore code: use graphify first (project rule).  
8. **Do not** merge code, force-push, or skip screenshots on FAIL.

### 6.1 Example persona one-liners

- **Sam:** “I want Jim to fan work out and I need to *see* subagents and trust denials.”  
- **Priya:** “If the Team graph lies, I don’t trust the product.”  
- **Dana:** “I live in the browser panel — overlay, pin, pop-out — typing must work everywhere.”  
- **Alex:** “I take the wheel, annotate a region, and expect the agent to understand.”

---

## 7. Usability / design rubric (required narrative)

Every tester answers:

| Question | Notes |
|----------|-------|
| First 30 seconds | Did I know what to do? |
| Control clarity | Am I watching vs driving vs annotating? |
| Delegation clarity | Do I know who is working and why a deny happened? |
| Visual system | Sovereign Deep: contrast, chrome, no emoji junk, motion |
| Trust | Did anything look saved/working but wasn’t? |
| Ship score 1–5 | Would I show this to a real user today? |

---

## 8. Pass bar (when to stop)

**Delegation ship gate:** all matrix §18 delegation P0s PASS (or deferred + issue + operator ACK).  

**Browser ship gate:** all matrix §18 browser P0s PASS, including:

- BH-07/08/09 (agent drive on three hosts)  
- BK-08 (pop-out typing)  
- BC-03 (take-over one click)  
- BT-01 + BT-03 (target=_blank not lost)  
- BN-02 (non-blank crop)  
- BT-07 (close active tab)

**Usability:** no P0 “I cannot complete the happy path without a guide”; average readiness ≥ 3 unless operator accepts lower with tracked UX issues.

---

## 9. What the lead does vs subagents

| Lead | Subagents |
|------|-----------|
| Phase 0 setup, ports, homes, binary | Impersonate humans; drive UI; fill matrix |
| Spawn waves; watch resource use | Screenshot + first-person UX |
| Triage P0 mid-flight | Do not expand scope without asking |
| Synthesis reports + artifact refresh | Return structured report only |
| Optional MCP smoke between waves | — |

---

## 10. Operator decisions still needed before spawn

1. **GO** to execute after CI tip?  
2. **Playwright driver:** accept **library for parallel** + MCP for lead smoke? (Recommended **yes**.)  
3. **X-*** external CLI: in or skip?  
4. **Vision model** for BN-05: which model id?  
5. **Wave size:** 3-wide (safer) vs try 6-wide?  

---

## 11. Clarity check

| Your requirement | Plan response |
|------------------|---------------|
| Multiple subagents in parallel | Yes — waves A/B/C, up to 3 concurrent per wave |
| Multi-agent (delegation) UAT | G-Del-1…6 |
| Browser UAT all surfaces | G-Br-1…3; hosts overlay/pinned/pop-out |
| Impersonate human testers | Persona + first-person UX required |
| Share usability/design feelings | Rubric §7 + readiness 1–5 |
| Use Playwright browser tools via MCP | Lead/smoke: MCP; parallel: library **or** staggered MCP (constraint §2) |

**Is a detailed plan needed?** Yes — this document *is* that plan. The matrix is the **what**; this file is the **how / who / when / isolation**.

---

## 12. Next action

On operator **GO** (and CI tip OK):

1. Lead runs Phase 0.  
2. Spawn Wave A (3 agents).  
3. Continue B → C → synthesis.

Without GO: no fan-out; only plan/matrix edits.

---

*End of execution plan.*
