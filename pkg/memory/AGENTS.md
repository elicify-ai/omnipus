# pkg/memory — session-core model-window types (the name is a misnomer)

Shared value types and scanner interfaces for a session's model context window
and its addressed archive. It holds NO storage code: the archive, the window
checkpoints and every durability rule live in `pkg/session`
(`archive_backend.go::archiveBackend`, `archive_day_store.go::ArchiveDayStore`).
NOT agent long-term memory, NOT the knowledge base, and NOT the SQLite
properties index (that is `pkg/records/propindex`). Sole production consumer:
`pkg/session` (and `pkg/agent` through it). Everything here is internal
persistence state, never a gateway wire type.

What is here (four files, nothing else):

- `archived_message.go::ArchivedMessage` (a `providers.Message` plus `TS`) and
  `EncodedLineBound` (8 MB, 0.8 x the 10 MB `maxLineSize`).
- `window.go::WindowState` (Skip/Count/AnchorLine/Projection, with `Clone`),
  `ArchiveSpan`, `ErrWindowChanged`.
- `projection.go::ProjectionState`, `ProjectionKey`, `ProjectionSet`,
  `ProjectionMeta`, `RecordAddress`.
- `archive_range.go::ArchiveRangeScanner` and `EvictedArchiveScanner`
  (implemented by `pkg/session/archive_backend.go::archiveBackend`).

## Running tests here

The only test file is the DEL-09 absence guard:

`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestSessionCoreConv_Del09MigrateFromJSONIsAbsent$' ./pkg/memory/`

Behaviour tests for everything these types describe live in `pkg/session`
(for example `archive_backend_test.go::TestArchiveBackend_RollbackIsNonDestructive`).

## The old JSONL store is gone — do not look for it

The JSONL store (`jsonl.go`/`store.go`: `Store`, `StoreWriter`, `JSONLStore`,
`Compact`, `SetHistory`, `ErrArchiveNotEmpty`) and `migration.go::MigrateFromJSON`
were deleted (session-core DEL-09 / DEL-12). Nothing replaces `Compact`: no
production path rewrites or shrinks an archive, and an agent "reclaiming disk"
must not invent one — evicted lines are what recall depends on (FR-005).
`pkg/session/unified_conv.go` (the one-time saved-chat cutover) is the sole
legacy reader. Guard: `sessioncore_conv_del09_test.go`.

## Rollback is non-destructive and lives in pkg/session

`pkg/session/archive_backend.go::archiveBackend.RollbackWindow` (reached through
`context_window.go::UnifiedStore.RollbackWindow`) is the SOLE rollback
primitive (session-core FR-006 / DEL-12). It restores the turn-start
cursor/anchor/projection AND records the appended ordinal span as an
`ArchiveSpan` in the backend meta's `Retracted` list — RETAINED on disk but
EXCLUDED from every provider request. The archive never shrinks; spans stay
exact because the archive is append-only and never renumbered. It restores
state even when it retracts nothing (`pkg/agent`'s `windowTrim` can advance
Skip mid-turn, and a no-op rollback must still undo that); the trim itself
lives in `pkg/agent` — see that package's CLAUDE.md.

`ErrWindowChanged` is the compare-and-set failure of
`archiveBackend.commitWindow`: the stored window no longer equals the caller's
`before` state.

## ProjectionKey is composite because tool_call_ids repeat

`projection.go::ProjectionKey`: providers reuse tool_call_ids (B-29b —
`call_0` every turn), so the archive-line ordinal is what makes the address
exact; keying by id alone silently addresses the wrong turn's result.
Entries written before `ProjectionCappedFailure` existed read back as
`ProjectionCapped`; archive messages without a timestamp unmarshal with
`TS == 0`, meaning "unknown/earlier", never an error.
