package gateway

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailMutationLimiter guards every Mail panel mutation (MC-20): 10/min/IP,
// write-sized. Declared in rest_mail.go next to its handlers.
var mailMutationLimiter = newAPIRateLimiter(10, 1*time.Minute)

// handleWorkspaceMail dispatches /api/v1/workspaces/{id}/mail/... from
// HandleWorkspaces (rest still carries the "/{id}" prefix). The mux has no
// path wildcards, so this handler IS the mail router. Path segments are
// percent-decoded here (mid: refs carry <> characters).
func (a *restAPI) handleWorkspaceMail(w http.ResponseWriter, r *http.Request, rest string) {
	segs := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	if len(segs) < 2 || segs[1] != "mail" {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	workspaceID := segs[0]
	tail := segs[2:]
	for i, s := range tail {
		dec, derr := url.PathUnescape(s)
		if derr != nil {
			jsonErr(w, http.StatusBadRequest, "malformed path segment")
			return
		}
		tail[i] = dec
	}
	switch {
	case len(tail) == 1 && tail[0] == "summary":
		a.handleMailSummary(w, r, workspaceID)
	case len(tail) == 2 && tail[0] == "folders":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailFolders(w, r, workspaceID, tail[1])
	case len(tail) == 2 && tail[1] == "messages":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailSend(w, r, workspaceID, tail[0])
	// Draft actions shadow the generic folder-message read for folder
	// "drafts": the switch takes the FIRST matching case, so the drafts
	// cases must precede the generic one below.
	case len(tail) == 4 && tail[0] == "folders" && tail[1] == "drafts" && tail[2] == "messages":
		a.handleMailDraftAction(w, r, workspaceID, tail[0], tail[3])
	case len(tail) == 5 && tail[0] == "folders" && tail[1] == "drafts" && tail[2] == "messages" && tail[4] == "send":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailDraftSend(w, r, workspaceID, tail[0], tail[3])
	case len(tail) == 3 && tail[0] == "folders" && tail[2] == "messages":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailList(w, r, workspaceID, tail[0], tail[1])
	case len(tail) == 4 && tail[0] == "folders" && tail[2] == "messages":
		a.handleMailFolderMessage(w, r, workspaceID, tail[0], tail[1], tail[3])
	case len(tail) == 5 && tail[0] == "folders" && tail[2] == "messages" && tail[4] == "seen":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailSeen(w, r, workspaceID, tail[0], tail[1], tail[3])
	case len(tail) == 6 && tail[0] == "folders" && tail[2] == "messages" && tail[4] == "attachments":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		idx, ierr := strconv.Atoi(tail[5])
		if ierr != nil || idx < 0 {
			jsonErr(w, http.StatusBadRequest, "malformed part index")
			return
		}
		a.handleMailAttachment(w, r, workspaceID, tail[0], tail[1], tail[3], idx)
	default:
		jsonErr(w, http.StatusNotFound, "not found")
	}
}

// mailPairClient resolves one (workspace, agent) mailbox pair to a live
// authenticated email client: unknown pair, disabled mailbox, unresolvable
// password, or transport construction failure are all the spec's 404 "pair
// has no mailbox" class (the getAgentMailbox precedent; no distinguishable
// body - MC-13 sibling). Locked credential store is 500.
func (a *restAPI) mailPairClient(w http.ResponseWriter, agentID, workspaceID string) *email.Client {
	if !a.agentExists(agentID) {
		jsonErr(w, http.StatusNotFound, "agent not found")
		return nil
	}
	cfg := a.agentLoop.GetConfig()
	mb, ok := cfg.Mailboxes[agentID][workspaceID]
	if !ok || !mb.Enabled {
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return nil
	}
	store := a.credStore
	if store == nil {
		s := credentials.NewStore(a.credentialsStorePath())
		if err := credentials.Unlock(s); err != nil {
			slog.Error("rest: mail credential store locked", "agent_id", agentID, "error", err)
			jsonErr(w, http.StatusInternalServerError, "credential store unavailable")
			return nil
		}
		store = s
	}
	password, perr := store.Get(mb.PasswordRef)
	if perr != nil || strings.TrimSpace(password) == "" {
		slog.Warn("rest: mail password did not resolve", "agent_id", agentID, "workspace_id", workspaceID)
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return nil
	}
	client, cerr := email.NewClient(email.Account{
		IMAPHost: mb.IMAPHost,
		IMAPPort: mb.IMAPPort,
		SMTPHost: mb.SMTPHost,
		SMTPPort: mb.SMTPPort,
		Username: mb.Username,
		Password: password,
	})
	if cerr != nil {
		slog.Warn("rest: mail transport construction failed", "agent_id", agentID, "error", cerr)
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return nil
	}
	return client
}

// mailComposeConfig returns the compose-relevant mailbox settings (from
// address, signature, sent/drafts folder names) for one pair, or ok=false.
func (a *restAPI) mailComposeConfig(agentID, workspaceID string) (config.MailboxConfig, bool) {
	cfg := a.agentLoop.GetConfig()
	mb, ok := cfg.Mailboxes[agentID][workspaceID]
	if !ok || !mb.Enabled {
		return config.MailboxConfig{}, false
	}
	return mb, true
}

// auditMail writes one mail panel audit entry (MC-19). Auditor absence (a
// nil auditor in tests) skips silently; write failures are WARN, never fatal.
func auditMail(a *restAPI, event audit.EventName, decision audit.Decision, details map[string]any) {
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    string(event),
		Decision: string(decision),
		Details:  details,
	}); err != nil {
		slog.Warn("audit write failed", "event", event, "error", err)
	}
}

// mailErr502 writes the MC-8 error envelope: the closed error class is the
// only thing that crosses the wire.
func mailErr502(w http.ResponseWriter, err error) {
	slog.Error("rest: mail upstream failure", "class", email.ClassifyMailError(err), "error", err)
	jsonErr(w, http.StatusBadGateway, "mail server error: "+email.ClassifyMailError(err))
}
