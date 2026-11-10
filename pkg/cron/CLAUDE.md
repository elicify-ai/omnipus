# pkg/cron — scheduled task execution

What it owns: Persistent schedules, next-fire calculations, job retries and running scheduled tasks.
What it does not own: The Calendar editor; it must not expose raw cron input in the UI.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCronService_ComputeNextRun$' -v -p 1 ./pkg/cron/`

## Pitfalls here

- A wrong next-fire time can delay or skip scheduled work. `service_test.go::TestCronService_ComputeNextRun` checks only whether a next-fire value is present or absent, not its exact time; do not treat its green result as timing-accuracy evidence.

## Never bring back

- Command Center and raw cron entry in the UI (root `CLAUDE.md`, Retired surfaces); this backend engine stays. No local guard covers UI entry; keep that boundary explicit.

## Where decisions live

`docs/internal/specs/calendar-recurrence-redesign-spec.md` (Calendar recurrence editor).
