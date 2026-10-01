# pkg/memrooms — private and shared memory locations

What it owns: Per-agent private and per-workspace shared memory directory layouts and counter logs.
What it does not own: Derived search indexes or conversation transcript storage.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestLogs_AppendCounterRecord_WritesValidJSONL$' -v -p 1 ./pkg/memrooms/`

## Pitfalls here

- Counter events must stay valid append-only JSON lines. `logs_test.go::TestLogs_AppendCounterRecord_WritesValidJSONL` checks the log format.

## Never bring back

— none known

## Where decisions live

— none known
