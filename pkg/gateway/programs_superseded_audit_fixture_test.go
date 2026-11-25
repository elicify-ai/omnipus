package gateway

// Process-edge barriers for rev8 S90/S114. Actual routes, reservations, check
// subprocesses, credential/state stores and audit logger remain production code.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pgSupersededBarrier struct{ hold, release, ready, pid, fail string }

func pgInstallSupersededBarrier(t *testing.T, f *pgGateFixture, label string) pgSupersededBarrier {
	t.Helper()
	dir := t.TempDir()
	h := pgSupersededBarrier{filepath.Join(dir, "hold"), filepath.Join(dir, "release"), filepath.Join(dir, "after-print"), filepath.Join(dir, "pid"), filepath.Join(dir, "fail")}
	sh := fmt.Sprintf(`case "$GH_TOKEN" in
 tok_new_654321) printf 'account-b\n'; exit 0;;
 tok_valid_12345) ;;
 *) exit 91;;
esac
if [ -f %q ]; then printf 'HTTP 401: Bad credentials\n' >&2; exit 1; fi
printf '%%s\n' %q
if [ -f %q ]; then
 printf '%%s\n' "$$" > %q
 printf 'after-print' > %q
 while [ ! -f %q ]; do sleep 0.01; done
fi
`, h.fail, label, h.hold, h.pid, h.ready, h.release)
	bat := fmt.Sprintf(`@echo off
if "%%GH_TOKEN%%"=="tok_new_654321" (echo account-b
exit /b 0
)
if not "%%GH_TOKEN%%"=="tok_valid_12345" exit /b 91
if exist "%s" (echo HTTP 401: Bad credentials 1>&2
exit /b 1
)
echo %s
if not exist "%s" exit /b 0
echo after-print>"%s"
:hold
if exist "%s" exit /b 0
ping -n 2 127.0.0.1 >nul
goto hold
`, h.fail, label, h.hold, h.ready, h.release)
	pgInstallFakeGh(t, f, sh, bat)
	t.Cleanup(func() {
		assert.NoError(t, os.WriteFile(h.release, []byte("release"), 0o600))
		f.api.programService().Wait()
	})
	return h
}

func (h pgSupersededBarrier) waitAfterPrint(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(h.ready)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("instrument: after-print marker unreadable: %v", err)
		}
		return err == nil && strings.TrimSpace(string(raw)) == "after-print"
	}, 5*time.Second, 10*time.Millisecond, "actual A must print its account before the held barrier")
}

func pgSupersededAuditRows(t *testing.T, f *pgGateFixture) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.api.homePath, "system", "audit.jsonl"))
	require.NoError(t, err, "unreadable audit file is an instrument failure, not an empty log")
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var row map[string]any
		require.NoError(t, json.Unmarshal(line, &row), "every actual audit line must decode")
		for _, token := range []string{"tok_valid_12345", "tok_new_654321"} {
			assert.False(t, bytes.Contains(line, []byte(token)), "FR-021: actual audit rows contain no token value")
		}
		rows = append(rows, row)
	}
	return rows
}

func pgSupersededAuditInstrument(t *testing.T, f *pgGateFixture) {
	t.Helper()
	wirePackageAuditLogger(t, f.api)
	// A distinct instrument entry, never the product's required outcome. This
	// proves the file/reader can see a real shared-logger write before absence.
	require.NoError(t, f.api.auditor.Log(&audit.Entry{Event: "s1-audit-reader-instrument", Decision: "allow", Details: map[string]any{"instrument_outcome": "superseded"}}))
	rows := pgSupersededAuditRows(t, f)
	require.Len(t, rows, 1)
	require.Equal(t, "s1-audit-reader-instrument", rows[0]["event"])
	require.True(t, pgAuditHasSupersededOutcome(rows[0]), "instrument: the reader/matcher can see the exact outcome in a real logger write")
	require.False(t, pgAuditHasSupersededOutcome(map[string]any{"superseded": false, "message": "failed to audit superseded check"}), "instrument: a key or prose mention cannot satisfy a product outcome")
}

// The spec names the outcome, not audit JSON keys/event spelling. Accept the
// exact outcome scalar or its event-name suffix, not prose mentioning it or a
// boolean field named superseded. Only A can be superseded in these fixtures.
func pgAuditHasSupersededOutcome(value any) bool {
	switch v := value.(type) {
	case string:
		return v == "superseded" || strings.HasSuffix(v, ".superseded") || strings.HasSuffix(v, "_superseded")
	case map[string]any:
		for _, child := range v {
			if pgAuditHasSupersededOutcome(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if pgAuditHasSupersededOutcome(child) {
				return true
			}
		}
	}
	return false
}

// pgSupersededWant is what the real audit file must say about the finished and the
// superseded checks once every check of a superseded-check scenario has joined
// (FR-021, ruling H50, ruling-check-delta item 5).
type pgSupersededWant struct {
	// results is every program.check_result row, once each: one per check that
	// published a result. A superseded check is not in it.
	results []pgResultWant
	// superseded is the generation of every program.check_superseded row, once each:
	// one per superseded check.
	superseded []int64
	// unreported lists generations that must have no result row at all, because their
	// only check was superseded (S90, S139: A's generation).
	unreported []int64
}

// pgAssertSupersededAudit asserts the audit file after every check has joined: the
// superseded check is audited superseded exactly once, the exact multiset of
// program.check_result and program.check_superseded rows by generation and
// check_revision, and no result row for a generation whose check was superseded.
func pgAssertSupersededAudit(t *testing.T, f *pgGateFixture, want pgSupersededWant) {
	t.Helper()
	rows := pgSupersededAuditRows(t, f)
	count := 0
	product := 0
	for _, row := range rows {
		if row["event"] == "s1-audit-reader-instrument" {
			continue
		}
		product++
		if pgAuditHasSupersededOutcome(row) {
			count++
		}
	}
	assert.Equal(t, 1, count, "S90/S114/FR-021: actual A must be audited superseded exactly once, not merely discarded")
	t.Logf("actual person routes/check children joined; product_audit_rows=%d superseded_outcomes=%d", product, count)

	results := pgAuditRowsOf(t, f.api.homePath, s50EventResult)
	got := make([]pgResultWant, 0, len(results))
	for _, row := range results {
		result := pgResultOfRow(t, row, "github")
		for _, generation := range want.unreported {
			assert.NotEqual(t, generation, result.generation, "ruling H50: the check of generation %d was superseded and must write no %s row: %s", generation, s50EventResult, row.line)
		}
		got = append(got, result)
	}
	assert.ElementsMatch(t, want.results, got, "FR-021/H50: the %s rows are exactly one per check that published a result, and none for a superseded check", s50EventResult)

	superseded := pgAuditRowsOf(t, f.api.homePath, pgEventSuperseded)
	generations := make([]int64, 0, len(superseded))
	for _, row := range superseded {
		generations = append(generations, pgNumberOf(t, row, "generation"))
	}
	assert.ElementsMatch(t, want.superseded, generations, "FR-021/H50: the %s rows are exactly one per superseded check, by generation", pgEventSuperseded)
}
