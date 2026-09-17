# pkg/session — session store, lifecycle records, retention

UnifiedStore (transcripts + meta), LifecycleStore, MessageInboxStore,
day-partitioned transcript files, and the retention sweep. Interfaces in
`session_store.go`; the ADR-057 unified surface is `unified_api.go`.

## Running tests here

`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestStripedSessionLock_ShardIsolation$' ./pkg/session/`

## Two independent 64-shard lock pools — and a one-directional order

Two pool CLASSES with separate state, each 64 shards of FNV-32a keyed
mutexes: the session pool (`unified_lock.go::u4SessionStripedLock`, held by
`unified.go::UnifiedStore`) and the lifecycle pool
(`lifecycle_lock.go::lifecycleStripedLock`, instantiated by BOTH
`lifecycle.go::NewLifecycleStore` and
`message_inbox.go::NewMessageInboxStore`). The session pool's shape was
copied VERBATIM from the lifecycle pool — do not reinvent the hashing
scheme.

Lock order (FR-050, stated in `unified_lock.go`'s package doc):
**sessionLock(id) → cacheMu, one-directional** — never the reverse, and
`cacheMu` is never held across an `os.*`/`fileutil.*` call (FR-049). The
wider order (ADR-086) is goalLock(goalID) → sessionLock(sessionID) →
cacheMu. `unified.go::GetMeta`'s fast path takes `cacheMu` and the shard
sequentially, not nested — that does not invert the rule.

## The exactly-two two-shard exceptions

- `unified_lock.go::lockAllSessionShards` — ClearAll and RetentionSweep
  hold EVERY shard at once, in ascending INDEX (not hash) order; an
  accepted full-store stall (FR-050(a)). Nothing inside that hold may call
  into a goal store, in either direction.
- `unified_api.go::CreateSessionWithID`'s parent-Owner copy (FR-082) —
  reads parent meta under lockSession(parentID), releases COMPLETELY, then
  locks the child shard: a sequential protocol that never holds two shards
  at once. Enforced by caller discipline, not the type system.
  Regression: `TestCreateSessionWithID_NeverHoldsTwoSessionShards`.

## Durability — Save is a no-op; the sweep is the sole deleter

`jsonl_backend.go::Save` returns nil unconditionally: the wrapped
`memory.JSONLStore` fsyncs every write, and evicted (skipped) lines MUST
stay on disk for recall. The retention sweep is the only legitimate
deleter of transcript content; goal retention runs as a separate pass
AFTER the shard hold is released (a swappable hook, so pkg/session never
imports pkg/goal — that would be an import cycle).

## Two id namespaces, compile-enforced

`ids.go::SessionID` vs `ids.go::RoutingSessionID`: the routing id equals
the ROOT's SessionID and must never resolve store-backed state for anyone
but the root (this confusion was bugs #576/#577). `CreateSessionWithID`
refuses a colliding caller-supplied id loudly (FR-096). Scheduled-session
ids are reserved conventions (`sched-main-<owner>`), not ULIDs.

## A meta.json that fails to parse stays invisible

`unified.go::UnifiedStore`'s cacheLoadFailures: a session whose meta fails
to load once is excluded from metaCache — and therefore from
ListSessions — for the entire process lifetime; it does not self-correct
without a restart. Observable only via CacheLoadFailureCount.
