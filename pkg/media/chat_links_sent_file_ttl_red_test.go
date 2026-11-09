package media

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Shipped default: pkg/config/defaults.go MediaCleanup.MaxAge is 30 minutes.
// A sent file uses scope "tool:send_file:<channel>:<chat>" and forget_only
// (pkg/tools/send_file.go). Q17 A: that ref stays resolvable after MaxAge.
// A forget_only ref that is not a sent file and not session-pinned inline
// media still expires, so "never clean anything" cannot pass.
const sentFileMaxAge = 30 * time.Minute

func TestCleanExpired_SendFileOlderThanMaxAgeStaysResolvable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store := newTestStoreWithCleanup(t, sentFileMaxAge)
	store.nowFunc = func() time.Time { return now.Add(-sentFileMaxAge - time.Second) }
	ref, err := store.Store(path, MediaMeta{
		Filename: "plan.md", ContentType: "text/markdown", Source: "tool:send_file",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}, "tool:send_file:webchat:chat-1685")
	if err != nil {
		t.Fatal(err)
	}
	store.nowFunc = func() time.Time { return now }
	removed := store.CleanExpired()
	if removed != 0 {
		t.Errorf("CleanExpired removed %d, want 0: a sent file older than MaxAge stays (Q17 A)", removed)
	}
	got, resolveErr := store.Resolve(ref)
	if resolveErr != nil {
		t.Fatalf("sent file ref %s no longer resolves after MaxAge: %v", ref, resolveErr)
	}
	if got != path {
		t.Fatalf("Resolve = %q, want the sent file %q", got, path)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("sent file was deleted: %v", statErr)
	}
}

func TestCleanExpired_NonSendFileForgetOnlyStillExpires(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "upload.txt")
	if err := os.WriteFile(path, []byte("temp"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store := newTestStoreWithCleanup(t, sentFileMaxAge)
	store.nowFunc = func() time.Time { return now.Add(-sentFileMaxAge - time.Second) }
	ref, err := store.Store(path, MediaMeta{
		Filename: "upload.txt", CleanupPolicy: CleanupPolicyForgetOnly,
	}, "upload:temp")
	if err != nil {
		t.Fatal(err)
	}
	store.nowFunc = func() time.Time { return now }
	removed := store.CleanExpired()
	if removed != 1 {
		t.Fatalf("CleanExpired removed %d, want 1: a temporary ref that is not a sent file still expires", removed)
	}
	if _, resolveErr := store.Resolve(ref); !errors.Is(resolveErr, ErrNotFound) {
		t.Fatalf("temporary ref error = %v, want ErrNotFound", resolveErr)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("forget_only file should remain on disk: %v", statErr)
	}
}
