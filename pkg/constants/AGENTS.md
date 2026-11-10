# pkg/constants — internal channel names

What it owns: The shared distinction between internal (`cli`, `system`) and external channel names.
What it does not own: Connector registration or channel routing.

## Run its tests

— none known; this folder has no package-local `_test.go`. Do not interpret an empty `go test -run` match as a pass.

## Pitfalls here

- Internal channel names must not be shown to users or stored as the last active external channel. `channels.go::IsInternalChannel` is the shared predicate; no local test currently checks it.

## Never bring back

— none known

## Where decisions live

— none known
