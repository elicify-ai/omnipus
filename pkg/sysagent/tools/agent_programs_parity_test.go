// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: the tool-level rows of the agent `programs` parity (FR-007):
// S-14 (set_agent_permissions sets programs after approval), S-15 (the
// existing self-change refusal covers programs too) and S-16 (update_agent
// refuses programs, naming set_agent_permissions). The REST rows live in
// pkg/gateway; the run-time rows with the env/git integration slices.
//
// Oracle: FR-007 ("set through ... set_agent_permissions.programs (self-
// change refused), refused by update_agent") and S-14–S-16's Then clauses.
// Expected values come from the spec, never from the tools.
//
// "Unchanged" is judged on the FULL persisted agentstore record as a decoded
// JSON map (field-agnostic before/after compare, CHECK F16) — a refusal may
// change no field at all, and the S-14 write may change nothing outside
// `programs`. The agent's own reader (`get_agent`) must show the same
// programs list the record holds, with the record's revision. The parity
// itself is written down in pkg/agentmutation/policy.go::MappingRows and
// ::ExcludedRows (AG-07): the last subtest pins that the map carries the
// programs rows, so the two surfaces cannot drift apart even while both
// tools happen to refuse today.
//
// S-14's "GitHub connected" Given leg is carried by the gateway lifecycle
// rows (fake gh, pkg/gateway/programs_lifecycle_rest_test.go): switch-on
// does not require connection (S-17), so this Unit row needs only the
// record's initial state. At RED `programs` is an unknown argument to both
// tools, get_agent has no programs field, and the parity maps carry no
// programs rows — every failure below names its missing surface, never a
// skip.
//
// RED expectations: S-14 fails (programs is an unknown argument today);
// S-15 passes (the self-change refusal predates programs and must keep
// firing with programs in the args — a regression row); S-16 fails (the
// refusal does not name set_agent_permissions); the parity subtest fails
// (no programs rows in the maps).

package systools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// s16ProgramsRefusal is S-16's oracle: the refusal names the one tool that
// may set programs.
const s16ProgramsRefusal = "set_agent_permissions"

// s16RecordSnapshot reads the persisted agentstore record and decodes it to
// a field-agnostic JSON map — the before/after state oracle (CHECK F16).
func s16RecordSnapshot(t *testing.T, store *agentstore.Store, id string) map[string]any {
	t.Helper()
	rec, err := store.Get(id)
	require.NoError(t, err, "instrument: the persisted record reads")
	raw, merr := json.Marshal(rec)
	require.NoError(t, merr, "instrument: the record marshals")
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m), "instrument: the record decodes to a map")
	return m
}

// s16GetAgentPrograms reads the agent through its own tool (get_agent,
// fields ["programs"]) and returns the decoded result. At RED `programs` is
// not a selectable field and the read errors — the caller's BLOCKED case.
func s16GetAgentPrograms(t *testing.T, deps *systools.Deps, id string) map[string]any {
	t.Helper()
	result := systools.NewAgentGetTool(deps).Execute(context.Background(), map[string]any{
		"id":     id,
		"fields": []any{"programs"},
	})
	if result.IsError {
		t.Fatalf("BLOCKED: get_agent cannot read programs — required by FR-007/S-14 (the reader said: %s)", result.ForLLM)
	}
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &got),
		"the get_agent result decodes: %s", result.ForLLM)
	return got
}

func TestAgentPrograms_ParityRows(t *testing.T) {
	t.Run("S14_set_agent_permissions_sets_programs", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		require.NoError(t, store.Create("dev", &config.AgentConfig{ID: "dev", Name: "Dev"}))
		before := s16RecordSnapshot(t, store, "dev")
		require.Empty(t, before["programs"], "the Given: Dev's programs starts empty")

		result := systools.NewAgentPermissionsTool(deps).Execute(context.Background(), map[string]any{
			"id":       "dev",
			"revision": currentAgentRevision(t, deps, "dev"),
			"programs": []any{"github"},
		})
		require.False(t, result.IsError,
			"S-14: programs is set through set_agent_permissions: %s", result.ForLLM)
		require.Contains(t, result.ForLLM, "programs", "the change reports the programs field")

		// Then: Dev's programs is ["github"] — in the persisted record, and
		// nothing outside programs changed.
		after := s16RecordSnapshot(t, store, "dev")
		require.Equal(t, []any{"github"}, after["programs"],
			"S-14: the persisted record carries exactly programs [github]: %v", after["programs"])
		delete(before, "programs")
		delete(after, "programs")
		require.Equal(t, before, after,
			"the write changed nothing in the persisted record outside programs")

		// The agent's own reader agrees: get_agent shows the same list with
		// the record's current revision.
		got := s16GetAgentPrograms(t, deps, "dev")
		require.Equal(t, []any{"github"}, got["programs"],
			"get_agent reports the set list (got %v)", got["programs"])
		require.Equal(t, currentAgentRevision(t, deps, "dev"), got["revision"],
			"get_agent's revision is the record's current revision")
	})

	t.Run("S10_tool_create_starts_with_programs_off", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), createNativeArgs("Programs off"))
		require.False(t, result.IsError, result.ForLLM)
		id := extractID(t, result.ForLLM)
		got := s16GetAgentPrograms(t, deps, id)
		programs, ok := got["programs"].([]any)
		require.True(t, ok, "reader exposes the empty list, not an absent field")
		require.Empty(t, programs)
	})

	t.Run("create_cannot_enable_programs", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		before, skipped, err := store.List()
		require.NoError(t, err)
		require.Empty(t, skipped)
		args := createNativeArgs("Must not enable programs")
		args["programs"] = []any{"github"}
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), args)
		require.True(t, result.IsError, "Agent tool field table: programs is not settable on create")
		after, skipped, err := store.List()
		require.NoError(t, err)
		require.Empty(t, skipped)
		require.Equal(t, before, after, "refused create writes no agent")
	})

	t.Run("S14_real_approval_before_programs_mutation", func(t *testing.T) {
		// The actual hold/reject/approve scenario is relocated to package agent
		// TestAgentPrograms_RealLoopApproval, which can drive the real loop.
		deps, _ := newTestDeps(t)
		properties, ok := systools.NewAgentPermissionsTool(deps).Parameters()["properties"].(map[string]any)
		require.True(t, ok)
		require.Contains(t, properties, "programs", "FR-007: the executable permission tool exposes Programs")
	})

	t.Run("S15_self_change_refused_with_programs_too", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		require.NoError(t, store.Create("dev", &config.AgentConfig{ID: "dev", Name: "Dev"}))
		before := s16RecordSnapshot(t, store, "dev")

		ctx := tools.WithAgentID(context.Background(), "dev")
		result := systools.NewAgentPermissionsTool(deps).Execute(ctx, map[string]any{
			"id":       "dev",
			"revision": currentAgentRevision(t, deps, "dev"),
			"programs": []any{"github"},
		})
		require.True(t, result.IsError, "the existing self-change refusal fires")
		require.Contains(t, result.ForLLM, "SELF_PERMISSION_CHANGE",
			"the refusal is the existing self-change refusal: %s", result.ForLLM)
		after := s16RecordSnapshot(t, store, "dev")
		require.Equal(t, before, after,
			"S-15: unchanged — the refusal wrote no field of the persisted record (CHECK F16: the full record, not one field)")
	})

	t.Run("S16_update_agent_refuses_programs_naming_the_permissions_tool", func(t *testing.T) {
		deps, _ := newTestDeps(t)
		store := agentstore.New(deps.Home)
		require.NoError(t, store.Create("dev", &config.AgentConfig{ID: "dev", Name: "Dev"}))
		before := s16RecordSnapshot(t, store, "dev")

		result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
			"id":       "dev",
			"revision": currentAgentRevision(t, deps, "dev"),
			"programs": []any{"github"},
		})
		require.True(t, result.IsError, "S-16: update_agent refuses a programs change")
		require.Contains(t, result.ForLLM, s16ProgramsRefusal,
			"the refusal names set_agent_permissions: %s", result.ForLLM)
		after := s16RecordSnapshot(t, store, "dev")
		require.Equal(t, before, after,
			"S-16: unchanged — the refusal wrote no field of the persisted record (CHECK F16: the full record, not one field)")
	})

	t.Run("parity_maps_carry_the_programs_rows", func(t *testing.T) {
		// FR-007's parity is written down (pkg/agentmutation/policy.go,
		// Table T-AGENT): the REST↔tool map must carry programs as a
		// set_agent_permissions update leaf, and update_agent must exclude it
		// with the AG-07 reason — the same rule S-16's refusal pins. A
		// missing row lets the REST and tool surfaces drift even while both
		// happen to refuse today.
		mapped := false
		for _, row := range agentmutation.MappingRows() {
			if row.RESTPath == "programs" {
				require.Equal(t, "set_agent_permissions", row.Tool)
				require.Equal(t, "programs", row.ToolPath)
				require.Equal(t, agentmutation.OpUpdate, row.Ops, "no create or OpBoth mapping may enable programs")
			}
			if row.RESTPath == "programs" && row.Tool == "set_agent_permissions" &&
				row.ToolPath == "programs" && row.Ops == agentmutation.OpUpdate {
				mapped = true
			}
		}
		require.True(t, mapped,
			"MappingRows carries REST programs → set_agent_permissions.programs (OpUpdate, FR-007/AG-07)")
		excluded := false
		for _, row := range agentmutation.ExcludedRows() {
			// The canonical recursive guard spells array leaves with [].
			if row.Side == "REST" && row.Path == "programs[]" &&
				row.Ops == agentmutation.OpUpdate &&
				strings.Contains(row.Reason, s16ProgramsRefusal) {
				excluded = true
			}
		}
		require.True(t, excluded,
			"ExcludedRows excludes REST programs[] from update_agent with the AG-07 set_agent_permissions reason")
		// FR-007: programs starts empty and is not settable on create. There
		// is no create leaf to exclude; asserting an invented row breaks t8.
		for _, row := range agentmutation.MappingRows() {
			if row.RESTPath == "programs" || row.RESTPath == "programs[]" {
				require.Equal(t, agentmutation.OpUpdate, row.Ops, "no create mapping may enable programs")
			}
		}
	})
}
