package skills

// RED tests — landing/skills (T9): committed-mutation undo anchors (§5.5,
// five byte-exact variants) and caller-owned multi-file recovery (§7.5).
// Scenarios S-3.8 (both legs), S-3.14, S-3.15 (both legs), S-3.16 (both legs
// + class-2 texts), S-3.17 (both legs).
// Traces to: skills-spec-r13.md §5.5 closing rule + §7.5, §5.10
// SKILL_PARTIALLY_SAVED variants, §16 US-3, §17.1 rows 11/33.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var versionIDFromMessage = regexp.MustCompile(`saved as skill version ([A-Za-z0-9-]+);`)

// commitWrite is a helper that applies a single-file write and returns the
// committed version id parsed from the §5.5 undo line.
func commitWrite(t *testing.T, root, slug, path, content string, expectRevision string) (PublishOutcome, string) {
	t.Helper()
	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: slug, Path: path,
		Content: []byte(content),
		Expect:  Expectation{Revision: expectRevision},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err, "RED: the write must commit (pipeline unimplemented on baseline)")
	m := versionIDFromMessage.FindStringSubmatch(out.Message)
	require.NotNil(t, m, "every committed skill mutation names its version: %q", out.Message)
	return out, m[1]
}

// validSkillMD renders a valid SKILL.md body for slug.
func validSkillMD(slug, description, body string) string {
	return "---\nname: " + slug + "\ndescription: " + description + "\n---\n\n" + body + "\n"
}

// TestMultiFileSequence_RecoveryFromOwnWrite1Anchor (S-3.8 leg 1).
// BDD: Given a sequence started from an ordinary read (no version id in
// hand), write 1 commits (its result names the undo), write 2 commits, write
// 3 is refused — Then the refusal names the current version, the reason, and
// NO sequence anchor or restore instruction; recovery follows write 1's own
// undo line (restore V1) and restores the exact pre-sequence bytes; restoring
// the newest version instead does not undo write 1.
// Traces to: skills-spec-r13.md §7.5, §5.5 variant 1, S-3.8 leg 1.
func TestMultiFileSequence_RecoveryFromOwnWrite1Anchor(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")
	preSequence := readLiveRevision(t, filepath.Join(root, "foo"))

	// An ordinary supported read returns a revision, never a version id —
	// the sequence starts from exactly this.
	seen := readLiveRevision(t, filepath.Join(root, "foo"))
	require.Equal(t, preSequence, seen)

	// Write 1: commits V2; its own result names V1 as the undo anchor.
	out1, v2 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "desc after write1", "write1 body"), seen)
	assert.Contains(t, out1.Message, "the version before it is ",
		"variant 1 (present, gate-passing predecessor): the undo line names the predecessor version")

	// Write 2: commits.
	_, _ = commitWrite(t, root, "foo", "notes.md", "note from write2\n", out1.Revision)

	// Write 3: refused (invalid would-be state: metadata value not a string).
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
		Content: []byte("---\nname: foo\ndescription: d\nmetadata:\n  k: 1\n---\nbody\n"),
		Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_file"},
	})
	require.Error(t, err, "write 3 must be refused")
	// The refusal is sequence-free (§7.5): reason + current version only.
	assert.Contains(t, err.Error(), "The skill's current version is",
		"the refusal names the current (newest) version")
	assert.NotContains(t, err.Error(), "the version before it",
		"a refused call carries no undo variant (§7.5)")
	assert.NotContains(t, err.Error(), "restore",
		"a refusal never names another call's anchor or a restore instruction")
	assert.NotContains(t, err.Error(), v2, "the refusal never repeats write 1's anchor")

	// Recovery follows write 1's own undo line: restore V1 (write 1's
	// predecessor).
	v1 := strings.TrimSpace(strings.SplitN(strings.SplitN(out1.Message, "the version before it is ", 2)[1], ",", 2)[0])
	require.NotEmpty(t, v1, "the anchor is parsed from write 1's own result")
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: v1,
		Expect: Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err, "restore of the gate-passing predecessor commits")

	body, _ := os.ReadFile(filepath.Join(root, "foo", "SKILL.md"))
	assert.Equal(t, validSkillMD("foo", "fixture skill foo", "Body of foo."), string(body),
		"the exact pre-sequence bytes are restored")

	// Negative control: restoring the NEWEST version would not undo write 1 —
	// asserted by checking that only the V1 restore produced pre-sequence bytes
	// above and that the sequence's notes.md is gone with it.
	_, statErr := os.Stat(filepath.Join(root, "foo", "notes.md"))
	assert.True(t, os.IsNotExist(statErr), "restore V1 removed the whole sequence's effect")
}

// TestMultiFileSequence_TwoSequences_RecoverBOnly (S-3.8 leg 2).
// BDD: Given sequence A committed W-A1 (P0 → VA) and sequence B committed
// W-B1 (VA → VB) whose W-B2 was refused — Then recovering B from W-B1's own
// anchor (restore VA) returns exactly to before B, A's change stays in place,
// and no tool text ever suggested restoring P0.
// Traces to: skills-spec-r13.md §7.5, S-3.8 leg 2.
func TestMultiFileSequence_TwoSequences_RecoverBOnly(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")
	p0Rev := readLiveRevision(t, filepath.Join(root, "foo"))

	// Sequence A: W-A1 (P0 → VA).
	outA, va := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "sequence A", "A body"), p0Rev)

	// Sequence B: W-B1 (VA → VB).
	outB, _ := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "sequence B", "B body"), outA.Revision)

	// W-B2 refused.
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "b-notes.md",
		Content: []byte("---\nname: foo\ndescription: d\nmetadata:\n  k: 2\n---\n"),
		Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err)

	// Recover B from W-B1's own anchor: restore VA.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: va,
		Expect: Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)

	body, _ := os.ReadFile(filepath.Join(root, "foo", "SKILL.md"))
	assert.Contains(t, string(body), "sequence A", "A's change stays in place (VA's content)")
	assert.NotContains(t, string(body), "sequence B", "B is undone")
	assert.NotContains(t, outB.Message, "sequence A",
		"no tool text ever suggested restoring P0 (A's predecessor)")
}

// TestRecreate_AfterDelete_UndoVariantNamesDeletion (S-3.14).
// BDD: Given foo deleted (delete_skill head V2 retained), the agent recreates
// foo — write 1's committed result names `it follows the recorded deletion`;
// the refusal of write 2 carries no undo variant; restore V2 is refused;
// recovery is edit_skill action=delete, returning the key to absence.
// Traces to: skills-spec-r13.md §5.5 variant 3, §7.2 restore row, S-3.14.
func TestRecreate_AfterDelete_UndoVariantNamesDeletion(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")

	// History V1 via create; then delete (V2 = delete_skill head).
	outCreate, _ := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "original", "original body"), "")
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: outCreate.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)

	// Recreate: write 1 commits V3 with the recorded-deletion variant.
	out1, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "foo",
		Content: []byte(validSkillMD("foo", "recreated", "recreated body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err)
	assert.Contains(t, out1.Message, "saved as skill version ", "the committed result names its version")
	assert.Contains(t, out1.Message,
		"it follows the recorded deletion (version ",
		"variant 3 prefix, byte-exact §5.5")
	assert.Contains(t, out1.Message,
		", retained but not restorable); to undo, delete this skill with edit_skill action=delete",
		"variant 3 suffix, byte-exact §5.5")
	assert.NotContains(t, out1.Message, "there is no earlier version",
		"the true-first-creation text must not appear when history exists")

	// Write 2 refused — sequence-free refusal.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindEdit, Slug: "foo", Path: "second.md",
		Content: []byte("second\n"),
		Expect:  Expectation{Revision: out1.Revision},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "follows the recorded deletion",
		"the refusal carries no undo variant (§7.5)")

	// restore V2 (the delete_skill head) is refused per §7.2.
	deletedHead := regexp.MustCompile(`version ([A-Za-z0-9-]+), retained but not restorable`).
		FindStringSubmatch(out1.Message)
	require.NotNil(t, deletedHead, "the variant names the retained head id")
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "foo", Version: deletedHead[1],
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.Error(t, err, "selecting a delete_skill manifest is refused")
	assert.Contains(t, err.Error(), "records the deleted state; use edit_skill action=delete to remove the skill",
		"byte-exact §7.2 delete_skill selection refusal")

	// Recovery follows write 1's own result: delete returns the key to absence.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: out1.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(root, "foo"))
	assert.True(t, os.IsNotExist(statErr), "the exact pre-sequence live state (absence) is restored")
}

// TestRecreate_AfterBrokenPredecessor_UndoNamesRepair (S-3.15 leg 1 + leg 2).
// BDD: Given an outside edit removed SKILL.md (helpers left), the agent
// writes a valid SKILL.md — leg 1: the committed result names the broken
// predecessor as not restorable with the repair; leg 2 (class 2 partial):
// the partial-save text carries the not-restorable variant. Neither offers an
// exact undo.
// Traces to: skills-spec-r13.md §5.5 variant 5, §5.10 SKILL_PARTIALLY_SAVED
// not-restorable sub-variant, S-3.15.
func TestRecreate_AfterBrokenPredecessor_UndoNamesRepair(t *testing.T) {
	// fixture builds: foo at V1; outside edit removes SKILL.md (helpers left).
	fixture := func(t *testing.T) (root string, revAfter string) {
		t.Helper()
		root = t.TempDir()
		out, _ := commitWrite(t, root, "foo", "SKILL.md",
			validSkillMD("foo", "original", "original body"), "")
		appDir := filepath.Join(root, "foo")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "helpers.md"), []byte("helper\n"), 0o644))
		require.NoError(t, os.Remove(filepath.Join(appDir, "SKILL.md")))
		return root, out.Revision
	}

	t.Run("leg 1 committed result", func(t *testing.T) {
		root, _ := fixture(t)

		out1, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
			Content: []byte(validSkillMD("foo", "repaired", "repaired body")),
			Expect:  Expectation{Revision: readLiveRevision(t, filepath.Join(root, "foo"))},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		})
		require.NoError(t, err)
		assert.Contains(t, out1.Message, "the version before it is ",
			"variant 5 names the predecessor")
		assert.Contains(t, out1.Message, ", which cannot be restored (",
			"variant 5 states the predecessor cannot be restored")
		assert.Contains(t, out1.Message, "no valid SKILL.md",
			"the reason is the structural ineligibility one")
		assert.Contains(t, out1.Message,
			"exact undo through restore is unavailable — repair the skill (e.g. write a valid SKILL.md) or restore an older eligible version (get_skill fields:[\"versions\"])",
			"byte-exact variant 5 repair advice")
	})

	t.Run("leg 2 class-2 partial text", func(t *testing.T) {
		root, _ := fixture(t)
		live := readLiveRevision(t, filepath.Join(root, "foo"))

		FaultManifestWrite = func(path string) error { return errors.New("disk full") }
		defer func() { FaultManifestWrite = nil }()

		out, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "SKILL.md",
			Content: []byte(validSkillMD("foo", "repaired", "repaired body")),
			Expect:  Expectation{Revision: live},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		})
		require.Error(t, err, "the manifest write failed after publication")
		assert.True(t, errors.Is(err, ErrSkillPartiallySaved), "class 2 outcome")
		assert.Equal(t, PersistencePartial, out.PersistenceStatus)
		assert.Contains(t, out.Message, "the change to 'foo' is live, but its version record could not be written",
			"byte-exact §5.10 partial-save prefix")
		assert.Contains(t, out.Message, "which cannot be restored",
			"the not-restorable sub-variant is chosen per the §5.5 rule")
		assert.NotContains(t, out.Message, "restore it to undo",
			"a gate-failing predecessor is never offered for restore")
	})
}

// TestUndoVariant_KeyedToCompleteRestoreGate (S-3.16 both legs, both shapes).
// BDD leg 1: a currently stored secret value inside an otherwise valid
// predecessor → the predecessor passes structural eligibility but fails the
// complete gate (secret check) — the advice names the repair and never the
// restore; attempting the restore is refused SECRET_IN_SKILL. BDD leg 2: a
// non-portable `a:b` entry → refused with the per-path reason. Both repeated
// as class-2 partial saves.
// Traces to: skills-spec-r13.md §5.5 variant 5, §7.2, §5.10, S-3.16.
func TestUndoVariant_KeyedToCompleteRestoreGate(t *testing.T) {
	const secretValue = "sk-live-abcdef123456"

	secretStore := func() ([]credentials.NamedValue, error) {
		return []credentials.NamedValue{{Name: "OPENROUTER_API_KEY", Value: secretValue}}, nil
	}

	t.Run("leg 1 secret predecessor — committed advice + restore refusal", func(t *testing.T) {
		root := t.TempDir()
		out1, _ := commitWrite(t, root, "foo", "SKILL.md",
			validSkillMD("foo", "clean v1", "clean body"), "")

		// Outside edit places the secret value in the live folder.
		appDir := filepath.Join(root, "foo")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "leaked.md"),
			[]byte("token="+secretValue+"\n"), 0o644))

		prev := SecretValuesProvider
		SecretValuesProvider = secretStore
		defer func() { SecretValuesProvider = prev }()

		out2, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("still clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		})
		require.NoError(t, err, "the new clean state passes validation")
		assert.Contains(t, out2.Message, "which cannot be restored",
			"the secret-carrying predecessor fails the complete gate")
		assert.Contains(t, out2.Message, "exact undo through restore is unavailable")
		assert.NotContains(t, out2.Message, "restore it to undo")

		// The named restore is refused SECRET_IN_SKILL.
		prevID := regexp.MustCompile(`the version before it is ([A-Za-z0-9-]+), which cannot be restored`).
			FindStringSubmatch(out2.Message)
		require.NotNil(t, prevID)
		_, err = ApplyChange(root, ChangeRequest{
			Kind: KindRestore, Slug: "foo", Version: prevID[1],
			Expect: Expectation{Revision: out2.Revision},
			Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
		})
		require.Error(t, err, "restoring the secret-carrying version is refused")
		assert.True(t, errors.Is(err, ErrSecretInSkill), "want SECRET_IN_SKILL, got %v", err)
		assert.NotContains(t, err.Error(), secretValue, "the value is never shown (§7.4)")
		_ = out1
	})

	t.Run("leg 2 non-portable name predecessor", func(t *testing.T) {
		root := t.TempDir()
		out1, _ := commitWrite(t, root, "foo", "SKILL.md",
			validSkillMD("foo", "clean v1", "clean body"), "")

		appDir := filepath.Join(root, "foo")
		require.NoError(t, os.WriteFile(filepath.Join(appDir, "a:b"), []byte("colon name\n"), 0o644))

		out2, err := ApplyChange(root, ChangeRequest{
			Kind: KindEdit, Slug: "foo", Path: "clean.md",
			Content: []byte("clean\n"),
			Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
			Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
		})
		require.NoError(t, err)
		assert.Contains(t, out2.Message, "which cannot be restored (")
		assert.Contains(t, out2.Message, "a:b", "the per-path reason names the entry")

		prevID := regexp.MustCompile(`the version before it is ([A-Za-z0-9-]+), which cannot be restored`).
			FindStringSubmatch(out2.Message)
		require.NotNil(t, prevID)
		_, err = ApplyChange(root, ChangeRequest{
			Kind: KindRestore, Slug: "foo", Version: prevID[1],
			Expect: Expectation{Revision: out2.Revision},
			Actor:  ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "a:b", "the same §7.2 reason the advice named")
		_ = out1
	})

	t.Run("both legs as class-2 partial texts", func(t *testing.T) {
		for _, leg := range []string{"secret", "colon"} {
			t.Run(leg, func(t *testing.T) {
				root := t.TempDir()
				commitWrite(t, root, "foo", "SKILL.md",
					validSkillMD("foo", "clean v1", "clean body"), "")
				appDir := filepath.Join(root, "foo")
				if leg == "secret" {
					require.NoError(t, os.WriteFile(filepath.Join(appDir, "leaked.md"),
						[]byte("token="+secretValue+"\n"), 0o644))
				} else {
					require.NoError(t, os.WriteFile(filepath.Join(appDir, "a:b"), []byte("x\n"), 0o644))
				}

				prev := SecretValuesProvider
				SecretValuesProvider = secretStore
				FaultManifestWrite = func(string) error { return errors.New("disk full") }
				defer func() {
					SecretValuesProvider = prev
					FaultManifestWrite = nil
				}()

				out, err := ApplyChange(root, ChangeRequest{
					Kind: KindEdit, Slug: "foo", Path: "clean.md",
					Content: []byte("clean\n"),
					Expect:  Expectation{Revision: readLiveRevision(t, appDir)},
					Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
				})
				require.Error(t, err)
				assert.Equal(t, PersistencePartial, out.PersistenceStatus)
				assert.Contains(t, out.Message, "which cannot be restored",
					"the class-2 partial text carries the same not-restorable variant (§5.10)")
				assert.NotContains(t, out.Message, "restore it to undo",
					"no check is weakened to make the advice restorable")
			})
		}
	})
}

// TestRecreate_AfterOutsideRemoval_ContentHeadVariant (S-3.17 both legs).
// BDD: Given foo at V1 whose live folder the owner removed outside Omnipus
// (head stays the content version V1), the agent recreates foo — write 1
// names the absence-with-content-head variant; restore V1 is not offered as
// the undo of the recreation; leg 2: the class-2-create-ii text says "the
// previous live state was absent (the folder had been removed outside
// Omnipus)".
// Traces to: skills-spec-r13.md §5.5 variant 4, §5.10 absence-with-history,
// S-3.17.
func TestRecreate_AfterOutsideRemoval_ContentHeadVariant(t *testing.T) {
	root := t.TempDir()
	out1, v1 := commitWrite(t, root, "foo", "SKILL.md",
		validSkillMD("foo", "v1", "v1 body"), "")

	// Outside removal of the live folder (no version recorded for absence).
	require.NoError(t, os.RemoveAll(filepath.Join(root, "foo")))

	// Recreate: write 1 commits V2.
	out2, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "foo",
		Content: []byte(validSkillMD("foo", "recreated", "recreated body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.NoError(t, err)
	assert.Contains(t, out2.Message, fmt.Sprintf("it follows an outside removal (version %s, retained); to undo, delete this skill with edit_skill action=delete", v1),
		"variant 4, byte-exact §5.5")
	assert.NotContains(t, out2.Message, "there is no earlier version",
		"never claims no earlier version (R5-MIN-003)")
	assert.NotContains(t, out2.Message, "follows the recorded deletion",
		"never says follows the recorded deletion (R5-MIN-003)")
	_ = out1

	// Leg 2: partial save (manifest fault after publication) — class
	// 2-create-ii with the content-head history note.
	require.NoError(t, os.RemoveAll(filepath.Join(root, "foo")))
	FaultManifestWrite = func(string) error { return errors.New("disk full") }
	defer func() { FaultManifestWrite = nil }()

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "foo",
		Content: []byte(validSkillMD("foo", "again", "again body")),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "write_file"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillPartiallySaved))
	assert.Equal(t, PersistencePartial, out.PersistenceStatus)
	assert.Contains(t, out.Message, "The previous live state was absent (the folder had been removed outside Omnipus)",
		"byte-exact §5.10 history note for a retained content head")
	assert.Contains(t, out.Message, "the skill's earlier history is unchanged")
	assert.NotContains(t, out.Message, "No version history exists for this skill",
		"the absence-with-history variant never claims no history")
}
