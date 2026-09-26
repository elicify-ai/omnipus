package gateway

// rest_mail_draft.go - panel edit, discard and send of one draft (spec 2.3).
// Edits re-APPEND a new copy with the SAME Message-ID and \Deleted-flag the
// old one (FR-032); carries attachments by part reference (FR-035, D28).
// Panel send is idempotent per draft revision (in-memory, restart loses the
// records - the spec's in-memory token-store precedent).

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// draftSendTTL bounds how long one draft's first-send outcome stays
// replayable. Matches the Library preview-token TTL precedent (15 min).
const draftSendTTL = 15 * time.Minute

// draftSendRecord remembers the outcome of the first send of one draft
// revision, keyed on the draft's Message-ID (with angle brackets).
type draftSendRecord struct {
	sentSaved   bool
	saveWarning string
	cleanupWarn string
	expunged    bool
	messageID   string
	recordedAt  time.Time
}

var (
	draftSendMu      sync.Mutex
	draftSendRecords = map[string]draftSendRecord{}
)

// draftSendLookup returns the still-fresh record for one Message-ID, if any.
func draftSendLookup(messageID string) (draftSendRecord, bool) {
	draftSendMu.Lock()
	defer draftSendMu.Unlock()
	for k, v := range draftSendRecords {
		if time.Since(v.recordedAt) > draftSendTTL {
			delete(draftSendRecords, k)
		}
	}
	rec, ok := draftSendRecords[messageID]
	return rec, ok
}

// draftSendForget drops any record for one Message-ID: an edited or
// discarded draft is a NEW send, never a replay.
func draftSendForget(messageID string) {
	draftSendMu.Lock()
	defer draftSendMu.Unlock()
	delete(draftSendRecords, messageID)
}

// bracketMessageID renders a normalized (angle-less) Message-ID in mid:
// ref form (angle brackets).
func bracketMessageID(id string) string {
	if strings.HasPrefix(id, "<") {
		return id
	}
	return "<" + id + ">"
}

func (a *restAPI) handleMailDraftAction(w http.ResponseWriter, r *http.Request, workspaceID, agentID, ref string) {
	// MC-20: the closure is INVOKED - the limiter wraps the method dispatch.
	withRateLimit(mailMutationLimiter, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			a.handleMailDraftUpdate(w, r, workspaceID, agentID, ref)
		case http.MethodDelete:
			a.handleMailDraftDiscard(w, r, workspaceID, agentID, ref)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})(w, r)
}

func (a *restAPI) handleMailDraftUpdate(w http.ResponseWriter, r *http.Request, workspaceID, agentID, ref string) {
	var req gen.MailDraftUpdateRequest
	if !decodeMailJSON(a, w, r, "MailDraftUpdateRequest", &req) {
		return
	}
	if msg, ok := mailDraftRefBodyAgreement(ref, req.Uid, req.Uidvalidity); !ok {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	cur, err := client.ReadView(r.Context(), email.FolderDrafts, ref)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	if status, code, msg := mailDraftStaleness(cur, req.Uid, req.Uidvalidity); status != 0 {
		if code != "" {
			jsonErrCode(w, status, msg, code)
		} else {
			jsonErr(w, status, msg)
		}
		return
	}
	mb, mbOK := a.mailComposeConfig(agentID, workspaceID)
	if !mbOK {
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return
	}
	in := email.ComposeInput{
		From:          mb.Username,
		Subject:       req.Subject,
		Markdown:      req.BodyMarkdown,
		To:            req.To,
		Cc:            derefStrings(req.Cc),
		Bcc:           derefStrings(req.Bcc),
		MessageID:     bracketMessageID(cur.MessageID),
		Draft:         cur.IsOmnipusDraft,
		SignatureHTML: mb.SignatureHTML,
	}
	for _, idx := range req.KeepAttachmentParts {
		if idx < 0 || idx >= len(cur.Attachments) {
			jsonErr(w, http.StatusBadRequest, "keep_attachment_parts names no such part")
			return
		}
		p := cur.Attachments[idx]
		ct := p.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		in.Attachments = append(in.Attachments, email.Attachment{Name: p.Filename, ContentType: ct, Data: p.Data})
	}
	for _, at := range derefAttSlice(req.Attachments) {
		ct := at.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		if len(at.DataBase64) > mailMaxAttachmentBytes {
			jsonErr(w, http.StatusBadRequest, "attachments too large (max 25 MiB total)")
			return
		}
		in.Attachments = append(in.Attachments, email.Attachment{Name: at.Filename, ContentType: ct, Data: at.DataBase64})
	}
	out, cerr := email.Compose(in)
	if cerr != nil {
		jsonErr(w, http.StatusBadRequest, cerr.Error())
		return
	}
	flags := []string{imapDraftFlag}
	newUID, newUV, aerr := client.AppendMessage(r.Context(), client.DraftsFolderName(), flags, out.Transmitted)
	if aerr != nil {
		mailErr502(w, aerr)
		return
	}
	if derr := client.DeleteDraft(r.Context(), cur.UID); derr != nil {
		slog.Warn("rest: old draft copy delete failed", "agent_id", agentID, "error", derr)
	}
	draftSendForget(out.MessageID)
	auditMail(a, audit.EventMailPanelDraftUpdated, audit.DecisionAllow, map[string]any{
		"workspace_id": workspaceID, "agent_id": agentID,
		"message_id": out.MessageID, "attachment_count": len(in.Attachments),
		"uid": newUID, "uidvalidity": newUV,
	})
	resp := gen.MailMessage{
		Folder: gen.MailMessageFolderDrafts, Uid: int(newUID), Uidvalidity: int(newUV),
		Subject: req.Subject, To: req.To, Cc: derefStrings(req.Cc),
		From: mb.Username, IsOmnipusDraft: cur.IsOmnipusDraft, IsDraft: true,
		MarkdownLossy: false, HasHtml: true, Seen: false,
		BodyText: req.BodyMarkdown, MessageId: &out.MessageID,
	}
	bm := req.BodyMarkdown
	resp.BodyMarkdown = &bm
	bcc := derefStrings(req.Bcc)
	resp.Bcc = &bcc
	for _, at := range in.Attachments {
		resp.Attachments = append(resp.Attachments, struct {
			ContentType string `json:"content_type"`
			Filename    string `json:"filename"`
			PartIndex   int    `json:"part_index"`
			SizeBytes   int    `json:"size_bytes"`
		}{ContentType: at.ContentType, Filename: at.Name, PartIndex: len(resp.Attachments), SizeBytes: len(at.Data)})
	}
	jsonOK(w, resp)
}

func (a *restAPI) handleMailDraftDiscard(w http.ResponseWriter, r *http.Request, workspaceID, agentID, ref string) {
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	uv, uid, err := client.ResolveRef(r.Context(), email.FolderDrafts, ref)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	cur, err := client.ReadView(r.Context(), email.FolderDrafts, ref)
	if err != nil {
		mailErr502(w, err)
		return
	}
	if derr := client.DeleteDraft(r.Context(), uid); derr != nil {
		mailErr502(w, derr)
		return
	}
	draftSendForget(bracketMessageID(cur.MessageID))
	auditMail(a, audit.EventMailPanelDraftDiscarded, audit.DecisionAllow, map[string]any{
		"workspace_id": workspaceID, "agent_id": agentID,
		"message_id": bracketMessageID(cur.MessageID), "uid": uid, "uidvalidity": uv,
		"expunged": true,
	})
	w.WriteHeader(http.StatusNoContent)
}

// imapDraftFlag is the IMAP \Draft flag.
const imapDraftFlag = "\\Draft"

// mailDraftRefBodyAgreement is the MAJ-008 PRE-DIAL check: a uid-form path
// ref whose parts disagree with the request body is malformed input (400)
// before any IMAP work. Non-uid refs (mid:) have no path parts to agree on.
// Returns ("", true) when the request may proceed.
func mailDraftRefBodyAgreement(ref string, bodyUID, bodyUV int) (string, bool) {
	if !strings.HasPrefix(ref, "uid:") {
		return "", true
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 3 {
		return "", true
	}
	ru, e1 := strconv.Atoi(parts[1])
	ui, e2 := strconv.Atoi(parts[2])
	if e1 == nil && ru != bodyUV {
		return "uid precondition does not match the addressed draft", false
	}
	if e2 == nil && ui != bodyUID {
		return "uid precondition does not match the addressed draft", false
	}
	return "", true
}

// mailDraftStaleness is the MC-16 POST-READ check: a body describing
// anything other than the CURRENT copy is stale. Returns (409,
// "stale_draft", ...) so callers emit the closed code on the wire.
func mailDraftStaleness(cur *email.MailView, bodyUID, bodyUV int) (int, string, string) {
	if bodyUV != int(cur.UIDValidity) || bodyUID != int(cur.UID) {
		return http.StatusConflict, "stale_draft", "stale draft"
	}
	return 0, "", ""
}

func (a *restAPI) handleMailDraftSend(w http.ResponseWriter, r *http.Request, workspaceID, agentID, ref string) {
	// MC-20: the closure is INVOKED - the limiter wraps the method guard.
	withRateLimit(mailMutationLimiter, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleMailDraftSendInner(w, r, workspaceID, agentID, ref)
	})(w, r)
}

func (a *restAPI) handleMailDraftSendInner(w http.ResponseWriter, r *http.Request, workspaceID, agentID, ref string) {
	var req gen.MailDraftSendRequest
	if !decodeMailJSON(a, w, r, "MailDraftSendRequest", &req) {
		return
	}
	if msg, ok := mailDraftRefBodyAgreement(ref, req.Uid, req.Uidvalidity); !ok {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	cur, err := client.ReadView(r.Context(), email.FolderDrafts, ref)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	if status, code, msg := mailDraftStaleness(cur, req.Uid, req.Uidvalidity); status != 0 {
		if code != "" {
			jsonErrCode(w, status, msg, code)
		} else {
			jsonErr(w, status, msg)
		}
		return
	}
	key := bracketMessageID(cur.MessageID)
	if rec, ok := draftSendLookup(key); ok {
		resp := gen.MailSendResponse{MessageId: rec.messageID, SentSaved: rec.sentSaved}
		if rec.saveWarning != "" {
			swr := rec.saveWarning
			resp.SaveWarning = &swr
		}
		if rec.cleanupWarn != "" {
			cwr := rec.cleanupWarn
			resp.DraftCleanupWarning = &cwr
		}
		jsonOK(w, resp)
		return
	}
	subject := cur.Subject
	if req.Subject != "" {
		subject = req.Subject
	}
	markdown := cur.BodyMarkdown
	if req.BodyMarkdown != "" {
		markdown = req.BodyMarkdown
	}
	to := cur.To
	if len(req.To) > 0 {
		to = req.To
	}
	cc := cur.Cc
	if req.Cc != nil {
		cc = *req.Cc
	}
	mb, mbOK := a.mailComposeConfig(agentID, workspaceID)
	if !mbOK {
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return
	}
	in := email.ComposeInput{
		From: mb.Username, To: to, Cc: cc, Bcc: derefStrings(req.Bcc),
		Subject: subject, Markdown: markdown, SignatureHTML: mb.SignatureHTML,
	}
	keepParts := derefInts(req.KeepAttachmentParts)
	if req.KeepAttachmentParts == nil {
		for _, p := range cur.Attachments {
			ct := p.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			in.Attachments = append(in.Attachments, email.Attachment{Name: p.Filename, ContentType: ct, Data: p.Data})
		}
	} else {
		for _, idx := range keepParts {
			if idx < 0 || idx >= len(cur.Attachments) {
				jsonErr(w, http.StatusBadRequest, "keep_attachment_parts names no such part")
				return
			}
			p := cur.Attachments[idx]
			ct := p.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			in.Attachments = append(in.Attachments, email.Attachment{Name: p.Filename, ContentType: ct, Data: p.Data})
		}
	}
	out, cerr := email.Compose(in)
	if cerr != nil {
		jsonErr(w, http.StatusBadRequest, cerr.Error())
		return
	}
	if len(markdown) > mailMaxBodyBytes {
		jsonErr(w, http.StatusBadRequest, "body too large (max 1 MiB)")
		return
	}
	if err := client.Send(r.Context(), email.SendRequest{
		To:      strings.Join(out.EnvelopeRecipients, ", "),
		Subject: subject,
		Raw:     out.Transmitted,
	}); err != nil {
		mailErr502(w, err)
		return
	}
	resp := gen.MailSendResponse{MessageId: out.MessageID, SentSaved: true}
	if _, _, aerr := client.AppendMessage(r.Context(), client.SentFolderName(), nil, out.SentCopy); aerr != nil {
		resp.SentSaved = false
		sw := "the message was sent, but saving to the Sent folder failed"
		resp.SaveWarning = &sw
		slog.Warn("rest: draft sent copy APPEND failed", "agent_id", agentID, "error", aerr)
	}
	cleanupWarn := ""
	expunged := false
	if derr := client.DeleteDraft(r.Context(), cur.UID); derr != nil {
		cleanupWarn = "the message was sent, but deleting the draft copy failed"
		slog.Warn("rest: draft cleanup after send failed", "agent_id", agentID, "error", derr)
	} else {
		expunged = true
	}
	resp.DraftCleanupWarning = nil
	if cleanupWarn != "" {
		resp.DraftCleanupWarning = &cleanupWarn
	}
	draftSendMu.Lock()
	draftSendRecords[key] = draftSendRecord{
		sentSaved: resp.SentSaved, saveWarning: cleanupWarningOf(resp),
		cleanupWarn: cleanupWarn, expunged: expunged,
		messageID: key, recordedAt: time.Now(),
	}
	draftSendMu.Unlock()
	auditMail(a, audit.EventMailPanelDraftSent, audit.DecisionAllow, map[string]any{
		"workspace_id": workspaceID, "agent_id": agentID,
		"message_id": key, "recipients_count": len(to) + len(cc) + len(derefStrings(req.Bcc)),
		"attachment_count": len(in.Attachments), "sent_saved": resp.SentSaved,
		"draft_cleanup_warning": cleanupWarn != "", "expunged": expunged,
	})
	jsonOK(w, resp)
}

// derefInts flattens an optional int list.
func derefInts(list *[]int) []int {
	if list == nil {
		return nil
	}
	return *list
}

// cleanupWarningOf extracts the cleanup warning text for the record.
func cleanupWarningOf(resp gen.MailSendResponse) string {
	if resp.DraftCleanupWarning != nil {
		return *resp.DraftCleanupWarning
	}
	return ""
}
