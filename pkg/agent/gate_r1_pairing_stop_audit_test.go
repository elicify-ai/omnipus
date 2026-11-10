package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestPairingAudit_StoppedHasItsOwnTerminalRow is Train 2 gate r1 M6
// (security T2-S5; founder: fix now): a chat Stop that ends a pairing writes
// one value-free channel.pairing audit row with status "stopped" and the
// request/session attribution — never the QR. Timeout and linked are the
// existing terminal-row controls.
func TestPairingAudit_StoppedHasItsOwnTerminalRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := auditBootTestConfig()
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	require.NotNil(t, al.AuditLogger(), "instrument: a real audit logger")

	// A chat-owned attempt that showed a QR, then the chat's Stop (gate r3:
	// the Stop ends exactly the selected attempt); timeout and linked rows
	// come from unowned attempts of other connectors.
	store := pairingAttemptsFor(t, al)
	attemptID, _ := startOwnedAttempt(t, store, "whatsapp", "sess-stop", "req-stop")
	al.EmitWhatsAppPairingAttempt("whatsapp", attemptID, channels.PairingStatusCode, "QR-SECRET-PAYLOAD", "")
	al.stopSelectedPairings(store.OwnedBy("sess-stop"))
	for _, other := range []string{"whatsapp-2", "whatsapp-3"} {
		startUnownedAttempt(t, store, other, func(string) func(context.Context) error { return func(context.Context) error { return nil } })
	}
	al.EmitWhatsAppPairing("whatsapp-2", channels.PairingStatusTimeout, "", "expired")
	al.EmitWhatsAppPairing("whatsapp-3", channels.PairingStatusLinked, "", "")

	raw, err := os.ReadFile(filepath.Join(home, "system", "audit.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "QR-SECRET-PAYLOAD", "the QR must never reach the audit log")
	statuses := map[string]map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		var entry map[string]any
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry["event"] != "channel.pairing" {
			continue
		}
		details, _ := entry["details"].(map[string]any)
		status, _ := details["status"].(string)
		statuses[status] = entry
	}
	require.Contains(t, statuses, "timeout", "control: timeout is audited")
	require.Contains(t, statuses, "linked", "control: linked is audited")
	stopped, ok := statuses["stopped"]
	require.True(t, ok, "a stopped pairing must have its own terminal audit row")
	details, isMap := stopped["details"].(map[string]any)
	require.True(t, isMap, "the stopped row carries details")
	require.Equal(t, "whatsapp", details["channel"])
	require.Equal(t, "req-stop", details["request_id"])
	require.Equal(t, "sess-stop", details["session_id"])
	require.Equal(t, attemptID, details["attempt_id"], "the row names the stopped attempt (gate r3 M13)")
}
