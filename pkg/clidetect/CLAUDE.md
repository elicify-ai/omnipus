# pkg/clidetect — installed CLI discovery

What it owns: Finding supported command-line applications on the host without starting them.
What it does not own: Execution of a discovered program or its permissions.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDetectAll_KeysAndNoSpawn$' -v -p 1 ./pkg/clidetect/`

## Pitfalls here

- Discovery must not launch a found command just to identify it. `clidetect_test.go::TestDetectAll_KeysAndNoSpawn` covers the no-spawn contract.

## Never bring back

— none known

## Where decisions live

— none known
