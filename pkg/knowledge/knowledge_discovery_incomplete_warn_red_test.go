// Omnipus — FR-VA-037 / TDD row 86: incomplete discovery cannot retire
// recorded authority, and the operator receives a Warn naming the collection
// and skipped subtree. The real loader's SkipUnreadable walk is a separate
// integration seam; this test supplies the loader's incomplete report directly.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/stretchr/testify/require"
)

func TestIncompleteDiscovery_KeepsRecordMemberAndLogsWarnNamingSkippedPaths(t *testing.T) {
	home, _, root := a4Fixture(t, "KB")
	plantKnowledgeTrackedView(t, home, root, "Projects.base", "Open.view")
	// An external move into a subtree the discovery walk cannot read means
	// the recorded OLD path is absent, but a real claimant may still exist.
	skipped := filepath.Join(root, "private")
	require.NoError(t, os.MkdirAll(skipped, 0o700))
	require.NoError(t, os.Rename(filepath.Join(root, "Open.view"), filepath.Join(skipped, "Open.view")))
	incomplete := &records.ViewLoadReport{Rejections: []records.ViewRejection{{
		Paths: []string{skipped}, Code: records.RejectViewUnreadable,
		Reason: "private subtree skipped during discovery",
	}}}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	require.NoError(t, WithViewMembership(home, root, func(m *ViewMembership) error {
		return m.ReconcileManagedViewPaths(records.NewViewSet(), incomplete)
	}))
	require.Equal(t, "Open.view", loadKnowledgeMembers(t, home, root).Bases["Projects.base"]["open"],
		"an incomplete walk cannot prove the absent old path is retired")
	require.FileExists(t, filepath.Join(skipped, "Open.view"))
	warn := logs.String()
	require.Contains(t, warn, "level=WARN")
	require.Contains(t, warn, root, "FR-VA-037 requires the server warning to name the collection")
	require.True(t, strings.Contains(warn, skipped),
		"FR-VA-037 requires the server warning to name the skipped subtree: %s", warn)
}

func TestIncompleteDiscovery_ActualSkippedSubtreeKeepsRecordedMember(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-037/row 86's physical SkipUnreadable walk cannot be exercised: " +
		"the required pkg/knowledge.LoadViewsForCollection and ReadViewFile discovery entrypoints " +
		"are absent from this branch (fresh rg search); the synthetic-report test above proves " +
		"reconciliation and the Warn but does not claim a real unreadable subtree reached it")
}
