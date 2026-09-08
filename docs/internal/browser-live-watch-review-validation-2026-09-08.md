# Live attachment and death-watch review corrections

Base: `7bfcb05e4`. This is a focused review correction, not a complete browser/runtime validation.

## Reproduced behavior

The consolidated baseline (retained process handle `13698`) exited 1 in 18.258 seconds. It selected 18 top-level tests.

- A fresh real `LiveViewRegistry.AttachContext` followed immediately by the first different-tab switch never delivered a measured, qualified recapture. The fixture did not prime the active-target baseline with an extra metadata notification.
- A controlled pause at the actual `BrowserManager.browserAlive` context boundary let the old death watcher race with replacement. Four cases stopped a protected capture: replacement watch plus capture, pending capture on the same watch, a newer generation of the same capture, and a pending capture already installed before the death check. The latter three also emitted a false death notification. The genuine current-death control passed.
- Four migrated switch tests reproduced the same missing initial active-target baseline. The remaining selected controls passed.

The prior standalone fresh-switch attempt used a relay without the required context-bound ingest capability and failed during fixture setup. That attempt is not evidence of the product defect; the corrected consolidated baseline above is.

## Change and ownership

Fresh attachment now records its current active target before starting the death watch. Cleanup retains the original capture pointer and measured frame before checking browser death. A separate cancelable watch-ownership context is retired on a new attachment, rebind, or final detach, including when the watched tab is already dead. `CaptureSession.stopWhen` admits destructive cleanup only while that ownership and the original frame generation remain current. An unmeasured replacement capture has not claimed the dead picture and is preserved.

The stop predicate reads only the independent context and capture state already protected by `CaptureSession.mu`. It does not acquire `LiveView.mu` or the manager's capture-map lock. This avoids the inverse of capture installation, which may hold the map lock while acquiring capture state. All transport draining and status callbacks remain outside the live-view lock.

The migrated fixtures use actual manager/registry callbacks and a qualified ingest callback with exact panel ownership. Browser execution is substituted at the existing CDP action boundary; geometry is independently specified there. Tests compare browser target/executor identity, not operation-context pointer identity. Burst and viewport fixtures now carry actual target IDs and answer the current measured-frame action.

## Verification

The two exact old-defect faults were both caught: removing the attachment baseline failed the fresh-switch callback oracle, and restoring the old watcher failed the protected-capture oracles. The driver (`33655`) restored production source automatically before its final race/shuffle selection.

That selection recorded 23 passing test/subtest entries and two fixture failures (12.270 seconds): the ordering recorder counted the old tab’s focus release as a foreground action, and `require.NotEqual` recursively inspected mutable context internals, producing a test-only race. The latter trace originated in reflection at the assertion while ordinary context cancellation updated its internal state. Neither required a production change. The corrected oracles record actual `Page.bringToFront` actions and compare context identity directly.

Only those two corrected tests were rerun. Retained handle `43666` exited 0 in 10.024 seconds under race/shuffle, with two passes, no skips, and no race warnings. The other passing tests and the already-caught faults were not repeated. All selected behavior now has passing focused evidence; no full package or CI suite was run for this closeout.

Manual caller inspection found attachment through `live_attach_context.go`, death-watch creation in attach/rebind, and detach from registry detach or failed attachment cleanup. The commit changes only `live.go`, its assigned test fixtures, the new regression file, and this evidence record. Git diff and whitespace checks were used; no GitNexus tool was called after the user instructed us to stop using it.

No installed browser, user profile, or production process was modified. Full release-base review, integrated CI, and real application measurements remain separate parent-owned checks.
