# Design: knowledge-base indexing must survive a config reload

**Date:** 2026-08-24
**Branch:** `feat/library-improvements`
**Status:** Proposed — not yet implemented
**Tracks:** [#647](https://github.com/elicify-ai/omnipus/issues/647)

## Problem, stated plainly

Any config reload — onboarding completion is the common one, but any settings
change that triggers `POST /reload` qualifies — permanently and silently turns
off knowledge-base indexing for the rest of that process's life. A mount
created after the first reload gets a normal 201 response and never gets
indexed. There is no error, no log line, and no user-visible state anywhere
that says this happened. The only recovery is restarting the whole gateway
process.

This was found and reproduced live during manual testing (see #647): a
synthetic collection mounted before a reload indexed in ~3 seconds; an
identical collection mounted immediately after the same reload never indexed,
with a 30-second wait and a delete/recreate cycle both producing the same
silent nothing.

## Root cause

Two functions disagree about who owns the knowledge lifecycle's lifetime.

**`stopAndCleanupServices`** (`pkg/gateway/gateway.go:4952`) runs on both a
real shutdown and a reload (it takes an `isReload bool` precisely to tell
those apart), and it already treats most services differently depending on
which one is happening. The line directly above the knowledge call is the
model for how that's supposed to look:

```go
// reload should not stop channel manager
if !isReload && runningServices.ChannelManager != nil {
    runningServices.ChannelManager.StopAll(shutdownCtx)
}
...
stopKnowledgeLifecycles()
```

`stopKnowledgeLifecycles()` has no `isReload` guard. It runs unconditionally,
closing every open collection index and stopping every drift schedule,
whether this is a real shutdown or a config reload.

**`restartServices`** (`pkg/gateway/gateway.go:5106`) is what actually rebuilds
the world after a reload's `stopAndCleanupServices(..., isReload=true)` call.
It has zero references to "knowledge" anywhere in its ~260 lines. Every other
long-lived background service it's responsible for gets recreated here
(`CronService`, `TaskTrigger`, `MediaStore`, `DeviceService`, ...). The
knowledge lifecycle is not one of them.

`startKnowledgeLifecycle` — the only function that can put a lifecycle back
into the process-wide registry `knowledgeLifecycles.byHome[homePath]` — has
exactly one production call site, inside `setupAndStartServices`
(`gateway.go:4512`), which runs at **boot only**.

So: reload stops it, nothing restarts it, and every REST/mount handler that
asks for it afterward (`a.knowledgeLifecycle()`, `gateway.go:1052`) gets `nil`
for the rest of the process's life. `AttachMountAsync`'s nil-receiver guard —
correct and necessary for test harnesses that don't wire up a lifecycle at
all — then makes the mount-create handler's call into a silent no-op in a
context where a lifecycle unambiguously should exist.

This is the same shape of bug `restart_services_homepath_test.go` was written
to catch for two *other* services (the notification store's and the loop
scheduler's home-path derivation, and session-messaging's inbox caps) — a
service that behaves correctly at boot silently misbehaving after the first
reload, because `restartServices` didn't fully replay what boot did. It's a
recurring failure class in this codebase, not a one-off.

## Why the fix is safe to be simple

The two things a design like this needs to check before picking "just don't
stop it" are: does anything reload might change actually invalidate the
lifecycle's dependencies, and does skipping the stop leak anything real
shutdown needs to clean up.

Checked both, from the actual reload code path:

- **`agentLoop` is not replaced on reload.** `handleConfigReload` calls
  `al.ReloadProviderAndConfig(reloadCtx, newProvider, newCfg)` — the same
  `*agent.AgentLoop` instance is reused; only its internal config/provider
  state changes. The knowledge lifecycle's drift notifier closure captured
  `agentLoop.GetConfig` at boot (`gateway.go:4513`, via
  `knowledgeDriftNotifier(runningServices.notifStore, agentLoop,
  agentLoop.GetConfig)`), and that method reads whatever config is *current*
  on `al` — so it already sees a reloaded config live, with no reconstruction
  needed.
- **`wsHandler` is not touched anywhere in `handleConfigReload` or
  `restartServices`.** It's a stable, boot-once object; reload has no reason
  to recreate it, and doesn't.
- **`homePath` cannot change across a reload.** `$OMNIPUS_HOME` is fixed for
  the process's lifetime.

None of the three arguments `startKnowledgeLifecycle` needs
(`homePath, wsHandler, driftNotify`) go stale across a reload. There is
nothing to "pick up fresh" — the lifecycle just needs to keep existing.

That rules out the more invasive option (stop-and-restart-with-fresh-wiring
inside `restartServices`) in favor of the same one-line pattern the channel
manager already uses.

## Design options considered

**A — Guard the stop, mirror the channel-manager precedent (recommended).**
Change `stopKnowledgeLifecycles()`'s call site to `if !isReload {
stopKnowledgeLifecycles() }`. Reload stops touching the knowledge lifecycle at
all; it keeps running with the same dependencies it was given at boot, exactly
as `ChannelManager` already does one line above it.

- *For:* smallest possible change, follows an existing precedent in the same
  function, zero new wiring, nothing to keep in sync between two call sites.
- *Against:* if a future config knob genuinely needs to reach the knowledge
  lifecycle on reload (the code already has a TODO for a configurable drift
  interval — `gateway.go:4510`, *"there is no config key for it yet"*), this
  option doesn't pick that up until the next full restart. Acceptable today
  because no such knob exists yet; worth a one-line comment flagging it for
  whoever adds one.

**B — Stop and restart it inside `restartServices`, with fresh config.**
Leave `stopKnowledgeLifecycles()` unconditional, and add a
`startKnowledgeLifecycle` call into `restartServices` so reload rebuilds it
the same way boot does.

- *For:* automatically future-proof against a config value the lifecycle
  needs to pick up live (e.g. a drift-interval setting), with no extra
  guard to remember later.
- *Against:* every currently-open collection index gets closed and reopened
  on every reload, for no benefit today — reload becomes a full,
  unnecessary re-open of every mounted knowledge base, and (per FR-039's
  "reopen-don't-rebuild" contract) reopening is supposed to be a lightweight
  incremental reconcile rather than free, but it's still needless churn a
  config change having nothing to do with knowledge bases shouldn't trigger.
  It also duplicates wiring: two call sites (`setupAndStartServices` and
  `restartServices`) that must stay in sync, which is exactly the kind of
  divergence `restart_services_homepath_test.go`'s own history warns about.

**Recommendation: A.** It's the smaller, more precise change, it matches an
existing pattern in the exact same function, and nothing currently reachable
from reload needs the lifecycle to be rebuilt. Leave a comment noting that a
future drift-interval config knob is the one thing that would justify
revisiting this — so option B's argument isn't silently lost, just deferred
to when it's actually needed.

## Implementation plan

1. **`pkg/gateway/gateway.go`** — in `stopAndCleanupServices`, guard the
   `stopKnowledgeLifecycles()` call behind `!isReload`, with a comment
   parallel to the channel-manager one directly above it (*"reload should not
   stop the knowledge lifecycle — see docs/internal/design/
   knowledge-lifecycle-reload-survival-2026-08-24.md"*). One line changed,
   one comment added.

2. **Regression test** — extend `pkg/gateway/knowledge_realboot_wiring_test.go`,
   which already drives real `setupAndStartServices` boot and real REST
   handlers (unlike the lifecycle's own unit tests, which construct
   `KnowledgeLifecycle` directly and can't see this class of bug — that gap is
   exactly what this test file exists to close, per its own header). Add a
   new test alongside `TestRealBoot_ShutdownReleasesEveryKnowledgeCollection`:

   - Boot for real against a temp `$OMNIPUS_HOME`.
   - Drive the real reload path (`stopAndCleanupServices(...,
     isReload=true)` + `restartServices`, or `handleConfigReload` directly if
     that's a cleaner seam — check which is already exercised by
     `restart_services_homepath_test.go` and match it).
   - Create a mount through the real REST handler, same as
     `TestKnowledgeMountHandlers_AttachOnCreateAndReleaseOnDelete` already
     does.
   - Assert indexing actually completes: poll for the collection's search
     endpoint (or the manifest file, matching how `pkg/knowledge`'s own tests
     assert completion) to report `complete: true`, not just that the mount
     handler returned 201. The 647 investigation's own mistake worth avoiding
     in the test: a 201 from mount-create proves nothing about indexing —
     that's exactly the gap that let this ship unnoticed.
   - Name it something that states the property, not the mechanism, matching
     this file's convention — e.g.
     `TestRealBoot_MountCreatedAfterReloadStillIndexes`.

3. **Mutation-verify the new test**, per this project's standing practice:
   revert the fix, confirm the new test fails; reapply it, confirm it passes.
   Also confirm `TestRealBoot_ShutdownReleasesEveryKnowledgeCollection` still
   passes unmodified — the guard must not weaken real-shutdown cleanup.

4. **Manual confirmation** — rerun the exact repro from #647 (mount, reload,
   mount again, search) against a build with the fix, both with a synthetic
   collection and with a real vault, and record the result on the issue.

## What this does *not* fix

`AttachMount`'s error path is still the only signal an operator has that
indexing failed — this design closes the specific hole where nothing ever
reaches that path at all after a reload, but the broader "an attach failure
is invisible unless it happens to log" pattern is unchanged. Worth a separate
look if this class of bug recurs elsewhere, but out of scope here: this
design fixes the reload defect, not the general observability gap.
