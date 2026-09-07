// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"strings"
	"testing"
)

// TestFileGrep_GitignoreSemantics pins FR-007/DS-3: nested .gitignore files,
// negation, a non-git tree (no .git directory at all — gitignore semantics
// apply regardless), and pruned-count observability. The walker also reads
// .gitignore files even when hidden files are excluded from results.
func TestFileGrep_GitignoreSemantics(t *testing.T) {
	t.Run("top-level gitignore prunes a subtree", func(t *testing.T) {
		files := map[string]string{
			".gitignore":       "build/\n",
			"build/output.txt": "needle here",
			"src/main.txt":     "needle here too",
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle"})
		if len(res.Hits) != 1 || res.Hits[0].Path != "src/main.txt" {
			t.Fatalf("want exactly one hit in src/main.txt, got %+v", res.Hits)
		}
		if res.Stats.FilesPrunedIgnored == 0 {
			t.Fatal("want FilesPrunedIgnored > 0")
		}
	})

	t.Run("nested gitignore only prunes within its own subtree", func(t *testing.T) {
		files := map[string]string{
			"pkg/a/.gitignore":    "generated.txt\n",
			"pkg/a/generated.txt": "needle",
			"pkg/a/real.txt":      "needle",
			"pkg/b/generated.txt": "needle", // NOT pruned: different subtree
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle"})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if got["pkg/a/generated.txt"] {
			t.Fatal("pkg/a/generated.txt should be pruned by pkg/a/.gitignore")
		}
		if !got["pkg/a/real.txt"] {
			t.Fatal("pkg/a/real.txt should still be found")
		}
		if !got["pkg/b/generated.txt"] {
			t.Fatal("pkg/b/generated.txt should NOT be pruned by a sibling's .gitignore")
		}
	})

	t.Run("negation un-ignores a specific file", func(t *testing.T) {
		files := map[string]string{
			".gitignore": "*.log\n!keep.log\n",
			"debug.log":  "needle",
			"keep.log":   "needle",
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle"})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if got["debug.log"] {
			t.Fatal("debug.log should be pruned by *.log")
		}
		if !got["keep.log"] {
			t.Fatal("keep.log should be un-ignored by the negation")
		}
	})

	t.Run("gitignore semantics apply with no .git directory at all", func(t *testing.T) {
		// git-ness is irrelevant (FR-007): a plain folder with a .gitignore
		// but no .git/ still gets pruning.
		files := map[string]string{
			".gitignore": "skip.txt\n",
			"skip.txt":   "needle",
			"keep.txt":   "needle",
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle"})
		if len(res.Hits) != 1 || res.Hits[0].Path != "keep.txt" {
			t.Fatalf("want only keep.txt, got %+v", res.Hits)
		}
	})

	t.Run("gitignore is read even when hidden files are excluded from results", func(t *testing.T) {
		files := map[string]string{
			".gitignore": "skip.txt\n",
			"skip.txt":   "needle",
			"keep.txt":   "needle",
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle", IncludeHidden: false})
		if len(res.Hits) != 1 || res.Hits[0].Path != "keep.txt" {
			t.Fatalf(".gitignore itself is hidden but must still be read; want only keep.txt, got %+v", res.Hits)
		}
	})

	t.Run(".ignore file is honored alongside .gitignore", func(t *testing.T) {
		files := map[string]string{
			".ignore":  "skip.txt\n",
			"skip.txt": "needle",
			"keep.txt": "needle",
		}
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle"})
		if len(res.Hits) != 1 || res.Hits[0].Path != "keep.txt" {
			t.Fatalf("want only keep.txt via .ignore, got %+v", res.Hits)
		}
	})
}

// TestFileGrep_HiddenAndAlwaysPruned pins R2-MIN-006: include_hidden on/off,
// and .git/.library/.omnipus-vault never scanned regardless of the flag.
func TestFileGrep_HiddenAndAlwaysPruned(t *testing.T) {
	files := map[string]string{
		".git/config":             "needle",
		".library/index.json":     "needle",
		".omnipus-vault/notes.md": "needle",
		".hidden-dotfile.txt":     "needle",
		"visible.txt":             "needle",
	}

	t.Run("hidden excluded by default, always-pruned still pruned", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle"})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if got[".git/config"] || got[".library/index.json"] || got[".omnipus-vault/notes.md"] {
			t.Fatalf("always-pruned dirs must never be scanned: %+v", res.Hits)
		}
		if got[".hidden-dotfile.txt"] {
			t.Fatal("hidden dotfile should be excluded when IncludeHidden is false")
		}
		if !got["visible.txt"] {
			t.Fatal("visible.txt should be found")
		}
	})

	t.Run("include_hidden surfaces user dotfiles but not always-pruned dirs", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle", IncludeHidden: true})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if got[".git/config"] || got[".library/index.json"] || got[".omnipus-vault/notes.md"] {
			t.Fatalf("always-pruned dirs must never be scanned even with IncludeHidden: %+v", res.Hits)
		}
		if !got[".hidden-dotfile.txt"] {
			t.Fatal("hidden dotfile should be included when IncludeHidden is true")
		}
	})
}

// TestFileGrep_GlobIncludeExclude pins US-3: doublestar ** glob filtering.
func TestFileGrep_GlobIncludeExclude(t *testing.T) {
	files := map[string]string{
		"src/main.go":     "needle",
		"src/util.go":     "needle",
		"src/sub/deep.go": "needle",
		"docs/readme.md":  "needle",
		"vendor/lib/x.go": "needle",
	}

	t.Run("include glob restricts to matching paths", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(files)), Options{
			Query:        "needle",
			IncludeGlobs: []string{"src/**/*.go"},
		})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if !got["src/main.go"] || !got["src/util.go"] || !got["src/sub/deep.go"] {
			t.Fatalf("want all src/**/*.go files, got %+v", res.Hits)
		}
		if got["docs/readme.md"] || got["vendor/lib/x.go"] {
			t.Fatalf("include glob should exclude non-matching paths, got %+v", res.Hits)
		}
	})

	t.Run("exclude glob removes matching paths even if included", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(files)), Options{
			Query:        "needle",
			IncludeGlobs: []string{"**/*.go"},
			ExcludeGlobs: []string{"vendor/**"},
		})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if got["vendor/lib/x.go"] {
			t.Fatal("vendor/lib/x.go should be excluded")
		}
		if !got["src/main.go"] {
			t.Fatal("src/main.go should still match")
		}
	})

	t.Run("no include globs means everything not excluded is allowed", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(files)), Options{
			Query:        "needle",
			ExcludeGlobs: []string{"docs/**"},
		})
		got := map[string]bool{}
		for _, h := range res.Hits {
			got[h.Path] = true
		}
		if got["docs/readme.md"] {
			t.Fatal("docs/readme.md should be excluded")
		}
		if !got["vendor/lib/x.go"] {
			t.Fatal("vendor/lib/x.go should still be found (no include restriction)")
		}
	})
}

// TestFileGrep_ContextLines pins US-3 AS-4: context_lines carries up to N
// lines before/after, clamped at file boundaries.
func TestFileGrep_ContextLines(t *testing.T) {
	content := "line1\nline2\nline3\nneedle line4\nline5\nline6\nline7\n"
	files := map[string]string{"f.txt": content}

	t.Run("interior match gets full context window", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(files)), Options{Query: "needle", ContextLines: 2})
		if len(res.Hits) != 1 {
			t.Fatalf("want 1 hit, got %d", len(res.Hits))
		}
		h := res.Hits[0]
		if h.Line != 4 {
			t.Fatalf("want match on line 4, got %d", h.Line)
		}
		wantBefore := []string{"line2", "line3"}
		wantAfter := []string{"line5", "line6"}
		if !equalStrings(h.ContextBefore, wantBefore) {
			t.Fatalf("ContextBefore = %v, want %v", h.ContextBefore, wantBefore)
		}
		if !equalStrings(h.ContextAfter, wantAfter) {
			t.Fatalf("ContextAfter = %v, want %v", h.ContextAfter, wantAfter)
		}
	})

	t.Run("match near start of file clamps ContextBefore", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": "needle first\nline2\nline3\n",
		})), Options{Query: "needle", ContextLines: 3})
		h := res.Hits[0]
		if len(h.ContextBefore) != 0 {
			t.Fatalf("want no before-context at file start, got %v", h.ContextBefore)
		}
		if !equalStrings(h.ContextAfter, []string{"line2", "line3"}) {
			t.Fatalf("ContextAfter = %v", h.ContextAfter)
		}
	})

	t.Run("match near end of file clamps ContextAfter", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": "line1\nline2\nneedle last\n",
		})), Options{Query: "needle", ContextLines: 3})
		h := res.Hits[0]
		if !equalStrings(h.ContextBefore, []string{"line1", "line2"}) {
			t.Fatalf("ContextBefore = %v", h.ContextBefore)
		}
		if len(h.ContextAfter) != 0 {
			t.Fatalf("want no after-context at file end, got %v", h.ContextAfter)
		}
	})

	t.Run("context does not cross a file boundary", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"a.txt": "line1\nneedle a\n",
			"b.txt": "needle b\nline2\n",
		})), Options{Query: "needle", ContextLines: 5})
		for _, h := range res.Hits {
			for _, c := range h.ContextBefore {
				if c == "line1" && h.Path != "a.txt" {
					t.Fatalf("context leaked across file boundary into %s", h.Path)
				}
			}
		}
	})

	t.Run("adjacent matching line is not skipped as context_after", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": "foo\nfoo\nbar\n",
		})), Options{Query: "foo", ContextLines: 2})
		if len(res.Hits) != 2 {
			t.Fatalf("want 2 hits, got %d: %+v", len(res.Hits), res.Hits)
		}
		h1, h2 := res.Hits[0], res.Hits[1]
		if h1.Line != 1 || h2.Line != 2 {
			t.Fatalf("want hits on lines 1 and 2, got %d and %d", h1.Line, h2.Line)
		}
		if !equalStrings(h1.ContextAfter, []string{"foo", "bar"}) {
			t.Fatalf("hit 1 (line 1) ContextAfter = %v, want [foo bar] (line 2's own content, then line 3)", h1.ContextAfter)
		}
		if !equalStrings(h2.ContextBefore, []string{"foo"}) {
			t.Fatalf("hit 2 (line 2) ContextBefore = %v, want [foo] (line 1's content)", h2.ContextBefore)
		}
		if !equalStrings(h2.ContextAfter, []string{"bar"}) {
			t.Fatalf("hit 2 (line 2) ContextAfter = %v, want [bar]", h2.ContextAfter)
		}
	})

	t.Run("a huge adjacent line is capped the same way an excerpt is", func(t *testing.T) {
		huge := strings.Repeat("x", 200_000)
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": huge + "\nneedle line\n" + huge + "\n",
		})), Options{Query: "needle", ContextLines: 1})
		if len(res.Hits) != 1 {
			t.Fatalf("want 1 hit, got %d", len(res.Hits))
		}
		h := res.Hits[0]
		if len(h.ContextBefore) != 1 || len(h.ContextBefore[0]) > ExcerptCapBytes {
			t.Fatalf("ContextBefore[0] length = %d, want <= ExcerptCapBytes (%d)", len(h.ContextBefore[0]), ExcerptCapBytes)
		}
		if len(h.ContextAfter) != 1 || len(h.ContextAfter[0]) > ExcerptCapBytes {
			t.Fatalf("ContextAfter[0] length = %d, want <= ExcerptCapBytes (%d)", len(h.ContextAfter[0]), ExcerptCapBytes)
		}
	})

	t.Run("context_lines clamps to 0..5", func(t *testing.T) {
		m, err := compile(Options{Query: "x", ContextLines: 99})
		if err != nil {
			t.Fatal(err)
		}
		if m.contextN != 5 {
			t.Fatalf("want contextN clamped to 5, got %d", m.contextN)
		}
		m2, err := compile(Options{Query: "x", ContextLines: -3})
		if err != nil {
			t.Fatal(err)
		}
		if m2.contextN != 0 {
			t.Fatalf("want contextN clamped to 0, got %d", m2.contextN)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFileGrep_HitGranularity pins MV-14: a hit is ONE matching line (first
// match position); two matches on one line still count as one hit; a name
// match is one hit with KindName and Line 0.
func TestFileGrep_HitGranularity(t *testing.T) {
	t.Run("two matches on one line yield one hit", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"f.txt": "needle needle needle\n",
		})), Options{Query: "needle"})
		if len(res.Hits) != 1 {
			t.Fatalf("want exactly 1 hit for a line with 3 occurrences, got %d", len(res.Hits))
		}
		if res.Hits[0].Line != 1 {
			t.Fatalf("want line 1, got %d", res.Hits[0].Line)
		}
	})

	t.Run("name match is one hit with KindName and Line 0", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"needle.txt": "no content match here",
		})), Options{Query: "needle"})
		if len(res.Hits) != 1 {
			t.Fatalf("want exactly 1 hit, got %d", len(res.Hits))
		}
		h := res.Hits[0]
		if h.Kind != KindName {
			t.Fatalf("want KindName, got %v", h.Kind)
		}
		if h.Line != 0 {
			t.Fatalf("want Line 0 for a name match, got %d", h.Line)
		}
	})

	t.Run("a file can contribute both a name hit and content hits", func(t *testing.T) {
		res := mustSearch(t, oneRoot(buildFS(map[string]string{
			"needle.txt": "the needle is also in the content\n",
		})), Options{Query: "needle"})
		if len(res.Hits) != 2 {
			t.Fatalf("want 1 name hit + 1 content hit = 2, got %d: %+v", len(res.Hits), res.Hits)
		}
		kinds := make([]MatchKind, 0, len(res.Hits))
		for _, h := range res.Hits {
			kinds = append(kinds, h.Kind)
		}
		if kinds[0] != KindName || kinds[1] != KindContent {
			t.Fatalf("want [name, content] order (Line 0 sorts first), got %v", kinds)
		}
	})
}
