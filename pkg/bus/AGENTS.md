# pkg/bus — in-process message bus

What it owns: Inbound and outbound message envelopes and their in-process publish/subscribe path.
What it does not own: Connector configuration, identity matching or transcript persistence.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestPublishInbound_BusClosed$' -v -p 1 ./pkg/bus/`

## Pitfalls here

- A closed bus must not silently accept an inbound message. `bus_test.go::TestPublishInbound_BusClosed` covers the closed path.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-019-v01-workspaces-foundation.md` (Workspaces foundation; message instance identity).
