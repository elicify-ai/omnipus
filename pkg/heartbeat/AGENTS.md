# pkg/heartbeat — independent background drains

What it owns: Agent heartbeat work and the task/mailbox drain loops.
What it does not own: Task records, mailbox transport or Calendar recurrence entry.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestTaskDrainService_DispatchesIndependentOfHeartbeat$' -v -p 1 ./pkg/heartbeat/`

## Pitfalls here

- Queued tasks must drain even if an agent heartbeat is disabled or distant. `task_drain_test.go::TestTaskDrainService_DispatchesIndependentOfHeartbeat` checks this separation.

## Never bring back

— none known

## Where decisions live

— none known
