package media

import (
	"errors"
	"testing"
	"time"
)

// The missing-successor repair (#1685 SFH-3 fix) forwards a complete
// registration, under the scope the pin carries, to a successor whose copy of
// the ref is gone. Its contract is the send-file guarantee in
// docs/using-omnipus-ui.md: a sent file does not expire, so its card and
// download keep working after a reload — present is not enough, the live
// store must hold the ref PINNED (SFH-4: a repair that registers under an
// unpinned upload scope — the storeRef duplicate branch that protects
// handover refcounts — leaves the sent file expiring with its upload).
//
// A pending upload forward reaches the same repair deterministically: the
// outgoing store holds the upload but the successor does not, so the repair
// runs without a concurrent schedule. This checks M16's pin postcondition,
// not the SFH-4 late-forward interleaving. An unsent control upload proves
// the cleanup used below can expire entries at all.
func TestPinRef_MissingSuccessorRepairRegistersPinned(t *testing.T) {
	mediaDir := isolatedRegistryHome(t)
	sentPath := writePayload(t, mediaDir, "repair-pin-plan.md")
	controlPath := writePayload(t, mediaDir, "repair-pin-control.md")
	meta := MediaMeta{
		Filename: "repair-pin-plan.md", ContentType: "text/markdown",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}

	oldStore := NewFileMediaStoreWithCleanup(MediaCleanerConfig{MaxAge: time.Second, Interval: time.Hour})
	liveStore := NewFileMediaStoreWithCleanup(MediaCleanerConfig{MaxAge: time.Second, Interval: time.Hour})
	parkDebounceSave(oldStore)
	parkDebounceSave(liveStore)
	t.Cleanup(oldStore.Stop)
	t.Cleanup(liveStore.Stop)

	sentScope := SentFileScopePrefix + "chat:repair-pin"
	ref := "media://repair-pin-1"
	controlRef := "media://repair-pin-control-1"
	controlMeta := MediaMeta{
		Filename: "repair-pin-control.md", ContentType: "text/plain",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}

	// The swap: the successor copies the outgoing maps, which do not hold the
	// not-yet-registered upload.
	oldStore.HandoverTo(liveStore)
	// Expiry control on the live store: an unpinned upload the same cleanup
	// must remove, so the sent-file assertion cannot pass because the
	// cleanup simply never ran.
	liveStore.storeRef(controlRef, controlPath, controlMeta, "upload:repair-pin-control")
	// The in-flight upload: registered on the outgoing store only, forward
	// pending — the successor's lookup will miss it.
	oldStore.storeRef(ref, sentPath, meta, "upload:repair-pin-1")

	if err := oldStore.PinRef(ref, sentScope); err != nil {
		t.Fatalf("instrument: PinRef = %v, want nil", err)
	}

	// The repair's contract: present AND pinned, read from the store users
	// are served from.
	liveStore.mu.RLock()
	_, present := liveStore.refs[ref]
	scope := liveStore.refToScope[ref]
	liveStore.mu.RUnlock()
	if !present {
		t.Fatalf("the live store does not hold the ref the send reported, want the missing-successor repair to have registered it")
	}
	if !isPinnedScope(scope) {
		t.Errorf("the missing-successor repair registered ref %s under scope %q, want the sent-file scope: a repair that leaves the live ref unpinned loses the sent file at the next cleanup (SFH-4)", ref, scope)
	}

	// The user-visible half: the ref survives the original upload's age limit
	// on the live store, next to the expired control.
	start := time.Now()
	liveStore.nowFunc = func() time.Time { return start.Add(2 * time.Second) }
	liveStore.CleanExpired()
	if got, resolveErr := liveStore.Resolve(ref); resolveErr != nil || got != sentPath {
		t.Errorf("the sent ref after the upload age limit = %q %v, want the sent file still served from the live store (docs/using-omnipus-ui.md: a sent file does not expire)", got, resolveErr)
	}
	if _, controlErr := liveStore.Resolve(controlRef); !errors.Is(controlErr, ErrNotFound) {
		t.Errorf("control unsent upload error = %v, want ErrNotFound — the same cleanup must expire uploads", controlErr)
	}
}

func TestPinRef_LateUploadForwardCannotLeaveLiveRefUnpinned(t *testing.T) {
	// SFH-4 finding, steps 1–6: an upload registers locally,
	// then its original-scope forward lands after the successor's pin miss
	// but before repair. A nil PinRef result must still mean present AND
	// pinned in the live store, with exactly one count for this unique ref.
	// The hook fixes that operation order; no scheduler lottery is involved.
	mediaDir := isolatedRegistryHome(t)
	sentPath := writePayload(t, mediaDir, "late-forward-plan.md")
	controlPath := writePayload(t, mediaDir, "late-forward-control.md")
	const maxAge = time.Second
	registeredAt := time.Now()
	cfg := MediaCleanerConfig{MaxAge: maxAge, Interval: time.Hour}
	oldStore := NewFileMediaStoreWithCleanup(cfg)
	liveStore := NewFileMediaStoreWithCleanup(cfg)
	parkDebounceSave(oldStore)
	parkDebounceSave(liveStore)
	t.Cleanup(oldStore.Stop)
	t.Cleanup(liveStore.Stop)
	oldStore.nowFunc = func() time.Time { return registeredAt }
	liveStore.nowFunc = func() time.Time { return registeredAt }
	oldStore.HandoverTo(liveStore)

	const ref = "media://late-forward-sent"
	const controlRef = "media://late-forward-control"
	const uploadScope = "upload:late-forward-chat"
	sentScope := SentFileScopePrefix + "chat:late-forward"
	meta := MediaMeta{
		Filename: "late-forward-plan.md", ContentType: "text/markdown",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}
	// Store's local registration is complete, but its forward is pending.
	// Retain the original uploadScope just as that in-flight Store call does.
	oldStore.storeRef(ref, sentPath, meta, uploadScope)
	liveStore.storeRef(controlRef, controlPath, MediaMeta{
		Filename: "late-forward-control.md", ContentType: "text/plain",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}, "upload:late-forward-control")
	if got, err := liveStore.Resolve(controlRef); err != nil || got != controlPath {
		t.Fatalf("instrument: live control before cleanup = %q %v, want its registered path", got, err)
	}
	if _, err := liveStore.Resolve(ref); !errors.Is(err, ErrNotFound) {
		t.Fatalf("instrument: live sent ref before pin = %v, want ErrNotFound while the upload forward is pending", err)
	}

	hookCalls := 0
	oldStore.beforeSuccessorPinRegistration = func(successor *FileMediaStore, forwardedRef string) {
		hookCalls++
		if successor != liveStore || forwardedRef != ref {
			t.Fatalf("instrument: successor/ref = %p %q, want %p %q", successor, forwardedRef, liveStore, ref)
		}
		successor.mu.RLock()
		_, alreadyPresent := successor.refs[ref]
		successor.mu.RUnlock()
		if alreadyPresent {
			t.Fatal("instrument: live ref already present at the hook, want the missing-successor registration window")
		}
		oldStore.mu.RLock()
		outgoingScope := oldStore.refToScope[ref]
		oldStore.mu.RUnlock()
		if outgoingScope != sentScope {
			t.Fatalf("instrument: outgoing scope at hook = %q, want local pin to %q completed before the pending forward", outgoingScope, sentScope)
		}

		// Complete the REAL delayed Store forward, using its captured upload
		// scope rather than the outgoing entry's newly pinned scope.
		oldStore.forwardToSuccessors(forwardedRef, sentPath, meta, uploadScope)
		successor.mu.RLock()
		forwardedEntry, present := successor.refs[ref]
		forwardedScope := successor.refToScope[ref]
		forwardedCount := successor.pathStates[sentPath].refCount
		successor.mu.RUnlock()
		if !present || forwardedEntry.path != sentPath || forwardedScope != uploadScope || forwardedCount != 1 {
			t.Fatalf("instrument: delayed forward produced present=%t path=%q scope=%q count=%d, want the same ref/path under its unpinned upload scope with count 1", present, forwardedEntry.path, forwardedScope, forwardedCount)
		}
	}
	if err := oldStore.PinRef(ref, sentScope); err != nil {
		t.Fatalf("PinRef after the delayed upload forward = %v, want nil and a live pinned ref", err)
	}
	if hookCalls != 1 {
		t.Fatalf("instrument: pending-forward hook calls = %d, want exactly 1", hookCalls)
	}
	t.Log("instrument: delayed upload forward completed exactly once at the successor registration hook; PinRef returned nil")

	liveStore.mu.RLock()
	liveScope := liveStore.refToScope[ref]
	pathCount := liveStore.pathStates[sentPath].refCount
	liveStore.mu.RUnlock()
	if liveScope != sentScope {
		t.Errorf("SFH-4: PinRef returned nil but live ref scope = %q, want pinned sent-file scope %q after the late unpinned forward", liveScope, sentScope)
	}
	if pathCount != 1 {
		t.Errorf("SFH-4: live path reference count = %d, want exactly 1 for the same unique ref after forward and pin registration", pathCount)
	}
	if got, err := liveStore.Resolve(ref); err != nil || got != sentPath {
		t.Errorf("live sent ref immediately after successful pin = %q %v, want its original path", got, err)
	}

	// Cross the original upload's MaxAge using only the controlled clock.
	// The same cleaner must expire the unsent control, but not the sent ref.
	liveStore.nowFunc = func() time.Time { return registeredAt.Add(2 * maxAge) }
	if removed := liveStore.CleanExpired(); removed != 1 {
		t.Errorf("SFH-4: cleanup beyond MaxAge removed %d refs, want only the 1 unsent control", removed)
	}
	if got, err := liveStore.Resolve(ref); err != nil || got != sentPath {
		t.Errorf("SFH-4: live sent ref beyond MaxAge = %q %v, want the successfully pinned file still resolvable after the late upload forward", got, err)
	}
	if _, err := liveStore.Resolve(controlRef); !errors.Is(err, ErrNotFound) {
		t.Errorf("unsent expiry control beyond MaxAge = %v, want ErrNotFound from the same cleanup", err)
	}
}
