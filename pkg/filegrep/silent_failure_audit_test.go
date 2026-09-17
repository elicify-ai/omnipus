// Omnipus — a silent-failure audit of pkg/filegrep found eight findings
// (F-A through F-H) while this branch was already touching this package for
// KB-7b. Each test below reproduces the finding against the PRE-fix
// behavior first (in its own comment/reasoning), then proves the fix.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package filegrep

import (
	"io/fs"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// F-A — binary content skips are entirely uncounted
// ---------------------------------------------------------------------------

// TestFileGrep_BinarySkipIsCounted pins FR-005/finding F-A: a file whose
// first binarySniffBytes contain a NUL byte is content-invisible (name-
// matchable only), and that skip must be OBSERVABLE — before this fix,
// Stats had no counter for it at all, while FilesVisited still counted the
// file (asserting it was searched) and BytesScanned excluded it (so nothing
// could even be inferred from the discrepancy).
func TestFileGrep_BinarySkipIsCounted(t *testing.T) {
	fsys := buildFS(map[string]string{
		"binary.dat": "needle\x00trailing binary bytes",
		"text.txt":   "needle in plain text",
	})
	res := mustSearch(t, oneRoot(fsys), Options{Query: "needle"})

	if res.Stats.FilesSkippedBinary != 1 {
		t.Errorf("Stats.FilesSkippedBinary = %d, want 1 (binary.dat)", res.Stats.FilesSkippedBinary)
	}
	// The binary file must still be counted as VISITED (it was reached and
	// name-checked) — FilesSkippedBinary is what makes that count honest,
	// not a reason to change FilesVisited's own meaning.
	if res.Stats.FilesVisited != 2 {
		t.Errorf("Stats.FilesVisited = %d, want 2 (both files reached)", res.Stats.FilesVisited)
	}
	for _, h := range res.Hits {
		if h.Kind == KindContent && h.Path == "binary.dat" {
			t.Errorf("binary.dat produced a CONTENT hit; FR-005 requires it be name-matchable only: %+v", h)
		}
	}
}

// ---------------------------------------------------------------------------
// F-B — a malformed glob is silently discarded; exclude fails OPEN
// ---------------------------------------------------------------------------

// TestFileGrep_MalformedGlob_RefusedNotSilentlyDiscarded pins finding F-B:
// doublestar.Match's error was previously dropped at every call site, so a
// malformed EXCLUDE pattern excluded NOTHING (fails open — a caller
// excluding a sensitive directory got it walked and excerpted back) and a
// malformed INCLUDE pattern matched NOTHING (indistinguishable from "the
// term genuinely isn't present"). Both must now be refused up front,
// loudly, naming the bad pattern — never silently discarded either way.
func TestFileGrep_MalformedGlob_RefusedNotSilentlyDiscarded(t *testing.T) {
	fsys := buildFS(map[string]string{"a.txt": "needle"})
	const badPattern = "a[b" // doublestar.ValidatePattern(...) == false

	t.Run("malformed exclude_globs is refused, not fail-open", func(t *testing.T) {
		_, err := Search(t.Context(), oneRoot(fsys), Options{
			Query: "needle", ExcludeGlobs: []string{badPattern},
		})
		if err == nil {
			t.Fatal("expected an error for a malformed exclude_globs pattern, got nil " +
				"(a fail-open exclude silently searches what the caller meant to exclude)")
		}
		if !strings.Contains(err.Error(), badPattern) {
			t.Errorf("error %q does not name the offending pattern %q", err.Error(), badPattern)
		}
	})

	t.Run("malformed include_globs is refused, not a confusing zero-result", func(t *testing.T) {
		_, err := Search(t.Context(), oneRoot(fsys), Options{
			Query: "needle", IncludeGlobs: []string{badPattern},
		})
		if err == nil {
			t.Fatal("expected an error for a malformed include_globs pattern, got nil " +
				"(a fail-closed include silently reports zero results, indistinguishable " +
				"from a genuine miss, and OBS-G1's anchoring advice would misdiagnose it)")
		}
		if !strings.Contains(err.Error(), badPattern) {
			t.Errorf("error %q does not name the offending pattern %q", err.Error(), badPattern)
		}
	})

	t.Run("a valid glob list still searches normally", func(t *testing.T) {
		res := mustSearch(t, oneRoot(fsys), Options{Query: "needle", IncludeGlobs: []string{"**/*.txt"}})
		if len(res.Hits) == 0 {
			t.Fatal("a syntactically valid include glob must not be refused")
		}
	})
}

// ---------------------------------------------------------------------------
// F-C — a lost root aborts every remaining root and the result cannot say
// which
// ---------------------------------------------------------------------------

// f7RootLostFS answers Stat(".") normally (so walkRoot's own upfront health
// check — "verify the root opens at all" — passes and the walk actually
// starts) but fails EVERY ReadDir call, root listing included. walkDir's
// `if dir == "" { return err }` branch treats a failed ROOT listing as
// root_lost immediately, with no further Stat probing needed — this
// isolates "this root's own directory never lists" as cleanly as possible.
type f7RootLostFS struct {
	fs.FS
}

func (f f7RootLostFS) Open(name string) (fs.File, error) { return f.FS.Open(name) }

func (f f7RootLostFS) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, &fs.PathError{Op: "readdir", Path: ".", Err: errMountGone}
}

func (f f7RootLostFS) Stat(name string) (fs.FileInfo, error) {
	if sf, ok := f.FS.(fs.StatFS); ok {
		return sf.Stat(name)
	}
	return fs.Stat(f.FS, name)
}

// TestFileGrep_MultiRoot_OneLostRootDoesNotSilenceHealthyOnes pins finding
// F-C: mount #1 dying used to abort the WHOLE search immediately, so mounts
// #2 and #3 were never even opened — an agent seeing work-tree hits plus a
// generic root_lost message could reasonably (and wrongly) conclude the
// term is absent from mounts that were never searched. Now every other
// root is searched to completion, and the result names WHICH root died.
func TestFileGrep_MultiRoot_OneLostRootDoesNotSilenceHealthyOnes(t *testing.T) {
	dying := f7RootLostFS{FS: buildFS(map[string]string{"a.txt": "needle here"})}
	healthy := buildFS(map[string]string{"b.txt": "needle here too"})

	roots := []Root{
		{Name: "dying-mount", FS: dying},
		{Name: "healthy-mount", FS: healthy},
	}
	res := mustSearch(t, roots, Options{Query: "needle"})

	if !res.Truncated || res.TruncatedReason != ReasonRootLost {
		t.Fatalf("want truncated root_lost, got truncated=%v reason=%v", res.Truncated, res.TruncatedReason)
	}
	if res.TruncatedRoot != "dying-mount" {
		t.Errorf("TruncatedRoot = %q, want %q — the result must name which root died", res.TruncatedRoot, "dying-mount")
	}
	foundHealthy := false
	for _, h := range res.Hits {
		if strings.HasPrefix(h.Path, "healthy-mount/") {
			foundHealthy = true
		}
	}
	if !foundHealthy {
		t.Errorf("the healthy mount's own hits are missing — a lost root must not silence "+
			"other roots: got %v", pathsOf(res.Hits))
	}
}

func pathsOf(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

// ---------------------------------------------------------------------------
// F-D — glob-filtered directories are traversed but never charged to the
// Files budget
// ---------------------------------------------------------------------------

// TestFileGrep_IncludeGlobHeavyTree_StillHitsMaxFiles pins finding F-D: an
// include_globs search over a directory-heavy tree where most directory
// PATHS do not themselves match the include pattern (the ordinary case for
// something like "**/*.md" — a directory is matched by NAME, a file deep
// inside it by its full path) must still be bounded by Limits.Files. Before
// the fix, DirsVisited and the budget check sat inside globAllowed's true
// branch, so a glob-rejected directory's entire subtree evaded the budget
// even though it is still walked (F2's own correct behavior: pruning the
// subtree on an include-glob miss would drop legitimately matching
// descendants).
func TestFileGrep_IncludeGlobHeavyTree_StillHitsMaxFiles(t *testing.T) {
	files := map[string]string{}
	// 50 directories, none of which itself matches "**/*.md" by NAME (the
	// glob is checked against the directory's own path, "dirNNN", which
	// never ends ".md"), each holding one non-matching file so ReadDir has
	// something to enumerate. The include glob rejects every directory's
	// own hit, but the walk still recurses into every one of them.
	for i := 0; i < 50; i++ {
		files[dirName(i)+"/note.txt"] = "filler"
	}
	fsys := buildFS(files)

	res := mustSearch(t, oneRoot(fsys), Options{
		Query:        "filler",
		IncludeGlobs: []string{"**/*.md"},
		Limits:       Limits{Files: 10},
	})
	if !res.Truncated || res.TruncatedReason != ReasonMaxFiles {
		t.Fatalf("want truncated max_files (the Files budget must be enforced even though every "+
			"directory's OWN path is glob-rejected), got truncated=%v reason=%v stats=%+v",
			res.Truncated, res.TruncatedReason, res.Stats)
	}
	if res.Stats.DirsVisited == 0 {
		t.Error("Stats.DirsVisited = 0; glob-rejected directories must still be charged to the budget")
	}
}

func dirName(i int) string {
	return "dir" + string(rune('A'+i%26)) + string(rune('0'+i/26))
}

// ---------------------------------------------------------------------------
// F-E — readIgnoreFiles swallows every error including a real read failure
// ---------------------------------------------------------------------------

// f7UnreadableIgnoreFS makes a NAMED file's ReadFile fail with a permission
// error rather than ErrNotExist — simulating a REAL .gitignore that exists
// but cannot be read, as opposed to the routine case (no ignore file at
// this path at all).
type f7UnreadableIgnoreFS struct {
	fs.FS
	unreadable string
}

func (f f7UnreadableIgnoreFS) Open(name string) (fs.File, error) {
	if name == f.unreadable {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.FS.Open(name)
}

// TestFileGrep_UnreadableIgnoreFile_CountedNotConflatedWithMissing pins
// finding F-E: readIgnoreFiles previously collapsed ErrNotExist (the
// routine, silent case — most directories have neither ignore filename)
// with a genuine read failure on a file that DOES exist. A permission-
// denied .gitignore then silently applied none of its rules, with a
// deliberately-excluded file walked, name-matched and excerpted back, and
// NOTHING in the response said why.
func TestFileGrep_UnreadableIgnoreFile_CountedNotConflatedWithMissing(t *testing.T) {
	fsys := f7UnreadableIgnoreFS{
		FS: buildFS(map[string]string{
			".gitignore": "secret.txt\n",
			"secret.txt": "needle in a file .gitignore means to exclude",
			"normal.txt": "needle in an ordinary file",
		}),
		unreadable: ".gitignore",
	}
	res := mustSearch(t, oneRoot(fsys), Options{Query: "needle"})

	if res.Stats.IgnoreFilesUnreadable != 1 {
		t.Errorf("Stats.IgnoreFilesUnreadable = %d, want 1", res.Stats.IgnoreFilesUnreadable)
	}
	// Degraded behavior (rules from the unreadable file do not apply) is
	// UNCHANGED and still exercised here — the fix is observability, not a
	// behavior change: secret.txt is still walked because its exclusion
	// rule could not be read.
	foundSecret := false
	for _, h := range res.Hits {
		if h.Path == "secret.txt" {
			foundSecret = true
		}
	}
	if !foundSecret {
		t.Error("secret.txt should still be walked (its exclusion rule failed to read, " +
			"which degrades to 'not excluded', same as no .gitignore at all) — if this " +
			"assertion now fails, the DEGRADED BEHAVIOR changed, which is a bigger change " +
			"than this fix intends")
	}
}

// TestFileGrep_MissingIgnoreFile_NotCountedAsUnreadable is the negative
// case: the ordinary "no .gitignore/.ignore in this directory" outcome
// (ErrNotExist) must NOT increment IgnoreFilesUnreadable — that remains
// the routine, silent case, exactly as before F-E.
func TestFileGrep_MissingIgnoreFile_NotCountedAsUnreadable(t *testing.T) {
	fsys := buildFS(map[string]string{"a.txt": "needle"})
	res := mustSearch(t, oneRoot(fsys), Options{Query: "needle"})
	if res.Stats.IgnoreFilesUnreadable != 0 {
		t.Errorf("Stats.IgnoreFilesUnreadable = %d, want 0 (no ignore files exist here at all)",
			res.Stats.IgnoreFilesUnreadable)
	}
}

// ---------------------------------------------------------------------------
// F-F — scanFile does not promote a dead root; only walkDir does
// ---------------------------------------------------------------------------

// f7DyingMountFS lets a directory listing succeed once (so a shallow
// tree's files all dispatch to the content-scan worker pool), then makes
// every Open AND every subsequent Stat(".") fail — simulating a mount that
// dies AFTER the walker listed it but BEFORE the workers could read its
// files. This is the exact gap DEFECT-G1's single-file scope opened: an
// agent that just confirmed a file exists (a Stat) can have the mount die
// before the very next Open.
type f7DyingMountFS struct {
	fs.FS
	mu   sync.Mutex
	dead bool
}

func (f *f7DyingMountFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	f.dead = true
	f.mu.Unlock()
	return nil, &fs.PathError{Op: "open", Path: name, Err: errMountGone}
}

func (f *f7DyingMountFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if rd, ok := f.FS.(fs.ReadDirFS); ok {
		return rd.ReadDir(name)
	}
	return fs.ReadDir(f.FS, name)
}

func (f *f7DyingMountFS) Stat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	dead := f.dead
	f.mu.Unlock()
	if dead {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: errMountGone}
	}
	if sf, ok := f.FS.(fs.StatFS); ok {
		return sf.Stat(name)
	}
	return fs.Stat(f.FS, name)
}

// TestFileGrep_MountDiesBetweenListingAndReading_ReportsRootLost pins
// finding F-F: before this fix, every worker's Open failure just
// incremented FilesSkippedProblems (the isolated-per-file path) and
// Truncated stayed FALSE — a confidently EMPTY answer for a root that
// genuinely died, the same class of failure DEFECT-G1 fixed for the
// walker's OWN Stat/ReadDir calls and left unfixed for the content
// scanner's Open.
func TestFileGrep_MountDiesBetweenListingAndReading_ReportsRootLost(t *testing.T) {
	dying := &f7DyingMountFS{FS: buildFS(map[string]string{
		"a.txt": "needle", "b.txt": "needle",
	})}
	res := mustSearch(t, oneRoot(dying), Options{Query: "needle"})

	if !res.Truncated || res.TruncatedReason != ReasonRootLost {
		t.Fatalf("want truncated root_lost (the mount died between listing and reading), "+
			"got truncated=%v reason=%v hits=%v — a lost root must never be a quiet empty "+
			"result (FR-021)", res.Truncated, res.TruncatedReason, pathsOf(res.Hits))
	}
}

// ---------------------------------------------------------------------------
// F-G — excerpt truncation is unmarked while context-line truncation gets …
// ---------------------------------------------------------------------------

// TestFileGrep_ExcerptTruncationIsMarked pins finding F-G: a match near the
// MIDDLE of a line far longer than ExcerptCapBytes loses content on BOTH
// sides of the returned window, and before this fix neither side carried
// any trace of that — a reader (human or agent) could not tell a truncated
// fragment from the complete line.
func TestFileGrep_ExcerptTruncationIsMarked(t *testing.T) {
	pad := strings.Repeat("x", ExcerptCapBytes*2)
	line := pad + "needle" + pad
	fsys := buildFS(map[string]string{"long.txt": line})

	res := mustSearch(t, oneRoot(fsys), Options{Query: "needle"})
	var hit *Hit
	for i := range res.Hits {
		if res.Hits[i].Kind == KindContent {
			hit = &res.Hits[i]
		}
	}
	if hit == nil {
		t.Fatalf("no content hit found in %+v", res.Hits)
	}
	if !strings.HasPrefix(hit.Excerpt, contextTruncationMarker) {
		t.Errorf("excerpt %q does not mark LEFT truncation (content existed before the window)", hit.Excerpt)
	}
	if !strings.HasSuffix(hit.Excerpt, contextTruncationMarker) {
		t.Errorf("excerpt %q does not mark RIGHT truncation (content existed after the window)", hit.Excerpt)
	}
	if !strings.Contains(hit.Excerpt, "needle") {
		t.Errorf("excerpt %q lost the match itself", hit.Excerpt)
	}
	if len(hit.Excerpt) > ExcerptCapBytes {
		t.Errorf("marked excerpt is %d bytes, exceeds ExcerptCapBytes=%d", len(hit.Excerpt), ExcerptCapBytes)
	}
}

// TestFileGrep_ShortExcerptIsNotMarked is the negative case: a line short
// enough to need no truncation at all must not gain spurious markers.
func TestFileGrep_ShortExcerptIsNotMarked(t *testing.T) {
	fsys := buildFS(map[string]string{"short.txt": "the needle is right here"})
	res := mustSearch(t, oneRoot(fsys), Options{Query: "needle"})
	var hit *Hit
	for i := range res.Hits {
		if res.Hits[i].Kind == KindContent {
			hit = &res.Hits[i]
		}
	}
	if hit == nil {
		t.Fatalf("no content hit found in %+v", res.Hits)
	}
	if strings.Contains(hit.Excerpt, contextTruncationMarker) {
		t.Errorf("excerpt %q carries a truncation marker for a line that fit entirely", hit.Excerpt)
	}
	if hit.Excerpt != "the needle is right here" {
		t.Errorf("excerpt = %q, want the whole short line verbatim", hit.Excerpt)
	}
}
