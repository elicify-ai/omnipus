# pkg/agent — runtime kernel (turn loop)

The turn engine every workspace tab sits on. No screen owns it.

## Running tests here

This is the largest Go package in the repo. Never run it whole; scope to one
symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestSpawnSubTurn_TargetIdentity' ./pkg/agent/`). CI is the authority for
full-suite results.

Cite `file::symbol` in notes and reviews, never `file:line` — `loop.go`,
`turn.go` and `subturn.go` churn daily; every line number in older notes here
was stale within weeks.

## Size ceiling

`loop.go` and `loop_test.go` are pinned at their exact line counts in
`scripts/budgets/files.txt` — one appended line fails `make lint-budgets`.
New code belongs in a sibling file, not appended; a split re-keys the row
by hand (see that file's header).

## Delegation — a worker is a session steered by another session (ADR-091)

A worker is **not a special case.** It is a normal session that another
session (the steering session) launched through the `pkg/steer.SessionLauncher`
interface and recorded with one `LifecycleRecord` edge — the canonical parent
relationship. `pkg/steer` holds every published interface; `pkg/agent` supplies
the production implementations; the tools, channels, and gateway packages
inject them and cannot import `pkg/agent`.

The four implementation files:

- `pkg/agent/steer_launcher.go` — `SteerLauncher.Launch` writes the record and
  the mandatory fields (identity, ownership stamp, workspace, title and edge)
  atomically before any turn runs; `SteerLauncher.Dispatch` decides admission
  under one lock and returns the authoritative `running` / `queued` result.
- `pkg/agent/steer_audience.go` — `SteerAudienceResolver.Audience` answers
  "who is this session's audience?" at every publication boundary, so a
  steered session can never reach a user-facing address by accident. The
  companion `SteerUpwardDeliverer.Deliver` is the only upward path from a
  child to its parent.
- `pkg/agent/steer_cancel.go` — `SteerCanceller.CancelSubtree` walks the
  durable edge, stamps a Stop marker on every reachable non-terminal
  descendant under the node's cascade lock, and cancels each live turn with
  the stamped generation; `SteerCanceller.Revive` increments a stopped
  session's generation under the same record lock so a newer instruction can
  bring it back.
- `pkg/agent/steer_reconstruct.go` — `reconstructSteeredTurn` rebuilds a
  steered session's `turnState` from its record on every entry path (first
  run, wake, follow-up, boot). Identity is never per-path.

The `subturn.go` ring, borrowed `Channel`/`ChatID`, wait-inline
(`async:false`), the `ParentDurableKey` field, and every per-site
"is this a delegate?" boolean are gone in the same delivery. Self-target is
allowed for both delegation and tasks; recursion is bounded by the concurrency
and depth caps that already exist. There is no second memory, no second
identity, and no second path to the parent — the test suite asserts this on a
three-level delegation.

## Retired surfaces — resolve merges by keeping the deletion

- **Delegation Graph / `AgentConfig.DelegationPolicy`** (ADR-037): deleted. The
  per-workspace `Delegation[]` edge list (`pkg/workspace/delegation.go`) is the
  sole runtime authority. `PUT /api/v1/agents/{id}` 400s on a
  `delegation_policy` field; `coreagent.SeedDelegationEdges` is a bootstrap
  seed DTO only — never persisted, never on the wire.
- **Goal confirm-gate machinery** (ADR-088): goals activate instantly, the
  working agent authors the record via `set_goal`, steering replaces
  confirmation. Guard: `scripts/check-no-goal-confirm-gate.sh`.
- **Orphaned-foreground-turn watchdog** (ADR-082): a turn never depends on a UI
  connection; only explicit Stop/cancel (`RequestCancel`,
  `InterruptSessionHard`) ends a turn early. Guard:
  `scripts/check-no-orphan-turn-watchdog.sh`. Two unrelated mechanisms that
  also say "orphan" are KEPT: the subagent-span forwarder watchdog
  (`websocket.go::startOrphanWatchdog`) and `SubTurnOrphan`.

## Context compaction

`windowTrim` is the ONLY compaction path: evicts the oldest whole turn(s) on a
token budget, zero LLM calls, deletes nothing on disk. The legacy LLM
summariser (`maybeSummarize`, `summarizeSession`, `forceCompression`) is
deleted — `window_trim_test.go` asserts those methods are never redefined.
The sliding window is the authoritative history.
