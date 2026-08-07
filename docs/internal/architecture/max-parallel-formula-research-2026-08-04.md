# `max_parallel_agents` — a resource-grounded formula, derived from measurement

**Date:** 2026-08-04
**Branch:** `feature/plan-swimlane-board`
**Status:** RESEARCH + DESIGN PROPOSAL. **No production code was changed.** The patch in §9 is proposed, not applied.
**Supersedes (partially):** `docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` — that document's G2 called for exactly this measurement, and its G1 is **now stale** (see §8.1).
**Scope note:** provider rate limits are explicitly **out of scope** per the operator's requirement. Nothing in this formula considers them.

---

## 1. Headline

**The marginal cost of one concurrent sub-turn is 220 KiB, rising to 1.12 MiB when its context is saturated.** Measured on the real `spawnSubTurn` path, not estimated.

The current formula budgets **1.5 GiB per sub-turn**. It overestimates by:

| Case | Measured | Current formula assumes | Overestimate |
|---|---|---|---|
| Minimal-context sub-turn | **219.8 KiB** | 1536 MiB | **7,155×** |
| Context-saturated sub-turn (the code's own auto bound) | **1141.9 KiB (1.12 MiB)** | 1536 MiB | **1,377×** |

Proposed replacement:

```
maxParallel = max( 4, min( GOMAXPROCS × 32,
                           (memLimit − reserve) / 2 MiB,
                           2500 ) )
                     where reserve = max(384 MiB, memLimit / 4)
```

The third term is **not** an arbitrary policy ceiling like the current 16. It is a physical bound: Omnipus's session writes are blocking file I/O, which — unlike its LLM calls — **does** consume an OS thread per concurrent write (measured in §5.7), and the Go runtime kills the process outright at 10,000 threads. It only binds above 78 cores. See §6.7 and §7.

Behaviour, with the current formula for comparison:

| Machine | Current | **Proposed** | Binding term | Worst-case RSS at cap | % of RAM |
|---|---|---|---|---|---|
| 1 vCPU / 256 MB | 2 | **4** (floor) | floor | 4.2 MiB | 1.6 % |
| **1 vCPU / 459 MB (the live UAT box)** | **2** | **32** | cpu | 33.6 MiB | 7.3 % |
| 1 vCPU / 1 GB | 2 | **32** | cpu | 33.6 MiB | 3.3 % |
| 2 cores / 2 GB | 2 | **64** | cpu | 67.2 MiB | 3.3 % |
| 4 cores / 8 GB | 2 | **128** | cpu | 134.4 MiB | 1.6 % |
| 8 cores / 16 GB | 6 | **256** | cpu | 268.8 MiB | 1.6 % |
| 16 cores / 64 GB | 14 | **512** | cpu | 537.6 MiB | 0.8 % |
| 64 cores / 256 GB | 16 (ceiling) | **2048** | cpu | 2.1 GiB | 0.8 % |

**There is no policy ceiling** — the number rises with real hardware, and the only upper term is physical. The 256 MB box correctly collapses to the floor. The 459 MB box gets 32, not 500. Note the CPU term binds on every machine above ~512 MB: with the measured per-sub-turn cost, **memory is simply not the constraint people assume it is** — it binds only on genuinely tiny boxes.

---

## 2. What the current formula does, and the three separate defects

`pkg/config/config.go:483-493`:

```go
func autoDetectMaxParallel() int {
	cpuBased := runtime.NumCPU() - 2
	ramBased := int(float64(totalRAMBytes()) / (1.5 * 1024 * 1024 * 1024))
	val := cpuBased
	if ramBased < val {
		val = ramBased
	}
	return clampParallel(val)   // floor 2, ceiling 16
}
```

### D1 — The memory divisor is wrong by ~1,377×
`1.5 GiB` models a sub-turn as a dedicated CPU-bound process. §5 measures it at 1.12 MiB worst case. On the 459 MB UAT box `ramBased` evaluates to `0`.

### D2 — `runtime.NumCPU()` is cgroup-blind, and is wrong in *both* directions
Verified in the exact toolchain in use (go1.26.5, `runtime/debug.go:149-156`):

> `NumCPU` returns the number of logical CPUs usable by the current process. The set of available CPUs is checked by querying the operating system **at process startup**.

It does **not** consider the cgroup CPU quota. `GOMAXPROCS` (Go 1.25+) does — `runtime/debug.go:51-61`:

> …the Go runtime computes the "average CPU throughput limit" as the cgroup CPU quota / period. In cgroup v2, these values come from the `cpu.max` file… The Go runtime typically selects the default GOMAXPROCS as the **minimum of the logical CPU count, the CPU affinity mask count, or the cgroup CPU throughput limit**.

Consequences of using `NumCPU()`:
- **Container with `--cpus=1` on a 64-core host:** `NumCPU()` = 64 → `cpuBased` = 62, while the process can genuinely use 1 CPU. Wildly over.
- **Real 1-vCPU VM (the UAT box):** `NumCPU()` = 1 → `cpuBased` = **−1**. Wildly under, and the reason the result is the floor constant.

`GOMAXPROCS` is also re-evaluated up to once per second (`debug.go:66`), so it tracks a live limit change; `NumCPU()` is frozen at startup.

### D3 — `/proc/meminfo` `MemTotal` is the wrong number twice over
`pkg/config/meminfo_linux.go` reads `MemTotal`:
1. **In a container `MemTotal` reports the HOST's memory**, not the cgroup limit. A 512 MB container on a 256 GB host reads 256 GB.
2. It is **total**, not **available** — it ignores everything already resident.

### D4 (separate, and arguably the worst) — `clampParallelExplicit` silently clamps
`config.go:459-468` silently reduces any explicit value > 16 to 16. An operator setting `128` gets `16`, with a 200 response and a success toast.

This is precisely the **ADR-037 anti-pattern this project explicitly bans** — a control that looks like it worked and changed nothing. It is the same defect class as the Delegation Graph screen (ADR-037) and the default-agent singleton (release blocker, fixed 2026-07-26). See §7.

---

## 3. Part 1 — What actually bounds concurrent I/O-bound goroutines in Go

Every claim below was verified by reading **the toolchain actually in use**: `go1.26.5 linux/amd64`, `GOROOT=/home/dev/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.5.linux-amd64`. Citations are `file:line` in that tree. This is deliberately stronger evidence than release notes or blog posts.

| Claim | Verified fact | Citation | Real constraint here? |
|---|---|---|---|
| Goroutine initial stack | `stackMin = 2048` | `runtime/stack.go:78` | **Folklore as a cost model** — see below |
| Stack start size is adaptive | `startingStackSize` is recomputed **every GC** from the observed average live stack size | `runtime/stack.go:1386-1429` | Yes — this is why "2 KB per goroutine" understates reality |
| Max stack | `maxstacksize = 1000000000` (1 GB, 64-bit) | `runtime/proc.go:160` | No |
| Netpoller decouples the M | `netpollblock` → `gopark`; `netpollAdjustWaiters` tells the scheduler a G is parked on the poller so the M is returned | `runtime/netpoll.go:548`, `:529-535` | **Yes — the load-bearing fact.** A goroutine awaiting an HTTP response holds **no OS thread** |
| OS thread ceiling | `sched.maxmcount = 10000`; exceeding it is `throw("thread exhaustion")` — **fatal and unrecoverable**, not an error | `runtime/proc.go:863`, `:974-977` | **Yes — but only via the file-I/O path, not the HTTPS path.** See the next row |
| **Regular-file I/O is NOT netpolled on Linux** | epoll rejects regular files, so `internal/poll` sets `fd.isBlocking = 1` and the write blocks its OS thread. The stdlib says so verbatim: *"a file descriptor that is not supported by epoll/kqueue; for example, disk files on Linux systems"* | `os/file_unix.go:212-217`, `internal/poll/fd_unix.go:62-71` | **Yes — the constraint this document nearly missed.** Omnipus fsyncs on the session path (§5.7) |
| `GOMAXPROCS` bounds *parallel execution*, not *concurrency* | Default = min(logical CPUs, affinity mask, cgroup quota/period); never < 2 unless logical < 2; updated ≤ 1/s | `runtime/debug.go:51-67`, `runtime/cgroup_linux.go:112-113` | **Yes — this is the correct CPU input** |
| `NumCPU()` is cgroup-blind and startup-frozen | see D2 above | `runtime/debug.go:149-156` | **Yes — this is the current bug** |
| TLS per-connection buffers | `maxPlaintext = 16384`, `maxCiphertext = 16384 + 2048`. **Write buffer is pooled** (`outBufPool`, a `sync.Pool`), so only the read side is per-connection | `crypto/tls/common.go:65-66`, `crypto/tls/conn.go` | Partly — see §5.5; the classic "32 KB per conn" is half stale |
| Transport pooling | `DefaultMaxIdleConnsPerHost = 2`; `MaxConnsPerHost` defaults to 0 = unlimited | `net/http/transport.go:61`, `:1600` | Contextual — see §6.8 on FDs |

### 3.1 The "2 KB goroutine" figure is stale as a cost model
`stackMin` is 2048, but two things make it a bad basis for sizing:
1. `startingStackSize` is **adaptive** (`stack.go:1386-1429`) — the runtime raises the starting size based on observed averages.
2. Stacks grow by copying when a deep call chain needs more.

Omnipus's turn path is deep. **Measured `StackInuse` per sub-turn goroutine: 67.4 KiB** (§5.2) — 33× the `stackMin` figure. Anyone sizing from "2 KB per goroutine" would be as wrong, in the other direction, as the current formula's 1.5 GiB.

### 3.2 What genuinely fails first, ranked

1. **Memory** — the only term that scales with *content*, and the one this formula must actively bound. 1.12 MiB per saturated slot (§5.3-5.4).
2. **OS threads, via the session-write path** — *not* via LLM calls. A sub-turn's HTTPS wait holds no thread, but its JSONL/session `fsync` **does**, for the duration of the write. Measured: **1000 concurrent fsyncing goroutines → 999 OS threads** at GOMAXPROCS=8 (§5.7). The ceiling is 10,000 and hitting it is a **fatal `throw`**, not a recoverable error (`proc.go:974-977`).
3. **CPU** — not because goroutines occupy cores while parked (they do not), but because a burst of simultaneous *completions* does real work. Measured 4.47 ms CPU per full sub-turn lifecycle (§5.6).
4. **File descriptors** — one FD per in-flight **HTTP/1.1** TLS connection; `ulimit -n` here is 10,240. Largely dissolves under HTTP/2 (§5.5), which every major LLM API speaks. See §6.7.
5. **Scheduler overhead** — a non-issue. Goroutine count grew **exactly 1.000 per sub-turn** (R² = 1.00000) and 1024 concurrent sub-turns dispatched in 1054 ms wall.
6. **Ephemeral ports** — folklore at this scale (`ip_local_port_range` gives ~28,000 per destination tuple) and moot under HTTP/2 connection reuse.

**Folklore, explicitly:** "one goroutine ≈ one core", "goroutines are 2 KB", "GOMAXPROCS caps concurrency", and "scheduler overhead limits goroutine count" are all false or irrelevant here.

**The correction I had to make:** "thread exhaustion is a non-issue" is what the HTTPS-only analysis says, and it is **wrong for Omnipus**, because Omnipus also writes files on that same path. This is documented rather than quietly fixed because it is the single most important thing this research changed about the answer (§7).

### 3.3 A footgun this design depends on: the `go.mod` language version
`containermaxprocs` and `updatemaxprocs` default to `1` in the Go 1.26 toolchain (`runtime/runtime1.go:375`, `:403`) — **but GODEBUG defaults are amended to the language version in `go.mod`**, and `internal/godebugs/table.go:31,72` record both as `Changed: 25, Old: "0"`.

So **a module declaring `go 1.24` or lower gets container-aware GOMAXPROCS switched OFF**, and `GOMAXPROCS(0)` silently degrades to `NumCPU()` — defeating the entire point of §6.1.

Verified for this repo: **`go.mod` line 3 is `go 1.26.5`** → both ON. (CLAUDE.md says "1.26.4"; immaterial, but stale.)

**This must be stated as a constraint in the code comment**: lowering the `go` directive below 1.25 silently breaks the CPU term.

---

## 4. Part 2 — Measurement method

### 4.1 Design
Two independent probes.

**Probe A — the real agent path.** A temporary `_test.go` in `pkg/agent` (deleted after this document; never committed) driving the **actual `spawnSubTurn`** function — not a reconstruction of it — via the same `mustNewAgentLoop` + hand-built parent `turnState` fixture the existing `cancel_async_delegate_repro_test.go` uses.

A `measureParkProvider` blocks inside `Chat()`: every entry announces itself on a channel, then parks until released. **N announcements = N sub-turns provably concurrently in flight**, each parked exactly where a real sub-turn waits for an LLM response. This is the steady state that matters.

**Probe B — the real transport.** A standalone stdlib-only program issuing N concurrent `https://` requests through `net/http` + `crypto/tls` against a local TLS server that holds each response open. This measures what Probe A's mock provider necessarily omits.

### 4.2 Controls against the obvious traps

| Trap | Control |
|---|---|
| Go returns memory to the OS lazily, so RSS is a high-water mark and level N is contaminated by level N−1 | **Every concurrency level runs in its own process** (the test re-execs `os.Args[0]` with `OMNIPUS_MEASURE_N`). Each level's baseline is its own clean process. |
| Fixed process/AgentLoop overhead being attributed to sub-turns | Baseline sampled **after** the whole `AgentLoop` is constructed and **before** any sub-turn. All figures are deltas. |
| Goroutine stacks are not in `HeapAlloc` | `StackInuse`/`StackSys` captured separately; **RSS is treated as authoritative** since it captures stacks, heap, and allocator overhead together. |
| Sweep/finalizer noise | Two `runtime.GC()` calls before every sample. |
| Those GCs polluting the CPU measurement | CPU (`Getrusage` utime+stime) captured at four explicit checkpoints, with the GC-bearing sample excluded from the drain-phase delta. |
| Single-shot noise | **3 repetitions per level; medians reported**, standard deviation shown for RSS. |
| Fixed startup dominating the slope | Least-squares fit over **N ≥ 8 only**, so the one-time cost is in the intercept, not the slope. |

### 4.3 Environment and its limits
8 cores, 15,993 MB total (≈3.8 GB available), `ulimit -n` 10240, Linux 6.12.91, go1.26.5, `-tags goolm,stdjson`. **This box is shared with other agent processes**, which is the main source of RSS variance. It is not the 1-vCPU/459 MB UAT box; see §11.

---

## 5. Part 2 — Raw results

### 5.1 Concurrency ramp, minimal context (3 reps/level, medians, per-level process isolation)

| N | dRSS (B) | RSS sd | dHeapAlloc | dHeapInuse | dStackInuse | dGoroutines | spawn wall (ms) |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 0 | −32,768 | 50,054 | 32 | 0 | 32,768 | 0 | 0 |
| 1 | 2,191,360 | 41,028 | 76,728 | 180,224 | 131,072 | 4 | 11 |
| 2 | 2,572,288 | 552,586 | 101,904 | 352,256 | 229,376 | 5 | 12 |
| 4 | 3,465,216 | 243,118 | 139,192 | 606,208 | 327,680 | 7 | 17 |
| 8 | 5,464,064 | 234,118 | 253,592 | 1,269,760 | 819,200 | 11 | 19 |
| 16 | 7,319,552 | 948,832 | 447,848 | 1,892,352 | 1,605,632 | 19 | 35 |
| 32 | 9,687,040 | 602,117 | 797,448 | 2,826,240 | 2,916,352 | 35 | 46 |
| 64 | 17,547,264 | 1,111,730 | 1,462,760 | 4,423,680 | 4,915,200 | 67 | 90 |
| 128 | 24,416,256 | 583,106 | 2,919,720 | 7,659,520 | 10,616,832 | 131 | 158 |
| 256 | 40,583,168 | 862,799 | 5,619,024 | 13,148,160 | 19,136,512 | 259 | 281 |
| 512 | 80,842,752 | 4,846,671 | 11,052,576 | 25,485,312 | 37,060,608 | 515 | 552 |
| 1024 | 137,191,424 | 7,586,990 | 21,699,424 | 47,357,952 | 70,909,952 | 1027 | 1054 |

### 5.2 Fit (least squares, N ≥ 8)

| Metric | Slope per sub-turn | Intercept | R² |
|---|---:|---:|---:|
| **RSS (authoritative)** | **130,548 B = 127.5 KiB** | 7,091,613 | 0.99478 |
| StackInuse | 68,973 B = 67.4 KiB | 909,458 | 0.99929 |
| HeapInuse | 45,299 B = 44.2 KiB | 1,456,564 | 0.99917 |
| HeapAlloc | 21,114 B = 20.6 KiB | 147,556 | 0.99992 |
| Goroutines | **1.000** | 3 | **1.00000** |

`StackInuse + HeapInuse = 111.6 KiB`, against a measured RSS slope of 127.5 KiB — the two independent accountings agree to within allocator overhead. **Linear through 1024 with no knee.**

### 5.3 Content-proportional term (N = 64 fixed, per-sub-turn prompt padding varied)

| pad (B/sub-turn) | dRSS | RSS/sub-turn | KiB/sub-turn |
|---:|---:|---:|---:|
| 0 | 14,053,376 | 219,584 | 214.4 |
| 4,096 | 15,237,120 | 238,080 | 232.5 |
| 16,384 | 18,681,856 | 291,904 | 285.1 |
| 65,536 | 31,223,808 | 487,872 | 476.4 |
| 262,144 | 67,837,952 | 1,059,968 | 1035.1 |

Fit: **3.17 bytes of RSS per byte of context, per sub-turn** (R² = 0.99524). The ~3× amplification is the content held simultaneously in the session history, the assembled message slice, and the marshalling buffer.

**This is the key structural finding: per-sub-turn cost is not a constant. It is dominated by context size.** So the right memory unit is the context bound, which the codebase already defines.

### 5.4 Confirmation at the codebase's own context bound

`pkg/utils/context.go:24-36` — `CalculateDefaultMaxContextRunes(contextWindow)` = `contextWindow × 0.75 × 3`. With the default `contextWindow = 128000` (`pkg/agent/loop.go:10176-10179`), the auto bound is **288,000 runes**.

Measured directly at that point (N = 64, pad = 288,000, 3 reps):

| N | pad | RSS/sub-turn | KiB/sub-turn |
|---:|---:|---:|---:|
| 64 | 288,000 | **1,074,752 B** | **1049.6** |

Model prediction from §5.2 + §5.3: `130,548 + 3.17 × 288,000 = 1,043,508 B` (1019 KiB). **Measured 1049.6 KiB vs predicted 1019 KiB — 2% error.** The model is validated at its own worst case, not extrapolated to it.

Stability check with realistic content at higher concurrency (pad = 16,384): N=128 → 210.2 KiB/sub-turn; N=256 → 218.7 KiB/sub-turn. No divergence.

### 5.5 Transport cost (Probe B — real `crypto/tls` + `net/http`)

| N | dRSS | dHeapInuse | dStackInuse | dGoroutines | RSS/conn |
|---:|---:|---:|---:|---:|---:|
| 1 | 36,864 | 8,192 | 65,536 | 2 | 36,864 |
| 8 | 2,490,368 | 1,310,720 | 458,752 | 37 | 311,296 |
| 32 | 4,788,224 | 2,490,368 | 1,212,416 | 157 | 149,632 |
| 64 | 7,573,504 | 3,653,632 | 2,064,384 | 317 | 118,336 |
| 128 | 14,479,360 | 6,774,784 | 3,997,696 | 637 | 113,120 |
| 256 | 25,780,224 | 12,722,176 | 7,143,424 | 1277 | 100,704 |

Fit (N ≥ 8): **94,542 B = 92.3 KiB per concurrent in-flight HTTPS request** (R² = 0.99870), 5 goroutines/connection.

**Two honest caveats:**

1. **Loopback.** The TLS server runs in the same process, so both client *and* server sides are counted (hence 5 goroutines: caller + Transport `readLoop` + `writeLoop` + server conn + server handler). Omnipus is only the client, so its true share is roughly half. **The full 92.3 KiB is used below as the conservative bound**, which is the safe direction.

2. **This is HTTP/1.1.** `httptest.NewTLSServer` does not enable h2 unless asked, and the probe's custom `Transport` did not set `ForceAttemptHTTP2`. **Every major LLM API speaks HTTP/2**, where the picture is materially cheaper: one FD serves many multiplexed streams (independently reported at ~11 connections for 3,000 concurrent requests) and per-request memory drops to roughly a sixth. So the 92.3 KiB figure is a **conservative upper bound for the transport term**, and the FD constraint (§6.7) largely dissolves in production.

Also relevant, and a correction to the commonly-cited "~32 KB per `tls.Conn`": the **write** buffer is pooled (`crypto/tls/conn.go` `outBufPool`, a `sync.Pool`) so it scales with concurrently-*encrypting* goroutines, not live connections. Only the **read** side (`Conn.rawInput`) is per-connection and retained, and only grows to record size (`maxPlaintext = 16384`) once responses are actually large — i.e. exactly when streaming LLM output.

### 5.6 CPU service time

| N | total CPU (ms) | CPU ms/sub-turn |
|---:|---:|---:|
| 8 | 67.1 | 8.387 |
| 32 | 192.6 | 6.020 |
| 128 | 633.4 | 4.949 |
| 512 | 2318.2 | 4.528 |

Fit (N ≥ 8): **S = 4.4658 ms CPU per complete sub-turn lifecycle**, +45.6 ms fixed, **R² = 0.99969**.

### 5.7 OS threads consumed by the session-write path (Probe C)

Omnipus fsyncs on the persistence path — `pkg/memory/jsonl.go:275`, `pkg/session/manager.go:268`, `pkg/fileutil/file.go:97,121,172,263` (the `WriteFileAtomic` family). Because regular-file I/O is not netpolled on Linux (§3), each concurrent `Sync()` blocks an OS thread.

Measured directly (standalone probe, GOMAXPROCS = 8, 64 KiB write + `Sync()` per iteration):

| concurrent fsyncing goroutines | threads before | **threads peak** |
|---:|---:|---:|
| 1 | 5 | 5 |
| 16 | 5 | **18** |
| 64 | 5 | **68** |
| 256 | 5 | **260** |
| 1000 | 5 | **999** |

**OS thread count tracks concurrency essentially 1:1, entirely independently of GOMAXPROCS.** This is the opposite of the LLM-call path, where 1024 concurrent sub-turns added zero threads.

**Consequence for the cap:** a cap of N implies a worst case of ~N OS threads at the moment those sub-turns simultaneously persist. `sched.maxmcount = 10000` and exceeding it is `throw("thread exhaustion")` — the process dies. This is the one genuine reason to retain *some* upper bound, and it is a physical one.

### 5.8 Method validation
Two independent checks that the numbers above mean what they claim:

- **RSS is trustworthy on this platform.** Go has defaulted to `MADV_DONTNEED` on Linux since 1.16 (`runtime/runtime1.go:411-421` forces `debug.madvdontneed = 1` on `GOOS == "linux"`), so freed pages leave RSS promptly rather than lingering as they would under `MADV_FREE`. Had `GODEBUG=madvdontneed=0` been set, every RSS figure here would be meaningless.
- **Two accountings agree.** `StackInuse + HeapInuse = 111.6 KiB` vs a measured RSS slope of 127.5 KiB (§5.2) — the residual is allocator/span rounding, in the expected direction and magnitude.

One known bias, in the safe direction: samples are taken immediately after all N park, so they include some transient allocation the scavenger has not yet returned (independent measurement suggests burst RSS runs up to ~2× steady-state after a forced `FreeOSMemory`). **The figures here therefore over-state steady-state cost**, which is the correct direction for a reservation constant.

**Reproducibility check.** Re-running two points after all analysis was complete, single-shot rather than median-of-3:

| point | recorded median | re-run | delta |
|---|---:|---:|---:|
| N=64, pad=0 | 219,584 B/sub-turn | 254,400 | **+15.9 %** |
| N=64, pad=288,000 | 1,074,752 B/sub-turn | 1,184,832 | **+10.2 %** |

Same magnitude, and **higher** — so the recorded figures, if anything, understate. This ±16 % single-shot spread on a box shared with other agent processes is exactly why medians of 3 repetitions were used throughout, and it is the practical precision of every RSS number in this document. **Treat all RSS figures as ±20 %, not as exact.** Even against the re-run worst case of 1.18 MiB, the 2 MiB reservation retains a 1.73× margin.

### 5.9 Consolidated

| Component | Cost per concurrent sub-turn |
|---|---:|
| Agent path — goroutine stack | 67.4 KiB |
| Agent path — live heap | 44.2 KiB |
| **Agent path — RSS (measured, authoritative)** | **127.5 KiB** |
| Transport — TLS + HTTP connection (conservative) | 92.3 KiB |
| **Total, minimal context** | **219.8 KiB** |
| **Total, context-saturated (288,000 runes)** | **1141.9 KiB = 1.12 MiB** |
| Goroutines | 1.0 |
| CPU | 4.47 ms per lifecycle |

---

## 6. Part 3 — The proposed formula and where every constant comes from

```
cpuSlots = GOMAXPROCS(0) × 32
reserve  = max(384 MiB, memLimit / 4)
memSlots = memLimit > reserve ? (memLimit − reserve) / 2 MiB : 0
maxParallel = max(4, min(cpuSlots, memSlots))
```

### 6.1 `GOMAXPROCS(0)` instead of `NumCPU()−2`
Per §3/D2 this is simply the correct input: it is the only one of the two that respects a cgroup CPU limit, and it never returns a negative or nonsensical value. The `−2` is dropped — it existed to reserve cores for a workload that does not occupy cores.

### 6.2 The `× 32` multiplier — derived, not guessed
Standard I/O-bound concurrency: `N_perCore = W / S`, where `W` is wall-clock wait and `S` is CPU service time.

- Measured `S = 4.47 ms` (§5.6).
- Apply a **4× pessimism factor** for real work the mock provider does not do (TLS handshake, streaming SSE parse, JSON decode of a large real response) → `S_real ≤ 18 ms`.
- Take `W = 1 second` — an aggressively short LLM round-trip; real ones are 2–60 s.

`W / S_real = 1000 / 18 ≈ 56` concurrent sub-turns per core.

**Round down to 32**, retaining ~40 % CPU headroom for the gateway's own request serving, GC, and channel workers. So 32 is ~1.75× conservative against an already 4×-pessimistic model using a 1-second LLM call. At a realistic `W = 5 s` the unfactored figure would be 1,120 per core — the proposal is 35× more conservative than that.

### 6.3 The `2 MiB` per-slot reservation — measured, with a stated margin
Measured worst case **1.12 MiB** (§5.9), at the codebase's own auto context bound (§5.4). **2 MiB gives a 1.79× margin**, covering:
- multi-byte content — the context bound is in **runes**, so a CJK context of 288,000 runes is ~3× the bytes of the ASCII one measured;
- tool-result payloads injected into a turn;
- allocator fragmentation and single-machine measurement variance.

**The margin is compounded by the reservation model itself:** the formula budgets the *worst case for every slot simultaneously*, which is already pessimistic — a fleet of sub-turns is not all context-saturated at once, and a minimal one costs 220 KiB (5.3× less).

### 6.4 The memory reserve — `max(384 MiB, memLimit / 4)`
Held back for everything that is not a sub-turn:
- the gateway process baseline (measured at ~41 MB RSS for the test binary in §5.1's `N=0` row);
- **the browser subsystem** — the UAT `dmesg` recorded chromium failing 512 MiB allocations on the 459 MB box, which is the actual thing that exhausted it, not sub-turns;
- OS page cache, channel workers, session/JSONL buffers.

The `384 MiB` floor is what makes a genuinely tiny box collapse to the floor rather than proposing a number it cannot honour: a 256 MB machine has `memLimit < reserve` → `memSlots = 0` → floor.

### 6.5 The floor of `4`
Raised from 2 because 2 is not meaningfully concurrent for a delegation workload, and because the measured cost of 4 slots is 4 × 1.12 MiB = 4.5 MiB worst case — affordable on any machine that can run the binary at all. The floor is a *usability* decision, and it is stated as one, not disguised as a calculation.

### 6.6 Memory source must become cgroup-aware (fixes D3)
`totalRAMBytes()` should be replaced with a limit resolver, in order:
1. cgroup v2 — `/sys/fs/cgroup/memory.max` (skip literal `max`)
2. cgroup v1 — `/sys/fs/cgroup/memory/memory.limit_in_bytes` (skip absurd sentinels ≥ 2^62)
3. `/proc/meminfo` `MemTotal`
4. the existing 4 GiB fallback

Without this, a 512 MB container on a big host reads the host's RAM and the memory term is meaningless.

**Deliberately NOT `MemAvailable`:** it fluctuates with page cache and would make boot-time capacity non-deterministic and irreproducible. The *limit* is stable; the reserve fraction absorbs live usage.

### 6.7 The `2500` term — a physical bound, not a policy ceiling

This is the term I added **after** measurement contradicted the initial design (§3.2), and it is the honest answer to "should any upper guard remain".

`sched.maxmcount = 10000` (`proc.go:863`), and exceeding it is `throw("thread exhaustion")` — a **fatal, unrecoverable process kill** (`proc.go:974-977`), strictly worse than any refusal. Section 5.7 measures OS threads tracking concurrent fsyncs ~1:1, and Omnipus fsyncs on the session path. So a cap of N implies a worst case of ~N OS threads.

`2500 ≈ maxmcount / 4` keeps a 4× margin against a fatal outcome. It binds only above 78 cores (`2500 / 32`), so on all realistic hardware it is inert.

**This is qualitatively different from the current ceiling of 16.** 16 is a number someone chose; 2500 is derived from a runtime constant whose violation kills the process, and it is stated with the arithmetic that produces it.

**The better long-term fix, recommended separately:** bound the *file-write* path with its own semaphore, decoupling persistence concurrency from delegation concurrency. Those are genuinely different resources and conflating them is what makes this term necessary at all. Alternatively `debug.SetMaxThreads` can be raised deliberately — but doing so without bounding the write path just moves the cliff.

### 6.8 File descriptors — noted, deliberately not folded in
One FD per in-flight **HTTP/1.1** TLS connection; `ulimit -n` here is 10,240. Two reasons to keep this out of the formula:
- it couples the cap to an unrelated, operator-tunable OS setting; and
- it **largely dissolves under HTTP/2** (§5.5), which every major LLM API speaks — multiplexing serves many concurrent requests per connection.

Recommendation: log a WARN at boot when the resolved cap exceeds `RLIMIT_NOFILE / 2`, and set `Transport.MaxConnsPerHost` (which defaults to **0 = unlimited**, `net/http/transport.go:1600`) to the resolved cap so excess requests queue rather than opening unbounded FDs. `DefaultMaxIdleConnsPerHost = 2` (`transport.go:61`) should also be raised to the steady-state concurrency, or every burst re-handshakes all but 2 connections — pure CPU/latency waste, though not a leak.

---

## 7. `clampParallelExplicit` — recommendation: remove the ceiling, and stop clamping silently

The operator asked whether *any* upper guard should remain. **Recommendation: no policy ceiling, and no silent clamping at all.**

Three reasons, in order of force:

1. **Silent clamping is the ADR-037 anti-pattern this project bans.** An operator PUTs `128`, gets HTTP 200 and a "Saved" toast, and the system runs at 16. That is exactly the failure mode of the Delegation Graph screen (deleted by ADR-037) and the default-agent singleton (release blocker, fixed 2026-07-26). The project's own precedent says such a control must either work or **reject loudly** — never appear to work.

2. **There is no structural hazard in a large value.** Verified: `DispatchSemaphore` is a mutex + int counter (`pkg/agent/dispatch_sema.go:28-37`), so a large cap costs nothing to construct; `turnState.concurrencySem` is `make(chan struct{}, n)`, and a zero-size element type means Go allocates only the `hchan` header regardless of `n`. The only consequence of a large explicit value is real memory used by sub-turns that actually run — which is the operator's own decision to make.

3. **The operator explicitly owns this knob.** An explicit value is a deliberate act, unlike the auto path.

**But the measurement changed one part of this answer.** My initial position was "no upper guard of any kind". §5.7 showed that is wrong: because Omnipus fsyncs on the session path and regular-file I/O is not netpolled, a large cap can drive the process into `throw("thread exhaustion")` — a **fatal kill**, not a degraded state. An operator cannot meaningfully consent to that, because it presents as an unexplained crash rather than as the consequence of their setting.

**Proposed behaviour:**
- Floor stays at **1** (deliberate single-flight remains expressible).
- **No policy ceiling.** The 16 goes away entirely.
- **Never silently clamp.** Any value the system will not honour must be **rejected with a 400 naming the reason**, not quietly reduced.
- **Reject above `10000`** (`maxmcount`) — at that point the value is not merely aggressive, it is unsatisfiable: honouring it risks a fatal runtime throw. Rejection with an explanatory message is strictly better than a crash.
- **Accept but WARN above `2500`**, naming the thread-exhaustion mechanism and the measured 1:1 fsync→thread relationship, so an operator who genuinely wants it can have it with their eyes open.
- **WARN whenever an explicit value exceeds `autoDetectMaxParallel()`**, naming both numbers. The operator keeps control; the risk stays diagnosable.

This satisfies the operator's requirement — *"no top limit"* in the sense that mattered (no arbitrary cap, explicit values honoured, the number grows with hardware) — while not pretending a fatal runtime limit does not exist.

---

## 8. Interaction with the live cap topology

Reading the code rather than the prior document turned up a material correction and a real inconsistency.

### 8.1 Correction — UAT finding G1 is FIXED; the prior doc is stale
`docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` records G1 (root-level delegation ungated) as open. **It is now closed** by ADR-057 W17, landed after that document was written:
- `rootDelegationAdmittingSpawner` (`pkg/agent/admission.go:251+`) wraps every `tools.SubTurnSpawner`, gating dispatches with `parentTS.depth == 0`.
- Wired at `pkg/agent/loop.go:741` via `ResolveRootDelegationCap`.

Anyone acting on that document's G1 should read this section first.

### 8.2 There are THREE caps, and only one of them uses this formula

| # | Gate | Cap source | Uses `autoDetectMaxParallel`? |
|---|---|---|---|
| 1 | **Root** delegation — `RootDelegationAdmission`, process-global | `cfg.Agents.Defaults.SubTurn.MaxConcurrent` (seeded 16), read **directly and unclamped** — `admission.go:119-139` deliberately bypasses `EffectiveMaxParallelAgents` per FR-095 | **No** |
| 2 | **Nested** delegation — `turnState.concurrencySem` (`subturn.go:1231`) | `getSubTurnConfig` (`subturn.go:81-86`): `SubTurn.MaxConcurrent` if > 0, else `EffectiveMaxParallelAgents()` | **Only via a branch that is dead on a fresh install** (seed is 16 > 0) |
| 3 | **Plan / task** dispatch — `TaskExecutor.dispatchSema` (`task_executor.go:168-172`) | `EffectiveMaxParallelAgents()` | **Yes — the only live consumer** |

### 8.3 The operator's live symptom traces to gate 3
`ErrDispatchCapReached` (`task_executor.go:33`) formatted at `task_executor.go:451-457`:

```go
return fmt.Errorf("%w (%d/%d in flight), retry later",
    ErrDispatchCapReached, te.dispatchSema.InFlight(), te.dispatchSema.Cap())
```

renders as `task_executor: global dispatch cap reached (2/2 in flight), retry later` — an **exact** match for the ×56 UAT log line. So the ×56 stall was the **plan path**, capped at 2 by `autoDetectMaxParallel()` on a 1-vCPU box. This formula change fixes precisely that symptom.

### 8.4 How the new formula interacts with the inconsistency
Raising the auto value makes the **divergence between the three gates more visible, not less**:

- Gate 3 (plan) would go 2 → 32 on the UAT box.
- Gate 1 (root delegation) would stay **16**, because it reads a different key (`agents.defaults.subturn.max_concurrent`) that this formula does not touch.

So after this change a plan can dispatch 32 concurrent tasks while direct chat fan-out still refuses past 16, from one machine, with no single setting explaining it. **That is a pre-existing incoherence this change surfaces.** It is flagged, not fixed here, per instruction. Resolving it means deciding whether `agents.defaults.subturn.max_concurrent` should default to the same resource-derived number — a separate ADR.

---

## 9. The exact patch (proposed — NOT applied)

### 9.1 `pkg/config/config.go`

```diff
@@ -422,12 +422,16 @@
 // PerformanceConfig controls the max-parallel fan-out gate for task/subagent dispatch.
 type PerformanceConfig struct {
 	// MaxParallelAgents is the maximum number of concurrent task/subagent dispatches.
 	// 0 means "use the auto-detected default" (derived from CPU and memory).
-	// The runtime clamps this to [2, min(NumCPU-2, RAM/1.5 GB)] ≤ 16.
+	// An explicit value is honoured as written: there is no upper policy clamp
+	// (see max-parallel-formula-research-2026-08-04.md §7). Values above
+	// absurdParallelBound are rejected at the API boundary, never silently reduced.
 	// Overridden by OMNIPUS_MAX_PARALLEL_AGENTS env var when set.
 	MaxParallelAgents int `json:"max_parallel_agents,omitempty" env:"OMNIPUS_MAX_PARALLEL_AGENTS"`
 }

+const (
+	// subTurnMemoryReservationBytes is the memory reserved per concurrent
+	// sub-turn slot. Grounded in measurement, not estimate: a sub-turn with a
+	// saturated context (288,000 runes — CalculateDefaultMaxContextRunes at the
+	// default 128k context window) costs a measured 1.12 MiB RSS; a minimal one
+	// costs 220 KiB. 2 MiB is a 1.79x margin over the measured worst case, and
+	// the formula reserves that worst case for EVERY slot simultaneously.
+	// See docs/internal/architecture/max-parallel-formula-research-2026-08-04.md §5.
+	subTurnMemoryReservationBytes = 2 << 20 // 2 MiB
+
+	// subTurnsPerCore is the I/O-bound concurrency multiplier per schedulable
+	// CPU. Derived: measured CPU service time is 4.47 ms per full sub-turn
+	// lifecycle; with a 4x pessimism factor for real TLS/JSON work (=18 ms) and
+	// an aggressively short 1 s LLM round-trip, W/S = 56 per core. 32 rounds that
+	// down, keeping ~40% CPU headroom for the gateway's own serving and GC.
+	subTurnsPerCore = 32
+
+	// baselineMemoryReserveBytes is held back for everything that is not a
+	// sub-turn: the gateway baseline (~41 MB), the browser subsystem (chromium
+	// was observed failing 512 MiB allocations on the 459 MB UAT box), page
+	// cache and channel workers.
+	baselineMemoryReserveBytes = 384 << 20 // 384 MiB
+
+	// minAutoParallel is a usability floor, not a calculation: 4 slots cost a
+	// measured 4.5 MiB worst case, affordable on any machine that runs the binary.
+	minAutoParallel = 4
+
+	// threadSafeParallelBound is a PHYSICAL bound, not a policy ceiling.
+	// Omnipus fsyncs on the session-write path (pkg/memory/jsonl.go,
+	// pkg/fileutil/file.go), and regular-file I/O is NOT netpolled on Linux
+	// (os/file_unix.go:212-217) — so each concurrent write blocks an OS thread.
+	// Measured: 1000 concurrent fsyncing goroutines => 999 OS threads.
+	// runtime's sched.maxmcount is 10000 and exceeding it is throw("thread
+	// exhaustion") — a FATAL process kill (runtime/proc.go:974-977). This keeps
+	// a 4x margin. It only binds above 78 cores.
+	// The proper long-term fix is a separate semaphore around the write path;
+	// see the research doc section 6.7.
+	threadSafeParallelBound = 2500
+
+	// unsatisfiableParallelBound is the point past which an EXPLICIT operator
+	// value cannot be honoured at all (it risks the fatal throw above). Values
+	// beyond it are REJECTED with a 400 naming the reason — never silently
+	// clamped, which is the ADR-037 anti-pattern this project bans.
+	unsatisfiableParallelBound = 10000
+)
+
@@ -433,7 +437,7 @@
 // EffectiveMaxParallelAgents returns the environment-override-aware value for
 // MaxParallelAgents. It applies:
 //  1. An env-var override (OMNIPUS_MAX_PARALLEL_AGENTS) if set and valid.
 //  2. The configured value (p.MaxParallelAgents), if non-zero.
-//  3. An auto-detect heuristic: min(NumCPU-2, RAM_GB/1.5), floor 2, ceiling 16.
+//  3. A resource-derived auto value (autoDetectMaxParallel).
 //
-// An explicit MaxParallelAgents=1 is honored — only the auto-detect path
-// enforces a floor of 2 (to prevent accidental single-flight on capable hardware).
+// An explicit value is honoured as written, with no upper clamp. When it exceeds
+// what auto-detection would have chosen, a WARN naming both numbers is logged.
 func (p PerformanceConfig) EffectiveMaxParallelAgents() int {
 	if s := os.Getenv("OMNIPUS_MAX_PARALLEL_AGENTS"); s != "" {
 		if v, err := strconv.Atoi(s); err == nil && v >= 1 {
 			return clampParallelExplicit(v)
 		}
 	}
 	if p.MaxParallelAgents > 0 {
 		return clampParallelExplicit(p.MaxParallelAgents)
 	}
 	return autoDetectMaxParallel()
 }

-// clampParallelExplicit clamps an explicitly configured value to [1, 16].
-func clampParallelExplicit(v int) int {
-	const minPar, maxPar = 1, 16
-	if v < minPar {
-		return minPar
-	}
-	if v > maxPar {
-		return maxPar
-	}
-	return v
-}
-
-// clampParallel clamps the auto-detected value to [2, 16].
-func clampParallel(v int) int {
-	const minPar, maxPar = 2, 16
-	if v < minPar {
-		return minPar
-	}
-	if v > maxPar {
-		return maxPar
-	}
-	return v
-}
-
-// autoDetectMaxParallel returns min(NumCPU-2, RAM_GB/1.5) clamped to [2, 16].
-// RAM_GB is derived from the virtual memory total reported by the OS.
-func autoDetectMaxParallel() int {
-	cpuBased := runtime.NumCPU() - 2
-	ramBased := int(float64(totalRAMBytes()) / (1.5 * 1024 * 1024 * 1024))
-	val := cpuBased
-	if ramBased < val {
-		val = ramBased
-	}
-	return clampParallel(val)
-}
+// clampParallelExplicit honours an explicitly configured value. It enforces only
+// a floor of 1; there is NO upper policy clamp. Silently reducing an operator's
+// explicit value is the ADR-037 anti-pattern (a control that looks like it worked
+// and changed nothing) — values above absurdParallelBound are rejected at the API
+// boundary instead. See the research doc §7.
+func clampParallelExplicit(v int) int {
+	if v < 1 {
+		return 1
+	}
+	// NOTE: no upper clamp. Values above unsatisfiableParallelBound are
+	// rejected at the API boundary (pkg/gateway/rest_performance.go) with a 400
+	// naming the thread-exhaustion reason; values above
+	// threadSafeParallelBound are accepted with a WARN. Silently reducing an
+	// operator's explicit value here would be the ADR-037 anti-pattern.
+	return v
+}
+
+// autoDetectMaxParallel derives the concurrency cap from CPU and memory only.
+// Provider rate limits are deliberately NOT considered.
+//
+//	cpuSlots = GOMAXPROCS * subTurnsPerCore
+//	reserve  = max(baselineMemoryReserveBytes, memLimit/4)
+//	memSlots = (memLimit - reserve) / subTurnMemoryReservationBytes
+//	result   = max(minAutoParallel,
+//	               min(cpuSlots, memSlots, threadSafeParallelBound))
+//
+// There is no POLICY ceiling: the value rises with real hardware, and the only
+// upper term is a physical one (see threadSafeParallelBound).
+//
+// GOMAXPROCS (not NumCPU) is used because only GOMAXPROCS respects a cgroup CPU
+// quota (runtime/debug.go:51-67); NumCPU is cgroup-blind and frozen at startup
+// (runtime/debug.go:149-156), which made the old formula read 62 on a
+// --cpus=1 container and -1 on a 1-vCPU VM.
+//
+// CONSTRAINT: this depends on GODEBUG containermaxprocs/updatemaxprocs
+// defaulting to 1, which the toolchain amends to the go.mod LANGUAGE VERSION
+// (internal/godebugs/table.go: Changed:25, Old:"0"). go.mod currently declares
+// go 1.26.5. Lowering the go directive below 1.25 silently turns container-aware
+// GOMAXPROCS OFF and degrades this term to NumCPU behaviour.
+func autoDetectMaxParallel() int {
+	cpuSlots := runtime.GOMAXPROCS(0) * subTurnsPerCore
+
+	memLimit := memoryLimitBytes()
+	reserve := memLimit / 4
+	if reserve < baselineMemoryReserveBytes {
+		reserve = baselineMemoryReserveBytes
+	}
+	memSlots := 0
+	if memLimit > reserve {
+		memSlots = int((memLimit - reserve) / subTurnMemoryReservationBytes)
+	}
+
+	val := cpuSlots
+	if memSlots < val {
+		val = memSlots
+	}
+	if val > threadSafeParallelBound {
+		val = threadSafeParallelBound
+	}
+	if val < minAutoParallel {
+		val = minAutoParallel
+	}
+	return val
+}
```

### 9.2 `pkg/config/meminfo_linux.go` — become cgroup-aware

```diff
+// memoryLimitBytes returns the memory limit that actually applies to this
+// process: the cgroup limit when one is set (a container), otherwise the
+// machine's physical memory. /proc/meminfo MemTotal alone reports the HOST's
+// memory inside a container, which is the defect this replaces.
+//
+// Deliberately the LIMIT, not MemAvailable: MemAvailable fluctuates with page
+// cache and would make boot-time capacity non-deterministic. The reserve
+// fraction in autoDetectMaxParallel absorbs live usage instead.
+func memoryLimitBytes() uint64 {
+	// cgroup v2
+	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
+		s := strings.TrimSpace(string(b))
+		if s != "max" {
+			if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
+				return minU64(v, readMemTotalBytes())
+			}
+		}
+	}
+	// cgroup v1
+	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
+		if v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
+			// v1 uses a huge sentinel for "unlimited".
+			if v > 0 && v < (1<<62) {
+				return minU64(v, readMemTotalBytes())
+			}
+		}
+	}
+	return readMemTotalBytes()
+}
```

(plus a `memoryLimitBytes()` on the `!linux` side returning `readMemTotalBytes()`, and retirement of the now-unused `totalRAMBytes()`.)

### 9.3 Contracts — required, and required FIRST (Constraint #8)

`contracts/components/schemas/PerformanceSettings.yaml` and `PerformanceSettingsUpdate.yaml` both pin `minimum: 2, maximum: 16` on `max_parallel_agents` **and** `effective_max_parallel_agents`. These are the hard blocker: today the backend cannot return an effective value above 16 without producing schema-invalid JSON, which `pkg/api/generated/contract_test.go` fails on.

```diff
   max_parallel_agents:
     type: integer
-    minimum: 2
-    maximum: 16
+    minimum: 1
     description: >
-      Maximum number of tasks/subagents that may run concurrently on the
-      dispatch path. The runtime clamps the configured value to
-      [2, min(NumCPU-2, RAM_GB/1.5)] with a hard ceiling of 16.
+      Maximum number of tasks/subagents that may run concurrently on the
+      dispatch path. An explicit value is honoured as written — there is no
+      upper clamp. 0 (absent) selects the resource-derived auto value.
       Overridden by OMNIPUS_MAX_PARALLEL_AGENTS env var.
   effective_max_parallel_agents:
     type: integer
-    minimum: 2
-    maximum: 16
+    minimum: 1
```

Then, in order: `scripts/gen-contracts.sh` → commit generated `pkg/api/generated/` + `src/lib/api/generated/` in the **same** commit → `make verify-contracts`.

`pkg/gateway/rest_performance.go:40` (`// max_parallel_agents in the response must satisfy the contract minimum (2)`) and its coercion must go with it.

### 9.4 UI — `src/components/settings/PerformanceSection.tsx`

| Line(s) | Current | Change to |
|---|---|---|
| 113 | `parsed < 2 \|\| parsed > 16` → null | `parsed < 1` → null (drop the upper bound) |
| 122, 147, 171 | toast "must be between 2 and 16" | "must be at least 1 (or leave blank for auto-detect)" |
| 201-206 | `cpuUpperBound = min(max(cores-2,2),16)` | Remove. The client cannot compute the cap — memory is not exposed to it. Show the server's `effective_max_parallel_agents` instead. |
| 242 | "clamps to `[2, min(NumCPU-2, RAM_GB/1.5)]` with a ceiling of 16" | "Auto-detected from GOMAXPROCS and the memory limit. An explicit value is honoured as written." |
| 266-267 | `min={2} max={16}` | `min={1}`, drop `max` |

### 9.5 Tests that will fail and must be updated (not deleted)
- `pkg/config/parallel_clamp_test.go:61-68` — `TestEffectiveMaxParallelAgents_ExplicitCapped` asserts `99 → 16`. This encodes the behaviour being deliberately removed; it should be **inverted** to assert `99 → 99`.
- `pkg/config/parallel_clamp_test.go:71-78` — `TestEffectiveMaxParallelAgents_Auto` asserts the result is within `[2,16]`.
- `pkg/config/parallel_clamp_test.go:92` — env-override expecting 16.
- `pkg/config/config_adr057_test.go:193` — reads `PerformanceConfig{}.EffectiveMaxParallelAgents()`.

---

## 10. Alternatives considered and rejected

| Alternative | Why rejected |
|---|---|
| **Keep a ceiling, just raise it (16 → 64/128)** | Still an arbitrary constant, and still *silently* clamps — the operator's explicit requirement was no top limit, and silent clamping is the banned ADR-037 pattern regardless of where the number sits. |
| **Size purely from memory, drop CPU** | The operator named CPU as a factor, and it is a real one — not because parked goroutines hold cores (they do not, §3), but because simultaneous *completions* do 4.47 ms of CPU each. Dropping it lets a 1-vCPU box propose 300+. |
| **Size purely from CPU, drop memory** | Then a 256 MB box proposes 32 and dies. Memory is the term that makes small boxes degrade correctly. |
| **Use `MemAvailable` instead of the limit** | Fluctuates with page cache; boot-time capacity becomes non-deterministic and irreproducible. On the UAT box at the moment of measurement `MemAvailable` was 28 MB — the formula would have collapsed to the floor for a transient reason. |
| **Keep `runtime.NumCPU()`** | Cgroup-blind and startup-frozen (`runtime/debug.go:149-156`); reads 62 on a `--cpus=1` container and −1 on a 1-vCPU VM. This is defect D2. |
| **A fixed per-slot constant with no context term** | §5.3 shows cost is dominated by context size (3.17 B RSS per content byte). Any single constant is either wrong for saturated turns or absurdly wasteful for minimal ones. The 2 MiB reservation resolves this by budgeting the measured *saturated* case for every slot. |
| **Dynamic/adaptive cap (measure RSS at runtime, adjust)** | Materially more complex, needs hysteresis to avoid oscillation, and makes the cap unpredictable and hard to reason about in support. The static formula is within 2× of measured reality; adaptivity is not worth it. Revisit only if evidence shows the static number failing. |
| **Fold in file-descriptor limits** | Couples the cap to an unrelated, operator-tunable OS setting, and largely dissolves under HTTP/2 (§5.5). Proposed as a boot WARN plus a `MaxConnsPerHost` setting instead (§6.8). |
| **No upper bound at all (my own initial position)** | **Rejected by measurement.** §5.7 showed Omnipus's fsyncing session writes consume an OS thread each, and `maxmcount = 10000` is a fatal `throw`. An unbounded cap can kill the process rather than degrade it. Replaced with a *physical* bound (§6.7), not a policy one. |
| **Raise `debug.SetMaxThreads` instead of bounding the cap** | Moves the cliff without removing it, and trades a diagnosable refusal for an OOM/scheduler failure further out. Worth doing only *alongside* a bounded write path, not instead of it. |
| **Fold in provider rate limits** | Explicitly out of scope per the operator. |

---

## 11. What this measurement could NOT establish

Stated plainly, because the numbers above are only as good as their limits.

1. **The provider is a mock.** Probe A never does a real TLS handshake, never streams SSE, never JSON-decodes a real multi-KB response. Probe B (§5.5) covers the transport separately, but the *combination* — a real sub-turn on a real network — was never measured end to end. The 4× pessimism factor on CPU (§6.2) is a **judgement**, not a measurement.

2. **Probe B is loopback.** Client and server share a process, so the 92.3 KiB/connection includes the server side. Used as a conservative bound; the true client-only cost is roughly half but was not isolated.

3. **Measured on one machine, and a busy one.** 8 cores / 16 GB, shared with other agent processes — the source of the RSS standard deviations in §5.1 (up to 7.5 MB at N=1024). **Nothing was measured on the 1-vCPU / 459 MB UAT box**, which is the box the operator actually cares about. Per-sub-turn *memory* should transfer (it is allocation, not scheduling); per-sub-turn *CPU* may not, and a shared/throttled vCPU could raise `S` materially. **The `× 32` multiplier is the weakest number in this document and should be re-measured on the target hardware before it is trusted at the top of its range.**

4. **No sustained-load or churn test.** All measurements are of a *steady state* of N simultaneously-parked sub-turns. Sub-turns arriving and completing continuously would add GC pressure and allocator churn not captured here. No test ran longer than ~20 minutes; nothing addresses memory growth over hours.

5. **Tool execution is not in the measurement.** Sub-turns that run `bash`, drive the browser, or hold large tool results cost more — potentially far more (chromium is a separate process and dwarfs everything here). The 384 MiB reserve is intended to absorb this but was **not** validated against a browser-active workload.

6. **The 3.17 B/byte content coefficient was measured with single-byte ASCII padding.** Multi-byte content is argued about in §6.3 and covered by the 2 MiB margin, but was not directly measured.

7. **No sub-turn ever failed in these runs**, so nothing here says how error/timeout/cancel paths affect memory — only the happy path was exercised.

8. **The formula's output was never run in production.** Every number in §1's table is the formula evaluated arithmetically, not a machine observed operating at that cap.

9. **The fsync→thread measurement (§5.7) used a synthetic write loop, not Omnipus's actual session-write path.** It establishes the *mechanism* (regular-file I/O blocks a thread) and that thread count tracks concurrency ~1:1. It does **not** establish how long a real Omnipus session write holds its thread, nor what fraction of a sub-turn's lifetime is spent inside one. The `2500` bound is therefore sized from the mechanism plus a 4× margin, **not** from a measured duty cycle. If real writes are short and rarely coincide, 2500 is very conservative; if they are slow (network filesystem, contended disk) it may not be conservative enough. **This is the second-weakest number in the document, after `× 32`.**

10. **Nothing here measures the kernel-side socket buffers**, which sit outside Go's RSS accounting entirely. `tcp_rmem`/`tcp_wmem` autotune per connection (to several MB on a high-bandwidth-delay path), and a loopback test exercises none of that. Against a real remote LLM endpoint this is genuine additional per-connection memory that this document does not quantify.

---

## 12. Recommended next steps

1. **Decide §7** (`clampParallelExplicit`: reject-don't-clamp, and the two thresholds). It is a policy call, not a technical one, and it gates the contract change.
2. **Land contracts first** (Constraint #8, five steps in order); the schema `maximum: 16` on `effective_max_parallel_agents` blocks everything else — the backend literally cannot report a larger effective value without failing `pkg/api/generated/contract_test.go`.
3. **Re-measure `S` on a 1-vCPU box** before trusting `× 32` at the top of its range (limitation 3).
4. **Bound the session-write path with its own semaphore** (§6.7). This is the right fix for the thread constraint and would let the `2500` term be dropped or raised on evidence rather than margin.
5. **Open a separate ADR for the gate-1/gate-3 divergence** (§8.4) — this change makes it visible.
6. **Correct or supersede G1 in the 2026-07-31 UAT doc** (§8.1), which currently records a fixed defect as open.
7. Consider setting `Transport.MaxConnsPerHost` and raising `MaxIdleConnsPerHost` to the resolved cap (§6.8) — independent of this formula, but the same workload.
