# pkg/testutil — shared test fixtures and helpers

What it owns: Reusable test harnesses and security payload fixtures.
What it does not own: Production behavior or verification of a caller's package by itself.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestPercentile_UnsortedInputPreservesCallerOrder$' -v -p 1 ./pkg/testutil/`

## Pitfalls here

- A percentile helper must not reorder its caller's sample slice. `load_harness_test.go::TestPercentile_UnsortedInputPreservesCallerOrder` checks this invariant.

## Never bring back

— none known

## Where decisions live

— none known
