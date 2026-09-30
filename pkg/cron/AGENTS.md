# pkg/cron — scheduled task execution

What it owns: Persistent schedules, next-fire calculations, job retries and running scheduled tasks.
What it does not own: The Calendar editor; it must not expose raw cron input in the UI.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCronService_ComputeNextRun$' -v -p 1 ./pkg/cron/`

## Pitfalls here

- Changing next-fire calculation can skip or duplicate scheduled work. `service_test.go::TestCronService_ComputeNextRun` checks the scheduling path.

## Never bring back

- Command Center and raw cron entry in the UI (root `CLAUDE.md`, Retired surfaces); this backend engine stays. No local guard covers UI entry; keep that boundary explicit.

## Where decisions live

`docs/internal/specs/calendar-recurrence-redesign-spec.md` (Calendar recurrence editor).
