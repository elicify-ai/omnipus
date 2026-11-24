package gateway

// S50/FR-021: this row moved from the programs package so it can drive all
// five actual person routes and inspect their shared audit sink. No logger mock.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		// Instrument for the action check below: it tells the five actions apart.
		// A disconnect row must not satisfy "connect", nor a setting row "switch".
		require.True(t, auditEventNamesAction("x.connected", "connect"))
		require.False(t, auditEventNamesAction("x.disconnected", "connect"))
		require.True(t, auditEventNamesAction("x.disconnected", "disconnect"))
		require.False(t, auditEventNamesAction("x.connected", "disconnect"))
		require.True(t, auditEventNamesAction("X_Switch.changed", "switch"))
		require.False(t, auditEventNamesAction("x.setting_changed", "switch"))
		require.False(t, auditEventNamesAction("", "check"))
		baseline := 1
		check := func(action string) {
			t.Helper()
			rows := read()
			assert.Len(t, rows, baseline+1, "S50: exactly one new audit row for %s", action)
			// S-50 also says WHICH action each row records. Expected values come
			// from its five action words, not from the event strings the product
			// happens to write.
			var fresh []string
			if len(rows) > baseline {
				for _, row := range rows[baseline:] {
					var fields map[string]any
					require.NoError(t, json.Unmarshal(row, &fields), "each real audit line is one JSON object")
					event, _ := fields["event"].(string)
					fresh = append(fresh, event)
				}
			}
			named := 0
			for _, event := range fresh {
				if auditEventNamesAction(event, action) {
					named++
				}
			}
			assert.Equal(t, 1, named, "S50: exactly one new audit row must name the action %q; the new rows' events are %q", action, fresh)
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

// auditEventNamesAction reports whether an audit event name records the given
// action. S-50 gives the five action words (connect, check, switch, setting,
// disconnect) but not the event strings, so a row names its action when one word
// of its event name starts with the action word. Matching by word, not by
// substring, keeps "disconnected" from passing as "connect".
func auditEventNamesAction(event, action string) bool {
	words := strings.FieldsFunc(strings.ToLower(event), func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == ':' || r == ' '
	})
	for _, word := range words {
		if strings.HasPrefix(word, action) {
			return true
		}
	}
	return false
}
