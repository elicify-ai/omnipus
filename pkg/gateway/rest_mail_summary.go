package gateway

// rest_mail_summary.go - the Mail panel watcher summary (spec 2.3): one row
// per enabled mailbox pair in the workspace, rendered from the persisted
// watcher state on disk. It never dials IMAP (round-2 MAJ-019).

import (
	"net/http"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func (a *restAPI) handleMailSummary(w http.ResponseWriter, r *http.Request, workspaceID string) {
	cfg := a.agentLoop.GetConfig()
	out := gen.MailSummaryList{Items: []struct {
		AgentId        string                               `json:"agent_id"`
		LastErrorClass *string                              `json:"last_error_class"`
		LastSeenUid    *int                                 `json:"last_seen_uid"`
		LastSuccessAt  *time.Time                           `json:"last_success_at"`
		NextAttemptAt  *time.Time                           `json:"next_attempt_at"`
		UnseenTotal    int                                  `json:"unseen_total"`
		WatcherState   gen.MailSummaryListItemsWatcherState `json:"watcher_state"`
	}{}}
	for agentID, ws := range cfg.Mailboxes {
		mb, ok := ws[workspaceID]
		if !ok || !mb.Enabled {
			continue
		}
		if !a.agentExists(agentID) {
			continue
		}
		st, err := email.LoadWatcherState(a.homePath, agentID, workspaceID)
		if err != nil {
			continue
		}
		var row struct {
			AgentId        string                               `json:"agent_id"`
			LastErrorClass *string                              `json:"last_error_class"`
			LastSeenUid    *int                                 `json:"last_seen_uid"`
			LastSuccessAt  *time.Time                           `json:"last_success_at"`
			NextAttemptAt  *time.Time                           `json:"next_attempt_at"`
			UnseenTotal    int                                  `json:"unseen_total"`
			WatcherState   gen.MailSummaryListItemsWatcherState `json:"watcher_state"`
		}
		row.WatcherState = gen.MailSummaryListItemsWatcherStateOk
		if st == nil {
			st = &email.WatcherState{State: "ok", UnseenTotal: 0}
			row.LastSeenUid = nil
		}
		if st.State == "error" {
			row.WatcherState = gen.MailSummaryListItemsWatcherStateError
		} else if st.State == "backoff" {
			row.WatcherState = gen.MailSummaryListItemsWatcherStateBackoff
		}
		row.AgentId = agentID
		row.UnseenTotal = st.UnseenTotal
		if st.LastSeenUID > 0 {
			u := int(st.LastSeenUID)
			row.LastSeenUid = &u
		}
		if s := st.LastErrorClass; s != "" {
			row.LastErrorClass = &s
		}
		if ts, perr := time.Parse(time.RFC3339, st.LastSuccessAt); perr == nil && st.LastSuccessAt != "" {
			row.LastSuccessAt = &ts
		}
		if ts, perr := time.Parse(time.RFC3339, st.NextAttemptAt); perr == nil && st.NextAttemptAt != "" {
			row.NextAttemptAt = &ts
		}
		out.Items = append(out.Items, row)
	}
	jsonOK(w, out)
}
