package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
)

// singlePixelPNG is a 1x1 transparent PNG encoded as base64.
const singlePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func pngDataURL() string {
	return "data:image/png;base64," + singlePixelPNG
}

// newTestStore returns a FileMediaStore for use in tests.
func newTestStore(t *testing.T) *media.FileMediaStore {
	t.Helper()
	return media.NewFileMediaStore()
}

// TestToolSessionID_ContextRoundTrip verifies the context helper round-trip.
func TestToolSessionID_ContextRoundTrip(t *testing.T) {
	ctx := context.Background()

	// Empty by default.
	if got := ToolTranscriptSessionID(ctx); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Survives a round-trip.
	const want = "session_01KP30THP63YFESKGECYYHYQWY"
	ctx2 := WithTranscriptSessionID(ctx, want)
	if got := ToolTranscriptSessionID(ctx2); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Does not contaminate the parent context.
	if got := ToolTranscriptSessionID(ctx); got != "" {
		t.Errorf("parent context should be unchanged, got %q", got)
	}
}

// TestStoreInlineDataURL_WithSession_UsesUploadsDir verifies that when a
// transcript session ID is in context the decoded file lands under
// <home>/uploads/<sessionID>/ and is stored with CleanupPolicyForgetOnly.
func TestStoreInlineDataURL_WithSession_UsesUploadsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)

	const sessionID = "session_test_abc123"
	ctx := WithTranscriptSessionID(context.Background(), sessionID)

	ref, note := storeInlineDataURL(
		ctx,
		"browser.screenshot",
		store,
		"webchat",
		"chat-1",
		pngDataURL(),
		make(map[string]struct{}),
	)

	if ref == "" {
		t.Fatalf("expected a ref, got empty; note=%q", note)
	}

	// The note should indicate media was stored.
	if !strings.Contains(note, "image/png") {
		t.Errorf("expected note to mention image/png, got %q", note)
	}

	// Resolve the ref and confirm the path is under uploads/<sessionID>/.
	localPath, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("ResolveWithMeta: %v", err)
	}

	wantDir := filepath.Join(home, "uploads", sessionID)
	if !strings.HasPrefix(localPath, wantDir) {
		t.Errorf("file %q is not under expected uploads dir %q", localPath, wantDir)
	}

	// Cleanup policy must be forget_only so the TTL cleaner leaves it alone.
	if meta.CleanupPolicy != media.CleanupPolicyForgetOnly {
		t.Errorf("expected CleanupPolicyForgetOnly, got %q", meta.CleanupPolicy)
	}

	// File must actually exist on disk.
	if _, err := os.Stat(localPath); err != nil {
		t.Errorf("resolved path does not exist on disk: %v", err)
	}
}

// TestStoreInlineDataURL_NoSession_UsesEphemeralDir verifies that without a
// session ID the file lands in the media TempDir with delete_on_cleanup policy,
// preserving the pre-fix behavior for truly ephemeral media.
func TestStoreInlineDataURL_NoSession_UsesEphemeralDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)

	// No session ID in context.
	ctx := context.Background()

	ref, note := storeInlineDataURL(
		ctx,
		"browser.screenshot",
		store,
		"telegram",
		"chat-99",
		pngDataURL(),
		make(map[string]struct{}),
	)

	if ref == "" {
		t.Fatalf("expected a ref, got empty; note=%q", note)
	}

	localPath, meta, err := store.ResolveWithMeta(ref)
	if err != nil {
		t.Fatalf("ResolveWithMeta: %v", err)
	}

	// Must be in the media dir, NOT in uploads.
	wantDir := filepath.Join(home, "media")
	if !strings.HasPrefix(localPath, wantDir) {
		t.Errorf("file %q is not under expected media dir %q", localPath, wantDir)
	}

	// Cleanup policy must be delete_on_cleanup (original behavior).
	if meta.CleanupPolicy != media.CleanupPolicyDeleteOnCleanup {
		t.Errorf("expected CleanupPolicyDeleteOnCleanup, got %q", meta.CleanupPolicy)
	}
}

// TestCleanExpired_DoesNotDeleteForgetOnly asserts that CleanExpired:
//   - removes delete_on_cleanup entries (both ref and file) — regression guard
//   - leaves forget_only entries FULLY RESOLVABLE (ref stays in index) and their
//     files intact — the core safety property preventing session-bound screenshots
//     from disappearing after the 30-min TTL.
func TestCleanExpired_DoesNotDeleteForgetOnly(t *testing.T) {
	home := t.TempDir()

	// Write two real files.
	ephemeralPath := filepath.Join(home, "ephemeral.png")
	sessionPath := filepath.Join(home, "session.png")
	content := []byte("fake png content")
	if err := os.WriteFile(ephemeralPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	// Create a store with a very short TTL (1 nanosecond).
	store := media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		Enabled:  true,
		MaxAge:   time.Nanosecond,
		Interval: time.Hour, // don't auto-trigger; we call CleanExpired manually
	})

	// Register ephemeral file — delete_on_cleanup.
	ephRef, err := store.Store(ephemeralPath, media.MediaMeta{
		Filename:      "ephemeral.png",
		ContentType:   "image/png",
		CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
	}, "scope:ephemeral")
	if err != nil {
		t.Fatalf("store ephemeral: %v", err)
	}

	// Register session-bound file — forget_only.
	const sessionID = "session_clean_test"
	sessRef, err := store.Store(sessionPath, media.MediaMeta{
		Filename:      "session.png",
		ContentType:   "image/png",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "tool:inline:session:"+sessionID)
	if err != nil {
		t.Fatalf("store session: %v", err)
	}

	// Wait for entries to age past 1 nanosecond.
	time.Sleep(time.Millisecond)

	n := store.CleanExpired()

	// Exactly 1 entry (the ephemeral one) should have been cleaned.
	if n != 1 {
		t.Errorf("CleanExpired returned %d; expected exactly 1 (the ephemeral entry)", n)
	}

	// Ephemeral ref must be gone from the store index.
	if _, err = store.Resolve(ephRef); err == nil {
		t.Error("ephemeral ref should be unknown after CleanExpired")
	}
	// Ephemeral file must also be deleted from disk (delete_on_cleanup).
	if _, err = os.Stat(ephemeralPath); err == nil {
		t.Error("ephemeral file should be deleted from disk by CleanExpired")
	}

	// Session-bound ref must STILL be resolvable — this is the core assertion.
	if _, err = store.Resolve(sessRef); err != nil {
		t.Errorf("session ref must remain resolvable after CleanExpired: %v", err)
	}
	// Verify metadata round-trip too.
	resolvedPath, meta, err := store.ResolveWithMeta(sessRef)
	if err != nil {
		t.Fatalf("ResolveWithMeta on session ref: %v", err)
	}
	if resolvedPath != sessionPath {
		t.Errorf("resolved path %q != expected %q", resolvedPath, sessionPath)
	}
	if meta.CleanupPolicy != media.CleanupPolicyForgetOnly {
		t.Errorf("meta.CleanupPolicy = %q; want forget_only", meta.CleanupPolicy)
	}
	// Session file must still be on disk.
	if _, err := os.Stat(sessionPath); err != nil {
		t.Errorf("session file must NOT be deleted from disk: %v", err)
	}
}

// TestReleaseAll_FreesSessionInlineMedia verifies that after a session is deleted
// (simulated by calling ReleaseAll on the session scope), the session-bound
// inline media ref is no longer resolvable. This confirms the cleanup path used
// by deleteSession in rest.go does not leave orphaned in-memory entries.
func TestReleaseAll_FreesSessionInlineMedia(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)

	const sessionID = "session_release_test"
	ctx := WithTranscriptSessionID(context.Background(), sessionID)

	ref, note := storeInlineDataURL(
		ctx,
		"browser.screenshot",
		store,
		"webchat",
		"chat-1",
		pngDataURL(),
		make(map[string]struct{}),
	)
	if ref == "" {
		t.Fatalf("expected a ref, got empty; note=%q", note)
	}

	// Ref must be resolvable before release.
	if _, err := store.Resolve(ref); err != nil {
		t.Fatalf("ref should be resolvable before ReleaseAll: %v", err)
	}

	// Simulate deleteSession releasing the session's media scope.
	scope := "tool:inline:session:" + sessionID
	if err := store.ReleaseAll(scope); err != nil {
		t.Fatalf("ReleaseAll(%q): %v", scope, err)
	}

	// After ReleaseAll, the ref must no longer be resolvable.
	if _, err := store.Resolve(ref); err == nil {
		t.Error("ref should be unknown after ReleaseAll (session deleted)")
	}
}

// TestNormalizeToolResult_InjectsSessionMedia verifies the full normalizeToolResult
// path: a tool result carrying a data URL gets its media stored under the session
// uploads dir when a transcript session ID is in context.
func TestNormalizeToolResult_InjectsSessionMedia(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)

	const sessionID = "session_normalize_test"
	ctx := WithTranscriptSessionID(context.Background(), sessionID)

	result := &ToolResult{
		ForLLM:  fmt.Sprintf("Screenshot taken. ![screenshot](%s)", pngDataURL()),
		ForUser: "",
	}

	out := normalizeToolResult(ctx, result, "browser.screenshot", store, "webchat", "chat-42")

	if out == nil {
		t.Fatal("normalizeToolResult returned nil")
	}
	if len(out.Media) == 0 {
		t.Fatal("expected at least one media ref")
	}

	localPath, meta, err := store.ResolveWithMeta(out.Media[0])
	if err != nil {
		t.Fatalf("ResolveWithMeta: %v", err)
	}

	wantDir := filepath.Join(home, "uploads", sessionID)
	if !strings.HasPrefix(localPath, wantDir) {
		t.Errorf("media file %q not under session uploads dir %q", localPath, wantDir)
	}
	if meta.CleanupPolicy != media.CleanupPolicyForgetOnly {
		t.Errorf("expected CleanupPolicyForgetOnly, got %q", meta.CleanupPolicy)
	}
}

// TestSessionUploadsDir_RejectUnsafeIDs verifies that session IDs with
// path-traversal characters are rejected, protecting the uploads root.
func TestSessionUploadsDir_RejectUnsafeIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	unsafe := []string{
		"../etc/passwd",
		"session/../../escape",
		"session\x00null",
		"session\\windows",
		"",
	}
	for _, id := range unsafe {
		_, ok := media.SessionUploadsDir(id)
		if ok {
			t.Errorf("SessionUploadsDir(%q) returned ok=true; expected rejection", id)
		}
	}

	// A valid session ID must be accepted.
	validID := "session_01KP30THP63YFESKGECYYHYQWY"
	dir, ok := media.SessionUploadsDir(validID)
	if !ok {
		t.Errorf("SessionUploadsDir(%q) returned ok=false; expected acceptance", validID)
	}
	wantDir := filepath.Join(home, "uploads", validID)
	if dir != wantDir {
		t.Errorf("got %q, want %q", dir, wantDir)
	}
}

// TestStoreInlineDataURL_DeduplicatesDataURLs asserts that the same data URL
// in the seen set is skipped on a second call.
func TestStoreInlineDataURL_DeduplicatesDataURLs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)
	ctx := context.Background()
	seen := make(map[string]struct{})

	dataURL := pngDataURL()

	ref1, _ := storeInlineDataURL(ctx, "tool", store, "ch", "id", dataURL, seen)
	ref2, _ := storeInlineDataURL(ctx, "tool", store, "ch", "id", dataURL, seen)

	if ref1 == "" {
		t.Error("first call: expected a ref")
	}
	if ref2 != "" {
		t.Error("second call with same URL in seen: expected empty ref (deduplicated)")
	}
}

// TestStoreInlineDataURL_RawDataURLInForLLM exercises the raw (non-markdown)
// data URL path through extractInlineMediaRefs / normalizeToolResult.
func TestStoreInlineDataURL_RawDataURLInForLLM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)
	const sessionID = "session_raw_test"
	ctx := WithTranscriptSessionID(context.Background(), sessionID)

	// Simulate a tool that returns the raw data URL without markdown wrapping.
	result := &ToolResult{
		ForLLM: pngDataURL(),
	}

	out := normalizeToolResult(ctx, result, "image.gen", store, "webchat", "chat-7")
	if len(out.Media) == 0 {
		t.Fatal("expected media ref from raw data URL in ForLLM")
	}

	localPath, _, err := store.ResolveWithMeta(out.Media[0])
	if err != nil {
		t.Fatalf("ResolveWithMeta: %v", err)
	}
	wantDir := filepath.Join(home, "uploads", sessionID)
	if !strings.HasPrefix(localPath, wantDir) {
		t.Errorf("file %q not under session dir %q", localPath, wantDir)
	}
}

// TestStoreInlineDataURL_FilenamePattern checks that the temp file created for a
// session-bound entry uses the tool name as its prefix.
func TestStoreInlineDataURL_FilenamePattern(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store := newTestStore(t)
	ctx := WithTranscriptSessionID(context.Background(), "session_name_test")

	ref, _ := storeInlineDataURL(ctx, "browser.screenshot", store, "ch", "id", pngDataURL(), make(map[string]struct{}))
	if ref == "" {
		t.Fatal("expected ref")
	}

	localPath, _, _ := store.ResolveWithMeta(ref)
	base := filepath.Base(localPath)
	if !strings.HasPrefix(base, "tool-browser_screenshot-") {
		t.Errorf("filename %q does not start with 'tool-browser_screenshot-'", base)
	}
	// Valid PNG extension.
	decodedBytes, _ := base64.StdEncoding.DecodeString(singlePixelPNG)
	if len(decodedBytes) == 0 {
		t.Fatal("test setup: base64 decode failed")
	}
	if !strings.HasSuffix(base, ".png") {
		t.Errorf("filename %q does not end with .png", base)
	}
}
