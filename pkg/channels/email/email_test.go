package email

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
//	And Stop returns promptly after cancelling the loop.
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
