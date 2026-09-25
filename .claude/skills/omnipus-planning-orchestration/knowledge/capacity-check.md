# The capacity check — monitor, HOLD semantics, escalation

Detail behind SKILL.md §3. Design source: section 5.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md` (the monitor script itself is
a dev-team tool authored under that design; it is deliberately not `check-`-prefixed, so
the CI guards runner does not adopt it as a gate).

## Calling the monitor

```bash
scripts/dev-machine-capacity.sh
```

It prints its measurements (memory, disk, CPU load, active dispatch count) and exactly
one verdict line:

- `CAPACITY: OK` — dispatch more work.
- `CAPACITY: HOLD <reason>` — hold new dispatches.

**Where it is run:** before every new dispatch wave, before widening an existing one,
and as the first step of idle-playbook lane 3 (dispatching an independent team or
squad). Team-lead and any squad lead widening its own fan-out both run it.

## Signals

| Signal | Role | HOLD threshold (approx.) | Why this signal |
|---|---|---|---|
| Memory | **hard** | Available RAM below ~4 GB, or swap-in activity rising | Parallel agents and build caches OOM the machine, not just one lane |
| Disk | **hard** | Free space on the workspace volume below ~20 GB | Worktrees, node_modules and Go caches grow fast |
| CPU load | advisory — feeds the verdict, never holds alone | Sustained 1-minute load above ~80% of logical cores over two samples ~30 s apart | One spike is noise; sustained load means lanes already compete |
| Active dispatches | advisory — counted from the ledger's in-flight rows across all squad files | More than ~12 in-flight rows | The work-in-flight measure; the ledger knows what is actually running, the process table does not |

Thresholds are named constants at the top of the script, founder-adjustable — the
numbers above are the shipped starting points, tuned through governance.

## What HOLD means

- **A HOLD queues the dispatch — it never cancels work** already running.
- Retry after the next check passes or a running lane finishes.
- The monitor is **binding on dispatch decisions and advisory to the founder**.

## The 20-minute escalation

A HOLD lasting longer than **20 minutes** fires an event status update to the founder
(events only — SKILL.md §5), stating the reason and what is queued; the founder can
override the HOLD. A silent, open-ended HOLD is the failure this escalation exists to
prevent.
