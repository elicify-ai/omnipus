// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func selfEdgeIn(edges []workspace.DelegationEdge, id string) bool {
	for _, e := range edges {
		if e.FromAgent == id && e.ToAgent == id {
			return true
		}
	}
	return false
}

// TestHandleWorkspacePut_ExplicitGraph_SeedsNewMemberSelfEdge: session-core
// FR-014/015/016 (founder ruling 2026-10-10). The Team tab's "Add agent"
// submits core_team plus an explicit graph without the new member's self-line.
// The server must still seed the new member's self-edge; the continuing
// member whose self-line the graph omits is not resurrected; the explicit
// edge is kept.
func TestHandleWorkspacePut_ExplicitGraph_SeedsNewMemberSelfEdge(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t) // team: jim, ava, ray, planner

	w := putWorkspaceGraph(t, api, id,
		`,"core_team":["jim","ava","ray","planner"],"delegation":[{"from_agent":"jim","to_agent":"ava","modes":["direct"]}]`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	baseline, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	require.False(t, selfEdgeIn(baseline.Delegation, "ava"), "fixture: ava starts without a self-edge")

	// Remove ray, then add him back together with an explicit graph that lacks his self-line.
	w = putWorkspaceGraph(t, api, id, `,"core_team":["jim","ava","planner"]`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = putWorkspaceGraph(t, api, id,
		`,"core_team":["jim","ava","planner","ray"],"delegation":[{"from_agent":"jim","to_agent":"ava","modes":["direct"]}]`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	state, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	require.True(t, selfEdgeIn(state.Delegation, "ray"),
		"newly added member ray must get a self-edge even though the explicit graph omitted it; edges=%+v", state.Delegation)
	require.False(t, selfEdgeIn(state.Delegation, "ava"),
		"continuing member ava's omitted self-line must not be resurrected; edges=%+v", state.Delegation)
	require.False(t, selfEdgeIn(state.Delegation, "jim"), "continuing member jim must not gain a self-line")
	found := false
	for _, e := range state.Delegation {
		if e.FromAgent == "jim" && e.ToAgent == "ava" {
			found = true
		}
	}
	require.True(t, found, "the explicit edge jim->ava must be kept; edges=%+v", state.Delegation)
	require.Len(t, state.Delegation, 2, "explicit edge plus exactly one seeded self-edge; edges=%+v", state.Delegation)
}
