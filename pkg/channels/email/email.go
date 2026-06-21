// Package email implements an email channel for Omnipus: IMAP-inbound (TLS)
// and SMTP-outbound (TLS). Inbound mail is fetched via IDLE; outbound replies
// go to the From address of the originating message.
//
// Start is non-blocking: it spawns the IMAP receive loop in a background
// goroutine and returns immediately (mirroring telegram/irc/weixin), so the
// channel Manager — which calls Start while holding its mutex — never deadlocks.
//
// Failure classification (SEC-23 / Spec-2 FR-2.8), evaluated inside the loop:
//   - Auth failure (AUTHENTICATE/LOGIN error) → permanent; the background loop
//     logs and exits, leaving the channel idle.
//   - Unreachable server (dial error, TLS error) → transient; exponential
//     backoff and re-dial from the run loop.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/identity"
)

const (
	defaultIMAPPort = 993
	defaultSMTPPort = 587

	// Backoff bounds for transient (network) errors.
	backoffMin = 5 * time.Second
	backoffMax = 5 * time.Minute

	// idleTimeout is the maximum duration of a single IDLE command.
	// RFC 2177 recommends servers keepalive at ≤29 min; we re-issue at 20 min.
	idleTimeout = 20 * time.Minute

	// pollInterval is used when the server does not advertise IDLE capability.
	pollInterval = 60 * time.Second

	// maxAuthRetries — auth errors are permanent; after this many we give up.
	maxAuthRetries = 1

	// healthySessionThreshold — when a runLoop session stays connected at least
	// this long before erroring, we treat the connection/auth as having been
	// healthy and reset the backoff + auth-retry counters. Without this, a single
	// long-lived session that eventually drops would inherit a maxed-out backoff
	// (or a near-exhausted auth-retry budget) from transient blips hours earlier.
	healthySessionThreshold = 60 * time.Second
)

// ErrAuthFailure is returned when the IMAP server rejects our credentials.
// It is permanent — callers must not retry.
var ErrAuthFailure = fmt.Errorf("email: authentication failed (permanent)")

// EmailChannel implements channels.Channel for email.
type EmailChannel struct {
	*channels.BaseChannel
	cfg      config.EmailConfig
	password string // resolved at construction from PasswordRef

	mu     sync.Mutex
	client *imapclient.Client // protected by mu; nil when not connected

	ctx    context.Context
	cancel context.CancelFunc

	// done is closed when the background run loop exits. Stop waits on it so the
	// IMAP client is fully torn down (and m.client cleared) before Stop returns.
	done chan struct{}

	// runLoopFn is the per-session dial+idle function driven by run. It defaults
	// to c.runLoop in Start; tests may override it to inject deterministic
	// permanent/transient errors without standing up a real IMAP server (N4).
	runLoopFn func(context.Context) error
}

// NewEmailChannel creates a new EmailChannel. Returns an error (wrapping
// ErrAuthFailure) if credentials are obviously invalid at construction time.
// Actual auth verification happens on first IMAP dial in Start.
func NewEmailChannel(
	cfg config.EmailConfig,
	secrets credentials.SecretBundle,
	messageBus *bus.MessageBus,
) (*EmailChannel, error) {
	if cfg.IMAPHost == "" {
		return nil, fmt.Errorf("email channel: imap_host is required")
	}
	if cfg.SMTPHost == "" {
		return nil, fmt.Errorf("email channel: smtp_host is required")
	}
	if cfg.Username == "" {
		return nil, fmt.Errorf("email channel: username is required")
	}

	password := secrets.GetString(cfg.PasswordRef)
	if password == "" {
		return nil, fmt.Errorf("email channel: password credential not resolved (ref=%q): %w",
			cfg.PasswordRef, ErrAuthFailure)
	}

	if cfg.IMAPPort == 0 {
		cfg.IMAPPort = defaultIMAPPort
	}
	if cfg.SMTPPort == 0 {
		cfg.SMTPPort = defaultSMTPPort
	}

	base := channels.NewBaseChannel("email", cfg, messageBus, nil)

	return &EmailChannel{
		BaseChannel: base,
		cfg:         cfg,
		password:    password,
	}, nil
}

// Start spawns the IMAP receive loop in a background goroutine and returns
// immediately. It mirrors the non-blocking contract of the telegram/irc/weixin
// channels: the Manager calls Start synchronously while holding its channel
// mutex, so a blocking Start would deadlock the entire channel subsystem.
//
// Connection and auth happen asynchronously inside run; permanent auth failures
// are logged and stop the loop (the channel goes idle, exactly as it would for
// any other channel whose background loop terminates).
func (c *EmailChannel) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	c.SetRunning(true)

	if c.runLoopFn == nil {
		c.runLoopFn = c.runLoop
	}

	slog.Info("email channel: starting", "imap_host", c.cfg.IMAPHost, "username", c.cfg.Username)

	go c.run(c.ctx)
	return nil
}

// run drives the IMAP receive loop until ctx is canceled or a permanent error
// occurs. It owns the IMAP client lifecycle: on exit it clears the running flag
// and closes c.done so Stop can wait for a clean teardown.
func (c *EmailChannel) run(ctx context.Context) {
	defer close(c.done)
	defer c.SetRunning(false)

	backoff := backoffMin
	authAttempts := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sessionStart := time.Now()
		err := c.runLoopFn(ctx)
		if err == nil {
			// Normal shutdown via ctx cancellation.
			return
		}

		// A session that stayed up long enough indicates the connection and auth
		// were healthy; reset the transient backoff and auth-retry counters so an
		// eventual drop after a long healthy run does not inherit a stale, maxed-out
		// backoff or a near-exhausted auth budget from earlier blips.
		if time.Since(sessionStart) >= healthySessionThreshold {
			backoff = backoffMin
			authAttempts = 0
		}

		// Classify: permanent vs. transient.
		if isAuthError(err) {
			authAttempts++
			if authAttempts >= maxAuthRetries {
				slog.Error("email channel: authentication failed (permanent), stopping channel",
					"error", err)
				return
			}
		}

		slog.Warn("email channel: transient error, retrying after backoff",
			"error", err, "backoff", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

// runLoop dials the IMAP server, logs in, selects INBOX, and spins in an
// IDLE/poll loop until ctx is canceled or an error breaks the connection.
func (c *EmailChannel) runLoop(ctx context.Context) error {
	addr := fmt.Sprintf("%s:%d", c.cfg.IMAPHost, c.cfg.IMAPPort)

	// TLS-only (IMAPS). STARTTLS is not supported — TLS is required per spec.
	tlsCfg := &tls.Config{ServerName: c.cfg.IMAPHost}
	client, err := imapclient.DialTLS(addr, &imapclient.Options{
		TLSConfig: tlsCfg,
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				// New mail arrived during IDLE — logged for observability.
				if data.NumMessages != nil {
					slog.Debug("email channel: new mail notification from server",
						"num_messages", *data.NumMessages)
				}
			},
		},
	})
	if err != nil {
		return fmt.Errorf("email channel: dial TLS %s: %w", addr, err)
	}
	defer func() {
		c.mu.Lock()
		c.client = nil
		c.mu.Unlock()
		client.Close()
	}()

	c.mu.Lock()
	c.client = client
	c.mu.Unlock()

	// Login with the resolved credentials.
	if err := client.Login(c.cfg.Username, c.password).Wait(); err != nil {
		return fmt.Errorf("email channel: login failed (auth): %w", err)
	}

	slog.Info("email channel: authenticated", "username", c.cfg.Username, "server", addr)

	// Select INBOX.
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("email channel: select INBOX: %w", err)
	}

	// Initial fetch of all unseen messages before entering the IDLE loop.
	if err := c.fetchUnseen(ctx, client); err != nil {
		slog.Warn("email channel: initial unseen fetch failed", "error", err)
	}

	// Enter IDLE / poll loop.
	return c.idleLoop(ctx, client)
}

// idleLoop uses IMAP IDLE (or falls back to polling) to wait for new messages.
func (c *EmailChannel) idleLoop(ctx context.Context, client *imapclient.Client) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		idleCmd, err := client.Idle()
		if err != nil {
			// IDLE not available or connection reset — fall back to polling.
			slog.Debug("email channel: IDLE not available, polling",
				"error", err, "interval", pollInterval)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(pollInterval):
			}
		} else {
			// Wait up to idleTimeout for server push or context cancellation.
			idleDone := make(chan error, 1)
			go func() { idleDone <- idleCmd.Wait() }()

			timer := time.NewTimer(idleTimeout)
			select {
			case <-ctx.Done():
				timer.Stop()
				if closeErr := idleCmd.Close(); closeErr != nil {
					slog.Debug("email channel: IDLE close on shutdown", "error", closeErr)
				}
				<-idleDone
				return nil
			case <-timer.C:
				// Re-issue IDLE per RFC 2177 §3.
				if closeErr := idleCmd.Close(); closeErr != nil {
					slog.Debug("email channel: IDLE close on timer", "error", closeErr)
				}
				<-idleDone
			case idleErr := <-idleDone:
				timer.Stop()
				if idleErr != nil {
					return fmt.Errorf("email channel: IDLE error: %w", idleErr)
				}
			}
		}

		// Fetch any new unseen messages after waking.
		if err := c.fetchUnseen(ctx, client); err != nil {
			return fmt.Errorf("email channel: fetch unseen after wake: %w", err)
		}
	}
}

// fetchUnseen fetches all UNSEEN messages in the selected mailbox and
// dispatches them to the MessageBus as inbound messages.
func (c *EmailChannel) fetchUnseen(ctx context.Context, client *imapclient.Client) error {
	// Search for unseen messages (sequence numbers).
	searchData, err := client.Search(&imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}, nil).Wait()
	if err != nil {
		return fmt.Errorf("search unseen: %w", err)
	}

	seqNums := searchData.AllSeqNums()
	if len(seqNums) == 0 {
		return nil
	}

	seqSet := imap.SeqSetNum(seqNums...)
	bodySec := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierText}
	messages, err := client.Fetch(seqSet, &imap.FetchOptions{
		Envelope:    true,
		Flags:       true,
		BodySection: []*imap.FetchItemBodySection{bodySec},
	}).Collect()
	if err != nil {
		return fmt.Errorf("fetch unseen messages: %w", err)
	}

	// Only mark messages \Seen once they were successfully dispatched to the bus.
	// Marking the whole seqSet seen unconditionally would silently drop any message
	// whose dispatch failed (it would never be re-fetched on the next UNSEEN scan).
	dispatched := make([]uint32, 0, len(messages))
	for _, msg := range messages {
		if err := c.dispatchBufferedMessage(ctx, msg); err != nil {
			slog.Warn("email channel: dispatch message error; leaving message UNSEEN for retry",
				"seq_num", msg.SeqNum, "error", err)
			continue
		}
		dispatched = append(dispatched, msg.SeqNum)
	}

	if len(dispatched) == 0 {
		return nil
	}

	// Mark only the successfully-dispatched messages as seen.
	seenSet := imap.SeqSetNum(dispatched...)
	storeFlags := imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagSeen},
		Silent: true,
	}
	if err := client.Store(seenSet, &storeFlags, nil).Close(); err != nil {
		slog.Warn("email channel: mark seen failed", "error", err)
	}

	return nil
}

// dispatchBufferedMessage converts a collected IMAP message to an InboundMessage
// and publishes it on the MessageBus.
func (c *EmailChannel) dispatchBufferedMessage(ctx context.Context, msg *imapclient.FetchMessageBuffer) error {
	if msg.Envelope == nil {
		return nil
	}
	env := msg.Envelope

	// Extract From address.
	var fromAddr string
	if len(env.From) > 0 {
		addr := env.From[0]
		if addr.Host != "" {
			fromAddr = addr.Mailbox + "@" + addr.Host
		} else {
			fromAddr = addr.Mailbox
		}
	}
	if fromAddr == "" {
		slog.Debug("email channel: message has no From address, skipping")
		return nil
	}

	// Subject is stored in metadata for context.
	subject := strings.TrimSpace(env.Subject)

	// Read the text body from the first body section.
	var content string
	if len(msg.BodySection) > 0 {
		content = strings.TrimSpace(string(msg.BodySection[0].Bytes))
	}
	if content == "" {
		// Fall back to subject if body is empty.
		content = subject
	}
	if content == "" {
		slog.Debug("email channel: message has no content, skipping", "from", fromAddr)
		return nil
	}

	// chatID = sender address so outbound reply is routed back correctly.
	chatID := fromAddr
	messageID := fmt.Sprintf("email-%s-%d", fromAddr, env.Date.UnixNano())

	sender := bus.SenderInfo{
		Platform:    "email",
		PlatformID:  fromAddr,
		CanonicalID: identity.BuildCanonicalID("email", fromAddr),
		Username:    fromAddr,
		DisplayName: displayName(env.From),
	}

	metadata := map[string]string{
		"platform":    "email",
		"subject":     subject,
		"imap_server": c.cfg.IMAPHost,
	}
	if env.MessageID != "" {
		metadata["message_id"] = env.MessageID
	}

	slog.Debug("email channel: dispatching inbound message",
		"from", fromAddr, "subject", subject, "content_len", len(content))

	c.HandleMessage(
		ctx,
		bus.Peer{Kind: bus.PeerDirect, ID: fromAddr},
		messageID,
		fromAddr,
		chatID,
		content,
		nil,
		metadata,
		sender,
	)
	return nil
}

// Stop cancels the run loop and waits for it to tear down the IMAP connection.
//
// The run loop is the sole owner of the *imapclient.Client lifecycle: runLoop's
// deferred cleanup clears c.client and calls Close on it. Stop therefore only
// cancels the context (which unblocks idleLoop) and waits on c.done — it must
// not close the client itself, or it would race the run loop into a double-close
// of the same *imapclient.Client.
func (c *EmailChannel) Stop(stopCtx context.Context) error {
	slog.Info("email channel: stopping")
	if c.cancel != nil {
		c.cancel()
	}
	// Wait for the background run loop to exit and release the IMAP client.
	// Bound the wait by the caller's shutdown context so Stop can never hang.
	if c.done != nil {
		select {
		case <-c.done:
		case <-stopCtx.Done():
			slog.Warn("email channel: stop timed out waiting for run loop to exit",
				"error", stopCtx.Err())
		}
	}
	c.SetRunning(false)
	slog.Info("email channel: stopped")
	return nil
}

// Send delivers an outbound message via SMTP. The ChatID is the recipient
// email address.
func (c *EmailChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return channels.ErrNotRunning
	}

	to := msg.ChatID
	if to == "" {
		return fmt.Errorf("email send: chat_id (recipient) is empty: %w", channels.ErrSendFailed)
	}
	if strings.TrimSpace(msg.Content) == "" {
		return nil
	}

	smtpAddr := fmt.Sprintf("%s:%d", c.cfg.SMTPHost, c.cfg.SMTPPort)
	from := c.cfg.Username
	body := buildEmailBody(from, to, "Re: Omnipus", msg.Content)

	if c.cfg.SMTPPort == 465 {
		// SMTPS — implicit TLS.
		tlsCfg := &tls.Config{ServerName: c.cfg.SMTPHost}
		if err := sendSMTPS(smtpAddr, from, to, body, c.cfg.Username, c.password, tlsCfg); err != nil {
			slog.Error("email channel: SMTPS send failed", "to", to, "error", err)
			return fmt.Errorf("email send (SMTPS): %w", err)
		}
	} else {
		// STARTTLS (port 587 or custom).
		auth := smtp.PlainAuth("", c.cfg.Username, c.password, c.cfg.SMTPHost)
		tlsCfg := &tls.Config{ServerName: c.cfg.SMTPHost}
		if err := sendSMTPWithSTARTTLS(smtpAddr, auth, from, to, body, tlsCfg); err != nil {
			slog.Error("email channel: STARTTLS send failed", "to", to, "error", err)
			return fmt.Errorf("email send (STARTTLS): %w", err)
		}
	}

	slog.Debug("email channel: message sent", "to", to, "content_len", len(msg.Content))
	return nil
}

// sendSMTPWithSTARTTLS sends an email via SMTP+STARTTLS using net/smtp.
func sendSMTPWithSTARTTLS(addr string, auth smtp.Auth, from, to, body string, tlsCfg *tls.Config) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err := c.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("STARTTLS: %w", err)
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
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
	c, err := smtp.NewClient(conn, tlsCfg.ServerName)
	if err != nil {
		return fmt.Errorf("SMTP client: %w", err)
	}
	defer c.Close()

	auth := smtp.PlainAuth("", username, password, tlsCfg.ServerName)
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := io.WriteString(w, body); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return w.Close()
}

// buildEmailBody constructs a minimal RFC 5322-compliant email body string.
func buildEmailBody(from, to, subject, text string) string {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(text)
	return sb.String()
}

// isAuthError returns true when err indicates an authentication failure.
// For go-imap/v2, login errors carry substrings like "login failed" or
// "[AUTHENTICATIONFAILED]". We match case-insensitively.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "login failed") ||
		strings.Contains(msg, "authenticate failed") ||
		strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "authenticationfailed") ||
		strings.Contains(msg, "invalid credentials") ||
		strings.Contains(msg, "bad credentials")
}

// displayName returns a best-effort display name from an IMAP address slice.
func displayName(addrs []imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	if addrs[0].Name != "" {
		return addrs[0].Name
	}
	if addrs[0].Host != "" {
		return addrs[0].Mailbox + "@" + addrs[0].Host
	}
	return addrs[0].Mailbox
}
