# pkg/health — process health endpoints

What it owns: Health, readiness and reload HTTP handlers and their reported checks.
What it does not own: The gateway's main listener or the services providing each check.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestReadyHandler_NotReady$' -v -p 1 ./pkg/health/`

## Pitfalls here

- A process not yet marked ready must not return a ready status. `server_test.go::TestReadyHandler_NotReady` covers the initial state.

## Never bring back

— none known

## Where decisions live

— none known
