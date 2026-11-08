// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the SHIPPED delegation-depth default as exposed on the workspace
// delegation REST surface, plus the gateway's own pin of its mirror constant.
// The explicit-config path (fixture cap 3) stays covered in
// rest_workspace_delegation_test.go; everything here uses the config left
// UNSET with LITERAL expected numbers, so a moved default fails loudly.

package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestDelegationDepthCeilingFallbackPin_IsTen pins the gateway's mirror of
// the shipped delegation-depth backstop to the literal 10 (founder ruling
// 2026-10-27). pkg/gateway cannot import pkg/agent's unexported constant, so
// the mirror is pinned here, in the package that owns it.
func TestDelegationDepthCeilingFallbackPin_IsTen(t *testing.T) {
	if delegationDepthCeilingFallback != 10 {
		t.Fatalf("delegationDepthCeilingFallback = %d, want the literal 10 (founder ruling 2026-10-27)", delegationDepthCeilingFallback)
	}
}

// TestWorkspaceDelegation_GetDefaultDepth_UnsetConfigShipsTen: with
// performance.max_delegation_depth UNSET (the shipped default), GET must
// expose the shipped backstop 10 — a literal, never a fixture number.
func TestWorkspaceDelegation_GetDefaultDepth_UnsetConfigShipsTen(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPIWithPerf(t, 0)
	w := getDelegation(t, api, id)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	d := decodeDelegation(t, w.Body.Bytes())
	assert.Equal(t, 10, d.DefaultDepth,
		"default_depth must expose the shipped default 10 when no operator cap is configured")
}

// TestWorkspaceDelegation_PutRoundTrip_UnsetConfigShipsTen: the same shipped
// default on the PUT round-trip — the response and a following GET expose 10,
// and an edge saved without a depth stays inherit rather than being
// materialised as a number.
func TestWorkspaceDelegation_PutRoundTrip_UnsetConfigShipsTen(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPIWithPerf(t, 0)
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava","modes":["direct"]}]}`
	w := putDelegation(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	put := decodeDelegation(t, w.Body.Bytes())
	require.Len(t, put.Edges, 1)
	assert.Equal(t, 10, put.DefaultDepth,
		"PUT response must expose the shipped default 10 when no operator cap is configured")
	assert.Nil(t, put.Edges[0].Depth,
		"an edge saved without a depth stays inherit — the response must not materialise a depth")

	got := decodeDelegation(t, getDelegation(t, api, id).Body.Bytes())
	require.Len(t, got.Edges, 1)
	assert.Equal(t, 10, got.DefaultDepth,
		"GET after the PUT must still expose the shipped default 10")
	assert.Nil(t, got.Edges[0].Depth,
		"an inherit edge must still be inherit after the round-trip")
}

// TestWorkspaceDelegation_ExplicitDepthThreePersists: operator data saved with
// an explicit edge depth of 3 keeps 3 when the RESOLVED default is the shipped
// 10. The fixture leaves performance.max_delegation_depth UNSET, so "kept 3"
// can only come from preserving the stored value — a fixture cap of 3 would
// make a regression that rewrites a saved 3 to the resolved default invisible
// (the default would be 3 too; mutation-verified). The delegation store is
// seeded directly BEFORE the first read, so both the plain read-back and a PUT
// round-trip must preserve the operator's 3, while default_depth keeps
// exposing the shipped 10 — never the stored edge depth, never an upgrade of
// operator data to the shipped default.
func TestWorkspaceDelegation_ExplicitDepthThreePersists(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPIWithPerf(t, 0) // cap UNSET → default_depth is the shipped 10
	seed := []storedDelegationEdge{{
		FromAgent: "jim",
		ToAgent:   "ava",
		Modes:     []workspace.DelegationMode{workspace.ModeDirect},
		Depth:     intPtrGW(3),
	}}
	require.NoError(t, saveWorkspaceDelegation(api.homePath, id, seed),
		"seed the delegation store BEFORE the first read, so the GET below serves persisted operator data")

	// First read: the stored explicit 3 comes back unchanged, and default_depth
	// exposes the shipped 10 — the stored 3 is neither promoted to the default
	// nor rewritten to it.
	w := getDelegation(t, api, id)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	got := decodeDelegation(t, w.Body.Bytes())
	require.Len(t, got.Edges, 1)
	require.NotNil(t, got.Edges[0].Depth)
	assert.Equal(t, 3, *got.Edges[0].Depth,
		"the stored explicit edge depth 3 must survive a read back unchanged")
	assert.Equal(t, 10, got.DefaultDepth,
		"default_depth must expose the shipped default 10, never the stored edge depth")

	// PUT round-trip: resubmitting the explicit graph keeps 3, still against a
	// default_depth of 10 — the explicit 3 is operator data, not the default.
	body := `{"edges":[{"from_agent":"jim","to_agent":"ava","modes":["direct"],"depth":3}]}`
	w = putDelegation(t, api, id, body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	put := decodeDelegation(t, w.Body.Bytes())
	require.Len(t, put.Edges, 1)
	require.NotNil(t, put.Edges[0].Depth)
	assert.Equal(t, 3, *put.Edges[0].Depth,
		"explicit edge depth 3 must survive the PUT response unchanged")
	assert.Equal(t, 10, put.DefaultDepth,
		"the PUT response must expose the shipped default 10, not upgrade the edge depth into the default")

	got2 := decodeDelegation(t, getDelegation(t, api, id).Body.Bytes())
	require.Len(t, got2.Edges, 1)
	require.NotNil(t, got2.Edges[0].Depth)
	assert.Equal(t, 3, *got2.Edges[0].Depth,
		"explicit edge depth 3 must survive the PUT round-trip unchanged")
	assert.Equal(t, 10, got2.DefaultDepth,
		"GET after the PUT must still expose the shipped default 10")
}
