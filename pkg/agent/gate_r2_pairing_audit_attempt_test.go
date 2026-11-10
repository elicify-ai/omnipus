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

// TestPairingAudit_RowNamesTheAttemptsOwnRequest is Train 2 gate r2/r3
// (T2-R3-S2, M13): chat A's attempt ends without a terminal event (its channel
// was disabled); a later attempt started without a chat claim (Connectors)
// times out. Its audit row names its own attempt and no chat — never A's
// request. An owned attempt's row names its own request (control).
func TestPairingAudit_RowNamesTheAttemptsOwnRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(auditBootTestConfig(), msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	store := pairingAttemptsFor(t, al)
	noop := func(string) func(context.Context) error { return func(context.Context) error { return nil } }

	a, _ := startOwnedAttempt(t, store, "whatsapp", "sess-a", "req-a")
	// A disable cut A off; Connectors then turned the connector on again: an
	// unowned attempt of its own admission (gate r4: no tokenless start).
	_, interrupted := store.Complete("whatsapp", a, channels.PairingOutcomeInterrupted)
	require.True(t, interrupted)
	connectors := startUnownedAttempt(t, store, "whatsapp", noop)
	al.EmitWhatsAppPairingAttempt("whatsapp", connectors.AttemptID, channels.PairingStatusTimeout, "", "expired")
	owned, _ := startOwnedAttempt(t, store, "whatsapp-2", "sess-b", "req-b")
	al.EmitWhatsAppPairingAttempt("whatsapp-2", owned, channels.PairingStatusTimeout, "", "expired")

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
	require.Equal(t, owned, rows["whatsapp-2"]["attempt_id"])
	require.Contains(t, rows, "whatsapp")
	require.NotContains(t, rows["whatsapp"], "request_id", "an unowned attempt's row must not name an earlier chat's request")
	require.Equal(t, connectors.AttemptID, rows["whatsapp"]["attempt_id"])
}
