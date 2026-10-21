package systools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// pairingDepsRecorder fakes the agent loop's pairing admission for the tool
// and records what the connect asked of it, in order.
type pairingDepsRecorder struct {
	calls     []string
	admitErr  error
	commitOK  bool
	restarted []string
}

func (p *pairingDepsRecorder) wire(d *Deps, cfg *config.Config) {
	d.AdmitPairing = func(id, _, _ string) (string, error) {
		_, saved := cfg.Channels[id]
		p.calls = append(p.calls, "admit saved="+boolText(saved))
		if p.admitErr != nil {
			return "", p.admitErr
		}
		return "attempt-1", nil
	}
	d.CommitPairing = func(id, attemptID string) bool {
		_, saved := cfg.Channels[id]
		p.calls = append(p.calls, "commit "+attemptID+" saved="+boolText(saved))
		return p.commitOK
	}
	d.ReleasePairing = func(_, attemptID string) { p.calls = append(p.calls, "release "+attemptID) }
	d.SettlePairing = func(_, attemptID string) { p.calls = append(p.calls, "settle "+attemptID) }
	d.RequestChannelRestart = func(id string) bool {
		p.restarted = append(p.restarted, id)
		return true
	}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestConnectChannel_RestartsAChannelStoppedInPlace is Train 2 gate r1
// architect F04, re-aimed for gate r3 M10: a channel stopped in place keeps an
// unchanged config, so the reload alone never restarts it. An approved
// connect_channel asks the reload lifecycle for a fresh start — a WhatsApp
// connect through its admitted, committed pairing attempt (which arms the
// restart), any other channel through RequestChannelRestart.
func TestConnectChannel_RestartsAChannelStoppedInPlace(t *testing.T) {
	t.Run("whatsapp: admitted before the save, committed after it", func(t *testing.T) {
		tool, cfg := connectRevisionFixture(t, func(*config.Config) {})
		rec := &pairingDepsRecorder{commitOK: true}
		rec.wire(tool.deps, cfg)
		res := tool.Execute(context.Background(), map[string]any{
			"id": "whatsapp", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
		require.NotNil(t, res)
		require.Equal(t, []string{"admit saved=false", "commit attempt-1 saved=true", "settle attempt-1"}, rec.calls,
			"the attempt is reserved before anything is saved, made startable after the save, and settled once the confirmed reload is done")
		require.Empty(t, rec.restarted, "the committed attempt arms the restart; no second start is requested")
		require.Contains(t, strings.ToLower(res.ForLLM), "pairing not finished", res.ForLLM)
	})
	t.Run("other channel: restart requested through the reload", func(t *testing.T) {
		tool, cfg := connectRevisionFixture(t, func(*config.Config) {})
		rec := &pairingDepsRecorder{commitOK: true}
		rec.wire(tool.deps, cfg)
		res := tool.Execute(context.Background(), map[string]any{
			"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
		require.NotNil(t, res)
		require.Equal(t, []string{"telegram"}, rec.restarted, "connect_channel must ask for the stopped channel's restart")
		require.Empty(t, rec.calls, "a non-QR channel takes no pairing attempt")
	})
}

// TestConnectChannel_LosingConnectIsRefusedAndSavesNothing is gate r3
// T2-SF-12 (M5): while another pairing attempt of the connection is in
// flight, a second connect is refused before anything is saved — it never
// takes over the winner's attempt and never starts the channel.
func TestConnectChannel_LosingConnectIsRefusedAndSavesNothing(t *testing.T) {
	tool, cfg := connectRevisionFixture(t, func(*config.Config) {})
	rec := &pairingDepsRecorder{admitErr: channels.ErrPairingConflict}
	rec.wire(tool.deps, cfg)
	res := tool.Execute(context.Background(), map[string]any{
		"id": "whatsapp", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
	require.NotNil(t, res)
	require.True(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "PAIRING_IN_PROGRESS")
	require.Contains(t, res.ForLLM, "nothing saved")
	require.Equal(t, []string{"admit saved=false"}, rec.calls, "no commit, no release of someone else's attempt")
	require.Empty(t, rec.restarted, "the losing connect starts nothing")
	_, saved := cfg.Channels["whatsapp"]
	require.False(t, saved, "the refused connect saved no configuration")
}

// TestConnectChannel_RevokedAdmissionIsNotReportedAsStarted is gate r3 M14:
// a Stop that revoked the admission between the save and the commit leaves a
// saved connection whose pairing did not start; the result says so instead
// of reporting a running pairing.
func TestConnectChannel_RevokedAdmissionIsNotReportedAsStarted(t *testing.T) {
	tool, cfg := connectRevisionFixture(t, func(*config.Config) {})
	rec := &pairingDepsRecorder{commitOK: false}
	rec.wire(tool.deps, cfg)
	res := tool.Execute(context.Background(), map[string]any{
		"id": "whatsapp", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
	require.NotNil(t, res)
	out := strings.ToLower(res.ForLLM)
	require.Contains(t, out, "saved but not working", res.ForLLM)
	require.Contains(t, out, "stopped before it started", res.ForLLM)
	require.NotContains(t, out, "pairing not finished")
	require.Equal(t, []string{"admit saved=false", "commit attempt-1 saved=true"}, rec.calls[:2])
	require.Empty(t, rec.restarted)
}
