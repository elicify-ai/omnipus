package gateway

import "net/http"

// The initiating workspace PUT, not a concurrent room reader, owns its room
// roster change and actor. A saved workspace is never described as rolled
// back when the following room update or cancellation-label write fails.
func (rw *restAPIHandleWorkspacePut) persistWorkspaceAndRooms() bool {
	saved := false
	commit := func() error {
		if err := writeWorkspaceFile(rw.a.homePath, rw.ws); err != nil {
			return err
		}
		saved = true
		return nil
	}
	var err error
	if hub := rw.a.rooms.Load(); hub != nil && hub.svc != nil {
		err = hub.svc.ApplyWorkspaceUpdate(rw.id, requestUsername(rw.r), commit)
	} else {
		err = commit()
	}
	if err == nil {
		return true
	}
	logsafeError("rest: update workspace and group chats", "id", rw.id, "workspace_saved", saved, "error", err)
	message := "internal server error"
	if saved {
		message = "The workspace changes were saved, but its groups or pending-request labels could not be updated. Read the workspace and groups again after storage recovers before retrying."
	}
	jsonErr(rw.w, http.StatusInternalServerError, message)
	return false
}
