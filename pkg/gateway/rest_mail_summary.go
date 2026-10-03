package gateway

// rest_mail_summary.go - the Mail panel watcher summary (spec 2.3): one row
// per enabled mailbox pair in the workspace, rendered from the persisted
// watcher state on disk. It never dials IMAP (round-2 MAJ-019).

import (
	"errors"
	"net/http"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func (a *restAPI) handleMailSummary(w http.ResponseWriter, r *http.Request, workspaceID string) {
	started := time.Now()
	cfg := a.agentLoop.GetConfig()
	// items is ALWAYS a JSON array: zero enabled mailboxes render [] — never
	// null (MailSummaryList.yaml: items required, array, not nullable;
	// architect confirmation 1, coordination/logs/email-arch-sigcsp.log).
	out := gen.MailSummaryList{Items: []struct {
		AgentId        string                               `json:"agent_id"`
		LastErrorClass *string                              `json:"last_error_class"`
		LastSeenUid    *int64                               `json:"last_seen_uid"`
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
		row := gen.MailboxNewMailSummary{WatcherState: gen.MailboxNewMailSummaryWatcherStateOk}
		renderState := err == nil
		if err != nil {
			// MC-23/MAJ-019: an absent or unreadable state must never render
			// as ok, and must never drop the row - the mailbox stays on the
			// panel with an honest shape.
			row.WatcherState = gen.MailboxNewMailSummaryWatcherStateError
			if errors.Is(err, email.ErrNoWatcherState) {
				// Never checked: no state file has ever been written. No
				// cycle ever succeeded and none has failed either, so no
				// error class - just the honest never-checked shape.
			} else {
				// Corrupt/unreadable state file: name the failure.
				// "state_unreadable" is a load-failure class, deliberately
				// outside pkg/email's MC-8 transport-failure enum - this is
				// not a mail-server failure.
				logsafeWarn("rest: mail summary state load failed; rendering unreadable state",
					"agent_id", agentID, "workspace_id", workspaceID, "error", err)
				cls := "state_unreadable"
				row.LastErrorClass = &cls
			}
		}
		row.AgentId = agentID
		if renderState {
			switch st.EffectiveState(time.Now()) {
			case "error":
				row.WatcherState = gen.MailboxNewMailSummaryWatcherStateError
			case "backoff":
				row.WatcherState = gen.MailboxNewMailSummaryWatcherStateBackoff
			}
			row.UnseenTotal = st.UnseenTotal
			if st.LastSeenUID > 0 {
				u := mailUIDToWire(st.LastSeenUID)
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
		}
		out.Items = append(out.Items, struct {
			AgentId        string                               `json:"agent_id"`
			LastErrorClass *string                              `json:"last_error_class"`
			LastSeenUid    *int64                               `json:"last_seen_uid"`
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
	// w5 US-7.6/MC-18: the summary boundary emits its one record — zero mail
	// commands by construction (saved watcher state only), so source "none"
	// with no acquisition.
	//
	// The agentID is deliberately EMPTY: a summary request spans every
	// enabled pair in the workspace, so no single pair identity may be named
	// (naming one would be a lie about the operation's scope). The record's
	// pair_ref therefore degrades to mailPairRef's documented opaque
	// placeholder — and that is all it can do: config.LoadOrMintMailPairIdentity
	// refuses an empty agent before any write, so no identity file is ever
	// minted for the degenerate pair. Do not "fix" the empty agentID by
	// passing one row's agent: per-pair summary records are a w6-publisher
	// question (the §13-Q8 pattern), not a local choice.
	a.emitMailOperationTiming("summary", "", workspaceID, started, nil, "none", false)
	jsonOK(w, out)
}
