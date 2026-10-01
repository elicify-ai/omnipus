# pkg/daemon — gateway background-process primitives

What it owns: The shared PID-file convention and gateway spawn/status/stop primitives for the CLI and launcher.
What it does not own: The gateway HTTP lifecycle or user-facing launcher UI.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestStatus_StalePID_NeverExisted$' -v -p 1 ./pkg/daemon/`

## Pitfalls here

- A stale PID file does not prove a gateway is running. `daemon_test.go::TestStatus_StalePID_NeverExisted` covers stale-file handling.

## Never bring back

— none known

## Where decisions live

— none known
