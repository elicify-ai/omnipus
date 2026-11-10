# pkg/pairing — device pairing records

What it owns: Pending-device approvals, rejection and expiry in the pairing store.
What it does not own: Device transports or the gateway's approval UI.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestApprove_NotFound_DoesNotResurrectRejected$' -count=1 -v -p 1 ./pkg/pairing/`

## Pitfalls here

- Approval after rejection must not recreate the device. `store_test.go::TestApprove_NotFound_DoesNotResurrectRejected` checks the missing-device error and empty returned value, but does not inspect the paired-device store after approval; an additional state assertion is needed to prove no resurrection. Require a named `--- PASS` for this narrower check.

## Never bring back

— none known

## Where decisions live

— none known
