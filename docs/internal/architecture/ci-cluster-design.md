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
4. **A dead gate must not leave an indefinite console wait.** The 2026-10-18
   lint log stopped mid-line with no verdict while the local console remained
   waiting. Inspected machine/kernel logs did not establish the exact killer.
   Liveness checks and bounded control calls handle the missing-verdict state
   without claiming an unproved cause of death.
5. **Stale-checkout false-red.** Every gate log's `HEAD: <sha>` line must be a prefix of the
   locally-resolved ref sha. A worker that tested a different commit fails its gates'
   verdicts (`FAIL:head-mismatch`), so a mixed-commit run can never be reported as a pass.
   (By nature this check fires after dispatch — the HEAD line is printed during the run; it
   refuses to *bless the verdict*, not to start.)

## Lifecycle — scale to zero

1. **Pre-flight, per machine, before any dispatch**: `fly machine status` → start it if
   stopped (recording ownership) → poll `fly ssh console -C true` until the machine is
   actually SSH-reachable (a started machine is not immediately usable) → md5 check.
2. **Run evidence and dispatch**: the local run directory is exclusively
   created. An existing path is refused before machine work without modifying
   or adopting old logs. An entropy/identity-write failure records a local
   `FAIL:dispatch` outcome without launching the gate. Nonzero local tier exits
   independently fail the overall invocation while collection still proceeds;
   unlike remote SSH wrapper statuses, these are reliable orchestration errors.
   Dispatch is concurrent: one background job per tier; each gate is its own
   `fly ssh console` with output captured to `<logdir>/<tier>-<gate>.log`. Gates within a
   tier run sequentially (the machine's `runci.lock` would queue a second run for up to 90
   minutes); all gates in a tier run even after one fails, mirroring `runci.sh all`.

   **Gate watchdog (`ci-cluster.sh::dispatch_tier`).** Every gate invocation
   has a fresh random token, saved locally and passed as the runner's third
   argument. The liveness probe uses one quoted procps pattern for the resolved
   full SHA, gate and exact token. Its explicit `PROBE_STATUS` distinguishes a
   match, a clean no-match and an error independently of flyctl's wrapper
   status. A healthy running or lock-queued gate has no duration cap.

   The dispatcher checks console progress each second and probes at
   `CI_CLUSTER_WATCH_INTERVAL` (default 30s). After
   `CI_CLUSTER_DEAD_STRIKES` (3) consecutive not-alive answers it terminates and
   reaps the owned console and attempts recovery. Each probe has its own
   `CI_CLUSTER_PROBE_TIMEOUT` (20s). Recovery/copy calls have
   `CI_CLUSTER_RECOVER_TIMEOUT` (60s); a timeout is an explicit failed operation,
   never a successful partial copy. TERM-to-KILL escalation is bounded, and tier
   exit traps retain ownership of active control and console children through
   cancellation. A console that prints its verdict then hangs is reaped too.

   **Attempt-bound evidence.** The runner names its file
   `/cache/logs/<gate>@<sha>-<stamp>-at<token>.log` and announces its path.
   Recovery refuses a missing or mismatched announcement: there is no
   newest-by-gate search. Both recovery and collection require the exact
   attempt/gate/ref identity line and a matching checked-out HEAD after a
   successful transfer. An older PASS from the same commit cannot certify a
   new attempt. Recovered partial artifacts stay available for triage; a
   remote-only `RUNCI: KILLED by signal ...` trailer remains a nonzero
   `FAIL:killed-by-signal`, not a reason-less stalled result.

   With no valid completed verdict, the result is an explicit STALLED/DIED
   (`FAIL:stalled`) naming the gate, machine and last output. The real runner's
   HUP/INT/TERM traps print their trailer and exit 129/130/143; handling may be
   deferred while a foreground child runs. SIGKILL has no catchable trailer.
   The signal fixture rejects every wait timeout regardless of the eventual
   cleanup status and retains separate timeout/observed-exit receipts. A
   TERM-only trailer-then-hang mutation verifies that distinction.

   **Producer and partial-transfer coverage.** The root runner prologue and
   user-switch re-exec are exercised with guest filesystem prefixes remapped
   into a private scratch guest and setup/user-switch commands stubbed. Both
   supplied tokens must survive the real filename/header/child-argv producer.
   For recovery and collection separately, unsuccessful transfers deliberately
   leave correct identity/HEAD/verdict bytes behind; transfer status must
   still reject them. The companion mutation checks cover both boundaries.
   `FAIL:no-result-line`, `FAIL:gate` and other existing verdicts remain.

3. **Collect**: verdicts + HEAD shas from the logs; a summary table; one exit code
   (0 all green · 1 any failure/incomplete/stalled/mismatch · 2 config or pre-flight refusal).
4. **Teardown**: the EXIT path first reaps its owned local transports/tiers,
   then collects and identity-verifies every dispatched log before stopping a
   machine it started. Interrupted or failed collection leaves that machine
   running with a manual-retrieval warning; the same exception applies to
   foreign owners, active work or unreadable ownership/idleness. Those checks
   and stop calls are bounded control operations. Already-running machines are
   never claimed or stopped. Remote signal delivery on an SSH disconnect is
   not assumed: the log and liveness evidence decide the verdict.

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
| `go` tier (`go-test`, `go-race`) | the performance machine | sustained, CPU-bound compiles; `runci.sh`'s contention tuning (`-p 2` + `GOMAXPROCS=4`) assumes 8 real cores |
| full `e2e` gate | the performance machine, **after** the go tier, never in parallel with it | 24 shards of Chromium rendering; starves under shared vCPU throttling |
| `node` tier (`contracts`, `spa`) | a shared machine (default `d8d0d90a1e9648`) | short and light enough |
| `xplat` tier (`embed-build`, `records-no-sqlite`, `cli-verb-guard`) | a shared machine (default `78113d4f329908`) | same |
| targeted few-spec e2e rerun (`E2E_SPECS=...`) | may use a shared machine | a handful of specs, no matrix |

**Measured, not assumed.** Shared vCPUs throttle under sustained load. On 2026-10-11 the full
e2e gate was run on shared machines: it ran 2 h at load around 21 and starved (even the baseline
failed nearly everything). `embed-build` on a shared machine took ~46 min against minutes on the
performance one. Both were a **placement error** - heavy sustained work put on a shared machine -
not undersizing. A false RED from a starved machine trains people to ignore a gate (see the e2e
trap history in `deploy/ci-worker/CLAUDE.md`), which is why the placement rule above is a rule.

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
