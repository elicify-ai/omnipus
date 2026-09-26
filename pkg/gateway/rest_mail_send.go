package gateway

// rest_mail_send.go - the Mail panel manual send (spec 2.3 POST
// /workspaces/{id}/mail/{agentId}/messages, D9). Compose is pure; the dial
// happens here. Caps are enforced before dialing: 1 MiB markdown body
// (MC-22), 10 files / 25 MiB decoded attachments (MC-32), 50 recipients
// (MC-27). All wire types are generated.

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailMaxBodyBytes is MC-22: the outbound markdown body cap.
const mailMaxBodyBytes = 1 << 20

// mailMaxAttachmentBytes is MC-32: the total decoded attachment cap.
const mailMaxAttachmentBytes = 25 << 20

// mailMaxAttachments is MC-32: the attachment count cap.
const mailMaxAttachments = 10

// mailMaxRecipients is MC-27: the total recipient cap across To/Cc/Bcc.
const mailMaxRecipients = 50

// mailWireBodyCap allows 25 MiB of base64 attachments to travel in one JSON
// body (base64 inflates by 4/3) with headroom.
const mailWireBodyCap = 36 << 20

// decodeMailJSON decodes and schema-validates one mail mutation body. The
// shared decodeAndValidate caps bodies at 1 MiB, which would reject
// legitimate attachment payloads, so mail uses its own cap.
func decodeMailJSON(a *restAPI, w http.ResponseWriter, r *http.Request, schemaName string, dst any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, mailWireBodyCap))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	if len(raw) >= mailWireBodyCap {
		jsonErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return false
	}
	if a.agentLoop.GetConfig().Gateway.ValidateInbound { // a is the receiver param
		if msg, serverErr := validateBodyAgainstSchema(schemaName, raw); serverErr {
			jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
			return false
		} else if msg != "" {
			jsonErr(w, http.StatusBadRequest, "request body does not match schema "+schemaName+": "+msg)
			return false
		}
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (a *restAPI) handleMailSend(w http.ResponseWriter, r *http.Request, workspaceID, agentID string) {
	// MC-20: the closure is INVOKED - the limiter wraps the inner handler.
	withRateLimit(mailMutationLimiter, func(w http.ResponseWriter, r *http.Request) {
		a.handleMailSendInner(w, r, workspaceID, agentID)
	})(w, r)
}

func (a *restAPI) handleMailSendInner(w http.ResponseWriter, r *http.Request, workspaceID, agentID string) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req gen.MailSendRequest
	if !decodeMailJSON(a, w, r, "MailSendRequest", &req) {
		return
	}
	if len(req.To) == 0 {
		jsonErr(w, http.StatusBadRequest, "to is required")
		return
	}
	total := len(req.To) + len(derefStrings(req.Cc)) + len(derefStrings(req.Bcc))
	if total > mailMaxRecipients {
		jsonErr(w, http.StatusBadRequest, "too many recipients (max 50 across to, cc, bcc)")
		return
	}
	if len(req.BodyMarkdown) > mailMaxBodyBytes {
		jsonErr(w, http.StatusBadRequest, "body too large (max 1 MiB)")
		return
	}
	atts := derefAttSlice(req.Attachments)
	if len(atts) > mailMaxAttachments {
		jsonErr(w, http.StatusBadRequest, "too many attachments (max 10)")
		return
	}
	totalAtt := 0
	for _, at := range atts {
		totalAtt += len(at.DataBase64)
	}
	if totalAtt > mailMaxAttachmentBytes {
		jsonErr(w, http.StatusBadRequest, "attachments too large (max 25 MiB total)")
		return
	}
	mb, ok := a.mailComposeConfig(agentID, workspaceID)
	if !ok {
		jsonErr(w, http.StatusNotFound, "no mailbox configured for this agent and workspace")
		return
	}
	in := email.ComposeInput{
		From:          mb.Username,
		To:            req.To,
		Cc:            derefStrings(req.Cc),
		Bcc:           derefStrings(req.Bcc),
		Subject:       req.Subject,
		Markdown:      req.BodyMarkdown,
		SignatureHTML: mb.SignatureHTML,
	}
	for _, at := range atts {
		ct := at.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		in.Attachments = append(in.Attachments, email.Attachment{
			Name:        at.Filename,
			ContentType: ct,
			Data:        at.DataBase64,
		})
	}
	if req.InReplyTo != nil && *req.InReplyTo != "" {
		in.InReplyTo = *req.InReplyTo
	}
	out, cerr := email.Compose(in)
	if cerr != nil {
		jsonErr(w, http.StatusBadRequest, cerr.Error())
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	if err := client.Send(r.Context(), email.SendRequest{
		To:      strings.Join(out.EnvelopeRecipients, ", "),
		Subject: req.Subject,
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
		slog.Error("rest: mail sent copy APPEND failed", "agent_id", agentID, "error", aerr)
	}
	attCount := len(atts)
	auditMail(a, audit.EventMailPanelSend, audit.DecisionAllow, map[string]any{
		"workspace_id": workspaceID, "agent_id": agentID,
		"message_id": out.MessageID, "recipients_count": total,
		"attachment_count": attCount, "sent_saved": resp.SentSaved,
	})
	jsonOK(w, resp)
}

// derefStrings flattens an optional string list.
func derefStrings(list *[]string) []string {
	if list == nil {
		return nil
	}
	return *list
}

// derefAttSlice flattens an optional attachment list.
func derefAttSlice(list *[]struct {
	ContentType string `json:"content_type"`
	DataBase64  []byte `json:"data_base64"`
	Filename    string `json:"filename"`
}) []struct {
	ContentType string `json:"content_type"`
	DataBase64  []byte `json:"data_base64"`
	Filename    string `json:"filename"`
} {
	if list == nil {
		return nil
	}
	return *list
}
