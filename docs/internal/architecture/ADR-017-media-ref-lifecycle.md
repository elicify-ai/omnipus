# ADR-017 — media:// Ref Lifecycle and Store Invariants

**Status:** Accepted
**Date:** 2026-05-31
**Deciders:** backend-lead, architect

---

## Context

The media pipeline introduced in sprint #254 has several lifecycle invariants that must
be enforced to prevent stale refs, data races, and orphaned upload files. This ADR
documents the lifecycle contract and the code changes that enforce it.

### Problem areas identified in sprint #258 review

1. **Stale media store in HandleUpload** — `HandleUpload` captured `a.mediaStore` at
   construction time. `restartServices` swaps a new store via `al.SetMediaStore`. Uploads
   after a config reload were registered in the old (dead) store and invisible to the agent
   loop.

2. **Silent drops in resolveMediaRefs** — Unknown refs and stat failures produced only a
   debug Warn log with a `continue`. The LLM received no signal that an attachment was
   referenced but unavailable.

3. **Non-media:// strings forwarded verbatim** — `resolveMediaRefs` passed non-`media://`
   strings directly into the LLM content array. Channels that fall back to local paths
   (telegram, discord, onebot) could inadvertently inject file paths into the LLM.

4. **Data race on al.mediaStore / al.channelManager** — Both fields were written by
   `restartServices` on the config-watch goroutine and read on the hot turn goroutine
   without synchronization.

5. **Orphaned upload files** — `DeleteSession` and the retention sweep removed session
   JSONL files but left `uploads/<sessionID>/` on disk.

6. **Pending registry save lost on swap** — `restartServices` replaced `runningServices.MediaStore`
   without calling `Stop()` on the outgoing store, silently discarding any coalesced but
   not-yet-flushed registry writes.

---

## Decision

### D1 — Always resolve via agentLoop.GetMediaStore() (core invariant)

Every code path that needs the media store must call `al.GetMediaStore()` (or
`a.agentLoop.GetMediaStore()` from gateway handlers) at call time. Storing a reference
at construction time is forbidden. `GetMediaStore()` is thread-safe (backed by a
dedicated `sync.RWMutex`).

**Invariant:** Never cache a `MediaStore` reference beyond a single request/turn scope.

### D2 — Flush old store before swap

`restartServices` calls `oldFMS.Stop()` on the outgoing `*FileMediaStore` before
assigning a new one. `Stop()` flushes the pending debounced save synchronously so no
registry writes are lost across reloads.

### D3 — resolveMediaRefs validates media:// prefix (centralized guard)

`resolveMediaRefs` drops any string that does not parse as a valid `media://` ref (via
`media.ParseMediaRef`). This is the single centralized enforcement point; no channel or
handler needs its own guard. Unknown refs and missing files produce a visible
`[attachment unavailable: <name>]` marker in the LLM content so the model can report
them to the user.

### D4 — Lock-protected mediaStore and channelManager fields

`AgentLoop.mediaStore` and `AgentLoop.channelManager` are protected by dedicated
`sync.RWMutex` fields (`mediaStoreMu`, `channelManagerMu`). Setters hold the write lock;
getters hold the read lock. Hot turn paths snapshot both at the start of `runTurn`.

### D5 — Cascade-delete uploads on session delete

`UnifiedStore.DeleteSession`, `UnifiedStore.ClearAll`, and `RetentionSweep` cascade-delete
`<homePath>/uploads/<sessionID>/` when removing a session. Upload files are **home-rooted**
(`rest.go:4352` writes them at `<homePath>/uploads/<sessionID>/`), so all three delete
paths must use the home path regardless of the store's `baseDir` depth. Stores created
via `NewUnifiedStoreWithHome(baseDir, homePath)` carry the home path; `NewUnifiedStore`
derives it as `filepath.Dir(baseDir)` (correct for stores directly under home). Failure to
remove uploads is logged at Warn but never fails the operation.

### D6 — media.ParseMediaRef as the validation chokepoint

`media.ParseMediaRef(string) (Ref, error)` is the canonical constructor for validated
media:// refs. It rejects empty strings, non-prefixed strings, and refs with an empty ID
(bare `"media://"`). `media.FilterValidRefs([]string) []string` is a batch convenience
helper. The authoritative ingress chokepoints are:

- **WebSocket ingress** (`pkg/gateway/websocket.go`): calls `ParseMediaRef` per-ref and
  drops + counts (`inboundDropped`) any that fail validation before they reach the bus.
- **LLM pipeline** (`pkg/agent/loop_media.go::resolveMediaRefs`): calls `ParseMediaRef`
  per-ref and drops non-matching strings before the model ever sees them.

`FilterValidRefs` is available for callers that need batch filtering outside these two
paths. `InboundMessage.Media` remains `[]string` for backwards compatibility; `Ref` is
the validation type used at call-sites, not a data-model type.

---

## Consequences

- Upload registration after config reload works correctly.
- LLM content never contains raw file paths or HTTP URLs from channel fallback code.
- The race detector (`go test -race`) passes for `pkg/agent`.
- Deleted sessions leave no orphaned upload files on disk.
- Registry writes are never lost across reloads.
- `media.ParseMediaRef` / `media.FilterValidRefs` are the canonical ingress guards for
  all future channel implementations.
