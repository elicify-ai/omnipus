package workspace

import "testing"

// The revision is a pure function of the persisted record (UpdatedAt excluded),
// so EVERY writer of core_team - REST PUT, update_workspace, create_agent's
// join, the built-in roster back-fill - moves it. This pins that property: a
// stale client holding the old revision is refused with 409.
func TestRevisionForState_ChangesWhenCoreTeamChanges(t *testing.T) {
	base := Workspace{ID: "ws", Name: "n", Status: "active", CoreTeam: []string{"jim"}}
	r0, err := RevisionForState(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	grown := base
	grown.CoreTeam = []string{"jim", "mia"}
	r1, _ := RevisionForState(grown, nil)
	if r0 == r1 {
		t.Fatal("adding a member must change the revision")
	}
	stamped := base
	stamped.UpdatedAt = "2026-10-12T00:00:00Z"
	r2, _ := RevisionForState(stamped, nil)
	if r0 != r2 {
		t.Fatal("instrument check: UpdatedAt alone must not change the revision (the property under test is the team)")
	}
}
