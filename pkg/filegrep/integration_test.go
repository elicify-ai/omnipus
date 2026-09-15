// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"os"
	"path/filepath"
	"testing"
)

// openConfinedRoot opens dir as an os.Root-confined fs.FS, matching
// production's confinement mechanism (os.Root.FS()) exactly, and registers
// cleanup.
func openConfinedRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("os.OpenRoot(%q): %v", dir, err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// TestFileGrep_SymlinkConfinement pins US-2 AS-5 / FR-014: a symlink
// pointing outside the confined root must never leak content from outside,
// over a REAL temp-dir filesystem confined via os.Root.FS() — the same
// confinement mechanism production uses (library.Root).
func TestFileGrep_SymlinkConfinement(t *testing.T) {
	tmp := t.TempDir()
	ws := filepath.Join(tmp, "workspace")
	outside := filepath.Join(tmp, "outside")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("topsecret needle content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "normal.txt"), []byte("ordinary needle content"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink INSIDE the workspace pointing OUTSIDE it, by both a
	// relative traversal and (separately) an absolute path.
	if err := os.Symlink(filepath.Join("..", "outside", "secret.txt"), filepath.Join(ws, "escape-relative.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(ws, "escape-absolute.txt")); err != nil {
		t.Fatal(err)
	}
	// A symlink to a directory outside the root, to confirm directories
	// aren't traversed either.
	if err := os.Symlink(outside, filepath.Join(ws, "escape-dir")); err != nil {
		t.Fatal(err)
	}

	root := openConfinedRoot(t, ws)
	res := mustSearch(t, oneRoot(root.FS()), Options{Query: "needle"})

	for _, h := range res.Hits {
		if h.Kind == KindContent {
			if h.Path != "normal.txt" {
				t.Fatalf("content hit from outside the confined root leaked: %+v", h)
			}
			if !containsSubstr(h.Excerpt, "ordinary needle content") && !containsSubstr(h.Excerpt, "needle") {
				t.Fatalf("unexpected excerpt content: %q", h.Excerpt)
			}
		}
	}
	// The symlinks' own NAMES may legitimately match ("escape" is not being
	// searched here, so none of them should appear at all) — searching
	// "secret" must find nothing, proving the outside file's content and
	// even a name-based hint about it never surfaces via the symlink.
	secretRes := mustSearch(t, oneRoot(root.FS()), Options{Query: "secret"})
	for _, h := range secretRes.Hits {
		t.Fatalf("query for 'secret' must find nothing through a symlink, got %+v", h)
	}
	if secretRes.Truncated {
		t.Fatalf("no error/truncation expected, got reason=%v", secretRes.TruncatedReason)
	}
}

// TestFileGrep_UnreadableSkippedCounted pins FR-021: an injected per-file
// Open error (never chmod-000 — see repo convention: void under root CI) is
// skipped and counted, while readable files still return their matches. A
// dangling symlink is exercised as the second, naturally-occurring variant
// of "a file entry that fails to open."
func TestFileGrep_UnreadableSkippedCounted(t *testing.T) {
	t.Run("injected FS-error seam", func(t *testing.T) {
		base := buildFS(map[string]string{
			"good.txt": "needle here",
			"bad.txt":  "needle here too",
		})
		wrapped := &failingFS{FS: base, failOpen: func(name string) bool { return name == "bad.txt" }}
		res := mustSearch(t, oneRoot(wrapped), Options{Query: "needle"})

		if res.Truncated {
			t.Fatalf("an isolated per-file problem must not truncate the request, got reason=%v", res.TruncatedReason)
		}
		var gotGood bool
		for _, h := range res.Hits {
			if h.Kind == KindContent && h.Path == "good.txt" {
				gotGood = true
			}
			if h.Kind == KindContent && h.Path == "bad.txt" {
				t.Fatal("bad.txt should never produce a content hit — its Open call fails")
			}
		}
		if !gotGood {
			t.Fatalf("good.txt should still be found, got %+v", res.Hits)
		}
		if res.Stats.FilesSkippedProblems == 0 {
			t.Fatal("want FilesSkippedProblems > 0")
		}
	})

	t.Run("dangling symlink variant", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, "good.txt"), []byte("needle here"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(tmp, "does-not-exist.txt"), filepath.Join(tmp, "dangling.txt")); err != nil {
			t.Fatal(err)
		}
		root := openConfinedRoot(t, tmp)
		res := mustSearch(t, oneRoot(root.FS()), Options{Query: "needle"})

		var gotGood bool
		for _, h := range res.Hits {
			if h.Kind == KindContent && h.Path == "good.txt" {
				gotGood = true
			}
		}
		if !gotGood {
			t.Fatalf("good.txt should still be found alongside a dangling symlink, got %+v", res.Hits)
		}
		// A dangling symlink is a non-regular directory entry (ModeSymlink),
		// so it is skipped by the same IsRegular() guard that protects
		// symlink confinement — it should not even attempt an Open that
		// would fail, but either way the walk must not truncate.
		if res.Truncated {
			t.Fatalf("a dangling symlink must not truncate the request, got reason=%v", res.TruncatedReason)
		}
	})
}

// TestFileGrep_RootLostMidWalk pins US-2 AS-6/FR-021: when the walk root
// (or a mount) becomes unreadable partway through the walk — not just one
// isolated file or subdirectory, but the mount itself going dark — the
// result MUST be a visible root_lost truncation, never a bare empty result.
func TestFileGrep_RootLostMidWalk(t *testing.T) {
	files := map[string]string{
		"a/one.txt": "hello\n",
		"b/two.txt": "hello\n",
		"c/six.txt": "hello\n",
	}
	// Let the walk root's own top-level ReadDir(".") succeed (that's call
	// #1), then have every ReadDir call after it — starting with whichever
	// subdirectory (a/, b/, or c/) the walker visits next — fail, and take
	// the wrapper's Stat(".") health probe down with it, simulating the
	// whole mount dying mid-walk rather than one directory hiccuping.
	wrapped := &failingFS{FS: buildFS(files), readDirBudget: 1}
	res := mustSearch(t, oneRoot(wrapped), Options{Query: "hello"})

	if !res.Truncated || res.TruncatedReason != ReasonRootLost {
		t.Fatalf("want truncated root_lost, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
	}
}

// TestFileGrep_RootLostMidWalk_IsolatedSubdirDoesNotPromote proves the
// boundary the promotion logic draws: a single subdirectory failing to read
// while the ROOT remains healthy is an isolated per-item problem (counted),
// not a root_lost truncation — only a genuinely dead root/mount promotes.
// Uses the injectable FS-error seam (never chmod-000, which is void when CI
// runs as root — repo convention) to fail exactly one subdirectory's
// ReadDir while every other directory, including the root's own listing and
// its Stat(".") health probe, keeps working normally.
func TestFileGrep_RootLostMidWalk_IsolatedSubdirDoesNotPromote(t *testing.T) {
	files := map[string]string{
		"locked/inside.txt": "hello\n",
		"readable.txt":      "hello\n",
	}
	wrapped := &failingFS{
		FS:              buildFS(files),
		failReadDirOnce: func(name string) bool { return name == "locked" },
	}
	res := mustSearch(t, oneRoot(wrapped), Options{Query: "hello"})

	if res.TruncatedReason == ReasonRootLost {
		t.Fatalf("an isolated unreadable subdirectory (root itself still healthy) must not promote to root_lost, got %+v", res)
	}
	if res.Truncated {
		t.Fatalf("an isolated unreadable subdirectory must not truncate the request at all, got reason=%v", res.TruncatedReason)
	}
	if res.Stats.FilesSkippedProblems == 0 {
		t.Fatal("want the locked subdirectory counted as a skipped problem")
	}
	var gotReadable bool
	for _, h := range res.Hits {
		if h.Path == "readable.txt" {
			gotReadable = true
		}
	}
	if !gotReadable {
		t.Fatalf("readable.txt should still be found, got %+v", res.Hits)
	}
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
