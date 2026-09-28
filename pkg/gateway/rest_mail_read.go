package gateway

// Read-side Mail panel handlers (email-mail-view-spec 2.3): the folder
// counts, the envelope page, one message, seen, and attachments. Every
// wire type comes from pkg/api/generated.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// pageResult bundles ReadFolderPage's multi-value result so a coalesced
// budget flight can share it as one value.
type pageResult struct {
	rows      []email.MailRow
	uv        uint32
	truncated bool
}

// mailNonNilSlice preserves populated slices and turns a nil slice into the
// empty JSON array required by the mail response contracts.
func mailNonNilSlice[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func (a *restAPI) handleMailFolders(w http.ResponseWriter, r *http.Request, workspaceID, agentID string) {
	client := a.mailPairClient(w, agentID, workspaceID)
	if client == nil {
		return
	}
	stats, handled := mailBudgetWrap(a, w, r, agentID, workspaceID, client, "listMailFolders",
		map[string]any{"scope": "folders"}, func(c context.Context) ([]email.FolderStat, error) {
			return client.FolderCounts(c)
		})
	if handled {
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
	pr, handled := mailBudgetWrap(a, w, r, agentID, workspaceID, client, "listMailMessages",
		map[string]any{"folder": folder, "limit": limit, "before_uid": beforeUID}, func(c context.Context) (pageResult, error) {
			rows, uv, truncated, err := client.ReadFolderPage(c, folder, limit, beforeUID)
			return pageResult{rows: rows, uv: uv, truncated: truncated}, err
		})
	if handled {
		return
	}
	rows, uv, truncated := pr.rows, pr.uv, pr.truncated
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
			Cc:             mailNonNilSlice(row.Cc),
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
			To:             mailNonNilSlice(row.To),
			Uid:            int(row.UID),
			Uidvalidity:    int(uv),
		})
	}
	out.Messages = mailNonNilSlice(out.Messages)
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
	mv, handled := mailBudgetWrap(a, w, r, agentID, workspaceID, client, "getMailMessage",
		map[string]any{"folder": folder, "ref": ref}, func(c context.Context) (*email.MailView, error) {
			return client.ReadView(c, folder, ref)
		})
	if handled {
		return
	}
	v := mv
	out := gen.MailMessage{
		Cc:             mailNonNilSlice(v.Cc),
		Date:           v.Date,
		Folder:         gen.MailMessageFolder(folder),
		From:           v.From,
		HasHtml:        v.HasHTML,
		IsOmnipusDraft: v.IsOmnipusDraft,
		MarkdownLossy:  v.MarkdownLossy,
		Subject:        v.Subject,
		To:             mailNonNilSlice(v.To),
		Uid:            int(v.UID),
		Uidvalidity:    int(v.UIDValidity),
		BodyText:       v.TextBody,
		Seen:           mailFlagHas(v.Flags, "\\Seen"),
		ReadByAgent:    mailFlagHas(v.Flags, "$OmnipusAgentRead"),
	}
	mid := v.MessageID
	if mid != "" {
		out.MessageId = &mid
	}
	out.IsDraft = mailFlagHas(v.Flags, "\\Draft")
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
		bcc := mailNonNilSlice(v.Bcc)
		out.Bcc = &bcc
	}
	for _, p := range v.Attachments {
		if mailViewDraftBodyPart(p) {
			// The draft's own body bookkeeping part (the X-Omnipus-Part:
			// draft-body marker header — never the name/type pair) is Omnipus
			// bookkeeping, not a user attachment: never listed, on any
			// folder's read surface. A genuine user message.md is an ordinary
			// attachment and stays listed.
			continue
		}
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
	out.Attachments = mailNonNilSlice(out.Attachments)
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

// mailPartByStableIndex returns the view's attachment part whose stable
// PartIndex equals idx, or nil when none does — the download route's
// addressing rule (the {partIndex} path parameter's meaning), shared with
// the draft keep paths so one scheme answers every part reference.
func mailPartByStableIndex(atts []email.MailPart, idx int) *email.MailPart {
	for i := range atts {
		if atts[i].PartIndex == idx {
			return &atts[i]
		}
	}
	return nil
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
	mv, handled := mailBudgetWrap(a, w, r, agentID, workspaceID, client, "getMailAttachment",
		map[string]any{"folder": folder, "ref": ref, "idx": idx}, func(c context.Context) (*email.MailView, error) {
			return client.ReadView(c, folder, ref)
		})
	if handled {
		return
	}
	v := mv
	part := mailPartByStableIndex(v.Attachments, idx)
	if part == nil {
		jsonErr(w, http.StatusNotFound, "no such attachment part")
		return
	}
	if part.DataUnavailable {
		jsonErr(w, http.StatusRequestEntityTooLarge, "attachment part unavailable: over the 25 MiB per-part fetch cap or failed to decode")
		return
	}
	name := email.SanitizeAttachmentName(part.Filename)
	if name == "" {
		name = "attachment"
	}
	var ctype string
	// MC-42 (Content-Type discipline): the type is derived from the sanitized
	// filename's EXTENSION through the compiled-in fixed table (library_mime.go)
	// — never from the host MIME registry (mime.TypeByExtension answers
	// differently per machine, FR-015b) and not from the part's self-declared
	// type when the table knows the extension. Unknown extension → the part's
	// declared type if any, else the one stated default.
	if ct, ok := libraryExtContentTypes[libraryExtOf(name)]; ok {
		ctype = ct
	} else if part.ContentType != "" {
		ctype = part.ContentType
	} else {
		ctype = "application/octet-stream"
	}
	// MC-42: an .html part is never inline. The headers go through the shared
	// inline_serving.go helper: FR-008c's source gate allows the disposition
	// policy to be written in exactly one place in this package.
	applyMailByteHeaders(w, ctype, name)
	w.WriteHeader(http.StatusOK)
	_, werr := w.Write(part.Data)
	if werr != nil {
		slog.Error("rest: mail attachment write failed", "error", werr)
	}
}
