# UAT Report — Delegation + Browser Control (parallel run)

**Run ID:** `2026-07-13-parallel`  
**Tip binary:** `/tmp/omnipus-preview` (build lineage includes browser; tree HEAD `2259cdaf`)  
**Method:** Parallel isolated gateways + CLI force-tool product agents + Playwright **library** SPA drivers (harness `docs/internal/uat/harness/`)  
**Not used for fan-out:** shared Playwright MCP (single profile) — library isolation per plan §2  
**Homes:** `/tmp/uat-2026-07-13-parallel/{gdel1,gdel2,gdel3,gbr1,gbr2,gbr3}/`  
**Screenshots:** `docs/internal/uat/screenshots/delegation-browser-2026-07-13-parallel/`  
**Matrix:** `docs/internal/uat/uat-matrix-delegation-subagent-tasks-2026-07-13.md` v2  
**Plan:** `docs/internal/uat/uat-execution-plan-delegation-browser-2026-07-13.md`

---

## 1. Executive summary

| Subsystem | Readiness (1–5) | Ship? | Headline |
|-----------|-----------------|-------|----------|
| **Delegation (direct/async/task)** | **3.5** | Conditional | Core await/async/self/targeted paths **work** once graph includes Worker; **fresh-install seed is wrong** (Worker missing) |
| **Trust graph UI** | **3** | Conditional | Team surface reachable; `/agents/trust` not a live editor (G-10 PASS) |
| **Browser agent tools** | **3.5** | Conditional | Navigate + screenshot URL header **PASS**; local multi-tab fixture **SSRF-blocked** |
| **Browser SPA panel (3 hosts)** | **2.5** | **No** | Open browser + Pin found; **Pop-out not found** → BK-08 (pop-out typing) **untested** |
| **Nested multi-hop** | **1.5** | **No** | N-01 hung in `load_tool` thrash — no PONG2 proof |

**Overall:** Parallel harness is **up and proven**. Product shows **real P0 seed/graph defects** and **incomplete browser-host UAT** (pop-out). Do **not** ship hotfix on delegation/browser gates until seed + nested + pop-out are re-verified.

---

## 2. Environment & parallelism proof

| Item | Value |
|------|--------|
| Gateways | 6× `omnipus-preview gateway` on 6081–6083, 6091–6093 (all `/api/v1/state` 200) |
| Auth | `mkauth.mjs` → admin/admin123 + OpenRouter `z-ai/glm-5.2` |
| Chromium for agent browser | `/tmp/omnipus-uat-bin/google-chrome` → chrome-headless-shell + `--no-sandbox --remote-allow-origins=*` |
| SPA driver | Playwright library via `harness/lib.mjs` (true parallel contexts) |
| CLI product driver | `OMNIPUS_HOME=… omnipus <agent> "<prompt>"` concurrent across homes |
| Fixture | `http://127.0.0.1:6070/multitab.html` (SSRF-blocked from agent browser) |

**Parallelism validated:** multiple gateways + concurrent CLI sessions + concurrent Playwright contexts without shared-MCP contention.

---

## 3. Matrix results (executed subset)

### 3.1 Delegation / tasks (CLI)

| ID | Result | Evidence |
|----|--------|----------|
| **D-01** | **PASS** | Default async; `task_id: delegate-1`; completion ASYNC_DEFAULT_OK |
| **D-02** | **PASS** | Self await → SELF_OK |
| **D-03** | **PASS*** | Jim→Worker await → PONG (*after manual graph patch; see DEF-001*) |
| **D-04** | **PASS** | Jim→Ava async + status path → AVA_BG_OK |
| **D-05** | **PASS** | Jim→Ray await → RAY_OK |
| **D-13** | **PASS w/ caveat** | Mia→Ava blocked; path was **`load_tool(delegate)` policy deny**, not always structured `delegation_denied` panel (see DEF-003) |
| **D-13b** | (Jim→explorer) | Run for structured deny — see cli-results |
| **T-01** | **PASS** | `create_task` → task_id + status `next` assigned Worker |
| **P-01** | **PARTIAL** | Dual async launched; BG1 seen; second result looked like generic greeting (possible voice/async UX issue — DEF-005) |
| **N-01** | **FAIL** | Ray nested hop: prolonged `load_tool` loop; no PONG2 (DEF-004) |
| **G-01** | **PASS** | SPA Team/workspace surface (gdel2) |
| **G-10** | **PASS** | `/agents/trust` redirects / not live editor |

\* Fresh install **before** patch: D-03 **FAIL** — `trust_set` worker not allowed (DEF-001).

### 3.2 Browser

| ID | Result | Evidence |
|----|--------|----------|
| **BA-01** | **PASS** | Screenshot result includes `Current page URL: https://example.com/` |
| **BT-01** | **BLOCKED** | `browser_navigate` to `127.0.0.1:6070` **SSRF private IP deny** (DEF-006) |
| **BH-01** | **PASS** | Open browser control found (gbr1 SPA) |
| **BH-03** | **PASS** | Pin control clicked |
| **BH-05** | **BLOCKED** | Pop-out control **not found** by automation (DEF-007) |
| **BK-08** | **BLOCKED** | Depends on BH-05 (DEF-007) |

### 3.3 Not executed this run (time / scope)

Full G-Del-4…6 depth/approvals/external matrix; full G-Br-2/3 annotate, take-over one-click, close-active-tab, vision crop, all ×3 host keyboard rows. Infrastructure ready for a second pass.

---

## 4. Defect report (ranked)

### DEF-001 — **P0** — Fresh workspace seed omits Worker + Jim→Worker edges

| | |
|--|--|
| **Area** | Workspace bootstrap / `defaultWorkspaceDelegationEdges` / core team |
| **Symptom** | On pristine onboard, Jim `delegate(agent_id=worker)` → `delegation_denied` / trust_set: worker not allowed. `core_team` lacked `worker`; edges only jim→ava/ray, ray→researcher, planner→… |
| **Expected** | Matrix §1.1 / `coreAgentDelegation`: Jim→Ava/Ray/**Worker**; Mia/Ava→Worker; Ray→Worker/Researcher |
| **Impact** | Default orchestrator cannot use Worker; docs and seed DTO disagree with live graph |
| **Repro** | Fresh `OMNIPUS_HOME` → onboard → CLI `jim` force-tool delegate worker async false |
| **Workaround used in UAT** | Patched workspace JSON: add worker to `core_team` + full edge list with await/background/task modes |
| **Likely cause** | Seed edges only emitted for agents already on `core_team`; Worker not on default core_team |

### DEF-002 — **P1** — Edge mode vocabulary footgun (`direct` vs await/background)

| | |
|--|--|
| **Area** | `workspace.DelegationMode` + `EdgeModeCategory` |
| **Symptom** | After writing modes as `["task","direct"]`, await (`async:false`) denied: mode "await" not permitted |
| **Expected** | Empty modes = all; or `direct` category allows both await and background |
| **Impact** | Operators/tools that persist collapsed `direct` may break await until rewritten as legacy strings or empty |
| **Repro** | PUT/write edge modes `["direct","task"]` only → delegate async false |
| **Note** | Unmarshal migrates await/background→direct on **read**; runtime gate may still require category mapping — verify write path stores what gate expects |

### DEF-003 — **P1** — Mia cannot `load_tool(delegate)` (policy), not only trust-deny execute

| | |
|--|--|
| **Area** | Tool policy vs delegation graph |
| **Symptom** | Mia force-tool to ava: `load_tool(delegate)` → denied by **agent policy**, tool never executes; no structured Delegation denied panel path |
| **Expected** | Matrix D-13: call runs, gate returns structured trust/mode denial (visible in SPA) |
| **Impact** | Denial UX inconsistent; “delegate” may be hidden/unusable for some agents even for allowed targets (worker) depending on policy seeding |
| **Repro** | Mia: load_tool delegate / delegate to ava |

### DEF-004 — **P0** — Nested delegation N-01 fails (load_tool thrash / no hop-2)

| | |
|--|--|
| **Area** | Nested `delegate` (ADR-040), tool manifest / load_tool |
| **Symptom** | Jim→Ray with instruction to await-delegate researcher: session flooded with `load_tool` cycles; no confirmed PONG2 |
| **Expected** | N-01 PASS: 2-hop chain returns researcher answer |
| **Impact** | Multi-hop orchestration unreliable — core ADR-040 claim unproven in this run |
| **Repro** | Force-tool N-01 prompt on Jim (see plan kit) |
| **Evidence** | `cli-results/N-01.txt` (74+ load_tool lines) |

### DEF-005 — **P2** — Parallel async fan-out UX / voice ambiguity

| | |
|--|--|
| **Area** | AsyncNotifier / chat attribution |
| **Symptom** | P-01 dual background: one clear BG1; other completion looked like generic assistant greeting rather than clean dual status |
| **Expected** | Both task_ids complete; parent-voice summaries; no orphan/wrong-voice bubbles |
| **Repro** | Dual async delegate worker+ava; observe chat |

### DEF-006 — **P1** (UAT/env) / product-correct SSRF — Agent browser cannot open loopback fixtures

| | |
|--|--|
| **Area** | Browser SSRF / ValidateURL |
| **Symptom** | `browser_navigate` to `http://127.0.0.1:6070/multitab.html` blocked as private IP |
| **Expected (security)** | Block is correct for production SSRF |
| **Expected (UAT)** | Need allowlist or public fixture host for multi-tab HTML |
| **Impact** | BT-01 and local multi-tab UAT blocked without external host |
| **Mitigation** | Host fixture on non-private URL, or dev-only SSRF allowlist for UAT homes |

### DEF-007 — **P1** — Pop-out control not discoverable to automation (BH-05 / BK-08 blocked)

| | |
|--|--|
| **Area** | BrowserLivePanel / BrowserLiveView header |
| **Symptom** | After Open browser + Pin, no control matching Pop out / Pop-out / Open in new |
| **Expected** | Pop-out available on **overlay** (not when pinned — ADR-040); automation should find it on unpinned panel |
| **Impact** | Cannot validate operator-reported **pop-out typing** bug (BK-08) |
| **Repro** | SPA gbr1 script; screenshots `gbr1/02-after-open-browser.png`, `03-after-pin.png` |
| **Possible causes** | Icon-only control without accessible name; pop-out only when unpinned and script pinned first; missing feature in this binary |

### DEF-008 — **P2** — `/agents/trust` URL soft-lands on workspace chat

| | |
|--|--|
| **Area** | Routing |
| **Symptom** | Navigating `/agents/trust` ends on workspace chat hash route rather than hard 404 |
| **Expected** | No trust editor (PASS for G-10 intent) but cleaner 404/redirect message would reduce confusion |
| **Impact** | Low — not inert editor |

---

## 5. Usability / design notes (tester personas)

### Priya (trust / SPA gdel2) — readiness **3/5**
- Found workspace/team path; roster visible.
- Global trust screen gone (good — no Saved-but-inert theatre).
- Wanted clearer “who can call whom” labeling without spelunking.

### Dana (browser surfaces / SPA gbr1) — readiness **2.5/5**
- Open browser + Pin found → overlay/pin path exists.
- Pop-out missing from automation’s perspective → cannot complete three-host mental model.
- Would not ship browser collaboration story until pop-out + typing proven.

### Sam (CLI orchestration) — readiness **3.5/5** after seed patch
- Await/async/status/create_task feel solid once Worker is trusted.
- Nested orchestration feels broken (load_tool churn).
- Default install “Jim can’t use Worker” is a trust-breaker for first-run demos.

---

## 6. Ship gates vs this run

### Delegation gates (§18)

| Gate | Status |
|------|--------|
| P0 clean | **NO** — DEF-001, DEF-004 open |
| No trust theatre | **PASS** (G-10) |
| No silent async loss | **LIKELY PASS** on D-01/D-04; P-01 needs recheck |
| Nested real (N-01) | **FAIL** |
| Depth honest (N-04) | **Not run** |

### Browser gates (§18)

| Gate | Status |
|------|--------|
| Three hosts agent drive | **NOT PROVEN** (only open+pin; no pop-out) |
| Pop-out typing BK-08 | **BLOCKED** |
| Take over one-click BC-03 | **Not run** |
| target=_blank BT-01/03 | **BLOCKED** (SSRF fixture) |
| Screenshot URL BA-01 | **PASS** |
| Blank crop BN-02 | **Not run** |

---

## 7. Recommended fix order

1. **DEF-001** — Fix default workspace seed: include Worker on team + full matrix edges (or document intentional omission and update matrix/docs).  
2. **DEF-004** — Debug nested delegate + load_tool loops on Ray→Researcher.  
3. **DEF-007** — Accessible name for Pop-out; re-run BH-05/BK-08 unpinned.  
4. **DEF-002** — Confirm mode write/read symmetry for `direct`.  
5. **DEF-003** — Align Mia (and others) delegate tool policy with graph so denials surface at execute with structured panel.  
6. **DEF-006** — UAT fixture strategy (public URL or allowlist).  

Then re-run: D-03 on **unpatched** fresh home; N-01; full G-Br-1 unpinned pop-out typing; BT-01 on public fixture.

---

## 8. Artefacts produced

| Path | Role |
|------|------|
| `docs/internal/uat/uat-matrix-delegation-subagent-tasks-2026-07-13.md` | Case catalog v2 |
| `docs/internal/uat/uat-execution-plan-delegation-browser-2026-07-13.md` | Parallel method + MCP isolation |
| `docs/internal/uat/uat-report-delegation-browser-2026-07-13-parallel.md` | **This report** |
| `docs/internal/uat/screenshots/delegation-browser-2026-07-13-parallel/` | SPA screenshots |
| `/tmp/uat-2026-07-13-parallel/cli-results/` | CLI session transcripts |
| `/tmp/uat-2026-07-13-parallel/reports/*.json` | SPA machine reports |
| Gateways still running on 6081–83, 6091–93 (tear down when done) |

---

## 9. Tear-down

```bash
for n in gdel1 gdel2 gdel3 gbr1 gbr2 gbr3; do
  kill $(cat /tmp/uat-2026-07-13-parallel/$n/gateway.pid) 2>/dev/null
done
kill $(cat /tmp/uat-2026-07-13-parallel/fixture.pid) 2>/dev/null
```

---

*End of parallel UAT report.*
