# pkg/clidetect — installed CLI discovery

What it owns: Finding supported command-line applications on the host without starting them.
What it does not own: Execution of a discovered program or its permissions.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDetectAll_KeysAndNoSpawn$' -count=1 -v -p 1 ./pkg/clidetect/`

## Pitfalls here

- Keep discovery filesystem-only: do not launch a found program just to identify it. Despite its name, `TestDetectAll_KeysAndNoSpawn` checks only that `DetectAll` returns the three supported keys; it cannot detect a process launch. Review `clidetect.go::DetectAll` and `detector.detect` for the no-spawn design until a dedicated assertion exists. Require a named `--- PASS` to confirm the key test ran.

## Never bring back

— none known

## Where decisions live

— none known
