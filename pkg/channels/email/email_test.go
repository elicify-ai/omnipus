package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
)

// --- Unit tests for NewEmailChannel construction ---

// TestNewEmailChannel_MissingIMAPHost verifies that NewEmailChannel returns an error
// when imap_host is not configured.
//
// BDD:
//
//	Given a config with no imap_host,
//	When NewEmailChannel is called,
//	Then an error is returned.
func TestNewEmailChannel_MissingIMAPHost(t *testing.T) {
	cfg := config.EmailConfig{
		SMTPHost: "smtp.example.com",
		Username: "bot@example.com",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:password": "secret"}
	cfg.PasswordRef = "cred:password"

	_, err := NewEmailChannel(cfg, secrets, b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "imap_host")
}

// TestNewEmailChannel_MissingSMTPHost verifies that NewEmailChannel returns an error
// when smtp_host is not configured.
//
// BDD:
//
//	Given a config with no smtp_host,
//	When NewEmailChannel is called,
//	Then an error is returned.
func TestNewEmailChannel_MissingSMTPHost(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost: "imap.example.com",
		Username: "bot@example.com",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:password": "secret"}
	cfg.PasswordRef = "cred:password"

	_, err := NewEmailChannel(cfg, secrets, b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "smtp_host")
}

// TestNewEmailChannel_MissingUsername verifies that NewEmailChannel returns an error
// when username is not configured.
//
// BDD:
//
//	Given a config with no username,
//	When NewEmailChannel is called,
//	Then an error is returned.
func TestNewEmailChannel_MissingUsername(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost: "imap.example.com",
		SMTPHost: "smtp.example.com",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:password": "secret"}
	cfg.PasswordRef = "cred:password"

	_, err := NewEmailChannel(cfg, secrets, b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "username")
}

// TestNewEmailChannel_MissingPassword verifies that NewEmailChannel returns an error
// (wrapping ErrAuthFailure) when the credential ref resolves to an empty string.
//
// BDD:
//
//	Given a config with a password_ref that resolves to "",
//	When NewEmailChannel is called,
//	Then an error wrapping ErrAuthFailure is returned.
func TestNewEmailChannel_MissingPassword(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:nonexistent",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{} // empty bundle

	_, err := NewEmailChannel(cfg, secrets, b)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAuthFailure),
		"missing credential must return ErrAuthFailure, got: %v", err)
}

// TestNewEmailChannel_DefaultPorts verifies that default port values (993 / 587)
// are applied when the caller leaves IMAPPort and SMTPPort as zero.
//
// BDD:
//
//	Given a fully valid config with IMAPPort=0 and SMTPPort=0,
//	When NewEmailChannel is called,
//	Then channel.cfg.IMAPPort == 993 and channel.cfg.SMTPPort == 587.
func TestNewEmailChannel_DefaultPorts(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
		// IMAPPort / SMTPPort intentionally zero.
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	assert.Equal(t, defaultIMAPPort, ch.cfg.IMAPPort, "default IMAP port must be 993")
	assert.Equal(t, defaultSMTPPort, ch.cfg.SMTPPort, "default SMTP port must be 587")
}

// TestNewEmailChannel_ExplicitPorts verifies that caller-supplied ports are honored.
//
// BDD:
//
//	Given a config with IMAPPort=143 and SMTPPort=465,
//	When NewEmailChannel is called,
//	Then channel.cfg.IMAPPort == 143 and channel.cfg.SMTPPort == 465.
func TestNewEmailChannel_ExplicitPorts(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
		IMAPPort:    143,
		SMTPPort:    465,
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	assert.Equal(t, 143, ch.cfg.IMAPPort, "explicit IMAP port must be preserved")
	assert.Equal(t, 465, ch.cfg.SMTPPort, "explicit SMTP port must be preserved")
}

// TestNewEmailChannel_ValidConfig verifies that a channel is constructed without
// error when all required fields are present.
func TestNewEmailChannel_ValidConfig(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "email", ch.Name())
}

// --- Unit tests for isAuthError ---

func TestIsAuthError_KnownStrings(t *testing.T) {
	authErrors := []string{
		"LOGIN FAILED",
		"authenticate failed: invalid credentials",
		"[AUTHENTICATIONFAILED] Bad credentials",
		"authentication failed",
	}
	for _, msg := range authErrors {
		t.Run(msg, func(t *testing.T) {
			assert.True(t, isAuthError(errors.New(msg)),
				"expected isAuthError to be true for: %q", msg)
		})
	}
}

func TestIsAuthError_TransientStrings(t *testing.T) {
	transientErrors := []string{
		"connection refused",
		"dial tcp: no such host",
		"TLS handshake failed",
		"io.EOF",
	}
	for _, msg := range transientErrors {
		t.Run(msg, func(t *testing.T) {
			assert.False(t, isAuthError(errors.New(msg)),
				"expected isAuthError to be false for: %q", msg)
		})
	}
}

func TestIsAuthError_Nil(t *testing.T) {
	assert.False(t, isAuthError(nil))
}

// --- Unit tests for buildEmailBody ---

func TestBuildEmailBody_ContainsRequiredHeaders(t *testing.T) {
	body := buildEmailBody("from@example.com", "to@example.com", "Test Subject", "Hello body")
	assert.Contains(t, body, "From: from@example.com\r\n")
	assert.Contains(t, body, "To: to@example.com\r\n")
	assert.Contains(t, body, "Subject: Test Subject\r\n")
	assert.Contains(t, body, "MIME-Version: 1.0\r\n")
	assert.Contains(t, body, "Content-Type: text/plain; charset=UTF-8\r\n")
	assert.Contains(t, body, "\r\n\r\n")
	assert.Contains(t, body, "Hello body")
}

func TestBuildEmailBody_HeadersBeforeBody(t *testing.T) {
	body := buildEmailBody("a@b.com", "c@d.com", "S", "the body text")
	// The blank line (header/body separator) must come before the body.
	idx := strings.Index(body, "\r\n\r\n")
	bodyStart := strings.Index(body, "the body text")
	assert.Greater(t, bodyStart, idx, "body must come after header separator")
}

// --- Unit tests for displayName ---

func TestDisplayName_EmptySlice(t *testing.T) {
	assert.Equal(t, "", displayName(nil))
}

// --- Unit tests for Stop when not started ---

// TestStop_WhenNotStarted verifies that Stop on a non-started channel returns nil
// (idempotent, no panic).
//
// BDD:
//
//	Given a newly constructed email channel that has never been started,
//	When Stop is called,
//	Then nil is returned without panic.
func TestStop_WhenNotStarted(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Must not panic.
	err = ch.Stop(context.Background())
	require.NoError(t, err)
}

// TestSend_WhenNotRunning verifies that Send returns channels.ErrNotRunning
// when the channel is not in the running state.
//
// BDD:
//
//	Given a channel that has not been started,
//	When Send is called,
//	Then channels.ErrNotRunning is returned.
func TestSend_WhenNotRunning(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	err = ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "user@example.com",
		Content: "hello",
	})
	assert.ErrorIs(t, err, channels.ErrNotRunning)
}

// --- Blocker 1 regression: Start must be non-blocking ---

// TestStart_ReturnsPromptly_RunLoopRunsInBackground verifies the CRITICAL
// release-blocker fix: Start spawns the IMAP receive loop in a background
// goroutine and returns immediately, instead of blocking until ctx-cancel or a
// permanent error.
//
// The channel Manager calls channel.Start(ctx) SYNCHRONOUSLY while holding its
// channel mutex (m.mu). A blocking Start therefore deadlocks the entire channel
// subsystem the moment an email channel is enabled. This test points the channel
// at an unreachable IMAP host so runLoop fails to dial and enters its backoff
// loop — exactly the steady state a real (mis)configured channel reaches.
//
// BDD:
//
//	Given a configured email channel with an unreachable IMAP host,
//	When Start(ctx) is called,
//	Then it returns nil promptly (it does NOT block on the receive loop),
//	And the channel reports running with the loop executing in the background,
//	And Stop returns promptly after canceling the loop.
func TestStart_ReturnsPromptly_RunLoopRunsInBackground(t *testing.T) {
	cfg := config.EmailConfig{
		// 127.0.0.1:1 is not a listening IMAPS server, so DialTLS fails fast and
		// the run loop enters transient backoff — it never blocks Start.
		IMAPHost:    "127.0.0.1",
		IMAPPort:    1,
		SMTPHost:    "127.0.0.1",
		SMTPPort:    1,
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start MUST return promptly. Run it on a watchdog so a regression (blocking
	// Start) fails loudly instead of hanging the whole test binary.
	startReturned := make(chan error, 1)
	go func() { startReturned <- ch.Start(ctx) }()

	select {
	case err := <-startReturned:
		require.NoError(t, err, "Start must return nil immediately (non-blocking)")
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within 2s — it is blocking (the original deadlock bug)")
	}

	// The background run loop must be live: SetRunning(true) is set by Start, and
	// the loop only clears it on exit. Poll briefly to confirm it is running.
	assert.True(t, ch.IsRunning(), "channel must report running after Start")

	// Confirm the loop is genuinely in the background and reacts to cancellation:
	// Stop cancels the ctx and waits for run() to close c.done. This must return
	// promptly (the loop is parked in its backoff select, which honors ctx.Done).
	stopReturned := make(chan error, 1)
	go func() { stopReturned <- ch.Stop(context.Background()) }()
	select {
	case err := <-stopReturned:
		require.NoError(t, err, "Stop must return nil")
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return within 3s — run loop failed to exit on cancel")
	}

	assert.False(t, ch.IsRunning(), "channel must not report running after Stop")
}

// TestStop_BoundedByContext verifies that Stop never hangs indefinitely waiting
// for the run loop: if the caller's shutdown context expires first, Stop returns.
//
// This guards the Stop teardown path (cancel + wait on c.done, bounded by the
// caller ctx) against a run loop that is slow to release the IMAP client.
func TestStop_BoundedByContext(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "127.0.0.1",
		IMAPPort:    1,
		SMTPHost:    "127.0.0.1",
		SMTPPort:    1,
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	// Simulate a started channel whose run loop is wedged and never releases
	// c.done. We DO NOT call Start here (no real run loop), so nothing else will
	// ever close c.done — Stop must fall back to its context deadline. cancel is a
	// no-op so the wait-on-done branch is exercised, not the cancel path.
	ch.cancel = func() {}
	ch.done = make(chan struct{}) // never closed

	stopCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- ch.Stop(stopCtx) }()
	select {
	case err := <-done:
		require.NoError(t, err, "Stop must return nil even when bounded by an expiring ctx")
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not honor its shutdown context deadline")
	}
}

// TestEmailChannel_ImplementsChannelInterface verifies at compile time that
// EmailChannel satisfies the channels.Channel interface.
var _ channels.Channel = (*EmailChannel)(nil)

// TestErrAuthFailure_IsSentinel verifies that ErrAuthFailure is a non-nil error
// and has the expected message substring.
func TestErrAuthFailure_IsSentinel(t *testing.T) {
	require.NotNil(t, ErrAuthFailure)
	assert.Contains(t, ErrAuthFailure.Error(), "authentication failed")
}

// TestChannelType verifies the channel type string.
func TestChannelType(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	assert.Equal(t, "email", ch.Name())
}

// TestBackoffConstants verifies the declared backoff bounds are sane.
func TestBackoffConstants(t *testing.T) {
	assert.LessOrEqual(t, backoffMin, backoffMax)
	assert.Greater(t, backoffMin, time.Duration(0))
	assert.Greater(t, backoffMax, backoffMin)
}

// ---------------------------------------------------------------------------
// Spec-2 integration tests (#6 IMAP/SMTP round-trip, #7 IMAP-down degraded
// boot, N4 auth-failure vs transient distinction).
//
// A real IMAP/SMTP server is not feasible in the ephemeral CI environment, so
// these tests exercise the message-handling pipeline with mock connections and
// injected run-loop functions, exactly as the task spec allows.
// ---------------------------------------------------------------------------

// --- #6: IMAP/SMTP round-trip (message processing pipeline) ---

// TestEmail_ReceivePipeline_IMAPMessageToBus verifies the inbound (IMAP)
// message-processing pipeline: a collected IMAP FetchMessageBuffer is converted
// to a bus.InboundMessage with the correct sender, subject, content, chatID and
// metadata, and published on the MessageBus.
//
// BDD:
//
//	Given a constructed email channel and a simulated IMAP fetch buffer,
//	When dispatchBufferedMessage is called,
//	Then an InboundMessage appears on the bus with from-address as chatID,
//	And the subject is stored in metadata,
//	And the body text is the message content,
//	And the message_id metadata is propagated when present.
func TestEmail_ReceivePipeline_IMAPMessageToBus(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	// Simulate an IMAP FETCH response: an envelope + a body section.
	msg := &imapclient.FetchMessageBuffer{
		SeqNum: 42,
		Envelope: &imap.Envelope{
			Subject:   "Hello from afar",
			MessageID: "<abc123@example.com>",
			From:      []imap.Address{{Name: "Alice", Mailbox: "alice", Host: "example.com"}},
		},
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: []byte("This is the email body text.\r\n")},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, ch.dispatchBufferedMessage(ctx, msg))

	select {
	case got := <-b.InboundChan():
		assert.Equal(t, "email", got.Channel, "channel name must be email")
		assert.Equal(t, "alice@example.com", got.ChatID, "chatID must be the sender address for reply routing")
		assert.Equal(t, "alice@example.com", got.Sender.PlatformID)
		assert.Equal(t, "alice@example.com", got.Sender.Username)
		assert.Equal(t, "Alice", got.Sender.DisplayName, "display name must come from the From header")
		assert.Contains(t, got.Content, "This is the email body text", "content must come from the body section")
		assert.Equal(t, "Hello from afar", got.Metadata["subject"], "subject must be in metadata")
		assert.Equal(t, "<abc123@example.com>", got.Metadata["message_id"], "message_id must be propagated")
		assert.Equal(t, "imap.example.com", got.Metadata["imap_server"])
	case <-time.After(500 * time.Millisecond):
		t.Fatal("inbound message was not published on the bus within 500ms")
	}
}

// TestEmail_ReceivePipeline_NoFromAddressSkipped verifies that a message with
// no From address is silently skipped (no bus publish, no error).
//
// BDD:
//
//	Given an IMAP fetch buffer with an empty From list,
//	When dispatchBufferedMessage is called,
//	Then nil is returned and no message is published on the bus.
func TestEmail_ReceivePipeline_NoFromAddressSkipped(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	msg := &imapclient.FetchMessageBuffer{
		SeqNum:      1,
		Envelope:    &imap.Envelope{Subject: "no sender"},
		BodySection: []imapclient.FetchBodySectionBuffer{{Bytes: []byte("body")}},
	}

	require.NoError(t, ch.dispatchBufferedMessage(context.Background(), msg))

	select {
	case <-b.InboundChan():
		t.Fatal("a message with no From address must not be published")
	case <-time.After(100 * time.Millisecond):
		// expected: no message
	}
}

// TestEmail_ReceivePipeline_EmptyBodyFallsBackToSubject verifies that when the
// body section is empty, the subject is used as the message content.
//
// BDD:
//
//	Given an IMAP fetch buffer with an empty body section but a non-empty subject,
//	When dispatchBufferedMessage is called,
//	Then the published message content equals the subject.
func TestEmail_ReceivePipeline_EmptyBodyFallsBackToSubject(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	msg := &imapclient.FetchMessageBuffer{
		SeqNum: 7,
		Envelope: &imap.Envelope{
			Subject: "subject-only message",
			From:    []imap.Address{{Mailbox: "bob", Host: "example.com"}},
		},
		// no BodySection
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, ch.dispatchBufferedMessage(ctx, msg))

	select {
	case got := <-b.InboundChan():
		assert.Contains(t, got.Content, "subject-only message", "content must fall back to subject when body is empty")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fallback message was not published on the bus")
	}
}

// TestEmail_SendPipeline_HeaderConstruction verifies the outbound (SMTP) body
// construction: buildEmailBody produces a complete RFC 5322 message with the
// configured From (Username) and the recipient To, ready for SMTP DATA.
//
// BDD:
//
//	Given a from address, recipient, subject and text,
//	When buildEmailBody is called,
//	Then the body contains From, To, Subject, MIME-Version and Content-Type headers,
//	And the headers precede the body text,
//	And the body text is the exact supplied content.
func TestEmail_SendPipeline_HeaderConstruction(t *testing.T) {
	from := "bot@example.com"
	to := "user@example.com"
	subject := "Re: Omnipus"
	text := "Hello, this is the reply."

	body := buildEmailBody(from, to, subject, text)

	// Header block must contain all required RFC 5322 headers.
	assert.Contains(t, body, "From: bot@example.com\r\n")
	assert.Contains(t, body, "To: user@example.com\r\n")
	assert.Contains(t, body, "Subject: Re: Omnipus\r\n")
	assert.Contains(t, body, "MIME-Version: 1.0\r\n")
	assert.Contains(t, body, "Content-Type: text/plain; charset=UTF-8\r\n")

	// Header/body separator must precede the body text.
	sepIdx := strings.Index(body, "\r\n\r\n")
	bodyIdx := strings.Index(body, text)
	require.Greater(t, sepIdx, -1, "must contain a header/body separator")
	require.Greater(t, bodyIdx, sepIdx, "body text must come after the separator")

	// The body text must appear verbatim (no mutation).
	assert.Contains(t, body, text)
}

// TestEmail_SendPipeline_PortRouting verifies that Send selects the correct
// SMTP transport branch based on the configured port: 465 → SMTPS (implicit
// TLS), anything else → STARTTLS. We point both at an unreachable host and
// assert the error wrapper distinguishes the transport, proving the routing
// branch was taken.
//
// BDD:
//
//	Given a running email channel with SMTPPort=465,
//	When Send is called,
//	Then the error wraps "send (SMTPS)" (the SMTPS branch was taken).
//
//	Given a running email channel with SMTPPort=587,
//	When Send is called,
//	Then the error wraps "send (STARTTLS)" (the STARTTLS branch was taken).
func TestEmail_SendPipeline_PortRouting(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantSub string
	}{
		{"SMTPS implicit TLS on 465", 465, "send (SMTPS)"},
		{"STARTTLS on 587", 587, "send (STARTTLS)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.EmailConfig{
				IMAPHost:    "imap.example.com",
				SMTPHost:    "127.0.0.1",
				SMTPPort:    tc.port,
				Username:    "bot@example.com",
				PasswordRef: "cred:pw",
			}
			b := bus.NewMessageBus()
			secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

			ch, err := NewEmailChannel(cfg, secrets, b)
			require.NoError(t, err)

			// Mark running so Send does not short-circuit on ErrNotRunning.
			// We do not call Start (which would spawn the IMAP loop); Send only
			// checks the running flag.
			ch.SetRunning(true)

			err = ch.Send(context.Background(), bus.OutboundMessage{
				ChatID:  "user@example.com",
				Content: "hello",
			})
			require.Error(t, err, "Send against an unreachable SMTP host must fail")
			assert.Contains(t, err.Error(), tc.wantSub,
				"expected the %s transport branch to be selected", tc.wantSub)
		})
	}
}

// TestEmail_SendPipeline_EmptyContentNoop verifies that Send with empty content
// is a no-op (returns nil without attempting a connection).
//
// BDD:
//
//	Given a running email channel,
//	When Send is called with empty content,
//	Then nil is returned (no SMTP connection attempted).
func TestEmail_SendPipeline_EmptyContentNoop(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "127.0.0.1",
		IMAPPort:    1,
		SMTPHost:    "127.0.0.1",
		SMTPPort:    1,
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	ch.SetRunning(true)

	assert.NoError(t, ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "user@example.com",
		Content: "   \t  \n ", // whitespace-only is trimmed to empty
	}))
}

// TestEmail_SendPipeline_EmptyRecipient verifies that Send with an empty
// recipient returns an ErrSendFailed-wrapped error without connecting.
//
// BDD:
//
//	Given a running email channel,
//	When Send is called with an empty ChatID,
//	Then an error wrapping channels.ErrSendFailed is returned.
func TestEmail_SendPipeline_EmptyRecipient(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)
	ch.SetRunning(true)

	err = ch.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "",
		Content: "hello",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, channels.ErrSendFailed)
}

// --- #7: IMAP-down degraded boot ---

// TestEmail_IMAPDown_DegradedBoot verifies the degraded-boot contract: when the
// IMAP server is unreachable on startup, the channel still constructs and starts
// (non-fatal — Start returns nil and the channel reports running), the run loop
// fails transiently in the background, and a *second* channel instance can be
// started and stopped independently while the degraded one retries — i.e. the
// degraded email channel does not block the rest of the channel subsystem.
//
// BDD:
//
//	Given an email channel pointed at an unreachable IMAP host,
//	When Start is called,
//	Then Start returns nil (boot is not aborted),
//	And the channel reports running (degraded but live),
//	And a second email channel can be started concurrently and stopped cleanly,
//	And stopping the degraded channel tears it down without hanging.
func TestEmail_IMAPDown_DegradedBoot(t *testing.T) {
	mkChannel := func() *EmailChannel {
		cfg := config.EmailConfig{
			IMAPHost:    "127.0.0.1",
			IMAPPort:    1, // nothing listening → transient dial failure
			SMTPHost:    "127.0.0.1",
			SMTPPort:    1,
			Username:    "bot@example.com",
			PasswordRef: "cred:pw",
		}
		b := bus.NewMessageBus()
		secrets := credentials.SecretBundle{"cred:pw": "hunter2"}
		ch, err := NewEmailChannel(cfg, secrets, b)
		require.NoError(t, err)
		return ch
	}

	degraded := mkChannel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start must return promptly and nil — the IMAP failure happens in the
	// background run loop, NOT on the Start path (boot stays non-fatal).
	startDone := make(chan error, 1)
	go func() { startDone <- degraded.Start(ctx) }()
	select {
	case err := <-startDone:
		require.NoError(t, err, "Start must not fail when IMAP is unreachable (degraded boot)")
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked >2s on unreachable IMAP — degraded-boot contract broken")
	}
	assert.True(t, degraded.IsRunning(), "degraded channel must report running")

	// A second channel must be startable/stoppable while the first is degraded —
	// proving the degraded loop does not monopolise any shared resource.
	other := mkChannel()
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	require.NoError(t, other.Start(otherCtx))
	assert.True(t, other.IsRunning())

	// Give the degraded loop a moment to actually fail and enter backoff — it
	// must remain running (in backoff), not exit (transient, not permanent).
	time.Sleep(300 * time.Millisecond)
	assert.True(t, degraded.IsRunning(),
		"degraded channel must still be running (in backoff) after a transient failure")

	// Stop the second channel cleanly while the first is still degraded.
	require.NoError(t, other.Stop(context.Background()))
	assert.False(t, other.IsRunning(), "second channel must stop cleanly")

	// Stop the degraded channel — must tear down without hanging.
	stopDone := make(chan error, 1)
	go func() { stopDone <- degraded.Stop(context.Background()) }()
	select {
	case err := <-stopDone:
		require.NoError(t, err, "Stop must tear down the degraded channel cleanly")
	case <-time.After(3 * time.Second):
		t.Fatal("Stop on degraded channel hung >3s")
	}
	assert.False(t, degraded.IsRunning())
}

// --- N4: auth-failure vs transient distinction ---

// TestEmail_RunLoop_AuthFailurePermanentStop verifies that when the per-session
// runLoop returns an authentication error, the run loop stops permanently
// (no retry) — done closes and the channel leaves the running state.
//
// BDD:
//
//	Given a channel whose runLoopFn returns an auth error,
//	When Start is called,
//	Then the run loop exits without retrying (done closes promptly),
//	And the channel reports not running.
func TestEmail_RunLoop_AuthFailurePermanentStop(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	// Inject a runLoop that always returns an auth-classified error.
	authErr := errors.New("AUTHENTICATE FAILED: invalid credentials")
	ch.runLoopFn = func(context.Context) error { return authErr }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, ch.Start(ctx))

	// The run loop must exit promptly (maxAuthRetries=1 → permanent stop).
	select {
	case <-ch.done:
		// expected: loop exited without retry
	case <-time.After(2 * time.Second):
		t.Fatal("run loop did not exit within 2s on auth failure — permanent-stop contract broken")
	}
	assert.False(t, ch.IsRunning(),
		"channel must not be running after a permanent auth failure")
}

// TestEmail_RunLoop_TransientRetriesWithBackoff verifies that when the
// per-session runLoop returns a transient (non-auth) error, the run loop does
// NOT exit immediately — it enters backoff and will retry. We assert the loop
// is still running (done not closed) after a short window, then cancel the
// context and verify a clean shutdown.
//
// BDD:
//
//	Given a channel whose runLoopFn returns a transient error,
//	When Start is called,
//	Then the run loop does not exit within a short window (it is in backoff),
//	And the channel reports running,
//	And canceling the context exits the loop cleanly (done closes).
func TestEmail_RunLoop_TransientRetriesWithBackoff(t *testing.T) {
	cfg := config.EmailConfig{
		IMAPHost:    "imap.example.com",
		SMTPHost:    "smtp.example.com",
		Username:    "bot@example.com",
		PasswordRef: "cred:pw",
	}
	b := bus.NewMessageBus()
	secrets := credentials.SecretBundle{"cred:pw": "hunter2"}

	ch, err := NewEmailChannel(cfg, secrets, b)
	require.NoError(t, err)

	// Inject a runLoop that always returns a transient (non-auth) error.
	transientErr := errors.New("dial tcp: connection refused")
	ch.runLoopFn = func(context.Context) error { return transientErr }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, ch.Start(ctx))

	// The run loop must NOT exit immediately — it parks in backoff (backoffMin
	// is 5s, so within 500ms it must still be alive and retrying).
	time.Sleep(500 * time.Millisecond)
	select {
	case <-ch.done:
		t.Fatal(
			"run loop exited within 500ms on a transient error — transient-retry contract broken (should be in backoff)",
		)
	default:
		// expected: loop is still running in backoff
	}
	assert.True(t, ch.IsRunning(),
		"channel must still be running (in backoff) after a transient failure")

	// Cancellation must exit the backoff and tear the loop down cleanly.
	cancel()
	select {
	case <-ch.done:
		// expected: ctx cancellation unblocked the backoff select
	case <-time.After(2 * time.Second):
		t.Fatal("run loop did not exit within 2s of context cancellation")
	}
	assert.False(t, ch.IsRunning())
}

// TestEmail_RunLoop_AuthErrorClassificationViaIsAuthError is a focused
// classification test: the exact error strings produced by go-imap on a bad
// LOGIN are classified as auth (permanent), while network errors are not.
// This guards the isAuthError predicate that the run loop relies on for the
// permanent-vs-transient decision (N4).
func TestEmail_RunLoop_AuthErrorClassificationViaIsAuthError(t *testing.T) {
	permanent := []string{
		"LOGIN failed: invalid credentials",
		"[AUTHENTICATIONFAILED] Authentication failed",
		"authenticate failed: bad credentials",
	}
	for _, msg := range permanent {
		t.Run("permanent/"+msg, func(t *testing.T) {
			assert.True(t, isAuthError(errors.New(msg)),
				"must be classified auth (permanent): %q", msg)
		})
	}

	transient := []string{
		"dial tcp 127.0.0.1:993: connect: connection refused",
		"i/o timeout",
		"tls: handshake failure",
		"EOF",
	}
	for _, msg := range transient {
		t.Run("transient/"+msg, func(t *testing.T) {
			assert.False(t, isAuthError(errors.New(msg)),
				"must NOT be classified auth (transient): %q", msg)
		})
	}
}
