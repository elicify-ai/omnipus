package skills

// RED tests — landing/skills (T9): SD-30 grant-identity binding
// (spec §5.13a, revision 15; supersedes the r13 SD-28 deletion-time-removal
// mechanism and the r14 gate G-1–G-6/SD-29; founder Q-S2 = A stands,
// 2026-10-31).
//
// ⚠️ OPEN QUESTION Q-S3: what the owner sees when a stored grant row goes
// inert (the SKILL_GRANT_STALE surface on the Agents screen and the load
// refusal text) proceeds on Q-S3's RECOMMENDATION — §5.10 marks the code
// "proceeds on Q-S3's recommendation" while Q-S3 itself is still open.
// These tests pin the RECOMMENDED behaviour (the §5.10 SKILL_GRANT_STALE
// text byte-exact and the "row heals at the agent's next skills persist"
// rule) and are flagged "depends on Q-S3" in
// docs/internal/specs/skills-red-traceability.md; a Q-S3 ruling that changes
// the surface changes these legs, not the binding mechanics.
//
// Traces to: skills-spec-r15.md §5.13a (SD-30), §5.4 copy row + copy text,
// §5.10 SKILL_GRANT_STALE, §7.1 step 7 (r15), §16 S-6.12, S-6.18 (r15),
// §17.1 row 29 (T-K2), E-4, E-5, E-14, SR-5a, FR-046.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const SkillIdentityFile = ".omnipus-identity" // r15 §5.13a, test constant

// generationOf reads §5.13a's stamp from a skill folder.
func generationOf(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, SkillIdentityFile))
	require.NoError(t, err, "the generation stamp .omnipus-identity is present in %s", dir)
	require.Equal(t, 1, strings.Count(string(raw), "\n"), "stamp is exactly one line")
	gen := strings.TrimSuffix(string(raw), "\n")
	require.True(t, uuidShape.MatchString(gen), "stamp has UUID format")
	return gen
}

// uuidShape matches a UUID-format string (8-4-4-4-12 hex) — §5.13a stores
// "a random 128-bit value (UUID-format string)".
var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// staleLoad runs a §5.7 activation load for the agent whose Bindings carry
// the given table, and returns the error.
func staleLoad(t *testing.T, root, slug string, bindings map[string]string) error {
	t.Helper()
	opts := LoadActivationOptions{}
	f := reflect.ValueOf(&opts).Elem().FieldByName("Bindings")
	if !f.IsValid() {
		t.Fatal("BLOCKED: activation generation-binding input not implemented — required by r15 §5.13a SD-30")
	}
	require.Equal(t, reflect.TypeOf(map[string]string{}), f.Type())
	f.Set(reflect.ValueOf(bindings))
	_, err := LoadForActivation(root, slug, opts)
	return err
}

// createSkillViaPipeline creates a skill through the §7.1 pipeline (the only
// creation path any test may use — fixtures must not hand-build what the
// pipeline is supposed to build) and returns the outcome plus the version id
// parsed from the §5.5 undo line.
func createSkillViaPipeline(t *testing.T, root, slug, description, body string) (PublishOutcome, string) {
	t.Helper()
	_, statErr := os.Stat(filepath.Join(root, slug))
	require.True(t, os.IsNotExist(statErr), "create fixture must start absent: %s", slug)
	out, err := ApplyChange(root, ChangeRequest{
		Kind:    KindCreate,
		Slug:    slug,
		Content: []byte(validSkillMD(slug, description, body)),
		Expect:  Expectation{Absent: true},
		Actor:   ChangeActor{Kind: "agent", AgentID: "agent-a", SessionID: "s1", Tool: "edit_skill"},
	})
	require.NoError(t, err, "RED: the create must commit (pipeline unimplemented on baseline)")
	m := versionIDFromMessage.FindStringSubmatch(out.Message)
	require.NotNil(t, m, "every committed skill creation names its version: %q", out.Message)
	return out, m[1]
}

// TestDelete_TouchesNoGrantRecord_RowSurvivesInert (S-6.12 main leg, E-4,
// E-5; §7.1 step 7 r15). BDD: agent A granted foo (bound to generation G1);
// foo is deleted — Then the deletion commits, touches NO grant record
// (no removal, no rebuild, no sweep — SD-30), and A's stored row still
// lists foo: inert, bound to a generation no live skill carries.
// Traces to: skills-spec-r15.md §5.13a, E-4, E-5, S-6.12.
func TestDelete_TouchesNoGrantRecord_RowSurvivesInert(t *testing.T) {
	root := t.TempDir()

	// foo created through the pipeline (a creation event stamps G1).
	outCreate, _ := createSkillViaPipeline(t, root, "foo", "bound generation", "foo body")
	fooDir := filepath.Join(root, "foo")
	g1 := generationOf(t, fooDir)
	require.True(t, uuidShape.MatchString(g1), "the creation stamped a UUID-format generation, got %q", g1)

	grants := &recordingGrants{}
	grants.seededSet("foo")

	out, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: outCreate.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		Grants: grants,
	})
	require.NoError(t, err, "delete must commit (RED: pipeline not implemented → this fails first)")
	assert.Equal(t, PersistenceComplete, out.PersistenceStatus)

	// SD-30: the pipeline runs no grant removal and no rebuild — the editor
	// is never consulted, the row survives.
	assert.Empty(t, grants.removed,
		"deletion touches no grant record (§5.13a SD-30: no removal at deletion time)")
	assert.True(t, grants.holdsGrant("foo"),
		"the stored row still lists foo — inert, bound to the retired generation")
	_, statErr := os.Stat(fooDir)
	assert.True(t, os.IsNotExist(statErr), "the live folder is gone")
}

// TestRecreation_NewGeneration_StaleGrantInertAtUse (S-6.12, E-14, §5.4 copy
// text). BDD: after the deletion, plan is copied to foo — the recreation
// ALWAYS commits (no holder gate, no SKILL_NAME_GRANTED — withdrawn with the
// gate), stamped with a FRESH generation G2; the copy starts ungranted and
// its result text offers granting; former holder A's stale row (bound G1) is
// refused at use with SKILL_GRANT_STALE without rebuild or restart; an
// explicit re-grant binds G2 and A loads. Restart leg: deletion and a restart
// change nothing observable — the row survives, still inert.
// Traces to: skills-spec-r15.md §5.13a, E-14, S-6.12, §5.4 copy text,
// §5.10 SKILL_GRANT_STALE.
func TestRecreation_NewGeneration_StaleGrantInertAtUse(t *testing.T) {
	root := t.TempDir()
	historyRoot := SkillHistoryRoot(root)
	exportShipped(t, "plan", filepath.Join(root, "plan"))
	builtinMarkerFixture(t, filepath.Join(root, "plan"))

	outCreate, _ := createSkillViaPipeline(t, root, "foo", "first generation", "foo body")
	g1 := generationOf(t, filepath.Join(root, "foo"))

	grants := &recordingGrants{}
	grants.seededSet("foo", "other-skill") // two rows: no mutation may touch either
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "foo",
		Expect: Expectation{Revision: outCreate.Revision},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		Grants: grants,
	})
	require.NoError(t, err)

	// The recreation commits — no holder gate exists any more (E-14: "the
	// recreation always commits").
	outCopy, err := copySrcToDst(t, root, "plan", "foo")
	require.NoError(t, err, "the recreation onto the deleted name commits; SKILL_NAME_GRANTED is retired")
	assert.Equal(t, PersistenceComplete, outCopy.PersistenceStatus)

	// A fresh generation was minted — never the deleted one, never the
	// source's (Q-S2 = A: copy carries no source generation).
	g2 := generationOf(t, filepath.Join(root, "foo"))
	require.True(t, uuidShape.MatchString(g2))
	require.NotEqual(t, g1, g2, "a recreated name is a NEW generation (never reused)")

	// The copy starts ungranted: the pipeline mutated NO stored row —
	// deletion removed nothing and creation added nothing (§5.4: "no grant
	// is created"); the untouched second row proves no wholesale rewrite.
	assert.Empty(t, grants.removed, "no grant mutation anywhere in delete+recreate (SD-30)")
	assert.True(t, grants.holdsGrant("foo"),
		"the stored row still lists foo — unchanged, bound to the retired generation")
	assert.True(t, grants.holdsGrant("other-skill"),
		"the unrelated row is untouched too — no wholesale grant rewrite")

	// The copy result text offers granting (§5.4, byte-exact).
	assert.Contains(t, outCopy.Message,
		"Created 'foo' as your own copy of 'plan'. No agent is granted it yet; grant it to the agents that should use it.",
		"byte-exact §5.4 copy result text")
	assert.NotContains(t, outCopy.Message, "inherit")

	// A's stale binding (G1) is refused AT USE — the live skill carries G2.
	err = staleLoad(t, root, "foo", map[string]string{"foo": g1})
	require.Error(t, err, "the stale binding must not authorize the recreation")
	assert.True(t, strings.Contains(err.Error(), "SKILL_GRANT_STALE"), "want SKILL_GRANT_STALE, got %v", err)
	assert.Contains(t, err.Error(),
		"skill 'foo' is not available: your grant is for a previous skill with this name that no longer exists; ask the owner to re-grant the current 'foo'",
		"byte-exact §5.10 SKILL_GRANT_STALE text (Q-S3's recommended surface)")

	// An unbound row (binding matching nothing) authorizes nothing either:
	// inert means no load without an explicit re-grant.
	err = staleLoad(t, root, "foo", nil)
	require.Error(t, err, "no binding at all authorizes nothing (fail-closed)")

	// Restart leg: deletion and a restart change nothing observable — the
	// row survives, still inert.
	require.NoError(t, RecoverInterrupted(root, historyRoot), "the restart (§9.3 recovery) runs clean")
	assert.True(t, grants.holdsGrant("foo"), "the restart did not touch the row (no sweep exists)")
	err = staleLoad(t, root, "foo", map[string]string{"foo": g1})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "SKILL_GRANT_STALE"),
		"after the restart the stale binding is still inert: %v", err)

	// An explicit re-grant binds G2 (the owner's act, §5.13a) and A loads.
	err = staleLoad(t, root, "foo", map[string]string{"foo": g2})
	require.NoError(t, err, "the explicit re-grant to the live generation restores the load")
}

// TestGenerationLifecycle_StampRules (§5.13a Generation + Assignment rows).
// BDD: a creation event stamps a fresh generation; every content mutation of
// a live skill PRESERVES it (file write, install replace); restore into
// absence mints a NEW generation; copy does NOT carry the source's
// generation; a deletion retires the generation forever — a recreation is a
// new one; across the whole sequence no generation value is ever reused.
// Traces to: skills-spec-r15.md §5.13a (Generation, Assignment rows).
func TestGenerationLifecycle_StampRules(t *testing.T) {
	root := t.TempDir()
	seen := map[string]bool{}

	next := func(t *testing.T, dir string, mustBeNew bool) string {
		t.Helper()
		g := generationOf(t, dir)
		require.True(t, uuidShape.MatchString(g), "UUID-format generation, got %q", g)
		if mustBeNew {
			require.False(t, seen[g], "generation %s was already used — never reused", g)
		}
		seen[g] = true
		return g
	}

	outCreate, vCreate := createSkillViaPipeline(t, root, "app", "creation event", "app body")
	appDir := filepath.Join(root, "app")
	g1 := next(t, appDir, true)

	// A content mutation of the LIVE skill preserves the generation.
	commitWrite(t, root, "app", "notes.md", "note\n", outCreate.Revision)
	assert.Equal(t, g1, generationOf(t, appDir), "a live content write preserves the stamp")
	_, err := ApplyChange(root, ChangeRequest{Kind: KindRestore, Slug: "app", Version: vCreate, Expect: Expectation{Revision: readLiveRevision(t, appDir)}, Actor: agentActor("agent-a")})
	require.NoError(t, err)
	assert.Equal(t, g1, generationOf(t, appDir), "restore onto a live skill preserves its generation")

	// Install replace preserves it too (grants keep working — §5.13a).
	staged := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"),
		[]byte(validSkillMD("app", "replaced", "replaced body")), 0o644))
	rev := readLiveRevision(t, appDir)
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: staged,
		Expect: Expectation{Revision: rev},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.NoError(t, err)
	assert.Equal(t, g1, generationOf(t, appDir), "install replace preserves the live stamp")

	// Copy does NOT carry the source's generation (Q-S2 = A).
	_, err = copySrcToDst(t, root, "app", "app-copy")
	require.NoError(t, err)
	gCopy := next(t, filepath.Join(root, "app-copy"), true)
	require.NotEqual(t, g1, gCopy, "the copy is its own creation event — plan's grants must not authorize a copy of plan")

	// Deletion retires the generation forever; restore into absence mints a
	// new one (never the deleted generation, never reused).
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindDelete, Slug: "app",
		Expect: Expectation{Revision: readLiveRevision(t, appDir)},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err)
	_, err = ApplyChange(root, ChangeRequest{
		Kind: KindRestore, Slug: "app", Version: vCreate,
		Expect: Expectation{Absent: true},
		Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
	})
	require.NoError(t, err, "restore into absence commits")
	gRestored := next(t, appDir, true)
	require.NotEqual(t, g1, gRestored, "restore into absence is a creation event — a deleted grant can never match it")
}

// TestInstallReplace_PreservesGeneration_GrantsKeepWorking (§5.13a
// consequence row). BDD: a live replace preserves the generation, so the
// skill's grants keep working — the bound agent still loads it after the
// replace, with no re-grant.
// Traces to: skills-spec-r15.md §5.13a ("Install-replace is unchanged
// observably").
func TestInstallReplace_PreservesGeneration_GrantsKeepWorking(t *testing.T) {
	root := t.TempDir()
	outCreate, _ := createSkillViaPipeline(t, root, "app", "granted app", "app body")
	appDir := filepath.Join(root, "app")
	g1 := generationOf(t, appDir)

	staged := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(staged, "SKILL.md"),
		[]byte(validSkillMD("app", "replaced", "replaced body")), 0o644))
	_, err := ApplyChange(root, ChangeRequest{
		Kind: KindInstallReplace, Slug: "app", StagedDir: staged,
		Expect: Expectation{Revision: outCreate.Revision},
		Actor:  ChangeActor{Kind: "person", Tool: "rest-install"},
	})
	require.NoError(t, err)
	assert.Equal(t, g1, generationOf(t, appDir), "the replace preserved the generation")

	err = staleLoad(t, root, "app", map[string]string{"app": g1})
	assert.NoError(t, err, "the binding still matches the live generation — the grant keeps working")
}

// TestGenerationAuthorization_Table exercises only the identity comparison at
// activation. The real bind-at-persist table is in agentstore's SD-30 tests;
// supplying a binding here is NOT evidence that any persistence path derives it.
// Traces to r15 §5.13a Authorization and S-6.18's no-G1-authorizes-G2 property.
func TestGenerationAuthorization_Table(t *testing.T) {
	t.Run("leg i: foo live at persist binds G1 and loads", func(t *testing.T) {
		root := t.TempDir()
		createSkillViaPipeline(t, root, "foo", "live at persist", "foo body")
		g1 := generationOf(t, filepath.Join(root, "foo"))

		// The persist observes foo live and binds G1; the load succeeds.
		err := staleLoad(t, root, "foo", map[string]string{"foo": g1})
		assert.NoError(t, err, "binding = the live generation → the agent loads the then-live foo")
	})

	t.Run("leg ii: foo absent at persist binds nothing — the recreation stays unloadable", func(t *testing.T) {
		root := t.TempDir()
		exportShipped(t, "plan", filepath.Join(root, "plan"))
		builtinMarkerFixture(t, filepath.Join(root, "plan"))
		outCreate, _ := createSkillViaPipeline(t, root, "foo", "later deleted", "foo body")
		g1 := generationOf(t, filepath.Join(root, "foo"))
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindDelete, Slug: "foo",
			Expect: Expectation{Revision: outCreate.Revision},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		})
		require.NoError(t, err)

		// The persist observes foo ABSENT: binding = none (unbound is inert).
		// The recreation — before or after the load attempt, in any later
		// ordering — must never become loadable for this unbound row.
		err = staleLoad(t, root, "foo", nil)
		assert.Error(t, err, "an absent name binds nothing — inert")

		outCopy, err := copySrcToDst(t, root, "plan", "foo")
		require.NoError(t, err, "the recreation commits")
		assert.Equal(t, PersistenceComplete, outCopy.PersistenceStatus)
		err = staleLoad(t, root, "foo", nil)
		assert.Error(t, err, "the recreation in any later ordering stays unloadable for the unbound row (property)")
		err = staleLoad(t, root, "foo", map[string]string{"foo": g1})
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "SKILL_GRANT_STALE"),
			"even a stale G1 row cannot load the G2 recreation — no ordering authorizes it")
	})

	t.Run("leg iii: foo live as the recreation at persist binds G2 and loads", func(t *testing.T) {
		root := t.TempDir()
		exportShipped(t, "plan", filepath.Join(root, "plan"))
		builtinMarkerFixture(t, filepath.Join(root, "plan"))
		outCreate, _ := createSkillViaPipeline(t, root, "foo", "first", "foo body")
		g1 := generationOf(t, filepath.Join(root, "foo"))
		_, err := ApplyChange(root, ChangeRequest{
			Kind: KindDelete, Slug: "foo",
			Expect: Expectation{Revision: outCreate.Revision},
			Actor:  ChangeActor{Kind: "agent", AgentID: "a", Tool: "edit_skill"},
		})
		require.NoError(t, err)
		_, err = copySrcToDst(t, root, "plan", "foo")
		require.NoError(t, err)
		g2 := generationOf(t, filepath.Join(root, "foo"))
		require.NotEqual(t, g1, g2, "the recreation minted a fresh generation")

		// The persist observes the recreation live and binds G2 — the
		// explicit contemporaneous grant, never an inherited row.
		err = staleLoad(t, root, "foo", map[string]string{"foo": g2})
		assert.NoError(t, err, "binding = the recreation's generation → the agent loads the replacement")
	})
}

// recordingGrants lives in skill_identity_red_test.go (shared GrantEditor
// test double: records RemoveGrant calls, models seeded stored rows).
