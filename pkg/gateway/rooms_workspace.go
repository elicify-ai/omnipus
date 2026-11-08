package gateway

import (
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
)

// The initiating PUT owns the attributed room update. It reports whether the
// workspace itself committed, independently of the following room-sync result.
// Response writing is deferred until independent post-save cleanup has run.
func (rw *restAPIHandleWorkspacePut) persistWorkspaceAndRooms() (saved bool, saveErr error) {
	commit := func() error {
		if err := writeWorkspaceFile(rw.a.homePath, rw.ws); err != nil {
			return err
		}
		saved = true
		return nil
	}
	if hub := rw.a.rooms.Load(); hub != nil && hub.svc != nil {
		saveErr = hub.svc.ApplyWorkspaceUpdate(rw.id, requestUsername(rw.r), commit)
	} else {
		saveErr = commit()
	}
	if saveErr != nil {
		logsafeError("rest: update workspace and group chats", "id", rw.id, "workspace_saved", saved, "error", saveErr)
	}
	return saved, saveErr
}

// These effects follow the workspace record, not successful room/graph writes.
// A partial save must not strand removed members' heartbeat jobs or identities.
func (rw *restAPIHandleWorkspacePut) reconcileSavedWorkspace() {
	if mainsErr := rw.a.ensureMainsForTeamFor(rw.ws, requestUsername(rw.r)); mainsErr != nil {
		logsafeError("rest: workspace team update: some team mains were not created; each is re-attempted when its chat is opened", "workspace_id", rw.ws.ID, "error", mainsErr)
	}
	if rw.mcPresent || rw.coreTeamChanged {
		rw.a.reconcileHeartbeatSchedules()
	}
	if rw.a.auditor != nil {
		if err := rw.a.auditor.Log(&audit.Entry{Event: "workspace.update", Decision: audit.DecisionAllow, Details: map[string]any{"id": rw.id}}); err != nil {
			logsafeWarn("audit write failed", "event", "workspace.update", "id", rw.id, "error", err)
		}
	}
}

// Reuse the declared updateWorkspace failure shape. Revision and changed fields
// are the components actually committed, never the attempted graph or old cache.
func (rw *restAPIHandleWorkspacePut) writeWorkspaceSaveFailure(revision string, saved bool, stage, message string, fields []string) {
	persistence := gen.ConfigurationMutationStatePersistenceStatusNone
	if saved {
		persistence = gen.ConfigurationMutationStatePersistenceStatusPartial
	}
	writeJSON(rw.w, http.StatusInternalServerError, gen.ConfigurationMutationState{
		PersistenceStatus: persistence, ActivationStatus: gen.ConfigurationMutationStateActivationStatusNotAttempted,
		Revision: revision, ChangedFields: append([]string{}, fields...), ErrorStage: &stage, Message: &message,
	})
}
