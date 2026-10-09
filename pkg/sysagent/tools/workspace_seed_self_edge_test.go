package systools

import (
	"testing"

	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// findSelfEdge returns the id→id edge in edges, if any.
func findSelfEdge(edges []workspacepkg.DelegationEdge, id string) (workspacepkg.DelegationEdge, bool) {
	for _, e := range edges {
		if e.FromAgent == id && e.ToAgent == id {
			return e, true
		}
	}
	return workspacepkg.DelegationEdge{}, false
}

// TestSeedDelegationEdgesForNewMembers_SeedsNewMemberSelfRow is the
// team-growth writer of the U5a condition-1 shape: whatever an agent's own
// coreagent policy is (a built-in with a policy, or a custom agent with none),
// adding it to a workspace seeds its ordinary self-row — one shared
// computation, not a per-identity special case.
//
// Expected values derive from the seed-owner-decision (architect c4b574b9c)
// ::One shared workspace-graph seed computation consumed by existing workspace
// creation and authorized team-growth writers, plus DESIGN-RULING
// point 3 ("for every new agent at creation time").
//
// Current code: seedDelegationEdgesForNewMembers derives edges from
// coreagent.SeedDelegationEdges(from); a custom agent ("ctx-bot") has no seeded
// policy and mia's seed contains no self-target, so neither gets a self-row.
func TestSeedDelegationEdgesForNewMembers_SeedsNewMemberSelfRow(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"built-in new member", "mia"},
		{"custom new agent (no coreagent policy)", "ctx-bot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			newTeam := []string{tc.id}
			added := []string{tc.id}
			present := map[string]bool{tc.id: true}

			got := seedDelegationEdgesForNewMembers(newTeam, added, nil, 3, present)

			e, ok := findSelfEdge(got, tc.id)
			if !ok {
				t.Fatalf("a newly added agent %q must receive a seeded self-row at team addition "+
					"(U5a condition-1 shape: one shared seed computation for every writer); got %+v",
					tc.id, got)
			}
			if len(e.Modes) != 2 || !e.Modes[0].Valid() || !e.Modes[1].Valid() {
				// Shape only; membership is asserted below with exact values.
				t.Fatalf("self-row %s→%s modes = %v, want ordinary direct/task", tc.id, tc.id, e.Modes)
			}
			if e.Depth == nil {
				t.Fatalf("self-row %s→%s must carry an explicit depth of min(3, ceiling), got nil (inherit)", tc.id, tc.id)
			}
			if *e.Depth != 3 {
				t.Fatalf("fresh self-row %s→%s depth = %d, want 3 (FreshSelfDelegationMaxDepth)", tc.id, tc.id, *e.Depth)
			}
		})
	}
}

// TestSeedDelegationEdgesForNewMembers_DoesNotResurrectRemovedSelfRow pins the
// deletion/lifetime semantics: an operator-removed self-row stays absent while
// membership continues, even when an unrelated team addition re-runs the seed
// computation; the newly added member is seeded while the continuing member is
// not.
func TestSeedDelegationEdgesForNewMembers_DoesNotResurrectRemovedSelfRow(t *testing.T) {
	// mia is a continuing member whose self-row the operator removed (it is
	// deliberately absent from `existing`). An unrelated update adds "newbie".
	newTeam := []string{"mia", "newbie"}
	added := []string{"newbie"}
	present := map[string]bool{"mia": true, "newbie": true}

	got := seedDelegationEdgesForNewMembers(newTeam, added, nil, 3, present)

	// Lifetime: removed stays removed while mia's membership continues.
	if e, ok := findSelfEdge(got, "mia"); ok {
		t.Fatalf("a removed self-row must NOT be resurrected by an unrelated team addition "+
			"(seed-owner-decision ::deletion is authoritative for the continuing membership); got %+v", e)
	}
	// And the newly added member is seeded.
	if _, ok := findSelfEdge(got, "newbie"); !ok {
		t.Fatalf("the newly added member \"newbie\" must receive a self-row; got %+v", got)
	}
}
