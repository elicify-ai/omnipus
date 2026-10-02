package gateway

import (
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

// mailBodyLimit bounds the wire (JSON) request body for every /mail/ route
// registered under HandleWorkspaces — generous enough to actually reach
// MC-32's 25 MiB decoded-attachment budget (mailMaxAttachmentBytes,
// rest_mail_send.go), unlike the generic withAuth's 1 MiB cap. Attachment
// bytes travel base64-encoded on the wire (~4/3 inflation: 25 MiB decoded is
// ~33.3 MiB encoded), a send may carry up to mailMaxAttachments (10)
// attachments plus their filenames/content-types, and the enclosing JSON
// envelope adds its own overhead — 40 MiB leaves comfortable headroom over
// the 4/3-inflated 25 MiB budget while staying a small, bounded multiple of
// it (compare withUploadAuth's 1 GB for /api/v1/library, which streams
// arbitrary file uploads with no fixed budget at all).
const mailBodyLimit = 40 << 20

// withWorkspacesBodyLimit picks the request body limit BEFORE the body is
// wrapped in http.MaxBytesReader (withAuthAndBodyLimit does that wrapping
// exactly once, so the choice must be made here, at the mux entry point, not
// inside HandleWorkspaces — by the time HandleWorkspaces runs, the generic
// withAuth's 1 MiB limit has already truncated the read). /mail/ routes
// (handleWorkspaceMail: manual send, draft create/update/send, and the
// mail-panel read routes) get mailBodyLimit; every other workspace-scoped
// route keeps the generic withAuth's 1 MiB. The path check mirrors
// HandleWorkspaces' own dispatch (rest_workspaces.go) — the mux has no path
// wildcards, so both do the same substring check independently.
func (a *restAPI) withWorkspacesBodyLimit(handler http.HandlerFunc) http.HandlerFunc {
	mailLimited := a.withAuthAndBodyLimit(handler, mailBodyLimit)
	generic := a.withAuth(handler)
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimSuffix(r.URL.Path, "/")
		rest := strings.TrimPrefix(path, "/api/v1/workspaces")
		if idx := strings.Index(rest, "/mail/"); idx > 0 {
			mailLimited(w, r)
			return
		}
		generic(w, r)
	}
}

// mailUIDToWire widens the IMAP uint32 domain into the signed 64-bit wire
// domain without passing through architecture-sized int.
func mailUIDToWire(uid uint32) int64 { return int64(uid) }

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
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailSummary(w, r, workspaceID)
	case len(tail) == 2 && tail[1] == "folders":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailFolders(w, r, workspaceID, tail[0])
	case len(tail) == 2 && tail[1] == "messages" && r.Method == http.MethodPost:
		a.handleMailSend(w, r, workspaceID, tail[0])
	case len(tail) == 2 && tail[1] == "messages":
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	// Draft cases shadow the generic folder-message cases for folder
	// "drafts": the switch takes the FIRST matching case, so the drafts
	// cases must precede the generic ones below. GET is the one exception:
	// the contract's generic read path enumerates folder=drafts as valid
	// and defines no `get` on the literal drafts path item (contracts/
	// openapi.yaml) — a draft's detail read always belonged on the same
	// read path every other folder uses (US-7: the human must be able to
	// open an agent's draft to edit it). Only PUT/DELETE are the draft
	// panel's own mutations.
	case len(tail) == 5 && tail[1] == "folders" && tail[2] == "drafts" && tail[3] == "messages":
		if r.Method == http.MethodGet {
			a.handleMailFolderMessage(w, r, workspaceID, tail[0], tail[2], tail[4])
			return
		}
		a.handleMailDraftAction(w, r, workspaceID, tail[0], tail[4])
	case len(tail) == 6 && tail[1] == "folders" && tail[2] == "drafts" && tail[3] == "messages" && tail[5] == "send":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailDraftSend(w, r, workspaceID, tail[0], tail[4])
	case len(tail) == 4 && tail[1] == "folders" && tail[3] == "messages":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailList(w, r, workspaceID, tail[0], tail[2])
	case len(tail) == 5 && tail[1] == "folders" && tail[3] == "messages":
		a.handleMailFolderMessage(w, r, workspaceID, tail[0], tail[2], tail[4])
	case len(tail) == 6 && tail[1] == "folders" && tail[3] == "messages" && tail[5] == "seen":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailSeen(w, r, workspaceID, tail[0], tail[2], tail[4])
	case len(tail) == 7 && tail[1] == "folders" && tail[3] == "messages" && tail[5] == "attachments":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		idx, ierr := strconv.Atoi(tail[6])
		if ierr != nil || idx < 0 {
			jsonErr(w, http.StatusBadRequest, "malformed part index")
			return
		}
		a.handleMailAttachment(w, r, workspaceID, tail[0], tail[2], tail[4], idx)
	// F2: the save-to-library subresource (w4). The drafts shadowing note
	// above does not apply: drafts attachments save through the same
	// explicit subresource, and this case is longer than the generic
	// folder-message cases so it matches only its own shape.
	case len(tail) == 8 && tail[1] == "folders" && tail[3] == "messages" && tail[5] == "attachments" && tail[7] == "save-to-library":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		idx, ierr := strconv.Atoi(tail[6])
		if ierr != nil || idx < 0 {
			jsonErr(w, http.StatusBadRequest, "malformed part index")
			return
		}
		a.handleMailAttachmentSave(w, r, workspaceID, tail[0], tail[2], tail[4], idx)
	// F5: the reply-context operation (w4).
	case len(tail) == 6 && tail[1] == "folders" && tail[3] == "messages" && tail[5] == "reply-context":
		a.handleMailReplyContext(w, r, workspaceID, tail[0], tail[2], tail[4])
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
			logsafeError("rest: mail credential store locked", "agent_id", agentID, "error", err)
			jsonErr(w, http.StatusInternalServerError, "credential store unavailable")
			return nil
		}
		store = s
	}
	password, perr := store.Get(mb.PasswordRef)
	if perr != nil || strings.TrimSpace(password) == "" {
		logsafeWarn("rest: mail password did not resolve", "agent_id", agentID, "workspace_id", workspaceID)
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return nil
	}
	client, cerr := email.NewClient(email.Account{
		IMAPHost:     mb.IMAPHost,
		IMAPPort:     mb.IMAPPort,
		SMTPHost:     mb.SMTPHost,
		SMTPPort:     mb.SMTPPort,
		Username:     mb.Username,
		Password:     password,
		SentFolder:   mb.SentFolderName,
		DraftsFolder: mb.DraftsFolderName,
	})
	if cerr != nil {
		logsafeWarn("rest: mail transport construction failed", "agent_id", agentID, "error", cerr)
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return nil
	}
	// w5-integration (MC-1, wiring site 5): the panel's per-request client
	// borrows sessions from THE shared pool — never a private one — under
	// the pair's own identity scope.
	wireMailSessionSource(a.homePath, client, agentID, workspaceID, mb)
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

// auditMail writes one mail panel audit entry (MC-19). A nil auditor — audit
// logging disabled — is a legitimate production state, not a test-only
// shape; it skips silently. Write failures are WARN, never fatal.
func auditMail(a *restAPI, event audit.EventName, decision audit.Decision, details map[string]any) {
	if a.auditor == nil {
		return
	}
	if err := a.auditor.Log(&audit.Entry{
		Event:    string(event),
		Decision: string(decision),
		Details:  details,
	}); err != nil {
		logsafeWarn("audit write failed", "event", event, "error", err)
	}
}

// mailErr502 writes the MC-8 error envelope: the closed error class is the
// only thing that crosses the wire - `error` carries the class string,
// `code` its machine-readable duplicate. w5-integration US-7.4/MC-15: the
// same closed class is the only thing that reaches the LOG — the raw
// provider error value never does (it can carry subjects, addresses, folder
// names, Message-IDs, credentials, full URLs or raw server responses).
func mailErr502(w http.ResponseWriter, err error) {
	class := email.ClassifyMailError(err)
	logsafeError("rest: mail upstream failure", "class", class)
	jsonErrCode(w, http.StatusBadGateway, "mail server error: "+class, class)
}
