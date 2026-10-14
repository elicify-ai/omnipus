package systools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R5 / ADR-090: protect the actual fresh-install roster, including Omnipus,
// Judge, Plan Supervisor, ordinary built-ins, and compiled-in native workers.
// Even a same-value echo or an otherwise legal model change is refused in full.
func TestWU25ProtectedAgents_RefuseVisualEditsWithClearReason(t *testing.T) {
	roster := newWU25Harness(t).cfg.Agents.List
	protectedIDs := map[string]bool{}
	for _, agent := range roster {
		if !agent.Locked && agent.Type != config.AgentTypeSystem {
			continue
		}
		protectedIDs[agent.ID] = true
		for _, catalog := range wu25Catalogs(t) {
			saved := wu25StoredVisuals(&agent)[catalog.field]
			changed := ""
			for _, value := range catalog.values {
				if value != saved {
					changed = value
					break
				}
			}
			require.NotEmpty(t, changed, "protected negative case must attempt an actual different choice")
			for _, attempt := range []struct{ name, value string }{
				{"changed", changed}, {"echo", saved},
			} {
				t.Run(agent.ID+"/"+catalog.field+"/"+attempt.name, func(t *testing.T) {
					h := newWU25Harness(t)
					wu25StoreAgent(t, h, agent)
					state := h.state(t, agent.ID)
					descriptors := wu25Descriptors(t, h.read(t, agent.ID))
					descriptor, exists := descriptors[catalog.field]
					require.True(t, exists)
					assert.Equal(t, false, descriptor["editable"], "R5: protected visual is not advertised as editable")
					reason, isString := descriptor["reason"].(string)
					require.True(t, isString)
					assert.Contains(t, reason, "protected", "R5: descriptor explains the protection")
					before := wu25Snapshot(t, h.home)
					result := h.call(t, "update_agent", map[string]any{
						"id": agent.ID, "revision": state.Revision,
						"model": "test/replacement", catalog.field: attempt.value,
					})
					require.True(t, result.IsError, "R5: protected identity cannot be edited: %s", result.ForLLM)
					assert.Contains(t, result.ForLLM, "PROTECTED_FIELD", "R5: protection refusal, not an unsupported/missing-input error")
					assert.Contains(t, result.ForLLM, catalog.field)
					assert.Contains(t, result.ForLLM, "protected")
					assert.Equal(t, state, h.state(t, agent.ID), "R5: mixed protected edit must not save the model either")
					assert.Equal(t, before, wu25Snapshot(t, h.home))
				})
			}
		}
	}
	for _, id := range []string{"omnipus", "judge", "plansupervisor"} {
		assert.True(t, protectedIDs[id], "protected-roster enumeration must include the named #1303 control %s", id)
	}
}

// R6: compare the full saved configuration minus ONLY figure, role, color and
// the edit timestamp. Role "security" must not grant tools, and role "writer"
// must not rewrite instructions. External CLI selection is not presentation.
func TestWU25VisualEdit_DoesNotChangeFunctionalConfiguration(t *testing.T) {
	for _, runtimeType := range []string{"Main", "Subagent", "subagent_3p"} {
		for _, patch := range []struct {
			name   string
			fields map[string]any
			want   map[string]string
		}{
			{"figure", map[string]any{"figure": "Woman"}, map[string]string{"figure": "Woman", "role": "writer", "color": "#A97142"}},
			{"role", map[string]any{"role": "security"}, map[string]string{"figure": "Robot", "role": "security", "color": "#A97142"}},
			{"color", map[string]any{"color": "#7B7290"}, map[string]string{"figure": "Robot", "role": "writer", "color": "#7B7290"}},
			{"all", map[string]any{"figure": "Woman", "role": "security", "color": "#7B7290"}, map[string]string{"figure": "Woman", "role": "security", "color": "#7B7290"}},
		} {
			t.Run(runtimeType+"/"+patch.name, func(t *testing.T) {
				h := newWU25Harness(t)
				id := wu25SeedAgent(t, h, runtimeType)
				before := h.state(t, id)
				filesBefore := wu25Snapshot(t, h.home)
				args := map[string]any{"id": id, "revision": before.Revision}
				for field, value := range patch.fields {
					args[field] = value
				}
				wu25Success(t, h.call(t, "update_agent", args))
				after := h.state(t, id)
				assert.Equal(t, patch.want, wu25StoredVisuals(after.Agent), "must apply the requested presentation, not a no-op")
				assert.Equal(t, wu25StableConfig(before.Agent), wu25StableConfig(after.Agent), "R6: ALL nonvisual fields stay unchanged")
				assert.Equal(t, before.Soul, after.Soul, "R6: mandate remains unchanged")
				wu25AssertOnlyEntityChanged(t, h, id, filesBefore)
				h.reopen(t)
				wu25AssertVisuals(t, h.read(t, id), patch.want)
			})
		}
	}
}
