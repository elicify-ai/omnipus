package workspace

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// WorkspaceDeleteHook cascade-deletes a workspace's media library as part
// of workspace removal (FR-009). It opens the workspace library (if one
// exists; a never-uploaded-to workspace has no manifest), calls
// library.CascadeDelete() to remove every entry atomically, and emits a
// single media.cascade_delete audit event with the deleted media_ids +
// filenames + bytes_freed summary.
//
// The auditor argument is optional — pass nil on paths that have not
// wired an audit logger (e.g. test code that does not assert audit
// shape). When nil, no audit event is emitted; the cascade delete still
// runs to completion and its result is reported via the error return.
//
// actor is the authenticated principal (admin username, "cli", or a
// channel-platform identity) that triggered the workspace deletion. It is
// recorded in the audit Details so a forensic query can attribute the
// cascade to a specific caller. Empty string is preserved as-is — the
// audit shape contract requires the field to be present even when the
// principal could not be resolved.
//
// B1's signature was `func(home, workspaceID string) error`; B2 extends
// it to thread the actor + auditor through. The shape extension is
// forward-compatible: a caller that has no actor/auditor available can
// pass empty + nil. The wire-up at pkg/gateway/rest_workspaces.go was
// added in this slice to invoke the hook from the workspace-delete
// handler.
func WorkspaceDeleteHook(home, workspaceID, actor string, auditor *audit.Logger) error {
	if _, err := SafeWorkspaceDir(home, workspaceID); err != nil {
		return err
	}

	lib, err := library.New(home, workspaceID)
	if err != nil {
		return fmt.Errorf("workspace: open media library for cascade-delete %q: %w", workspaceID, err)
	}

	deleted, bytesFreed, cascadeErr := lib.CascadeDelete()

	if auditor != nil && len(deleted) > 0 {
		mediaIDs := make([]string, 0, len(deleted))
		filenames := make([]string, 0, len(deleted))
		for _, entry := range deleted {
			if entry.Id != nil {
				mediaIDs = append(mediaIDs, entry.Id.String())
			}
			filenames = append(filenames, entry.Filename)
		}
		if logErr := auditor.Log(&audit.Entry{
			Event:    audit.EventMediaCascadeDelete,
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"actor":        actor,
				"workspace_id": workspaceID,
				"media_ids":    mediaIDs,
				"filenames":    filenames,
				"bytes_freed":  bytesFreed,
				"count":        len(deleted),
			},
		}); logErr != nil {
			// Best-effort: cascade delete succeeded but the audit write
			// failed. Surface as a wrapping error so the REST handler logs
			// the gap; do NOT roll back the cascade (the on-disk state is
			// already correct).
			if cascadeErr == nil {
				return fmt.Errorf("workspace: cascade-delete media %q: audit write failed: %w", workspaceID, logErr)
			}
		}
	}

	return cascadeErr
}
