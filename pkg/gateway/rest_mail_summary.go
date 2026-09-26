package gateway

// rest_mail_summary.go - the Mail panel watcher summary (spec 2.3): one row
// per enabled mailbox pair in the workspace, rendered from the persisted
// watcher state on disk. It never dials IMAP (round-2 MAJ-019).

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func (a *restAPI) handleMailSummary(w http.ResponseWriter, r *http.Request, workspaceID string) {
	cfg := a.agentLoop.GetConfig()
	out := gen.MailSummaryList{}
	for agentID, ws := range cfg.Mailboxes {
		mb, ok := ws[workspaceID]
		if !ok || !mb.Enabled {
			continue
		}
		if !a.agentExists(agentID) {
			continue
		}
		st, err := email.LoadWatcherState(a.homePath, agentID, workspaceID)
		if err != nil && !errors.Is(err, email.ErrNoWatcherState) {
			slog.Warn("rest: mail summary state load failed; mailbox omitted from the panel",
				"agent_id", agentID, "workspace_id", workspaceID, "error", err)
			continue
		}
		row := gen.MailboxNewMailSummary{WatcherState: gen.MailboxNewMailSummaryWatcherStateOk}
		if st == nil {
			st = &email.WatcherState{State: "ok", UnseenTotal: 0}
		}
		switch st.EffectiveState(time.Now()) {
		case "error":
			row.WatcherState = gen.MailboxNewMailSummaryWatcherStateError
		case "backoff":
			row.WatcherState = gen.MailboxNewMailSummaryWatcherStateBackoff
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
		if st.LastSuccessAt != "" {
			if ts, perr := time.Parse(time.RFC3339, st.LastSuccessAt); perr == nil {
				row.LastSuccessAt = &ts
			}
		}
		if st.NextAttemptAt != "" {
			if ts, perr := time.Parse(time.RFC3339, st.NextAttemptAt); perr == nil {
				row.NextAttemptAt = &ts
			}
		}
		out.Items = append(out.Items, struct {
			AgentId        string                               `json:"agent_id"`
			LastErrorClass *string                              `json:"last_error_class"`
			LastSeenUid    *int                                 `json:"last_seen_uid"`
			LastSuccessAt  *time.Time                           `json:"last_success_at"`
			NextAttemptAt  *time.Time                           `json:"next_attempt_at"`
			UnseenTotal    int                                  `json:"unseen_total"`
			WatcherState   gen.MailSummaryListItemsWatcherState `json:"watcher_state"`
		}{
			AgentId:        row.AgentId,
			LastErrorClass: row.LastErrorClass,
			LastSeenUid:    row.LastSeenUid,
			LastSuccessAt:  row.LastSuccessAt,
			NextAttemptAt:  row.NextAttemptAt, UnseenTotal: row.UnseenTotal,
			WatcherState: gen.MailSummaryListItemsWatcherState(row.WatcherState),
		})
	}
	jsonOK(w, out)
}
