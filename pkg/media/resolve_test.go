package media_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// These tests cover the Slice G resolver contract (FR-028/FR-028a/FR-029/
// FR-030): the nilable caller-workspace context, the cross-workspace
// Spoofing guard, and the workspace-library-first / legacy-global-fallback
// two-tier resolution. They exercise the real media.FileMediaStore wired to
// a real library.Library through the WorkspaceLibraryProvider seam — no
// fakes — so the guard and routing are observed end to end.
//
// The test lives in the external media_test package (not package media)
// because it imports pkg/media/library, which itself imports pkg/media; an
// internal test would form an import cycle.

// fixedClock returns a library.WithClock-compatible func pinned to a fixed
// time so fixture uploads are deterministic and never trip the orphan-GC
// age gate.
func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) }
}

// writeLegacyFixture writes a temp file and registers it under the global
// FileMediaStore, returning the resulting legacy media://<uuid> ref.
func writeLegacyFixture(t *testing.T, store *media.FileMediaStore, scope, filename, contentType string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte("legacy media bytes"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ref, err := store.Store(path, media.MediaMeta{Filename: filename, ContentType: contentType}, scope)
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	return ref
}

// TestResolver_RejectsCrossWorkspaceRef asserts the FR-028a STRIDE Spoofing
// guard: an agent in ws-B resolving a media://workspace/ws-A/<id> ref MUST
// be rejected, a missing caller context MUST be rejected, and only a caller
// matching the ref's workspace resolves. Legacy media://<uuid> refs are out
// of scope here (see TestResolver_LegacyCallSites_UnaffectedByNilableContextParam).
func TestResolver_RejectsCrossWorkspaceRef(t *testing.T) {
	home := t.TempDir()
	const wsA = "ws-owner"
	const wsB = "ws-attacker"

	lib, err := library.New(home, wsA, library.WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	ref, _, uploadErr := lib.UploadFixture("secret.bin", strings.NewReader("workspace-only bytes"))
	if uploadErr != nil {
		t.Fatalf("UploadFixture: %v", uploadErr)
	}
	if !media.IsWorkspaceRef(ref) {
		t.Fatalf("expected workspace ref, got %q", ref)
	}

	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
		if workspaceID != wsA {
			// The guard authorizes before the provider is consulted, so a
			// mismatched caller never reaches here; returning an error keeps
			// the seam honest if it ever does.
			return nil, media.ErrCrossWorkspaceRef
		}
		return lib, nil
	})

	// Cross-workspace: ws-B resolving ws-A's ref MUST be rejected.
	_, _, crossErr := store.ResolveWithMetaOpts(ref, media.WithCallerWorkspace(wsB))
	if !errors.Is(crossErr, media.ErrCrossWorkspaceRef) {
		t.Fatalf("cross-workspace resolve: want ErrCrossWorkspaceRef, got %v", crossErr)
	}

	// Missing context: a nil caller MUST be rejected for workspace refs.
	if _, _, nilErr := store.ResolveWithMetaOpts(ref, media.ResolveOpts{}); !errors.Is(
		nilErr, media.ErrWorkspaceContextRequired) {
		t.Fatalf("nil-context resolve: want ErrWorkspaceContextRequired, got %v", nilErr)
	}

	// Empty-string context is equally rejected.
	empty := ""
	if _, _, emptyErr := store.ResolveWithMetaOpts(
		ref, media.ResolveOpts{CallerWorkspace: &empty}); !errors.Is(emptyErr, media.ErrWorkspaceContextRequired) {
		t.Fatalf("empty-context resolve: want ErrWorkspaceContextRequired, got %v", emptyErr)
	}

	// Same-workspace: ws-A resolving its own ref MUST succeed and surface the
	// library's on-disk path + transport metadata.
	path, meta, ownErr := store.ResolveWithMetaOpts(ref, media.WithCallerWorkspace(wsA))
	if ownErr != nil {
		t.Fatalf("same-workspace resolve: unexpected error %v", ownErr)
	}
	if path == "" {
		t.Fatal("same-workspace resolve: empty path")
	}
	if meta.Filename != "secret.bin" {
		t.Fatalf("meta.Filename = %q, want %q", meta.Filename, "secret.bin")
	}
}

// TestIsCallerWorkspaceDenied_DistinguishesGuardDenialFromRoutineFailure is
// the review FIX 4 regression test: pkg/media.IsCallerWorkspaceDenied must
// report true for both FR-028a guard sentinels (a missing caller-workspace
// context, and an explicit cross-workspace Spoofing attempt) reached through
// the actual channel-facing entry point, ResolveWithCallerWorkspace — not
// just the lower-level ResolveWithMetaOpts — and false for a routine
// stale/missing ref (ErrNotFound) or any other unrelated error, so a caller
// (e.g. a channel's SendMedia implementation) can tell a security-relevant
// denial apart from an ordinary delivery failure with one check instead of
// needing to know about and OR together two separate sentinel variables.
func TestIsCallerWorkspaceDenied_DistinguishesGuardDenialFromRoutineFailure(t *testing.T) {
	home := t.TempDir()
	const wsA = "ws-owner"
	const wsB = "ws-attacker"

	lib, err := library.New(home, wsA, library.WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	ref, _, uploadErr := lib.UploadFixture("secret.bin", strings.NewReader("workspace-only bytes"))
	if uploadErr != nil {
		t.Fatalf("UploadFixture: %v", uploadErr)
	}

	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
		if workspaceID != wsA {
			return nil, media.ErrCrossWorkspaceRef
		}
		return lib, nil
	})

	// Cross-workspace spoofing attempt, via the real channel entry point.
	if _, _, crossErr := store.ResolveWithCallerWorkspace(ref, wsB); !media.IsCallerWorkspaceDenied(crossErr) {
		t.Fatalf("cross-workspace resolve error %v: want IsCallerWorkspaceDenied=true", crossErr)
	}

	// Missing caller-workspace context (empty string — ResolveWithCallerWorkspace
	// has no *string, so "" is the nilable-equivalent legacy posture... but for
	// a WORKSPACE ref, an empty callerWorkspace still triggers the guard, not
	// the legacy bypass, since IsWorkspaceRef(ref) is true).
	if _, _, missingErr := store.ResolveWithCallerWorkspace(ref, ""); !media.IsCallerWorkspaceDenied(missingErr) {
		t.Fatalf("missing-context resolve error %v: want IsCallerWorkspaceDenied=true", missingErr)
	}

	// A routine stale/missing ref must NOT be reported as a guard denial.
	staleRef := "media://workspace/" + wsA + "/" + "00000000-0000-0000-0000-000000000000"
	_, _, staleErr := store.ResolveWithCallerWorkspace(staleRef, wsA)
	if staleErr == nil {
		t.Fatal("expected an error resolving a nonexistent media id")
	}
	if media.IsCallerWorkspaceDenied(staleErr) {
		t.Fatalf("stale/missing ref error %v: want IsCallerWorkspaceDenied=false", staleErr)
	}

	// Any other unrelated error (e.g. a plain sentinel, or nil) must also be
	// false — this is not a blanket "err != nil" check in disguise.
	if media.IsCallerWorkspaceDenied(errors.New("some unrelated error")) {
		t.Fatal("unrelated error: want IsCallerWorkspaceDenied=false")
	}
	if media.IsCallerWorkspaceDenied(nil) {
		t.Fatal("nil error: want IsCallerWorkspaceDenied=false")
	}

	// Sanity: the legitimate same-workspace resolve must still succeed and
	// must not be misreported as a denial.
	if _, _, ownErr := store.ResolveWithCallerWorkspace(ref, wsA); ownErr != nil {
		t.Fatalf("same-workspace resolve: unexpected error %v", ownErr)
	}
}

// TestResolver_LegacyCallSites_UnaffectedByNilableContextParam asserts the
// FR-029 nilable migration: legacy media://<uuid> resolution is identical
// whether the call-site uses the legacy entry point or the new opts-bearing
// one with a nil caller context. The membership guard never fires for legacy
// refs, and a non-nil caller workspace does not change legacy behavior
// either (FR-030: no auto-rescoping).
func TestResolver_LegacyCallSites_UnaffectedByNilableContextParam(t *testing.T) {
	store := media.NewFileMediaStore()
	ref := writeLegacyFixture(t, store, "upload:sess-1", "photo.png", "image/png")

	legacyPath, legacyMeta, legacyErr := store.ResolveWithMeta(ref)
	nilPath, nilMeta, nilErr := store.ResolveWithMetaOpts(ref, media.ResolveOpts{})
	if legacyErr != nil || nilErr != nil {
		t.Fatalf("resolve errors: legacy=%v nil=%v", legacyErr, nilErr)
	}
	if legacyPath != nilPath {
		t.Fatalf("path changed: legacy=%q nil=%q", legacyPath, nilPath)
	}
	if legacyMeta != nilMeta {
		t.Fatalf("meta changed: legacy=%#v nil=%#v", legacyMeta, nilMeta)
	}

	// A non-nil caller workspace MUST NOT re-scope or reject a legacy ref
	// (FR-030 no auto-rescoping): the guard is bypassed for the legacy shape.
	anyWS := "ws-irrelevant"
	wsPath, wsMeta, wsErr := store.ResolveWithMetaOpts(ref, media.WithCallerWorkspace(anyWS))
	if wsErr != nil {
		t.Fatalf("legacy resolve with workspace context failed: %v", wsErr)
	}
	if wsPath != legacyPath || wsMeta != legacyMeta {
		t.Fatalf("legacy resolve changed under workspace context: path=%q meta=%#v", wsPath, wsMeta)
	}

	// The metadata-returning and path-only opts entry points must agree.
	optsPath, optsErr := store.ResolveWithOpts(ref, media.ResolveOpts{})
	if optsErr != nil || optsPath != legacyPath {
		t.Fatalf("ResolveWithOpts: path=%q err=%v (want %q nil)", optsPath, optsErr, legacyPath)
	}

	// And the legacy path-only entry point still resolves identically.
	legacyPathOnly, legacyPathErr := store.Resolve(ref)
	if legacyPathErr != nil || legacyPathOnly != legacyPath {
		t.Fatalf("Resolve: path=%q err=%v (want %q nil)", legacyPathOnly, legacyPathErr, legacyPath)
	}
}

// TestResolver_WorkspaceLibraryFirstThenLegacyFallback asserts the FR-028
// two-tier resolution through a single entry point: a workspace-prefixed ref
// resolves via the owning workspace library, and a legacy media://<uuid> ref
// resolves via the global registry, both via ResolveWithMetaOpts. It also
// pins FR-030 (no auto-rescoping): the legacy ref is not rewritten or
// rejected when a workspace library is wired.
func TestResolver_WorkspaceLibraryFirstThenLegacyFallback(t *testing.T) {
	home := t.TempDir()
	const wsA = "ws-mixed"
	lib, err := library.New(home, wsA, library.WithClock(fixedClock()))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	wsRef, _, uploadErr := lib.UploadFixture("doc.bin", strings.NewReader("workspace bytes"))
	if uploadErr != nil {
		t.Fatalf("UploadFixture: %v", uploadErr)
	}

	store := media.NewFileMediaStore()
	store.SetWorkspaceLibraryProvider(func(workspaceID string) (media.WorkspaceLibraryResolver, error) {
		if workspaceID != wsA {
			return nil, media.ErrCrossWorkspaceRef
		}
		return lib, nil
	})
	// A legacy ref registered in the global registry alongside the wired
	// library — both tiers populated.
	legacyRef := writeLegacyFixture(t, store, "upload:sess-2", "legacy.png", "image/png")

	// Workspace tier: workspace ref resolves via the library (path returned).
	wsPath, wsMeta, wsResolveErr := store.ResolveWithMetaOpts(wsRef, media.WithCallerWorkspace(wsA))
	if wsResolveErr != nil {
		t.Fatalf("workspace ref did not resolve via library: %v", wsResolveErr)
	}
	if wsPath == "" || !strings.Contains(wsPath, wsA) {
		t.Fatalf("workspace path %q does not locate the owning workspace %q", wsPath, wsA)
	}
	if wsMeta.Filename != "doc.bin" {
		t.Fatalf("workspace meta.Filename = %q, want %q", wsMeta.Filename, "doc.bin")
	}

	// Legacy tier: legacy ref resolves via the global registry through the
	// SAME entry point, with a nil caller context (FR-029 posture).
	legacyPath, legacyMeta, legacyResolveErr := store.ResolveWithMetaOpts(legacyRef, media.ResolveOpts{})
	if legacyResolveErr != nil {
		t.Fatalf("legacy ref did not resolve via global registry: %v", legacyResolveErr)
	}
	if legacyMeta.Filename != "legacy.png" {
		t.Fatalf("legacy meta.Filename = %q, want %q", legacyMeta.Filename, "legacy.png")
	}
	if legacyPath == wsPath {
		t.Fatalf("legacy and workspace refs resolved to the same path %q", legacyPath)
	}

	// FR-030: the legacy ref is not auto-rescoped. Resolving it with the
	// workspace's caller context still returns the GLOBAL path (the legacy
	// ref shape bypasses the workspace tier), proving no re-scoping occurred.
	rescopedPath, _, rescopedErr := store.ResolveWithMetaOpts(legacyRef, media.WithCallerWorkspace(wsA))
	if rescopedErr != nil || rescopedPath != legacyPath {
		t.Fatalf("legacy ref auto-rescoped: path=%q err=%v (want %q nil)", rescopedPath, rescopedErr, legacyPath)
	}
}
