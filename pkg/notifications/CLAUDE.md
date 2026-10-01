# pkg/notifications — per-user notifications

What it owns: File-based recipient records, retention and deduplication of repeated failures.
What it does not own: Scheduled job execution or the SPA notification display.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCreate_CoalescesByCoalesceKey$' -v -p 1 ./pkg/notifications/`

## Pitfalls here

- Repeated failures for one schedule should not flood the recipient. `store_test.go::TestCreate_CoalescesByCoalesceKey` checks deduplication.

## Never bring back

— none known

## Where decisions live

— none known
