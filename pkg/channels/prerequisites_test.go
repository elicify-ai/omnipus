package channels

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestPrerequisiteChecker_Registered verifies that the data-driven
// prerequisite checker map is populated at init time for all expected channel
// types. A type with no registered checker activates without gating; the test
// confirms the registered set matches the W4-C refactor (no per-type switch
// in initChannels).
func TestPrerequisiteChecker_Registered(t *testing.T) {
	expected := []string{
		"telegram", "discord", "dingtalk", "slack", "matrix",
		"line", "wecom", "weixin", "irc", "google-chat", "email",
	}
	for _, ch := range expected {
		checker, ok := getPrerequisite(ch)
		if !ok {
			t.Errorf("expected prerequisite checker for %q; not registered", ch)
			continue
		}
		if checker == nil {
			t.Errorf("prerequisite checker for %q is nil", ch)
		}
	}
}

// TestPrerequisiteChecker_UnregisteredTypeActivatesWithoutGating verifies that
// a channel type with no registered checker returns ok=false from
// getPrerequisite — the initChannels loop treats this as "no gating" and
// activates the channel directly (factory-side validation handles misconfig).
func TestPrerequisiteChecker_UnregisteredTypeActivatesWithoutGating(t *testing.T) {
	_, ok := getPrerequisite("nonexistent-channel-type")
	if ok {
		t.Fatal("expected no prerequisite checker for unregistered type; got one")
	}
}

// TestPrerequisiteChecker_TokenRef validates that token-ref-gated channels
// (telegram, discord, weixin) return ok=false when the token ref is missing,
// and ok=true when it is present.
func TestPrerequisiteChecker_TokenRef(t *testing.T) {
	checker, ok := getPrerequisite("telegram")
	if !ok {
		t.Fatal("expected telegram prerequisite checker")
	}

	// Missing token ref → not ok
	missingFields, ok := checker(config.ChannelInstanceConfig{})
	if ok {
		t.Fatal("expected ok=false when token ref is missing")
	}
	if missingFields == "" {
		t.Fatal("expected non-empty missingFields when token ref is missing")
	}

	// Present token ref → ok
	_, ok = checker(config.ChannelInstanceConfig{TokenRef: "TELEGRAM_TOKEN"})
	if !ok {
		t.Fatal("expected ok=true when token ref is present")
	}
}

// TestPrerequisiteChecker_Email validates that the email checker enforces all
// four required fields (imap_host, smtp_host, username, password) and returns
// ok=true only when all are present.
func TestPrerequisiteChecker_Email(t *testing.T) {
	checker, ok := getPrerequisite("email")
	if !ok {
		t.Fatal("expected email prerequisite checker")
	}

	// All missing → not ok
	_, ok = checker(config.ChannelInstanceConfig{})
	if ok {
		t.Fatal("expected ok=false when all email fields are missing")
	}

	// All present → ok
	_, ok = checker(config.ChannelInstanceConfig{
		IMAPHost:      "imap.example.com",
		SMTPHost:      "smtp.example.com",
		EmailUsername: "user@example.com",
		PasswordRef:   "EMAIL_PASSWORD",
	})
	if !ok {
		t.Fatal("expected ok=true when all email fields are present")
	}
}

// TestPrerequisiteChecker_Matrix validates that the matrix checker enforces
// homeserver, user_id, and access_token — verifying it reads from the instance
// struct (not a hardcoded m.config.Channels["matrix"] lookup, which was the
// pre-W4-C bug).
func TestPrerequisiteChecker_Matrix(t *testing.T) {
	checker, ok := getPrerequisite("matrix")
	if !ok {
		t.Fatal("expected matrix prerequisite checker")
	}

	// Missing all → not ok
	_, ok = checker(config.ChannelInstanceConfig{})
	if ok {
		t.Fatal("expected ok=false when all matrix fields are missing")
	}

	// All present → ok
	_, ok = checker(config.ChannelInstanceConfig{
		Homeserver:     "https://matrix.org",
		UserID:         "@bot:matrix.org",
		AccessTokenRef: "MATRIX_TOKEN",
	})
	if !ok {
		t.Fatal("expected ok=true when all matrix fields are present")
	}
}
