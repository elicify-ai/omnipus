# pkg/askuser — structured clarification registry

What it owns: Question validation, durable pending sets, first-valid submission and resume dispatch.
What it does not own: The chat card UI or its WebSocket frames.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCreatePending_SteeredSession_ObservedAndRejected$' -v -p 1 ./pkg/askuser/`

## Pitfalls here

- A steered session must not accept an obsolete pending question. `registry_steer_test.go::TestCreatePending_SteeredSession_ObservedAndRejected` checks this race.

## Never bring back

— none known

## Where decisions live

`docs/internal/specs/askuserquestion-tool-spec.md` (validation and resume contract).
