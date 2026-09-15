// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_edit_type_change_test.go — Codex review 2026-09-14 finding #3
// (High): changing a record's `type` must not create a duplicate (type, id)
// identity.
//
// D-28 taught set_property to mint an id when `type` PROMOTES a plain note.
// It kept an existing id untouched in every other case — including a
// record whose type is being CHANGED. Two schemas without identity prefixes
// each legitimately hold a record `0001`; turning a `task 0001` into a
// `decision` carried `0001` along and produced two `decision 0001`s, making
// record addressing ambiguous with nothing reported.
//
// The disposition chosen is the SAFER one the reviewer offered: a note that
// is already a record (has both `type` and `id`) has its type change REFUSED
// with the way forward named, rather than being re-minted — an identifier
// belongs to its type's own sequence, and rewriting whatever addresses the
// record by (type, id) is a migration this op does not perform.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// unprefixedSchemas declares two record types with NO identity prefix, so
// both mint bare "0001" — the exact pair in which a carried-over id collides.
func unprefixedSchemas(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	for _, typ := range []string{"task", "decision"} {
		yaml := "schema_version: 1\n" +
			"type: " + typ + "\n" +
			"properties:\n" +
			"  status: { type: enum, values: [open, closed] }\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, typ+".yaml"), []byte(yaml), 0o600))
	}
}

func setType(t *testing.T, tool *EditTool, ws, root, rel, typ string) *tools.ToolResult {
	t.Helper()
	return tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "set_property", "path": rel,
		"property": "type", "value": typ, "expect_version": a4Version(t, root, rel),
	})
}

// TestSetProperty_CrossTypeChangeOnARecordIsRefused is the collision case the
// reviewer asked for: the destination type ALREADY holds the identifier.
func TestSetProperty_CrossTypeChangeOnARecordIsRefused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	unprefixedSchemas(t, root)
	const before = "---\ntype: task\nid: \"0001\"\nstatus: open\n---\nBody.\n"
	a4Note(t, root, "Tasks/A.md", before)
	a4Note(t, root, "Decisions/B.md", "---\ntype: decision\nid: \"0001\"\nstatus: open\n---\nBody.\n")

	res := setType(t, tool, ws, root, "Tasks/A.md", "decision")
	require.True(t, res.IsError, "a type change that would duplicate decision 0001 must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "0001", "the refusal must name the identifier at stake")
	require.Contains(t, res.ForLLM, "decision", "the refusal must name the destination type")
	require.Contains(t, res.ForLLM, "Decisions/B.md", "the refusal must name the record already holding that identifier")
	require.Contains(t, res.ForLLM, "op \"create\"", "the refusal must name the way forward")
	require.Equal(t, before, a4Read(t, root, "Tasks/A.md"), "a refused change must leave the note byte-identical")

	// Live identifiers are still unique per type: exactly ONE decision 0001.
	live, err := liveRecordIdentifiers(root, &records.Schema{Type: "decision"})
	require.NoError(t, err)
	require.Equal(t, "Decisions/B.md", live["0001"])
}

// TestSetProperty_CrossTypeChangeIsRefusedEvenWithoutACollisionToday — the
// refusal is about the identifier belonging to another type's sequence, not
// only about a collision that happens to exist right now: the next `decision`
// minted would be 0001 and collide then.
func TestSetProperty_CrossTypeChangeIsRefusedEvenWithoutACollisionToday(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	unprefixedSchemas(t, root)
	const before = "---\ntype: task\nid: \"0001\"\nstatus: open\n---\nBody.\n"
	a4Note(t, root, "Tasks/A.md", before)

	res := setType(t, tool, ws, root, "Tasks/A.md", "decision")
	require.True(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "task")
	require.Contains(t, res.ForLLM, "decision")
	require.Equal(t, before, a4Read(t, root, "Tasks/A.md"))
}

// TestSetProperty_SameTypeIsStillANoOp — re-sending the type a record already
// has is not a type change and must keep working (D-28's own second write).
func TestSetProperty_SameTypeIsStillANoOp(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	unprefixedSchemas(t, root)
	a4Note(t, root, "Tasks/A.md", "---\ntype: task\nid: \"0001\"\nstatus: open\n---\nBody.\n")

	res := setType(t, tool, ws, root, "Tasks/A.md", "task")
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "unchanged")
}

// TestSetProperty_PromotionWithAPreExistingIDChecksTheDestinationType — a
// plain note that already carries an `id:` (an imported note, say) is not a
// cross-type change, but the same uniqueness rule applies to the id it brings
// with it: taken in the destination type → refused naming the holder; free →
// the note keeps its own id and nothing is minted over it.
func TestSetProperty_PromotionWithAPreExistingIDChecksTheDestinationType(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	unprefixedSchemas(t, root)
	a4Note(t, root, "Decisions/B.md", "---\ntype: decision\nid: \"0001\"\nstatus: open\n---\nBody.\n")
	const taken = "---\nid: \"0001\"\ntitle: x\n---\nBody.\n"
	a4Note(t, root, "Imported.md", taken)
	a4Note(t, root, "Imported2.md", "---\nid: \"0042\"\ntitle: y\n---\nBody.\n")

	res := setType(t, tool, ws, root, "Imported.md", "decision")
	require.True(t, res.IsError, "promoting with an id the destination type already holds must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "Decisions/B.md")
	require.Equal(t, taken, a4Read(t, root, "Imported.md"))

	res2 := setType(t, tool, ws, root, "Imported2.md", "decision")
	require.False(t, res2.IsError, res2.ForLLM)
	got := a4Read(t, root, "Imported2.md")
	require.Contains(t, got, "type: decision\n")
	require.Contains(t, got, "id: \"0042\"\n", "a free pre-existing id is kept, not re-minted: %s", got)
	require.NotContains(t, res2.ForLLM, "PROMOTED", "nothing was minted")
}
