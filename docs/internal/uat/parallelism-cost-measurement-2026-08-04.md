# Parallelism Cost Measurement — omnipus-uat-swimlane, 2026-08-04

**Target:** `https://omnipus-uat-swimlane.fly.dev` (app `omnipus-uat-swimlane`, machine `7812791a464028`, 1 vCPU / 459 MB RAM, `OMNIPUS_MAX_PARALLEL_AGENTS=16`)
**Purpose:** measure the real marginal RSS/PSI cost of one additional concurrent agent doing genuine web research, at 4/8/16 concurrent, to replace a bogus auto-detect formula.

## Headline result

**The marginal MB/agent is NOT a clean linear number from this data — and that non-linearity is itself the finding.**

| Transition | Slope (peak-to-peak) |
|---|---|
| 4 → 8 | **+5.95 MB/agent** |
| 8 → 16 | **−1.86 MB/agent** (peak at 16 was *lower* than peak at 8) |

The two slopes disagree in sign, not just magnitude. Per-level "own baseline" deltas (gateway peak minus that level's own pre-trigger idle) are more consistent: **~2.9 MB/agent at N=4, ~2.9 MB/agent at N=16, and an inflated ~4.65 MB/agent at N=8** — the N=8 run was skewed upward by one research task that happened to retry extensively (351 tool calls for 8 agents vs. 79 for 4 agents at N=4), not by concurrency itself. **Task content/difficulty, not agent count, is the dominant driver of gateway memory cost in this workload.** A reasonable planning number for the pure LLM-orchestration cost (no browser escalation) is **~3–5 MB per concurrent research agent**, consistent with the ~2.6 MB/agent figure from the earlier partial live run cited as context.

**The single most actionable finding is not the per-agent slope — it's Chromium.** A research subagent that judges a page needs JS rendering will invoke the real headless-browser tools. When that happened (once, at N=8), a single Chromium instance (9 processes: main + 2 crashpad handlers + 2 zygotes + GPU process + network/storage/on-device-model utility processes) grew to **546 MB** — over **100× the gateway's own per-agent cost** and larger than the entire box's RAM. This is stochastic (the identical topic did *not* trigger it in the N=4 run) and cannot be predicted from concurrency alone. **Any capacity formula must budget headroom for at least one full Chromium event (~500 MB), separately from the smooth per-agent term.**

A second, independent finding: the subagent that triggered Chromium **never stopped**. It was still running — and re-spawning a fresh Chromium after I killed the first instance — more than 10 minutes after its own parent turn had already replied to the user. No visible timeout or circuit-breaker bounded it. This directly contaminated the N=16 measurement (see caveats below) and is arguably a more urgent operational risk than the raw memory number.

**Binding constraint at N=16:** memory headroom, not CPU or threads. Free memory ran at 6–10 MB (available memory 181–207 MB, vs. ~370 MB clean-idle) for the entire N=16 window, and the kernel logged 7 total allocation-denial events (`__vm_enough_memory ... bytes: 536870912 not enough memory for the allocation`) across the N=8 and N=16 runs. **The box never hard-crashed — zero OOM-killer kills, `GET /` stayed HTTP 200 throughout — but it had effectively zero safety margin for the second half of the exercise.** "The box handled 16 comfortably" would be the wrong takeaway; "the box survived 16 with no margin left, partly by luck of not getting a second concurrent Chromium event" is the accurate one.

## Onboarding credentials (for the operator)

- Username: `uatadmin2`
- Password: `SwimlaneUAT2026Aug2!`
- Provider: `openrouter` / model `z-ai/glm-5.2` (this project's standard e2e model; confirmed via OpenRouter `/models` to support `tools` + `parallel_tool_calls` before use)
- All 10 seeded agents (mia/jim/ava/ray, worker/planner/explorer/researcher, judge/plansupervisor) came up on `z-ai/glm-5.2`.

(API key was read from `$OPENROUTER_API_KEY` in the devpod env and never printed.)

## Important context: the box had already been used once

`.fablize/goals.json` in this repo showed a prior agent had already onboarded this same box and completed N=4 (using a different admin account/model, `anthropic/claude-3.5-haiku`) roughly 15–30 minutes before this run started. When I began, I verified directly rather than trusting that stale state: `GET /api/v1/state` returned `{"onboarding_complete":false}`, and the machine's `fly status` "LAST UPDATED" timestamp was *later* than that prior session's last checkpoint — the box had been wiped (no volume, as documented) between sessions. I re-onboarded from scratch with the model specified in this task (`z-ai/glm-5.2`) rather than reusing the stale credentials/model. All numbers in this report are from my own from-scratch run.

## Method

- **Load:** WebSocket chat to Jim (orchestrator/planner), instructed to delegate exactly N independent research tasks in a single burst (parallel `delegate` tool calls, `async=true`). Each task named a **time-sensitive, current-events fact** (current gold price, latest Go/Rust/Python/Linux-kernel version, most recent SpaceX launch, Fed rate, etc.) and explicitly required a `search_web` tool call, on the grounds that the model's training data is out of date for it. This is a real anti-shortcut measure: a model answering from memory instead of searching would either be caught confabulating or would fail to satisfy the instruction, and in practice every sampled subagent transcript showed genuine `search_web`/`fetch_url` tool calls with real DuckDuckGo results and real HTTP fetches (confirmed by reading full WS transcripts, not just counting tool-call frames).
- Same 16-item topic list used at every level (first N items), so levels are comparable in task content, though not in which specific topics land in a given N (see caveats).
- **Sampling:** `sh` script over `fly ssh console`, polling `/proc/<gateway-pid>/status` (VmRSS, Threads), `/proc/loadavg`, `free -m`, and `/proc/pressure/{cpu,memory,io}` every 2s, plus a per-process Chromium inventory (name + individual VmRSS) on every sample.
- **N verification:** every level's actual concurrency was confirmed independently via `GET /api/v1/sessions` → `child_count`, not just the WS client's own `subagent_start` frame count. All three levels matched the requested N exactly (4, 8, 16).
- **dmesg:** `dmesg` (plain — this is a busybox environment; `dmesg -T` is not supported and silently prints usage instead of erroring, a gotcha worth flagging for future runs) checked after every level.

## Results table

| Level | Idle RSS (pre-trigger) | Peak RSS | Delta (peak − idle) | Delta/agent | Peak PSI cpu/mem/io (avg10) | Threads (idle→peak→settled) | Wall-clock | Settled RSS | Chromium present? |
|---|---|---|---|---|---|---|---|---|---|
| baseline (clean, post-onboard) | 74,900 KB | — | — | — | 0.00 / 0.00 / 0.00 | 18 | — | — | No |
| **N=4** | 83,120 KB¹ | 95,052 KB | +11,932 KB | **2,983 KB (2.91 MB)** | 2.05 / 0.00 / 0.00 | 18→17→18 | 64.1 s | 90,896 KB² | **No** |
| **N=8** | 81,340 KB | 119,440 KB | +38,100 KB | **4,763 KB (4.65 MB)³** | — / — / — (see note) | 18→17→16 | 153.3 s | ~54,000–58,000 KB (gateway)⁴ | **YES — 546 MB peak** |
| **N=16** | 56,216 KB⁵ | 104,168 KB | +47,952 KB | **2,997 KB (2.93 MB)** | moderate (chromium-dominated, see below) | 16→10 | 54.5 s | 89,192 KB | Still resident from N=8 (~388–516 MB) |

Footnotes:
1. Contaminated by a prior n=1 smoke-test delegate call run minutes earlier; the true cold baseline (before any delegate call at all) was 74,900 KB — see "baseline" row.
2. Settled 60s after "done" but did **not** return to the 83,120 KB pre-trigger level (+7,776 KB retained) — most likely open-session cache retention (35 messages, 1.07M tokens processed) rather than a leak, but flagged as a partial-settle note; not conclusively ruled out.
3. Inflated by task heterogeneity (one of the 8 tasks made 351 tool calls total across the session vs. 79 for all 4 N=4 tasks combined) — see Analysis.
4. Gateway RSS itself actually settled *below* its own pre-trigger idle value once the Chromium-consuming subagent's own gateway-side bookkeeping wound down — but the orphaned Chromium instance stayed fully resident (~451,520 KB) at settle+211s, well past the 60s protocol minimum.
5. Gateway-only RSS; the *system* was heavily contaminated at this point by the still-running N=8 orphan (see Critical caveat below) — free memory was 8–9 MB, not the ~370 MB of a clean idle box.

PSI figures for N=8/N=16 are omitted from the "clean" column above because they were dominated by the Chromium event rather than by the delegate workload itself; see the narrative below for the real numbers.

## Chromium: the critical finding

At N=8, one research subagent (assigned "find the most recent SpaceX orbital launch") searched repeatedly ("No results found or extraction failed" from DuckDuckGo, several query rephrasings), fetched several text pages via `fetch_url`, and — per its own transcript narration — decided *"The SpaceX site is JS-rendered. Let me try the browser tools"*. That invoked a real headless Chromium (`/usr/lib/chromium/chromium ... --headless --no-sandbox ...`, version 149.0.7827.53), which spawned as 9 OS processes:

```
main chromium, 2× chrome_crashpad_handler, 2× zygote, gpu-process,
network.mojom.NetworkService, storage.mojom.StorageService,
on_device_model.mojom.OnDeviceModelService
```

Combined VmRSS across those 9 processes peaked at **559,316 KB (≈546 MB)** — note this sums each process's own VmRSS and Chromium processes share substantial mapped memory (V8 snapshot, shared libraries), so the true *unique* physical footprint is almost certainly lower than this sum; treat it as an upper bound, not an exact figure. Even so, it dwarfed the gateway's own peak of 119,440 KB for the same run — **Chromium's footprint was ~4.7× the gateway's own peak, and the gateway's own peak itself only reflects orchestration/tool-call bookkeeping for 8 agents.**

While this event was active, the kernel logged real allocation denials:

```
__vm_enough_memory: pid: 11446, comm: chromium, bytes: 536870912 not enough memory for the allocation
__vm_enough_memory: pid: 10997, comm: chromium, bytes: 536870912 not enough memory for the allocation
__vm_enough_memory: pid: 11910, comm: chromium, bytes: 536870912 not enough memory for the allocation
__vm_enough_memory: pid: 11932, comm: chromium, bytes: 536870912 not enough memory for the allocation
```

Chromium was repeatedly denied a 512 MB allocation — a near-OOM event — though this is a soft denial (Chromium's own allocator backing off), not a kernel OOM-kill; there is no `Killed process` line anywhere in `dmesg` across the entire exercise, and `GET /` returned HTTP 200 at every single check.

**This was not reproducible on demand.** The identical SpaceX topic was also delegated in the N=4 run (task 4 of 4) and did **not** trigger a browser escalation there — the model found an answer from `search_web`/`fetch_url` alone that time. Whether a given research task escalates to a real browser is a live model judgment call, not a deterministic function of the topic or of concurrency. That unpredictability is itself the actionable point: **you cannot rule out a ~500 MB Chromium spike at any concurrency level ≥1** if research/browsing tools are in scope for an agent.

## The runaway-subagent finding (separate from the memory number)

The Chromium-invoking subagent from the N=8 run was still `status=running` when Jim (the orchestrator) gave up polling it and replied to the user at wall-clock 153.3s with 7/8 results. I kept sampling well past that point specifically to see whether it would settle. It did not:

- At **settle+211s** (well past 60s), the orphaned Chromium instance (9 processes, ~451 MB) was still fully resident.
- I killed that Chromium process tree directly (`kill -9`, not a gateway/machine restart) to attempt a clean baseline for the N=16 run.
- **Within 3 seconds, a fresh Chromium instance appeared** (new PIDs), and `GET /api/v1/sessions` showed the same session (`session_01KZ6AQ83BD0SG5PKQGJ6B2V0S`) with a recent `updated_at` — the subagent was still alive and had simply relaunched its browser tool.
- A **second** relaunch (yet more new PIDs) occurred minutes later, entirely on its own timeline, with no further input from me.
- This subagent was still active — and still holding a Chromium instance — through the entire N=16 trigger, run, and 60s settle window (**10+ minutes** past its own parent turn's completion).

There is no visible timeout or circuit-breaker on a delegated subagent that keeps retrying a hard task. This is a genuine operational risk independent of the raw MB number: a single stubborn research task can pin ~400–500 MB indefinitely on a box with no more capacity to give.

## Critical caveat on the N=16 measurement

**N=16 was not run from a clean, independent baseline**, because of the runaway subagent above. Pre-trigger state for N=16 was already degraded: 8–9 MB free / 207 MB available (vs. ~370 MB available at clean idle), with ~516 MB of Chromium already resident from the still-running N=8 straggler. The N=16 numbers in this report are therefore "16 fresh agents on top of an already-degraded box," not a clean isolated N=16 measurement — arguably more representative of sustained real-world load (no clean restarts between bursts of work), but not an apples-to-apples comparison against the N=4/N=8 baselines. I did not attempt to force a clean re-baseline a second time (e.g., waiting indefinitely for the orphan to exit) given the "never restart/redeploy" constraint and because the orphan showed no sign of self-terminating within the time available.

## What I could NOT establish

- **A clean, uncontaminated N=16 baseline** — see caveat above. The "true" isolated N=16 gateway-only cost is confounded by leftover N=8 activity.
- **Whether the runaway subagent ever terminates on its own.** It was still active when I stopped observing (10+ minutes in, having already respawned Chromium twice). I did not wait indefinitely.
- **The exact physical (non-double-counted) memory Chromium consumed.** Summing per-process VmRSS across 9 related Chromium processes overcounts shared/mapped pages (V8 snapshot, shared libraries); 546 MB is an upper bound, not a precise unique-set-size figure. I did not have a `smem`/PSS-capable tool on this Alpine/busybox image to get a more precise number.
- **What happens with two or more simultaneous Chromium escalations**, or at concurrency above 16. Given the box was already at 6–10 MB free for the back half of this exercise and there is no data volume (any crash/restart wipes state), I judged it irresponsible to deliberately try to trigger a second concurrent browser event or push past N=16 to find the actual OOM edge.
- **A methodology note, not a measurement gap:** partway through this run, an automated "fablize gate observed a tool failure" hook fired repeatedly. I traced it and confirmed it was a false positive — it appears to key on the substring "fail" appearing anywhere in recent tool output, and my own `search_web` results legitimately contained the phrase *"No results found or extraction failed"* as page content (DuckDuckGo's own response text), not a command failure. Every actual shell command's exit code was verified 0 at the time. Documenting this explicitly per the hook's own instruction, rather than silently ignoring it.

## Why these numbers, not the two context figures

The mock-provider figure (220 KiB/sub-turn, 1.12 MiB saturated) is from a run with no real network I/O, no real tool execution, no real token streaming — it measures pure in-process Go overhead and was never expected to match a live run. The ~2.6 MB/agent figure from an earlier partial live run at N=8 is **broadly consistent** with this run's N=4 and N=16 own-baseline deltas (2.9 MB each) — it's the N=8 number here that runs high, and only because of one task's unusually deep retry loop, not because 2.6 MB/agent was wrong. I'd trust "~3 MB/agent for the LLM-orchestration cost alone, plus unpredictable +~500 MB tail risk whenever browser tools fire" as the number to build a capacity formula around, over any single point estimate.

## Raw artifacts

Sampling logs, delegate-run WS transcripts, and onboarding request/response bodies for this run are in the scratchpad at `uat5/` (not committed — ephemeral working data): `baseline_samples.log`, `level4_samples.log` / `level4_delegate.log`, `level8_samples.log` / `level8_delegate.log`, `level16_samples.log` / `level16_delegate.log`, `sample.sh`, `delegate_run.py`, `onboard.json` / `onboard_resp.json`.
