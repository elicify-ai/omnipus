// Omnipus — regression coverage for a REOPENING of F-9
// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md),
// introduced by this branch's instant-indexing path.
//
// THE DEFECT THIS FILE GUARDS: pkg/knowledge/author.go's
// refreshIndexesForNote calls knowledge.OpenIndex to keep the text index
// current after an agent's own write. OpenIndex CREATES the index directory
// — and, on a first-ever open, an empty index — as a side effect of opening
// it, regardless of whether the collection has ever been swept.
// Index.UpdatePath then SAVES a manifest holding exactly the one note just
// written. findTextSearcher.Populated (find_tool.go, this package) answers
// knowledgefind's "has this index ever finished a build" question from the
// MANIFEST'S EXISTENCE alone — which is exactly right for a collection the
// boot sweep indexed, and exactly wrong here: a single knowledge_edit
// op:create on a collection nobody has ever indexed flips Populated() from
// false to true, and every OTHER, still-never-indexed note in that
// collection becomes invisible to a `words=` search that now answers
// "complete: true, 0 rows" instead of refusing. Byte for byte, F-9's
// original defect, reopened by a different code path.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestF9Reopened' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// Fixtures. Prefixed f9 so they cannot collide with this package's other
// test files.
// ---------------------------------------------------------------------------

var f9Seq atomic.Uint64

// f9Home returns a fresh $OMNIPUS_HOME, real-path resolved so a macOS
// /var -> /private/var symlink cannot make an otherwise-correct assertion
// fail (the same reasoning pkg/knowledge's own b5Real/a4Real apply).
func f9Home(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return filepath.Clean(resolved)
}

// f9Workspace seeds a minimal valid workspace record and returns its id.
func f9Workspace(t *testing.T, home string) string {
	t.Helper()
	id := "f9ws-" + strconv.FormatUint(f9Seq.Add(1), 10)
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: id, Name: id, Status: "active", CreatedAt: now, UpdatedAt: now,
	}))
	return id
}

// f9Ctx is the tool context the agent loop installs for one turn.
func f9Ctx(agentID, workspaceID string) context.Context {
	return tools.WithWorkspaceID(tools.WithAgentID(context.Background(), agentID), workspaceID)
}

// f9Note writes a note inside a collection.
func f9Note(t *testing.T, root, relPath, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
}

// ---------------------------------------------------------------------------
// The reproduction.
// ---------------------------------------------------------------------------

// TestF9Reopened_OneWriteToNeverIndexedCollectionMustNotFakeACompleteZero is
// the reproduction the task requires be shown red on unfixed code before any
// fix lands, and green after.
//
// Sequence: build a collection with several notes and NO index anywhere
// (never OpenIndex'd, never Sync'd); perform exactly ONE knowledge_edit
// op:create through the real EditTool; then run a real knowledge_find
// words= query, through the real FindTool, for a term that lives only in
// one of the OTHER notes — one the create never touched. The answer must be
// a refusal ("the text index has never finished indexing this vault"), not
// a success carrying zero rows.
func TestF9Reopened_OneWriteToNeverIndexedCollectionMustNotFakeACompleteZero(t *testing.T) {
	home := f9Home(t)
	ws := f9Workspace(t, home)

	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()

	// Several notes on disk, none of them ever indexed — no OpenIndex, no
	// Sync, for this collection root, ever, before the write below.
	const untouchedTerm = "zephyrhoncho"
	f9Note(t, root, "one.md", "# One\n\nsomething about "+untouchedTerm+" right here.\n")
	f9Note(t, root, "two.md", "# Two\n\nunrelated content.\n")
	f9Note(t, root, "three.md", "# Three\n\nmore unrelated content.\n")

	// Precondition: genuinely never indexed. If this fails, the test below
	// would pass or fail for a reason unrelated to the defect it exists to
	// catch.
	dir, derr := knowledge.IndexDirFor(home, root)
	require.NoError(t, derr)
	manifestExists, existsErr := knowledge.ManifestExists(filepath.Join(dir, knowledge.ManifestFileName))
	require.NoError(t, existsErr)
	require.False(t, manifestExists,
		"test precondition failed: a manifest already exists for this collection before any write")

	// ONE knowledge_edit create, through the REAL tool — the instant-indexing
	// path under test. No `collection` argument: exactly one collection is in
	// scope, so an empty ref selects it (Scope.Select's own documented rule).
	editDeps := knowledge.AuthoringDeps{
		Home:  home,
		Audit: knowledge.AuthorAuditFunc(func(knowledge.AuthorAuditRecord) {}),
	}
	editTool := knowledge.NewEditTool(editDeps)
	editRes := editTool.Execute(f9Ctx("mia", ws), map[string]any{
		"op": "create", "path": "New.md", "body": "# New\n\na brand new note.\n",
	})
	require.NotNil(t, editRes)
	require.False(t, editRes.IsError, "the create itself must succeed: %s", editRes.ForLLM)

	// Now the regression check: a words= search for a term that lives ONLY in
	// one.md — a note the create above never touched, in a collection that
	// has still never been through a real Sync — must refuse, not answer a
	// confident zero.
	findTool := NewFindTool(home)
	findRes := findTool.Execute(f9Ctx("mia", ws), map[string]any{"words": untouchedTerm})
	require.NotNil(t, findRes)

	if !findRes.IsError {
		t.Fatalf("F-9 REOPENED: a words= search over a collection where only the ONE note just "+
			"created (of four total) has ever reached the index answered success instead of "+
			"refusing.\ngot: %s", findRes.ForLLM)
	}
	if !strings.Contains(findRes.ForLLM, "never finished indexing") {
		t.Fatalf("expected the text-index-not-populated refusal wording; got:\n%s", findRes.ForLLM)
	}
}
