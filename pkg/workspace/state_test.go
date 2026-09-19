package workspace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRevisionForStateCoversConfigurationButNotUpdatedAt(t *testing.T) {
	base := Workspace{ID: "01workspace", Name: "Alpha", CoreTeam: []string{"jim", "ava"}, UpdatedAt: "2026-01-01T00:00:00Z"}
	edges := []DelegationEdge{{FromAgent: "jim", ToAgent: "ava", Modes: []DelegationMode{ModeDirect}}}

	revision, err := RevisionForState(base, edges)
	require.NoError(t, err)
	require.Len(t, revision, 64)
	require.Equal(t, strings.ToLower(revision), revision)

	touched := base
	touched.UpdatedAt = "2026-09-17T00:00:00Z"
	touchedRevision, err := RevisionForState(touched, edges)
	require.NoError(t, err)
	require.Equal(t, revision, touchedRevision)

	renamed := base
	renamed.Name = "Beta"
	renamedRevision, err := RevisionForState(renamed, edges)
	require.NoError(t, err)
	require.NotEqual(t, revision, renamedRevision)

	changedGraph := append([]DelegationEdge(nil), edges...)
	changedGraph[0].Modes = []DelegationMode{ModeTask}
	graphRevision, err := RevisionForState(base, changedGraph)
	require.NoError(t, err)
	require.NotEqual(t, revision, graphRevision)
}

func TestCheckRevisionLockedRejectsMalformedAndStaleWithoutChangingState(t *testing.T) {
	home := t.TempDir()
	w := Workspace{ID: "01workspace", Name: "Alpha", CoreTeam: []string{"jim"}}
	require.NoError(t, SaveRecord(home, w))
	unlock := LockID(w.ID)
	defer unlock()

	state, err := ReadStateLocked(home, w.ID)
	require.NoError(t, err)
	_, err = CheckRevisionLocked(home, w.ID, "ABC")
	require.ErrorIs(t, err, ErrInvalidRevision)
	_, err = CheckRevisionLocked(home, w.ID, strings.Repeat("0", 64))
	require.ErrorIs(t, err, ErrRevisionConflict)
	current, err := CheckRevisionLocked(home, w.ID, state.Revision)
	require.NoError(t, err)
	require.Equal(t, state, current)
}

func TestDelegationSelfEdgesAreLimitedToGeneralPurposeAgents(t *testing.T) {
	for _, id := range []string{"jim", "worker"} {
		require.NoError(t, (DelegationEdge{FromAgent: id, ToAgent: id}).ValidateShape())
	}
	for _, id := range []string{"ava", "admin", "custom"} {
		err := (DelegationEdge{FromAgent: id, ToAgent: id}).ValidateShape()
		require.Error(t, err)
	}
}
