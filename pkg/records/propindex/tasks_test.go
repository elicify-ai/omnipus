// Omnipus — FR-076a: `kind: task` is served from an INDEXED checkbox row, not a walk.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"reflect"
	"testing"
)

// TestExtractTasks_MatchesTheShippingDefinitionOfATask.
//
// FR-076a replaces the WALK, not the definition. `knowledge_tasks` walks the
// collection, reads each file and matches one regex per line; a checkbox it
// returned and `vault_find` did not would be a silent behaviour change dressed
// as an optimisation. The cases below are the ones that regex actually decides,
// including the ones a "tidier" implementation would get wrong.
func TestExtractTasks_MatchesTheShippingDefinitionOfATask(t *testing.T) {
	src := "---\ntitle: A note\n---\n" + // 3 lines of frontmatter
		"- [ ] water the ferns\n" + // 4
		"* [x] repot the aloe\n" + // 5
		"+ [X] prune\n" + // 6
		"    - [ ] indented, still a task\n" + // 7
		"\t- [ ] tab-indented\n" + // 8
		"- [] not a checkbox\n" + // 9  (no space between brackets)
		"- [-] not a checkbox either\n" + // 10 (only space/x/X)
		"-[ ] no space after the bullet\n" + // 11
		"text - [ ] mid-line\n" + // 12 (must be at line start)
		"- [ ]   trailing spaces trimmed   \n" // 13

	want := []TaskRow{
		{Line: 4, Status: TaskOpen, Text: "water the ferns"},
		{Line: 5, Status: TaskDone, Text: "repot the aloe"},
		{Line: 6, Status: TaskDone, Text: "prune"},
		{Line: 7, Status: TaskOpen, Text: "indented, still a task"},
		{Line: 8, Status: TaskOpen, Text: "tab-indented"},
		{Line: 13, Status: TaskOpen, Text: "trailing spaces trimmed"},
	}
	got := ExtractTasks([]byte(src))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("checkbox extraction diverged from the shipping definition:\n  got:  %#v\n  want: %#v", got, want)
	}
	if len(ExtractTasks(nil)) != 0 {
		t.Error("an empty file produced tasks")
	}
}

// TestExtractTasks_LineNumbersCountFromTheFirstByte.
//
// A task row must render with its LINE, and the line the operator's editor shows
// counts the frontmatter block. An extractor that numbered from the body would
// send every reader to the wrong line of their own note, consistently, by the
// length of the frontmatter.
func TestExtractTasks_LineNumbersCountFromTheFirstByte(t *testing.T) {
	got := ExtractTasks([]byte("---\na: 1\nb: 2\n---\n\n- [ ] first\n"))
	if len(got) != 1 {
		t.Fatalf("expected one task, got %#v", got)
	}
	if got[0].Line != 6 {
		t.Errorf("line %d, want 6 — counted from the first byte of the file, frontmatter included", got[0].Line)
	}
}

// TestTasks_AreServedFromTheIndexWithTheirNoteContext.
//
// FR-076a: a checkbox is its own row carrying path, line, status, text and the
// source_hash every other row carries — so freshness, bounds, paging and
// rendering apply to it unchanged.
func TestTasks_AreServedFromTheIndexWithTheirNoteContext(t *testing.T) {
	store, _ := openIndex(t, Options{})
	src := "---\ntype: plant\nid: PL-0055\nspecies: Fern\n---\n\n- [ ] mist\n- [x] feed\n"
	rows := note(t, "garden/fern.md", plantSchema(t), src)
	mustUpsert(t, store, rows,
		note(t, "elsewhere/other.md", nil, "- [ ] not in scope\n"))

	var got []TaskHit
	if err := store.Tasks(context.Background(), Selector{PathPrefix: "garden/"}, func(h TaskHit) error {
		got = append(got, h)
		return nil
	}); err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the two checkboxes inside the scope, got %d: %#v", len(got), got)
	}
	for _, h := range got {
		if h.Path != "garden/fern.md" {
			t.Errorf("a task escaped its scope: %#v", h)
		}
		if h.SourceHash != rows.SourceHash {
			t.Errorf("a task row lost its note's freshness token: %#v", h)
		}
		if h.Task.Text == "" || h.Task.Line == 0 {
			t.Errorf("a task row cannot be rendered: %#v", h)
		}
	}
}

// TestTasks_ScopeUsesAnEscapedPrefix.
//
// The LIKE pattern is built here, from an already-resolved root. A path
// containing a wildcard character must therefore match itself and nothing else —
// an unescaped `_` in a folder name would silently widen the scope by one
// character in every position, which is a scope leak that looks like a correct
// answer (FR-060, FR-061).
func TestTasks_ScopeUsesAnEscapedPrefix(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t,
		store,
		note(t, "a_b/inside.md", nil, "- [ ] inside\n"),
		note(t, "axb/outside.md", nil, "- [ ] outside\n"),
		note(t, "100%/pct.md", nil, "- [ ] percent\n"),
	)

	for _, tc := range []struct{ prefix, want string }{
		{"a_b/", "inside"},
		{"100%/", "percent"},
	} {
		var got []string
		if err := store.Tasks(context.Background(), Selector{PathPrefix: tc.prefix}, func(h TaskHit) error {
			got = append(got, h.Task.Text)
			return nil
		}); err != nil {
			t.Fatalf("Tasks(%q): %v", tc.prefix, err)
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("scope %q returned %v, want exactly [%q] — the LIKE metacharacter was not escaped",
				tc.prefix, got, tc.want)
		}
	}
}

// TestTasks_TheVisitorsErrorStopsTheStream.
func TestTasks_TheVisitorsErrorStopsTheStream(t *testing.T) {
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/many.md", nil, "- [ ] a\n- [ ] b\n- [ ] c\n"))

	boom := errNoMore
	seen := 0
	err := store.Tasks(context.Background(), Selector{}, func(TaskHit) error {
		seen++
		return boom
	})
	if err == nil || seen != 1 {
		t.Errorf("the stream ignored its visitor: err=%v seen=%d", err, seen)
	}
}

var errNoMore = errStr("enough")

type errStr string

func (e errStr) Error() string { return string(e) }
