# CI worker cluster — tier dispatch and scale-to-zero

Status: implemented (`deploy/ci-worker/ci-cluster.sh`), 2026-09-16. Operational authority for
the machines themselves remains `deploy/ci-worker/CLAUDE.md`; this note records the design of
the dispatcher and why the pieces are shaped the way they are.

**Operating-policy amendment — 2026-10-23:** root `CLAUDE.md` ("CI scope and
remote pre-flight", "CI quarantine") governs permission and qualified results. This
document describes runner capabilities, not authorization to run full CI on work
or lane branches; apply the shared skill's "Build and test" procedure first.

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

Gates are grouped so CPU-sensitive Go and SPA work runs sequentially on performance while
contracts and cross-platform checks overlap it on shared machines. The map is data at the
top of `ci-cluster.sh` (machine ids from `CI_CLUSTER_*_MACHINE` env vars, whole map from
`CI_CLUSTER_TIERS`) — never dispatch logic. Placement follows the founder's 2026-10-21
"ok a" decision; the 2026-10-12 no-resize ruling stands.

| Tier | Default machine | Gates, in dispatch order (from `runci.sh`'s `case` block) |
|---|---|---|
| `go` | `080d74db170408` (performance) | `gofmt go-build go-vet lint go-test go-race spa` |
| `node` | `d8d0d90a1e9648` (shared) | `contracts` |
| `xplat` | `78113d4f329908` (shared) | `embed-build records-no-sqlite cli-verb-guard` |

The new placement has not been timed as a whole: the former Go-only tier was ~43 min
(`go-race` alone ~25), contracts + SPA together ~12 min, and xplat ~8 min. The `go` tier
now adds SPA time; contracts-only time has not been measured separately.

Why these groups:

- **`go` uses performance** because `go-test` and `go-race` dominate CPU-bound work;
  `spa` follows all of them in the same sequential tier, so it cannot compete with them.
  `spa` runs npm ci + typecheck + vitest; `typecheck` and `vitest` are steps inside that
  gate, not separately dispatchable gates.
- **`node` keeps contracts only** (npm ci + verify-contracts). Both `contracts` and `spa`
  call `runci.sh::run_npm`, which unconditionally runs `npm ci` and replaces `node_modules`.
  A combined tier would not reuse an installed dependency tree. Keeping contracts on its
  existing shared machine is the smallest change and preserves all per-tier overrides.
- **`xplat` runs the cross-platform path**: `embed-build` (SPA build + embed sync + Go build,
  whose `run_gobuild` includes the mipsle/lite cross-compile link checks), plus the two small
  gates the proposal didn't place — `records-no-sqlite` (branch-scoped, usually skips) and
  `cli-verb-guard` (a grep).
- **`e2e` is deliberately NOT in the default map**: it runs ~20–30 min and needs the
  OpenRouter secrets. Run it on performance `080d74db170408` after the entire `go` tier
  (including SPA), never in parallel: by hand (`fly ssh console --app ci-omnipus-1
  --machine 080d74db170408 -C "/cache/runci.sh <ref> e2e"`) or append `e2e` after `spa`
  on the same `go` line in a custom `CI_CLUSTER_TIERS` map. Do not add a second tier on
  that machine.
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

## Sizing and placement - sized per test type, on purpose

The cluster is deliberately NOT five identical machines. One machine is `performance` 8x/16 GB
(`080d74db170408`); four are `shared` 8x/16 GB (`d8d3977c946558`, `d8d0d90a1e9648`,
`78113d4f329908`, `48ee71eb1e9768`). Founder ruling, 2026-10-12: that is the intended setup and
there is no plan to resize; the work is placed to fit the machine.

| Work | Where | Why |
|---|---|---|
| `go` tier (Go gates, then `spa`) | the performance machine | CPU-bound Go work and timing-sensitive SPA tests run sequentially, without competing for cores |
| full `e2e` gate | the performance machine, **after** the entire go tier including SPA, never in parallel with it | 24 shards of Chromium rendering; starves under shared vCPU throttling |
| `node` tier (`contracts` only) | a shared machine (default `d8d0d90a1e9648`) | contract verification stays separate; no installed node_modules reuse between gates |
| `xplat` tier (`embed-build`, `records-no-sqlite`, `cli-verb-guard`) | a shared machine (default `78113d4f329908`) | unchanged placement |
| targeted few-spec e2e rerun (`E2E_SPECS=...`) | may use a shared machine | a handful of specs, no matrix |

**Measured, not assumed.** Shared vCPUs throttle under sustained load. On 2026-10-11 the full
e2e gate was run on shared machines: it ran 2 h at load around 21 and starved (even the baseline
failed nearly everything). `embed-build` on a shared machine took ~46 min against minutes on the
performance one. Both were a **placement error** - heavy sustained work put on a shared machine -
not undersizing. A false RED from a starved machine trains people to ignore a gate (see the e2e
trap history in `deploy/ci-worker/CLAUDE.md`), which is why the placement rule above is a rule.
The 2026-10-21 founder decision moves SPA for the same placement reason: three shared-machine
runs each failed a different timing test, the performance run passed all 13,599 tests, and a
shared spare showed ~62% CPU steal (time taken by other virtual machines). These are the
decision's supplied measurements, not a new cluster run for this dispatcher change.

## Configuration surface

| Env | Meaning | Default |
|---|---|---|
| `CI_CLUSTER_APP` | Fly app name | `ci-omnipus-1` |
| `CI_CLUSTER_GO_MACHINE` / `_NODE_` / `_XPLAT_` | machine id per tier (default map) | `080d74db170408` (performance) / `d8d0d90a1e9648` (shared) / `78113d4f329908` (shared); set EMPTY → tier skipped |
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
