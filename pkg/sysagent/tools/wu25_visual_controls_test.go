package systools_test

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wu25AssertOnlyEntityChanged(t *testing.T, h *wu25Harness, id string, before map[string]string) {
	t.Helper()
	after := wu25Snapshot(t, h.home)
	entity := filepath.Join(h.home, "entities", "agents", id+".json")
	require.Contains(t, before, entity, "snapshot instrument must see the real pre-edit entity")
	require.Contains(t, after, entity, "visual edit must not delete the entity")
	delete(before, entity)
	delete(after, entity)
	assert.Equal(t, before, after, "R6: visual edits may only modify the agent entity, never instructions/config/heartbeat")
}

// These are deliberately GREEN controls, separate from the WU-25 RED claims.
// They prove the registry, policy filter, actual writer, error path, catalog
// reader, and zero-write instrument work on the pre-change production tree.
func TestWU25Controls_RealRegistryAndStore(t *testing.T) {
	t.Run("catalog_and_model_schema_instrument", func(t *testing.T) {
		h := newWU25Harness(t)
		counts := map[string]int{"figure": 5, "role": 31, "color": 10} // read catalog counts, 2026-10-14
		anchors := map[string]string{"figure": "Robot", "role": "developer", "color": "#6E8BA8"}
		for _, catalog := range wu25Catalogs(t) {
			assert.Len(t, catalog.values, counts[catalog.field])
			assert.Contains(t, catalog.values, anchors[catalog.field])
		}
		for _, name := range []string{"create_agent", "update_agent"} {
			assert.Contains(t, wu25Properties(t, wu25Function(t, h.registry, name)), "color")
		}
	})
	t.Run("create_color_only_real_writer", func(t *testing.T) {
		h := newWU25Harness(t)
		args := wu25CreateArgs("Color Control", "Main")
		args["color"] = "#6e8ba8"
		body := wu25Success(t, h.call(t, "create_agent", args))
		id, ok := body["id"].(string)
		require.True(t, ok)
		require.NotEmpty(t, id)
		state := h.state(t, id)
		assert.Equal(t, "Color Control", state.Agent.Name)
		assert.Equal(t, "#6E8BA8", state.Agent.Color)
		assert.Equal(t, args["soul"], state.Soul)
		assert.Equal(t, "metadata_only", body["status"], "control does not claim activation/workspace membership")
	})
	t.Run("fixture_is_durable", func(t *testing.T) {
		h := newWU25Harness(t)
		id := wu25SeedAgent(t, h, "Main")
		state, err := agentstore.New(h.home).ReadState(id)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"figure": "Robot", "role": "writer", "color": "#A97142"}, wu25StoredVisuals(state.Agent))
		assert.Equal(t, "Only prepare drafts; never change permissions.", state.Soul)
		assert.Equal(t, state.Revision, h.read(t, id)["revision"])
	})
	t.Run("sparse_color_only_preserves_function", func(t *testing.T) {
		h := newWU25Harness(t)
		id := wu25SeedAgent(t, h, "Main")
		before := h.state(t, id)
		files := wu25Snapshot(t, h.home)
		wu25Success(t, h.call(t, "update_agent", map[string]any{
			"id": id, "revision": before.Revision, "color": "#7B7290",
		}))
		after := h.state(t, id)
		assert.Equal(t, map[string]string{"figure": "Robot", "role": "writer", "color": "#7B7290"}, wu25StoredVisuals(after.Agent))
		assert.NotEqual(t, before.Revision, after.Revision)
		assert.Equal(t, wu25StableConfig(before.Agent), wu25StableConfig(after.Agent))
		assert.Equal(t, before.Soul, after.Soul)
		wu25AssertOnlyEntityChanged(t, h, id, files)
	})
	for _, operation := range []string{"create_agent", "update_agent"} {
		t.Run(operation+"_invalid_color_control", func(t *testing.T) {
			h := newWU25Harness(t)
			args := wu25CreateArgs("Rejected Color", "Main")
			if operation == "update_agent" {
				id := wu25SeedAgent(t, h, "Main")
				args = map[string]any{"id": id, "revision": h.state(t, id).Revision}
			}
			before := wu25Snapshot(t, h.home)
			args["color"] = "#22C55E" // well-formed but outside AgentColor.yaml
			result := h.call(t, operation, args)
			require.True(t, result.IsError)
			wu25AssertInvalidReason(t, result.ForLLM, "color")
			assert.Equal(t, before, wu25Snapshot(t, h.home))
		})
	}
	t.Run("protected_color_control", func(t *testing.T) {
		h := newWU25Harness(t)
		found := false
		for _, agent := range h.cfg.Agents.List {
			if agent.ID != "omnipus" {
				continue
			}
			found = true
			wu25StoreAgent(t, h, agent)
			state := h.state(t, agent.ID)
			before := wu25Snapshot(t, h.home)
			result := h.call(t, "update_agent", map[string]any{
				"id": agent.ID, "revision": state.Revision, "color": "#A97142",
			})
			require.True(t, result.IsError)
			assert.Contains(t, result.ForLLM, "PROTECTED_FIELD")
			assert.Contains(t, result.ForLLM, "color")
			assert.Equal(t, state, h.state(t, agent.ID))
			assert.Equal(t, before, wu25Snapshot(t, h.home))
		}
		require.True(t, found, "protected color control must actually exercise Omnipus")
	})
}
