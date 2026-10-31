package skills

// RED tests — landing/skills (T9): SD-28 "a grant never outlives its skill"
// (spec §5.13a, revision 13, founder Q-S2 = A; change record §44).
//
// ⚠️ PENDING GRILL: revision r13 (the SD-28 contract, scenario S-6.12 and
// error SKILL_NAME_GRANTED) is still in a short grill per the chief's brief.
// These tests implement the CURRENT r13 wording byte-for-byte; the chief
// reports any change from the grill and these tests are updated then. Flagged
// "pending grill" in docs/internal/specs/skills-red-traceability.md.
//
// Traces to: skills-spec-r13.md §5.13a, §5.4 copy row + copy text, §5.10
// SKILL_NAME_GRANTED, §7.1 step 7, §16 S-6.12 (rewritten r13), §17.1 row 29
// (T-K2), E-5, E-14, FR-046.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelete_RemovesGrants_AtDeletionTime (S-6.12 main leg, SD-28 rule 1).
// BDD: Given agent A granted foo, When foo is deleted (edit_skill
// action=delete), Then A's stored record no longer lists foo (RemoveGrant ran
// in the delete's lock hold, after the version commit), the delete commits,
// and no grant row for foo exists anywhere.
// Traces to: skills-spec-r13.md §5.13a "Removal at deletion", S-6.12.
func TestDelete_RemovesGrants_AtDeletionTime(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")

	grants := &recordingGrants{}
	grants.seededSet("foo")

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: "whatever-current"}, // seen record supplied by the adapter
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		Grants: grants,
	})
	require.NoError(t, err, "delete must commit (RED: pipeline not implemented → this fails first)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)

	// RED: the scaffold never calls RemoveGrant.
	assert.Contains(t, grants.removed, "foo",
		"the deletion must remove foo from every agent's grant list (§5.13a)")
	assert.False(t, grants.holdsGrant("foo"),
		"after the delete no stored grant row for foo may remain")

	// The deletion version is recorded (history kept until retention).
	_, statErr := os.Stat(filepath.Join(root, "foo"))
	assert.True(t, os.IsNotExist(statErr), "live folder moved away")
}

// TestCopy_AfterDelete_CommitsUngranted_TextOffersGrant (S-6.12, SD-28
// "recreation inherits nothing" + §5.4 copy result text, r13 byte-exact).
// BDD: Given foo deleted (its grants removed), When plan is copied to foo,
// Then the copy commits ungranted and its result text offers granting —
// never an inheritance sentence.
// Traces to: skills-spec-r13.md §5.13a, §5.4 copy result text, S-6.12.
func TestCopy_AfterDelete_CommitsUngranted_TextOffersGrant(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")
	grants := &recordingGrants{}
	grants.seededSet("foo")
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: "r1"},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		Grants: grants,
	})
	require.NoError(t, err)

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "plan", To: "foo",
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		Grants: grants,
	})
	require.NoError(t, err, "the recreation onto the deleted name commits (grant was removed at deletion)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	// "Starts ungranted": the pipeline has no grant-adding channel at all
	// (§5.13a — nothing added, nothing inherited); the only grant mutation the
	// delete made was the removal. Any future grant-adding side effect would
	// need a new GrantEditor method, which this contract forbids.
	assert.Equal(t, []string{"foo"}, grants.removed,
		"the only grant mutation in this sequence is the delete-time removal")

	assert.Contains(t, out.Message,
		"Created 'foo' as your own copy of 'plan'. No agent is granted it yet; grant it to the agents that should use it.",
		"byte-exact §5.4 copy result text (r13: the grant offer replaces the r3 inheritance sentence)")
	assert.NotContains(t, out.Message, "inherit",
		"no recreation text may claim grant inheritance (§5.13a)")
}

// TestCreateCopyInstall_OntoStillGrantedName_Refused_SKILL_NAME_GRANTED
// (§5.13a "Grant-reference precondition" — the A-answer's refusal; safety net
// for the failed-removal window).
// BDD: Given a stored grant still references foo (the failed-removal window),
// When a create/copy/install targets foo, Then SKILL_NAME_GRANTED with the
// byte-exact text naming every holding agent.
// Traces to: skills-spec-r13.md §5.13a, §5.10 SKILL_NAME_GRANTED, E-14.
func TestCreateCopyInstall_OntoStillGrantedName_Refused_SKILL_NAME_GRANTED(t *testing.T) {
	root := t.TempDir()

	stillGranted := &grantRefusalEditor{holders: []string{"agent-a", "agent-b"}}
	newSkillMD := "---\nname: foo\ndescription: recreated\n---\nbody\n"

	t.Run("create write_file foo/SKILL.md", func(t *testing.T) {
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindCreate, Slug: "foo", Content: []byte(newSkillMD),
			Expect: Expectation{Absent: true},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "write_file"},
			Grants: stillGranted,
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSkillNameGranted), "want SKILL_NAME_GRANTED, got %v", err)
		assert.Contains(t, err.Error(),
			"'foo' is still granted to agent-a, agent-b; remove the grant (Agents settings) or restart, then retry",
			"byte-exact §5.10 SKILL_NAME_GRANTED text naming every holding agent")
	})

	t.Run("copy plan to=foo", func(t *testing.T) {
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindCreate, Slug: "plan", To: "foo",
			Expect: Expectation{Absent: true},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
			Grants: stillGranted,
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSkillNameGranted), "want SKILL_NAME_GRANTED, got %v", err)
	})

	t.Run("install foo", func(t *testing.T) {
		staged := t.TempDir()
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindInstall, Slug: "foo", StagedDir: staged,
			Expect: Expectation{Absent: true},
			Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
			Grants: stillGranted,
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSkillNameGranted), "want SKILL_NAME_GRANTED, got %v", err)
	})
}

// TestDelete_GrantRemovalFails_DeleteStands_WarningSurfaces_CopyRefused
// (S-6.12 fault leg — SD-28 "Removal failure" + the §5.13a fault-injected
// variant of the required test T-K2).
// BDD: Given the grant-removal write fails, When foo is deleted, Then the
// deletion STANDS with the class-3 finishing warning; a later copy onto foo
// is refused SKILL_NAME_GRANTED naming A; after the start-up sweep removes
// the grant, the copy commits ungranted.
// Traces to: skills-spec-r13.md §5.13a "Removal failure" + "Start-up sweep",
// §7.6 class 3, S-6.12 fault leg.
func TestDelete_GrantRemovalFails_DeleteStands_WarningSurfaces_CopyRefused(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "foo")

	failing := &recordingGrants{err: errors.New("disk on fire")}
	failing.seededSet("foo")

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: "r1"},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		Grants: failing,
	})
	require.NoError(t, err, "a failed grant removal does NOT fail the deletion")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus, "the deletion stands")
	assert.NotEmpty(t, out.Warning,
		"the failure surfaces through the §7.6 class-3 finishing-warning channel — never silent")
	assert.Contains(t, out.Warning, "foo",
		"the warning names what could not be cleaned up")

	// The stored grant still references foo (the removal failed).
	assert.True(t, failing.holdsGrant("foo"), "the failed removal leaves the stored grant in place")

	// Recreation onto the still-referenced name is refused.
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "plan", To: "foo",
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "b", Tool: "edit_skill"},
		Grants: failing,
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillNameGranted), "want SKILL_NAME_GRANTED, got %v", err)
	assert.Contains(t, err.Error(), "agent-a", "the refusal names the holding agent")

	// The start-up sweep repairs: removes the dangling grant (logged,
	// idempotent — the same RemoveGrant), after which the copy commits
	// ungranted.
	sweep := &recordingGrants{}
	sweep.seededSet("foo")
	assert.NoError(t, SweepDanglingGrants(root, sweep, nil), "the sweep succeeds once the store is writable")
	assert.Contains(t, sweep.removed, "foo", "the sweep removed the dangling grant")

	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindCreate, Slug: "plan", To: "foo",
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "b", Tool: "edit_skill"},
		Grants: sweep,
	})
	require.NoError(t, err, "after the sweep the recreation commits ungranted")
}

// TestInstallReplace_KeepsGrants (§5.13a "Consistency": install-replace is
// unchanged — replacing a LIVE skill keeps its grants; the rule is about
// deletion only).
// Traces to: skills-spec-r13.md §5.13a "Consistency".
func TestInstallReplace_KeepsGrants(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "app")
	historyRoot := t.TempDir() // §5.11: system/skill-history/<id>

	grants := &recordingGrants{}
	grants.seededSet("app")

	staged := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"),
		[]byte("---\nname: app\ndescription: replaced\n---\nbody\n"), 0o644))

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: staged,
		Expect: Expectation{Revision: "r1"},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
		Grants: grants,
	})
	// RED: the pipeline is not implemented; assert the refusal shape so a
	// future implementation cannot pass by silently dropping grants.
	if err != nil && errors.Is(err, redNotImplemented) {
		t.Fatal("BLOCKED: ApplyChange not implemented — required by skills-spec-r13.md §5.13a/§7.1 (install_replace keeps grants)")
	}
	require.NoError(t, err)
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)
	assert.Empty(t, grants.removed,
		"install-replace must keep the granted skill's grants (nothing dangles)")
	_ = historyRoot
}

// grantRefusalEditor models a stored grant state whose removal cannot run
// (the Agents-settings store is the holder): any create/copy/install onto the
// name must be refused while it holds the name.
type grantRefusalEditor struct {
	holders []string
}

func (g *grantRefusalEditor) RemoveGrant(name string) error {
	return errors.New("grant store busy (models the failed-removal window)")
}

// holdersOf renders the holders list the way §5.10 names agents.
func (g *grantRefusalEditor) holdersOf(slug string) string {
	return strings.Join(g.holders, ", ")
}
