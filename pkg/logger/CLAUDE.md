# pkg/logger — application logging

What it owns: Process-wide log formatting, level control and bridges to structured logging.
What it does not own: Tamper-evident security audit records (`pkg/audit`).

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestSlogHandler_Handle_MessageAndFieldsSurvive$' -v -p 1 ./pkg/logger/`

## Pitfalls here

- The structured-log bridge must preserve both messages and fields. `slog_bridge_test.go::TestSlogHandler_Handle_MessageAndFieldsSurvive` checks that handoff.

## Never bring back

— none known

## Where decisions live

— none known
