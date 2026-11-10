package systools_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R1 (#1303): all choices come from the existing contracts. Native Main and
// Subagent creation must support a complete visual assignment, not just color.
func TestWU25CreateAgent_AcceptsEveryCatalogVisual(t *testing.T) {
	for _, runtimeType := range []string{"Main", "Subagent"} {
		for _, catalog := range wu25Catalogs(t) {
			for _, value := range catalog.values {
				t.Run(runtimeType+"/"+catalog.field+"/"+value, func(t *testing.T) {
					h := newWU25Harness(t)
					args := wu25CreateArgs("Visual Creation", runtimeType)
					want := map[string]string{"figure": "Robot", "role": "writer", "color": "#A97142"}
					want[catalog.field] = value
					for field, visual := range want {
						args[field] = visual
					}
					created := wu25Success(t, h.call(t, "create_agent", args))
					id, ok := created["id"].(string)
					require.True(t, ok)
					require.NotEmpty(t, id)
					state := h.state(t, id)
					assert.Equal(t, want, wu25StoredVisuals(state.Agent), "R1: all supplied visuals must be durably saved")
					assert.Equal(t, args["description"], state.Agent.Description)
					assert.Equal(t, args["soul"], state.Soul)
					require.NotNil(t, state.Agent.Model)
					assert.Equal(t, "test/model", state.Agent.Model.Primary)
					h.reopen(t)
					read := h.read(t, id)
					wu25AssertVisuals(t, read, want)
					assert.Equal(t, runtimeType, read["type"], "role is presentation, not a runtime selector")
				})
			}
		}
	}
}

// R1/R2/R6: each catalog value is changed independently on all three custom
// runtime classes. Omitted visuals and every functional field must survive.
func TestWU25UpdateAgent_AcceptsEveryCatalogVisual(t *testing.T) {
	for _, runtimeType := range []string{"Main", "Subagent", "subagent_3p"} {
		for _, catalog := range wu25Catalogs(t) {
			for _, value := range catalog.values {
				t.Run(runtimeType+"/"+catalog.field+"/"+value, func(t *testing.T) {
					h := newWU25Harness(t)
					id := wu25SeedAgent(t, h, runtimeType)
					before := h.state(t, id)
					descriptor, present := wu25Descriptors(t, h.read(t, id))[catalog.field]
					require.True(t, present, "R3: each custom runtime must describe its visual input")
					assert.Equal(t, true, descriptor["editable"], "R3: accepted %s must be advertised for %s", catalog.field, runtimeType)
					want := map[string]string{"figure": "Robot", "role": "writer", "color": "#A97142"}
					want[catalog.field] = value
					wu25Success(t, h.call(t, "update_agent", map[string]any{
						"id": id, "revision": before.Revision, catalog.field: value,
					}))
					after := h.state(t, id)
					assert.Equal(t, want, wu25StoredVisuals(after.Agent))
					assert.Equal(t, wu25StableConfig(before.Agent), wu25StableConfig(after.Agent), "R6: presentation must not change functional configuration")
					assert.Equal(t, before.Soul, after.Soul, "R6: original mandate must remain byte-for-byte")
					h.reopen(t)
					read := h.read(t, id)
					wu25AssertVisuals(t, read, want)
					assert.Equal(t, runtimeType, read["type"])
				})
			}
		}
	}
}

// R2: this read test deliberately bypasses the currently broken visual writes
// only for fixture setup. It isolates omission in the actual get_agent result.
func TestWU25GetAgent_ReturnsSavedVisuals(t *testing.T) {
	for _, runtimeType := range []string{"Main", "Subagent", "subagent_3p"} {
		t.Run(runtimeType, func(t *testing.T) {
			h := newWU25Harness(t)
			id := wu25SeedAgent(t, h, runtimeType)
			state := h.state(t, id)
			h.reopen(t)
			body := h.read(t, id)
			wu25AssertVisuals(t, body, map[string]string{"figure": "Robot", "role": "writer", "color": "#A97142"})
			assert.Equal(t, state.Revision, body["revision"], "readback must describe the saved revision")
			assert.Equal(t, runtimeType, body["type"])
		})
	}
}

// R2: color → role → figure → ordinary description is a real sparse sequence.
// Every step uses the revision from get_agent, and reopens the registry/store;
// a readback cache cannot stand in for persistence. Inputs are catalog values.
func TestWU25Visuals_SparseUpdateRoundTrip(t *testing.T) {
	for _, runtimeType := range []string{"Main", "Subagent", "subagent_3p"} {
		t.Run(runtimeType, func(t *testing.T) {
			h := newWU25Harness(t)
			id := wu25SeedAgent(t, h, runtimeType)
			want := map[string]string{"figure": "Robot", "role": "writer", "color": "#A97142"}
			for _, step := range []struct{ field, value string }{
				{"color", "#7B7290"}, {"role", "security"}, {"figure", "Woman"},
				{"description", "Changed only by an explicit ordinary edit."},
			} {
				readBefore := h.read(t, id)
				revision, ok := readBefore["revision"].(string)
				require.True(t, ok)
				require.NotEmpty(t, revision)
				before := h.state(t, id)
				wu25Success(t, h.call(t, "update_agent", map[string]any{
					"id": id, "revision": revision, step.field: step.value,
				}))
				if step.field != "description" {
					want[step.field] = step.value
				}
				after := h.state(t, id)
				assert.Equal(t, want, wu25StoredVisuals(after.Agent), "omission must retain every unmentioned visual at step %s", step.field)
				assert.NotEqual(t, revision, after.Revision, "this step changed an actual value")
				assert.Equal(t, before.Agent.Tools, after.Agent.Tools)
				assert.Equal(t, before.Agent.Type, after.Agent.Type)
				assert.Equal(t, before.Agent.Subagents, after.Agent.Subagents)
				assert.Equal(t, before.Soul, after.Soul)
				h.reopen(t)
				readAfter := h.read(t, id)
				wu25AssertVisuals(t, readAfter, want)
				assert.Equal(t, after.Revision, readAfter["revision"])
			}
		})
	}
}

// AgentColor's contract explicitly normalizes letter-case. An enum added to
// the tool schema must not accidentally make lowercase catalog colors unusable.
func TestWU25Color_LowercaseRoundTripsCanonical(t *testing.T) {
	for _, operation := range []string{"create_agent", "update_agent"} {
		t.Run(operation, func(t *testing.T) {
			h := newWU25Harness(t)
			args := wu25CreateArgs("Canonical Visual", "Main")
			id := ""
			if operation == "update_agent" {
				id = wu25SeedAgent(t, h, "Main")
				args = map[string]any{"id": id, "revision": h.state(t, id).Revision}
			}
			args["color"] = "#6e8ba8"
			body := wu25Success(t, h.call(t, operation, args))
			if operation == "create_agent" {
				var ok bool
				id, ok = body["id"].(string)
				require.True(t, ok)
				require.NotEmpty(t, id)
			}
			assert.Equal(t, "#6E8BA8", h.state(t, id).Agent.Color)
			h.reopen(t)
			assert.Equal(t, "#6E8BA8", h.read(t, id)["color"], "R2: normalized saved color must be returned to the model")
		})
	}
}
