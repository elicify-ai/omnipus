# 32-Agent Concurrency & Memory Floor — UAT Report

**Date:** 2026-08-05
**Target:** `https://omnipus-uat-swimlane.fly.dev` (app `omnipus-uat-swimlane`, machine `7812791a464028`, performance-2x: 2 dedicated cores, 3917 MB)
**Purpose:** Validate the new memory-based `max_parallel_agents` auto-default in production, and measure the per-agent memory floor for a cheap, minimal-LLM-traffic workload (bash-only, no browser).

**Run terminated early by operator instruction** (mid-way through the planned settle/dmesg step) because a different test was about to start on the same shared machine. This report covers only the data actually collected before that stop; the gaps are called out explicitly in their own section.

---

## Headline

| Metric | Value |
|---|---|
| **`effective_max_parallel_agents`** | **1028** (`max_parallel_agents: 1028`, `tools_on_demand: true`) — confirms the memory-based auto-default is live (not 2, not 16) |
| **Peak concurrent agent turns actually observed** | **32 of 32** (sustained, not momentary — see below) |
| **Memory per agent (peak RSS delta ÷ peak concurrent N=32)** | **≈1.71 MiB/agent** (56,168 KiB delta ÷ 32) |
| **Peak gateway RSS** | 129,888 KiB (vs. 73,720 KiB idle baseline) |
| **Peak machine memory used** (`free -m`) | 173 MB used, out of 3,917 MB total (4.4%) |
| **Peak CPU PSI** (`some avg10`) | 0.36 (i.e. ≤0.4% of a 10 s window stalled on CPU) |
| **Peak memory PSI** | 0.00 throughout — never any memory-pressure stall |
| **Chromium processes** | 0 at every one of the 200 real samples across the whole run — no browser contamination |
| **Turns completed successfully** | 32/32 (`"type":"done"` received for all 32; zero non-success `tool_call_result`, zero error frames) |
| **OOM kills / allocation denials** | **NOT ESTABLISHED** — dmesg check was never reached (see Gaps) |
| **Admin credentials created during onboarding** | username `uat-admin`, password `Uat32Stability!2026` (API key was read from `$OPENROUTER_API_KEY` and never printed) |

**Bottom line:** the machine stayed stable for everything that was actually measured — no errors, no failed turns, memory usage never exceeded 4.4% of available RAM, and memory-pressure PSI was flat zero for the entire run. The per-agent memory floor for this cheap bash-only workload (≈1.7 MiB) is lower than the ~2–3 MB/agent floor measured on the prior browser/web-research workload, consistent with the hypothesis that browser-related overhead was the larger driver of the earlier number. However, **whether there was any OOM activity in the kernel log could not be checked** — the operator ordered all further commands against the box stopped before that step ran.

---

## 1. Setup

- `GET /` → 200. `GET /api/v1/state` → `{"onboarding_complete":false}` (freshly wiped, confirmed).
- 60 s idle baseline (30 samples @ 2 s, before onboarding/launch): gateway pid 651 steady at **VmRSS = 73,720 KiB, Threads = 19**, chromium = 0, `free -m` used 106–112 MB / available 3,596–3,602 MB, loadavg 0.05–0.16, CPU PSI `avg10=0.00`, memory PSI `avg10=0.00`.
- Onboarded via `POST /api/v1/onboarding/complete` (CSRF token from the `__Host-csrf` cookie set by `GET /`): admin `uat-admin` / `Uat32Stability!2026`, provider `openrouter` / model `z-ai/glm-5.2`, API key sourced from `$OPENROUTER_API_KEY` (never printed, never persisted to the report). Response: `200`, bearer token issued.
- `GET /api/v1/performance` → **`{"effective_max_parallel_agents":1028,"max_parallel_agents":1028,"tools_on_demand":true}`** — confirms the auto-default formula (availableRAM ÷ 3.5 MB) is live in this deployment; matches the expected ≈1027 figure from the pre-run `MemAvailable: 3,681,876 kB`.

## 2. Workload

32 identical WS chat dispatches to the same named agent (`jim` — chosen because Jim's seeded tool policy grants `bash: allow` on a fresh install per project convention), using the proven `wschat.py` client (`/tmp/.../scratchpad/uat2/wschat.py`, hardcoded to this app's WS endpoint). Prompt to all 32, verbatim:

> "Run the bash command "sleep 60" five times in a row, one after another. Do not use any other tool. Do not report anything between runs. After the fifth sleep completes, reply with just the word done."

- `GET /api/v1/sessions` **before launch**: `{"sessions":[]}` (baseline 0).
- All 32 `wschat.py send` client processes launched together via a single shell loop; **`GET /api/v1/sessions` immediately after launch → 32 sessions**, all `agent_id: jim`, `status: active`, same title text. **Actual N = 32, verified directly against the server, not assumed.**
- Tool usage across all 32 sessions: **`{'bash'}` only** — no other tool was ever invoked by any of the 32 agents (checked via every `tool_call_start` frame across all 32 client logs). Confirms no browser/Chromium contamination from the workload itself, in addition to the 0-Chromium-process sampling below.
- Of the 32 agents: 27 issued 5 separate sequential `bash` calls (`sleep 60` each), matching the literal instruction; 5 issued a single combined call (`sleep 60 && sleep 60 && sleep 60 && sleep 60 && sleep 60`). Both patterns are cheap, bash-only, and equally valid interpretations of "run sleep 60 five times in a row."
- All 32 sessions reached `"type":"done"` (32/32). Zero `tool_call_result` with a non-`success` status. Zero `"type":"error"` frames. **All 32 turns completed successfully.**

## 3. The concurrency question — how many of the 32 ran AT ONCE

This is the question the coordinator flagged as the single most important one, after independently sampling the box mid-run and counting only **7** concurrent `sleep` processes. That 7-count is **not disputed as a real observation** — but it is a single snapshot, and the full time series shows the concurrency level was **not flat**; it oscillated between the full 32 and periodic troughs. The evidence below is reconstructed from two independent sources that agree with each other:

### 3a. Client-side dispatch/result timestamps (the authoritative source)

Built from the actual `tool_call_start` → `tool_call_result` timestamp pairs recorded by each of the 32 independent WS client connections (140 bash calls total across all 32 sessions — not inferred, directly timestamped at dispatch and completion for every call):

- **Dispatch stagger:** all 32 `session_started` events landed within **1.36 seconds** of each other; the first `tool_call_start` across all 32 agents spanned only **5.61 seconds** end-to-end (from +0.00s to +5.61s). **The 32 dispatches were effectively simultaneous, not staggered over a meaningful window.**
- **Peak concurrent bash calls / distinct sessions with an in-flight call: 32 of 32**, reached repeatedly and **sustained for 50+ second windows**, not momentary blips. A 5-second-bucket sweep of the whole run shows, e.g.:

  ```
  t+10s through t+60s   → active_sessions = 32  (continuous, 50s)
  t+75s through t+125s  → active_sessions = 32  (continuous, 50s)   ← peak RSS sample falls inside this window
  t+140s through t+185s → active_sessions = 32  (continuous, 45s)
  t+215s through t+255s → active_sessions = 32  (continuous, 40s)
  t+285s through t+305s → active_sessions = 32  (continuous, 20s)
  ```

  Interspersed with these are real, shorter troughs where concurrency drops — **t+65s: 11**, **t+130s: 24**, **t+190/195s: 19 / 11**, **t+260s: 11**, **t+330s: 3** — caused by the 27 "five separate calls" agents periodically re-synchronizing on their inter-call gap (dispatch latency + brief model turnaround between one `sleep 60` finishing and the next starting), roughly once per ~60–70 s cycle.

  **This is very likely what the coordinator's single snapshot caught.** A `ps aux` sample taken during any of those trough windows would plausibly show a low single-digit `sleep` count — 7 is entirely consistent with landing in one of the ~10–20-second troughs between calls, not with a true concurrency ceiling of 7.

### 3b. Server-side thread/RSS correlation (independent corroboration)

The server-side sampling loop (started slightly after the client dispatch, running continuously through the whole run) shows the **same oscillating shape**, correlated in time with the client-side sweep:

- Idle baseline: 19 threads / 73,720 KiB.
- During the run, gateway threads repeatedly hit a ceiling of **39**, at *several different RSS plateaus* (105,224 / 129,884 / 114,876 / 107,760 / 110,424 KiB), each separated by dips to lower thread counts (14–36) — matching the client-observed trough timing almost exactly. The **peak RSS sample (129,888 KiB @ ts=1785906686)** falls squarely inside the client-measured "32 concurrent sessions" window (ts ≈ 1785906635–1785906685).

**Conclusion: the actual peak concurrent N was 32 of 32**, sustained across multiple substantial windows in the run (not a single instant) — established independently from client-side call timestamps and corroborated by server-side thread/RSS plateaus lining up in time. The lower counts observed at other moments (by the coordinator, and independently reproduced in this data) are real troughs in an oscillating pattern, not the ceiling. **The memory-per-agent figure below is computed against this measured peak of 32 — not against an assumed value, and not by dividing by 32 in the face of contrary evidence.**

## 4. Memory per agent

- Peak gateway RSS: **129,888 KiB**. Idle baseline: **73,720 KiB** (steady across 30 samples). Delta: **56,168 KiB (54.85 MiB)**.
- Peak concurrent N, established per §3: **32**.
- **Memory per agent ≈ 56,168 KiB ÷ 32 ≈ 1,755 KiB ≈ 1.71 MiB/agent.**

This is lower than the ~2–3 MB/agent floor measured on the prior browser/web-research workload — consistent with this run's "no browser" constraint (confirmed: 0 Chromium processes at every one of the 200 real samples, and only the `bash` tool ever invoked by any of the 32 agents) and minimal LLM traffic (each turn: one short prompt, 1–5 short bash calls, one short "done" reply; per-turn stats show ~25k tokens and ~$0.019 cost for a 5-call session, ~320s duration).

## 5. Machine-level stability

| Metric | Idle baseline | Peak during run |
|---|---|---|
| `free -m` used | 106–112 MB | **173 MB** (4.4% of 3,917 MB total) |
| `free -m` available | 3,596–3,602 MB | ≥3,534 MB (never dropped meaningfully) |
| loadavg (1m) | 0.05–0.16 | up to ~0.18 |
| CPU PSI (`some avg10`) | 0.00 | **0.36** peak |
| Memory PSI (`some avg10`) | 0.00 | **0.00** — never any stall on memory, at any sample |
| IO PSI (`some avg10`) | ~4.2 (idle housekeeping) | 0.02 peak during run (lower than idle — idle number was retention-sweep IO, not run-related) |
| Chromium processes | 0 | **0** at every sample |

No errors, no failed tool calls, no failed turns, no dropped WS connections were observed in anything sampled. Memory-pressure PSI was flat zero for the entire window. **Everything actually measured indicates the machine stayed stable during the run.**

## 6. Settle behavior (partial — see Gaps)

The last of the 32 sessions reached `"done"` at ts=1785906906.36. The server-side sampling loop (already in flight from earlier in the run) continued for a further **91.6 seconds** past that point, to ts=1785906998, showing:

- Resting RSS **flat at 103,528–103,532 KiB** for the observed tail — **not decayed back to the 73,720 KiB pre-run idle baseline** within this ~92 s window (residual ≈29.8 MiB above baseline, and the trend had plateaued rather than continuing to decline over the last ~90 s of observation).
- Threads settled to 12 (below the original 19-thread idle baseline, for reasons not investigated further).

The coordinator's own later, independent check reported the box **fully idle at 80,400 KiB**, 0 sleep/bash processes, 117 MB used, load 0.00 — i.e., by the time they checked (some time after my last sample), the resting RSS had continued down closer to (though still ~6.7 MB above) the original 73,720 KiB baseline. This is consistent with settle simply taking longer than the ~92 s this report's own sampling window covers, rather than a genuine leak — but that conclusion is inferred from the coordinator's one later data point, not directly observed by this report's own continuous sampling.

## 7. Gaps — what could NOT be established

- **dmesg / OOM check was never performed.** The planned step (checking `dmesg -T | tail -20` for OOM kills or allocation denials) was reached only after the coordinator ordered all further commands against `omnipus-uat-swimlane` stopped, because a different test was about to start on the shared machine. **No claim is made here about kernel-level OOM activity, in either direction.**
- **Full settle-to-baseline was not directly observed** by this report's own sampling (only ~92 s of post-completion data, ending at a still-elevated 103.5 MB plateau) — see §6. The coordinator's later, independent single-sample check (80,400 KiB, fully idle) is cited but was not this report's own measurement.
- **No commands were issued against the target machine after the stop instruction was received.** Everything in §3 onward beyond the already-completed background sampling job is derived from data already collected locally (server sampling log + 32 client-side JSONL transcripts) before that instruction arrived.

## 8. Data sources

All raw data referenced above is preserved at `/tmp/claude-1000/-home-dev-omnipus3/9a5cc9d5-94c8-4246-b11e-938e082e3387/scratchpad/uat32/`:
- `idle_baseline.log` — 30-sample, 60 s pre-launch idle baseline.
- `run_sampling.log` — 201-line continuous server-side sampling series covering the whole run (gateway RSS/threads, `free -m`, PSI ×3, loadavg, chromium count, every ~2 s).
- `logs/agent_01.jsonl` … `agent_32.jsonl` — full WS frame transcripts for all 32 sessions (session_started, tool_call_start/result ×140, done ×32).
- `sessions_check1.json` — the `GET /api/v1/sessions` response confirming 32 real server-side sessions immediately after launch.
- `launch_meta.txt` — per-process launch metadata for the 32 client dispatches.
