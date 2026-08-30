// Package email provides a reusable, pure-Go IMAP (inbound) + SMTP (outbound)
// transport for the email *tool* surface (M11). Email is modeled as a TOOL, not
// a conversational channel: an agent pulls its inbox on demand (read_inbox,
// search_email, read_message) and sends/replies (send_email, reply) over a
// single configured mailbox account. There is no push loop and no MessageBus
// involvement — this package is the transport only.
//
// The transport is pure Go (CGO_ENABLED=0): IMAP via emersion/go-imap/v2 over
// implicit TLS (IMAPS, port 993), SMTP via net/smtp over STARTTLS (587) or
// implicit TLS (465/SMTPS). The Transport interface is the test seam — the
// email tools depend only on it, so unit tests inject an in-memory fake and
// never stand up a real server.
//
// Inbound bodies are decoded from MIME on the read path: read_message fetches
// the FULL message (BODY[], headers included) and go-message decodes the
// Content-Transfer-Encoding (base64/quoted-printable) and charset, preferring
// the text/plain part and falling back to a tag-stripped rendering of an
// HTML-only body. Decoded bodies are capped (maxBodyBytes) so an
// attachment-heavy message cannot bloat the agent's context.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/smtp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	message "github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"golang.org/x/net/html"

	// Register the extended charset decoders (ISO-8859-*, Windows-125x, GBK, …)
	// so go-message can decode non-UTF-8 bodies. Pure Go via golang.org/x/text.
	_ "github.com/emersion/go-message/charset"
)

const (
	defaultIMAPPort = 993
	defaultSMTPPort = 587

	// dialTimeout bounds a single IMAP/SMTP dial so a tool call cannot hang the
	// agent turn indefinitely on an unreachable server.
	dialTimeout = 30 * time.Second

	// commandTimeout bounds a single IMAP command (LOGIN/SELECT/SEARCH/FETCH/
	// STORE) once connected. A slow un-indexed body search on a huge mailbox
	// therefore returns a timeout error instead of hanging the whole turn.
	commandTimeout = 45 * time.Second

	// defaultListLimit / maxListLimit bound how many messages a list/search
	// returns. The cap is enforced in Go (clampLimit), not merely advertised in
	// the JSON schema, so a model that ignores the schema still cannot request an
	// unbounded fetch.
	defaultListLimit = 20
	maxListLimit     = 100

	// maxBodyBytes caps a decoded message body. Larger bodies are truncated on a
	// UTF-8 boundary with an explicit marker so the agent knows content was cut.
	maxBodyBytes = 256 * 1024
)

// imapDial is the production IMAPS dialer. It is a package-level var only so
// tests can point dialIMAP at an in-memory server over a plaintext connection;
// production code never reassigns it.
var imapDial = func(addr string, tlsCfg *tls.Config) (*imapclient.Client, error) {
	return imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: tlsCfg})
}

// Account holds the connection parameters for a single mailbox. The password is
// resolved by the caller (from the encrypted credential store) and passed here
// in plaintext for the duration of a transport operation — it is never persisted
// by this package.
type Account struct {
	IMAPHost string
	IMAPPort int
	SMTPHost string
	SMTPPort int
	Username string
	Password string
}

// withDefaults returns a copy of a with default ports applied.
func (a Account) withDefaults() Account {
	if a.IMAPPort == 0 {
		a.IMAPPort = defaultIMAPPort
	}
	if a.SMTPPort == 0 {
		a.SMTPPort = defaultSMTPPort
	}
	return a
}

// Message is a transport-level representation of a single email message,
// independent of the underlying IMAP library types so tools and tests do not
// import go-imap.
type Message struct {
	// UID is the IMAP UID — a stable, mailbox-scoped identifier usable by
	// read_message and reply to address a specific message.
	UID uint32 `json:"uid"`
	// MessageID is the RFC 5322 Message-ID header (threading basis), if present.
	MessageID string `json:"message_id,omitempty"`
	From      string `json:"from"`
	FromName  string `json:"from_name,omitempty"`
	// ReplyTo is the RFC 5322 Reply-To header address, if the sender set one.
	// When present it names the address the sender explicitly asked replies to
	// go to (common for mailing lists, ticketing systems, and no-reply@
	// senders) and callers such as the reply tool should prefer it over From.
	ReplyTo string `json:"reply_to,omitempty"`
	To      string `json:"to,omitempty"`
	Subject string `json:"subject"`
	// Date is the message Date header in RFC 3339 (UTC), best-effort.
	Date string `json:"date,omitempty"`
	// Body is the decoded plain-text body. Populated by ReadMessage; for
	// list/search results it is empty (envelope-only) to keep payloads small.
	Body string `json:"body,omitempty"`
	// Seen reflects the \Seen IMAP flag at fetch time.
	Seen bool `json:"seen"`
}

// InboxOptions controls ReadInbox.
type InboxOptions struct {
	// Limit is the maximum number of messages to return. <=0 uses the default;
	// values above maxListLimit are clamped down.
	Limit int
	// UnseenOnly restricts to \Unseen messages (uses a server-side UIDSearch).
	UnseenOnly bool
	// BeforeUID, when >0, returns only messages with UID strictly less than it —
	// a deterministic pagination cursor so the caller can page an inbox without
	// raising Limit.
	BeforeUID uint32
}

// SearchOptions controls Search.
type SearchOptions struct {
	// Limit is the maximum number of results. <=0 uses the default; values above
	// maxListLimit are clamped down.
	Limit int
	// BeforeUID, when >0, restricts to messages with UID strictly less than it
	// (pagination cursor).
	BeforeUID uint32
	// Body, when true, opts in to an expensive server-side BODY substring scan in
	// addition to the light Subject/From header match. Left false by default so
	// the common case never issues an un-indexed full-body scan.
	Body bool
}

// SearchResult is the outcome of a Search: the matched messages plus enough
// metadata for the agent to know the results are partial and to page further.
type SearchResult struct {
	// Messages are the envelope-only matches, newest first, capped to the limit.
	Messages []Message `json:"messages"`
	// TotalMatches is how many messages the server matched BEFORE the limit was
	// applied — so the agent can tell "5 of 5" from "20 of 4000".
	TotalMatches int `json:"total_matches"`
	// Truncated is true when TotalMatches exceeded the limit and results were
	// cut. Never silently drop: this flag makes partial results explicit.
	Truncated bool `json:"truncated"`
	// NextBeforeUID is the pagination cursor for the next page (pass it back as
	// SearchOptions.BeforeUID). Set only when Truncated. It is the smallest UID
	// in this page, so the next page continues strictly older.
	NextBeforeUID uint32 `json:"next_before_uid,omitempty"`
}

// SendRequest describes an outbound message.
type SendRequest struct {
	To      string
	Subject string
	Body    string
	// InReplyTo, when non-empty, is set as the In-Reply-To and References
	// headers so replies thread correctly in the recipient's client.
	InReplyTo string
}

// Transport is the test seam the email tools depend on. The production
// implementation is *Client; tests inject an in-memory fake.
type Transport interface {
	// ReadInbox returns up to opts.Limit of the most recent INBOX messages
	// (envelope only, newest first).
	ReadInbox(ctx context.Context, opts InboxOptions) ([]Message, error)
	// Search returns envelope-only matches for a free-text query (matched
	// against subject and from by default, plus body when opts.Body is set),
	// newest first, up to opts.Limit, with truncation/pagination metadata.
	Search(ctx context.Context, query string, opts SearchOptions) (SearchResult, error)
	// ReadMessage fetches a single message (including a decoded body) by IMAP UID.
	ReadMessage(ctx context.Context, uid uint32) (*Message, error)
	// Send delivers an outbound message via SMTP.
	Send(ctx context.Context, req SendRequest) error
	// MarkSeen sets the \Seen flag on the message with the given UID. The heartbeat
	// drainer calls this after a message has been turned into a Board task so the
	// next unseen scan does not re-enqueue it.
	MarkSeen(ctx context.Context, uid uint32) error
}

// Client is the production pure-Go Transport over IMAPS + SMTP. It is
// connectionless between calls: each operation dials, authenticates, performs
// the operation, and tears down. This keeps the agent's tool calls stateless
// and avoids holding an idle IMAP connection between heartbeats.
type Client struct {
	acct Account
}

// NewClient constructs a Client for the given account. It validates that the
// minimum connection parameters are present; it does NOT dial (auth is verified
// on first operation).
func NewClient(acct Account) (*Client, error) {
	if acct.IMAPHost == "" {
		return nil, fmt.Errorf("email transport: imap_host is required")
	}
	if acct.SMTPHost == "" {
		return nil, fmt.Errorf("email transport: smtp_host is required")
	}
	if acct.Username == "" {
		return nil, fmt.Errorf("email transport: username is required")
	}
	if acct.Password == "" {
		return nil, fmt.Errorf("email transport: password is required")
	}
	return &Client{acct: acct.withDefaults()}, nil
}

// Address returns the mailbox's own email address (the SMTP/IMAP username).
func (c *Client) Address() string { return c.acct.Username }

// clampLimit normalises a caller-supplied limit: <=0 becomes the default, and
// anything above the max is clamped down. Enforced here (not just in the JSON
// schema) so an out-of-range request cannot trigger an unbounded fetch.
func clampLimit(n int) int {
	switch {
	case n <= 0:
		return defaultListLimit
	case n > maxListLimit:
		return maxListLimit
	default:
		return n
	}
}

// runIMAP bounds a single blocking IMAP command by ctx (and commandTimeout). If
// the deadline fires first the error is returned; the caller's deferred
// client.Close then tears down the connection, which unblocks the goroutine
// still parked in Wait/Collect. The result channel is buffered so that
// goroutine can never leak even if it finishes after the timeout.
func runIMAP[T any](ctx context.Context, op string, fn func() (T, error)) (T, error) {
	cctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()

	select {
	case <-cctx.Done():
		var zero T
		return zero, fmt.Errorf("email transport: %s: %w", op, cctx.Err())
	case r := <-ch:
		return r.v, r.err
	}
}

// dialIMAP dials the IMAP server over implicit TLS, logs in, and selects INBOX.
// It returns the SELECT response (whose NumMessages count drives the
// trailing-range read path) alongside the client. The caller must Close the
// returned client.
func (c *Client) dialIMAP(ctx context.Context) (*imapclient.Client, *imap.SelectData, error) {
	addr := fmt.Sprintf("%s:%d", c.acct.IMAPHost, c.acct.IMAPPort)
	tlsCfg := &tls.Config{ServerName: c.acct.IMAPHost, MinVersion: tls.VersionTLS12}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	// imapDial does not take a context; guard the dial with a timeout goroutine
	// so an unreachable server cannot block past dialTimeout.
	type dialResult struct {
		cl  *imapclient.Client
		err error
	}
	ch := make(chan dialResult, 1)
	go func() {
		cl, err := imapDial(addr, tlsCfg)
		ch <- dialResult{cl, err}
	}()
	var client *imapclient.Client
	select {
	case <-dialCtx.Done():
		return nil, nil, fmt.Errorf("email transport: dial %s: %w", addr, dialCtx.Err())
	case res := <-ch:
		if res.err != nil {
			return nil, nil, fmt.Errorf("email transport: dial TLS %s: %w", addr, res.err)
		}
		client = res.cl
	}

	if _, err := runIMAP(ctx, "login", func() (struct{}, error) {
		return struct{}{}, client.Login(c.acct.Username, c.acct.Password).Wait()
	}); err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("email transport: login failed: %w", err)
	}
	selData, err := runIMAP(ctx, "select INBOX", func() (*imap.SelectData, error) {
		return client.Select("INBOX", nil).Wait()
	})
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("email transport: select INBOX: %w", err)
	}
	return client, selData, nil
}

// ReadInbox returns up to opts.Limit of the most recent INBOX messages, newest
// first. The default (seen+unseen, no cursor) path avoids a full-mailbox
// SEARCH ALL: it takes the SELECT EXISTS count and fetches the trailing
// sequence range. Unseen-only mode and cursor paging go through a bounded
// UIDSearch instead.
func (c *Client) ReadInbox(ctx context.Context, opts InboxOptions) ([]Message, error) {
	limit := clampLimit(opts.Limit)
	client, selData, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	if opts.UnseenOnly || opts.BeforeUID > 0 {
		crit := &imap.SearchCriteria{}
		if opts.UnseenOnly {
			crit.NotFlag = []imap.Flag{imap.FlagSeen}
		}
		switch {
		case opts.BeforeUID == 1:
			// Nothing has a UID strictly below 1.
			return []Message{}, nil
		case opts.BeforeUID > 1:
			var s imap.UIDSet
			s.AddRange(imap.UID(1), imap.UID(opts.BeforeUID-1))
			crit.UID = []imap.UIDSet{s}
		}
		searchData, err := runIMAP(ctx, "search inbox", func() (*imap.SearchData, error) {
			return client.UIDSearch(crit, nil).Wait()
		})
		if err != nil {
			return nil, fmt.Errorf("email transport: search inbox: %w", err)
		}
		uids := searchData.AllUIDs()
		if len(uids) == 0 {
			return []Message{}, nil
		}
		sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
		if len(uids) > limit {
			uids = uids[:limit]
		}
		return c.fetchMessages(ctx, client, imap.UIDSetNum(uids...), false)
	}

	// Default path: the newest `limit` messages by sequence number, derived from
	// the EXISTS count — no SEARCH ALL.
	n := selData.NumMessages
	if n == 0 {
		return []Message{}, nil
	}
	start := uint32(1)
	if n > uint32(limit) {
		start = n - uint32(limit) + 1
	}
	var seq imap.SeqSet
	seq.AddRange(start, n)
	return c.fetchMessages(ctx, client, seq, false)
}

// Search returns matches for query, newest first, up to opts.Limit, with
// truncation and pagination metadata. Every IMAP command is bounded by the
// context so a slow body scan on a large mailbox returns a timeout error rather
// than hanging the turn.
func (c *Client) Search(ctx context.Context, query string, opts SearchOptions) (SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return SearchResult{}, fmt.Errorf("email transport: search query is empty")
	}
	if opts.BeforeUID == 1 {
		// Nothing has a UID strictly below 1 — terminate the pagination loop
		// here rather than falling through to buildSearchCriteria, which
		// ignores a beforeUID of 1 (its "beforeUID > 1" guard) and would
		// otherwise re-run the query with no UID restriction at all, silently
		// returning page 1 again instead of an empty final page.
		return SearchResult{Messages: []Message{}}, nil
	}
	limit := clampLimit(opts.Limit)
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	defer client.Close()

	crit := buildSearchCriteria(q, opts.Body, opts.BeforeUID)
	searchData, err := runIMAP(ctx, "search", func() (*imap.SearchData, error) {
		return client.UIDSearch(crit, nil).Wait()
	})
	if err != nil {
		return SearchResult{}, fmt.Errorf("email transport: search %q: %w", q, err)
	}
	uids := searchData.AllUIDs()
	total := len(uids)
	if total == 0 {
		return SearchResult{Messages: []Message{}}, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })

	truncated := false
	if total > limit {
		uids = uids[:limit]
		truncated = true
	}
	msgs, err := c.fetchMessages(ctx, client, imap.UIDSetNum(uids...), false)
	if err != nil {
		return SearchResult{}, err
	}
	res := SearchResult{
		Messages:     msgs,
		TotalMatches: total,
		Truncated:    truncated,
	}
	if truncated && len(msgs) > 0 {
		// Cursor for the next page: everything strictly older than the smallest
		// UID we returned (results are newest-first, so that is the last one).
		res.NextBeforeUID = msgs[len(msgs)-1].UID
	}
	return res, nil
}

// buildSearchCriteria assembles the server-side SEARCH criteria. The default is
// the light, header-indexed Subject-OR-From match; the expensive un-indexed
// BODY substring scan is added only when body is true. A non-zero beforeUID is
// ANDed on as a "UID < beforeUID" range for pagination.
func buildSearchCriteria(q string, body bool, beforeUID uint32) *imap.SearchCriteria {
	crit := &imap.SearchCriteria{}
	if body {
		// (Subject OR (From OR BODY)) — go-imap models OR as a pair, so chain it.
		crit.Or = [][2]imap.SearchCriteria{{
			{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: q}}},
			{Or: [][2]imap.SearchCriteria{{
				{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: q}}},
				{Body: []string{q}},
			}}},
		}}
	} else {
		crit.Or = [][2]imap.SearchCriteria{{
			{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: q}}},
			{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: q}}},
		}}
	}
	if beforeUID > 1 {
		var s imap.UIDSet
		s.AddRange(imap.UID(1), imap.UID(beforeUID-1))
		crit.UID = []imap.UIDSet{s}
	}
	return crit
}

// ReadMessage fetches a single message (with a decoded body) by UID.
func (c *Client) ReadMessage(ctx context.Context, uid uint32) (*Message, error) {
	if uid == 0 {
		return nil, fmt.Errorf("email transport: uid is required")
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	msgs, err := c.fetchMessages(ctx, client, imap.UIDSetNum(imap.UID(uid)), true)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("email transport: message uid %d not found", uid)
	}
	return &msgs[0], nil
}

// fetchMessages fetches envelopes (and, when withBody, the FULL raw message so
// the MIME body can be decoded) for the given number set, converts them to
// transport Messages, and returns them newest-first (by UID). The FETCH is
// bounded by the context.
func (c *Client) fetchMessages(ctx context.Context, client *imapclient.Client, numSet imap.NumSet, withBody bool) ([]Message, error) {
	opts := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	if withBody {
		// BODY[] — the whole message including the headers that declare
		// Content-Type / Content-Transfer-Encoding / boundary, which BODY[TEXT]
		// omitted. Those headers are what makes MIME decoding possible.
		opts.BodySection = []*imap.FetchItemBodySection{{}}
	}
	fetched, err := runIMAP(ctx, "fetch", func() ([]*imapclient.FetchMessageBuffer, error) {
		return client.Fetch(numSet, opts).Collect()
	})
	if err != nil {
		return nil, fmt.Errorf("email transport: fetch: %w", err)
	}

	out := make([]Message, 0, len(fetched))
	for _, m := range fetched {
		if m == nil || m.Envelope == nil {
			continue
		}
		out = append(out, bufferToMessage(m, withBody))
	}
	// Newest first, regardless of the order the server returned.
	sort.Slice(out, func(i, j int) bool { return out[i].UID > out[j].UID })
	return out, nil
}

// bufferToMessage converts a fetched IMAP buffer to a transport Message. When
// withBody is set the BODY[] section is decoded from MIME.
func bufferToMessage(m *imapclient.FetchMessageBuffer, withBody bool) Message {
	env := m.Envelope
	msg := Message{
		UID:       uint32(m.UID),
		MessageID: env.MessageID,
		Subject:   strings.TrimSpace(env.Subject),
		Seen:      hasSeenFlag(m.Flags),
	}
	if len(env.From) > 0 {
		msg.From = addressString(env.From[0])
		msg.FromName = env.From[0].Name
	}
	if len(env.ReplyTo) > 0 {
		msg.ReplyTo = addressString(env.ReplyTo[0])
	}
	if len(env.To) > 0 {
		msg.To = addressString(env.To[0])
	}
	if !env.Date.IsZero() {
		msg.Date = env.Date.UTC().Format(time.RFC3339)
	}
	if withBody {
		for _, bs := range m.BodySection {
			if decoded := decodeBody(bs.Bytes); decoded != "" {
				msg.Body = decoded
				break
			}
		}
	}
	return msg
}

// noHTMLTextMarker is returned when a message is HTML-only and the HTML flattens
// to no readable text at all (an image/link-only mail). It keeps an empty render
// distinguishable from a truly empty message.
const noHTMLTextMarker = "[no text content in HTML body]"

// decodeBody parses a full RFC 822 message (BODY[] — headers + body) and returns
// a readable plain-text rendering: the first text/plain part (decoded from its
// Content-Transfer-Encoding and charset by go-message), or, if the message is
// HTML-only, the text/html part stripped to text. Attachments are skipped. The
// result is trimmed and capped at maxBodyBytes.
//
// The overriding contract is "the agent still sees SOMETHING rather than
// nothing", and every way a decode can degrade is surfaced LOUDLY rather than
// silently dropping to a raw blob or an empty string:
//   - an unknown transfer-encoding/charset (go-message hands back a usable reader
//     but leaves the body raw) is prefixed with an explicit "could not be fully
//     decoded" marker;
//   - a corrupt base64/quoted-printable part (partial bytes + read error) keeps
//     the partial content but is suffixed with a truncation marker;
//   - an unrecoverable mid-stream MIME parse error stops the walk and marks the
//     body partial;
//   - a message with no text/plain or text/html part (e.g. text/calendar, or an
//     empty BodySection) falls back to the trimmed raw bytes;
//   - an HTML-only body that strips to nothing returns an explicit marker.
func decodeBody(raw []byte) string {
	mr, headerErr := gomail.CreateReader(bytes.NewReader(raw))
	if mr == nil {
		// Unparseable as MIME. CreateReader returns a nil reader for a hard parse
		// error OR a top-level unknown transfer-encoding — in the latter case the
		// body is real content we could not decode, so mark the degrade instead of
		// passing it off as clean text.
		best := capBody(strings.TrimSpace(string(raw)))
		if isDecodeDegrade(headerErr) {
			return decodeDegradeMarker(headerErr) + best
		}
		return best
	}
	defer mr.Close()

	var plain, htmlBody string
	var partial bool        // an unrecoverable mid-stream parse error cut the walk short
	var truncatedPart bool  // a part body decode returned partial bytes + error
	var sawInlineText bool  // at least one inline text/* part was present
	var sawAttachment bool  // at least one attachment/non-inline part was present
	degradeErr := headerErr // the first unknown charset/encoding we observe (header or part)

	for {
		part, perr := mr.NextPart()
		if errors.Is(perr, io.EOF) {
			break
		}
		if perr != nil {
			if isDecodeDegrade(perr) {
				// Usable part + error: the part body is present but raw/undecoded.
				if degradeErr == nil {
					degradeErr = perr
				}
			} else {
				// Genuine mid-stream failure (not a mere unknown CTE/charset): do NOT
				// treat it as a clean EOF — stop and mark the body partial.
				partial = true
				break
			}
		}
		if part == nil {
			break
		}
		switch h := part.Header.(type) {
		case *gomail.InlineHeader:
			sawInlineText = true
			ct, _, _ := h.ContentType()
			b, rerr := io.ReadAll(part.Body)
			if rerr != nil {
				// A corrupt base64/quoted-printable part returns the bytes decoded so
				// far PLUS an error. Keep the partial content, but never silently
				// truncate — flag it below.
				truncatedPart = true
			}
			switch {
			case strings.EqualFold(ct, "text/plain"):
				if plain == "" {
					plain = string(b)
				}
			case strings.EqualFold(ct, "text/html"):
				if htmlBody == "" {
					htmlBody = string(b)
				}
			}
		default:
			// Attachment (or non-inline part) — drain and skip.
			sawAttachment = true
			_, _ = io.Copy(io.Discard, part.Body)
		}
	}

	var body string
	htmlEmpty := false
	switch {
	case strings.TrimSpace(plain) != "":
		body = plain
	case strings.TrimSpace(htmlBody) != "":
		body = htmlToText(htmlBody)
		if strings.TrimSpace(body) == "" {
			// HTML-only mail that flattens to nothing (image/link-only): return an
			// explicit marker so "empty render" != "empty message".
			body = noHTMLTextMarker
			htmlEmpty = true
		}
	}
	body = strings.TrimSpace(body)

	// Contract fallback: no usable text part was found (no text/plain or text/html
	// — e.g. text/calendar, or an empty BodySection), but the raw bytes carry
	// content. Show the raw content rather than nothing. The HTML-empty marker is
	// itself content, so it is NOT overwritten here.
	attachmentOnly := false
	if body == "" && !htmlEmpty {
		if trimmed := strings.TrimSpace(string(raw)); trimmed != "" {
			body = trimmed
			// A message whose ONLY parts were attachments (no inline text of any
			// kind) has no meaningful text body; flag it so the base64 blob is not
			// mistaken for the message text.
			attachmentOnly = sawAttachment && !sawInlineText
		}
	}

	out := capBody(body)
	if attachmentOnly {
		out = "[no readable text body: message is attachment-only]\n" + out
	}
	if truncatedPart {
		out += "\n[body truncated: decode error]"
	}
	if partial {
		out += "\n[body incomplete: MIME parse error]"
	}
	if isDecodeDegrade(degradeErr) {
		out = decodeDegradeMarker(degradeErr) + out
	}
	return out
}

// isDecodeDegrade reports whether err is an unknown-charset or unknown-encoding
// error from go-message — the kind that leaves a part body raw/undecoded but
// still yields usable content.
func isDecodeDegrade(err error) bool {
	return err != nil && (message.IsUnknownCharset(err) || message.IsUnknownEncoding(err))
}

// decodeDegradeMarker renders the loud prefix for a partially-decoded body,
// naming why decoding could not complete.
func decodeDegradeMarker(err error) string {
	reason := "unknown transfer-encoding or charset"
	switch {
	case message.IsUnknownCharset(err):
		reason = "unknown charset"
	case message.IsUnknownEncoding(err):
		reason = "unknown transfer-encoding"
	}
	return fmt.Sprintf("[body could not be fully decoded: %s]\n", reason)
}

// blockHTMLTags are tags whose start/end should introduce a line break when
// flattening HTML to text, so paragraphs and list items don't run together.
var blockHTMLTags = map[string]bool{
	"p": true, "br": true, "div": true, "tr": true, "li": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"table": true, "ul": true, "ol": true, "blockquote": true, "pre": true,
	"section": true, "article": true, "header": true, "footer": true, "hr": true,
}

// htmlToText strips HTML to readable text using an x/net/html tokenizer walk
// (pure Go, no heavy markdown dep): script/style content is dropped, block-level
// tags become line breaks, entities are decoded, and runs of whitespace are
// collapsed.
func htmlToText(h string) string {
	z := html.NewTokenizer(strings.NewReader(h))
	var sb strings.Builder
	skipDepth := 0 // >0 while inside a <script>/<style> subtree
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break // includes io.EOF
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if tag == "script" || tag == "style" {
				if tt == html.StartTagToken {
					skipDepth++
				}
				continue
			}
			if blockHTMLTags[tag] {
				sb.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if (tag == "script" || tag == "style") && skipDepth > 0 {
				skipDepth--
				continue
			}
			if blockHTMLTags[tag] {
				sb.WriteByte('\n')
			}
		case html.TextToken:
			if skipDepth == 0 {
				sb.Write(z.Text()) // already entity-decoded
			}
		}
	}
	return collapseWhitespace(sb.String())
}

// collapseWhitespace normalises runs of spaces/tabs within each line to a single
// space and collapses three-or-more consecutive newlines to a blank line,
// preserving paragraph structure.
func collapseWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.Join(strings.Fields(ln), " ")
	}
	joined := strings.Join(lines, "\n")
	for strings.Contains(joined, "\n\n\n") {
		joined = strings.ReplaceAll(joined, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(joined)
}

// capBody truncates s to at most maxBodyBytes, backing off to a UTF-8 rune
// boundary and appending an explicit marker naming how many bytes were dropped.
func capBody(s string) string {
	if len(s) <= maxBodyBytes {
		return s
	}
	truncated := s[:maxBodyBytes]
	// Back off any partial rune left at the cut point.
	for len(truncated) > 0 {
		r, size := utf8.DecodeLastRuneInString(truncated)
		if r == utf8.RuneError && size <= 1 {
			truncated = truncated[:len(truncated)-1]
			continue
		}
		break
	}
	removed := len(s) - len(truncated)
	return truncated + fmt.Sprintf("\n…[truncated %d bytes]", removed)
}

func hasSeenFlag(flags []imap.Flag) bool {
	for _, f := range flags {
		if f == imap.FlagSeen {
			return true
		}
	}
	return false
}

func addressString(a imap.Address) string {
	if a.Host != "" {
		return a.Mailbox + "@" + a.Host
	}
	return a.Mailbox
}

// MarkSeen sets the \Seen flag on the message with the given UID.
func (c *Client) MarkSeen(ctx context.Context, uid uint32) error {
	if uid == 0 {
		return fmt.Errorf("email transport: uid is required")
	}
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	uidSet := imap.UIDSetNum(imap.UID(uid))
	storeFlags := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagSeen},
		Silent: true,
	}
	if _, err := runIMAP(ctx, "store", func() (struct{}, error) {
		return struct{}{}, client.Store(uidSet, storeFlags, nil).Close()
	}); err != nil {
		return fmt.Errorf("email transport: mark seen uid %d: %w", uid, err)
	}
	return nil
}

// Send delivers an outbound message via SMTP (STARTTLS on 587/custom, implicit
// TLS on 465).
func (c *Client) Send(_ context.Context, req SendRequest) error {
	to := strings.TrimSpace(req.To)
	if to == "" {
		return fmt.Errorf("email transport: recipient (to) is empty")
	}
	if strings.TrimSpace(req.Body) == "" {
		return fmt.Errorf("email transport: body is empty")
	}
	subject := req.Subject
	if subject == "" {
		subject = "(no subject)"
	}

	smtpAddr := fmt.Sprintf("%s:%d", c.acct.SMTPHost, c.acct.SMTPPort)
	body := buildEmailBody(c.acct.Username, to, subject, req.Body, req.InReplyTo)

	if c.acct.SMTPPort == 465 {
		tlsCfg := &tls.Config{ServerName: c.acct.SMTPHost, MinVersion: tls.VersionTLS12}
		if err := sendSMTPS(smtpAddr, c.acct.Username, to, body, c.acct.Username, c.acct.Password, tlsCfg); err != nil {
			return fmt.Errorf("email transport: SMTPS send: %w", err)
		}
		return nil
	}
	auth := smtp.PlainAuth("", c.acct.Username, c.acct.Password, c.acct.SMTPHost)
	tlsCfg := &tls.Config{ServerName: c.acct.SMTPHost, MinVersion: tls.VersionTLS12}
	if err := sendSMTPWithSTARTTLS(smtpAddr, auth, c.acct.Username, to, body, tlsCfg); err != nil {
		return fmt.Errorf("email transport: STARTTLS send: %w", err)
	}
	return nil
}

// sendSMTPWithSTARTTLS sends an email via SMTP+STARTTLS using net/smtp.
func sendSMTPWithSTARTTLS(addr string, auth smtp.Auth, from, to, body string, tlsCfg *tls.Config) error {
	cl, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer cl.Close()

	if err = cl.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("STARTTLS: %w", err)
	}
	if err = cl.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err = cl.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err = cl.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return w.Close()
}

// sendSMTPS sends an email via implicit TLS (port 465 / SMTPS).
func sendSMTPS(addr, from, to, body, username, password string, tlsCfg *tls.Config) error {
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("TLS dial: %w", err)
	}
	cl, err := smtp.NewClient(conn, tlsCfg.ServerName)
	if err != nil {
		return fmt.Errorf("SMTP client: %w", err)
	}
	defer cl.Close()

	auth := smtp.PlainAuth("", username, password, tlsCfg.ServerName)
	if err = cl.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err = cl.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err = cl.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return w.Close()
}

// buildEmailBody constructs a minimal RFC 5322-compliant message. When inReplyTo
// is set, the In-Reply-To and References headers are added so the message threads.
func buildEmailBody(from, to, subject, text, inReplyTo string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + sanitizeHeader(subject) + "\r\n")
	if inReplyTo != "" {
		sb.WriteString("In-Reply-To: " + sanitizeHeader(inReplyTo) + "\r\n")
		sb.WriteString("References: " + sanitizeHeader(inReplyTo) + "\r\n")
	}
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(text)
	return sb.String()
}

// sanitizeHeader strips CR/LF from a header value to prevent header injection.
func sanitizeHeader(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return v
}
