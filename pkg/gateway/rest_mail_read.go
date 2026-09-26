package gateway

// Read-side Mail panel handlers (email-mail-view-spec 2.3): the folder
// counts, the envelope page, one message, seen, and attachments. Every
// wire type comes from pkg/api/generated.

import (
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

func (a *restAPI) handleMailFolders(w http.ResponseWriter, r *http.Request, workspaceID, agentID string) {
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	stats, err := client.FolderCounts(r.Context())
	if err != nil {
		mailErr502(w, err)
		return
	}
	var out gen.MailFolderList
	for _, s := range stats {
		var unread *int
		if s.Slug == email.FolderInbox {
			u := s.Unseen
			unread = &u
		}
		out.Folders = append(out.Folders, struct {
			DisplayName string                        `json:"display_name"`
			Slug        gen.MailFolderListFoldersSlug `json:"slug"`
			Total       int                           `json:"total"`
			UnreadCount *int                          `json:"unread_count"`
		}{
			DisplayName: s.DisplayName,
			Slug:        gen.MailFolderListFoldersSlug(s.Slug),
			Total:       s.Total,
			UnreadCount: unread,
		})
	}
	jsonOK(w, out)
}

func (a *restAPI) handleMailList(w http.ResponseWriter, r *http.Request, workspaceID, agentID, folder string) {
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "malformed limit")
			return
		}
		limit = v
	}
	var beforeUID uint32
	if raw := r.URL.Query().Get("before_uid"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "malformed before_uid")
			return
		}
		beforeUID = uint32(v)
	}
	rows, uv, truncated, err := client.ReadFolderPage(r.Context(), folder, limit, beforeUID)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	var out gen.MailMessagePage
	out.Truncated = truncated
	if truncated && len(rows) > 0 {
		c := int(rows[len(rows)-1].UID)
		out.NextBeforeUid = &c
	}
	for _, row := range rows {
		mid := row.MessageID
		var midPtr *string
		if mid != "" {
			midPtr = &mid
		}
		fn := row.FromName
		var fnPtr *string
		if fn != "" {
			fnPtr = &fn
		}
		out.Messages = append(out.Messages, struct {
			Cc             []string                          `json:"cc"`
			Date           time.Time                         `json:"date"`
			Folder         gen.MailMessagePageMessagesFolder `json:"folder"`
			From           string                            `json:"from"`
			FromName       *string                           `json:"from_name"`
			IsDraft        bool                              `json:"is_draft"`
			IsOmnipusDraft bool                              `json:"is_omnipus_draft"`
			MessageId      *string                           `json:"message_id"`
			ReadByAgent    bool                              `json:"read_by_agent"`
			Seen           bool                              `json:"seen"`
			Subject        string                            `json:"subject"`
			To             []string                          `json:"to"`
			Uid            int                               `json:"uid"`
			Uidvalidity    int                               `json:"uidvalidity"`
		}{
			Cc:             row.Cc,
			Date:           row.Date,
			Folder:         gen.MailMessagePageMessagesFolder(folder),
			From:           row.From,
			FromName:       fnPtr,
			IsDraft:        row.IsDraft,
			IsOmnipusDraft: row.IsOmnipusDraft,
			MessageId:      midPtr,
			ReadByAgent:    row.ReadByAgent,
			Seen:           row.Seen,
			Subject:        row.Subject,
			To:             row.To,
			Uid:            int(row.UID),
			Uidvalidity:    int(uv),
		})
	}
	jsonOK(w, out)
}

// mailFlagHas reports whether an IMAP flag list carries a flag (keyword
// case is server-dependent, so match case-insensitively).
func mailFlagHas(flags []string, name string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, name) {
			return true
		}
	}
	return false
}

func (a *restAPI) handleMailFolderMessage(w http.ResponseWriter, r *http.Request, workspaceID, agentID, folder, ref string) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	v, err := client.ReadView(r.Context(), folder, ref)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	out := gen.MailMessage{
		Cc:             v.Cc,
		Date:           v.Date,
		Folder:         gen.MailMessageFolder(folder),
		From:           v.From,
		HasHtml:        v.HasHTML,
		IsOmnipusDraft: v.IsOmnipusDraft,
		MarkdownLossy:  v.MarkdownLossy,
		Subject:        v.Subject,
		To:             v.To,
		Uid:            int(v.UID),
		Uidvalidity:    int(v.UIDValidity),
		BodyText:       v.TextBody,
		Seen:           mailFlagHas(v.Flags, "\\Seen"),
		ReadByAgent:    mailFlagHas(v.Flags, "$OmnipusAgentRead"),
	}
	if v.FromName != "" {
		fn := v.FromName
		out.FromName = &fn
	}
	if v.InReplyTo != "" {
		irt := v.InReplyTo
		out.InReplyTo = &irt
	}
	if v.References != "" {
		refv := v.References
		out.References = &refv
	}
	if v.ReplyTo != "" {
		rt := v.ReplyTo
		out.ReplyTo = &rt
	}
	if v.BodyMarkdown != "" {
		bm := v.BodyMarkdown
		out.BodyMarkdown = &bm
	}
	if folder == email.FolderDrafts || folder == email.FolderSent {
		bcc := v.Bcc
		out.Bcc = &bcc
	}
	for _, p := range v.Attachments {
		out.Attachments = append(out.Attachments, struct {
			ContentType string `json:"content_type"`
			Filename    string `json:"filename"`
			PartIndex   int    `json:"part_index"`
			SizeBytes   int    `json:"size_bytes"`
		}{
			ContentType: p.ContentType,
			Filename:    p.Filename,
			PartIndex:   p.PartIndex,
			SizeBytes:   p.SizeBytes,
		})
	}
	jsonOK(w, out)
}

func (a *restAPI) handleMailSeen(w http.ResponseWriter, r *http.Request, workspaceID, agentID, folder, ref string) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	_, uid, err := client.ResolveRef(r.Context(), folder, ref)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	if err := client.MarkSeenIn(r.Context(), folder, uid); err != nil {
		mailErr502(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) handleMailAttachment(w http.ResponseWriter, r *http.Request, workspaceID, agentID, folder, ref string, idx int) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	v, err := client.ReadView(r.Context(), folder, ref)
	if err != nil {
		if errors.Is(err, email.ErrMailRefInvalid) {
			jsonErr(w, http.StatusBadRequest, err.Error())
		} else {
			mailErr502(w, err)
		}
		return
	}
	var part *email.MailPart
	for i := range v.Attachments {
		if v.Attachments[i].PartIndex == idx {
			part = &v.Attachments[i]
			break
		}
	}
	if part == nil {
		jsonErr(w, http.StatusNotFound, "no such attachment part")
		return
	}
	name := email.SanitizeAttachmentName(part.Filename)
	if name == "" {
		name = "attachment"
	}
	ctype := part.ContentType
	if ctype == "" {
		ctype = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, werr := w.Write(part.Data)
	if werr != nil {
		slog.Error("rest: mail attachment write failed", "error", werr)
	}
}
