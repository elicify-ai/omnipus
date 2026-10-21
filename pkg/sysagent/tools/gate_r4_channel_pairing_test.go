package systools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// whatsappOff seeds a disabled WhatsApp connector and returns its revision.
func whatsappOff(cfg *config.Config) string {
	if cfg.Channels == nil {
		cfg.Channels = map[string]config.ChannelInstanceConfig{}
	}
	cfg.Channels["whatsapp"] = config.ChannelInstanceConfig{Type: "whatsapp", Enabled: false}
	return channelRevision(cfg, "whatsapp")
}

// TestEnableChannel_WhatsAppGoesThroughPairingAdmission is gate r4 R4-A02
// (M1, M2): enable_channel turning a WhatsApp connector on reserves its
// unowned attempt BEFORE saving — a refusal saves nothing — and commits it
// after the save; an enable of a connector already on reserves nothing.
func TestEnableChannel_WhatsAppGoesThroughPairingAdmission(t *testing.T) {
	cases := map[string]struct {
		admitErr  error
		commitOK  bool
		wantCode  string
		wantSaved bool
		wantText  string
	}{
		"refused while another pairing is in flight":   {admitErr: channels.ErrPairingConflict, wantCode: "PAIRING_IN_PROGRESS"},
		"refused while the connector is being deleted": {admitErr: channels.ErrPairingDeleting, wantCode: "CHANNEL_BEING_DELETED"},
		"admitted and committed":                       {commitOK: true, wantSaved: true, wantText: "will connect on the next config reload"},
		"admitted, cancelled before its commit":        {commitOK: false, wantSaved: true, wantText: "pairing was cancelled before it started"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			connect, cfg := connectRevisionFixture(t, func(*config.Config) {})
			rev := whatsappOff(cfg)
			var calls []string
			d := connect.deps
			d.AdmitEnablePairing = func(id string) (string, error) {
				calls = append(calls, "admit enabled="+boolText(cfg.Channels[id].Enabled))
				if tc.admitErr != nil {
					return "", tc.admitErr
				}
				return "attempt-e", nil
			}
			d.CommitPairing = func(id, attemptID string) bool {
				calls = append(calls, "commit "+attemptID+" enabled="+boolText(cfg.Channels[id].Enabled))
				return tc.commitOK
			}
			d.ReleasePairing = func(_, attemptID string) { calls = append(calls, "release "+attemptID) }
			res := NewChannelEnableTool(d).Execute(context.Background(), map[string]any{"id": "whatsapp", "expected_revision": rev})
			require.NotNil(t, res)
			require.Equal(t, "admit enabled=false", calls[0], "admission precedes the save")
			if tc.wantCode != "" {
				require.True(t, res.IsError, res.ForLLM)
				require.Contains(t, res.ForLLM, tc.wantCode)
				require.Contains(t, res.ForLLM, "nothing saved")
				require.False(t, cfg.Channels["whatsapp"].Enabled, "a refusal saves nothing")
				require.Len(t, calls, 1)
				return
			}
			require.Equal(t, tc.wantSaved, cfg.Channels["whatsapp"].Enabled)
			require.Equal(t, "commit attempt-e enabled=true", calls[1], "the commit follows the save")
			require.Contains(t, strings.ToLower(res.ForLLM), tc.wantText, res.ForLLM)
		})
	}
	t.Run("already on: idempotent, nothing reserved", func(t *testing.T) {
		connect, cfg := connectRevisionFixture(t, func(*config.Config) {})
		whatsappOff(cfg)
		ch := cfg.Channels["whatsapp"]
		ch.Enabled = true
		cfg.Channels["whatsapp"] = ch
		admitted := false
		connect.deps.AdmitEnablePairing = func(string) (string, error) { admitted = true; return "x", nil }
		res := NewChannelEnableTool(connect.deps).Execute(context.Background(),
			map[string]any{"id": "whatsapp", "expected_revision": channelRevision(cfg, "whatsapp")})
		require.NotNil(t, res)
		require.False(t, admitted)
	})
}

// TestDisableChannel_RevokesAnUnstartedAdmission is gate r4 R4-A02 (M2): a
// pairing admission that has not started cannot survive disable_channel and
// start later on a re-enable.
func TestDisableChannel_RevokesAnUnstartedAdmission(t *testing.T) {
	connect, cfg := connectRevisionFixture(t, func(*config.Config) {})
	whatsappOff(cfg)
	ch := cfg.Channels["whatsapp"]
	ch.Enabled = true
	cfg.Channels["whatsapp"] = ch
	var revoked []string
	connect.deps.RevokePairingForDisable = func(id string) {
		revoked = append(revoked, id+" enabled="+boolText(cfg.Channels[id].Enabled))
	}
	res := NewChannelDisableTool(connect.deps).Execute(context.Background(),
		map[string]any{"id": "whatsapp", "expected_revision": channelRevision(cfg, "whatsapp")})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	require.Equal(t, []string{"whatsapp enabled=true"}, revoked, "revoked before the disable is saved")
	require.False(t, cfg.Channels["whatsapp"].Enabled)
}

// TestConnectChannel_ReportsWhatItsOwnPairingDid is gate r4 R4-A05 (M14): the
// connect result follows its own admitted attempt — an already-linked device
// is reported connected (no QR), a running pairing needs the QR scanned, and a
// failed start is saved but not working with its cause.
func TestConnectChannel_ReportsWhatItsOwnPairingDid(t *testing.T) {
	cases := map[string]struct {
		state    channels.PairingAttempt
		want     string
		mustLack string
	}{
		"already linked": {
			state: channels.PairingAttempt{Phase: channels.PairingPhaseTerminal, Outcome: channels.PairingOutcomeNoPairing},
			want:  "already linked", mustLack: "scan the whatsapp qr code",
		},
		"pairing running": {
			state: channels.PairingAttempt{Phase: channels.PairingPhaseActive},
			want:  "pairing not finished: scan the whatsapp qr code",
		},
		"start failed": {
			state: channels.PairingAttempt{Phase: channels.PairingPhaseTerminal, Outcome: channels.PairingOutcomeStartFailed},
			want:  "saved but not working", mustLack: "scan the whatsapp qr code",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tool, cfg := connectRevisionFixture(t, func(*config.Config) {})
			rec := &pairingDepsRecorder{commitOK: true}
			rec.wire(tool.deps, cfg)
			tool.deps.PairingAttemptState = func(_, attemptID string) (channels.PairingAttempt, bool) {
				require.Equal(t, "attempt-1", attemptID, "the result reads its own attempt")
				return tc.state, true
			}
			res := tool.Execute(context.Background(), map[string]any{
				"id": "whatsapp", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
			require.NotNil(t, res)
			out := strings.ToLower(res.ForLLM)
			require.Contains(t, out, tc.want, res.ForLLM)
			if tc.mustLack != "" {
				require.NotContains(t, out, tc.mustLack)
			}
		})
	}
}
