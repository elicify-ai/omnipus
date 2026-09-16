# CI worker cluster — tier dispatch and scale-to-zero

Status: implemented (`deploy/ci-worker/ci-cluster.sh`), 2026-09-16. Operational authority for
the machines themselves remains `deploy/ci-worker/CLAUDE.md`; this note records the design of
the dispatcher and why the pieces are shaped the way they are.

## Context

The CI workers used to be one shared machine: every run took an exclusive `flock` on
`/tmp/runci.lock` for its whole duration, so concurrent sessions queued behind each other for
up to 90 minutes. The former multi-app shape was destroyed by founder decision — today there
is **one app, `ci-omnipus-1`, several machines**, each machine a
tier with its own `/cache` volume and its own `/tmp/runci.lock`. That per-machine lock is what
makes real parallelism safe: two machines never share a checkout, a build cache, or a lock.

Machines are stopped when idle and bill nothing for compute while stopped. The failure mode
that motivated this design was the opposite: machines left running unattended — an 8-CPU box
billing around the clock because a session died without stopping it. The dispatcher's central
invariant is therefore teardown, not dispatch: **every machine this run starts gets stopped on
every exit path — success, gate failure, pre-flight refusal, and Ctrl-C.**

## The tier map

Gates are grouped so the heaviest Go work gets the biggest machine and lighter work overlaps
it. The map is data at the top of `ci-cluster.sh` (machine ids from `CI_CLUSTER_*_MACHINE`
env vars, whole map from `CI_CLUSTER_TIERS`) — never flow — because tier machines are
provisioned by the harness and their ids are not known when the script is written.

| Tier | Machine | Gates (verified against `runci.sh`'s `case` block) |
|---|---|---|
| `go` | 1 (largest) | `gofmt go-build go-vet lint go-test go-race` |
| `node` | 2 | `contracts spa` |
| `xplat` | 3 | `embed-build records-no-sqlite cli-verb-guard` |

Why these groups:

- **`go` gets the big box** because `go-test` (~19 min for `pkg/agent` alone, measured
  uncontended) and `go-race` dominate wall-clock; everything else rides along on a machine
  that is CPU-saturated anyway. `gofmt` is seconds.
- **`node` runs the npm side**: `contracts` (npm ci + verify-contracts) and `spa` (npm ci +
  typecheck + vitest). The original proposal listed "typecheck, vitest, contracts" — but
  `typecheck` and `vitest` are *steps inside the `spa` gate*, not dispatchable gates; `spa`
  is the dispatchable unit that contains them.
- **`xplat` runs the cross-platform path**: `embed-build` (SPA build + embed sync + Go build,
  whose `run_gobuild` includes the mipsle/lite cross-compile link checks), plus the two small
  gates the proposal didn't place — `records-no-sqlite` (branch-scoped, usually skips) and
  `cli-verb-guard` (a grep).
- **`e2e` is deliberately NOT in the default map**: it runs ~20–30 min, needs the
  OpenRouter secrets, and would pin one machine for the length of a full cluster run. Run it
  per-machine by hand (`fly ssh console --app ci-omnipus-1 --machine <id> -C
  "/cache/runci.sh <ref> e2e"`) or add it to a custom `CI_CLUSTER_TIERS` line.
- **`all` is refused outright** (exit 2): it would run every gate serially on one machine —
  exactly what the cluster exists to avoid.
- **One tier per machine, enforced**: two tiers on one machine would serialize on that
  machine's `runci.lock` and share one checkout. The dispatcher refuses a map that assigns
  the same machine twice.

The set of legal gate names is *derived* by parsing the `case` block of the local
`runci.sh` at run time, so the dispatcher cannot drift from the script it dispatches: add a
gate to `runci.sh`'s `case` and it becomes dispatchable with no second edit. The md5
pre-flight check (below) then guarantees the deployed copy on every machine is that same
script.

## Traps the dispatcher must respect

All are documented with burn history in `deploy/ci-worker/CLAUDE.md`:

1. **`fly ssh console -C` does not forward stdin.** Any file transfer to a machine uses
   `fly ssh sftp put` (the md5-refusal message prints the exact recipe). The dispatcher
   itself transfers nothing — it only needs `runci.sh` to already be deployed.
2. **`/cache/runci.sh` is deployed per machine.** The dispatcher md5-compares the deployed
   script on *every* machine it will use against the repo copy *before dispatching anything*,
   and refuses (exit 2) on mismatch — otherwise tiers run different script versions and the
   verdicts are not comparable.
3. **Wrapper-exit-code false-green.** `fly ssh console -C` returns the SSH wrapper's status,
   not the command's. The dispatcher never reads those exit codes for verdicts; it parses
   each gate's log for `ALL GATES GREEN` / `GATE FAILURE(S)` / `REAL FAILURE`. A log with no
   RESULT line is `FAIL:no-result-line` (SSH drop or interruption) — never assumed green.
   The proof harness exploits this deliberately: its stub `fly` exits 0 even for failing
   gates, and the dispatcher still reports the failure.
4. **Stale-checkout false-red.** Every gate log's `HEAD: <sha>` line must be a prefix of the
   locally-resolved ref sha. A worker that tested a different commit fails its gates'
   verdicts (`FAIL:head-mismatch`), so a mixed-commit run can never be reported as a pass.
   (By nature this check fires after dispatch — the HEAD line is printed during the run; it
   refuses to *bless the verdict*, not to start.)

## Lifecycle — scale to zero

1. **Pre-flight, per machine, before any dispatch**: `fly machine status` → start it if
   stopped (recording ownership) → poll `fly ssh console -C true` until the machine is
   actually SSH-reachable (a started machine is not immediately usable) → md5 check.
2. **Dispatch, concurrently**: one background job per tier; each gate is its own
   `fly ssh console` with output captured to `<logdir>/<tier>-<gate>.log`. Gates within a
   tier run sequentially (the machine's `runci.lock` would queue a second run for up to 90
   minutes); all gates in a tier run even after one fails, mirroring `runci.sh all`.
3. **Collect**: verdicts + HEAD shas from the logs; a summary table; one exit code
   (0 all green · 1 any failure/incomplete/mismatch · 2 config or pre-flight refusal).
4. **Teardown**: an EXIT trap stops every machine *this run started* — including on gate
   failure and on SIGINT/SIGTERM (proven with a stubbed `fly` in the script's development
   log, 2026-09-16). Machines that were **already running** when the dispatcher arrived are
   used but never stopped: another session may own them. Note on interrupt semantics:
   killing the dispatcher mid-gate cuts the SSH session, which kills the non-detached
   `runci.sh` on the machine; the log keeps no RESULT line and the run reports failure.

**Cost note — stopping saves compute, not storage.** A stopped machine bills nothing for
CPU but its persistent `/cache` volume bills while the machine is stopped. Scale-to-zero is
therefore about not paying for 8 idle vCPUs, not about zero bill. The volumes are what make
repeat runs fast (Go/npm caches and the cloned repo survive), so deleting them to save
storage would trade minutes of every run for cents — not worth it.

## Sizing — performance vs shared-cpu, measured or not at all

The `go` tier needs the big `performance-8x` box: `go-test` and `go-race` are sustained,
CPU-bound compiles, and `runci.sh`'s own contention tuning (`-p 2` + `GOMAXPROCS=4`) assumes
8 real cores.

The `node` and `xplat` tiers *may* be cheaper as `shared-cpu-4x` machines — but that must be
**measured, not assumed**. Shared vCPUs throttle under sustained load, so 4 shared cores are
not 4 performance cores on a long compile; a naive "vitest is just tests, shared is fine"
guess is exactly how a wall-clock doubles silently. The measurement is one comparable run of
each tier's heaviest gate on both machine sizes (for the `go` tier that is one `go-test` on
shared vs performance; for the others, `spa` and `embed-build`), comparing wall-clock — and
watching for throttle-induced timing flakes, which are the more dangerous outcome (a false
RED trains people to ignore a gate; see the e2e trap history in `deploy/ci-worker/CLAUDE.md`).
Until those numbers exist, all tiers stay on `performance`.

## Configuration surface

| Env | Meaning | Default |
|---|---|---|
| `CI_CLUSTER_APP` | Fly app name | `ci-omnipus-1` |
| `CI_CLUSTER_GO_MACHINE` / `_NODE_` / `_XPLAT_` | machine id per tier (default map) | unset → tier skipped |
| `CI_CLUSTER_TIERS` | full map override, `<name>\|<machine>\|<gates>` per line | the default map above |
| `CI_CLUSTER_START_TIMEOUT` | seconds to wait for a started machine to answer SSH | 180 |
| `CI_CLUSTER_LOG_DIR` | where per-gate logs go | fresh dir under `$TMPDIR` |

Usage: `deploy/ci-worker/ci-cluster.sh <git-ref> [tier …]` — optional trailing tier names
restrict the run (e.g. just `go` on a one-machine day).

## Known limits

- Real-flyctl flag spellings (`fly machine status --json` / `start` / `stop`, `fly ssh
  console --machine`) follow flyctl's documented forms and the lane brief, but this lane was
  forbidden from running any real `fly` command — they were exercised only against a stub.
  The first real run should watch the pre-flight lines.
- The dispatcher does not deploy `runci.sh`; a mismatch is a hard refusal with the redeploy
  recipe, by design (deploying is an operator action, and a mid-run redeploy mutates the
  script under a live run — see the mutex note in `deploy/ci-worker/CLAUDE.md`).
- If a machine's `runci.sh` invocation queues behind another operator's run (per-machine
  lock), the dispatcher detects the "already running" line in the log and warns that the
  verdict may reflect contention; it does not kill the other run (never kill another
  operator's run).
