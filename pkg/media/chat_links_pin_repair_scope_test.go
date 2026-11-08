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
