# pkg/goal — stored goal records

What it owns: Goal identity, phases, criteria and persisted lifecycle state.
What it does not own: Agent-loop activation or the task and plan stores; those consume goal records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestActivateFromDefining$' -v -p 1 ./pkg/goal/`

## Pitfalls here

— none known

## Never bring back

- The goal confirm gate: goals activate without a confirmation round; `scripts/check-no-goal-confirm-gate.sh` guards the retired symbols.

## Where decisions live

`docs/internal/architecture/ADR-086-goal-as-a-first-class-entity.md` (Goal as a first-class entity); `docs/internal/architecture/ADR-088-work-first-goal-flow.md` (Work-first goal flow).
