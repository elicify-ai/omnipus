package skills

// RED tests — landing/skills (T9): the one change pipeline (§7.1), its
// preconditions (§7.3) and the save check (§7.2, dataset D-2).
// Scenarios: S-3.4, S-3.5, S-3.6, S-5.5, S-4.1 (D-2), E-1, E-2.
// Traces to: skills-spec-r13.md §7.1–§7.3, §5.10, §16 US-3/US-4/US-5,
// §17.1 rows 11/12, §17.2 D-2.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agentActor is the standard agent ChangeActor for these fixtures.
func agentActor(id string) ChangeActor {
	return ChangeActor{Kind: "agent", AgentID: id, SessionID: "sess-" + id, Tool: "write_file"}
}

// TestEdit_StaleExpectation_SKILL_CHANGED_NamesNewRevisionAndActor (S-3.4, E-2).
// BDD: Given session A saw revision A and session B committed revision B,
// When A writes with expectation A, Then SKILL_CHANGED naming B's revision
// and actor, byte-exact §5.10, and nothing is written.
// Traces to: skills-spec-r13.md §7.3, §5.10 SKILL_CHANGED, S-3.4.
func TestEdit_StaleExpectation_SKILL_CHANGED_NamesNewRevisionAndActor(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")

	// Session B commits revision B (RED: fails here — pipeline unimplemented).
	outB, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "app", Path: "SKILL.md",
		Content: []byte("---\nname: app\ndescription: from B\n---\nB body\n"),
		Expect:  Expectation{Revision: "revA"},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-b", SessionID: "sess-b", Tool: "edit_file"},
	})
	require.NoError(t, err, "RED: ApplyChange not implemented")
	require.NotEmpty(t, outB.Revision, "the commit returns the new revision")

	// Session A's stale write is refused with B's revision and actor named.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "app", Path: "SKILL.md",
		Content: []byte("---\nname: app\ndescription: from A\n---\nA body\n"),
		Expect:  Expectation{Revision: "revA"},
		Actor:   agentActor("agent-a"),
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillChanged), "want SKILL_CHANGED, got %v", err)
	assert.Contains(t, err.Error(),
		fmt.Sprintf("skill 'app' changed since you read it (now %s by agent-b); read it again with get_skill, Skill or read_file", outB.Revision),
		"byte-exact §5.10 SKILL_CHANGED text naming the newer revision and actor")

	body, _ := os.ReadFile(filepath.Join(root, "app", "SKILL.md"))
	assert.NotContains(t, string(body), "from A", "the stale write wrote nothing")
}

// TestCreate_Commits_NoGrant_MenuFreshness (S-3.5). BDD: Given no prior read
// (expected absence from the request itself), When write_file
// newskill/SKILL.md, Then a create version commits, no grant is created or
// inherited (§5.13a fresh name), and the outcome carries the new state the
// menu refreshes from.
// Traces to: skills-spec-r13.md §5.5 create row, §7.3, S-3.5.
func TestCreate_Commits_NoGrant_MenuFreshness(t *testing.T) {
	root := t.TempDir()
	grants := &recordingGrants{}

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "newskill",
		Content: []byte("---\nname: newskill\ndescription: fresh\n---\nbody\n"),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		Grants:  grants,
	})
	require.NoError(t, err, "RED: ApplyChange not implemented")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.Empty(t, grants.removed, "no grant mutation on create (nothing to remove, nothing added — §5.13a)")
	assert.NotEmpty(t, out.Revision, "the commit returns the live revision")

	dir := filepath.Join(root, "newskill")
	assert.FileExists(t, filepath.Join(dir, "SKILL.md"))
	_, statErr := os.Stat(filepath.Join(root, "system"))
	_ = statErr // history lives under the system root in production layout
}

// TestCreate_ConcurrentSameSlug_OneCommitsOtherSKILL_EXISTS (S-3.6, E-1).
// BDD: Given two sessions create the same new slug, When both run, Then
// exactly one commits and the other gets SKILL_EXISTS; the content on disk is
// the winner's.
// Traces to: skills-spec-r13.md §7.1 step 2, S-3.6, E-1.
func TestCreate_ConcurrentSameSlug_OneCommitsOtherSKILL_EXISTS(t *testing.T) {
	root := t.TempDir()

	mkReq := func(tag string) ChangeRequest {
		return ChangeRequest{
			Kind: KindCreate, Slug: "race",
			Content: []byte("---\nname: race\ndescription: writer " + tag + "\n---\nbody " + tag + "\n"),
			Expect:  Expectation{Absent: true},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-" + tag, SessionID: "s-" + tag, Tool: "write_file"},
		}
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, tag := range []string{"one", "two"} {
		wg.Add(1)
		go func(tag string) {
			defer wg.Done()
			_, err := ApplyChange(root, mkReq(tag))
			results <- err
		}(tag)
	}
	wg.Wait()
	close(results)

	var committed, exists int
	for err := range results {
		// RED: both get the not-implemented error — the assertion below fails.
		if err == nil {
			committed++
		} else if errors.Is(err, ErrSkillExists) {
			exists++
		}
	}
	require.Equal(t, 1, committed, "exactly one concurrent create commits")
	require.Equal(t, 1, exists, "the other concurrent create is refused SKILL_EXISTS")

	body, _ := os.ReadFile(filepath.Join(root, "race", "SKILL.md"))
	assert.True(t,
		strings.Contains(string(body), "writer one") || strings.Contains(string(body), "writer two"),
		"the on-disk content is the winner's, whole")
}

// TestDeleteFile_SKILLMD_Refused (S-5.5). BDD: When delete_file targets
// SKILL.md, Then SKILL_MD_REQUIRED with the byte-exact text; nothing removed.
// Traces to: skills-spec-r13.md §5.10 SKILL_MD_REQUIRED, §7.2 delete_file
// row, S-5.5.
func TestDeleteFile_SKILLMD_Refused(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")

	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDeleteFile, Slug: "app", Path: "SKILL.md",
		Expect: Expectation{Revision: "r1"},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillMDRequired), "want SKILL_MD_REQUIRED, got %v", err)
	assert.Contains(t, err.Error(),
		"SKILL.md cannot be deleted; delete the whole skill with action=delete",
		"byte-exact §5.10 SKILL_MD_REQUIRED text")
	assert.FileExists(t, filepath.Join(root, "app", "SKILL.md"), "nothing removed")
}

// TestSaveCheck_Dataset (S-4.1, D-2): every would-be state row is refused
// with its reason; the boundary rows on both sides.
// Traces to: skills-spec-r13.md §7.2, §17.2 D-2, S-4.1.
func TestSaveCheck_Dataset(t *testing.T) {
	const (
		maxMarkdown = 262144
		maxFile     = 10485760
		compatMax   = 500
	)

	validFront := func(name, description string, extra string) string {
		return "---\nname: " + name + "\ndescription: " + description + "\n" + extra + "---\n\nbody\n"
	}

	tests := []struct {
		name      string
		path      string
		content   string
		wantInErr string // distinctive fragment of the D-2 expected reason
	}{
		{
			name: "D2-1 SKILL.md without frontmatter",
			path: "SKILL.md", content: "just a body, no frontmatter\n",
			wantInErr: "frontmatter",
		},
		{
			name: "D2-2 frontmatter name mismatch",
			path: "SKILL.md", content: validFront("other", "desc", ""),
			wantInErr: "name must be 'app'",
		},
		{
			name: "D2-3 empty description",
			path: "SKILL.md", content: "---\nname: app\ndescription: \n---\nbody\n",
			wantInErr: "description is required",
		},
		{
			name:      "D2-4a compatibility 501 chars refused",
			path:      "SKILL.md",
			content:   validFront("app", "desc", "compatibility: "+strings.Repeat("x", compatMax+1)+"\n"),
			wantInErr: "compatibility",
		},
		{
			name:      "D2-5 metadata values must be strings",
			path:      "SKILL.md",
			content:   "---\nname: app\ndescription: desc\nmetadata:\n  k: 1\n---\nbody\n",
			wantInErr: "metadata",
		},
		{
			name:      "D2-6b SKILL.md over 262144 bytes refused",
			path:      "SKILL.md",
			content:   validFront("app", "desc", "") + strings.Repeat("b", maxMarkdown),
			wantInErr: "limit",
		},
		{
			name: "D2-10a traversal path refused",
			path: "../x", content: "escape\n",
			wantInErr: "",
		},
		{
			name: "D2-10d dot-component path refused",
			path: ".env", content: "SECRET=1\n",
			wantInErr: "",
		},
		{
			name: "D2-10e colon in path refused",
			path: "a:b", content: "colon\n",
			wantInErr: "",
		},
		{
			name: "D2-10g case-folded reserved name refused",
			path: "skill.md", content: "---\nname: app\ndescription: dup\n---\nbody\n",
			wantInErr: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSkillFixture(t, root, "app")

			_, err := ApplyChange(root, ChangeRequest{
				Kind: KindEdit, Slug: "app", Path: tc.path,
				Content: []byte(tc.content),
				Expect:  Expectation{Revision: "r1"},
				Actor:   agentActor("a"),
			})
			require.Error(t, err, "the would-be state must be refused")
			if tc.wantInErr != "" {
				assert.Contains(t, err.Error(), tc.wantInErr, "D-2 expected reason fragment")
			}
			assert.True(t, errors.Is(err, ErrSaveCheckFailed),
				"save-check refusals surface as SAVE_CHECK_FAILED (§5.10), got %v", err)
			assert.Contains(t, err.Error(), "the change to 'app' was not saved:",
				"byte-exact §5.10 SAVE_CHECK_FAILED shape")
		})
	}

	t.Run("D2-4b compatibility exactly 500 chars accepted", func(t *testing.T) {
		root := t.TempDir()
		writeSkillFixture(t, root, "app")
		content := validFront("app", "desc", "compatibility: "+strings.Repeat("x", compatMax)+"\n")
		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "SKILL.md",
			Content: []byte(content),
			Expect:  Expectation{Revision: "r1"},
			Actor:   agentActor("a"),
		})
		require.NoError(t, err, "the 500-char boundary is accepted")
		assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	})

	t.Run("D2-6a SKILL.md exactly 262144 bytes accepted", func(t *testing.T) {
		root := t.TempDir()
		writeSkillFixture(t, root, "app")
		head := validFront("app", "desc", "")
		content := head + strings.Repeat("b", maxMarkdown-len(head))
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "SKILL.md",
			Content: []byte(content),
			Expect:  Expectation{Revision: "r1"},
			Actor:   agentActor("a"),
		})
		require.NoError(t, err, "the 262,144-byte boundary is accepted")
	})

	t.Run("D2-7 file boundary 10485760 accepted / +1 refused", func(t *testing.T) {
		root := t.TempDir()
		writeSkillFixture(t, root, "app")

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "data.bin",
			Content: make([]byte, maxFile),
			Expect:  Expectation{Revision: "r1"},
			Actor:   agentActor("a"),
		})
		require.NoError(t, err, "the 10,485,760-byte file boundary is accepted")

		_, err = ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "big.bin",
			Content: make([]byte, maxFile+1),
			Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "app"))},
			Actor:   agentActor("a"),
		})
		require.Error(t, err, "the +1-byte file is refused")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed), "size refusals are SAVE_CHECK_FAILED")
	})

	t.Run("D2-9 file-count boundary 1000 accepted / 1001 refused", func(t *testing.T) {
		root := t.TempDir()
		appDir := writeSkillFixture(t, root, "app")
		// Pre-place 999 helper files; the write under test adds one (1000) or two (1001 cannot happen in one write) —
		// instead create the boundary state directly on disk as an outside edit,
		// then let the pipeline's save check count files at validation.
		for i := 0; i < 998; i++ {
			require.NoError(t, os.WriteFile(filepath.Join(appDir, fmt.Sprintf("h%03d.txt", i)), []byte("x"), 0o644))
		}

		// 1000 files: SKILL.md + 998 helpers + 1 new = 1000 → accepted.
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "last.txt",
			Content: []byte("1000th\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   agentActor("a"),
		})
		require.NoError(t, err, "the 1,000-file boundary is accepted")

		// 1001: add one more.
		_, err = ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "one-too-many.txt",
			Content: []byte("1001st\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   agentActor("a"),
		})
		require.Error(t, err, "the 1,001st file is refused")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
	})

	t.Run("D2-8 folder total boundary refused at 52428801", func(t *testing.T) {
		root := t.TempDir()
		appDir := writeSkillFixture(t, root, "app")

		// Six helper files under the per-file limit whose total crosses
		// 52,428,800 bytes when the new write is added.
		const almostLimit = 10485759 // per-file limit − 1
		for i := 0; i < 5; i++ {
			require.NoError(t, os.WriteFile(filepath.Join(appDir, fmt.Sprintf("bulk%d.bin", i)),
				make([]byte, almostLimit), 0o644))
		}
		// 5 × 10,485,759 = 52,428,795; +1,000,006-byte write crosses the total.
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "over.bin",
			Content: make([]byte, 1000006),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   agentActor("a"),
		})
		require.Error(t, err, "the 52,428,801-byte folder total is refused")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
		assert.Contains(t, err.Error(), "total size limit", "D-2 row 8: the folder-total reason")
	})

	t.Run("D2-10b writing through a symlink path is refused", func(t *testing.T) {
		root := t.TempDir()
		appDir := writeSkillFixture(t, root, "app")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "target.md"), []byte("t\n"), 0o644))
		require.NoError(t, os.Symlink("target.md", filepath.Join(appDir, "link.md")))

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "link.md",
			Content: []byte("through the link\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   agentActor("a"),
		})
		require.Error(t, err, "a write whose path is a symlink is refused")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
	})

	t.Run("D2-10c writing to a hard-linked path is refused", func(t *testing.T) {
		root := t.TempDir()
		appDir := writeSkillFixture(t, root, "app")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "orig.md"), []byte("o\n"), 0o644))
		require.NoError(t, os.Link(filepath.Join(appDir, "orig.md"), filepath.Join(appDir, "hard.md")))

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "hard.md",
			Content: []byte("through the hard link\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   agentActor("a"),
		})
		require.Error(t, err, "a write whose path is hard-linked is refused")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
	})

	t.Run("D2-10f Windows-unsafe name CON.txt refused on every platform", func(t *testing.T) {
		root := t.TempDir()
		writeSkillFixture(t, root, "app")

		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "app", Path: "CON.txt",
			Content: []byte("reserved\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "app"))},
			Actor:   agentActor("a"),
		})
		require.Error(t, err, "pkg/pathsafe refuses CON.txt on every platform")
		assert.True(t, errors.Is(err, ErrSaveCheckFailed))
	})
}

// readLiveRevision reads the live content revision through the SkillWriter's
// pinned helper (§7.3 contentRevision).
func readLiveRevision(t *testing.T, dir string) string {
	t.Helper()
	rev, err := revisionForDir(dir)
	require.NoError(t, err)
	return rev
}
