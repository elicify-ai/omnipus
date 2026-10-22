package secretrequest_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// §6.7: deletion terminates pending requests. FR-066's install-wide bound
// counts PENDING requests, not orphaned records that can never be answered.
// A control fills all 64 slots before deletion, so accepting the next card
// cannot be explained by failing to enforce the bound at all.
func TestSecretMatrix_DeletionFreesPendingCapacity(t *testing.T) {
	for _, deleted := range []string{"session", "acting agent"} {
		t.Run(deleted, func(t *testing.T) {
			f := newMatrixEnv(t)
			cards := make([]secretrequest.Record, 0, 64)
			for range 64 {
				cards = append(cards, f.pending())
			}
			require.Len(t, f.reg.Snapshot(matrixOwner, ""), 64)
			target := f.newSession("mia", "")
			_, err := f.reg.Create(context.Background(), target, "mia", "web-card", "", providerInput())
			require.EqualError(t, err, "too many pending secret requests", "capacity control before deletion")
			if deleted == "session" {
				require.NoError(t, f.sessions.DeleteSession(cards[0].Card.SessionId))
				requireMatrixNotFound(t, f.reg, cards[0].Card.RequestId)
			} else {
				f.cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore}}
			}
			_, err = f.reg.Create(context.Background(), target, "mia", "web-card", "", providerInput())
			require.NoError(t, err, "§6.7: deleted %s must not permanently occupy pending capacity", deleted)
		})
	}
}
