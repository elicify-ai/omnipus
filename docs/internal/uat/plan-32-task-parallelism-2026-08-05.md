# Plan-execution-path 32-task concurrency acceptance test — 2026-08-05

**Date:** 2026-08-05
**Target:** `omnipus-uat-swimlane.fly.dev` (app `omnipus-uat-swimlane`, performance-2x: 2 dedicated cores, 3917 MB), branch `feature/plan-swimlane-board`
**Method:** built ONE `Plan` (ADR-053 entity) with 32 independent member `Task`s via the REST API (`contracts/openapi.yaml` — `POST /workspaces/{id}/plans`, `POST /tasks`, `PATCH /tasks/{id}`, `POST /plans/{id}/approve`), executed it, and measured concurrency with two independent instruments. This is the acceptance test for the memory-derived concurrency-cap consolidation (`availableRAM / 3.5 MB`, ADR referenced in `CLAUDE.md`) specifically on the **Plan-execution dispatch path** — distinct from the earlier `delegate`-tool run (`max-parallel-concurrency-gap-2026-07-31.md`) that first found only ~7 simultaneous processes against an expectation of many more.

---

## Headline result

**Peak concurrent tasks that genuinely executed the intended bash workload: 6.** Peak API-reported `in_progress` count: 23. The two instruments disagree, and the disagreement itself is the most important finding — it is root-caused below, not just noted.

**The global memory-derived admission cap was NOT meaningfully tested by this run.** Two *other*, unrelated ceilings bound real concurrency far below where the global cap (`effective_max_parallel_agents` = 1024–1028 across repeated reads, confirmed live and fluctuating with available RAM, never 2 and never 16) could ever bind:

1. **Tool-policy mismatch (test-design confound, and a real product gap).** The 32 tasks were round-robined across all 8 non-system workspace agents (`mia`, `jim`, `ava`, `ray`, `worker`, `planner`, `explorer`, `researcher`). Only **2 of the 8** (`jim`, `worker`) have `bash` allowed by policy. All 24 tasks assigned to the other 6 agents (`mia`, `ava`, `ray`, `planner`, `explorer`, `researcher`) failed, and every single failure's `result` field cites the identical cause verbatim: *"the bash tool is denied by \[agent\]'s policy"* (delegate/create_task/run_task fallbacks also denied). This matches `CLAUDE.md`'s own documented seed: *"Jim's seed grants him `bash: allow` so he has shell access on a fresh install"* — implying (and this run empirically confirms) the other core agents do not get it by default. **`worker` also has it** (not documented in `CLAUDE.md` but confirmed empirically: 4/4 `worker` tasks succeeded).
2. **A hardcoded, non-configurable per-agent concurrency cap:** `defaultMaxConcurrentTasksPerAgent = 3` (`pkg/agent/task_executor.go:26`, enforced at `task_executor.go:483-495`), counting `in_progress` tasks per `agent_id`. This is a **separate gate from, and not overridden by,** the global `EffectiveMaxParallelAgents()` dispatch semaphore (`task_executor.go:464-472` — the consolidated RAM/3.5MB cap this whole exercise exists to validate). With only 2 bash-capable agents in play, the achievable ceiling was mathematically `2 × 3 = 6` — which is **exactly** what the OS-level instrument measured, independently, with no foreknowledge baked into the sampling script.

Both root causes were confirmed against the actual Go source (not inferred from behavior alone) via a dedicated read-only research pass before the run, then confirmed a second time against the live task-failure text after the run — the code-level prediction and the live observation match exactly.

---

## Instrument 1 — OS-level process scan (ground truth for "real work happening")

Sampled every ~2s for the whole run window (240 samples, one `fly ssh console` command, `for i in $(seq 1 240); do <scan /proc/[0-9]*/comm>; sleep 2; done`) plus a separate 30-sample settle window afterward. Walked `/proc/[0-9]*/comm` directly per instructions (not `pgrep -f`).

| Metric | Peak | When |
|---|---|---|
| `sleep` processes (primary concurrency instrument) | **6** | 06:05:39 UTC |
| `bash` processes | **0**, entire run | — (exec replaces the shell in place; no persisting `bash`-named process ever appears — `sleep` is the correct/only reliable instrument here, matching the task brief's own guidance) |
| chromium processes | **0**, entire run | — (no browser tool used, as required) |
| gateway VmRSS | 97,372 kB | 06:05:45 UTC |
| gateway Threads | 16 | during run |
| `free -m` used | 133 MB | 06:07:16 UTC |
| 1-min loadavg | 0.33 | early in run |
| CPU/mem/IO PSI | near-zero throughout | — |

## Instrument 2 — Plan/Task REST API poll (independent, second instrument)

Sampled every ~2s for the same window (240 iterations, one Python process, one command) by fetching `GET /api/v1/plans/{id}` + `GET /api/v1/tasks?workspace_id=...` and counting member tasks by status and `in_progress` count per `agent_id`.

| Metric | Peak | When |
|---|---|---|
| `in_progress` count (API) | **23** | 06:05:38 UTC (14s after approve) |
| per-agent `in_progress` at peak | `{researcher:3, explorer:3, ray:2, planner:3, jim:3, ava:3, mia:3, worker:3}` | 06:05:38 UTC |

Note every non-`jim`/`worker` agent is already pinned at exactly 3 in that very first sample — the hardcoded per-agent cap engaging immediately, before any of those turns had even failed yet.

## Why the two instruments disagree (root-caused, not hand-waved)

At 06:05:38 the API said 23 tasks were "in progress." The OS scan at the same moment showed only 6 `sleep` processes. Pulling every task's full record after the run resolved this completely: the API's `in_progress` state reflects **"an LLM turn has started"**, not **"a bash command is executing."** 24 of the 32 turns started, immediately (or after a delegate/run_task fallback attempt) discovered their assigned agent's policy denies `bash`, and transitioned to `failed` — anywhere from 4 seconds to 439 seconds later (highly inconsistent latency, itself worth a follow-up: some agents fail fast, others burn most of a 5-minute timeout on a doomed fallback). Those turns were real, resource-consuming LLM calls the whole time they were "in progress," but never once touched the shell. Only the 8 tasks assigned to `jim`/`worker` ever spawned a real `sleep` process — and 3+3=6 of those 8 could be in flight at any one instant because of the per-agent cap.

**Final task outcome (rechecked ~13 min after approve, then the plan was stopped):**

| Agent | bash policy | Outcome |
|---|---|---|
| `jim` | allow | **4/4 done** |
| `worker` | allow | **4/4 done** |
| `mia` | deny | 3/4 failed (bash denied); **1/4 stuck `in_progress` for 13+ minutes with zero forward progress** — an anomaly, flagged below, not root-caused within this run's scope |
| `ava` | deny | 4/4 failed |
| `ray` | deny | 4/4 failed |
| `planner` | deny | 4/4 failed |
| `explorer` | deny | 4/4 failed |
| `researcher` | deny | 4/4 failed |

8 done, 23 failed, 1 stuck-then-force-stopped. **100% success rate on the 8 tasks assigned to a bash-capable agent; 0% on the other 24** (23 failed cleanly, 1 hung).

---

## Compact time series (thinned to ~15-25s intervals; full logs retained in scratchpad)

| T (UTC) | sleep (OS) | in_progress (API) | gw_rss_kB | free-m used (MB) | load(1m) |
|---|---|---|---|---|---|
| 06:05:24 | — | plan approved | — | — | — |
| 06:05:34 | 3 | — | 96,984 | 124 | 0.33 |
| 06:05:38 | ~6 | **23 (peak)** | ~97,300 | ~127 | 0.28 |
| 06:05:45 | 6 | 18 | **97,372 (peak)** | 128 | 0.25 |
| 06:06:07 | 6 | 15 | 94,848 | 125 | 0.18 |
| 06:07:09 | 6 | 13 | 94,696 | 132 | 0.06 |
| 06:08:34 | 6 | 13 | 93,224 | 126 | 0.14 |
| 06:09:07 | 6 | 12 | 94,420 | 122 | 0.16 |
| 06:10:27 | 6 | 12 | 92,308 | 125 | 0.04 |
| 06:11:33 | 3 | 8 | 91,780 | 122 | 0.01 |
| 06:12:16 | 3 | 6 | 92,404 | 124 | 0.00 |
| 06:13:14 | 3 | 5 | 91,604 | 126 | 0.00 |
| 06:14:28 | — | 5 (stopped decreasing — 1 stuck) | — | — | — |
| 06:19:03–06:20:08 (60s settle, post-stop) | 0–1 | — | **90,620 kB (flat, 30/30 samples)** | 119–125 | 0.00–0.04 |

The curve shows a clean, monotonic **decay** from the tool-policy-denial failures resolving one by one over the ~5-minute window they each burned before failing — not a rise-plateau-rise "wave/batch" pattern. There is no batching signature in this data because the limiting factor was never a batch scheduler; it was the fixed per-agent-cap ceiling (flat at 6) sitting underneath a large, slowly-draining pool of doomed turns.

---

## Memory per task

Using the operator-supplied pre-run idle baseline (80,400 kB) and the **instrument-confirmed genuine peak concurrency (6)**, per the task brief's own rule — *"never ÷32 unless 32 were genuinely concurrent"*:

```
(97,372 kB peak − 80,400 kB baseline) / 6 genuinely-concurrent tasks ≈ 2,829 kB/task (≈ 2.8 MB/task)
```

For completeness, against the API's (confounded) peak of 23 turns-in-flight:

```
(97,372 kB − 80,400 kB) / 23 ≈ 738 kB/task
```

Both numbers are trivially small either way — this workload never got large enough, in either interpretation, to produce a meaningful memory-per-task signal. The gateway's own RSS barely moved during the whole run (96,984 → 97,372 kB, a ~400 kB swing) and settled to 90,620 kB afterward — **below** the in-run baseline, with zero indication of a leak.

---

## Machine stability

- Peak `free -m` used: **133 MB** (of 3,917 MB total) — trivial.
- Peak loadavg (1m): **0.33**.
- CPU/memory/IO PSI: at or near zero throughout (`memPSI` was `0.00` on every single sample, all 270 combined).
- `dmesg`: BusyBox `dmesg` has no `-T` flag (worked around with plain `dmesg`); ring buffer contained only boot-time messages (VM was never restarted during the test — consistent with no crash). `grep -iE "oom|out of memory|killed process|allocation fail"` → **no matches**.
- Zero OOM kills, zero allocation denials, zero instability of any kind. The machine was never remotely stressed — a direct consequence of true concurrency topping out at 6, not because the global cap held under real load.

---

## Did all 32 tasks complete successfully?

**No.** 8/32 done (25%), 23/32 failed (72%, all cleanly attributable to tool-policy denial, not a crash or timeout in the underlying mechanism being tested), 1/32 stuck `in_progress` for 13+ minutes with no forward progress before the plan was manually stopped (`POST /plans/{id}/stop` — this task then resolved to `failed[stopped_by_user]` as part of that cleanup, its root cause not further investigated within this run's scope). This is flagged as a genuine anomaly worth a follow-up: every other bash-denied task eventually gave up and failed (4s–439s); this one (`mia`, task `91e5f58f`) never did on its own.

**Independence was verified programmatically, not assumed:** `GET /tasks?workspace_id=...` filtered to the plan's 32 members showed `blocked_by` edges = 0 across all 32, confirmed both immediately after creation and again mid-run.

---

## `effective_max_parallel_agents`

Read three times across the session:

| When | Value |
|---|---|
| Before this run (reused prior onboarding) | 1026 |
| Mid-run | 1024 |
| Just before the run (separate read) | 1028 |

The small fluctuation (1024–1028) is itself informative: it confirms the value is being **computed live from available RAM** on each read (matching the documented `availableRAM / 3.5 MB` formula), not served from a frozen/cached config value — consistent with the fix under test being live, but **this run could not exercise it**, because concurrency never got anywhere close to even the low end of that range.

---

## What this run could NOT establish

- **Whether the global memory-derived admission cap holds under genuine load approaching hundreds of concurrent tasks.** This run topped out at 6 real concurrent bash executions; the cap (~1024–1028) was never remotely approached. A clean test of the global cap specifically would require either (a) many more bash-allowed agents (11+, to clear `agentCount × 3 ≥ 32`), or (b) removing/raising the hardcoded `defaultMaxConcurrentTasksPerAgent = 3` for the duration of a test, or (c) a dispatch path that doesn't route through per-agent `task_executor` admission at all. None of these were attempted here — they would require either creating new agents (out of scope for "build a plan with 32 tasks") or touching production code (out of scope for this QA role).
- **Root cause of the single stuck `mia` task.** It matches the same "bash denied, delegate/run_task also denied" failure class as the other 23, but unlike all of them, it never resolved on its own in 13+ minutes. Not root-caused within this run — flagged for follow-up, not fixed or further chased here.
- **Whether assigning all 32 tasks to only `jim` + `worker` (i.e. designing around the tool-policy confound from the start) would have revealed the per-agent cap alone, cleanly, without the policy-denial noise.** Not attempted as a follow-up run in this session — the finding was already unambiguous from the data collected (the per-agent cap engaged identically and immediately for every agent, bash-allowed or not, before any denial-driven failures had occurred).
- **The exact tool-policy JSON for each agent.** `GET /api/v1/agents/{id}` did not return a `tools.builtin.policies` field in its response body (checked directly, came back absent for all 8 agents queried) — the policy-denial conclusion rests on the agents' own consistent, verbatim self-reported failure text plus the clean behavioral correlation (100% success for `jim`/`worker`, 0% for the other 6), not on inspecting the policy object directly. A `GET`-able per-agent tool-policy endpoint (if one exists under a different path) was not located during this run.

---

## Methodology notes

- Plan API discovered from `contracts/openapi.yaml` (routes: `POST /workspaces/{id}/plans`, `POST /tasks`, `PATCH /tasks/{id}`, `POST /plans/{id}/approve`, `POST /plans/{id}/stop`) — used directly via the REST API (not the agent-side plan tools), with a reused, still-valid admin bearer token from this session's own earlier onboarding (not re-onboarded, per instructions).
- Each of the 32 tasks: `action: llm`, one placeholder `check`-kind acceptance criterion (`command: "true"`, `expected_exit_code: 0`) to satisfy the unconditional FR-084 criteria gate at plan-approve time, prompt instructing "run `sleep 60` five times sequentially via the bash tool only, report nothing in between." No `blocked_by`, no `stream`, no `write_set` set on any task (confirmed via research that empty `write_set` does not trigger an isolated-checkout path on a fresh run — that path is wired only into the resume-a-failed-plan flow, not initial dispatch).
- Tasks were created in `inbox` (the mandatory landing state) and explicitly `PATCH`ed to `next` before approval, since `dispatchReadyMembers` only drains `status == next` members and `POST /plans/{id}/approve` does not itself promote member task status.
- Two independent, single-command sampling instruments were run concurrently for the whole window (one backgrounded `fly ssh console` proc-scan, one foregrounded local API-poll process) — no foreground-sleep-waiting-on-a-background-job pattern was used.
- The plan was manually stopped (`POST /plans/{id}/stop`) after data collection to clean up the one hung task rather than leaving it running indefinitely on the shared UAT box.

## Known false-positive noise during this session

The `fablize` skill's `PostToolUse` gate flagged "a tool failure" repeatedly throughout this session. Every instance checked was a false positive from its regex matching the literal substrings `"failed"`/`"FAILED"`/`"error:"` inside spec/schema documentation text being read (e.g. `Task.yaml`'s `failed_reason` field, `AcceptanceCriterion` doc comments) or inside this run's own print-statement labels (`"CREATE TASK FAILED"`) — not from any actual non-zero exit code. No real tool-invocation failure occurred in this session outside of the plan-execution task failures documented above (which are the subject of this report, not an artifact of the harness).
