// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_email_q14_test.go — founder decision Q14 B (2026-10-30):
// the Judge must SEE plain e-mail addresses in tool parameters and tool
// results, so a goal such as "email the order to supplier@x.com" can be
// verified. Everything else the Judge-evidence scrubber hides stays hidden:
// registered credentials at any length, the known secret shapes, and the
// password in a URL's userinfo — including a userinfo whose user part is an
// e-mail address.

package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestJudgeEvidenceQ14_EmailVisibleSecretsStillHidden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	al := mustNewAgentLoop(t, &config.Config{}, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	const email = "supplier@x.com"
	registered := "Rg7#qz"
	al.GetConfig().RegisterSensitiveValues([]string{registered})

	apiKey := "sk-" + strings.Repeat("Ab3d", 6)
	urlPassword := "hunter2" + "Pass" + "word"
	userinfoEmailPassword := "s3cr3t" + "Mail" + "Pw"
	// The e-mail sits in a URL's userinfo next to a password: the whole
	// userinfo is a login, not a recipient, so it stays hidden.
	userinfoEmailUser := "login.user@corp.example"
	userinfoURL := "https://" + userinfoEmailUser + ":" + userinfoEmailPassword + "@mail.corp.example/inbox"
	userinfoURLEncoded := "imaps://" + "enc.user%40corp.example" + ":" + userinfoEmailPassword + "@mail.corp.example/"

	hidden := map[string]string{
		"registered_cred":            registered,
		"api_key":                    apiKey,
		"url_password":               urlPassword,
		"userinfo_email_password":    userinfoEmailPassword,
		"userinfo_email_user":        userinfoEmailUser,
		"userinfo_email_user_encode": "enc.user%40corp.example",
	}

	params := map[string]any{
		"to": email,
		// No space on purpose: tool parameters render as space-free JSON, so
		// the path-less "smtp" URL is followed directly by "subject" and
		// "to" — a userinfo match must not run on into the recipient.
		"subject": "Order-4711",
		"body":    "Please confirm to " + email + ". token=" + registered + " key=" + apiKey,
		"smtp":    "smtps://app:" + urlPassword + "@smtp.example:465",
		"imap":    userinfoURL,
		"imap2":   userinfoURLEncoded,
	}
	result := map[string]any{
		"status": "sent",
		"text":   "Message accepted for " + email + " via " + userinfoURL + " and " + userinfoURLEncoded,
	}

	// Channel 1: machine-check evidence.
	rec, err := al.evidenceStore().Build("", "c-q14", 1,
		"send_mail --to "+email+" --key "+apiKey, "delivered to <"+email+"> "+registered, 0, false, false)
	if err != nil {
		t.Fatalf("evidenceStore().Build: %v", err)
	}

	// Channel 2: the session transcript window (tool parameters and output).
	shared, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = shared
	const agentID = "mia"
	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	entry := session.TranscriptEntry{
		Role: "assistant", Type: session.EntryTypeToolCall,
		ToolCalls: []session.ToolCall{{
			ID: "tc-mail", Tool: "send_email", Status: "success",
			Parameters: params, Result: result,
		}},
	}
	if appendErr := shared.AppendTranscript(meta.ID, entry); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}
	window := al.goalSessionWindowText(meta.ID, agentID)
	if window == "" {
		t.Fatal("goalSessionWindowText returned empty; the window channel was not exercised")
	}

	prompt, err := buildJudgeUserContent(
		[]task.AcceptanceCriterion{{ID: "p1", Kind: task.KindProse, Text: "The order was emailed to " + email}},
		[]task.EvidenceRecord{*rec}, "", "", window, "")
	if err != nil {
		t.Fatalf("buildJudgeUserContent: %v", err)
	}

	// The e-mail must reach the Judge through BOTH evidence channels, not
	// only via the criterion text the test itself supplied.
	if !strings.Contains(rec.Command, email) || !strings.Contains(rec.Output, email) {
		t.Errorf("e-mail redacted from machine-check evidence: command=%q output=%q", rec.Command, rec.Output)
	}
	if n := strings.Count(window, email); n < 3 {
		t.Errorf("e-mail must be visible in tool parameters and tool result of the window (want >=3 occurrences, got %d):\n%s", n, window)
	}
	for name, v := range hidden {
		if strings.Contains(prompt, v) {
			t.Errorf("%s (%q) reached the Judge prompt", name, v)
		}
	}
	if !strings.Contains(prompt, "[REDACTED]") {
		t.Errorf("scrubbed spans must stay visible as [REDACTED]")
	}
}
