# ADR-054 Wave 4 — Performance gate: baseline measurements

**Status:** Measured 2026-07-25 against `feature/plan-swimlane-board` while
Waves 1-3 were concurrently in flight (`pkg/entity`, `pkg/agentstore`,
`pkg/agent/registry.go`, `pkg/coreagent`, `pkg/gateway`, `pkg/sysagent`,
`pkg/routing` were all touched by other agents during this session — this
document measures `pkg/entity` and `pkg/audit` only, and was re-verified
against the latest state of both before being written up).

**Scope:** this closes the `[UNKNOWN]` items ADR-054 §12 explicitly deferred
to "the perf gate at the end of this work": real contention numbers for the
config lock and audit mutex, and the O(N) `List()` question.

**Environment:** 8 vCPU (AMD EPYC), 15GB RAM, Go 1.26.5, linux/amd64,
`CGO_ENABLED=0` (benchmarks) / `CGO_ENABLED=1` (`-race` only, required by the
toolchain — the literal `CGO_ENABLED=0 ... -race` invocation given in the task
brief errors with `-race requires cgo`; this is a toolchain constraint, not a
finding). Shared, resource-constrained pod — see "Honesty about noise" below.

**Method:** every benchmark models one "op" as **one whole batch of N
concurrent writers**, not a single write. This makes Go's own `ns/op` figure
equal to the wall-clock cost of the whole N-writer batch — exactly what the
task asked for ("the batch number is what shows the lock convoy, per-op
averages can hide it") — with no separate manual timer needed. A per-single-op
estimate is recovered by dividing `ns/op` by N, shown in every table below.
Every benchmark ran with `-benchmem -benchtime 200x -count=3` (three
independent repetitions per data point); `go test -race -run 'Concurrent'`
correctness companions ran separately and are reported per section.

Raw commands:
```
CGO_ENABLED=0 go test -tags goolm,stdjson -run '^$' -bench . -benchmem -benchtime 200x -count=3 ./pkg/entity/...
CGO_ENABLED=0 go test -tags goolm,stdjson -run '^$' -bench 'BenchmarkAuditAppend_Concurrent' -benchmem -benchtime 200x -count=3 ./pkg/audit/...
CGO_ENABLED=1 go test -tags goolm,stdjson -race -run 'Concurrent' -count=1 ./pkg/entity/... ./pkg/audit/...
```

---

## B1 — parallel entity writes vs whole-file config rewrite (THE headline claim)

**New path:** `entity.Store[benchAgent].Create` — N goroutines each creating a
**distinct** agent record (`pkg/entity/perf_bench_test.go`,
`BenchmarkEntityCreate_Concurrent`).

**Old path baseline:** the REAL `config.SaveConfig` (`config.go:3567`) called
against a shared `*config.Config` seeded with an 8-agent roster (matching the
`~/.omnipus` 8-agent install ADR-054 §1 measured), with all N goroutines
serialized behind **one `sync.Mutex`** — standing in for the real gateway's
`al.mu`, which `Deps.WithConfig` (`pkg/sysagent/tools/deps.go:239`) holds
across mutate-and-persist today. Each batch starts from a fresh 8-agent copy
(not accumulated across batches) so the comparison is "N concurrent creates
landing on a representative ~8-agent install," not a confounded growth curve.

| N | Entity batch (ms) | Config batch (ms) | Entity per-op (µs) | Config per-op (µs) | Speedup (config/entity) |
|---|---|---|---|---|---|
| 1 | 1.129 (±8.5%) | 1.152 (±3.3%) | 1,129 | 1,152 | 1.02x |
| 2 | 1.243 (±5.6%) | 2.455 (±11.5%) | 622 | 1,228 | 1.97x |
| 4 | 1.828 (±20.1%) | 4.638 (±4.3%) | 457 | 1,159 | 2.54x |
| 8 | 2.593 (±0.9%) | 9.689 (±5.2%) | 324 | 1,211 | 3.74x |
| 16 | 5.406 (±12.5%) | 22.516 (±28.4%) | 338 | 1,407 | 4.17x |
| 32 | 10.922 (±4.1%) | 43.823 (±11.6%) | 341 | 1,369 | **4.01x** |

(± is `(max-min)/mean` across the 3 repetitions — see "Honesty about noise.")

Batch-wall-clock scaling relative to N=1 (1.0 = perfect parallelism, N =
perfect serialization):

| N | Entity scaling | Config scaling |
|---|---|---|
| 2 | 1.10x | 2.13x |
| 4 | 1.62x | 4.02x |
| 8 | 2.30x | 8.41x |
| 16 | 4.79x | 19.54x |
| 32 | 9.67x | 38.03x |

`B/op` / `allocs/op` (from `-benchmem`): entity per-op cost is **constant**
(~9.2KB / ~105 allocs regardless of N — batch totals scale linearly, e.g.
294KB/3332 allocs at N=32 ≈ 32× the N=1 figures). Config per-op cost **grows**
with N within a batch (the file gets progressively larger as each of the N
sequential appends lands — 37KB/34 allocs at N=1 vs 1.63MB/978 allocs at
N=32, i.e. more than linear in N, because a batch of N appends means N
successive whole-file rewrites of a growing roster).

**What this means for the ADR:** **yes, per-entity writing measurably beats
whole-file rewrite under concurrency, and the gap widens with N.** At N=1
they're at parity (~1.03x — no concurrency to exploit either way, and a
single entity write's flock+verify-readback costs about the same as one
small-file whole rewrite). By N=8 the entity path is 3.7x faster per batch;
by N=16-32 it plateaus around 4.0-4.2x. The entity store's **per-op cost
actually falls** as N grows (1,129µs → ~324-341µs, an ~3.3x drop) — real
evidence of parallelism, not just "less bad serialization" — while the config
path's per-op cost stays flat-to-rising (~1.15-1.4ms regardless of N),
consistent with full serialization on one lock plus a linearly-growing file.
The entity store's speedup appears to plateau near 4x rather than growing
with N indefinitely — plausibly an 8-core ceiling (this pod has 8 vCPUs) or
disk-parallelism limit, not evidence the design stops helping past N=32; that
plateau point is itself useful data but wasn't specifically asked for and
should be treated as directional, not definitive, given the pod's shared,
resource-constrained nature.

---

## B2 — same-entity contention

N goroutines call `entity.Store[benchAgent].Update` on the **same** record,
each incrementing a `Counter` field
(`BenchmarkEntityUpdate_SameID_Concurrent`).

| N | Batch (ms) | Per-op (µs) | Scaling vs N=1 |
|---|---|---|---|
| 1 | 1.108 (±16.0%) | 1,108 | 1.0x |
| 2 | 2.023 (±3.1%) | 1,012 | 1.83x |
| 4 | 4.007 (±3.6%) | 1,002 | 3.62x |
| 8 | 8.196 (±3.6%) | 1,025 | 7.40x |
| 16 | 16.553 (±5.9%) | 1,035 | 14.94x |
| 32 | 37.007 (±13.6%) | 1,156 | 33.41x |

**What this means:** same-entity contention serializes **almost exactly
linearly** — batch wall-clock roughly doubles with every doubling of N, and
per-op cost stays essentially flat (~1.0-1.16ms) across the whole range.
This is the textbook "no parallelism available, no pathology either" result:
the striped mutex + sidecar flock do exactly what D3 says they should (fully
serialize the worst case) **without** a lock-convoy penalty on top (no
super-linear blowup — 33.4x scaling at N=32 against an ideal-serial 32x is
close, and the small overshoot is within the run-to-run noise band, not a
systematic effect). Quantitatively: contending 32-way on one agent record
costs ~37ms total wall-clock for the batch — a real but bounded worst case,
not a design that falls over under contention.

---

## B3 — `List()` at scale

`entity.Store[benchAgent].List()` called against a pre-seeded store of N
records (`BenchmarkEntityList_Scale`).

| N | List() cost (ms) | Per-entity cost (µs) | Implied throughput |
|---|---|---|---|
| 10 | 0.348 (±15.0%) | 34.76 | ~28,770 entities/sec |
| 100 | 3.321 (±10.3%) | 33.21 | ~30,110 entities/sec |
| 1,000 | 35.054 (±6.8%) | 35.05 | ~28,530 entities/sec |
| 5,000 | 174.442 (±2.2%) | 34.89 | ~28,660 entities/sec |

**What this means:** `List()` is a clean, textbook **O(N)** — per-entity cost
is ~34.8-35.1µs and essentially constant across three orders of magnitude of
N (no hidden super-linear behavior from the sort, GC, or directory-entry
handling shows up in this range). Using ~35µs/entity as the rate:
- **~460 agents** to hit a 16ms (single-frame-ish) budget
- **~1,430 agents** to hit 50ms
- **~2,860 agents** to hit 100ms
- 5,000 agents costs ~174ms for a single full scan.

Given ADR-054 §1's own measured installs have **8 agents**, and §12 flags
"expected steady-state agent count" as unknown, the honest answer is
two-layered:
1. **At any plausible near-term install size (tens to low hundreds of
   agents), raw `List()` cost is a non-issue** — even 100 agents costs
   ~3.3ms, imperceptible for an admin-triggered listing.
2. **The `AgentRegistry` cache's real justification is structural, not raw
   scan speed** — ADR-054 D3 already establishes that `List()`/disk reads
   must never sit on the per-inbound-message routing hot path regardless of
   N, because a `LOCK_EX` sidecar flock has no shared-read mode and would
   serialize every reader against every writer. This benchmark shows the cache
   is not urgently load-bearing **for raw throughput** below roughly
   1,000-3,000 agents, but it remains load-bearing **for the flock-avoidance
   reason D3 already gives**, independent of N. Only if real installs grow
   into the thousands does raw scan cost become an additional argument for
   the cache on its own — outside this project's expected personal-agent
   usage pattern, but not impossible if `List()` is ever called per-request
   on an admin surface with a large roster.

---

## B4 — audit append under fan-out

ADR-054 §7 (D5) explicitly deferred the audit-chain-scoping decision pending
this measurement. `pkg/audit.Logger` uses one `sync.Mutex` + `O_APPEND` +
a sequential HMAC chain (`writeLine`, `audit.go`).

Two variants were measured: the bulk/allow path (buffered write + `Flush()`,
no `fsync`) and the deny/error path (`criticalEventNeedsSync` triggers
`f.Sync()` before `Log()` returns) — these have materially different cost
profiles and the task's "is the mutex the ceiling" question needs both.

### Allow path (bulk logging — no fsync)

| N | Batch (µs) | Per-op (µs) | Scaling vs N=1 |
|---|---|---|---|
| 1 | 45.3 (±3.3%) | 45.3 | 1.0x |
| 8 | 377.0 (±5.9%) | 47.1 | 8.33x |
| 32 | 1,422.1 (±0.9%) | 44.4 | 31.40x |

Sustained throughput even at 32-way fan-out: **~22,000 appends/sec**
(1 / 44.4µs).

### Deny/error path (fsync'd — `DecisionDeny`, `criticalEventNeedsSync`)

| N | Batch (ms) | Per-op (µs) | Scaling vs N=1 |
|---|---|---|---|
| 1 | 0.387 (±6.4%) | 387.2 | 1.0x |
| 8 | 2.881 (±0.7%) | 360.2 | 7.44x |
| 32 | 11.507 (±3.3%) | 359.6 | 29.72x |

fsync makes each append **~8x more expensive** (359-387µs vs 44-47µs) — as
expected, since it adds a real disk-sync syscall — but sustained throughput
even at 32-way fan-out is still **~2,780 appends/sec** (1 / 359.6µs).

**What this means for the ADR — direct answer to "is the audit mutex the next
ceiling": no, not at any fan-out level measured here (up to 32-way), on
either path.** The evidence for this:
- **Per-op cost is essentially flat regardless of N** on both paths (allow:
  44.4-47.1µs; fsync: 359.6-387.2µs) — if the mutex itself were adding
  contention overhead (queueing delay beyond the work each holder actually
  does), per-op cost would rise with N. It doesn't, on either path.
- **Batch wall-clock scales close to linearly with N** (allow: 31.4x at
  N=32 vs an ideal-serial 32x; fsync: 29.7x at N=32) — this is exactly what a
  single mutex around a sequential chain SHOULD look like: full serialization,
  with no additional lock-convoy penalty stacked on top.
- Even the slower, disk-latency-dependent fsync path sustains ~2,780
  appends/sec at 32-way fan-out — several orders of magnitude above any
  plausible sustained deny/error rate for a personal-agent system.

**This evidence supports keeping the global chain as-is.** Splitting it
(ADR-054 §7's follow-up #2: per-entry sequence numbers + periodic signed tip
anchors) is architecturally interesting for the *tamper-evidence* gap
(whole-log deletion currently verifies clean — a real, separate audit
finding filed as its own v0.2 issue per §7) but this benchmark finds **no
throughput evidence** that the mutex is a bottleneck at realistic fan-out.
Revisit only if real usage shows sustained fan-out in the thousands/sec
range, which nothing in this codebase's usage pattern suggests today.

---

## Honesty about noise

This pod is shared and resource-constrained (8 vCPU, variable load); every
number above is the mean of **3 independent repetitions** (`-count=3`), with
the `(max-min)/mean` spread shown alongside each headline figure. Most
figures cluster within 3-15% run-to-run; a few (config-save N=16 at 28.4%,
entity-create N=4 at 20.1%) show wider spread consistent with scheduling
noise on a shared host rather than a systematic effect — in both cases the
outlier is a single high sample among three, not a directional trend, and the
qualitative conclusions above (parallelism plateau ~4x, near-linear same-ID
and audit-mutex scaling, clean O(N) listing) hold whether the outlier is
included or excluded. No single-run result is presented as fact anywhere in
this document. The `List()` N=5000 figure has the tightest spread (2.2%) of
any measurement here, likely because seeded files are still warm in the OS
page cache immediately after creation — a cold-cache real-world `List()`
could be slower; this benchmark does not (and, within the scope given, was
not asked to) control for page-cache state.

**Scope limitation, stated plainly:** B4 only measures Decision=Allow and
Decision=Deny: it does not exercise audit rotation, retention cleanup, or
recovery-from-corruption paths concurrently with appends, and B1's "old
path" mutex is a stand-in for `al.mu`, not the real gateway wiring
end-to-end (no HTTP layer, no JSON request parsing) — both benchmarks isolate
the write-path primitive the ADR is actually deciding between, which is what
was asked for, but neither is a full-system load test.

## Summary — direct answers to the three questions

**(a) Does per-entity writing actually beat whole-file rewrite under
concurrency?** Yes. At N=1 they're at parity (~1.02x); the entity path pulls
ahead steadily with N, reaching ~4.0-4.2x faster per-batch by N=16-32, driven
by falling entity per-op cost (real parallelism) against flat-to-rising
config per-op cost (full serialization plus growing-file cost).

**(b) At what N does `List()` need a cache?** Raw scan cost alone doesn't
demand one below roughly 1,000-3,000 agents (O(N) at ~35µs/entity — 100
agents costs ~3.3ms, negligible). The cache is already justified independent
of N by ADR-054 D3's flock-avoidance argument for the per-message routing
path; this benchmark adds that raw throughput becomes an *additional* reason
only at agent counts far beyond this project's expected steady state.

**(c) Is the audit mutex the next ceiling?** No evidence of it at any
fan-out level tested (1/8/32-way), on either the buffered-allow path
(~22,000 appends/sec sustained) or the fsync'd deny/error path (~2,780
appends/sec sustained). Per-op cost stays flat with N on both paths — the
signature of clean serialization, not lock-convoy pathology. The evidence
supports keeping the global chain as-is; the real, separate finding here is
the tamper-evidence gap (whole-log-deletion detection), not throughput.
