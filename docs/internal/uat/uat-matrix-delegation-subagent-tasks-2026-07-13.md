# UAT Matrix — Delegation + Live Browser Control

**Status:** Review draft v2 (extended with browser surfaces) — artefact for operator sign-off before execution  
**Date:** 2026-07-13 (v2 same day — browser extension)  
**Branch target:** `hotfix/v0.1.1` (includes browser ADR-038→041 + delegation ADR-032/036/037/040)  
**Owner:** Operator review → then UAT execution (parallel human-impersonation / CLI-driven agents)  
**Does not replace:** CI unit/integration tests. This matrix is **end-to-end product UAT** — real gateway, real LLM (where marked), real SPA + Chromium + chat surfaces.

---

## 0. Purpose

Prove, end-to-end, that **delegation patterns** and the **live browser control surface** work as the product claims.

### 0.1 Delegation families

| Pattern family | What “done” means |
|----------------|-------------------|
| **Direct sub-turn (`delegate`)** | Await + background + status poll, identity = target, result visible correctly |
| **Task-mode assignment** | `create_task` / reassignment via `update_task`, board lifecycle, pickup |
| **Parallel / fan-out** | Multiple concurrent background delegates + concurrency cap behaviour |
| **Nested / multi-hop** | Configurable depth (global + per-edge), not the old one-hop hard block |
| **Trust graph (workspace)** | Deny-by-default edges; Team tab is the only operator surface |
| **External CLI workers (`subagent_3p`)** | Target identity + workspace `work/` rooting + non-stalling dispatch |
| **Handoff (out of trust graph)** | Session agent switch remains open / not gated by delegation edges |
| **Approvals inheritance** | Parent “Always Allow” grants copy-at-spawn into child `ask` only |
| **Cross-workspace task tools** | Same gate as in-workspace task tools |

### 0.2 Live browser families (ADR-038 → 041) — **required**

| Pattern family | What “done” means |
|----------------|-------------------|
| **Three panel hosts** | Same session works as **(1) overlay sidebar**, **(2) pinned docked sidebar**, **(3) pop-out browser tab** |
| **Agent drives** | Screencast follows agent tools; agent completes real multi-step browse jobs |
| **Human take control** | Implicit / Take-over model (ADR-040): click-to-drive, pause agent, type/click/scroll; release / hand back |
| **Annotate** | Pen mode → crop (+ inspect context) → chat attachment; **not blank**; vision-capable model when validating crop |
| **Keyboard** | Plain typing **and** editing keys (Backspace/Enter/Tab/arrows/Ctrl+A) work in **all three hosts** — especially pop-out |
| **Multi-tab** | `target=_blank` / `window.open` **adopted**; agent + user can switch/close/open tabs without getting “lost” |
| **URL / SSRF / launcher** | User omnibox + Open browser; friendly errors; shared session with agent |

**Out of scope (flag only):** marketplace packs, remote-a2a agent refs, full CAPTCHA-solving product claims, non-Chromium engines.

---

## 1. Ground truth (live model — do not UAT against retired surfaces)

These are **authoritative** as of `hotfix/v0.1.1`. Older UAT plans that mention `/agents/trust`, global `delegation_policy`, or separate `spawn` / `run_subagent` / `check_spawn_status` tools are **stale**.

| Topic | Live behaviour | Authority |
|-------|----------------|-----------|
| Single delegation tool | `delegate` only (`action: run\|status`, `async` default **true**) | ADR-036, `pkg/tools/delegate.go` |
| Trust authority | **Per-workspace** `Delegation[]` edges only; deny-by-default | ADR-037, `pkg/workspace/delegation.go` |
| Edge modes (stored) | `direct` \| `task` (legacy `await`/`background` → `direct` on read) | `workspace.DelegationMode` |
| Call modes (tool) | `await` (`async:false`), `background` (`async:true`), `task` (`create_task` / `update_task`) | `config.DelegationMode*` + `EdgeModeCategory` |
| Mode mapping at gate | `await` + `background` both require edge category **`direct`**; task tools require **`task`** | `pkg/agent/loop.go` `EdgeModeCategory` |
| Nested chains | Allowed; governed by edge + global depth (FR-H-006 one-hop **reversed**) | ADR-040 |
| Identity | Child turn = **target** agent (tools, soul, model, policy, workspace) — parent contributes prompt + auth only | ADR-032 (2026-07-09) |
| Seed graph (fresh workspace) | Jim→Ava/Ray/Worker; Mia/Ava→Worker; Ray→Worker/Researcher; Planner→Explorer/Researcher (depth 2); leaves have no outward edges | `coreagent.SeedDelegationEdges` |
| Operator UI | Workspace **Team** tab only (global trust screen deleted) | ADR-037 |
| Handoff | `hand_off` — **not** gated by trust graph; excluded from nested sub-turn registry | `pkg/tools/handoff.go`, ADR-040 |
| Concurrency | `SubTurn.MaxConcurrent` / `Performance.MaxParallelAgents` fan-out semaphore | `pkg/agent/subturn.go`, `config.EffectiveMaxParallelAgents` |
| Async delivery | Background requires `Critical:true`; status via `delegate(action=status)`; completion via AsyncNotifier (no raw child voice as orphan bubble) | `delegate.go` executeAsync |

### 1.1 Seed trust matrix (default workspace — verify first)

| From | To (seeded) | Modes (tool vocabulary → edge) | Onward depth |
|------|-------------|--------------------------------|--------------|
| **Jim** | Ava, Ray, Worker | task + background + await → edge `[task, direct]` | inherit global |
| **Mia** | Worker | task + background → `[task, direct]` | inherit |
| **Ava** | Worker | task + background → `[task, direct]` | inherit |
| **Ray** | Worker, Researcher | task + background + await → `[task, direct]` | inherit |
| **Planner** | Explorer, Researcher | await + task → `[direct, task]` | **2** |
| **Worker / Explorer / Researcher** | — | no outward seed edges | n/a |

**Default global depth backstop:** `defaultMaxSubTurnDepth = 3` when `SubTurn.MaxDepth` unset.

### 1.2 Agent types under test

| Wire / kind | Role in matrix | Chat target? |
|-------------|----------------|--------------|
| Core: Mia, Jim, Ava, Ray | Base roster | Yes |
| Subagent workers: Worker, Planner, Explorer, Researcher | Delegation-only | No (not default chat targets) |
| Custom Main | User-defined chat agent | Yes |
| Custom Subagent (native) | User-defined worker | No |
| Custom `subagent_3p` | External CLI: claude-code / codex / opencode | No |

---

## 2. Pre-flight (mandatory before any row)

| # | Check | Pass criteria |
|---|-------|---------------|
| P0 | Build binary from tip of `hotfix/v0.1.1` (SPA embed sync if UI touched) | Gateway boots; SPA loads |
| P1 | Fresh `OMNIPUS_HOME` + onboarding complete | 4 base + workers present |
| P2 | Tool-capable model (e.g. `z-ai/glm-5.2`) on Jim / Ray / Planner | Agents actually call `delegate` / `create_task` |
| P3 | Open workspace → **Team** tab | Seed edges match §1.1 |
| P4 | Confirm **no** `/agents/trust` route | 404 or redirect — not a live editor |
| P5 | Verbose chat OFF then ON once | Hidden vs visible infra tool calls understood |
| P6 | Screenshot / log dir ready | `docs/internal/uat/screenshots/delegation-matrix-<runid>/` |
| P7 | **Browser:** Chromium/chrome-headless-shell on PATH with `--no-sandbox --remote-allow-origins=*` (see browser launch gotchas) | Agent can open a live session; port **9223** free (one Chromium at a time) |
| P8 | **Browser:** use **Ray or Jim** for browse agents (not Mia/Ava if tools restricted); model tool-capable | Browser tools actually called |
| P9 | **Browser annotate vision:** if validating crop content (not just non-blank pixels), use a **vision** model (e.g. `anthropic/claude-sonnet-4.5` via OpenRouter) — glm-5.2 has **no** vision | Annotate vision path meaningful |
| P10 | **Browser:** kill zombie chromes holding 9223 before each group | No cascade of websocket timeouts |

**Isolation:** one gateway per UAT group (unique `OMNIPUS_HOME` + ports). Real LLM groups need OpenRouter (or equivalent) credentials. **Browser groups:** only one live agent-browser per gateway (fixed CDP 9223) — do not parallelize two browser sessions on the same gateway.

**Prompting tip (glm-5.2):** small, force-tool prompts work better than open-ended “orchestrate something” — e.g. *“Call the `delegate` tool once with agent_id=worker, async=false, task='Reply with exactly: PONG' and nothing else.”*

**Browser agent tip:** force concrete tool sequences (navigate → wait → click → get_text) and name the site; for multi-tab use a page with a real `target=_blank` (e.g. elicify contact → cal.com, or a local HTML fixture).

---

## 3. How to read the matrix

| Column | Meaning |
|--------|---------|
| **ID** | Stable case id (`D-…` direct, `T-…` task, `P-…` parallel, `N-…` nested, `G-…` graph/trust, `X-…` external, `A-…` approvals, `H-…` handoff, `U-…` UI, `E-…` edge/error) |
| **Pattern** | Product pattern under test |
| **Setup** | Graph / agents / settings required |
| **Stimulus** | Exact user / operator action (prefer force-tool chat prompts) |
| **Expect (backend)** | Runtime outcome |
| **Expect (UX)** | What the human must see in SPA |
| **Sev if fail** | P0 ship-blocker · P1 major · P2 polish |
| **LLM?** | Needs real model (`Y`) or pure UI/API (`N`) |
| **Result** | blank for run log: `PASS` / `FAIL` / `BLOCKED` + note |

**Pass rule:** backend **and** UX expectations must both hold. Silent backend success with wrong UX = **FAIL**.

---

## 4. Matrix A — Direct sub-turn (`delegate`)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **D-01** | Self-delegation, **background** (default) | Chat as Jim; seed graph OK | Prompt: `delegate` with only `task` set (omit `agent_id`, omit `async`) | `async` defaults true; returns `task_id` (`delegate-N`); child runs as Jim identity; status map records `running`→`completed` | Immediate ack tool result; later parent-voice completion (AsyncNotifier), **no** orphan raw child bubble; Subagent block / activity shows work | P0 | Y | |
| **D-02** | Self-delegation, **await** | Jim | `delegate(async=false, task=…)` | Blocks until child done; inline tool result with wrapped “Subagent task completed” | Tool call stays open until done; result in-thread; no second unattributed bubble | P0 | Y | |
| **D-03** | Targeted **await** Jim→Worker | Seed Jim→Worker | `delegate(agent_id=worker, async=false, task='Reply PONG')` | Gate allows (direct); child soul/tools/model = Worker; parent result wraps child answer | Subagent UI names Worker; expanded steps/output not empty; transcript agent_id = worker for child frames | P0 | Y | |
| **D-04** | Targeted **background** Jim→Ava | Seed Jim→Ava | `delegate(agent_id=ava, async=true, label='build-x', task=…)` | Background gate; `Critical:true` child survives parent end; status poll works | Label visible; status via `action=status`; completion delivered to parent chat | P0 | Y | |
| **D-05** | Targeted **background** Jim→Ray | Seed Jim→Ray | Same as D-04 targeting `ray` | Allow + Ray identity | Scout persona in child work, not Jim’s | P0 | Y | |
| **D-06** | **Status** poll happy path | After D-04 | `delegate(action=status, task_id=…)` | Returns running/completed/failed/canceled from **same** map async wrote | Status text truthful (not “no subagents spawned”) | P0 | Y | |
| **D-07** | Status list (no task_id) | ≥1 async task in session | `delegate(action=status)` | Lists tasks for **this** channel/chat only | List matches visible session work | P1 | Y | |
| **D-08** | Status cross-session isolation | Two chats; async in A | From chat B, status with A’s `task_id` | “No subagent found…” (not leak) | No foreign task data | P0 | Y | |
| **D-09** | Untargeted vs targeted identity | Jim | Two awaits: omit `agent_id` vs `agent_id=worker` | Omit → Jim self; target → Worker | Distinct personas / tool policies in results | P0 | Y | |
| **D-10** | Label surface | Any allow edge | `label` set | Label in task state + start frame | Label in Subagent block header | P2 | Y | |
| **D-11** | Cancel mid **await** | Jim→Worker await long task | Stop / cancel parent turn mid-child | Child interrupted; result marks interrupted not generic fail | Live + **reload** both show interrupted (not failed) | P0 | Y | |
| **D-12** | Cancel mid **background** | Jim→Worker async long | Cancel parent soon after ack | Child **continues** (`Critical`); eventually completes or session cancel policy applies | Parent can leave; completion still lands; no silent drop of multi-tool child answer | P0 | Y | |
| **D-13** | Denied target (no edge) | Chat as Mia; try `agent_id=ava` (no Mia→Ava seed) | Force `delegate` to Ava | `DelegationDenied` trust set | Structured **Delegation denied** panel (expand); prose may narrate; collapsed must not look like success | P0 | Y | |
| **D-14** | Mode mismatch — edge task-only | Team: Jim→Worker modes=`[task]` only | `delegate(async=true, agent_id=worker)` | Deny mode (background needs `direct`) | Denial reason cites mode / blocked by mode | P0 | Y | |
| **D-15** | Mode allow — edge direct-only | Jim→Worker modes=`[direct]` only | `delegate` await + background | Both allow; `create_task` to worker denied | Task deny separate from direct allow | P0 | Y | |
| **D-16** | Empty / invalid args | Any | Missing `task`; bad `action`; non-bool `async` | Clear tool errors | No crash; LLM can recover | P1 | Y | |
| **D-17** | Worker as chat target blocked | — | User tries to open chat as Worker / set default | UI + API prevent | Consistent with “workers not chat targets” | P1 | N | |

---

## 5. Matrix B — Task-mode delegation (board / orchestrator path)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **T-01** | `create_task` allowed (Jim→Worker) | Seed modes include task | Jim: create task assigned to Worker with clear title | Gate mode=task allow; task lands ready for pickup; assignee=worker | Board card shows assignee; Graph/List consistent; chat tool result success | P0 | Y | |
| **T-02** | `create_task` denied (no edge) | Mia → try assign Ava | Force create_task assignee=ava | Trust deny | Denial visible; **no** board card created | P0 | Y | |
| **T-03** | `create_task` mode deny | Edge modes=`[direct]` only | create_task to that target | Mode deny | Structured denial | P0 | Y | |
| **T-04** | `update_task` reassignment allow | Task on Worker; Jim may task→Ava | update assignee Worker→Ava | Both “may reassign from” and “may assign to” policy paths allow | Board updates assignee; no silent drop | P0 | Y | |
| **T-05** | `update_task` reassignment deny | Jim cannot task→Explorer | Reassign to Explorer | Deny; task unchanged | Denial; board unchanged | P0 | Y | |
| **T-06** | Task lifecycle human-visible | T-01 task | Drive status through board (or agent pickup) | Status transitions valid; terminal states stick | Board/List/Graph reflect; no dual GTD/workflow systems | P0 | Y/N | |
| **T-07** | Task DAG / blocked_by | Create A then B blocked_by A | Complete A | B unblocks (AdvanceUnblocked); Jim/orchestrator path if wired | Graph edge; B becomes actionable | P1 | Y | |
| **T-08** | Create & Run (if UI still exposes) | Board create form | Create&Run if present | Consistent with agent-pickup model | If control missing: document as coverage gap, not silent fail | P2 | N | |
| **T-09** | Cross-workspace `create_task_in_workspace` | ≥2 workspaces; different graphs | Sysagent/cross tool assign in WS-B | **Same** gate as in-WS create against **WS-B** graph | Denial/allow matches WS-B Team tab, not WS-A | P0 | Y | |
| **T-10** | Cross-workspace `update_task_in_workspace` | Task in WS-B | Reassign via cross tool | Same parity as T-04/T-05 for WS-B | No cross-WS privilege leak | P0 | Y | |
| **T-11** | Task depth / nested task spawn | Planner depth=2 edges | Chain of task creates beyond depth | Deny at depth | Clear depth denial | P1 | Y | |
| **T-12** | Worker cannot create task to Jim (no edge) | Chat path via parent that spawns worker then… | From a Worker sub-turn if tools allow create_task | No outward seed edge → deny | Fail closed | P1 | Y | |

---

## 6. Matrix C — Parallel / fan-out

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **P-01** | Dual background fan-out | Jim→Worker + Jim→Ray allowed | One turn: two `delegate` async calls (worker + ray) **or** two sequential force calls | Both run; two task_ids; both complete | Two blocks / two statuses; both results return to parent | P0 | Y | |
| **P-02** | Triple fan-out | Ray→Worker + Researcher + self/worker | Ray deep-research style: ≥3 background delegates | All complete; parent synthesizes | Activity / chat shows concurrent work without dead UI | P0 | Y | |
| **P-03** | Max-parallel queue | Set `max_parallel_agents` low (e.g. 2) if Settings exposes it; else config | Launch 3+ concurrent backgrounds | ≤N concurrent; others wait / timeout policy documented | No crash; excess either queue or clear concurrency error | P1 | Y | |
| **P-04** | Same-target concurrent external | Two async to same `subagent_3p` workspace | Two parallel delegates same external agent | Per-workspace serialization (no git/workspace race corruption) | Both eventually finish; workspace not corrupted | P0 | Y | |
| **P-05** | Mixed await + background | Jim | One await + one async in same parent turn | Await blocks parent tool loop as designed; async independent | UX doesn’t freeze entire SPA; await tool stays pending | P1 | Y | |
| **P-06** | Status under fan-out | After P-01 | status without id; then each id | List accurate; per-id accurate | No crossed results | P0 | Y | |
| **P-07** | Parallel task creates | Jim | create_task ×3 different assignees allowed | All three board tasks | Board shows 3 cards correct assignees | P1 | Y | |

---

## 7. Matrix D — Nested / multi-hop (ADR-040)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **N-01** | 2-hop chain allowed | Edges: Jim→Ray (direct), Ray→Researcher (direct); depths unrestricted | Jim await→Ray with task “await-delegate to researcher: reply PONG2” | Ray’s child registry **includes** `delegate`; hop-2 succeeds | Nested Subagent blocks or nested attribution; final answer reaches Jim | P0 | Y | |
| **N-02** | 3-hop at default cap | Chain of direct edges A→B→C→D; global depth default 3 | Force chain to depth 3 success, depth 4 deny | depth 3 OK; depth 4 `ErrDepthLimitExceeded` / deny | Depth denial reason visible | P0 | Y | |
| **N-03** | Per-edge depth=1 | Edge Jim→Ray depth=1; Ray→Worker exists | Jim→Ray then Ray tries Worker | First hop OK; Ray→Worker denied by edge depth | Denial cites depth | P0 | Y | |
| **N-04** | Per-edge depth=10 vs unset global | Edge depth=10; `SubTurn.MaxDepth` unset | Chain length 4–5 on that path | Must **not** hard-stop at 3 (#477 class) | Chain works | P0 | Y | |
| **N-05** | Tighter-of-two | Edge depth=5; global MaxDepth=2 | Attempt depth 3 | Deny at 2 | Advertised cap matches enforced | P1 | Y | |
| **N-06** | Nested child cannot hand_off | Any nested child | Child attempts `hand_off` | Tool absent / denied (ExcludedHandoff) | No session hijack | P0 | Y | |
| **N-07** | Nested grant inheritance transitive | Parent Always-Allow tool X; hop-2 child policy ask | Grandchild calls X | Auto-approve (copy-at-spawn chain) | No re-prompt at hop-2 | P1 | Y | |
| **N-08** | load_tool honesty | Nested child | load_tool(`delegate`) then call | load_tool reflects real child registry (no false success) | If load fails, matches execute | P1 | Y | |

---

## 8. Matrix E — Graph / Team tab (operator surface)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **G-01** | Seed graph render | Fresh workspace | Open Team tab | Edges match §1.1 | Readable graph; mode pills; depth shown | P0 | N | |
| **G-02** | Add edge | Team editor | Draw CustomMain→Worker direct+task; Save | Persisted in workspace JSON; runtime allows | Saved; edge visible after reload | P0 | N | |
| **G-03** | Remove edge | Existing Jim→Ava | Delete edge; Save | Immediate deny Jim→Ava | Chat D-13 style deny after edit | P0 | Y | |
| **G-04** | Edit modes only | Jim→Worker | Modes → task only; Save | D-14/D-15 behaviour | Editor reflects modes after reload | P0 | Y | |
| **G-05** | Edit depth | Jim→Ray | Depth 1; Save | N-03 | Depth badge correct | P0 | Y | |
| **G-06** | Self-edge rejected | Team | Attempt From=To | Validate error; not persisted | Clear validation message | P1 | N | |
| **G-07** | Off-team endpoint rejected | Team | Edge to agent not on team | 400 / tool error | Cannot save illegal edge | P1 | N | |
| **G-08** | Cycle detection | Team | Create cycle if product forbids | Reject or warn per product rule | Consistent | P2 | N | |
| **G-09** | Per-workspace isolation | WS-A allows Jim→Ava; WS-B does not | Same Jim in both | Allow only in WS-A | Switching workspace changes effective trust | P0 | Y | |
| **G-10** | Retired global trust | — | Navigate `/agents/trust` | Gone | No decorative “Saved but inert” screen | P0 | N | |
| **G-11** | PUT agent `delegation_policy` | API | PUT with retired field | 400 sniff (ADR-037) or documented drop | Prefer 400 with message | P1 | N | |
| **G-12** | Legacy mode migration | Persist edge with `await`/`background` strings | Read graph | Modes collapse to `direct` | UI shows `direct` not unknown | P1 | N | |

---

## 9. Matrix F — Identity, tools & workspace rooting (ADR-032)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **I-01** | Native target model | Worker model ≠ Jim model | Await Jim→Worker “what model are you / reply with configured name” | Child uses Worker model/provider | Result not Jim’s model voice only | P0 | Y | |
| **I-02** | Native target tool policy | Worker deny `bash`; Jim allow | Child tries bash | Denied by **Worker** policy | Denial is child’s policy, not parent | P0 | Y | |
| **I-03** | CoreTeam work root | Agents on workspace team | Child write_file in work tree | Writes under `workspaces/<id>/work/` | Cannot clobber `AGENT.md` / memory room via relative escape | P0 | Y | |
| **I-04** | load_tool vs execute policy split | Historical bug class | Child load_tool + execute sensitive tool | Both evaluate **child** agent id | No parent/child split-brain | P0 | Y | |
| **I-05** | Token / agent_id attribution | Nested + parallel | Inspect WS frames / usage | Child frames carry child `agent_id` | Activity bar / transcript attribution correct (no rewrite on switch) | P0 | Y | |

---

## 10. Matrix G — External CLI (`subagent_3p`)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **X-01** | Create + edge | Create subagent_3p (claude-code if installed); Team edge Jim→that agent direct | Await delegate simple “echo hi in a file” | External dispatch; correct cwd (`work/` if CoreTeam); completes | Subagent block shows external agent; no hang on approval prompt | P0 | Y | |
| **X-02** | codex path | If codex installed | Same | sandbox workspace-write path | Completes | P1 | Y | |
| **X-03** | opencode path | If opencode installed | Same | Completes under operator-trust posture | Completes; no TTY hang | P1 | Y | |
| **X-04** | Missing CLI | subagent_3p pointing at missing binary | Delegate | Clear fail (not hang) | Error message actionable | P1 | Y | |
| **X-05** | Wrong identity regression | Parent native, target external | Delegate | Dispatch uses **target** executor, not parent native | External run evidence (CLI logs / result style) | P0 | Y | |
| **X-06** | Concurrent same workspace | See P-04 | — | Serialized | — | P0 | Y | |
| **X-07** | Task-mode to external | Edge modes include task | create_task assignee=3p | Allowed if edge+modes; pickup path defined | Board assignee correct; execution path documented if not auto | P1 | Y | |

---

## 11. Matrix H — Approval grant inheritance

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **A-01** | Inherit into ask | Parent Always-Allow tool T; child policy ask T | Delegate then child calls T | No second prompt | Child proceeds | P0 | Y | |
| **A-02** | Deny not overridden | Child policy deny T; parent grant T | Child calls T | Still deny | Denial | P0 | Y | |
| **A-03** | Allow path independent | Child policy allow T | Child calls T | Allow without grant | No prompt | P1 | Y | |
| **A-04** | Copy-at-spawn not live | Child already running; parent grants T after | Child calls T | No retroactive grant | May still prompt | P1 | Y | |
| **A-05** | Transitive (see N-07) | 2-hop | — | Inherit chain | — | P1 | Y | |

---

## 12. Matrix I — Handoff (not delegation trust)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|--------|
| **H-01** | Handoff open despite no edge | Mia has no edge to Jim | `hand_off` to Jim (or UI agent switch + hand_off tool) | **Allowed** (ungated) | Active agent becomes Jim; subsequent replies Jim | P0 | Y | |
| **H-02** | Handoff ≠ delegate | After H-01 | Jim’s later tools use Jim identity | Not a sub-turn; session pin | No Subagent block for handoff itself | P1 | Y | |
| **H-03** | Handoff to worker rejected | — | hand_off to Worker | Reject | Clear error; no sticky worker pin | P0 | Y | |
| **H-04** | Nested handoff blocked | Inside delegate child | hand_off | Tool excluded | No session takeover from child | P0 | Y | |
| **H-05** | Attribution after switch | Multi-agent history | Switch agents in UI | Historical authors stable | **No rewrite** of past bubbles’ agent names | P0 | N | |

---

## 13. Matrix J — UI / observability (product feel)

| ID | Pattern | Setup | Stimulus | Expect (UX) | Sev | LLM? | Result |
|----|---------|-------|----------|-------------|-----|------|--------|
| **U-01** | SubagentBlock collapsed | Any successful await | Expand/collapse | Collapsed success ≠ empty failure; expand shows steps **or** honest empty with result still in parent prose | P1 | Y | |
| **U-02** | Denial only on expand (known gap) | Denied call | Collapsed vs expand | Record whether G17 still true; if still collapsed “Failed” only → document severity | P2 | Y | |
| **U-03** | Activity bar | Parallel fan-out | Watch bar | Concurrent children visible; clear on finalize | P1 | Y | |
| **U-04** | Verbose chat | Settings | Toggle | Hidden infra vs visible; delegate default-hidden cases per toolVisibility rules | P2 | N | |
| **U-05** | Board roll-ups | Task + sub-delegate | Board | Delegation roll-ups / altitude if present match reality | P2 | Y | |
| **U-06** | Graph live tree | During fan-out | Graph tab | Live tree not stale forever | P2 | Y | |
| **U-07** | Team vs Agents library | — | Navigate | Operator can find “who can call whom” without old trust screen | P1 | N | |
| **U-08** | Prompt depth advertising | Inspect system prompt / debug | — | Advertised max depth = effective enforced (#477) | P1 | Y | |

---

## 14. Matrix K — Failure / chaos (breaker)

| ID | Pattern | Setup | Stimulus | Expect | Sev | LLM? | Result |
|----|---------|-------|----------|--------|-----|------|--------|
| **E-01** | Gateway restart mid-background | Async running | Restart gateway | Defined: cancel vs lose vs resume — **document actual**; no corrupt session file | P1 | Y | |
| **E-02** | Provider 5xx mid-child | Await child | Kill/provider error | Failed status; parent sees error | P1 | Y | |
| **E-03** | Parent session delete mid-async | — | Delete session | No panic; orphan cleanup | P1 | N | |
| **E-04** | Malicious task_id guess | Other session id | status | Not found | P0 | N | |
| **E-05** | Unwired deny-checker (dev only) | N/A production | — | Fail-closed deny (code assert / unit already) | P2 | N | |
| **E-06** | Max depth zero edge | Edge depth=0 | Any onward | No onward delegation | P1 | Y | |
| **E-07** | God mode interaction | God mode ON | Delegate + bash | Document which fences fall; audit still on | P2 | Y | |

---

## 15. Coverage map (pattern → case IDs)

| Product claim | Cases |
|---------------|-------|
| Unified `delegate` replaces spawn/run_subagent/status | D-01–D-08, D-16 |
| Default background | D-01, D-04 |
| Await / sync | D-02, D-03, D-11 |
| Workspace trust sole authority | G-*, D-13, T-02, G-09, G-10 |
| Mode direct vs task | D-14–15, T-01–03, G-04 |
| Parallel agents | P-01–P-07 |
| Nested multi-hop | N-01–N-08, ADR-040 |
| Depth caps (#477) | N-02–N-05, U-08 |
| Target identity | I-01–I-05, D-03, X-05 |
| External CLI workers | X-01–X-07, P-04 |
| Task board assignment | T-01–T-12 |
| Cross-workspace tasks | T-09–T-10 |
| Approval inheritance | A-01–A-05, N-07 |
| Handoff open / nested excluded | H-01–H-05, N-06 |
| Observability / attribution | U-01–U-06, I-05, H-05 |
| **Browser three hosts** | **BH-01–BH-18** |
| **Browser agent drive** | **BA-01–BA-12** |
| **Take control** | **BC-01–BC-14** |
| **Annotate** | **BN-01–BN-12** |
| **Multi-tab / target=_blank** | **BT-01–BT-14** |
| **Keyboard (esp. pop-out)** | **BK-01–BK-10** |
| **URL / SSRF / launcher** | **BU-01–BU-10** |
| **Browser chaos** | **BE-01–BE-08** |

---

## 15A. Live browser ground truth (ADR-038 → 041)

Do **not** UAT against retired control UX (`Take control` / `Release control` toggles + `Hand to agent` as primary — ADR-040 redesign).

| Topic | Live behaviour | Authority |
|-------|----------------|-----------|
| Three hosts | **(1) Overlay** right Sheet (default, chat visible) · **(2) Pinned** docked `<aside>` side-by-side · **(3) Pop-out** route `/browser-live` new browser tab | ADR-038, ADR-040 D4 |
| Pin | Pin/unpin in panel header; pinned omits Pop-out prop | `BrowserLivePanel` + `ui` store |
| Screencast | CDP pixels via gateway WS `/api/v1/browser/ws` — never raw CDP 9223 to client | ADR-038 |
| Control model | Implicit click-to-drive when agent idle; while agent streaming → watch-only + **Take over** (session-scoped cancel); chip/glow “You’re driving” / agent working | ADR-040 D2 |
| Annotate | ✎ Pen → drag/click crop → upload/media/vision; hidden in **pop-out** (no composer); mutual exclusion with driving | ADR-039, ADR-040 D3 |
| Multi-tab | Adopt new targets (`target=_blank`/`window.open`); tools `browser_list_tabs` / `switch` / `close` / `open_tab`; FE tab strip; **must attach via browserCtx not allocator** | ADR-041 |
| Keyboard | `key`+`code`+**`key_code`** (windowsVirtualKeyCode) for editing keys | post-ADR-039 keyboard fix |
| Shared session | User + agent same Chromium session; screenshot reports **Current page URL** | ADR-038/039 |
| Control multi-viewer | `controlled_by_other` / `control_only` on status frames | ADR-039 UAT |
| Agents | Prefer **Ray / Jim** for browse UAT | delivery notes |

### 15A.1 Host matrix (run critical rows in **all three** hosts)

| Host code | How to enter | Annotate? | Pop-out button? | Chat visible? |
|-----------|--------------|-----------|-----------------|---------------|
| **H-overlay** | Open browser / Watch live → default Sheet | Yes | Yes | Behind panel (modal=false) |
| **H-pinned** | From overlay, click **Pin** | Yes | **No** (omitted) | Side-by-side |
| **H-popout** | From overlay, **Pop out** → new tab `/browser-live?...` | **No** (hidden) | n/a (already out) | Only in original tab |

**Rule:** any row marked **×3 hosts** must PASS on H-overlay, H-pinned, and H-popout (or explicitly BLOCKED with reason). Keyboard + take-control rows are **always ×3** — pop-out typing was a prior live bug.

### 15A.2 Recommended fixtures

| Fixture | Use for |
|---------|---------|
| `https://example.com` | Smoke navigate (note: CTA text is “Learn more”, not “More information”) |
| Local HTML with form + text inputs | Keyboard BK-*, take control typing |
| Local HTML with `<a target="_blank" href="…">` + `window.open` button | Multi-tab BT-* |
| Live: site with real `target=_blank` (e.g. contact → calendar) | Regression of “agent lost after blank” |
| Image-heavy page / photo | Annotate crop non-blank + vision |

---

## 15B. Matrix L — Browser hosts (overlay / pinned / pop-out)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | ×3 | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|-----|--------|
| **BH-01** | Open overlay from **Open browser** launcher | Chat Ray/Jim | ChatControls Open browser | Session starts / attaches; screencast frames | Overlay Sheet opens; frame updates; chat still clickable (modal=false) | P0 | N | — | |
| **BH-02** | Open overlay from **Watch live** on tool call | Agent ran browser tool | Click Watch live on tool card | Same session as agent tools | Panel shows **that** session’s page, not blank | P0 | Y | — | |
| **BH-03** | Pin from overlay | Overlay open | Click Pin | Session continues (no teardown) | Becomes docked aside; chat + browser side-by-side; pin state sticky for session | P0 | N | — | |
| **BH-04** | Unpin back to overlay | Pinned | Unpin | Session continues | Returns to Sheet; screencast not stuck | P0 | N | — | |
| **BH-05** | Pop-out from overlay | Overlay open | Pop out | WS may re-attach in new window; same browser session | New browser tab opens `/browser-live`; live frames; original panel closes or hands off cleanly | P0 | N | — | |
| **BH-06** | Pop-out keyboard focus | Pop-out focused | Click into page field; type | Input frames accepted | **Characters appear in page** (not only in SPA chrome) | P0 | N | H-popout | |
| **BH-07** | Agent drives while **overlay** open | Agent multi-step browse | Watch | Tools execute; screencast follows | User sees agent navigation live | P0 | Y | H-overlay | |
| **BH-08** | Agent drives while **pinned** | Side-by-side | Same as BH-07 | Same | User can read chat + watch browser simultaneously; no layout crash | P0 | Y | H-pinned | |
| **BH-09** | Agent drives while **pop-out** | Pop-out + chat tab | Same | Same | Pop-out follows agent; chat tab still usable | P0 | Y | H-popout | |
| **BH-10** | Close overlay does not kill agent session | Agent mid-browse | Close panel | Chromium session may persist for tools | Agent can continue tools; re-open shows same tab | P1 | Y | — | |
| **BH-11** | Pin state vs active chat session mismatch | Panel pinned to sess A; switch chat to B | Observe | Control cancel is **session-scoped** to panel’s session | Take-over does not cancel wrong chat; no cross-session control leak | P0 | Y | H-pinned | |
| **BH-12** | Pop-out closed by user | Pop-out open | Close browser tab | Clean detach; no zombie WS storm | Re-open from SPA works | P1 | N | — | |
| **BH-13** | No Pop-out when pinned | Pinned | Inspect header | — | Pop-out control **absent** | P1 | N | H-pinned | |
| **BH-14** | Screencast recovers after tab switch in host | Multi-tab | Switch FE tabs | Rebind screencast epoch | Frame updates; no permanent black | P0 | N | ×3 | |
| **BH-15** | Host switch mid-control | Holding control in overlay | Pin or pop-out while driving | Lock semantics defined | Either keep control or clear UI honestly; no half-dead lock | P1 | N | — | |
| **BH-16** | Watch live on **finalized** tool call (GenericToolCall path) | After turn done | Watch live on historical browser tool card | Attaches correct session | Works on finalized card, not only live/streaming card | P0 | Y | — | |
| **BH-17** | Two SPA windows same session | Overlay + pop-out briefly both | Take control in one | `controlled_by_other` on the other | Other shows “Someone else is driving”; cannot steal silently | P0 | N | — | |
| **BH-18** | Mobile/narrow layout pin | Narrow viewport | Pin | — | Usable or graceful; no permanent overflow trap | P2 | N | — | |

---

## 15C. Matrix M — Agent browser job quality

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | ×3 | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|-----|--------|
| **BA-01** | Navigate + screenshot URL header | Ray | Navigate example.com; screenshot | Screenshot text includes `Current page URL:` | User/agent not guessing URL from pixels alone | P0 | Y | any | |
| **BA-02** | Multi-step: navigate → click → get_text | Ray | Force tool chain | Tools succeed in order | Live panel shows each step; final answer grounded in get_text | P0 | Y | ×3 | |
| **BA-03** | Text selector click (has-text / text param) | Page with unique visible text | Click by text | Innermost visible match; no-match message consistent | Agent clicks right control | P0 | Y | any | |
| **BA-04** | Wait / missing selector fail-fast | Missing selector | wait / get_text | Fails in seconds not ~30s hang | Error clear | P0 | Y | any | |
| **BA-05** | Agent after **user** navigated | User omnibox to site B | Agent get_text / screenshot | Reads site B (shared tab) | Agent does not claim still on site A | P0 | Y | ×3 | |
| **BA-06** | Agent after **user** typed in form | User filled field | Agent get_text / submit | Sees user-entered state | Collaborative session proven | P0 | Y | H-pinned | |
| **BA-07** | Soft deny while user controlling | User holds control | Agent browser_click | Soft “user is controlling” (not crash) | Agent narrates wait; panel shows You’re driving | P0 | Y | ×3 | |
| **BA-08** | Resume after user releases | After BA-07 | User release / idle; agent continues | Tools work again | Screencast continues | P0 | Y | ×3 | |
| **BA-09** | Long job with panel open | Multi-page browse | Full task | Completes | No panel freeze; frames keep arriving | P1 | Y | H-pinned | |
| **BA-10** | browser_open_tab tool | Agent | open_tab url | New tab adopted in **same** browser | Tab strip shows new tab; agent can switch | P0 | Y | any | |
| **BA-11** | Wrong agent Watch live | Ava without browser tools if restricted | — | — | Launcher/tools honest | P2 | N | — | |
| **BA-12** | Tool policy deny browser | Agent deny browser_* | Attempt navigate | Policy deny | Structured denial | P1 | Y | any | |

---

## 15D. Matrix N — Take control / Take the wheel (ADR-040)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | ×3 | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|-----|--------|
| **BC-01** | Click-to-drive when agent idle | Panel open, not streaming | Click page | Control lock acquired | “You’re driving” / glow; cursor active | P0 | N | ×3 | |
| **BC-02** | Watch-only while agent streaming | Agent mid-tools | Try click without Take over | No control fight | Clicks ignored or blocked; agent keeps driving | P0 | Y | ×3 | |
| **BC-03** | **Take over** one-click | Agent streaming | Click Take over | Session-scoped cancel of **panel** session; lock to user | **Single click** flips to You’re driving (not 2-click bug) | P0 | Y | ×3 | |
| **BC-04** | Take over does not cancel other session | Panel on sess A; B streaming | Take over on A | Only A cancelled | B continues | P0 | Y | H-pinned | |
| **BC-05** | Mouse click inject | Controlling | Click a link/button | CDP click | Page navigates / UI reacts | P0 | N | ×3 | |
| **BC-06** | Scroll inject | Controlling | Wheel/scroll | Page scrolls | Visible scroll | P0 | N | ×3 | |
| **BC-07** | Type inject plain text | Controlling; focus input | Type hello | InsertText/key path | **hello** appears in page field | P0 | N | ×3 | |
| **BC-08** | Release / lose control | Controlling | Explicit release or agent Take path | Lock cleared | Chip returns to agent/idle | P0 | N | ×3 | |
| **BC-09** | controlled_by_other blocks steal | Two viewers | Second tries drive | Blocked | “Someone else is driving” | P0 | N | H-overlay+H-popout | |
| **BC-10** | Status control_only frame | Other viewer take/release | Observe banner | control_only updates only that axis | Error banner not wiped | P1 | N | — | |
| **BC-11** | Rate limit abuse | Spam inputs | Flood keys | Rate limited / no crash | UI remains usable | P2 | N | any | |
| **BC-12** | Audit log take/release | — | Take + release | Audit entries | (ops check) | P2 | N | any | |
| **BC-13** | Implicit drive does not fire in Pen mode | Pen on | Drag | Annotate, not navigate | No accidental navigate | P0 | N | H-overlay/H-pinned | |
| **BC-14** | Omnibox while driving | Controlling | Submit URL | Navigate SSRF-gated | Page changes; user keeps or re-acquires control per design | P1 | N | ×3 | |

---

## 15E. Matrix O — Annotate (ADR-039/040)

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | ×3 | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|-----|--------|
| **BN-01** | Enter Pen mode | Overlay/pinned | Click Pen | Release control if held | “You’re annotating”; pen cursor | P0 | N | H-overlay, H-pinned | |
| **BN-02** | Drag crop non-blank | Wide viewport (>1280) | Drag region with image content | — | Crop preview / attach **not** white/black empty (`scaleCropToImagePixels`) | P0 | N | H-overlay | |
| **BN-03** | Click-point annotate | Pen on | Single click | Inspect best-effort | Popover; element context optional | P0 | N | H-pinned | |
| **BN-04** | Send annotate to chat | Composer free | Send comment + crop | media:// upload path | Attachment in composer/chat; comment text present | P0 | Y | H-overlay | |
| **BN-05** | Vision describes crop | Vision model | Annotate photo region | Model sees image | Answer refers to cropped content (not only DOM text) | P0 | Y | H-overlay | |
| **BN-06** | Annotate **hidden in pop-out** | Pop-out | Inspect chrome | — | No Pen / no dead-end annotate | P0 | N | H-popout | |
| **BN-07** | Annotate while streaming | Agent streaming | Pen + send | — | Warn or queue; **does not silently drop** | P1 | Y | H-pinned | |
| **BN-08** | Annotate does not clobber draft | Non-empty composer | Send annotate | — | Draft preserved or merge UX honest | P1 | N | H-overlay | |
| **BN-09** | Zero-size drag | Pen | Click-drag zero area | — | Toast/reset; no throw | P1 | N | H-overlay | |
| **BN-10** | Inspect timeout/fail soft | Broken page | Click annotate | ok:false best-effort | Still can send image-only | P1 | N | H-overlay | |
| **BN-11** | Pen vs drive mutual exclusion | Driving | Enable Pen | Lock released | No double handlers | P0 | N | H-pinned | |
| **BN-12** | Annotation targets panel session/agent | Multi-session | Annotate | Pinned (session, agent) | Message goes to correct chat | P0 | Y | H-pinned | |

---

## 15F. Matrix P — Multi-tab / target=_blank (ADR-041) — **prior “agent got lost”**

| ID | Pattern | Setup | Stimulus | Expect (backend) | Expect (UX) | Sev | LLM? | ×3 | Result |
|----|---------|-------|----------|------------------|-------------|-----|------|-----|--------|
| **BT-01** | Click `target=_blank` adopts tab | Fixture or live site | Agent `browser_click` blank link | **New tab adopted in same browser** (not 2nd Chromium on 9223) | Tab strip gains tab; screencast can show new page | P0 | Y | ×3 | |
| **BT-02** | window.open adopts | Fixture button | Agent click | Same as BT-01 | Same | P0 | Y | any | |
| **BT-03** | Agent continues on new tab | After BT-01 | Agent get_text / screenshot | Tools bind active/new tab correctly | Agent **not** stuck on opener page claiming success | P0 | Y | ×3 | |
| **BT-04** | browser_list_tabs | ≥2 tabs | list_tabs | Accurate ids/titles/urls | FE strip matches | P0 | Y | any | |
| **BT-05** | browser_switch_tab | ≥2 tabs | switch | Active changes; screencast rebinds | User sees switch; agent tools hit active tab | P0 | Y | ×3 | |
| **BT-06** | browser_close_tab non-active | ≥2 tabs | close background | Tab gone; active stable | Strip updates; no false death | P0 | Y | any | |
| **BT-07** | Close **active** tab | ≥2 tabs | close active | Rebind to survivor; **no** false session-ended | No “session ended/re-attach” banner; frames continue | P0 | Y | ×3 | |
| **BT-08** | Close last tab | 1 tab | close | Defined teardown or empty state | Honest empty; agent tools fail clearly | P1 | Y | any | |
| **BT-09** | browser_open_tab | — | open_tab with url | Same-browser new tab | Strip + agent can use | P0 | Y | any | |
| **BT-10** | User switches tab while agent works | ≥2 tabs | User strip click mid-agent | Coordination defined | Prefer no silent tool mis-target; document if race | P1 | Y | H-pinned | |
| **BT-11** | User opens tab (if UI allows) | Panel | Open tab control | Adopt | Strip updates | P1 | N | H-overlay | |
| **BT-12** | OAuth/popup style blank | Real or mock | Click login popup | Tab adopted or clear failure | Agent not silent-success on opener only | P1 | Y | any | |
| **BT-13** | Positional index limitations | Documented ADR-041 | Switch by index if exposed | Matches accepted limitations | UX doesn’t claim stable IDs if positional | P2 | N | any | |
| **BT-14** | Live regression: booking/calendar blank | Site with real blank (if allowed) | Full book-slot path | Adopt + fill works or honest block | **No “I clicked but nothing happened” false success** | P0 | Y | H-pinned | |

---

## 15G. Matrix Q — Keyboard (especially pop-out typing bug)

| ID | Pattern | Setup | Stimulus | Expect | Sev | LLM? | ×3 | Result |
|----|---------|-------|----------|--------|-----|------|-----|--------|
| **BK-01** | Plain character typing | Focus text input | Type `abc` | Appears in page | P0 | N | ×3 | |
| **BK-02** | Backspace / Delete | Text present | Backspace | Deletes chars | P0 | N | ×3 | |
| **BK-03** | Enter submits / newline | Form or textarea | Enter | Correct page behaviour | P0 | N | ×3 | |
| **BK-04** | Tab moves focus | Multiple fields | Tab | Focus moves in page | P0 | N | ×3 | |
| **BK-05** | Arrow keys | Text / contenteditable | Arrows | Caret moves | P0 | N | ×3 | |
| **BK-06** | Ctrl/Cmd+A select all | Text | Select all | Selection in page | P1 | N | ×3 | |
| **BK-07** | Ctrl/Cmd+C/V | Text | Copy/paste | Paste works if CDP allows | P1 | N | ×3 | |
| **BK-08** | **Pop-out only:** type after click focus | Pop-out tab | Click field then type | **Must work** (operator-reported failure class) | P0 | N | H-popout | |
| **BK-09** | Pop-out: keys not stolen by SPA shortcuts | Pop-out focused | Type letters used as SPA hotkeys | Characters go to page, not SPA | P0 | N | H-popout | |
| **BK-10** | key_code on wire | Devtools/WS observe optional | key_down | Frame includes key_code for editing keys | P1 | N | any | |

---

## 15H. Matrix R — URL bar, launcher, SSRF, status

| ID | Pattern | Setup | Stimulus | Expect | Sev | LLM? | Result |
|----|---------|-------|----------|--------|-----|------|--------|
| **BU-01** | Omnibox navigate valid URL | Panel open | Enter https URL | Navigate; screencast updates | P0 | N | |
| **BU-02** | Omnibox search-like input | If supported | Query string | Search or clear error | P1 | N | |
| **BU-03** | SSRF blocked host | Internal/metadata IP | Navigate | Blocked | Friendly **security** message (not raw Go) | P0 | N | |
| **BU-04** | DNS fail / typo host | `https://not-a-real-tld.invalid` | Navigate | Fail | Message = **unreachable**, not “blocked for security” | P0 | N | |
| **BU-05** | Malformed URL | `ht!tp://` | Navigate | Fail | Invalid-URL class message | P0 | N | |
| **BU-06** | Error banner clears on next good nav | After BU-03 | Good URL | Success | Banner clears | P1 | N | |
| **BU-07** | Open browser creates session without prior tool | Fresh chat | Open browser | Session ready | User can omnibox before agent | P0 | N | |
| **BU-08** | Hand-to-agent / release bridge (if still present) | Per ADR-040 residual | If UI still exposes | Document actual | No dead control | P2 | N | |
| **BU-09** | Screenshot shows URL after user omnibox | User navigated | Agent screenshot | URL header matches omnibox | P0 | Y | |
| **BU-10** | Live view disabled config | live_view_enabled=false | Open panel | Defined degrade | Honest message | P2 | N | |

---

## 15I. Matrix S — Browser chaos / edge

| ID | Pattern | Setup | Stimulus | Expect | Sev | LLM? | Result |
|----|---------|-------|----------|--------|-----|------|--------|
| **BE-01** | Kill Chromium mid-session | Panel live | Kill chrome process | Detect death | Re-attach or clear error; no infinite spinner only | P0 | N | |
| **BE-02** | Port 9223 contention | Second launch | Start second browser session | Fail or serialize | Clear error; no wedged gateway | P0 | N | |
| **BE-03** | Gateway restart mid-live | Panel open | Restart | Reconnect path | User can re-open; no corrupt state | P1 | N | |
| **BE-04** | WS reconnect | Drop network briefly | — | Client reconnects | Frames resume | P1 | N | |
| **BE-05** | take_control_enabled=false | Config | Try drive | Refused | Honest UX | P2 | N | |
| **BE-06** | Agent tool during panel close race | Close during click tool | — | No panic | Tool result or soft fail | P1 | Y | |
| **BE-07** | Stale fixture text | example.com | Text click “More information” | May no-match | Document correct CTA “Learn more” | P2 | Y | |
| **BE-08** | Zombie chromes from prior UAT | Many chrome PIDs | Before run | Kill list | P10 preflight | P1 | N | |

---

## 16. Suggested execution plan (parallel UAT groups)

Fan out **9 groups** after CI is green. Delegation groups may parallelize freely. **Browser groups: one Chromium (9223) per gateway — do not stack two browser groups on one process.**

### 16.1 Delegation groups

| Group | Persona | Case IDs | LLM | Ports (example) |
|-------|---------|----------|-----|-----------------|
| **G-Del-1** | “Sam — orchestrator” | D-01–D-12, U-01, U-03 | Y | 6081 / 6181 |
| **G-Del-2** | “Priya — trust enforcer” | D-13–D-17, G-01–G-12, T-02–T-03 | Y/N | 6082 / 6182 |
| **G-Del-3** | “Lee — task board” | T-01, T-04–T-08, T-11–T-12, P-07, U-05–U-06 | Y | 6083 / 6183 |
| **G-Del-4** | “Ray — parallel scout” | P-01–P-06, N-01–N-02 | Y | 6084 / 6184 |
| **G-Del-5** | “Nest — depth” | N-03–N-08, U-08, A-01–A-05 | Y | 6085 / 6185 |
| **G-Del-6** | “Ext — 3p + chaos” | X-01–X-07, I-01–I-05, H-01–H-05, E-01–E-07, T-09–T-10 | Y | 6086 / 6186 |

### 16.2 Browser groups (required)

| Group | Persona | Case IDs | LLM | Hosts | Ports (example) |
|-------|---------|----------|-----|-------|-----------------|
| **G-Br-1** | “Dana — surfaces” | BH-*, BU-*, BK-* (all three hosts), BC-01–02, BN-06 | N/Y light | **must complete ×3** | 6091 / 6191 |
| **G-Br-2** | “Sam — agent driver” | BA-*, BT-01–05, BT-14, BC-02–03, BC-07 | Y | prefer H-pinned + H-popout | 6092 / 6192 |
| **G-Br-3** | “Alex — control & annotate” | BC-*, BN-* (vision model), BK-08–09, BH-17, BE-* | Y (vision for BN-05) | H-overlay + H-pinned + pop-out keyboard | 6093 / 6193 |

**Lead synthesis:**  
- `docs/internal/uat/uat-report-delegation-matrix-<date>.md`  
- `docs/internal/uat/uat-report-browser-control-matrix-<date>.md`  
(or one combined report with two scorecards)

Each report: PASS/FAIL per ID · bugs · UX · coverage gaps · readiness 1–5 for that subsystem.

---

## 17. Force-tool prompt kit (copy/paste)

Use when the model wanders. Replace ids as needed.

```
D-AWAIT-WORKER:
Call the tool `delegate` exactly once with:
  agent_id: "worker"
  async: false
  label: "uat-d03"
  task: "Reply with exactly the single word PONG and no other text or tools."
Do not call any other tools.

D-ASYNC-AVA:
Call `delegate` once with agent_id "ava", async true, label "uat-d04",
task "Create a one-line file notes/uat-pong.txt with content PONG then stop."
Then call `delegate` with action "status" and the returned task_id until completed.

D-DENY-AVA-AS-MIA:
You are Mia. Call `delegate` with agent_id "ava", async false,
task "say hi". If denied, explain the denial text.

T-CREATE-WORKER:
Call `create_task` once assigning agent worker, title "UAT T-01",
description "Reply done when picked up".

N-CHAIN:
Call `delegate` async false agent_id "ray" with task:
"You MUST call delegate async=false agent_id researcher with task 'Reply PONG2' and return their answer."

BA-NAV-CLICK:
You are Ray. Call tools only, in order:
1) browser_navigate url="https://example.com"
2) browser_screenshot
3) browser_click with text "Learn more" (or the visible CTA)
4) browser_screenshot
Then reply with the Current page URL from the last screenshot header.

BT-BLANK:
Open the multi-tab fixture page (or elicify contact). Click the control that opens
target=_blank. Then browser_list_tabs, browser_switch_tab to the new tab,
browser_get_text or screenshot proving you are ON the new page — not the opener.
```

---

## 18. Pass / fail thresholds (suggested)

### Delegation

| Gate | Rule |
|------|------|
| **Ship P0 clean** | All P0 rows PASS (or deferred with tracked issue + operator ACK) |
| **No trust theatre** | G-10 PASS; no inert global policy UI |
| **No silent async loss** | D-04, D-12, P-01: multi-tool background children deliver final answers |
| **No identity inheritance** | I-01, I-02, X-05 PASS |
| **Nested real** | N-01 PASS (proves ADR-040 nested delegation live) |
| **Depth honest** | N-04 PASS (proves #477 class fixed in product) |

### Browser control

| Gate | Rule |
|------|------|
| **Three hosts work** | BH-07, BH-08, BH-09 PASS (agent drive on overlay / pinned / pop-out) |
| **Pop-out typing** | BK-08 + BK-01–05 on H-popout PASS (operator-reported gap) |
| **Take over one-click** | BC-03 PASS on all three hosts |
| **No blank crop** | BN-02 PASS (wide viewport) |
| **target=_blank not lost** | BT-01 + BT-03 PASS (adopt + agent continues on new tab) |
| **Close active tab** | BT-07 PASS (no false session death) |
| **Shared session** | BA-05, BA-06 PASS |
| **SSRF messaging** | BU-03 vs BU-04 distinguished |

---

## 19. Known historical defects to re-verify (do not assume fixed)

| Historical issue | Re-check via |
|------------------|--------------|
| check_spawn_status disconnected from spawn | D-06 (must be fixed by `delegate` merge) |
| Global trust Saved-but-inert | G-10 |
| One-hop hard block despite edge | N-01 |
| Silent depth=3 override of edge depth=10 | N-04 |
| Async child killed when parent ends | D-12 (`Critical:true`) |
| Raw child voice as orphan bubble | D-01, D-04 |
| Interrupted shown as failed on reload | D-11 |
| Parent/child tool-policy split-brain | I-04 |
| Attribution rewrite on agent switch | H-05 |
| Delegation denied only visible expanded | U-02 / G17 |
| Expanded subagent “No steps recorded” | U-01 |
| **Blank annotate crop (DeviceWidth vs MaxWidth 1280)** | **BN-02, BN-05** |
| **Modal trap (chat inert behind panel)** | **BH-01** |
| **Pop-out annotate dead-end** | **BN-06** |
| **controlled_by_other multi-viewer desync** | **BH-17, BC-09** |
| **DNS fail labeled as security block** | **BU-04** |
| **Editing keys no-op (missing key_code)** | **BK-02–BK-05, BK-08** |
| **Agent guessed URL from screenshot** | **BA-01, BU-09** |
| **get_text / wait hung ~30s** | **BA-04** |
| **target=_blank not adopted (2nd browser on allocator)** | **BT-01–BT-03** |
| **Close active tab false death** | **BT-07** |
| **Take-over required 2 clicks** | **BC-03** |
| **Watch live only on live tool card, not finalized** | **BH-16** |
| **example.com CTA text wrong in prompts** | **BE-07** |

---

## 20. Artefact control

| Field | Value |
|-------|-------|
| Path | `docs/internal/uat/uat-matrix-delegation-subagent-tasks-2026-07-13.md` |
| Preview HTML | project-artifact `delegation-uat-matrix` (served on pod preview when active) |
| Intended next step | Operator review → mark rows in/out of scope → schedule G-Del-1…6 **and** G-Br-1…3 |
| Related specs | `agent-delegation-spec.md`, ADR-032/036/037/040 (delegation); ADR-038/039/040-take-the-wheel/041 (browser) |
| Supersedes for this topic | Journey 4/6 fragments in `uat-plan-agent-features.md` (delegation); ad-hoc browser UAT notes — **use this matrix** |

---

## 21. Operator review checklist (sign-off)

- [ ] Scope OK (include/exclude X-* external if CLIs absent in env)  
- [ ] **Browser groups G-Br-1…3 required** (or explicitly defer with date)  
- [ ] P0 list accepted as ship gate (delegation **and** browser gates in §18)  
- [ ] Seed matrix §1.1 matches your install expectations  
- [ ] Parallel group plan OK (browser = one Chromium per gateway)  
- [ ] Model choice confirmed (`z-ai/glm-5.2` for tools; vision model for BN-05)  
- [ ] Confirm pop-out typing (BK-08) and target=_blank (BT-01/03) are **P0 must-pass**  
- [ ] Any extra patterns (channel-bound browse, mobile pin, CAPTCHA flows)  

**Reviewer:** _________________ **Date:** _________________ **Decision:** RUN / REVISE / DEFER  

---

*End of matrix artefact (v2 — delegation + browser control).*
