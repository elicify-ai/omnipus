package gateway

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"encoding/json"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Successor to the retired client-success/rollback audit assertions: the
// SERVER claims success only after publishing its real non-human wake.
func TestL1WU10ServerSetupOffer_AuditClaimsOnlySuccessfulPublication(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := map[bool]string{false: "published", true: "publish_failed"}[closed]
		t.Run(name, func(t *testing.T) {
			h := newL1WU10SetupHarness(t)
			dir := t.TempDir()
			logger, loggerErr := audit.NewLogger(audit.LoggerConfig{Dir: dir, AuditLogRequested: true})
			require.NoError(t, loggerErr)
			t.Cleanup(func() { require.NoError(t, logger.Close()) })
			h.api.auditor = logger
			require.NoError(t, logger.Log(&audit.Entry{Event: "workspace.create", Decision: audit.DecisionAllow,
				Details: map[string]any{"id": "wu10-audit-instrument"}}))
			if closed {
				h.bus.Close()
			}
			created := h.post(t, gen.WorkspaceCreateRequest{Name: "Audit setup offer"})
			h.awaitHandler(t)
			h.requireProducer(t)
			if !closed {
				h.awaitOffer(t, created.Id)
			}
			entries := l1WU10ReadAuditEntries(t, dir)
			instrument, consumed := 0, 0
			for _, entry := range entries {
				if entry.Event == "workspace.create" && entry.Details["id"] == "wu10-audit-instrument" {
					instrument++
				}
				if entry.Event == "workspace.setup_consumed" {
					consumed++
					assert.Equal(t, "omnipus", entry.AgentID)
					assert.Equal(t, h.mainID, entry.SessionID)
					assert.Equal(t, "admin", entry.User, "the real REST wrapper carries the authenticated caller")
				}
			}
			assert.Equal(t, 1, instrument, "positive audit-file carrier prevents a false empty-record verdict")
			want := 1
			if closed {
				want = 0
			}
			assert.Equal(t, want, consumed, "a failed publish cannot produce a successful-consume audit record")
		})
	}
}

func l1WU10ReadAuditEntries(t *testing.T, dir string) []audit.Entry {
	t.Helper()
	files, listErr := os.ReadDir(dir)
	require.NoError(t, listErr)
	var entries []audit.Entry
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
			continue
		}
		input, openErr := os.Open(filepath.Join(dir, file.Name()))
		require.NoError(t, openErr)
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			var entry audit.Entry
			require.NoError(t, json.Unmarshal(scanner.Bytes(), &entry), "a corrupt audit line is not a missing-success control")
			entries = append(entries, entry)
		}
		require.NoError(t, scanner.Err())
		require.NoError(t, input.Close())
	}
	return entries
}
