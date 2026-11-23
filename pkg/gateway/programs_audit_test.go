package gateway

// S50/FR-021: this row moved from the programs package so it can drive all
// five actual person routes and inspect their shared audit sink. No logger mock.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramSentinel_AllSinks(t *testing.T) {
	t.Run("S50_audit_rows_value_free", func(t *testing.T) {
		f := pgBoundChannel(t)
		wirePackageAuditLogger(t, f.api)
		read := func() []json.RawMessage {
			t.Helper()
			raw, err := os.ReadFile(filepath.Join(f.api.homePath, "system", "audit.jsonl"))
			require.NoError(t, err, "missing audit output is not an empty successful log")
			lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
			rows := make([]json.RawMessage, 0, len(lines))
			for _, line := range lines {
				require.True(t, json.Valid(line), "each real audit line must decode")
				rows = append(rows, append(json.RawMessage(nil), line...))
			}
			return rows
		}
		// Test the instrument before interpreting an absent product event.
		require.NoError(t, f.api.auditor.Log(&audit.Entry{Timestamp: time.Now().UTC(), Event: "s1-test-instrument", Decision: "allow"}))
		control := read()
		require.Len(t, control, 1)
		require.Contains(t, string(control[0]), "s1-test-instrument")
		baseline := 1
		check := func(action string) {
			t.Helper()
			rows := read()
			assert.Len(t, rows, baseline+1, "S50: exactly one new audit row for %s", action)
			baseline = len(rows)
			for _, row := range rows {
				assert.NotContains(t, string(row), "tok_valid_12345", "audit remains value-free after %s", action)
			}
		}
		pgConnectGithub(t, f)
		check("connect")
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs/github/check", "")
		require.Equal(t, http.StatusAccepted, w.Code, "%s", w.Body.String())
		f.api.programService().Wait()
		check("check")
		w = pgSwitchProgramOn(t, f, "github")
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		check("switch")
		pgEnableChannelPrograms(t, f)
		check("setting")
		w = pgProgramsSessionCall(t, f, http.MethodDelete, "/api/v1/programs/github/credential", "")
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		check("disconnect")
	})
}
