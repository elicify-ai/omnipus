// Omnipus — FR-VA-032 / TDD rows 77–78: typed Retry consumes the trusted
// post-revocation receipt; a view file's editable marker cannot grant authority.
// Go execution is UNVERIFIED until the merged discovery helpers compile.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/stretchr/testify/require"
)

const redRetryID = "test-view-retry-001"

// pendingRetryFixture persists the same revoked-member + pending-receipt
// state that a post-revocation rename failure leaves. It does not pretend to
// inject or prove the initial failure; the Retry behavior is the assertion.
func pendingRetryFixture(t *testing.T, started time.Time) (*restAPI, string, string) {
	t.Helper()
	api, ws, vault, _ := twoKBWorkspace(t)
	plantTrackedView(t, api.homePath, vault, "Projects.base", "Open.view")
	stagePendingViewMove(t, api.homePath, vault, redRetryID, "Open.view", "Renamed.view", false,
		pendingViewMember("Projects.base", "Projects.base", "Open.view", "Renamed.view"), started)
	return api, ws, vault
}

func TestRetryMove_NormalRetryAfterPostRevocationFailureRestoresManagement(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	result := requireRetryResult(t, retryMoveHTTP(t, api, ws, redRetryID), gen.RetryMoveResultOutcomeReEnrolled)
	require.Contains(t, result.ReEnrolledPaths, "vault-a/Renamed.view")
	require.FileExists(t, filepath.Join(vault, "Renamed.view"))
	require.NoFileExists(t, filepath.Join(vault, "Open.view"))
	m := loadRecordedViews(t, api.homePath, vault)
	require.Equal(t, "Renamed.view", m.Bases["Projects.base"]["open"])
	require.NotContains(t, m.PendingMoves, redRetryID)
	require.Contains(t, m.CompletedMoves, redRetryID)
}

func TestRetryMove_AlreadyLandedVerifiesAndEnrolls(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	require.NoError(t, os.Rename(filepath.Join(vault, "Open.view"), filepath.Join(vault, "Renamed.view")))
	result := requireRetryResult(t, retryMoveHTTP(t, api, ws, redRetryID), gen.RetryMoveResultOutcomeReEnrolled)
	require.Contains(t, result.ReEnrolledPaths, "vault-a/Renamed.view")
	require.Equal(t, "Renamed.view", loadRecordedViews(t, api.homePath, vault).Bases["Projects.base"]["open"])
}

func TestRetryMove_PreflightFailedOnOccupiedDestination(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	occupied := filepath.Join(vault, "Renamed.view")
	require.NoError(t, os.WriteFile(occupied, []byte("independent operator content"), 0o600))
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
	body, err := os.ReadFile(occupied)
	require.NoError(t, err)
	require.Equal(t, "independent operator content", string(body))
	require.FileExists(t, filepath.Join(vault, "Open.view"))
	m := loadRecordedViews(t, api.homePath, vault)
	require.Empty(t, m.Bases)
	require.Contains(t, m.PendingMoves, redRetryID)
}

func TestRetryMove_PreflightFailedOnSourceVanished(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	require.NoError(t, os.Remove(filepath.Join(vault, "Open.view")))
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryPreflightFailed)
	require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
	m := loadRecordedViews(t, api.homePath, vault)
	require.Empty(t, m.Bases)
	require.Contains(t, m.PendingMoves, redRetryID)
}

func TestRetryMove_ExpiredAfterSevenDaysFromTrustedTimestamp(t *testing.T) {
	// No clock-injection seam exists yet; vary the persisted trusted timestamp,
	// not the process clock. This asserts the seven-day behavior, not clock API.
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC().Add(-7*24*time.Hour-time.Minute))
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusGone,
		redRetryID, gen.RetryMoveErrorCodeRetryExpired)
	require.FileExists(t, filepath.Join(vault, "Open.view"))
	require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
	require.Empty(t, loadRecordedViews(t, api.homePath, vault).Bases)
}

func TestRetryMove_ExpiredCompletedReceiptReturnsRetryExpired(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	requireRetryResult(t, retryMoveHTTP(t, api, ws, redRetryID), gen.RetryMoveResultOutcomeReEnrolled)
	require.NoError(t, knowledge.WithViewMembership(api.homePath, vault, func(m *knowledge.ViewMembership) error {
		receipt := m.CompletedMoves[redRetryID]
		require.NotZero(t, receipt.CompletedAt, "a successful Retry must first leave a completed receipt")
		receipt.CompletedAt = time.Now().UTC().Add(-7*24*time.Hour - time.Minute)
		m.CompletedMoves[redRetryID] = receipt
		return knowledge.SaveViewMembership(m)
	}))
	before := loadRecordedViews(t, api.homePath, vault)
	viewBefore, err := os.ReadFile(filepath.Join(vault, "Renamed.view"))
	require.NoError(t, err)
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusGone,
		redRetryID, gen.RetryMoveErrorCodeRetryExpired)
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vault), "an expired receipt must remain inert")
	viewAfter, err := os.ReadFile(filepath.Join(vault, "Renamed.view"))
	require.NoError(t, err)
	require.Equal(t, viewBefore, viewAfter)
}

func TestRetryMove_RepeatAfterSuccessReturnsAlreadyComplete(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	requireRetryResult(t, retryMoveHTTP(t, api, ws, redRetryID), gen.RetryMoveResultOutcomeReEnrolled)
	before, err := os.ReadFile(filepath.Join(vault, "Renamed.view"))
	require.NoError(t, err)
	result := requireRetryResult(t, retryMoveHTTP(t, api, ws, redRetryID), gen.RetryMoveResultOutcomeAlreadyComplete)
	require.Empty(t, result.ReEnrolledPaths, "an inert completed receipt must not claim a second enrollment")
	after, err := os.ReadFile(filepath.Join(vault, "Renamed.view"))
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Empty(t, loadRecordedViews(t, api.homePath, vault).PendingMoves)
}

func TestRetryMove_LockedCollectionReturnsTypedRetryLocked(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	before := loadRecordedViews(t, api.homePath, vault)
	// Hold the real collection lock through the HTTP request: the nested
	// acquisition must time out visibly, not replay without the lock.
	require.NoError(t, knowledge.WithViewMembershipLock(api.homePath, vault, func() error {
		requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusServiceUnavailable,
			redRetryID, gen.RetryMoveErrorCodeRetryLocked)
		return nil
	}))
	require.Equal(t, before, loadRecordedViews(t, api.homePath, vault))
	require.FileExists(t, filepath.Join(vault, "Open.view"))
	require.NoFileExists(t, filepath.Join(vault, "Renamed.view"))
}

func TestRetryMove_UnknownIdReturnsRetryNotFound(t *testing.T) {
	api, ws, _, _ := twoKBWorkspace(t)
	requireRetryError(t, retryMoveHTTP(t, api, ws, "unknown-receipt"), http.StatusNotFound,
		"unknown-receipt", gen.RetryMoveErrorCodeRetryNotFound)
}

func TestRetryMove_IdentityMismatchLeavesPlantedCopyUnmanaged(t *testing.T) {
	api, ws, vault := pendingRetryFixture(t, time.Now().UTC())
	// Landed path is occupied by a different identity; a marker that names
	// the old base is deliberately insufficient to authorize this new file.
	require.NoError(t, os.Remove(filepath.Join(vault, "Open.view")))
	copyBytes := []byte("name: planted\nkind: table\nderived_from: Projects.base\n")
	require.NoError(t, os.WriteFile(filepath.Join(vault, "Renamed.view"), copyBytes, 0o600))
	requireRetryError(t, retryMoveHTTP(t, api, ws, redRetryID), http.StatusConflict,
		redRetryID, gen.RetryMoveErrorCodeRetryIdentityMismatch)
	actual, err := os.ReadFile(filepath.Join(vault, "Renamed.view"))
	require.NoError(t, err)
	require.Equal(t, copyBytes, actual)
	m := loadRecordedViews(t, api.homePath, vault)
	require.Empty(t, m.Bases, "a planted copy is never managed")
	require.Contains(t, m.PendingMoves, redRetryID)
}
