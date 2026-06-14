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
