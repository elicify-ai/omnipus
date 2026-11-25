package gateway

// FR-021 and ruling H50 (/workspace/logs/1960/spec-s1/ruling-H50.md), with the reason
// classes ratified in ruling-check-delta.md item 5 (2026-11-25) and the FR-021 note of
// the spec: one program.check_result audit row for each check that finished and
// published a result, carrying the program, the generation and check_revision of that
// check, one outcome (connected, failed, timed_out) and one value-free reason class of
// that outcome. A superseded or replaced check writes one program.check_superseded row
// and no check_result row.
//
// The independent CHECK of the S1 delta (check-s1-delta/check-report.md, F-1, F-3 and
// F-4) found that no row drove a failed or a timed-out check to the real audit file,
// that the reason class was never read, and that a superseded check could write a
// result row without any test noticing. Every row here runs the real person route, the
// real service and its check child, and reads the real audit file. The one stand-in is
// the program the check runs: a script, as in S-01 to S-06.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgEventSuperseded is the event ruling H50 names for a check whose reservation was
// replaced before it finished.
const pgEventSuperseded = "program.check_superseded"

// pgResultReasonClasses lists the reason classes ratified for a program.check_result
// row, by outcome (spec FR-021 note, ruling-check-delta.md item 5). "replaced" is in no
// set: a replaced check writes a program.check_superseded row and no result row.
var pgResultReasonClasses = map[string][]string{
	"connected": {"account_named"},
	"timed_out": {"timeout"},
	"failed": {
		"store_unreadable", "program_not_found", "program_location_refused", "not_started",
		"run_error", "exit_status", "label_carries_token", "no_account_name",
	},
}

// pgReasonMaxLen is the "short" the CHECK asks of a reason class: a category, never
// prose or a program's own words. 32 bytes holds every ratified class (the longest,
// program_location_refused, has 24) and cannot hold a sentence.
const pgReasonMaxLen = 32

// pgResultWant is what one program.check_result row says about the check that finished.
type pgResultWant struct {
	generation, revision int64
	outcome, reason      string
}

// pgAuditRow is one real audit line of an event, with its details object.
type pgAuditRow struct {
	details map[string]any
	line    string
}

// pgAuditRowsOf returns the rows of one event in the real audit file, in file order.
func pgAuditRowsOf(t *testing.T, home, event string) []pgAuditRow {
	t.Helper()
	var rows []pgAuditRow
	for _, raw := range s50ReadAudit(t, home) {
		fields := s50Fields(t, raw)
		if fields["event"] != event {
			continue
		}
		details, ok := fields["details"].(map[string]any)
		require.True(t, ok, "the %s row carries a details object: %s", event, raw)
		rows = append(rows, pgAuditRow{details: details, line: string(raw)})
	}
	return rows
}

// pgNumberOf reads an audit field as the whole number it must be.
func pgNumberOf(t *testing.T, row pgAuditRow, key string) int64 {
	t.Helper()
	n, ok := row.details[key].(json.Number)
	require.True(t, ok, "the audit row carries the number %q: %s", key, row.line)
	whole, err := n.Int64()
	require.NoError(t, err, "the audit field %q is a whole number: %s", key, row.line)
	return whole
}

// pgTokenPieceOf returns the first 8-byte piece of token that text holds. Any longer
// fragment of the token holds an 8-byte piece, so 8 bytes is the whole check (H50
// ruling, item 4).
func pgTokenPieceOf(token, text string) (string, bool) {
	for start := 0; start+8 <= len(token); start++ {
		if piece := token[start : start+8]; strings.Contains(text, piece) {
			return piece, true
		}
	}
	return "", false
}

// pgAssertReasonClass requires what holds for the reason of every result row (CHECK
// F-3): it is present, not empty, short, and one of the ratified classes of the row's
// outcome. context names the row in a failure.
func pgAssertReasonClass(t *testing.T, outcome, reason any, context string) {
	t.Helper()
	text, isText := reason.(string)
	if !assert.True(t, isText, "ruling-check-delta item 5: the result row carries a reason class: %s", context) {
		return
	}
	assert.NotEmpty(t, text, "ruling-check-delta item 5: the reason class is not empty: %s", context)
	assert.LessOrEqual(t, len(text), pgReasonMaxLen, "ruling-check-delta item 5: the reason is a short class, not prose: %s", context)
	name, _ := outcome.(string)
	classes, known := pgResultReasonClasses[name]
	if !assert.True(t, known, "ruling H50: the outcome is connected, failed or timed_out: %s", context) {
		return
	}
	assert.Contains(t, classes, text, "ruling-check-delta item 5: the reason is one of the classes ratified for the outcome %q: %s", name, context)
}

// pgResultOfRow reads one program.check_result row and asserts what holds for every
// such row whatever the check was: it names the program and carries a ratified reason
// class. The values it returns are compared with the check's own by the caller.
func pgResultOfRow(t *testing.T, row pgAuditRow, programID string) pgResultWant {
	t.Helper()
	assert.Equal(t, programID, row.details["program_id"], "FR-021: the result row names the program: %s", row.line)
	pgAssertReasonClass(t, row.details["outcome"], row.details["reason"], row.line)
	outcome, _ := row.details["outcome"].(string)
	reason, _ := row.details["reason"].(string)
	return pgResultWant{
		generation: pgNumberOf(t, row, "generation"), revision: pgNumberOf(t, row, "check_revision"),
		outcome: outcome, reason: reason,
	}
}

// pgAssertResultRows reads the real audit file, after every check has joined, and
// requires its program.check_result rows to be exactly want (one per finished check,
// in any order) and the file to hold no token and no 8-byte piece of one.
func pgAssertResultRows(t *testing.T, f *pgGateFixture, programID string, want []pgResultWant, tokens ...string) {
	t.Helper()
	rows := pgAuditRowsOf(t, f.api.homePath, s50EventResult)
	got := make([]pgResultWant, 0, len(rows))
	for _, row := range rows {
		got = append(got, pgResultOfRow(t, row, programID))
	}
	assert.ElementsMatch(t, want, got, "FR-021/H50: exactly one %s row for each finished check, with the generation and check_revision of that check and its outcome and reason class", s50EventResult)
	for _, line := range s50ReadAudit(t, f.api.homePath) {
		for _, token := range tokens {
			if piece, found := pgTokenPieceOf(token, string(line)); found {
				assert.Failf(t, "an audit row holds part of a token", "FR-021: an audit row holds the 8-byte piece %q of a token: %s", piece, line)
			}
		}
	}
}

// pgAssertFinishedChecks joins the checks in flight and then asserts the result rows.
func pgAssertFinishedChecks(t *testing.T, f *pgGateFixture, programID string, want []pgResultWant, tokens ...string) {
	t.Helper()
	f.api.programService().Wait()
	pgAssertResultRows(t, f, programID, want, tokens...)
}

// pgAssertOneFinishedCheck is pgAssertFinishedChecks for a program whose first check,
// at generation 1 (S-01: the first save of a fresh store), is the only one that
// finished. The check_revision is the one the service holds for it.
func pgAssertOneFinishedCheck(t *testing.T, f *pgGateFixture, programID, outcome, reason string, tokens ...string) {
	t.Helper()
	f.api.programService().Wait()
	revision := f.api.programService().Connection(programID).CheckRevision
	pgAssertResultRows(t, f, programID, []pgResultWant{{generation: 1, revision: revision, outcome: outcome, reason: reason}}, tokens...)
}

// pgAuditedGateway is the person-account gateway with the real audit logger wired. It
// proves the reader sees a real logger write before any absent row is read as absent.
func pgAuditedGateway(t *testing.T) *pgGateFixture {
	t.Helper()
	f := pgPersonAPI(t)
	wirePackageAuditLogger(t, f.api)
	require.NoError(t, f.api.auditor.Log(&audit.Entry{Timestamp: time.Now().UTC(), Event: "s1-test-instrument", Decision: "allow"}))
	control := s50ReadAudit(t, f.api.homePath)
	require.Len(t, control, 1, "instrument: the reader sees exactly the one row the real logger wrote")
	require.Contains(t, string(control[0]), "s1-test-instrument")
	return f
}

// pgAcmeEntryLimited creates the custom Acme entry of the S116 pack (program word gh,
// identity check "gh whoami old") with the given check limit in seconds and returns
// its id. The spec allows 1 to 30 seconds (ProgramIdentityCheck).
func pgAcmeEntryLimited(t *testing.T, f *pgGateFixture, seconds int) string {
	t.Helper()
	created := pgCreateProgramEntry(t, f, fmt.Sprintf(`{"display_name":"Acme","program_words":["gh"],"token_variables":["ACME_TOKEN"],"network_sites":["api.acme.example"],"identity_check":{"program_word":"gh","args":["whoami","old"],"timeout_seconds":%d}}`, seconds))
	id, ok := created["id"].(string)
	require.True(t, ok, "the created entry carries its id: %v", created)
	return id
}

func pgSaveAcmeToken(t *testing.T, f *pgGateFixture, id string) {
	t.Helper()
	save := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/"+id+"/credential", `{"value":"acme_tok_123456"}`)
	require.Equal(t, http.StatusOK, save.Code, "the save is processed: %s", save.Body.String())
}

func TestProgramAudit_CheckResultRows(t *testing.T) {
	// One finished check of each outcome the ruling names, through the real route and
	// the real check child, each on its own fixture. The exact (outcome, reason) pair
	// of each is the architect's addendum of 2026-11-25.
	t.Run("R1_connected_account_named", func(t *testing.T) {
		f := pgAuditedGateway(t)
		pgConnectGithub(t, f) // the spec's octocat fake; saves tok_valid_12345 and waits for connected
		pgAssertOneFinishedCheck(t, f, "github", "connected", "account_named", "tok_valid_12345")
	})

	t.Run("R2_failed_exit_status", func(t *testing.T) {
		f := pgAuditedGateway(t)
		// S-04: the program rejects the token and exits with a non-zero status.
		shBody, cmdBody := pgFakeGhAccounts(map[string]string{"tok_valid_12345": "octocat"})
		pgInstallFakeGh(t, f, shBody, cmdBody)
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_wrong_999999"}`)
		require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
		pgPollEntryState(t, f, "github", "check_failed", 15*time.Second)
		pgAssertOneFinishedCheck(t, f, "github", "failed", "exit_status", "tok_wrong_999999")
	})

	t.Run("R3_failed_program_not_found", func(t *testing.T) {
		f := pgAuditedGateway(t)
		t.Setenv("PATH", t.TempDir()) // S-03: no gh exists on the gateway PATH
		w := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_valid_12345"}`)
		require.Equal(t, http.StatusOK, w.Code, "the save is processed: %s", w.Body.String())
		pgPollEntryState(t, f, "github", "saved_not_checked", 15*time.Second)
		pgAssertOneFinishedCheck(t, f, "github", "failed", "program_not_found", "tok_valid_12345")
	})

	t.Run("R4_timed_out_timeout", func(t *testing.T) {
		f := pgAuditedGateway(t)
		// S-06 at the lower end of the spec's 1 to 30 second range, so the row does not
		// wait 20 s: a custom entry whose check limit is 1 s, run against a program
		// that outlives it.
		shBody, cmdBody := pgFakeGhSleep(filepath.Join(t.TempDir(), "gh-pid"))
		pgInstallFakeGh(t, f, shBody, cmdBody)
		id := pgAcmeEntryLimited(t, f, 1)
		pgSaveAcmeToken(t, f, id)
		pgPollEntryState(t, f, id, "check_failed", 15*time.Second)
		pgAssertOneFinishedCheck(t, f, id, "timed_out", "timeout", "acme_tok_123456")
	})

	t.Run("R5_check_revision_is_the_one_of_the_check_that_finished", func(t *testing.T) {
		f := pgAuditedGateway(t)
		// A check-relevant edit advances check_revision by exactly one and starts a new
		// check (S-116), so the two finished checks carry different revisions: the
		// first connects, the second runs the edited arguments and is rejected.
		pgInstallFakeGh(t, f,
			"case \"$*\" in\n  \"whoami old\") echo acme-user;;\n  *) echo denied >&2; exit 3;;\nesac",
			"@echo off\r\nif \"%~1 %~2\"==\"whoami old\" (\r\necho acme-user\r\nexit /b 0\r\n)\r\necho denied 1>&2\r\nexit /b 3\r\n")
		id := pgAcmeEntryLimited(t, f, 20)
		pgSaveAcmeToken(t, f, id)
		pgPollEntryState(t, f, id, "connected", 15*time.Second)
		f.api.programService().Wait()
		first := f.api.programService().Connection(id)
		require.Equal(t, int64(1), first.Generation, "instrument: the first save is generation 1")
		pgEditCheckEntry(t, f, id, map[string]any{"identity_check": map[string]any{"program_word": "gh", "args": []string{"whoami", "new"}, "timeout_seconds": 20}})
		pgPollEntryState(t, f, id, "check_failed", 15*time.Second)
		pgAssertFinishedChecks(t, f, id, []pgResultWant{
			{generation: 1, revision: first.CheckRevision, outcome: "connected", reason: "account_named"},
			{generation: 1, revision: first.CheckRevision + 1, outcome: "failed", reason: "exit_status"},
		}, "acme_tok_123456")
	})
}
