# pkg/askuser — structured clarification registry

What it owns: Question validation, durable pending sets, first-valid submission and resume dispatch.
What it does not own: The chat card UI or its WebSocket frames.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCreatePending_SteeredSession_ObservedAndRejected$' -count=1 -v -p 1 ./pkg/askuser/`

## Pitfalls here

- Route a steered session's new question upward, not into a user card. `registry_steer_test.go::TestCreatePending_SteeredSession_ObservedAndRejected` checks rejection and relay when the resolver identifies the session as steered; it does not stage an old card, inspect stored pending sets, or cover resolver failures. `registry.go::CreatePending` maps a resolver error to `AudienceNone`; review that path separately. Require a named `--- PASS` for the selected test.

## Never bring back

— none known

## Where decisions live

`docs/internal/specs/askuserquestion-tool-spec.md` (validation and resume contract).
