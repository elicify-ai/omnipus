# pkg/commands — in-chat command dispatch

What it owns: Registered command parsing, surface gating and handler dispatch.
What it does not own: Natural-language turns or built-in tool-policy composition.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestExecutor_RegisteredWithoutHandler_ReturnsPassthrough$' -v -p 1 ./pkg/commands/`

## Pitfalls here

- A registered name without an executable handler should pass through rather than silently consume a message. `executor_test.go::TestExecutor_RegisteredWithoutHandler_ReturnsPassthrough` checks this case.

## Never bring back

— none known

## Where decisions live

— none known
