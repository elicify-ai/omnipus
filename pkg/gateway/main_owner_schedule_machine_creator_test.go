package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/cron"
)

// F2: an EXISTING ownerless main is adopted only by a person. A schedule
// created by the machine principal ("cli") or by no one ("") is not a person,
// so the main stays ownerless and its identity file is not rewritten. (The
// operator fallback applies to a main this run CREATES, never to an existing
// one: create-or-reuse is not adoption.)
func TestOwnerWiringF2_ExistingOwnerlessMainNotAdoptedByMachineOrEmptyCreator(t *testing.T) {
	for _, tc := range []struct{ name, creator string }{
		{name: "machine_principal_cli", creator: "cli"},
		{name: "empty_creator", creator: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store := ownerWiringRunner(t, true) // accounts include an "operator" person
			main, err := store.GetOrCreateMainSession("default", "mia")
			require.NoError(t, err)
			require.Equal(t, "", main.Owner, "fixture: an existing main with no owner")
			updated := ownerWiringHistoricalRecency(t, store, main.ID)
			before := ownerWiringIdentityBytes(t, store, main.ID)

			job := &cron.CronJob{ID: "owner-f2-" + tc.name, AgentID: "mia", CreatedBy: tc.creator, SessionMode: cron.SessionModeMain}
			id, err := runner.pickSession(job, "mia")
			require.NoError(t, err)
			require.Equal(t, main.ID, id, "the existing main is reused")

			after := ownerWiringFreshMeta(t, store, id)
			require.Equal(t, "", after.Owner, "a non-person creator must not give the main an owner, not even the operator")
			require.Equal(t, updated, after.UpdatedAt)
			require.Equal(t, before, ownerWiringIdentityBytes(t, store, id), "the identity file bytes must be unchanged")
		})
	}
}
