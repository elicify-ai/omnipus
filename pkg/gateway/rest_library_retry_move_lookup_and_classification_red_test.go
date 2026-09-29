// Omnipus — FR-VA-032 Retry lookup and classification addendum.
// The private record, not edited .view content, identifies a revoked move.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/require"
)

func TestRetryMove_CorruptUnrelatedRecordDoesNotBlockAValidRetryAndLogsWarn(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	other := filepath.Join(workDir(api, ws), "vault-b")
	dir, err := knowledge.IndexDirFor(api.homePath, other)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, knowledge.ViewMembershipFileName), []byte("{broken"), 0o600))

	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	result := requireRetryResult(t, retryMoveHTTP(t, api, ws, redRetryID), gen.RetryMoveResultOutcomeReEnrolled)
	require.Contains(t, result.ReEnrolledPaths, "vault-a/Renamed.view")
	require.Equal(t, "Renamed.view", loadRecordedViews(t, api.homePath, vault).Bases["Projects.base"]["open"])
	require.Contains(t, logs.String(), "level=WARN", "a corrupt sibling must produce a server warning")
	require.Contains(t, logs.String(), filepath.Join(dir, knowledge.ViewMembershipFileName),
		"the warning must identify the skipped record")
}

func TestRetryMove_CorruptTargetRecordReturnsRetryNotFoundNeverA500(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	dir, err := knowledge.IndexDirFor(api.homePath, vault)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, knowledge.ViewMembershipFileName), []byte("{broken"), 0o600))
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusNotFound,
		redRetryID, gen.RetryMoveErrorCodeRetryNotFound)
	require.FileExists(t, filepath.Join(vault, "Open.view"))
	require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
}

func TestRetryMove_SourcePathAbsentOrUnsafeReturnsPreflightFailed(t *testing.T) {
	for _, scenario := range []string{"absent", "unsafe_symlink"} {
		t.Run(scenario, func(t *testing.T) {
			api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
			source := filepath.Join(vault, "Open.view")
			require.NoError(t, os.Remove(source))
			var outside string
			if scenario == "unsafe_symlink" {
				outside = filepath.Join(t.TempDir(), "outside.view")
				require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
				require.NoError(t, os.Symlink(outside, source))
			}
			requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
				redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
			require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
			if outside != "" {
				info, err := os.Lstat(source)
				require.NoError(t, err)
				require.NotZero(t, info.Mode()&os.ModeSymlink, "the unsafe source must remain a symlink")
				body, err := os.ReadFile(outside)
				require.NoError(t, err)
				require.Equal(t, "outside", string(body), "a failed Retry cannot mutate a symlink target")
			}
			m := loadRecordedViews(t, api.homePath, vault)
			require.Empty(t, m.Bases)
			require.Contains(t, m.PendingMoves, redRetryID)
		})
	}
}

func TestRetryMove_SourceIdentityChangedFailsPreflightWithoutReenrollment(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	changed := "name: other\nkind: table\nsource: Projects.base\nderived_from: Projects.base\n"
	require.NoError(t, os.WriteFile(filepath.Join(vault, "Open.view"), []byte(changed), 0o600))
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
	body, err := os.ReadFile(filepath.Join(vault, "Open.view"))
	require.NoError(t, err)
	require.Equal(t, changed, string(body), "preflight failure must not rewrite the changed source")
	require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
	m := loadRecordedViews(t, api.homePath, vault)
	require.Empty(t, m.Bases)
	require.Contains(t, m.PendingMoves, redRetryID)
}
