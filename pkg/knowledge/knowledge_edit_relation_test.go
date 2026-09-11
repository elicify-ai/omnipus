// Omnipus — tests for the agent door's relation verbs (op "relation") and
// for the two write guards it made it safe to switch on: FR-046 (derived
// properties) and FR-045 (relation/person properties through set_property).
//
// # What these tests are built to catch
//
// The property that MATTERS about `add` is not that the new target lands —
// an implementation that wiped the list and wrote one element would pass
// that. It is that every target ALREADY THERE survives, including ones this
// caller never saw. Every add/remove assertion below therefore checks the
// untouched edges explicitly and by exact count, not just the one that moved.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// relSchema writes a "deal" schema carrying one MANY relation, one SCALAR
// relation, one person property and one ordinary text property — the four
// declarations every rule below needs something to bind against.
func relSchema(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// Every `relation` carries `to:` because FR-034 makes it mandatory — a
	// relation with no declared target type is REJECTED at load, which would
	// silently degrade this whole fixture to an ungoverned note and make
	// every refusal below pass for the wrong reason. `person` takes no `to:`:
	// its target is whatever record type the vault uses for people.
	yaml := "schema_version: 1\n" +
		"type: deal\n" +
		"properties:\n" +
		"  partners: { type: relation, to: deal, many: true }\n" +
		"  company:  { type: relation, to: deal }\n" +
		"  owner:    { type: person }\n" +
		"  status:   { type: text }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deal.yaml"), []byte(yaml), 0o600))

	// ⚠️ PROVE THE SCHEMA ACTUALLY LOADED. This is not belt-and-braces — it
	// is the only thing standing between this file and a whole suite of
	// vacuous passes, and the first draft of this fixture WAS rejected at
	// load (it omitted FR-034's mandatory `to:`). A rejected schema does not
	// fail anything: it degrades the note to "ordinary and unconstrained",
	// so FR-045 and FR-046 never fire, every refusal test sees a successful
	// write instead of a refusal, and — had the assertions been written the
	// other way round — every "this is still allowed" test would have passed
	// while proving nothing at all. The tool reports the degrade only as a
	// NOTE line buried in an otherwise successful result.
	set, report, err := records.LoadSchemas(root)
	require.NoError(t, err)
	require.Emptyf(t, report.Rejections,
		"the fixture schema must LOAD, or every governed-write test below passes vacuously: %+v", report.Rejections)
	schema, ok := set.Get("deal")
	require.True(t, ok, "the fixture schema must resolve as type \"deal\"")
	for _, name := range []string{"partners", "company", "owner", "status"} {
		_, declared := schema.Property(name)
		require.Truef(t, declared, "fixture must declare %q", name)
	}
}

// relCount reports how many block-sequence items a property currently holds,
// so a preservation assertion can pin an exact COUNT rather than merely the
// presence of the items it happens to name.
func relCount(src, property string) int {
	lines := strings.Split(src, "\n")
	inProp := false
	n := 0
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, property+":"):
			inProp = true
		case inProp && strings.HasPrefix(ln, "  - "):
			n++
		case inProp && strings.TrimSpace(ln) != "" && !strings.HasPrefix(ln, "  "):
			return n
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// FR-045 — set_property is refused on a relation, and the refusal is READABLE
// ---------------------------------------------------------------------------

// TestRelation_FR045_SetPropertyRefused covers BOTH declared relation shapes
// and the person property, as three subtests rather than one: a guard that
// lost the person half, or the scalar half, would still pass a many-relation-
// only test. That is the same reasoning records' own
// TestCheckRecordPropertyWrites_RefusesRelationAndPerson gives for splitting
// its cases.
func TestRelation_FR045_SetPropertyRefused(t *testing.T) {
	for _, property := range []string{"partners", "company", "owner"} {
		t.Run(property, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "kb")
			relSchema(t, root)
			deps, _ := a4Deps(home)
			tool := veTool(deps)

			a4Note(t, root, "Deal.md", "---\ntype: deal\n---\nBody.\n")
			before := a4Read(t, root, "Deal.md")

			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "set_property", "path": "Deal.md",
				"property": property, "value": "Acme Ltd",
				"expect_version": a4Version(t, root, "Deal.md"),
			})
			require.True(t, res.IsError, "FR-045: set_property on %s must be refused, got: %s", property, res.ForLLM)

			// THE REFUSAL IS PART OF THE DELIVERABLE. An agent that cannot
			// read the fix out of the error text retries the same call and
			// fails again, so every fact needed to build the correct retry
			// is asserted individually — not as one substring match that a
			// reworded message would silently stop covering.
			msg := res.ForLLM
			for _, want := range []string{
				`op "relation"`,                  // the op to use
				"relation_op",                    // the argument that carries the verb
				`"add"`, `"remove"`, `"replace"`, // every verb it accepts
				"targets",        // the argument that carries the targets
				"expect_version", // the token it must carry over
				property,         // the property it was refused on
			} {
				require.Containsf(t, msg, want,
					"the refusal must name %q so an agent can retry correctly; got: %s", want, msg)
			}

			require.Equal(t, before, a4Read(t, root, "Deal.md"),
				"a refused write must leave the note byte-identical")
		})
	}
}

// TestRelation_FR045_SetPropertyListOpRefused pins the SECOND set_property
// mode. list_op is additive and harmless in itself; it is refused because it
// writes relations as untyped strings with none of op "relation"'s rules —
// see knowledgeEditListOpEdit's comment. A guard placed only on the plain
// value path would leave this one open and this test is what notices.
func TestRelation_FR045_SetPropertyListOpRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")
	before := a4Read(t, root, "Deal.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deal.md",
		"property": "partners", "list_op": "add", "value": "[[Beta]]",
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.True(t, res.IsError, "FR-045: set_property list_op on a relation must be refused, got: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `op "relation"`)
	require.Equal(t, before, a4Read(t, root, "Deal.md"))
}

// ---------------------------------------------------------------------------
// FR-045 does NOT cost a capability — the two sanctioned paths still work
// ---------------------------------------------------------------------------

// TestRelation_FR045_CreateStillAcceptsRelations is the capability guarantee.
// Switching FR-045 on at create would mean an agent could not author a note
// carrying a relation at all — and, because create validates the whole
// ASSEMBLED frontmatter, could not use any template that mentions one. The
// hazard FR-045 names (replacing a list another writer added to) cannot exist
// on a create, which refuses outright when the file is already there.
func TestRelation_FR045_CreateStillAcceptsRelations(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "Fresh.md",
		"frontmatter": map[string]any{
			"type":     "deal",
			"company":  "[[Acme Ltd]]",
			"partners": []any{"[[Alpha]]", "[[Beta]]"},
		},
	})
	require.False(t, res.IsError, "create must still accept relation properties: %s", res.ForLLM)

	got := a4Read(t, root, "Fresh.md")
	require.Contains(t, got, "[[Acme Ltd]]")
	require.Contains(t, got, "[[Alpha]]")
	require.Contains(t, got, "[[Beta]]")
}

// TestRelation_FR045_LinkOpStillWorks pins op "link"'s relation mode, which
// is one of the explicit verbs FR-045 points callers at and therefore cannot
// itself be subject to the rule.
func TestRelation_FR045_LinkOpStillWorks(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "link", "path": "Deal.md",
		"target": "Beta", "relation": "partners",
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "op link with a relation must keep working: %s", res.ForLLM)

	got := a4Read(t, root, "Deal.md")
	require.Contains(t, got, "[[Alpha]]", "link must not discard the existing edge")
	require.Contains(t, got, "[[Beta]]")
}

// ---------------------------------------------------------------------------
// FR-046 — a derived property is refused for EVERY caller
// ---------------------------------------------------------------------------

// TestRelation_FR046_DerivedPropertyRefused exercises the guard directly
// rather than through the tool, and that is forced rather than chosen.
// records.LoadSchemas REFUSES a schema file that declares `formula:`
// (schema.go's propertyDeclKeys), so no schema the tool can load is able to
// carry one — the branch is defence in depth against a *Schema arriving from
// elsewhere, exactly as it is on the web door, where
// CheckRecordPropertyWrites' own doc comment records the same fact. Building
// the Schema in the test is the only way to reach it at all; routing through
// the tool would assert nothing.
func TestRelation_FR046_DerivedPropertyRefused(t *testing.T) {
	schema := &records.Schema{
		SchemaVersion: 1,
		Type:          "deal",
		Properties: map[string]*records.Property{
			"margin": {Name: "margin", Type: records.TypeDecimal, Formula: "revenue - cost"},
		},
		PropertyOrder: []string{"margin"},
	}

	// Both postures, because FR-046 is unconditional: unlike FR-045 it has
	// no sanctioned caller, so the create path (RelationAllowed) must refuse
	// a derived write exactly as set_property (RelationRefused) does.
	for name, posture := range map[string]knowledgeEditRelationPosture{
		"set_property": knowledgeEditRelationRefused,
		"create":       knowledgeEditRelationAllowed,
	} {
		t.Run(name, func(t *testing.T) {
			err := knowledgeEditValidatePropertyAgainstSchema(
				schema, "deal", "margin", []string{"42"}, false, posture)
			require.Error(t, err, "FR-046: a derived property must not be writable")
			require.ErrorIs(t, err, ErrDerivedProperty,
				"the refusal must carry its own sentinel, not be indistinguishable from a value error")
			require.Contains(t, err.Error(), "derived value")
			require.Contains(t, err.Error(), "margin")
		})
	}
}

// TestRelation_FR046_SharedPredicate pins that both doors decide "is this
// derived" and "is this a relation" through the SAME function. If someone
// re-inlines either check on one door, the two can drift; these assertions
// are cheap and they name the shared symbol, so a reader of a failure knows
// where the single implementation is meant to live.
func TestRelation_FR046_SharedPredicate(t *testing.T) {
	derived := &records.Property{Name: "margin", Type: records.TypeDecimal, Formula: "a - b"}
	plain := &records.Property{Name: "status", Type: records.TypeText}
	relation := &records.Property{Name: "company", Type: records.TypeRelation}
	person := &records.Property{Name: "owner", Type: records.TypePerson}

	require.True(t, records.IsDerivedProperty(derived))
	require.False(t, records.IsDerivedProperty(plain))
	require.False(t, records.IsDerivedProperty(nil), "a nil property is not a derived declaration")

	require.True(t, records.IsRelationProperty(relation))
	require.True(t, records.IsRelationProperty(person), "person is a relation shape and FR-045 covers it")
	require.False(t, records.IsRelationProperty(plain))
	require.False(t, records.IsRelationProperty(nil))
}

// ---------------------------------------------------------------------------
// The three verbs — list-shaped relation
// ---------------------------------------------------------------------------

// TestRelation_AddPreservesExistingTargets is THE test this whole feature
// exists for. An `add` that produced a correct-looking result by discarding
// the rest of the list would pass any assertion that only checked the new
// target, so this one pins the survivors BY NAME and the list length BY
// COUNT, and does it against a list the caller never sent back.
func TestRelation_AddPreservesExistingTargets(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md",
		"---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n  - \"[[Beta]]\"\n  - \"[[Gamma]]\"\n---\nBody.\n")
	require.Equal(t, 3, relCount(a4Read(t, root, "Deal.md"), "partners"), "fixture must start with three edges")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "add", "targets": []any{"Delta"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "add refused: %s", res.ForLLM)

	got := a4Read(t, root, "Deal.md")
	for _, survivor := range []string{"[[Alpha]]", "[[Beta]]", "[[Gamma]]"} {
		require.Containsf(t, got, survivor,
			"add DISCARDED an existing target (%s) — the one failure these verbs exist to prevent:\n%s",
			survivor, got)
	}
	require.Contains(t, got, "[[Delta]]", "the new target did not land")
	require.Equal(t, 4, relCount(got, "partners"),
		"the list must hold exactly the three originals plus the new one:\n%s", got)
	require.Contains(t, got, "Body.", "the body must be untouched")
}

// TestRelation_AddIsIdempotent pins the contract's "adding a target already
// present is a no-op, not a duplicate", including that it reports unchanged
// rather than erroring — an agent doing bulk work must be able to add
// blindly without reading the list first.
func TestRelation_AddIsIdempotent(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")
	before := a4Read(t, root, "Deal.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "add", "targets": []any{"Alpha"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "a duplicate add must be a no-op, not an error: %s", res.ForLLM)
	require.Equal(t, before, a4Read(t, root, "Deal.md"), "a duplicate add must write nothing")
	require.Contains(t, res.ForLLM, "unchanged")
}

// TestRelation_AddAcceptsBareAndBracketedTargets pins that both spellings an
// agent plausibly sends resolve to the SAME stored edge — so sending back a
// target exactly as it was read does not create a second, broken one.
func TestRelation_AddAcceptsBareAndBracketedTargets(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "add", "targets": []any{"[[Alpha]]"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "a bracketed target must not be refused: %s", res.ForLLM)

	got := a4Read(t, root, "Deal.md")
	require.NotContains(t, got, "[[[[", "a bracketed target was double-wrapped into a broken link:\n%s", got)
	require.Equal(t, 1, relCount(got, "partners"),
		"the bracketed spelling must resolve to the edge already there, not a second one:\n%s", got)
}

// TestRelation_RemoveLeavesTheRestIntact is the mirror of the add
// preservation test: the same "only the named element moves" property, in
// the direction where a careless implementation truncates instead.
func TestRelation_RemoveLeavesTheRestIntact(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md",
		"---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n  - \"[[Beta]]\"\n  - \"[[Gamma]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "remove", "targets": []any{"Beta"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "remove refused: %s", res.ForLLM)

	got := a4Read(t, root, "Deal.md")
	require.NotContains(t, got, "[[Beta]]", "the named target was not removed")
	require.Contains(t, got, "[[Alpha]]", "remove discarded an untouched edge")
	require.Contains(t, got, "[[Gamma]]", "remove discarded an untouched edge")
	require.Equal(t, 2, relCount(got, "partners"), "exactly one edge should have gone:\n%s", got)
}

// TestRelation_RemoveMissingTargetIsNoOp pins "removing a target that is not
// present is a no-op, not an error" — the same blind-write affordance add has.
func TestRelation_RemoveMissingTargetIsNoOp(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")
	before := a4Read(t, root, "Deal.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "remove", "targets": []any{"NotThere"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "removing an absent target must be a no-op: %s", res.ForLLM)
	require.Equal(t, before, a4Read(t, root, "Deal.md"))
}

// TestRelation_ReplaceDiscardsOnPurpose pins the destructive verb actually
// being destructive. `replace` is the one verb allowed to drop edges, and it
// has to work — otherwise a caller who genuinely needs to reset a list is
// left with no way to do it and will reach for something worse.
func TestRelation_ReplaceDiscardsOnPurpose(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md",
		"---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n  - \"[[Beta]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "replace", "targets": []any{"Gamma"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "replace refused: %s", res.ForLLM)

	got := a4Read(t, root, "Deal.md")
	require.Contains(t, got, "[[Gamma]]")
	require.NotContains(t, got, "[[Alpha]]", "replace must discard the old targets")
	require.NotContains(t, got, "[[Beta]]")
	require.Equal(t, 1, relCount(got, "partners"))
}

// TestRelation_ReplaceEmptyClears pins the contract's "an empty targets
// clears the property", and that it is accepted ONLY for replace.
func TestRelation_ReplaceEmptyClears(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md",
		"---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n  - \"[[Beta]]\"\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "replace", "targets": []any{},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "replace with no targets must clear: %s", res.ForLLM)

	got := a4Read(t, root, "Deal.md")
	require.NotContains(t, got, "[[Alpha]]")
	require.Contains(t, got, "partners: []",
		"a cleared list must stay DECLARED and empty, never silently deleted:\n%s", got)
}

// TestRelation_EmptyTargetsRefusedForAddAndRemove pins that the two verbs
// which cannot mean anything with an empty list say so, rather than
// reporting a successful write that wrote nothing.
func TestRelation_EmptyTargetsRefusedForAddAndRemove(t *testing.T) {
	for _, verb := range []string{"add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "kb")
			relSchema(t, root)
			deps, _ := a4Deps(home)
			tool := veTool(deps)
			a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")

			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "relation", "path": "Deal.md",
				"property": "partners", "relation_op": verb, "targets": []any{},
				"expect_version": a4Version(t, root, "Deal.md"),
			})
			require.True(t, res.IsError, "%s with no targets must be refused, not a silent no-op", verb)
			require.Contains(t, res.ForLLM, "replace")
		})
	}
}

// ---------------------------------------------------------------------------
// Scalar relation — FR-035
// ---------------------------------------------------------------------------

// TestRelation_ScalarAddSecondTargetRefused is FR-035. The refusal must name
// the verb that CAN do what the caller wanted, or the caller has been told
// "no" with no route forward.
func TestRelation_ScalarAddSecondTargetRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\ncompany: \"[[Acme Ltd]]\"\n---\nBody.\n")
	before := a4Read(t, root, "Deal.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "company", "relation_op": "add", "targets": []any{"Other Ltd"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.True(t, res.IsError, "FR-035: adding a second target to a scalar relation must be refused")
	require.Contains(t, res.ForLLM, "replace", "the refusal must name the verb that can do this")
	require.Contains(t, res.ForLLM, "[[Acme Ltd]]", "the refusal must name what is already there")
	require.Equal(t, before, a4Read(t, root, "Deal.md"))
}

// TestRelation_ScalarAddThenReplaceThenRemove walks the whole scalar
// lifecycle, because each step's correctness depends on the previous one's
// stored shape.
func TestRelation_ScalarAddThenReplaceThenRemove(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\nstatus: open\n---\nBody.\n")

	// add into an empty slot
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "company", "relation_op": "add", "targets": []any{"Acme Ltd"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "add into an empty scalar slot refused: %s", res.ForLLM)
	require.Contains(t, a4Read(t, root, "Deal.md"), "[[Acme Ltd]]")

	// replace points it somewhere else
	res = tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "company", "relation_op": "replace", "targets": []any{"Other Ltd"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "scalar replace refused: %s", res.ForLLM)
	got := a4Read(t, root, "Deal.md")
	require.Contains(t, got, "[[Other Ltd]]")
	require.NotContains(t, got, "[[Acme Ltd]]")

	// remove clears it, and leaves every other property alone
	res = tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "company", "relation_op": "remove", "targets": []any{"Other Ltd"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.False(t, res.IsError, "scalar remove refused: %s", res.ForLLM)
	got = a4Read(t, root, "Deal.md")
	require.NotContains(t, got, "[[Other Ltd]]")
	require.Contains(t, got, "status: open", "removing one property must not disturb another")
	require.Contains(t, got, "Body.")
}

// ---------------------------------------------------------------------------
// Argument handling and the wrong-op redirect
// ---------------------------------------------------------------------------

// TestRelation_NonRelationPropertyRefused pins that op "relation" refuses a
// property it does not own, naming set_property — the exact mirror of
// set_property's own refusal, so a caller who guesses wrong in either
// direction is routed back rather than left to guess again.
func TestRelation_NonRelationPropertyRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\nstatus: open\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "status", "relation_op": "add", "targets": []any{"Acme"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.True(t, res.IsError, "op relation must refuse a text property")
	require.Contains(t, res.ForLLM, "set_property", "the refusal must name the op that CAN write it")
}

// TestRelation_UnknownPropertyRefused pins the declared-set refusal, with the
// declared names listed so the retry is informed rather than a guess.
func TestRelation_UnknownPropertyRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "nosuch", "relation_op": "add", "targets": []any{"Acme"},
		"expect_version": a4Version(t, root, "Deal.md"),
	})
	require.True(t, res.IsError, "an undeclared property on a governed note must be refused")
	require.Contains(t, res.ForLLM, "partners", "the refusal must list the declared properties")
}

// TestRelation_UngovernedNoteIsUnconstrained is FR-005 on this op: a note
// with no record type has nothing to violate, and a relation on one is
// routine. Refusing here would block the single most common case — linking
// ordinary notes together — which is squarely the capability loss this work
// was told not to cause.
func TestRelation_UngovernedNoteIsUnconstrained(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Plain.md", "---\ntitle: Plain\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Plain.md",
		"property": "related", "relation_op": "add", "targets": []any{"Alpha", "Beta"},
		"expect_version": a4Version(t, root, "Plain.md"),
	})
	require.False(t, res.IsError, "an ordinary note must accept a relation: %s", res.ForLLM)

	got := a4Read(t, root, "Plain.md")
	require.Contains(t, got, "[[Alpha]]")
	require.Contains(t, got, "[[Beta]]")
	require.Equal(t, 2, relCount(got, "related"))
}

// TestRelation_BadArguments covers the argument refusals as a table — each
// one a shape a model actually sends, each asserted to refuse rather than to
// guess.
func TestRelation_BadArguments(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing property", map[string]any{"relation_op": "add", "targets": []any{"X"}}, "'property' is required"},
		{"missing relation_op", map[string]any{"property": "partners", "targets": []any{"X"}}, "'relation_op' is required"},
		{"bad relation_op", map[string]any{"property": "partners", "relation_op": "set", "targets": []any{"X"}}, "must be one of"},
		{"object target", map[string]any{"property": "partners", "relation_op": "add", "targets": []any{map[string]any{"a": 1}}}, "must be a note name or path"},
		{"targets not a list", map[string]any{"property": "partners", "relation_op": "add", "targets": 42.0}, "must be a list"},
		// A genuine argument of a DIFFERENT op, which the global name sweep
		// accepts and only the per-op set catches (EMB-100's distinction).
		{"foreign argument", map[string]any{"property": "partners", "relation_op": "add", "targets": []any{"X"}, "body": "nope"}, "does not read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "kb")
			relSchema(t, root)
			deps, _ := a4Deps(home)
			tool := veTool(deps)
			a4Note(t, root, "Deal.md", "---\ntype: deal\n---\nBody.\n")

			args := map[string]any{
				"collection": "kb", "op": "relation", "path": "Deal.md",
				"expect_version": a4Version(t, root, "Deal.md"),
			}
			for k, v := range tc.args {
				args[k] = v
			}
			res := tool.Execute(a4Ctx("mia", ws), args)
			require.True(t, res.IsError, "expected a refusal, got: %s", res.ForLLM)
			require.Contains(t, res.ForLLM, tc.want)
		})
	}
}

// TestRelation_StaleVersionTokenRefused pins that this op is under the same
// compare-and-swap every other write on this tool is. A relation verb that
// skipped it would reintroduce, through the back door, the concurrent-writer
// loss FR-045 exists to prevent.
func TestRelation_StaleVersionTokenRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	relSchema(t, root)
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	a4Note(t, root, "Deal.md", "---\ntype: deal\npartners:\n  - \"[[Alpha]]\"\n---\nBody.\n")
	stale := a4Version(t, root, "Deal.md")

	// Somebody else writes first.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "add", "targets": []any{"Beta"},
		"expect_version": stale,
	})
	require.False(t, res.IsError, "the first write must succeed: %s", res.ForLLM)

	// The stale token must now be refused.
	res = tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "Deal.md",
		"property": "partners", "relation_op": "add", "targets": []any{"Gamma"},
		"expect_version": stale,
	})
	require.True(t, res.IsError, "a stale version token must be refused")

	got := a4Read(t, root, "Deal.md")
	require.NotContains(t, got, "[[Gamma]]", "the refused write must not have landed")
	require.Contains(t, got, "[[Alpha]]")
	require.Contains(t, got, "[[Beta]]")
}
