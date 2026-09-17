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

## Delegation identity — never inherit agent-level settings from the parent

`subturn.go::spawnSubTurn` sources every agent-level setting (ID, Name,
Workspace, ContextBuilder, Tools, tool policy, AgentType, and the
Model/Provider/Candidates/ProviderPool quad) from `execSource` — the resolved
delegate, or the parent for self-delegation — with no per-field exception. The
mutex-protected quad is read from the SAME source under one `RLock`; mixing
parent and target fields was itself an identity-inheritance bug once. Do not
reintroduce a "keep some fields parent-sourced" exception. Regression:
`subturn_target_identity_test.go`.

The one REQUIRED parent-to-child assignment is
`childTS.routingSessionID = parentTS.routingSessionID` (ADR-057 D2) — the
cancel/interrupt reachability key. It pattern-matches the prohibited shape and
is not covered by it: delete it and chat-wide Stop silently stops reaching
delegated sub-turns, no error, no obvious test failure. Full contract: the
`routingSessionID` field's doc comment in `turn.go`.

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
