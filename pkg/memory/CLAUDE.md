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

## SetHistory is hydration-only; RollbackAppended is the only undo

- `SetHistory` refuses any archive with ≥1 line
  (`projection.go::ErrArchiveNotEmpty`) and never touches Skip.
- That refusal is load-bearing: `migration.go::MigrateFromJSON` treats
  `errors.Is(err, ErrArchiveNotEmpty)` as its already-imported signal.
- `jsonl.go::RollbackAppended` is the ONLY correct way to undo turn
  appends — a SetHistory-based rollback would reset Skip to 0,
  permanently deleting evicted turns (SC-001).
- `RollbackAppended` restores state even when it rewrites no bytes:
  pkg/agent's `windowTrim` can advance Skip mid-turn, and a no-op file
  must still undo that. This is pkg/memory's only relationship to
  windowTrim — the function itself lives in pkg/agent; see that package's
  CLAUDE.md.

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

## migration.go has a live caller

`pkg/agent/instance.go`'s startup path calls `MigrateFromJSON` (legacy
single-file `sessions/*.json` snapshots → JSONL, one atomic SetHistory,
sources renamed `.json.migrated`). Not dead code under the greenfield
ruling — deleting it breaks first boot on installs carrying legacy files.
