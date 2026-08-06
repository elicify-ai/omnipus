package workspace

import (
	"errors"
	"fmt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// ErrCascadeStraggler marks a WorkspaceDeleteHook error that occurred
// AFTER the media library's manifest was already fully and correctly
// updated. library.CascadeDelete's two-phase commit (see its own doc
// comment) only ever returns a non-nil error together with a non-empty
// `deleted` slice when phase 2 (manifest mutation + persist) already
// committed cleanly and the sole remaining failure is a final on-disk
// unlink of one or more already-quarantined files — see wrapCascadeError.
//
// Callers (rest_workspaces.go's delete-workspace handler) MUST NOT treat
// an error wrapping ErrCascadeStraggler as a failed delete: the media
// entries are genuinely gone from the manifest, and every quarantine path
// this straggler could refer to lives under wsDir/media/ — which that
// handler's own subsequent os.RemoveAll(wsDir) always cleans up
// regardless, moments later. A caller that DOES want to distinguish this
// case for its own logging/audit purposes can errors.Is(err,
// ErrCascadeStraggler).
var ErrCascadeStraggler = errors.New("workspace: cascade-delete committed; final unlink straggler only")

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
// An audit event is emitted on BOTH success and failure — including a
// failure to even OPEN the library (e.g. a corrupt manifest.json). An
// earlier version of this hook only logged when at least one entry was
// deleted, which meant a cascade that failed before deleting anything
// produced ZERO audit trail: exactly the repudiation gap FR-009 exists to
// close. See logCascadeAuditEvent's doc for the decision/detail shape.
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
		openErr := fmt.Errorf("workspace: open media library for cascade-delete %q: %w", workspaceID, err)
		logCascadeAuditEvent(auditor, workspaceID, actor, nil, 0, openErr)
		return openErr
	}

	deleted, bytesFreed, cascadeErr := lib.CascadeDelete()
	if auditErr := logCascadeAuditEvent(auditor, workspaceID, actor, deleted, bytesFreed, cascadeErr); auditErr != nil {
		// Best-effort: cascade delete succeeded but the audit write
		// failed. Surface as a wrapping error so the REST handler logs
		// the gap; do NOT roll back the cascade (the on-disk state is
		// already correct).
		if cascadeErr == nil {
			return auditErr
		}
	}

	return wrapCascadeError(deleted, cascadeErr)
}

// wrapCascadeError applies the ErrCascadeStraggler distinction (re-review
// FIX 2) to a raw library.CascadeDelete result. Extracted as its own pure
// function — taking only CascadeDelete's three return values, no
// filesystem access — specifically so the distinguishing logic is
// unit-testable with synthetic inputs: forcing a REAL *library.Library
// into the "manifest committed, final-unlink straggler" state requires
// library.go's package-private removeFile injection seam
// (library.WithFileRemover), which WorkspaceDeleteHook's own
// library.New(home, workspaceID) call (no Options) does not expose, so an
// end-to-end test of WorkspaceDeleteHook itself cannot reach this branch
// deterministically — see media_delete_test.go for both this function's
// unit coverage and a separate integration test proving library.go's real
// CascadeDelete does return exactly this (non-empty deleted, non-nil err)
// shape under a genuine final-unlink failure.
//
// cascadeErr is returned unwrapped whenever deleted is empty — that shape
// covers every hard-failure exit of CascadeDelete (a quarantine-rename
// failure partway through phase 1, or a persist failure in phase 2 that
// rolls the manifest back): in both cases nothing was actually committed,
// so the caller must treat it as a genuine failure where media may still
// remain.
//
// A hard-failure cascadeErr may now additionally wrap library.ErrEntryStranded
// when a COMPOUND rollback-of-rollback happens mid-cascade — a rollback
// restores an entry's manifest presence AND the compensating rename-back of
// its already-quarantined file also fails (see library.go's ErrEntryStranded
// doc). CascadeDelete's own contract keeps `deleted` empty on that path
// specifically so this function's classification here is unaffected: the
// caller (rest_workspaces.go's delete-workspace handler) still sees an
// unwrapped hard failure, not ErrCascadeStraggler. That is intentional and
// safe for this specific caller regardless: every quarantine path an
// ErrEntryStranded id could refer to still lives under wsDir/media/, which
// the handler's own subsequent os.RemoveAll(wsDir) always wipes moments
// later — the same reasoning ErrCascadeStraggler's own doc already gives for
// the final-unlink-only case, just for a different underlying failure shape.
func wrapCascadeError(deleted []gen.MediaLibraryEntry, cascadeErr error) error {
	if cascadeErr != nil && len(deleted) > 0 {
		return fmt.Errorf("%w: %w", ErrCascadeStraggler, cascadeErr)
	}
	return cascadeErr
}

// logCascadeAuditEvent emits the media.cascade_delete audit event for a
// workspace-delete cascade. It fires whenever auditor is non-nil,
// regardless of outcome — unlike an earlier version of this logic, which
// only fired when len(deleted) > 0 and therefore produced no audit record
// at all for a cascade that failed before deleting anything (library.New
// failing to open a corrupt manifest, or CascadeDelete's own
// quarantine/persist step failing outright before a single entry was
// removed).
//
// Decision is DecisionError whenever cascadeErr is non-nil (even a
// "partial" one — CascadeDelete's two-phase commit, see its doc, DOES have
// an in-between state as of its own FIX 3/4: `deleted` is non-empty AND
// cascadeErr is non-nil exactly when phase 2 (manifest mutation + persist)
// already committed cleanly and only a final-unlink straggler on one or
// more already-quarantined files failed afterward. wrapCascadeError
// (called by this function's caller, WorkspaceDeleteHook, AFTER this audit
// event is emitted) wraps that specific case in ErrCascadeStraggler so its
// own caller — the REST delete-workspace handler — can treat it as
// non-fatal to the client response (re-review FIX 2). This audit event
// still records it as DecisionError regardless: at the moment this event
// is emitted, disk cleanup genuinely was incomplete, even though the
// directory wipe the REST handler runs immediately afterward happens to
// erase the evidence a moment later — the audit trail should reflect what
// actually happened here, not what a later, unrelated step does about it),
// DecisionAllow otherwise. A
// successful no-op cascade on an empty library (deleted == nil,
// cascadeErr == nil) still emits the event with count=0 — that is itself
// useful signal (the cascade ran and confirmed there was nothing to
// delete) and keeps "every cascade attempt leaves a trail" an
// unconditional invariant rather than one gated on a count.
//
// Returns the auditor.Log error (or nil if auditor is nil / the write
// succeeded) so WorkspaceDeleteHook can decide whether to surface an
// audit-write failure to its own caller.
func logCascadeAuditEvent(
	auditor *audit.Logger,
	workspaceID, actor string,
	deleted []gen.MediaLibraryEntry,
	bytesFreed int64,
	cascadeErr error,
) error {
	if auditor == nil {
		return nil
	}
	mediaIDs := make([]string, 0, len(deleted))
	filenames := make([]string, 0, len(deleted))
	for _, entry := range deleted {
		if entry.Id != nil {
			mediaIDs = append(mediaIDs, entry.Id.String())
		}
		filenames = append(filenames, entry.Filename)
	}
	decision := audit.DecisionAllow
	details := map[string]any{
		"actor":        actor,
		"workspace_id": workspaceID,
		"media_ids":    mediaIDs,
		"filenames":    filenames,
		"bytes_freed":  bytesFreed,
		"count":        len(deleted),
	}
	if cascadeErr != nil {
		decision = audit.DecisionError
		details["error"] = cascadeErr.Error()
	}
	return auditor.Log(&audit.Entry{
		Event:    audit.EventMediaCascadeDelete,
		Decision: decision,
		Details:  details,
	})
}
