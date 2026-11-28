package gateway

// S50/FR-021: this row moved from the programs package so it can drive all
// five actual person routes and inspect their shared audit sink. No logger mock.
//
// H50 ruling (architect, 2026-11-24): S-50's "one audit row per action" counts
// action rows by their own event name. The outcome of a finished check is a
// separate event, one row per finished check, asserted on its own. Both counts
// stay exact, so the total is the action rows plus the check-result rows.
import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// s50Token is the token S-50 connects with (pgConnectGithub saves it). No audit
// row may hold it or any 8-byte piece of it.
const s50Token = "tok_valid_12345"

// The event names the H50 ruling fixes for S-50: one action row per person
// action under its own name, and one program.check_result row per finished check.
const (
	s50EventConnect    = "program.connected"
	s50EventRecheck    = "program.check"
	s50EventSwitch     = "program.agent_switch_changed"
	s50EventSetting    = "program.setting_changed"
	s50EventDisconnect = "program.disconnected"
	s50EventResult     = "program.check_result"
)

// s50ReadAudit returns the lines of the real audit file. A missing file is a
// failure, never an empty successful log.
func s50ReadAudit(t *testing.T, home string) []json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "system", "audit.jsonl"))
	require.NoError(t, err, "missing audit output is not an empty successful log")
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	rows := make([]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		require.True(t, json.Valid(line), "each real audit line must decode")
		rows = append(rows, append(json.RawMessage(nil), line...))
	}
	return rows
}

// s50Fields decodes one audit line into its top-level fields, keeping numbers exact.
func s50Fields(t *testing.T, row json.RawMessage) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(row))
	decoder.UseNumber()
	var fields map[string]any
	require.NoError(t, decoder.Decode(&fields), "each real audit line is one JSON object")
	return fields
}

func s50Number(n int64) json.Number { return json.Number(strconv.FormatInt(n, 10)) }

// s50TokenPiece returns the first 8-byte piece of the token that text holds (H50
// ruling, item 4; the piece search is shared with the check-result rows).
func s50TokenPiece(text string) (string, bool) { return pgTokenPieceOf(s50Token, text) }

func TestProgramSentinel_AllSinks(t *testing.T) {
	t.Run("S50_audit_rows_value_free", func(t *testing.T) {
		f := pgBoundChannel(t)
		wirePackageAuditLogger(t, f.api)
		read := func() []json.RawMessage { return s50ReadAudit(t, f.api.homePath) }
		// Test the instrument before interpreting an absent product event.
		require.NoError(t, f.api.auditor.Log(&audit.Entry{Timestamp: time.Now().UTC(), Event: "s1-test-instrument", Decision: "allow"}))
		control := read()
		require.Len(t, control, 1)
		require.Contains(t, string(control[0]), "s1-test-instrument")
		// Instrument for the token check below: pieces of the token are found at
		// both ends and in the middle, while a 7-byte piece and an unrelated row are not.
		for _, leaky := range []string{s50Token, `{"details":{"a":"tok_vali"}}`, `{"note":"x_valid_1y"}`, `{"note":"id_12345"}`} {
			_, found := s50TokenPiece(leaky)
			require.True(t, found, "instrument: %s holds a piece of the token", leaky)
		}
		for _, clean := range []string{"", `{"event":"program.connected","details":{"program_id":"github","generation":1}}`, `{"note":"tok_val"}`, `{"note":"valid_1"}`, `{"note":"d_12345"}`} {
			_, found := s50TokenPiece(clean)
			require.False(t, found, "instrument: %s holds no 8-byte piece of the token", clean)
		}
		baseline := 1 // the instrument row
		resultRowsSeen := 0
		// step checks the rows written since the previous step against want: one
		// action row per action under its own event name, one check-result row per
		// finished check, and no other row. The caller has joined the checks with
		// Wait, so the result rows are written already; the bounded poll only
		// covers a late write, and a row still missing at its end is a missing
		// event, not a reason to wait longer.
		step := func(action string, want ...string) []map[string]any {
			t.Helper()
			rows := read()
			for deadline := time.Now().Add(3 * time.Second); len(rows) < baseline+len(want) && time.Now().Before(deadline); {
				time.Sleep(25 * time.Millisecond)
				rows = read()
			}
			require.GreaterOrEqual(t, len(rows), baseline, "the audit log only grows")
			fresh := make([]map[string]any, 0, len(rows)-baseline)
			events := make([]string, 0, len(rows)-baseline)
			for _, row := range rows[baseline:] {
				fields := s50Fields(t, row)
				event, _ := fields["event"].(string)
				if event == s50EventResult {
					resultRowsSeen++
				}
				fresh = append(fresh, fields)
				events = append(events, event)
			}
			assert.ElementsMatch(t, want, events, "S50/FR-021 after %s: the new rows must be exactly one action row per action under its own event name plus one %s row per finished check, and nothing else", action, s50EventResult)
			baseline = len(rows)
			return fresh
		}
		// checkResults checks each check-result row of a joined check: the program,
		// the generation and check_revision of the check that finished, and the
		// outcome the person sees. The ruling names these fields but not their place
		// in the row; they are read from details, where program.check keeps its
		// program_id, generation and check_revision and program.check_superseded
		// keeps its program_id, generation and outcome.
		checkResults := func(fresh []map[string]any, wantGeneration, wantRevision int64) {
			t.Helper()
			for _, fields := range fresh {
				if fields["event"] != s50EventResult {
					continue
				}
				details, _ := fields["details"].(map[string]any)
				assert.Equal(t, "github", details["program_id"], "S50/FR-021: the result row names the program")
				assert.Equal(t, s50Number(wantGeneration), details["generation"], "S50/FR-021: the result row carries the generation of the check that finished")
				assert.Equal(t, s50Number(wantRevision), details["check_revision"], "S50/FR-021: the result row carries the check_revision of the check that finished")
				assert.Equal(t, "connected", details["outcome"], "S50/FR-021: the result row carries the outcome the person sees")
				// CHECK F-3, ruling-check-delta item 5: a connected check's row carries
				// its reason class: exactly account_named, and, like every result row,
				// present, not empty, short and a ratified class of its outcome.
				assert.Equal(t, "account_named", details["reason"], "S50/FR-021: the result row of a connected check carries the reason class account_named")
				shown, _ := json.Marshal(details)
				pgAssertReasonClass(t, details["outcome"], details["reason"], string(shown))
			}
		}
		// joined waits for the checks in flight, confirms the person sees the
		// finished check connected (the outcome its row must describe) and returns
		// the generation and check_revision the service holds for that check.
		joined := func() (generation, revision int64) {
			t.Helper()
			f.api.programService().Wait()
			require.Equal(t, "connected", pgConnection(t, pgProgramEntryByID(t, f, "github"))["state"], "S-50 connects with a valid token, so the finished check is connected")
			conn := f.api.programService().Connection("github")
			return conn.Generation, conn.CheckRevision
		}
		pgConnectGithub(t, f)
		generation, revision := joined()
		checkResults(step("connect", s50EventConnect, s50EventResult), generation, revision)
		w := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs/github/check", "")
		require.Equal(t, http.StatusAccepted, w.Code, "%s", w.Body.String())
		generation, revision = joined()
		checkResults(step("check", s50EventRecheck, s50EventResult), generation, revision)
		w = pgSwitchProgramOn(t, f, "github")
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		step("switch", s50EventSwitch)
		pgEnableChannelPrograms(t, f)
		step("setting", s50EventSetting)
		w = pgProgramsSessionCall(t, f, http.MethodDelete, "/api/v1/programs/github/credential", "")
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		step("disconnect", s50EventDisconnect)
		// Total: the instrument row, one action row for each of connect, check,
		// switch, setting and disconnect, and one check-result row for each of the
		// two finished checks (the one connect starts and the re-check).
		final := read()
		assert.Len(t, final, 1+5+2, "S50/FR-021: total rows = the instrument row + five action rows + two check-result rows")
		resultRows := 0
		for _, row := range final {
			fields := s50Fields(t, row)
			if fields["event"] == s50EventResult {
				resultRows++
			}
			if piece, found := s50TokenPiece(string(row)); found {
				assert.Failf(t, "an audit row holds part of the token", "S50/FR-021: the %v row holds the 8-byte piece %q of the token: %s", fields["event"], piece, row)
			}
		}
		assert.Equal(t, 2, resultRows, "S50/FR-021: exactly one %s row per finished check; S-50 joins two (the one connect starts and the re-check)", s50EventResult)
		if resultRowsSeen == 0 {
			t.Fatalf("BLOCKED: no %s audit row was written for either finished check; FR-021 and S-50 (ruling H50) require one row for each finished check", s50EventResult)
		}
	})
}
