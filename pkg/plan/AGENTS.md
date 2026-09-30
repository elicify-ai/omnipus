# pkg/plan — stored plans and their lifecycle

What it owns: Plan records, state transitions, handover text and write-ahead intent logging.
What it does not own: Task execution or the Judge; the engine consumes these plan records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^(TestStoreWrite_ClampsHandoverForAWriterThatSkipsNormalize|TestUpdate_OverlongJudgeErrorHandoverIsWrittenNotRejected)$' -count=1 -v -p 1 ./pkg/plan/`

## Pitfalls here

- Clamp handover at the shared write boundary: `TestStoreWrite_ClampsHandoverForAWriterThatSkipsNormalize` writes without normalization and checks the persisted bound and truncation marker; `TestUpdate_OverlongJudgeErrorHandoverIsWrittenNotRejected` checks a long provider-error note is saved within the bound while still naming the failure. Require a named `--- PASS` for each test, not just a green package result.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-049-planning-goals-system-agents.md` (Planning, goals and system agents); `docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md` (Unified goal, plan and subagent).
