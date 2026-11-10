package agent

// F1 (security review 2026-10-25, founder ruling Q87): a caller whose
// configuration revision is stale is not current authority. admitPairing
// derived its revision from the served config and InterruptStaleReady checked
// only inequality — so a caller armed with revision R1 interrupted attempt B,
// readied at the newer R2, and slipped into its place. Retirement of a
// stale-ready attempt must validate the current accepted configuration under
// the same ordering boundary; an R1-armed caller is refused (or re-evaluated
// against R2) and attempt B is never interrupted. A foreign Retry is refused
// by the ownership rule anyway and must likewise leave the ready attempt
// untouched.
//
// The interleaving is driven synchronously — never with sleeps, and without a
// synctest bubble: in this toolchain a sync.Mutex park is not a durable block,
// so a caller parked on the held channel order never satisfies
// synctest.Wait. The pause between caller A's capture and its admission is
// realized structurally instead: attempt B is recorded ready at R2 while the
// loop still serves R1 — the same durable-but-unapplied skew the ordering
// model already guards on the write side (CheckConfigSnapshot) — and only
// then does caller A run the real admitPairing path. Deterministic by
// construction: every step is one synchronous call.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestPairingAdmission_StaleSnapshotCannotInterruptTheCurrentReadyAttempt is
// security F1: caller A's captured revision R1 is stale against caller B's
// committed-and-readied R2. B must NOT be interrupted (A's revision is not
// current authority), and A must be refused or re-evaluated against R2 —
// never admitted over the attempt it could not author.
func TestPairingAdmission_StaleSnapshotCannotInterruptTheCurrentReadyAttempt(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())

	for _, tc := range []struct {
		name  string
		admit func(*AgentLoop) (string, error)
	}{
		{
			name:  "stale_connect_capturer_is_refused_and_ready_attempt_stays",
			admit: func(al *AgentLoop) (string, error) { return al.AdmitConnectPairing("whatsapp", "req-a", "chat-a") },
		},
		{
			name:  "stale_retry_capturer_is_refused_and_ready_attempt_stays",
			admit: func(al *AgentLoop) (string, error) { return al.AdmitRetryPairing("whatsapp", "req-a", "chat-a") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newSteerAL(t)
			store := pairingAttemptsFor(t, al)

			// R1: the configuration the loop serves when caller A captures.
			// The instance is enabled so the revision hashes the real entry
			// (toChannelHashes skips disabled ones).
			require.NoError(t, al.MutateConfig(func(c *config.Config) error {
				c.Channels = map[string]config.ChannelInstanceConfig{
					"whatsapp": {Type: "whatsapp", Enabled: true},
				}
				return nil
			}))
			r1 := channels.ChannelRevision(al.GetConfig(), "whatsapp")
			require.NotEmpty(t, r1, "precondition: R1 is the served configuration's own revision")

			// Caller B commits R2 — the channel gains its workspace binding —
			// and readies its attempt at R2. The readiness is recorded on the
			// connector while the loop still serves R1: B's commit is durable,
			// its application (the served snapshot) is the part still pending.
			r2 := channels.ChannelRevision(&config.Config{Channels: map[string]config.ChannelInstanceConfig{
				"whatsapp": {Type: "whatsapp", Enabled: true, WorkspaceID: "sales"},
			}}, "whatsapp")
			require.NotEqual(t, r1, r2, "precondition: B's commit changes the revision")
			admittedB, err := store.Admit("whatsapp", channels.PairingAdmissionConnect,
				&channels.PairingOwner{SessionID: "chat-b", RequestID: "req-b"})
			require.NoError(t, err)
			require.True(t, store.MarkReady("whatsapp", admittedB.AttemptID, r2),
				"caller B readies its attempt at the committed R2")

			// Caller A resumes into admission through the real production path.
			aAttemptID, aErr := tc.admit(al)

			// B is untouched: a stale snapshot never interrupts the current
			// ready attempt, and the stale caller never takes its place.
			bRec, ok := store.Attempt("whatsapp", admittedB.AttemptID)
			require.True(t, ok)
			require.Equal(t, channels.PairingPhaseReady, bRec.Phase,
				"F1: a caller armed with a stale revision must not interrupt the current ready attempt")
			require.Empty(t, bRec.Outcome, "attempt B was never interrupted")
			latest, ok := store.Latest("whatsapp")
			require.True(t, ok)
			require.Equal(t, admittedB.AttemptID, latest.AttemptID, "B stays the latest — no fallback attempt was minted")
			require.ErrorIs(t, aErr, channels.ErrPairingConflict,
				"the stale caller is refused (or re-evaluated against R2), never admitted over B")
			require.Empty(t, aAttemptID)
		})
	}
}
