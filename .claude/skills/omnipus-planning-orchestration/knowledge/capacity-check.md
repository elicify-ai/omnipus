# The capacity check — monitor, HOLD semantics, escalation

Detail behind SKILL.md §3. Design source: section 5.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md` (the monitor script itself is
a dev-team tool authored under that design; it is deliberately not `check-`-prefixed, so
the CI guards runner does not adopt it as a gate).

Last reviewed: 2026-10-28 — dev-team roster update (ten roles)

Current safety authority: root `CLAUDE.md` ("Running CLI agents" and "Build, test,
and quality gates"). The monitor and the worker controller are separate instruments.

## Calling the monitor

```bash
scripts/dev-machine-capacity.sh
```

It prints its measurements (memory, disk, CPU load, active dispatch count) and exactly
one verdict line:

- `CAPACITY: OK` — monitor checks passed; complete the controller/load pre-flight too.
- `CAPACITY: HOLD <reason>` — hold new dispatches.

**Where it is run:** before every new dispatch wave, before widening an existing one,
and as the first step of idle-playbook lane 3 (dispatching an independent team or
stream). Every team-lead session widening its own fan-out runs it.

## Signals

| Signal | Role | HOLD threshold (approx.) | Why this signal |
|---|---|---|---|
| Memory | **hard** | Available RAM below ~3 GB (founder, 2026-09-27; was 4 GB), or swap-in activity rising | Parallel agents and build caches OOM the machine, not just one lane |
| Overload | **hard** (founder, 2026-09-28) | 5-minute load average above 4x logical cores (32 on an 8-core Mac); `DEV_CAPACITY_OVERLOAD_FACTOR` | A load of 700+ stalled every lane while memory and disk read fine; normal busy periods stay below it |
| Disk | **hard** | Free space on the workspace volume below ~20 GB | Worktrees, node_modules and Go caches grow fast |
| CPU load | advisory — feeds the verdict, never holds alone | Sustained 1-minute load above ~80% of logical cores over two samples ~30 s apart | One spike is noise; sustained load means lanes already compete |
| Active dispatches | advisory — feeds the verdict, never holds alone (Round 18) | More than ~12 in-flight rows | The work-in-flight measure; the ledger knows what is actually running, the process table does not |

**2026-10-23 amendment:** the advisory-only one-minute CPU row above describes the
existing monitor, not permission to launch. Sample the **1-minute** load (`uptime`),
compare it with the refusal threshold in root `CLAUDE.md` ("Running CLI agents"),
and queue new workers when over it even if the monitor says OK. The five-minute
overload check is not a substitute. The controller load refusal is implemented;
use its actual admission result, not this monitor's advisory CPU row. Current
controller behavior and shared-root lock exemptions are in root `CLAUDE.md`
("Worker safety") and `knowledge/dispatch-and-watch.md`. Keep memory/disk HOLDs too.

Thresholds are named constants at the top of the script, founder-adjustable — the
numbers above are the shipped starting points, tuned through governance.

**Exit code:** the script exits 0 for both `CAPACITY: OK` and `CAPACITY: HOLD` — parse
the verdict line, do not trust the exit code alone. It exits 2 only when a hard signal
(memory or disk) could not be measured at all on this platform; treat exit 2 the same as
a HOLD — the verdict cannot be trusted without both hard signals.

## What HOLD means

- **A HOLD queues the dispatch — it never cancels work** already running.
- Retry after the next check passes or a running lane finishes; re-check live lane/worktree ownership before resuming.
- The monitor is **binding on dispatch decisions and advisory to the founder**.

## The 20-minute escalation

A HOLD lasting longer than **20 minutes** fires an event status update to the founder
(events only — SKILL.md §5), stating the reason and what is queued; the founder can
override the monitor HOLD. That is not silent permission to bypass the controller
or start a second live owner; report a controller refusal to the founder explicitly. A silent, open-ended HOLD is the failure this escalation exists to
prevent.
