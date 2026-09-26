package email

// Folder/ref/read surface behind the Mail panel REST endpoints
// (email-mail-view-spec §2.3/§2.3a). Everything here is a live, read-only
// IMAP read over one (agent, workspace) mailbox: counts, envelope pages,
// full-message views (BODY.PEEK everywhere), \Seen writes only through the
// explicit panel endpoint, and the draft-expunge helper. Nothing is stored
// locally (D6); raw upstream text stays in error text server-side only.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	gomail "github.com/emersion/go-message/mail"
)

// The three D5 folder slugs (MC-5): the closed set every folder parameter
// accepts; anything else is a caller error, never a folder name.
const (
	FolderInbox  = "inbox"
	FolderSent   = "sent"
	FolderDrafts = "drafts"
)

// FolderSlugs is the closed slug set in panel tab order.
var FolderSlugs = []string{FolderInbox, FolderSent, FolderDrafts}

// ErrMailRefInvalid marks a malformed/overlong message ref (uid:/mid:) — the
// handler's 400 class, distinct from a well-formed ref that names no message.
var ErrMailRefInvalid = errors.New("invalid message ref")

// FolderStat is one folder's live STATUS.
type FolderStat struct {
	Slug        string
	DisplayName string
	Total       int
	Unseen      int
	UIDValidity uint32
}

// folderNameFor maps a slug to the mailbox's real folder name (the §2.1
// sent_folder_name / drafts_folder_name overrides apply; INBOX is fixed).
func (c *Client) folderNameFor(slug string) (string, error) {
	switch slug {
	case FolderInbox:
		return "INBOX", nil
	case FolderSent:
		return c.acct.withDefaults().sentFolder(), nil
	case FolderDrafts:
		return c.acct.withDefaults().draftsFolder(), nil
	}
	return "", fmt.Errorf("%w: unknown folder slug %q", ErrMailRefInvalid, slug)
}

// ClassifyMailError maps an upstream mail failure to the closed 502 error
// class of §2.3: timeout | dns | connect_refused | auth_failed | tls |
// folder_missing | server_error (MC-8). Raw upstream text is logged
// server-side only; the class is the only thing that crosses the wire.
func ClassifyMailError(err error) string { return classifyMailError(err) }

// FolderCounts STATUSes the three D5 folders (inbox, sent, drafts — with the
// §2.1 overrides) and returns their live counts, unseen (Inbox carries it;
// the others report what the server says) and UIDVALIDITY. One IMAP session
// for the whole request (round-1 MAJ-012).
func (c *Client) FolderCounts(ctx context.Context) ([]FolderStat, error) {
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	out := make([]FolderStat, 0, len(FolderSlugs))
	for _, slug := range FolderSlugs {
		name, ferr := c.folderNameFor(slug)
		if ferr != nil {
			return nil, ferr
		}
		status, serr := runIMAP(ctx, "status "+slug, func() (*imap.StatusData, error) {
			return client.Status(name, &imap.StatusOptions{NumMessages: true, NumUnseen: true, UIDValidity: true}).Wait()
		})
		if serr != nil {
			return nil, fmt.Errorf("email transport: status %s (%s): %w", slug, name, serr)
		}
		st := FolderStat{Slug: slug, DisplayName: name, UIDValidity: status.UIDValidity}
		if status.NumMessages != nil {
			st.Total = int(*status.NumMessages)
		}
		if status.NumUnseen != nil {
			st.Unseen = int(*status.NumUnseen)
		}
		out = append(out, st)
	}
	return out, nil
}

// mailRef is a parsed folder-scoped message reference (§2.3, D21): either
// uid:<uidvalidity>:<uid> — always resolvable within its epoch — or
// mid:<Message-ID> — stable across UID renumbering.
type mailRef struct {
	kind        string // "uid" or "mid"
	uidvalidity uint32
	uid         uint32
	messageID   string
}

// parseMailRef validates a ref per the §2.3 grammar. mid: Message-IDs are
// bounded (<…@…> shape, ≤ 998 bytes, no CR/LF) because the value feeds an
// IMAP SEARCH string — bounds on shape and length are the defense (MC-4
// sibling for the IMAP side).
func parseMailRef(ref string) (mailRef, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return mailRef{}, fmt.Errorf("%w: ref is required", ErrMailRefInvalid)
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return mailRef{}, fmt.Errorf("%w: CR/LF in ref", ErrMailRefInvalid)
	}
	if len(trimmed) > 1100 {
		return mailRef{}, fmt.Errorf("%w: ref too long", ErrMailRefInvalid)
	}
	switch {
	case strings.HasPrefix(trimmed, "uid:"):
		rest := strings.TrimPrefix(trimmed, "uid:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return mailRef{}, fmt.Errorf("%w: uid ref must be uid:<uidvalidity>:<uid>", ErrMailRefInvalid)
		}
		v, err1 := strconv.ParseUint(parts[0], 10, 32)
		u, err2 := strconv.ParseUint(parts[1], 10, 32)
		if err1 != nil || err2 != nil || u == 0 {
			return mailRef{}, fmt.Errorf("%w: uid ref must carry numeric uidvalidity and a positive uid", ErrMailRefInvalid)
		}
		return mailRef{kind: "uid", uidvalidity: uint32(v), uid: uint32(u)}, nil
	case strings.HasPrefix(trimmed, "mid:"):
		mid := strings.TrimPrefix(trimmed, "mid:")
		if len(mid) == 0 || len(mid) > 998 || !strings.HasPrefix(mid, "<") || !strings.HasSuffix(mid, ">") || !strings.Contains(mid, "@") {
			return mailRef{}, fmt.Errorf("%w: mid ref must be a <…@…> Message-ID of at most 998 bytes", ErrMailRefInvalid)
		}
		return mailRef{kind: "mid", messageID: mid}, nil
	}
	return mailRef{}, fmt.Errorf("%w: must be uid:<uidvalidity>:<uid> or mid:<message-id>", ErrMailRefInvalid)
}

// selectFolder SELECTs the slug's real folder and returns its UIDVALIDITY and
// message count. Reads are ordinary SELECTs; the fetches behind them are all
// BODY.PEEK, so no flag is ever written (round-2 MAJ-003).
func (c *Client) selectFolder(ctx context.Context, client *imapclient.Client, name string) (uidvalidity uint32, exists uint32, err error) {
	sel, serr := runIMAP(ctx, "select "+name, func() (*imap.SelectData, error) {
		return client.Select(name, nil).Wait()
	})
	if serr != nil {
		return 0, 0, fmt.Errorf("email transport: select %s: %w", name, serr)
	}
	return sel.UIDValidity, sel.NumMessages, nil
}

// ReadFolderPage returns one envelope page for any D5 folder, newest first
// (§2.3): beforeUID==0 fetches the newest limit messages; beforeUID>0 fetches
// messages with UID strictly below it. Truncation is explicit — the caller
// learns whether more rows exist and gets the next page cursor. Envelopes
// only: no body fetch, so no \Seen side effect is even possible.
func (c *Client) ReadFolderPage(ctx context.Context, slug string, limit int, beforeUID uint32) ([]MailRow, uint32, bool, error) {
	name, err := c.folderNameFor(slug)
	if err != nil {
		return nil, 0, false, err
	}
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	defer client.Close()
	uidvalidity, _, err := c.selectFolder(ctx, client, name)
	if err != nil {
		return nil, 0, false, err
	}

	var all []uint32
	switch {
	case beforeUID > 0:
		if beforeUID == 1 {
			return []MailRow{}, uidvalidity, false, nil
		}
		crit := &imap.SearchCriteria{}
		var s imap.UIDSet
		s.AddRange(imap.UID(1), imap.UID(beforeUID-1))
		crit.UID = []imap.UIDSet{s}
		sd, serr := runIMAP(ctx, "search page", func() (*imap.SearchData, error) {
			return client.UIDSearch(crit, nil).Wait()
		})
		if serr != nil {
			return nil, 0, false, fmt.Errorf("email transport: search page %s: %w", slug, serr)
		}
		all = searchDataUIDs(sd)
	default:
		// SEARCH ALL — the price of an explicitly-truncating page cursor
		// (the schema promises the page never silently drops rows).
		sd, serr := runIMAP(ctx, "search all", func() (*imap.SearchData, error) {
			return client.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		})
		if serr != nil {
			return nil, 0, false, fmt.Errorf("email transport: search page %s: %w", slug, serr)
		}
		all = searchDataUIDs(sd)
	}
	if len(all) == 0 {
		return []MailRow{}, uidvalidity, false, nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i] > all[j] }) // newest first
	truncated := len(all) > limit
	if truncated {
		all = all[:limit]
	}
	rows, ferr := c.fetchMailRows(ctx, client, imap.UIDSetNum(toUIDs(all)...))
	if ferr != nil {
		return nil, 0, false, ferr
	}
	return rows, uidvalidity, truncated, nil
}

// MailRow is one envelope page row for the Mail panel lists — the transport
// Message envelope plus the flag bits the wire rows require (\Seen, \Draft,
// the $OmnipusAgentRead keyword) and the X-Omnipus-Draft header peek. The
// header peek rides the SAME fetch command (a second BODY.PEEK section:
// HEADER.FIELDS (X-OMNIPUS-DRAFT)) so the page stays one round trip.
type MailRow struct {
	UID            uint32
	UIDValidity    uint32
	Seen           bool
	IsDraft        bool
	ReadByAgent    bool
	IsOmnipusDraft bool
	MessageID      string
	From           string
	FromName       string
	ReplyTo        string
	To             []string
	Cc             []string
	Subject        string
	Date           time.Time
}

// fetchMailRows fetches envelopes + flags + the X-Omnipus-Draft header peek
// for one UID set and maps the buffers to MailRow. The header peek rides the
// same FETCH (round-1 MAJ-012's one-session-per-request budget is preserved —
// this is one command, not one per row).
func (c *Client) fetchMailRows(ctx context.Context, client *imapclient.Client, set imap.UIDSet) ([]MailRow, error) {
	opts := &imap.FetchOptions{
		UID: true, Flags: true, Envelope: true,
		BodySection: []*imap.FetchItemBodySection{{
			Specifier:    imap.PartSpecifierHeader,
			HeaderFields: []string{"X-Omnipus-Draft"},
			Peek:         true,
		}},
	}
	bufs, err := runIMAP(ctx, "fetch rows", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(set, opts).Collect()
	})
	if err != nil {
		return nil, fmt.Errorf("email transport: fetch rows: %w", err)
	}
	rows := make([]MailRow, 0, len(bufs))
	for _, buf := range bufs {
		if buf == nil {
			continue
		}
		row := MailRow{
			UID:       uint32(buf.UID),
			MessageID: buf.Envelope.MessageID,
			Subject:   strings.TrimSpace(buf.Envelope.Subject),
			Date:      buf.Envelope.Date.UTC(),
		}
		if len(buf.Envelope.From) > 0 {
			row.From = addressString(buf.Envelope.From[0])
			row.FromName = buf.Envelope.From[0].Name
		}
		if len(buf.Envelope.ReplyTo) > 0 {
			row.ReplyTo = addressString(buf.Envelope.ReplyTo[0])
		}
		row.To = splitAddressList(addressListString(buf.Envelope.To))
		row.Cc = splitAddressList(addressListString(buf.Envelope.Cc))
		for _, f := range buf.Flags {
			switch {
			case string(f) == string(imap.FlagSeen):
				row.Seen = true
			case string(f) == string(imap.FlagDraft):
				row.IsDraft = true
			case strings.EqualFold(string(f), readByAgentKeyword):
				row.ReadByAgent = true
			}
		}
		for _, sec := range buf.BodySection {
			if len(sec.Bytes) > 0 && strings.Contains(strings.ToLower(string(sec.Bytes)), "x-omnipus-draft:") {
				row.IsOmnipusDraft = true
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].UID > rows[j].UID }) // newest first
	return rows, nil
}

// searchDataUIDs extracts a search result's UIDs.
func searchDataUIDs(sd *imap.SearchData) []uint32 {
	if sd == nil {
		return nil
	}
	uids := sd.AllUIDs()
	out := make([]uint32, 0, len(uids))
	for _, u := range uids {
		out = append(out, uint32(u))
	}
	return out
}

// toUIDs widens uint32s to imap.UID.
func toUIDs(in []uint32) []imap.UID {
	out := make([]imap.UID, 0, len(in))
	for _, u := range in {
		out = append(out, imap.UID(u))
	}
	return out
}

// MailView is the full message view behind the panel read path (§2.2
// MailMessage) — everything the gateway handler maps onto the generated wire
// type. The HTML body part is carried RAW here; sanitization happens at
// preview mint against the dedicated mail policy, never on this struct.
type MailView struct {
	UID            uint32
	UIDValidity    uint32
	Flags          []string
	MessageID      string
	InReplyTo      string
	References     string
	From           string
	FromName       string
	ReplyTo        string
	To             []string
	Cc             []string
	Bcc            []string
	Subject        string
	Date           time.Time
	TextBody       string
	HTMLBody       string
	HasHTML        bool
	BodyMarkdown   string
	MarkdownLossy  bool
	IsOmnipusDraft bool
	RenderHash     string
	Attachments    []MailPart
	Inline         []MailPart
}

// MailPart is one leaf MIME part of a message view. PartIndex is the stable
// walk-order leaf index — the {partIndex} path parameter of the download and
// inline-part routes addresses the same enumeration.
type MailPart struct {
	PartIndex   int
	Filename    string
	ContentType string
	SizeBytes   int
	ContentID   string
	Disposition string
	Data        []byte
	// DataUnavailable marks a part that is listed but whose bytes are NOT
	// present: it exceeded maxViewPartBytes (memory guard, D6) or its body
	// failed to read. The gateway refuses such a part download-side (413)
	// and the draft keep paths refuse it (400) instead of silently
	// transmitting or serving nothing.
	DataUnavailable bool
}

// maxViewPartBytes is the gateway's memory guard for one inbound part: a
// hostile giant attachment must not materialize whole in RAM (D6: nothing is
// stored; this is in-memory such only). It mirrors the MC-32 outbound cap —
// parts larger than 25 MiB (or whose bytes failed to decode) are still
// LISTED by their header claim, with a loud unavailable marker (the
// decodeBody house style), but their bytes are not fetched and addressing
// them download-side fails closed (413).
const maxViewPartBytes = 25 << 20

// ReadView fetches one message fully (BODY.PEEK[]) by folder-scoped ref and
// walks its MIME structure into a MailView. Fetching never writes flags
// (round-2 MAJ-003). mid: refs search ONLY the addressed folder, among
// non-\Deleted messages, resolving multiple hits to the highest UID
// (round-2 MIN-005).
func (c *Client) ReadView(ctx context.Context, slug, ref string) (*MailView, error) {
	name, err := c.folderNameFor(slug)
	if err != nil {
		return nil, err
	}
	r, err := parseMailRef(ref)
	if err != nil {
		return nil, err
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	uidvalidity, _, err := c.selectFolder(ctx, client, name)
	if err != nil {
		return nil, err
	}

	uid := r.uid
	if r.kind == "mid" {
		uid, err = c.searchMessageID(ctx, client, r.messageID)
		if err != nil {
			return nil, err
		}
		if uid == 0 {
			return nil, fmt.Errorf("email transport: no message %s in %s", ref, slug)
		}
	}

	opts := &imap.FetchOptions{UID: true, Flags: true, Envelope: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}
	fetched, ferr := runIMAP(ctx, "fetch view", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(imap.UIDSetNum(imap.UID(uid)), opts).Collect()
	})
	if ferr != nil {
		return nil, fmt.Errorf("email transport: fetch view: %w", ferr)
	}
	if len(fetched) == 0 || fetched[0] == nil {
		return nil, fmt.Errorf("email transport: message %s not found in %s", ref, slug)
	}
	buf := fetched[0]
	var raw []byte
	for _, sec := range buf.BodySection {
		if len(sec.Bytes) > 0 {
			raw = sec.Bytes
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("email transport: message %s fetched empty", ref)
	}
	view := viewFromRaw(raw, slug, uid)
	view.UID = uid
	view.UIDValidity = uidvalidity
	for _, f := range buf.Flags {
		view.Flags = append(view.Flags, string(f))
	}
	if buf.Envelope != nil {
		// Same header handling as bufferToMessage (transport.go) — the view
		// and the tool lists must never disagree about a header.
		view.Subject = strings.TrimSpace(buf.Envelope.Subject)
		view.MessageID = buf.Envelope.MessageID
		if len(buf.Envelope.InReplyTo) > 0 {
			view.InReplyTo = buf.Envelope.InReplyTo[0]
		}
		if len(buf.Envelope.From) > 0 {
			view.From = addressString(buf.Envelope.From[0])
			view.FromName = buf.Envelope.From[0].Name
		}
		if len(buf.Envelope.ReplyTo) > 0 {
			view.ReplyTo = addressString(buf.Envelope.ReplyTo[0])
		}
		view.To = splitAddressList(addressListString(buf.Envelope.To))
		view.Cc = splitAddressList(addressListString(buf.Envelope.Cc))
		if !buf.Envelope.Date.IsZero() {
			view.Date = buf.Envelope.Date.UTC()
		}
	}
	return view, nil
}

// searchMessageID UID-SEARCHes the already-selected folder for the Message-ID
// among non-\Deleted messages and returns the highest matching UID (0 when
// absent).
func (c *Client) searchMessageID(ctx context.Context, client *imapclient.Client, messageID string) (uint32, error) {
	crit := &imap.SearchCriteria{
		Header:  []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: messageID}},
		NotFlag: []imap.Flag{imap.FlagDeleted},
	}
	sd, err := runIMAP(ctx, "search mid", func() (*imap.SearchData, error) {
		return client.UIDSearch(crit, nil).Wait()
	})
	if err != nil {
		return 0, fmt.Errorf("email transport: search mid: %w", err)
	}
	uids := searchDataUIDs(sd)
	if len(uids) == 0 {
		return 0, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	return uids[0], nil
}

// splitAddressList splits a comma-joined address header back into individual
// bare addresses (the envelope rendering the transport already produces).
func splitAddressList(joined string) []string {
	joined = strings.TrimSpace(joined)
	if joined == "" {
		return nil
	}
	parts := strings.Split(joined, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// viewFromRaw parses the raw RFC 5322 bytes: top-level headers (Bcc — present
// only on the owner's own copies, X-Omnipus-* draft markers, References) and
// the MIME leaf walk (text bodies, the stored text/markdown part of Omnipus
// drafts, attachment and inline parts).
// slug and uid exist only so the unavailable-part WARN can name folder and UID.
func viewFromRaw(raw []byte, slug string, uid uint32) *MailView {
	view := &MailView{}
	reader, err := gomail.CreateReader(bytes.NewReader(raw))
	if err == nil {
		view.Bcc = splitAddressList(reader.Header.Get("Bcc"))
		view.References = strings.TrimSpace(reader.Header.Get("References"))
		view.IsOmnipusDraft = strings.TrimSpace(reader.Header.Get("X-Omnipus-Draft")) != ""
		view.RenderHash = strings.TrimSpace(reader.Header.Get("X-Omnipus-Render-Hash"))
	}
	if err != nil {
		return view
	}
	defer reader.Close()
	var leaf int
	for {
		part, perr := reader.NextPart()
		if perr != nil || part == nil {
			break
		}
		ct, ctParams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		disp, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := dispParams["filename"]
		if name == "" {
			name = ctParams["name"]
		}
		idx := leaf
		leaf++

		switch {
		case ct == "text/plain" && name == "" && disp == "":
			view.TextBody = readViewText(part.Body)
			view.BodyMarkdown = view.TextBody // derived; lossy until a stored part proves otherwise
			view.MarkdownLossy = true
		case ct == "text/html" && name == "" && disp == "":
			view.HTMLBody = readViewText(part.Body)
			view.HasHTML = view.HTMLBody != ""
		case ct == "text/markdown" && name == "" && disp == "":
			view.BodyMarkdown = readViewText(part.Body)
			view.MarkdownLossy = false
		default:
			p := MailPart{
				PartIndex:   idx,
				Filename:    sanitizeMailPartName(name),
				ContentType: ct,
				ContentID:   stripContentIDAngles(part.Header.Get("Content-ID")),
				Disposition: disp,
			}
			data, rerr := io.ReadAll(io.LimitReader(part.Body, maxViewPartBytes+1))
			unavailReason := ""
			switch {
			case rerr != nil:
				p.DataUnavailable = true
				unavailReason = "failed to decode"
			case len(data) > maxViewPartBytes:
				p.DataUnavailable = true
				unavailReason = "over the 25 MiB per-part fetch cap"
			default:
				p.Data = data
				p.SizeBytes = len(data)
			}
			isAttachment := p.Disposition == "attachment" || p.Filename != ""
			if p.DataUnavailable {
				if claimed, perr := strconv.Atoi(dispParams["size"]); perr == nil && claimed > 0 {
					p.SizeBytes = claimed
				}
				p.Filename += " [attachment unavailable: " + unavailReason + "]"
				slog.Warn("email view: attachment part listed but unavailable",
					"folder", slug, "uid", uid, "part_index", p.PartIndex,
					"reason", unavailReason)
			}
			if isAttachment {
				view.Attachments = append(view.Attachments, p)
			} else {
				view.Inline = append(view.Inline, p)
			}
		}
	}
	return view
}

// readViewText reads a text part as UTF-8-ish text. Charsets beyond UTF-8
// fall back to the raw bytes (the transport's decodeBody remains the tool
// path's authority; this is the panel read path).
func readViewText(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return ""
	}
	return string(data)
}

// sanitizeMailPartName strips path separators and control characters from a
// MIME part filename — never trusted raw (MC-32).
func sanitizeMailPartName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == ':':
			b.WriteRune('-')
		case r < 0x20 || r == 0x7f:
			// control characters dropped
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "." || out == ".." {
		return "-"
	}
	return out
}

// stripContentIDAngles removes the <…> wrapper of a Content-ID header so
// cid: references match without punctuation disagreement.
func stripContentIDAngles(cid string) string {
	cid = strings.TrimSpace(cid)
	cid = strings.TrimPrefix(cid, "<")
	cid = strings.TrimSuffix(cid, ">")
	return strings.TrimSpace(cid)
}

// MarkSeenIn marks \Seen on one message in any folder — the panel's POST seen
// endpoint (FR-020). It writes \Seen ONLY: the agent-read keyword belongs to
// read_message's writer (D38), and the list/read paths never write at all
// (round-2 MAJ-003). Idempotent: re-flagging an already-seen message is a
// server-side no-op (204 semantics at the endpoint).
func (c *Client) MarkSeenIn(ctx context.Context, slug string, uid uint32) error {
	if uid == 0 {
		return fmt.Errorf("email transport: uid is required")
	}
	name, err := c.folderNameFor(slug)
	if err != nil {
		return err
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if _, _, err := c.selectFolder(ctx, client, name); err != nil {
		return err
	}
	storeFlags := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagSeen},
		Silent: true,
	}
	if _, err := runIMAP(ctx, "store seen", func() (struct{}, error) {
		return struct{}{}, client.Store(imap.UIDSetNum(imap.UID(uid)), storeFlags, nil).Close()
	}); err != nil {
		return fmt.Errorf("email transport: mark seen uid %d in %s: %w", uid, slug, err)
	}
	return nil
}

// DeleteDraft flags a draft \Deleted and, when the server supports UIDPLUS,
// expunges EXACTLY that UID (FR-032). Thin wrapper over DeleteDraftStatus
// that discards the expunge fact.
func (c *Client) DeleteDraft(ctx context.Context, uid uint32) error {
	_, err := c.DeleteDraftStatus(ctx, uid)
	return err
}

// DeleteDraftStatus is DeleteDraft with the expunge fact exposed: expunged is
// true only when the UIDPLUS UIDExpunge actually ran. Without UIDPLUS the
// \Deleted flag stays for deferred expunge — a plain EXPUNGE would also purge
// unrelated \Deleted messages a human's other client may have, so it is
// never sent here. The panel's discard and send paths audit this value so
// the log never claims an expunge that did not happen.
func (c *Client) DeleteDraftStatus(ctx context.Context, uid uint32) (expunged bool, err error) {
	if uid == 0 {
		return false, fmt.Errorf("email transport: uid is required")
	}
	name, err := c.folderNameFor(FolderDrafts)
	if err != nil {
		return false, err
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return false, err
	}
	defer client.Close()
	if _, _, err := c.selectFolder(ctx, client, name); err != nil {
		return false, err
	}
	storeFlags := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagDeleted},
		Silent: true,
	}
	if _, err := runIMAP(ctx, "store deleted", func() (struct{}, error) {
		return struct{}{}, client.Store(imap.UIDSetNum(imap.UID(uid)), storeFlags, nil).Close()
	}); err != nil {
		return false, fmt.Errorf("email transport: flag draft uid %d deleted: %w", uid, err)
	}
	if client.Caps().Has(imap.CapUIDPlus) {
		if _, err := runIMAP(ctx, "uid expunge", func() (struct{}, error) {
			return struct{}{}, client.UIDExpunge(imap.UIDSetNum(imap.UID(uid))).Close()
		}); err != nil {
			return false, fmt.Errorf("email transport: expunge draft uid %d: %w", uid, err)
		}
		expunged = true
	}
	return expunged, nil
}

// ResolveRef resolves a folder-scoped ref to its current (uidvalidity, uid)
// without fetching the body: uid-form refs ride the live folder epoch;
// mid-form refs search only the addressed folder among non-\Deleted messages,
// resolving multiple hits to the highest UID (round-2 MIN-005). The gateway's
// staleness preconditions and the seen action resolve through this so list
// rows, reads and mutations address one consistently-defined target.
func (c *Client) ResolveRef(ctx context.Context, slug, ref string) (uint32, uint32, error) {
	name, err := c.folderNameFor(slug)
	if err != nil {
		return 0, 0, err
	}
	r, err := parseMailRef(ref)
	if err != nil {
		return 0, 0, err
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer client.Close()
	uv, _, err := c.selectFolder(ctx, client, name)
	if err != nil {
		return 0, 0, err
	}
	if r.kind == "uid" {
		return uv, r.uid, nil
	}
	uid, err := c.searchMessageID(ctx, client, r.messageID)
	if err != nil {
		return 0, 0, err
	}
	if uid == 0 {
		return 0, 0, fmt.Errorf("email transport: no message %s in %s", ref, slug)
	}
	return uv, uid, nil
}

// SanitizeAttachmentName is the exported MC-32 sanitizer for outbound
// attachment filenames: path separators become '-', control characters are
// dropped, and the "."/".." specials collapse to "-". The mail tools use it
// on agent-workspace attachments; the gateway uses it on panel uploads.
func SanitizeAttachmentName(name string) string { return sanitizeMailPartName(name) }
