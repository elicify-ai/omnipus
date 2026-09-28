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
		// FR-035/D28: keep values are each attachment's own stable PartIndex
		// (the download route's {partIndex}) — matched by search, never by
		// listing position (rest_mail_read.go::mailPartByStableIndex).
		p := mailPartByStableIndex(cur.Attachments, idx)
		if p == nil {
			jsonErr(w, http.StatusBadRequest, "keep_attachment_parts names no such part")
			return
		}
		att, carry, err := mailCarryAttachment(*p)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if !carry {
			continue
		}
		in.Attachments = append(in.Attachments, att)
	}
	for _, at := range derefAttSlice(req.Attachments) {
		ct := at.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		in.Attachments = append(in.Attachments, email.Attachment{Name: email.SanitizeAttachmentName(at.Filename), ContentType: ct, Data: at.DataBase64})
	}
	// MC-32 (same trio as the manual send): the carried-over parts count toward
	// the same caps as the new uploads — count first, then decoded total.
	if len(in.Attachments) > mailMaxAttachments {
		jsonErr(w, http.StatusBadRequest, "too many attachments (max 10)")
		return
	}
	totalAtt := 0
	for _, at := range in.Attachments {
		totalAtt += len(at.Data)
	}
	if totalAtt > mailMaxAttachmentBytes {
		jsonErr(w, http.StatusBadRequest, "attachments too large (max 25 MiB total)")
		return
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
	// MAJ-009/MC-28 (same mechanism as the send path below): a failed
	// old-copy delete after the successful update-APPEND is a warning on the
	// response, never an error — the new copy exists; the panel warns of a
	// possible duplicate draft.
	cleanupWarn := ""
	if derr := client.DeleteDraft(r.Context(), cur.UID); derr != nil {
		cleanupWarn = "the draft was updated, but deleting the old copy failed"
		slog.Warn("rest: old draft copy delete failed", "agent_id", agentID, "error", derr)
	}
	draftSendForget(out.MessageID)
	auditMail(a, audit.EventMailPanelDraftUpdated, audit.DecisionAllow, map[string]any{
		"workspace_id": workspaceID, "agent_id": agentID,
		"message_id": out.MessageID, "attachment_count": len(in.Attachments),
		"uid": newUID, "uidvalidity": newUV,
		// MC-19/D46 full fields.
		"recipients":  mailAuditRecipients(req.To, derefStrings(req.Cc), derefStrings(req.Bcc)),
		"origin":      mailDraftOrigin(cur.IsOmnipusDraft),
		"arg_hash":    mailAuditArgHash(req),
		"folder":      client.DraftsFolderName(),
		"attachments": mailAuditAttachments(in.Attachments),
	})
	resp := gen.MailMessage{
		Folder: gen.MailMessageFolderDrafts, Uid: int64(newUID), Uidvalidity: int64(newUV),
		Subject: req.Subject, To: mailNonNilSlice(req.To), Cc: mailNonNilSlice(derefStrings(req.Cc)),
		From: mb.Username, IsOmnipusDraft: cur.IsOmnipusDraft, IsDraft: true,
		MarkdownLossy: false, HasHtml: true, Seen: false,
		BodyText: req.BodyMarkdown, MessageId: &out.MessageID,
	}
	bm := req.BodyMarkdown
	resp.BodyMarkdown = &bm
	bcc := mailNonNilSlice(derefStrings(req.Bcc))
	resp.Bcc = &bcc
	// The listing describes the NEW copy the response returns (ruling: its
	// uid is in this response; download links are built from it). Parse the
	// just-composed wire bytes so every entry reports the same stable
	// PartIndex field the download route later serves the appended copy at
	// (APPEND stores verbatim; the walk is the view parser's own). The
	// draft's own body bookkeeping part is never listed.
	newView := email.ParseViewRaw(out.Transmitted)
	for _, p := range newView.Attachments {
		if mailViewDraftBodyPart(p) {
			continue
		}
		resp.Attachments = append(resp.Attachments, struct {
			ContentType string `json:"content_type"`
			Filename    string `json:"filename"`
			PartIndex   int    `json:"part_index"`
			SizeBytes   int    `json:"size_bytes"`
		}{ContentType: p.ContentType, Filename: p.Filename, PartIndex: p.PartIndex, SizeBytes: p.SizeBytes})
	}
	resp.Attachments = mailNonNilSlice(resp.Attachments)
	if cleanupWarn != "" {
		resp.DraftCleanupWarning = &cleanupWarn
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
	expunged, derr := client.DeleteDraftStatus(r.Context(), uid)
	if derr != nil {
		mailErr502(w, derr)
		return
	}
	draftSendForget(bracketMessageID(cur.MessageID))
	auditMail(a, audit.EventMailPanelDraftDiscarded, audit.DecisionAllow, map[string]any{
		"workspace_id": workspaceID, "agent_id": agentID,
		"message_id": bracketMessageID(cur.MessageID), "uid": uid, "uidvalidity": uv,
		"expunged": expunged,
		// MC-19/D46 full fields. A discard has no request args: the
		// recipients are the draft's own stored addresses — the COMPLETE
		// address list incl. Cc/Bcc (D46), via the shared helper every other
		// MC-19 audit call site uses.
		"recipients": mailAuditRecipients(cur.To, cur.Cc, cur.Bcc),
		"origin":     mailDraftOrigin(cur.IsOmnipusDraft),
		"arg_hash":   mailAuditArgHash(nil),
		"folder":     client.DraftsFolderName(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// imapDraftFlag is the IMAP \Draft flag.
const imapDraftFlag = "\\Draft"

// mailDraftRefBodyAgreement is the MAJ-008 PRE-DIAL check: a uid-form path
// ref whose parts disagree with the request body is malformed input (400)
// before any IMAP work. Non-uid refs (mid:) have no path parts to agree on.
// Returns ("", true) when the request may proceed.
func mailDraftRefBodyAgreement(ref string, bodyUID, bodyUV int64) (string, bool) {
	if !mailDraftUIDPreconditionsValid(bodyUID, bodyUV) {
		return "uid precondition is outside the IMAP uint32 range", false
	}
	if !strings.HasPrefix(ref, "uid:") {
		return "", true
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 3 {
		return "", true
	}
	ru, e1 := strconv.ParseUint(parts[1], 10, 32)
	ui, e2 := strconv.ParseUint(parts[2], 10, 32)
	if e1 == nil && int64(ru) != bodyUV {
		return "uid precondition does not match the addressed draft", false
	}
	if e2 == nil && int64(ui) != bodyUID {
		return "uid precondition does not match the addressed draft", false
	}
	return "", true
}

func mailDraftUIDPreconditionsValid(bodyUID, bodyUV int64) bool {
	const maxUint32 = int64(^uint32(0))
	return bodyUID >= 0 && bodyUID <= maxUint32 && bodyUV >= 0 && bodyUV <= maxUint32
}

// mailDraftStaleness is the MC-16 POST-READ check: a body describing
// anything other than the CURRENT copy is stale. Returns (409,
// "stale_draft", ...) so callers emit the closed code on the wire.
func mailDraftStaleness(cur *email.MailView, bodyUID, bodyUV int64) (int, string, string) {
	if !mailDraftUIDPreconditionsValid(bodyUID, bodyUV) {
		return http.StatusBadRequest, "", "uid precondition is outside the IMAP uint32 range"
	}
	if bodyUV != int64(cur.UIDValidity) || bodyUID != int64(cur.UID) {
		return http.StatusConflict, "stale_draft", "stale draft"
	}
	return 0, "", ""
}

// mailCarryAttachment converts one draft view part into the attachment one
// carry path carries forward — the single availability check shared by the
// draft update's explicit keep loop and the send path's default carry-all and
// explicit keep loops. Its carry result is false for the draft's own body
// bookkeeping part (mailViewDraftBodyPart — the X-Omnipus-Part: draft-body
// marker header, never the name/type pair): skipped silently, never a
// rejection, even when a keep list names its own stable index. It returns a
// non-nil error for a DataUnavailable part: the caller answers 400 with the
// static message, PRE-DIAL, so a listed-but-unavailable part is never
// transmitted as an empty attachment — on the default carry-all path exactly
// as on the explicit keep path.
func mailCarryAttachment(p email.MailPart) (email.Attachment, bool, error) {
	if mailViewDraftBodyPart(p) {
		return email.Attachment{}, false, nil
	}
	if p.DataUnavailable {
		return email.Attachment{}, false, errors.New("keep_attachment_parts names an unavailable part")
	}
	ct := p.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	return email.Attachment{Name: p.Filename, ContentType: ct, Data: p.Data}, true, nil
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
	// MC-27 (pre-dial): the contract documents recipient validation as a 400,
	// and >50 recipients previously fell through to the transport layer and
	// surfaced as a 502 server_error. Same check and message as the manual
	// send path.
	if totalRcpt := len(to) + len(cc) + len(derefStrings(req.Bcc)); totalRcpt > mailMaxRecipients {
		jsonErr(w, http.StatusBadRequest, "too many recipients (max 50 across to, cc, bcc)")
		return
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
		// Contract default: carry ALL — the draft's own body bookkeeping part
		// (the marker header) is not an attachment and is skipped silently
		// and unconditionally; an unavailable part refuses the whole send
		// exactly like the explicit keep path (pre-dial 400), never an empty
		// attachment on the wire.
		for _, p := range cur.Attachments {
			att, carry, err := mailCarryAttachment(p)
			if err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if !carry {
				continue
			}
			in.Attachments = append(in.Attachments, att)
		}
	} else {
		for _, idx := range keepParts {
			// FR-035/D28: keep values are each attachment's own stable
			// PartIndex (the download route's {partIndex}) — matched by
			// search, never by listing position.
			p := mailPartByStableIndex(cur.Attachments, idx)
			if p == nil {
				jsonErr(w, http.StatusBadRequest, "keep_attachment_parts names no such part")
				return
			}
			att, carry, err := mailCarryAttachment(*p)
			if err != nil {
				jsonErr(w, http.StatusBadRequest, err.Error())
				return
			}
			if !carry {
				continue
			}
			in.Attachments = append(in.Attachments, att)
		}
	}
	// MC-32 (same trio as the manual send): carried-over parts count toward
	// the same caps as any new attachments — count first, then decoded total.
	if len(in.Attachments) > mailMaxAttachments {
		jsonErr(w, http.StatusBadRequest, "too many attachments (max 10)")
		return
	}
	totalAtt := 0
	for _, at := range in.Attachments {
		totalAtt += len(at.Data)
	}
	if totalAtt > mailMaxAttachmentBytes {
		jsonErr(w, http.StatusBadRequest, "attachments too large (max 25 MiB total)")
		return
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
	expunged, derr := client.DeleteDraftStatus(r.Context(), cur.UID)
	if derr != nil {
		cleanupWarn = "the message was sent, but deleting the draft copy failed"
		slog.Warn("rest: draft cleanup after send failed", "agent_id", agentID, "error", derr)
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
		// MC-19/D46 full fields.
		"recipients":  mailAuditRecipients(to, cc, derefStrings(req.Bcc)),
		"origin":      mailDraftOrigin(cur.IsOmnipusDraft),
		"arg_hash":    mailAuditArgHash(req),
		"folder":      client.DraftsFolderName(),
		"attachments": mailAuditAttachments(in.Attachments),
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
