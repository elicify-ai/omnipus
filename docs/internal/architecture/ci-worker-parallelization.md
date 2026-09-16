# CI worker parallelization — one setup, three gate groups

**Date:** 2026-09-16
**Branch:** `perf/ci-worker-parallel`
**Status:** DELIVERED in `deploy/ci-worker/runci.sh` (gate `all-no-e2e`). Deployment to
`/cache/runci.sh` on the workers is a separate step — editing the repo file does not update
the executing copy (see `deploy/ci-worker/CLAUDE.md`, "Redeploying runci.sh").
**Scope:** the local PR-runner's per-run latency. Queueing between concurrent operators is
out of scope — that is what the second worker (`ci-omnipus-3`, added 2026-09-12) addresses.

---

## 1. The measured baseline, and where the time actually goes

The worker is one machine: `performance-8x` (8 performance vCPUs, 16 GB RAM), a 40 GB
`/cache` volume holding the clone, the Go build/mod caches and the npm cache, and a
whole-run `flock -w 5400` serializing every invocation. Gates are driven one SSH call at
a time (`fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> <gate>"`), so every
gate re-pays the per-invocation setup. From a real run on 2026-09-15:

| Gate | Wall clock |
|---|---|
| quick | 115 s |
| go-vet | 56 s |
| lint | 171 s |
| contracts | 270 s |

and across that run `fetch + checkout` executed **5 times** and `npm ci` **4 times** —
all against the same warm volume, all redundant after the first. On top of the repeated
setup, the checks themselves run strictly serially inside `all`: go-test cannot start
until gofmt, go-build, go-vet, golangci-lint, verify-contracts, typecheck and vitest have
all finished, even though most of those do not compete for the same resources.

The two costs have different fixes: run the setup once (a gate that wraps everything in
one invocation), and overlap checks that do not contend (fan out inside that invocation).
`all-no-e2e` does both for everything except `go-race` and `e2e`, which stay exclusive —
`go-race` for RAM, `e2e` because its solo shards already assume they own the box.

## 2. What `all-no-e2e` changes

`all-no-e2e` (`run_all_no_e2e` in `deploy/ci-worker/runci.sh`) covers exactly what `all`
covers minus `go-race` and `e2e`: cli-verb-guard, gofmt, go-build, go-vet, golangci-lint,
verify-contracts, typecheck, vitest, go-test, records-no-sqlite, plus the shared npm ci.
Structure:

1. **Serial setup** — fetch/checkout (top of script, once per invocation by construction),
   then `npm ci`, then `verify-contracts`.
2. **Three concurrent groups**, launched with `&` and joined with `wait`:
   - Group A (Go, RAM-heavy): `go-build → go-vet → go-test`
   - Group B (Node): `typecheck → vitest`
   - Group C (static/cheap): `gofmt → cli-verb-guard → golangci-lint`
3. **After the drain**: `records-no-sqlite`.

Contracts preserved from the existing script:

- Every group runs to completion even if another fails; all three exit codes are
  collected and any non-zero one fails the run. The RESULT / `ALL GATES GREEN` /
  `GATE FAILURE(S)` lines are untouched.
- Each group's output is buffered to its own file under `$TMPDIR` and emitted in a fixed
  order (A, then B, then C) after `wait`. An interleaved live log would make a failure
  unattributable; the buffers keep the per-gate `<gate> -> exit <code>` lines in the
  exact `step` format external harnesses grep for.
- The whole-run `flock` is untouched: the three groups are children of one lock holder.
- A once-a-minute heartbeat prints while the groups run. Buffering otherwise means a
  silent console for the ~30+ min go-test lane, which is the stale-log trap in
  `deploy/ci-worker/CLAUDE.md` (operators reconnect, see nothing, read a live run as
  wedged).

### 2.1 Two gates deliberately outside the fan-out

These are placement decisions forced by measured interactions, not style:

**verify-contracts runs in the serial setup phase, not in a group.**
`make verify-contracts` regenerates the tree the Go group compiles against:
`scripts/gen-contracts.sh` rewrites `pkg/api/generated/*.go` in place, `gofmt -w`s them,
and its Step 5 does `rm -f pkg/gateway/inboundschemas/*.yaml` before re-copying — while
`pkg/gateway/inboundschemas/schemas.go` declares `//go:embed *.yaml`. A concurrent
`go build ./...` that globs that directory inside the empty window fails with
"no matching files found"; a compiler reading a generated file mid-rewrite can see a torn
one. Both are manufactured false REDs. Regeneration therefore completes before anything
compiles. (This also serializes the target's internal `npx tsc -b --noEmit` against
group B's `typecheck` — two concurrent `tsc -b` runs share `.tsbuildinfo` state.)

**records-no-sqlite runs after the groups drain, not inside group C.** It builds and runs
a `pkg/gateway` test binary under the goolm tags — the same OLM-link class whose
unbounded concurrency is this project's known OOM spike. The rule is symmetric: `go-test`
must never run beside another Go test binary, and nothing may run beside `go-test`. On
branches without `pkg/gateway/rest_knowledge_find_propindexless_test.go` the gate is an
instant skip, so the placement costs nothing; on branches that carry the file it costs
its own ~minutes after the drain, off the critical path (group A dominates).

### 2.2 The `-p` bound

`go-test` keeps `run_gotest`'s existing `-p 2` + `GOMAXPROCS=4` unchanged: 2 concurrent
test binaries × 4 threads = 8 Go threads ≈ 8 vCPUs. That is the bound — Go can saturate
the box at worst, never oversubscribe it — and the Node lanes cap themselves
(`vitest --maxWorkers=4`, single tsc process). Worst-case combined oversubscription with
all three groups live is ~2:1, and only for the few minutes golangci-lint's analysis can
overlap go-test's build phase; the 4:1 regime that flaked timing tests (see
`run_gotest`'s 2026-07-17 comment) is not approached, and the flake filter plus the
`--- FAIL` carve-out remain as backstops. Forking a parallel-specific copy of
`run_gotest` with different flags was rejected: a second copy of the flake-filter logic
would drift from the one every other gate uses, and a worker-only scheduling knob was
not needed to stay inside the safe envelope.

## 3. Expected shape of the win

Serial `all`-minus-race-e2e pays `sum(setup, static, node, go)`; `all-no-e2e` pays
`setup + max(A, B, C) + records`. Group A (go-test alone runs tens of minutes)
dominates, so the ceiling on the saving is the removal of the repeated per-gate setup
plus the whole of the Node and static lanes — not a division by three. The largest
single gate is untouched: this change removes waste around `go-test`, it does not make
`go-test` faster.

## 4. Rejected: multiple cheaper machines with scale-to-zero

The alternative — N smaller workers instead of one performance box, stopped when idle —
fails on four facts, each independently sufficient:

1. **A Fly volume attaches to exactly one machine.** The warm caches (Go build/mod, npm,
   the clone, the browsers) live on `/cache`. N workers means N volumes, so N cold caches:
   every run pays the multi-GB Go toolchain rebuild and `npm ci` from scratch, which is
   precisely the cost this change removes.
2. **Stopped machines are free; volumes are not.** Volumes bill continuously whether any
   machine is attached or not. N workers × 40 GB of duplicated cache is a standing cost
   to buy slower runs.
3. **`auto_start_machines` is an HTTP-service feature.** This worker exposes no public
   service (that is deliberate — it is driven over `fly ssh console`). Waking a stopped
   worker therefore cannot be implicit; it requires an explicit
   `flyctl machine start` from whatever dispatcher wants a run — new orchestration to
   own, for a slower result.
4. **`shared-cpu` throttles under sustained load.** A long Go compile is exactly
   sustained load. 4 shared vCPUs are not equivalent to 4 performance vCPUs for
   `go build ./...` + `go test ./...`; the go-test lane would stretch, and it is already
   the critical path.

The existing second worker (`ci-omnipus-3`) already covers the problem multi-machine
actually solves here — queueing when one operator's run holds the lock — without giving
up any single-run latency.

## 5. Open question: is a tiered multi-machine layout worth it?

A plausible middle ground exists: keep the performance box for the Go lane, add one
cheap shared-CPU machine for the Node/static lanes, and let a dispatcher route groups to
machines. Whether that beats one box running the same groups concurrently is an
empirical question about one number: **how long does a single `go-test` run take on a
`shared-cpu-4x` machine versus tonight's performance-8x time?** If the throttled run
stretches past what the current total wall clock saves, the tier is not worth operating;
if it does not, it might be. Measure one run on each shape before reasoning further —
do not decide this from list prices and core counts.
