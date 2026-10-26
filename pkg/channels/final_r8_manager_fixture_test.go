package channels

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Direct fixture admissions stand in for an accepted local config write. Carry
// that exact identity and fingerprint, as the real writer does; never stamp an
// older snapshot with a successor's identity merely to bypass publication checks.
func finalManagerAcceptedSnapshot(t *testing.T, store *PairingAttempts, cfg *config.Config, id, attemptID string) {
	t.Helper()
	rec, ok := store.Attempt(id, attemptID)
	require.True(t, ok)
	cfg.SetChannelOrderGeneration(id, rec.Generation)
	order := store.LockChannel(id)
	order.BindReadySnapshot(cfg)
	order.Unlock()
	require.NoError(t, store.CheckConfigSnapshot(cfg), "instrument: this direct fixture write is actually the accepted snapshot")
}
