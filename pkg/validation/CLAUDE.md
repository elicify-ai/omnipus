# pkg/validation — entity identifier validation

What it owns: A shared validator for entity identifiers.
What it does not own: Entity persistence or gateway request handling.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestEntityID_BehaviorMatchesOldValidator$' -v -p 1 ./pkg/validation/`

## Pitfalls here

- The shared validator must retain its established acceptance boundary. `entityid_test.go::TestEntityID_BehaviorMatchesOldValidator` checks equivalence with the previous validator.

## Never bring back

— none known

## Where decisions live

— none known
