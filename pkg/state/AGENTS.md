# pkg/state — last-active workspace state

What it owns: Atomic persistence of a workspace's last active channel, chat identifier and update timestamp.
What it does not own: Full conversation transcripts or work/task records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAtomicSave$' -v -p 1 ./pkg/state/`

## Pitfalls here

- State updates must use atomic writes so an interrupted save does not corrupt the stored record. `state_test.go::TestAtomicSave` tests the save path.

## Never bring back

— none known

## Where decisions live

— none known
