# pkg/session — session store, lifecycle records, retention

UnifiedStore (transcripts + meta), LifecycleStore, MessageInboxStore,
day-partitioned transcript files, and the retention sweep. Interfaces in
`session_store.go`; the ADR-057 unified surface is `unified_api.go`.

## Running tests here

`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestStripedSessionLock_ShardIsolation$' ./pkg/session/`

## Three lock classes: two independent shard pools and a history owner

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

### Per-session history owner — not another shard pool

The third class is `chatHistoryOwner`, obtained through `historyOwner` for one
canonical session. `reconcileChatHistoryIndex` holds `owner.mu` across bounded
reconciliation I/O. It briefly takes the session shard and archive append mutex
to capture source ends, releases them for content I/O, and takes the shard again
for root publication. Do not describe this owner as an I/O-free mutex.

| Boundary | Required discipline |
|---|---|
| Blocking reconciliation | `owner.mu` precedes the session shard; capture can then take the archive append mutex. The existing shard-to-`cacheMu` order still applies. |
| Canonical append | `prepareHistoryAppend` uses `TryLock` before the shard. `historyAccepted` and `historyDispositionAccepted` also use `TryLock`, never queueing behind a recoverer; callbacks run after the archive append mutex is released. A skipped derived update remains a bounded suffix, not a failed canonical save. |
| Reverse nesting | Never acquire `owner.mu` with a blocking lock while holding a session shard. A writer's nonblocking `TryLock` is the deliberate exception, not permission to introduce the reverse blocking order. |
| Lifecycle shards | These remain a different pool. History reconciliation does not acquire them; no history-owner/lifecycle-shard nesting order is established. Do not introduce such nesting merely because both use session identifiers. |
| Generation pins | The owner's separate `refsMu` guards pin/retirement bookkeeping. A selected generation stays pinned through tree/content use; close the handle before releasing its pin, including on errors or panic. The release callback returns cleanup errors; surface them rather than hiding them in reselect. Failed retirement stays pending and normal reconciliation retries it, including zero-mutation reads. Reader body I/O holds neither shard nor archive append mutex. |

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_index.go::reconcileChatHistoryIndex`,
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_reconcile.go::reconcileHistoryOwned`,
and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::chatHistoryOwner`.

## One all-shard hold, one sequential parent-then-child protocol

Nothing else may hold two session shards at once.

- `unified_lock.go::lockAllSessionShards` — ClearAll and RetentionSweep
  hold EVERY shard at once, in ascending INDEX (not hash) order; an
  accepted full-store stall (FR-050(a)). Nothing inside that hold may call
  into a goal store, in either direction.
- `unified_api.go::CreateSessionWithID`'s parent-Owner copy (FR-082) —
  reads parent meta under lockSession(parentID), releases COMPLETELY, then
  locks the child shard. Sequential, never nested. Enforced by caller
  discipline, not the type system. Regression:
  `TestCreateSessionWithID_NeverHoldsTwoSessionShards`.

## Durability — Save is a no-op; the addressed archive is append-only

`archive_backend.go::archiveBackend.Save` and `unified_write.go::UnifiedStore.Save`
return nil unconditionally: every append already fsyncs, and evicted (skipped)
lines MUST stay on disk for recall.

- `archive_day_store.go::ArchiveDayStore.Append` validates the envelope, refuses
  one over `memory.EncodedLineBound` (8 MB) before writing anything, then
  appends under a per-store mutex with `O_APPEND`, newline framing and
  `f.Sync()`. A failed write or fsync truncates back to the pre-append size so
  unpublished bytes never linger; a torn final line gets a newline first.
  `NewArchiveDayStore` refuses an empty session id, `.`, `..` or any id holding a
  path separator.
- Backend state (`backend_meta.json`: cursor, anchor, retracted spans, projection)
  is written to a temp file and renamed by `archiveBackend.saveMetaLocked`; it is
  NOT fsynced.
- The ordinal index (`ordinal_index.go`) fsyncs its row log; its offsets table is
  derived and rebuilt from the rows when it disagrees.
- `archive_scan.go::ArchiveDayStore.ScanAll` skips a corrupt line without
  consuming an ordinal, so ordinals never shift under damage.
- Rollback never shrinks the archive: `archiveBackend.RollbackWindow` records the
  aborted span as retained-but-excluded (types: `pkg/memory`).
- The retention sweep (`retention_sweep.go::RetentionSweep`) removes aged-out
  session directories; goal retention runs as a separate pass AFTER the shard
  hold is released (a swappable hook, so pkg/session never imports pkg/goal —
  that would be an import cycle). Rolled-partition expiry has a real caller:
  `expireArchiveSessions` invokes `archiveBackend.expireSession`, which calls
  `ArchiveDayStore.SweepExpired` and repairs the model-window cursor. After
  releasing all session shards, the sweep calls `reconcileExistingChatHistory`
  even on a zero-removal pass, then records expiry notices and runs goal retention.
  Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/archive_expiry.go::expireSession`
  and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/retention_sweep.go::RetentionSweep`.

## Paged chat history and derived publication

`ReadChatHistoryPage` is the bounded display reader, not a wrapper around a
lifetime `ReadTranscript`/`ReadTranscriptView` scan. Whole source groups are
selected at an immutable cut; selected content and dependencies are read after
capture locks are released. The gateway must supply the real carrier weigher;
there is no guessed default weight in the session package.

| Invariant | Rule |
|---|---|
| One content authority | Canonical append-only archive bytes hold content. The `.chat-history` index is derived, content-free occurrence/correction metadata, not another transcript. |
| Publication order | Fsync canonical acceptance first, then derived copy-on-write node frames and transaction footer, then publish the checkpoint/root. An index failure must not relabel an accepted canonical save as unsaved. |
| New identity | Publish the verified empty baseline before making a new session identity readable. Never infer an empty conversation from missing or inconsistent derived state. |
| Online recovery | Reconcile at most 64 combined archive/disposition/deletion facts from a valid checkpoint; the 65th refuses (`catchup_limit`). Do not retry-walk a larger gap in batches. Derived-index damage (published root/tail) is checked before collection and refuses as `index_inconsistent` whatever the lag. |
| Demand self-heal (#1470) | Only demand paths (`CheckChatHistoryReadiness`, `readChatHistoryPage` via `reconcileChatHistoryIndexOnDemand`) may rebuild ONE session's index, and only on a pure `catchup_limit`; boot and the retention sweep use the non-healing `reconcileChatHistoryIndex`. `chat_history_heal.go::healChatHistoryOwned` holds `owner.mu`, takes the process-wide `chatHistoryHealSlot`, refuses a source over 32 MiB, builds with the demand options (strict existing-index proof; `source_changed` only on proven source movement) under a detached 30 s context, retires through the runtime owner in the store's path spelling, and proves readiness with one bounded reconcile. Never build a baseline for a missing index or heal any `index_inconsistent` cause. |
| Retention | Preserve only opaque token/rank tombstones for expired occurrences. Rich generation retirement waits for old reader pins; genuine Windows sharing-busy is not proof that a partition disappeared. |
| Offline repair | The explicit `PrepareChatHistoryIndex` builder and shipped repair command share the reducer; the demand self-heal reuses the same builder in-process. None of them is an application-startup or ordinary-send fallback, and they must not invoke the gateway/projector/model to reconstruct the index. |

Sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_select.go::readChatHistoryPage`
and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-green-be/pkg/session/chat_history_storage.go::historyPublish`.

## Two id namespaces, compile-enforced

`ids.go::SessionID` vs `ids.go::RoutingSessionID`: the routing id equals
the ROOT's SessionID and must never resolve store-backed state for anyone
but the root (this confusion was bugs #576/#577). `CreateSessionWithID`
refuses a colliding caller-supplied id loudly (FR-096). Scheduled-session
ids are ULIDs (`NewScheduledSession`).

A MAIN session's id is COMPUTED, never minted or stored:
`main_session.go::MainSessionID` joins the pair as
`main-session-<workspace>+<agent>` — the `+` cannot occur in either validated
component, so two distinct pairs can never collide on one id. Each component
keeps its own 128-byte bound; the computed id is capped at 255 UTF-8 bytes
(the directory-name limit) and above it the store REFUSES with a visible
error, storing nothing — no hash, truncation or replacement id.
`unified.go::GetOrCreateMainSession` is the only creator, and it treats a
read error on that id as the BDD-01.4 refusal, NOT as a cache miss: only a
genuinely absent record creates. A stored record whose owner, workspace or
type does not match the pair is refused untouched, never adopted or repaired.

## A meta.json that fails to parse stays invisible

`unified.go::UnifiedStore`'s cacheLoadFailures: a session whose meta fails
to load once is excluded from metaCache — and therefore from
ListSessions — for the entire process lifetime; it does not self-correct
without a restart. Observable only via CacheLoadFailureCount.
