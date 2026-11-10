package secretrequest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// TestStop_FailedVetoPublicationIsRetriedByTheNextStop is Train 2 gate r1
// SFH T2-SF-08: when publishing an explicit Stop's request-level veto fails,
// the in-memory flag must not count as durable. A repeated Stop after the
// storage is repaired must publish the veto, so a fresh registry (restart)
// reads ResumeStopped=true from disk.
func TestStop_FailedVetoPublicationIsRetriedByTheNextStop(t *testing.T) {
	f := newMatrixEnv(t)
	sid := f.newSession("omnipus", "")
	rec := submitMatrix(t, f, f.create(sid, "omnipus", providerInput()))
	require.Equal(t, "submitted", rec.Card.State)

	// Obstruct only the request-state publication: a directory where the
	// state file is atomically renamed into place.
	statePath := filepath.Join(f.home, "sessions", sid, "secret_requests.json")
	require.NoError(t, os.Remove(statePath), "instrument: submit published the state file")
	require.NoError(t, os.Mkdir(statePath, 0o700))

	require.Error(t, f.reg.Stop(sid), "the first Stop must report the failed veto publication")

	require.NoError(t, os.Remove(statePath), "repair the obstruction")
	require.NoError(t, f.reg.Stop(sid), "the repeated Stop must succeed")

	raw, err := f.sessions.ReadSecretRequestState(sid)
	require.NoError(t, err)
	var records []secretrequest.Record
	require.NoError(t, json.Unmarshal(raw, &records))
	found := false
	for _, r := range records {
		if r.Card.RequestId == rec.Card.RequestId {
			found = true
			require.True(t, r.ResumeStopped, "the repeated Stop must publish the durable veto")
		}
	}
	require.True(t, found, "the repeated Stop must publish the request's state")
}
