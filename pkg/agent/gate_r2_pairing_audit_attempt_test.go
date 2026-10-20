package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// TestPairingAudit_RowNamesTheAttemptsOwnRequest is Train 2 gate r2
// T2-R2-S1 (audit attribution): chat A's attempt ends without a terminal
// event (the channel was disabled); a later attempt started without a chat
// claim (Connectors) times out. Its audit row must not name A's request —
// only the attempt's own owner, here none.
func TestPairingAudit_RowNamesTheAttemptsOwnRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(auditBootTestConfig(), msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	al.NotePairingRequest("whatsapp", "req-a", "sess-a")
	al.EmitWhatsAppPairing("whatsapp", channels.PairingStatusWaiting, "", "")
	al.EmitWhatsAppPairing("whatsapp", channels.PairingStatusWaiting, "", "") // Connectors restart, no claim
	al.EmitWhatsAppPairing("whatsapp", channels.PairingStatusTimeout, "", "expired")
	al.NotePairingRequest("whatsapp-2", "req-b", "sess-b")
	al.EmitWhatsAppPairing("whatsapp-2", channels.PairingStatusWaiting, "", "")
	al.EmitWhatsAppPairing("whatsapp-2", channels.PairingStatusTimeout, "", "expired")

	raw, err := os.ReadFile(filepath.Join(home, "system", "audit.jsonl"))
	require.NoError(t, err)
	rows := map[string]map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		var entry map[string]any
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry["event"] != "channel.pairing" {
			continue
		}
		details, _ := entry["details"].(map[string]any)
		channel, _ := details["channel"].(string)
		rows[channel] = details
	}
	require.Contains(t, rows, "whatsapp-2", "control: an owned attempt's timeout is audited")
	require.Equal(t, "req-b", rows["whatsapp-2"]["request_id"], "control: an owned attempt names its own request")
	require.Contains(t, rows, "whatsapp")
	require.NotContains(t, rows["whatsapp"], "request_id", "an unowned attempt's row must not name an earlier chat's request")
}
