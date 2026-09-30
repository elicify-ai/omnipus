# pkg/plan — stored plans and their lifecycle

What it owns: Plan records, state transitions, handover text and write-ahead intent logging.
What it does not own: Task execution or the Judge; the engine consumes these plan records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestClampHandoverText_IsIdempotentAndDeterministic$' -v -p 1 ./pkg/plan/`

## Pitfalls here

- Clamp handover text at its shared storage boundary rather than at one writer: a later writer can otherwise persist an overlong handover. `handover_clamp.go` and `TestClampHandoverText_IsIdempotentAndDeterministic` cover the bound.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-049-planning-goals-system-agents.md` (Planning, goals and system agents); `docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md` (Unified goal, plan and subagent).
