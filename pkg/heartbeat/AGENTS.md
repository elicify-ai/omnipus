# pkg/heartbeat — independent background drains

What it owns: Agent heartbeat work and the task/mailbox drain loops.
What it does not own: Task records, mailbox transport or Calendar recurrence entry.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestTaskDrainService_DispatchesIndependentOfHeartbeat$' -count=1 -v -p 1 ./pkg/heartbeat/`

## Pitfalls here

- Poll task drains independently of agent heartbeats. `task_drain_test.go::TestTaskDrainService_DispatchesIndependentOfHeartbeat` uses a counting checker and confirms it is called with no `HeartbeatService`; it creates no queued task and cannot prove delivery. Require a named `--- PASS` for the polling check.

## Never bring back

— none known

## Where decisions live

— none known
