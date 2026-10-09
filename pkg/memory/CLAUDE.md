# pkg/memory — session-transcript store (the name is a misnomer)

Persistent per-session transcript storage: an append-only `{key}.jsonl`
archive plus a `{key}.meta.json` sidecar (Skip/Count/Projection/Hydrated).
NOT agent long-term memory, NOT the knowledge base, and NOT the SQLite
properties index (that is `pkg/records/propindex`). Interfaces:
`store.go::Store`; sole production consumer: `pkg/session`'s
`jsonl_backend.go::JSONLBackend`.

## Running tests here

`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestCrashRecovery_PartialLine$' ./pkg/memory/`

## Compact is production-forbidden

`jsonl.go::Compact` MUST NOT be called from any Save path — it destroys
the recall archive (FR-005). Test-only; there is no production caller. An
agent "reclaiming disk" through Compact permanently deletes every evicted
turn that recall depends on. Same warning on `store.go::StoreWriter`.

## SetHistory is hydration-only; the undo paths are RollbackWindow (abort) and RollbackAppended (truncation)

- `SetHistory` refuses any archive with ≥1 line
  (`projection.go::ErrArchiveNotEmpty`) and never touches Skip.
- That refusal is load-bearing: a first-fill caller treats
  `errors.Is(err, ErrArchiveNotEmpty)` as its already-imported signal (the
  deleted `migration.go::MigrateFromJSON` relied on exactly this).
- `window.go::RollbackWindow` is the abort-path undo (session-core FR-006 /
  DEL-12). It restores turn-start window metadata (Skip/count/anchor/source
  limits) and records the aborted span as a RETAINED-BUT-EXCLUDED effect
  (`sessionMeta.Retracted`). It NEVER rewrites the archive: the bytes stay
  on disk for recall, and `WindowHistory` omits the span from the model view.
  Because the archive is append-only, a later valid append lands above the
  span and is visible again.
- `jsonl.go::RollbackAppended` is a SEPARATE physical-truncation primitive
  (rewrite to the first `targetLines` lines + Skip restore in one write). It
  restores state even when it rewrites no bytes (pkg/agent's `windowTrim` can
  advance Skip mid-turn, and a no-op file must still undo that). pkg/agent's
  abort path does NOT call it — it uses RollbackWindow.
- A SetHistory-based rollback would reset Skip to 0, permanently deleting
  evicted turns (SC-001) — so neither undo path may use it.

## Durability contract

- Appends: striped per-session lock (`task.StripedLock`, 64 shards) +
  `O_APPEND` + fsync per line, then one meta update.
- Meta writes and full rewrites go through `fileutil.WriteFileAtomic`
  (temp + fsync + rename).
- Crash ordering: meta is written BEFORE file rewrites in
  RollbackAppended/SetHistory/Compact, so the failure direction is always
  "too many messages", never data loss; Skip paths reconcile
  `meta.Count` against the real line count (`countLines`).
- Malformed lines (partial crash writes) are logged and skipped, never
  fatal — `readMessages` and `ScanArchive` keep index parity. Scanner cap
  10 MB; the exported `jsonl.go::EncodedLineBound` (8 MB) is the upstream
  choke point.

## ProjectionKey is composite because tool_call_ids repeat

`projection.go::ProjectionKey`: providers reuse tool_call_ids (B-29b —
`call_0` every turn), so the archive line index is what makes the address
exact; keying by id alone silently addresses the wrong turn's result.
Pre-`capped_failure` entries read back as `ProjectionCapped`; legacy
archive lines unmarshal with `TS == 0`, meaning "unknown/earlier", never
an error.

## migration.go was deleted (session-core DEL-09)

`migration.go` and its `MigrateFromJSON` general importer are gone. CONV
(`pkg/session/unified_conv.go`, the one-time saved-chat cutover) is now the
sole legacy reader; the `pkg/agent/instance.go` startup path no longer calls
`MigrateFromJSON`. `SetHistory` still refuses a non-empty archive exactly as
the deleted importer relied on, and that refusal remains load-bearing for any
other first-fill caller.
