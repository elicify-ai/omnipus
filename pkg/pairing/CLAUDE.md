# pkg/pairing — device pairing records

What it owns: Pending-device approvals, rejection and expiry in the pairing store.
What it does not own: Device transports or the gateway's approval UI.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestApprove_NotFound_DoesNotResurrectRejected$' -v -p 1 ./pkg/pairing/`

## Pitfalls here

- Approval of a missing entry must not recreate a rejected device. `store_test.go::TestApprove_NotFound_DoesNotResurrectRejected` checks this state transition.

## Never bring back

— none known

## Where decisions live

— none known
