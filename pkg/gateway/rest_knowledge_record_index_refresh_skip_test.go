// Regression coverage for the 2026-09-14 Codex review, finding 14: the D-67
// index-refresh tests skipped on ANY vaultprops.Sync error, so a SQL
// regression, a corrupt index or a filesystem failure turned into a green
// "skipped" instead of a red test — the false-green pattern
// docs/internal/false-green-patterns.md forbids.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// recordingTB captures whether requirePropertiesIndexSynced chose to skip
// or to fail, without stopping the enclosing test. Embedding testing.TB
// satisfies the interface's unexported method; only the two verdict methods
// are overridden.
type recordingTB struct {
	testing.TB
	skipped string
	failed  string
}

func (r *recordingTB) Skipf(format string, args ...any)  { r.skipped = fmt.Sprintf(format, args...) }
func (r *recordingTB) Fatalf(format string, args ...any) { r.failed = fmt.Sprintf(format, args...) }
func (r *recordingTB) Helper()                           {}

// TestRequirePropertiesIndexSynced_SkipsOnlyForThePlatformSentinel — the
// explicit "not compiled into this build" error is the ONLY skip; every
// other Sync error must fail the test.
func TestRequirePropertiesIndexSynced_SkipsOnlyForThePlatformSentinel(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantSkip bool
	}{
		{"nil is neither", nil, false},
		{"platform sentinel", records.ErrPropertyIndexUnavailable, true},
		{"wrapped platform sentinel", fmt.Errorf("vaultprops: %w", records.ErrPropertyIndexUnavailable), true},
		{"a SQL regression", errors.New("propindex: updating \"x.md\": SQL logic error: no such column"), false},
		{"a filesystem failure", fmt.Errorf("vaultprops: creating the index directory: %w", os.ErrPermission), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingTB{TB: t}
			requirePropertiesIndexSynced(rec, tc.err)
			if tc.wantSkip {
				require.NotEmpty(t, rec.skipped, "the platform sentinel must skip")
				require.Empty(t, rec.failed)
				return
			}
			require.Empty(t, rec.skipped, "a non-platform error must never skip: %v", tc.err)
			if tc.err != nil {
				require.NotEmpty(t, rec.failed, "a non-platform error must fail the test")
			} else {
				require.Empty(t, rec.failed)
			}
		})
	}
}

// TestRequirePropertiesIndexSynced_ForcedNonPlatformSyncErrorFails — proof
// with a REAL vaultprops.Sync error that is not the platform sentinel: the
// index directory's parent is a regular file, so Sync cannot create it. The
// helper must fail, not skip.
func TestRequirePropertiesIndexSynced_ForcedNonPlatformSyncErrorFails(t *testing.T) {
	if err := records.RequirePropertyIndex(records.CapabilityOpenIndex); err != nil {
		t.Skipf("properties index unavailable on this platform: %v", err)
	}
	api, _, vault := buildRecordTestVault(t)
	// Force the failure: put a regular file where $OMNIPUS_HOME's index tree
	// must be created.
	blockedHome := filepath.Join(t.TempDir(), "home-is-a-file")
	require.NoError(t, os.WriteFile(blockedHome, []byte("not a directory"), 0o600))
	_ = api
	_, err := vaultprops.Sync(context.Background(), blockedHome, vault, vaultprops.SyncOptions{})
	require.Error(t, err, "the forced condition must produce a Sync error")
	require.False(t, errors.Is(err, records.ErrPropertyIndexUnavailable), "the forced error must not be the platform sentinel: %v", err)

	rec := &recordingTB{TB: t}
	requirePropertiesIndexSynced(rec, err)
	require.Empty(t, rec.skipped, "a forced non-platform Sync error must not be turned into a skip")
	require.NotEmpty(t, rec.failed, "a forced non-platform Sync error must fail the test")
}
