// Omnipus — FR-VA-032 agent Retry argument boundary: pending_move_id only.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeRestructureRetryMove_RejectsExtraArgumentsAndChangesNothing(t *testing.T) {
	for _, extra := range []string{
		"collection", "path", "new_name", "new_folder", "allow_ambiguity",
		"trashed_at", "folder", "expect_version", "unrecognised_argument",
	} {
		t.Run(extra, func(t *testing.T) {
			home, ws, root := a4Fixture(t, "KB")
			deps, _ := a4Deps(home)
			plantKnowledgeTrackedView(t, home, root, "Projects.base", "Open.view")
			dir, err := IndexDirFor(home, root)
			require.NoError(t, err)
			record := filepath.Join(dir, ViewMembershipFileName)
			before, err := os.ReadFile(record)
			require.NoError(t, err)
			viewBefore, err := os.ReadFile(filepath.Join(root, "Open.view"))
			require.NoError(t, err)

			res := NewRestructureTool(deps).Execute(a4Ctx("mia", ws), map[string]any{
				"op": "retry_move", "pending_move_id": "missing-receipt", extra: "unexpected",
			})
			require.True(t, res.IsError, "retry_move must refuse extra %q: %s", extra, res.ForLLM)
			require.Contains(t, res.ForLLM, "retry_move takes only pending_move_id",
				"FR-VA-032 requires one clear argument-shape error for extra %q", extra)
			after, err := os.ReadFile(record)
			require.NoError(t, err)
			require.Equal(t, before, after, "rejected arguments cannot consume or rewrite the receipt")
			viewAfter, err := os.ReadFile(filepath.Join(root, "Open.view"))
			require.NoError(t, err)
			require.Equal(t, viewBefore, viewAfter)
		})
	}
}
