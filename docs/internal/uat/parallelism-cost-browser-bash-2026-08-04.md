# Parallelism Cost — Browser vs Bash Workloads, omnipus-uat-swimlane, 2026-08-04

**Target:** `https://omnipus-uat-swimlane.fly.dev` (app `omnipus-uat-swimlane`, machine `7812791a464028`, **2 dedicated cores** / 3917 MB RAM, `OMNIPUS_MAX_PARALLEL_AGENTS=16`) — freshly scaled, freshly wiped, verified not-onboarded before this run.

**Purpose:** measure real machine CPU/memory cost of N concurrent instances of the *same* agent running the *same* prompt, for a browser-navigation workload (N=4/8/16) and a bash-heavy workload (N=8), and report per-agent memory separately from browser cost.

---

## Headline results

1. **Peak machine CPU:** 84% (single-core-equivalent; 2-core box) at genuine N=16 browser concurrency (achieved via delegation — see finding #2). At N=4/N=8 direct-chat and at N=8 bash, CPU stayed low (peak 26%, ~8-17%).
2. **Peak machine memory (`free -m` used, authoritative):** 996 MB used (of 3917 MB total, ~2662 MB still available) during the N=16 browser run. **Memory pressure (PSI) was 0.00 in *every single sample* taken across the entire exercise** — the box never stalled a process waiting for memory, at any concurrency level tested.
3. **Per-agent memory (gateway process only, clean single-process number):** ~2–3 MB/agent, consistent across N=4 (2.9 MB), N=8 (~2.0 MB), and N=16-via-delegation (3.2 MB gross, but that figure includes the delegating orchestrator's own overhead — see caveats).
4. **Browser (Chromium) cost is separate from and far larger than agent cost**, but it is a *shared, persistent* cost, not N× per-agent: ~65 MB PSS added per new browser-using instance at N=4, non-linearly thereafter (see finding #3 on tab-pool sharing).
5. **The binding constraint was neither CPU nor memory — it was a hardcoded software concurrency gate.** See finding #1. Hardware headroom was never exhausted anywhere in this exercise (0 OOM kills, 0 kernel allocation denials, PSI-memory always 0.00).

---

## Critical finding #1 — the real concurrency ceiling is 8, not 16

`OMNIPUS_MAX_PARALLEL_AGENTS=16` is **not** the effective limit on new concurrent chat sessions. There is a separate, global `AdmissionController` (`pkg/agent/admission.go`) instantiated **once** for the whole gateway (`newAdmissionController(0)` in `NewAgentLoop`, `pkg/agent/loop.go:772`) with:

```go
func newAdmissionController(softCap int) *AdmissionController {
    if softCap <= 0 {
        softCap = runtime.NumCPU() * 4   // = 8 on this 2-core box
    }
    ...
}
```

This gate is **global across every agent**, not per-agent, and it does **not** read `OMNIPUS_MAX_PARALLEL_AGENTS` at all in this call path — the two numbers (16 advertised, 8 actually enforced) are simply disconnected.

**Confirmed live** by attempting a direct-chat N=16 (16 independent top-level WS sessions, all `agent_id=ray`, byte-identical prompt): exactly 8 succeeded and 8 were silently rejected. Gateway log (`/home/omnipus/.omnipus/logs/gateway.log`), grepped for the rejection window:

```
{"level":"warn","component":"agent","active":8,"soft_cap":8,...,"caller":"/src/pkg/agent/loop.go:3327","message":"At capacity — rejecting new session"}
```

(8 identical lines, one per rejected session.) The rejected sessions are **silently dropped with no error surfaced to the WS client** — the client receives a bare `{"type":"done"}` frame with no `stats`, and the session record shows `message_count: 1, tokens_total: 0, cost: 0, tool_calls: 0` (the user message was stored; the assistant never ran at all). **This is a silent-failure UX bug**, not just a capacity limit — a rejected session is indistinguishable, from the client's perspective, from a session that happened to finish instantly.

This explains the whole shape of the data below: N=4 and N=8 always succeeded fully (at or below the cap); N=16 direct-chat did not.

**Workaround found:** the code comment on `AdmissionController` states it "gates inbound user-message dispatch only... [s]ubagent spawn and task-executor dispatch paths are NOT gated." Delegation bypasses this cap. Re-running N=16 as *one* top-level session (Jim) delegating 16 byte-identical tasks to `ray` via `async=true` **did** achieve genuine 16-way concurrency (`subagent_starts: 16`, all admitted). See the N=16 section below.

**Practical implication for the operator:** on a 2-core box, `OMNIPUS_MAX_PARALLEL_AGENTS=16` currently promises capacity the system will not actually grant to direct chat traffic — only delegation-routed traffic gets the full 16. This is worth a follow-up issue independent of this measurement task.

## Critical finding #2 — leaked Chromium processes never returned to zero

Chromium is architected as a small number of shared, persistent OS processes per agent (a "one dedicated" browser + per-agent CDP contexts, `pkg/tools/browser/coordinator.go`), not spawned-and-torn-down per turn. Observed over the ~50-minute exercise:

| Time | Event | Chromium process count |
|---|---|---|
| after N=4 (clean smoke-test warm-up only) | — | 10 |
| after N=4 run | — | 18 |
| after N=8 (×3 attempts) | — | 18 (flat — did not grow further) |
| ~10 min later, before N=16 retry | natural decay | 12 |
| during/after N=16-via-delegation | new tabs allocated | **31** (peak) |
| ~5 min later, into Experiment B | partial decay | 29 → 16 |

**Chromium processes are never torn down when their agent turn finishes** — every measurement after the first was taken on a **dirty baseline** with residual browser processes from a previous level. Partial reaping *does* happen (18→12 over ~10 min, 31→16 over ~10 min), so it is not a permanent leak, but it is **very slow** (tens of minutes) and **never reached zero** at any point I observed. I did not wait long enough to establish a true terminal/clean state — this is a real, unresolved "what I could not establish" gap (see below). This matches the "known live defect" class flagged in the task brief, though in this run it manifested as *slow decay*, not the previous box's *indefinite re-spawn after kill* (I did not re-test the kill/respawn behavior on this box).

**Consequence for methodology:** every level past N=4 is reported against its own actually-measured (dirty) baseline, never an assumed-clean one. This is stated explicitly in each level's numbers below rather than being silently absorbed into the per-agent figure.

## Critical finding #3 — RSS-summing overstates Chromium memory by 2–3×; use PSS + `free -m`

Summing each Chromium process's own `VmRSS` double-counts large shared regions (binary, V8 snapshot, GPU/renderer shared buffers). Confirmed with two independent data points during this run:

- At N=4 peak: summed raw RSS across 18 Chromium processes ≈ 1.95 GB, while PSS (`/proc/<pid>/smaps_rollup`, `Pss:` field) summed to ≈ 555–587 MB — **≈ 3.3× overstatement** from naive RSS-summing.
- A later idle snapshot: 16 Chromium processes summed ≈ 2+ GB raw RSS, while `free -m` showed only 474 MB *total system used* (minus ~82 MB gateway ≈ 390 MB attributable to Chromium, ≈ 25 MB/process) — consistent with the same overstatement.

**Every Chromium number in this report is PSS (via `smaps_rollup`), never summed RSS.** The **authoritative machine-level number is always `free -m` used/available**, per the operator's correction mid-run. Individual RSS values are still shown in raw sample logs for reference but are explicitly labeled and never totaled.

Caveat: PSS and `free -m used` do **not** reconcile 1:1 — Linux classifies many file-backed Chromium mmaps (shared libraries, V8 snapshot) under `buff/cache` rather than `used`, while `smaps`-based PSS attributes those same pages proportionally to the process. This is a known accounting-boundary difference, not an error in either number; both are reported, neither is forced to sum to the other.

---

## Method

- **Load generation:** Python `websockets` clients (no browser session, no cookies) hitting `wss://omnipus-uat-swimlane.fly.dev/api/v1/chat/ws`, authenticating via the legacy first-frame `{"type":"auth","token":...}` path.
- **Same agent, same prompt (hard requirement):** every instance at a given level received a byte-identical prompt and targeted the same named agent (`ray` for browser work — confirmed valid direct chat target with `browser_navigate`/`browser_get_text`/`browser_click`/`browser_type`/`browser_wait`/`browser_screenshot` all allowed, and explicitly forbidden from `search_web`/`fetch_url`; `jim` for bash work — the one core agent with `bash: allow`). `researcher` (the previously-used worker-tier agent) was tried first and rejected outright by the gateway ("this agent is a worker and cannot be a chat target — workers are invoked via delegation"), which is why `ray`/`jim` (core, chat-targetable) were used instead.
- **Verified real browser navigation, not shortcuts:** the prompt explicitly forbids `search_web`/`fetch_url` and mandates exactly 3× `browser_navigate` + 3× `browser_get_text` (to `example.com`, Wikipedia's Octopus article, and IANA's reserved-domains page). Every successful instance's tool-call trace was checked; zero `search_web`/`fetch_url` calls occurred anywhere in Experiment A. Transcript content was also read directly for several delegated N=16 instances — despite an identical prompt, each produced genuinely different page-content summaries (proving independent real execution, not a cached/hardcoded response).
- **N verification:** cross-checked via `GET /api/v1/sessions` (direct-chat levels: N distinct sessions created at the same timestamp) and via `child_count`/`subagent_starts` (delegated N=16: `child_count: 16` on the parent session, `subagent_starts: 16` from the WS stream).
- **Sampling:** `fly ssh console -C 'sh -c "/tmp/sample2.sh <count> 2"'` — a single foreground call per window (never a background wait; every window is one blocking command that returns its own full series, per the coordinator's correction after early attempts stalled on background-notification waits). Captures, every ~2s: `/proc/stat`-delta CPU%, `/proc/loadavg`, `/proc/pressure/{cpu,memory}`, `free -m`, gateway `VmRSS`/`Threads` (PID found by scanning `/proc/[0-9]*/comm` for `omnipus`), and a full Chromium-process inventory with both `VmRSS` and PSS per process.
- **`fly ssh console` has a consistent ~90–100s connection-establishment latency** before the remote command actually starts executing. This was not understood until partway through the exercise and cost two wasted N=8 sampling attempts (windows that started only seconds before the run's own completion, capturing settle-phase state instead of ramp-up). The fix used from N=16 onward: dispatch the WS trigger and the long single foreground sampling call together, sized generously enough (150–200 samples × 2s) to absorb the latency and still cover the full run + settle.
- **dmesg** checked at the end of the exercise: **0 OOM kills, 0 `__vm_enough_memory` allocation denials** (contrast with the earlier, smaller 459 MB box's prior report, which saw explicit denials at N=8 — not reproduced here on the larger box).

---

## Experiment A — browser workload results

### N=4 (direct chat to `ray`)

Actual N=4 verified via `GET /api/v1/sessions` (4 `ray` sessions created at the identical timestamp `2026-08-04T13:41:21`). All 4 completed with real browser calls: **17× `browser_navigate`, 34× `browser_get_text`, 0× `search_web`, 0× `fetch_url`**.

| | Baseline (pre-trigger) | Peak (during run) | Settled (+113s) |
|---|---|---|---|
| Gateway RSS | 80,300 KB | 92,184 KB | 86,956 KB (partial settle) |
| Chromium processes | 10 | 18 (+8) | 18 (no decrease) |
| Chromium PSS | — | ~555 MB (18 procs) | — |
| `free -m` used | 276–285 MB | 492 MB | — |
| `free -m` available | 3,421–3,429 MB | 3,177 MB (min) | — |
| CPU % | 2–6% | transient spike 26%, settled 8–10% | 3–5% |
| PSI cpu (avg10) | ~0 | peak 2.94 | — |
| PSI memory (avg10) | 0.00 | **0.00** | 0.00 |
| Gateway threads | 11 | 11 (unchanged) | 11 |
| Wall clock | — | 134.4s | — |

**Gateway-RSS-only per-agent delta: (92,184 − 80,300) / 4 = 2,971 KB ≈ 2.9 MB/agent.**

**Chromium PSS split by PID** (baseline PIDs vs the 8 new PIDs spawned by this run, since the same 10 baseline PIDs persisted unchanged throughout): baseline-PID PSS ≈ 290 MB (pre-existing, from smoke tests), new-PID PSS ≈ 259 MB added by the 4 new browser-using agents ≈ **65 MB/agent in browser-context cost** — stated separately from, and much larger than, the 2.9 MB/agent gateway-orchestration cost. This 65 MB/agent figure does not need to reconcile with the `free -m` used delta of ~210 MB, for the page-cache-accounting reason given in finding #3.

### N=8 (direct chat to `ray`, ×3 attempts)

Actual N=8 verified via `GET /api/v1/sessions` each time. All three attempts: **0 instances with zero browser calls**; real `browser_navigate`/`browser_get_text` present, zero `search_web`/`fetch_url`. Wall clock varied 69–129s across attempts (real LLM-latency variance, not a methodology artifact).

| | Baseline (clean, chrome warm from N=4) | Peak (best-available window)¹ |
|---|---|---|
| Gateway RSS | 80,596 KB | 97,244 KB |
| Chromium processes | 18 | 18 (flat — no growth beyond N=4's level) |
| Chromium PSS | ~551 MB | ~562 MB |
| `free -m` used | 443–452 MB | up to ~487 MB (see caveat) |
| CPU % | 5–9% | 8% (see caveat) |
| PSI cpu (avg10) | ~0 | 1.6–1.9 |
| PSI memory (avg10) | 0.00 | **0.00** |

**Gateway-RSS-only per-agent delta: (97,244 − 80,596) / 8 = 2,081 KB ≈ 2.0 MB/agent.**

¹**Caveat — this level's CPU/PSI peak is an under-estimate.** Due to the ~90–100s `fly ssh console` connection latency (discovered only after this level), the sampling window for two of the three N=8 attempts started only seconds before the run's own completion — the true mid-run ramp-up (where N=4 showed a transient 26% CPU spike) was missed. The gateway-RSS figure is more trustworthy than the CPU/PSI figures for this level, because gateway RSS reflects accumulated per-turn state that plateaus near completion, while CPU/PSI are instantaneous and volatile. **I explicitly do not claim N=8's true peak CPU was only 8% — it is unmeasured; 8% is a floor, not a peak.**

Chromium process count stayed at exactly 18 for N=8, identical to N=4's post-run level — direct-chat sessions to the same agent identity appear to share one pooled per-agent browser manager (`browserMgrs map[string]*browser.BrowserManager` keyed by agent ID, `pkg/agent/loop.go`), not one Chromium OS process per concurrent session.

### N=16

**Direct-chat N=16 to `ray` is INVALID and was not usable as a measurement** — see critical finding #1. Only 8 of 16 sessions were admitted; the other 8 were silently rejected by the global admission controller with zero tokens/tool-calls. This is itself the most important N=16 finding, not a discarded failed attempt.

**Retried via delegation** (one Jim session, delegating 16 byte-identical tasks to `ray`, `async=true` — bypasses the admission gate per its own code comment): **genuine 16-way concurrency achieved.** `subagent_starts: 16` (all 16 admitted and dispatched in a single burst, confirmed from the WS stream), real browser tool calls confirmed (**77× `browser_navigate`, 97× `browser_get_text`, 0× `search_web`, 0× `fetch_url`**), and reading several children's actual result text confirmed genuinely different summaries per instance despite the identical prompt — real independent execution, not a cached/hardcoded response.

`subagent_ends: 10` by the time Jim's own parent turn completed and replied — this is **not** a runaway; it is expected async-delegation semantics (the parent doesn't block on every child). I confirmed Jim's own session (`updated_at`) stopped advancing more than 5 minutes later with no further activity, i.e., the *orchestrator* did not run away; the remaining children continued independently in the background as designed.

| | Baseline (dirty — chrome decayed to 12 from earlier levels) | Peak |
|---|---|---|
| Gateway RSS | 83,604 KB | 136,744 KB |
| Chromium processes | 12 | **31** (first time exceeding the ~18 ceiling seen at direct-chat levels) |
| Chromium PSS | ~409 MB | **~1,060 MB** (first time crossing 1 GB) |
| `free -m` used | 312–320 MB | **996 MB** (largest swing observed in the whole exercise) |
| `free -m` available | 3,341 MB | 2,662 MB (min) |
| CPU % | 4–5% | **84%** (highest of the whole exercise) |
| PSI cpu (avg10) | ~0 | **25.36** (highest of the whole exercise) |
| PSI memory (avg10) | 0.00 | **0.00** (still zero, even at 996 MB used) |
| Gateway threads | 9–13 | — |

**Gateway-RSS-only per-agent delta: (136,744 − 83,604) / 16 = 3,321 KB ≈ 3.2 MB/agent — gross, not net.** This figure **includes Jim's own orchestrator overhead** (dispatching 16 delegate calls, then repeatedly polling status — 246 total `delegate` tool calls logged, the great majority being status polls, not new dispatches), on top of 16 Ray instances' own cost. It is **not** directly comparable to N=4/N=8's clean per-instance figures, and I have not attempted to separate the two — flagged explicitly rather than presented as a clean number.

Chromium reaching 31 processes (vs. the flat 18 seen at every direct-chat level) suggests **delegated sub-turns get independent tab allocation**, unlike direct-chat sessions to the same agent identity which appear to share one pooled browser manager. 31 (minus 2 `chrome_crashpad` helpers = 29 renderer-class processes) sits close to the global `tools.browser.max_total_tabs` default of 30 — consistent with this run pushing near that configured ceiling.

---

## Experiment B — bash workload (N=8, direct chat to `jim`)

Prompt: write a Python sieve-of-Eratosthenes script (primes to 200,000) and a shell script that generates a 50,000-line word file and computes top-10 word frequency; run both; cat the results. Real, substantive work — not a trivial one-liner.

Actual N=8 verified via `GET /api/v1/sessions`. **7 of 8 completed within the 240s client timeout** (the 8th was still running past the client-side timeout — it had made real bash calls, it simply hadn't finished the full multi-step task yet; this is a client-timeout artifact, not a zero-work failure). **Total 163 bash tool calls across the 8 instances, 0 instances with zero bash calls.**

| | Baseline (dirty — chrome=29, residual from N=16 delegate run) | Peak |
|---|---|---|
| Gateway RSS | 97,240 KB | 113,300 KB |
| `free -m` used | ~771 MB (dirty) | 780 MB |
| `free -m` available | 3,168 MB | 2,877 MB (min) |
| CPU % | 4–6% | **17%** |
| PSI cpu (avg10) | ~0 | **0.96** |
| PSI memory (avg10) | 0.00 | **0.00** |
| Chromium processes | 29 (unrelated to this experiment — pure decay in progress) | 16 (declined during the window; unrelated to bash work) |
| Wall clock | — | 245.3s (1 of 8 instances still running past timeout) |

**Gateway-RSS-only per-agent delta: (113,300 − 97,240) / 8 = 2,008 KB ≈ 2.0 MB/agent.** Note this run started on an *already-dirty, already-elevated* gateway RSS baseline (97,240 KB, itself still climbing from the N=16 delegate run 5+ minutes earlier and not settling) — the *baseline itself* was not clean, and this is stated explicitly rather than absorbed.

**Bash vs. browser, directly compared:** at the same N=8, bash's peak CPU (17%) and peak PSI-cpu (0.96) are both markedly lower than browser's N=4 figures (26% CPU, 2.94 PSI-cpu) and far below browser's genuine-N=16 figures (84% CPU, 25.36 PSI-cpu) — **despite the bash workload doing real, non-trivial computation** (sieve to 200,000; 50,000-line word-frequency count). The per-agent gateway-RSS cost is comparable between the two workload types (~2 MB/agent either way) — **the gateway's own bookkeeping cost is workload-agnostic; the entire CPU/PSI difference between "browser" and "bash" comes from Chromium**, not from the LLM-orchestration layer itself.

---

## Binding constraint, per experiment

- **Experiment A (browser):** at N=4 and N=8, **nothing hardware-level strained** — CPU peaked at 26% (direct-chat, likely with an undermeasured N=8 ramp-up), PSI-memory was 0.00 throughout, and `free -m` available never dropped below 3,177 MB. At genuine N=16 (via delegation), CPU/scheduling pressure became real (84% CPU, PSI-cpu 25.36) but still did not saturate a 2-core box (84% < 200% ceiling), and **memory pressure remained exactly 0.00** even at the highest observed memory usage (996 MB used, 2,662 MB still available). **Memory was never the binding constraint at any level tested.** The actual binding constraint for Experiment A, as configured, was the **software admission-controller soft-cap of 8** (finding #1) — a constraint hit at well under half of nominal hardware capacity, and well under the configured `OMNIPUS_MAX_PARALLEL_AGENTS=16`.
- **Experiment B (bash):** nothing strained at all — CPU peaked at 17%, PSI-cpu peaked at 0.96, PSI-memory stayed 0.00. On this hardware, **"nothing strained" is the honest, legitimate finding for the bash workload at N=8** — there is no evidence the box would have struggled at meaningfully higher N for pure bash work (though this was not tested beyond N=8, and N=8 sits at the same global admission cap as Experiment A, meaning direct-chat bash concurrency also cannot exceed 8 without hitting the same silent-rejection behavior).

---

## What I could not establish

- **A perfectly clean baseline for any level after N=4.** Chromium never returned to zero between levels; every subsequent baseline is a *measured, dirty* starting point, not an assumed-clean one. I did not wait long enough (would have required tens of minutes of idle time I judged not worth spending, given the "no restart" constraint and time budget) to see whether Chromium processes ever reach exactly zero on their own.
- **N=8's true peak CPU/PSI.** Two of three attempts had their sampling window start only seconds before the run's own completion (a `fly ssh console` connection-latency issue not understood until later), so the reported N=8 CPU/PSI figures are floors, not confirmed peaks. Gateway-RSS and memory figures for N=8 are more trustworthy than its CPU/PSI figures.
- **A clean per-agent marginal cost at N=16.** The only way to achieve genuine N=16 concurrency was via delegation through Jim, whose own orchestrator overhead (16 dispatches + ~230 status-poll tool calls) is baked into the same measurement window as the 16 Ray instances. I did not attempt to isolate Jim's own contribution from the 16 children's.
- **The true unique (non-double-counted) physical memory Chromium consumes.** PSS still proportionally attributes some shared pages rather than reporting a true unique-set-size; no `smem`/PSS-to-USS tool was available on this image.
- **Whether the 8-per-agent-turn silent-rejection is intentional policy or an unnoticed gap relative to the advertised `OMNIPUS_MAX_PARALLEL_AGENTS=16`.** This is a code-level observation (admission.go, loop.go), not something resolvable from outside the codebase within this task.
- **Whether Chromium processes killed on this box would re-spawn indefinitely**, as documented for the previous (459 MB) box. I did not repeat the kill test here — the natural-decay behavior (partial reaping over ~10 minutes, never to zero) was the behavior actually observed and is what is reported.

---

## Onboarding credentials (for the operator)

- Username: `uatadmin3`
- Password: `BrowserBashUAT2026Aug4!`
- Provider: `openrouter` / model `z-ai/glm-5.2` (project standard; all 10 seeded agents confirmed running this model post-onboard)

(API key was read from `$OPENROUTER_API_KEY` in the devpod environment and never printed.)

## Raw artifacts

Sampling logs, WS driver scripts (`browser_run.py`, `bash_run.py`, `delegate_browser_run.py`, `sample2.sh`), and per-instance transcripts are in the scratchpad at `uat6/` (not committed — ephemeral working data): `expA_n4/`, `expA_n8*/`, `expA_n16*/`, `expA_n16_delegate.log`, `expB_n8/`, plus raw sampler output persisted under the session's `tool-results/` directory.
