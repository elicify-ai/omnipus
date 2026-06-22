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
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	defaultIMAPPort = 993
	defaultSMTPPort = 587

	// dialTimeout bounds a single IMAP/SMTP dial so a tool call cannot hang the
	// agent turn indefinitely on an unreachable server.
	dialTimeout = 30 * time.Second
)

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
	To        string `json:"to,omitempty"`
	Subject   string `json:"subject"`
	// Date is the message Date header in RFC 3339 (UTC), best-effort.
	Date string `json:"date,omitempty"`
	// Body is the plain-text body. Populated by ReadMessage; for list/search
	// results it is empty (envelope-only) to keep payloads small.
	Body string `json:"body,omitempty"`
	// Seen reflects the \Seen IMAP flag at fetch time.
	Seen bool `json:"seen"`
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
	// ReadInbox returns up to limit of the most recent INBOX messages
	// (envelope only, newest first). unseenOnly restricts to \Unseen messages.
	ReadInbox(ctx context.Context, limit int, unseenOnly bool) ([]Message, error)
	// Search returns envelope-only matches for a free-text query (matched
	// against subject, from, and body), newest first, up to limit.
	Search(ctx context.Context, query string, limit int) ([]Message, error)
	// ReadMessage fetches a single message (including body) by IMAP UID.
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

// dialIMAP dials the IMAP server over implicit TLS, logs in, and selects INBOX.
// The caller must Close the returned client.
func (c *Client) dialIMAP(ctx context.Context) (*imapclient.Client, error) {
	addr := fmt.Sprintf("%s:%d", c.acct.IMAPHost, c.acct.IMAPPort)
	tlsCfg := &tls.Config{ServerName: c.acct.IMAPHost, MinVersion: tls.VersionTLS12}

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	// imapclient.DialTLS does not take a context; guard the dial with a timeout
	// goroutine so an unreachable server cannot block past dialTimeout.
	type dialResult struct {
		cl  *imapclient.Client
		err error
	}
	ch := make(chan dialResult, 1)
	go func() {
		cl, err := imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: tlsCfg})
		ch <- dialResult{cl, err}
	}()
	var client *imapclient.Client
	select {
	case <-dialCtx.Done():
		return nil, fmt.Errorf("email transport: dial %s: %w", addr, dialCtx.Err())
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("email transport: dial TLS %s: %w", addr, res.err)
		}
		client = res.cl
	}

	if err := client.Login(c.acct.Username, c.acct.Password).Wait(); err != nil {
		client.Close()
		return nil, fmt.Errorf("email transport: login failed: %w", err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		client.Close()
		return nil, fmt.Errorf("email transport: select INBOX: %w", err)
	}
	return client, nil
}

// ReadInbox returns up to limit of the most recent INBOX messages, newest first.
func (c *Client) ReadInbox(ctx context.Context, limit int, unseenOnly bool) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	client, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var crit *imap.SearchCriteria
	if unseenOnly {
		crit = &imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}
	} else {
		crit = &imap.SearchCriteria{}
	}
	searchData, err := client.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("email transport: search inbox: %w", err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return []Message{}, nil
	}
	// Newest first, then cap to limit.
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if len(uids) > limit {
		uids = uids[:limit]
	}
	return c.fetchEnvelopes(client, uids, false)
}

// Search returns envelope-only matches for query, newest first, up to limit.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("email transport: search query is empty")
	}
	client, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	// IMAP OR criteria across Subject / From / body Text. go-imap models OR as a
	// pair, so chain Subject OR (From OR Text).
	crit := &imap.SearchCriteria{
		Or: [][2]imap.SearchCriteria{
			{
				{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: q}}},
				{
					Or: [][2]imap.SearchCriteria{
						{
							{Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: q}}},
							{Body: []string{q}},
						},
					},
				},
			},
		},
	}
	searchData, err := client.UIDSearch(crit, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("email transport: search %q: %w", q, err)
	}
	uids := searchData.AllUIDs()
	if len(uids) == 0 {
		return []Message{}, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] })
	if len(uids) > limit {
		uids = uids[:limit]
	}
	return c.fetchEnvelopes(client, uids, false)
}

// ReadMessage fetches a single message (with body) by UID.
func (c *Client) ReadMessage(ctx context.Context, uid uint32) (*Message, error) {
	if uid == 0 {
		return nil, fmt.Errorf("email transport: uid is required")
	}
	client, err := c.dialIMAP(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	msgs, err := c.fetchEnvelopes(client, []imap.UID{imap.UID(uid)}, true)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("email transport: message uid %d not found", uid)
	}
	return &msgs[0], nil
}

// fetchEnvelopes fetches envelopes (and optionally the text body) for the given
// UIDs and converts them to transport Messages. Results follow the order of the
// supplied uids (already newest-first for list/search callers).
func (c *Client) fetchEnvelopes(client *imapclient.Client, uids []imap.UID, withBody bool) ([]Message, error) {
	uidSet := imap.UIDSetNum(uids...)
	opts := &imap.FetchOptions{Envelope: true, Flags: true, UID: true}
	if withBody {
		opts.BodySection = []*imap.FetchItemBodySection{{Specifier: imap.PartSpecifierText}}
	}
	fetched, err := client.Fetch(uidSet, opts).Collect()
	if err != nil {
		return nil, fmt.Errorf("email transport: fetch: %w", err)
	}

	byUID := make(map[imap.UID]*imapclient.FetchMessageBuffer, len(fetched))
	for _, m := range fetched {
		byUID[m.UID] = m
	}
	out := make([]Message, 0, len(uids))
	for _, uid := range uids {
		m, ok := byUID[uid]
		if !ok || m.Envelope == nil {
			continue
		}
		out = append(out, bufferToMessage(m, withBody))
	}
	return out, nil
}

// bufferToMessage converts a fetched IMAP buffer to a transport Message.
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
	if len(env.To) > 0 {
		msg.To = addressString(env.To[0])
	}
	if !env.Date.IsZero() {
		msg.Date = env.Date.UTC().Format(time.RFC3339)
	}
	if withBody && len(m.BodySection) > 0 {
		for _, bs := range m.BodySection {
			msg.Body = strings.TrimSpace(string(bs.Bytes))
			if msg.Body != "" {
				break
			}
		}
	}
	return msg
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
	client, err := c.dialIMAP(ctx)
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
	if err := client.Store(uidSet, storeFlags, nil).Close(); err != nil {
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
