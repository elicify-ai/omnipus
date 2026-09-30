# pkg/state — last-active workspace state

What it owns: Atomic persistence of a workspace's last active channel, chat identifier and update timestamp.
What it does not own: Full conversation transcripts or work/task records.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAtomicSave$' -v -p 1 ./pkg/state/`

## Pitfalls here

- Keep atomic writes so interruptions do not corrupt stored state. `state_test.go::TestAtomicSave` checks a completed save and reload, not an interrupted write; its green result is not crash-safety evidence.

## Never bring back

— none known

## Where decisions live

— none known
