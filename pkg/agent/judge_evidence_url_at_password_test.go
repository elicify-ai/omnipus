// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_url_at_password_test.go — security review 6b (2026-10-30,
// Major finding on top of founder decision Q14 B): a URL password that
// contains a raw "@" must be masked WHOLE. Go's URL parser ends a URL
// authority's userinfo at the LAST "@" before the host, so
// "postgres://app:p@ReviewTail9@db.example/main" carries the password
// "p@ReviewTail9" — the layer-3 pattern that stopped at the first "@" (or
// failed outright when the password started with one) left the password tail
// in front of the Judge. A plain e-mail address that is not a URL userinfo
// stays visible (Q14 B); masking a wider userinfo must not swallow one.

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

// TestJudgeEvidenceURLAtPassword_WholePasswordMasked: every case goes through
// the committed wiring — the scrubber itself, the persisted machine-check
// evidence (on-disk write AND read-back), the session transcript window, and
// the assembled Judge prompt. The three control rows are shapes the scrubber
// already masked before the fix; they prove the fix unmasks nothing.
func TestJudgeEvidenceURLAtPassword_WholePasswordMasked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	al := mustNewAgentLoop(t, &config.Config{}, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	redact := al.judgeEvidenceRedactor()

	// Q14 B keeps an ordinary recipient visible; masking a wider userinfo
	// must not swallow the e-mail sitting next to the URL in space-free JSON.
	const recipient = "supplier@x.com"

	cases := []struct {
		name      string
		rawURL    string
		forbidden string // password bytes that must not survive anywhere
		host      string // must stay visible: the mask is userinfo-scoped
	}{
		{name: "raw_at_password", rawURL: "postgres://app:p@ReviewTail9@db.example/main", forbidden: "ReviewTail9", host: "db.example"},
		{name: "leading_at_password", rawURL: "postgres://app:@ReviewPassword9@db.example/main", forbidden: "ReviewPassword9", host: "db.example"},
		{name: "control_ordinary_password", rawURL: "postgres://app:ReviewPassword9@db.example/main", forbidden: "ReviewPassword9", host: "db.example"},
		{name: "control_encoded_at_password", rawURL: "postgres://app:p%40ReviewTail9@db.example/main", forbidden: "ReviewTail9", host: "db.example"},
		{name: "control_email_login", rawURL: "imaps://login.user@corp.example:ReviewPassword9@mail.corp.example/inbox", forbidden: "ReviewPassword9", host: "mail.corp.example"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Channel 1: the scrubber itself.
			scrubbed := redact(tc.rawURL)
			if strings.Contains(scrubbed, tc.forbidden) {
				t.Errorf("scrubber exposes password bytes %q: %q", tc.forbidden, scrubbed)
			}
			if !strings.Contains(scrubbed, tc.host) {
				t.Errorf("scrubber lost the host %q — the mask must stay userinfo-scoped: %q", tc.host, scrubbed)
			}

			// Channel 2: machine-check evidence, the on-disk write/read path.
			es := al.evidenceStore()
			taskID := "url-at-" + tc.name
			if _, err := es.Record(taskID, "c-at", 1,
				"connect "+tc.rawURL, "recipient="+recipient+" connection="+tc.rawURL, 0, false, false); err != nil {
				t.Fatalf("evidenceStore().Record: %v", err)
			}
			readBack, err := es.List(taskID)
			if err != nil || len(readBack) != 1 {
				t.Fatalf("evidence read-back: count=%d err=%v", len(readBack), err)
			}
			for field, text := range map[string]string{"command": readBack[0].Command, "output": readBack[0].Output} {
				if strings.Contains(text, tc.forbidden) {
					t.Errorf("persisted evidence %s exposes password bytes %q: %q", field, tc.forbidden, text)
				}
			}
			if !strings.Contains(readBack[0].Output, recipient) {
				t.Errorf("persisted evidence masked the ordinary recipient %q: %q", recipient, readBack[0].Output)
			}

			// Channel 3: the session transcript window (the production
			// tier-one redact wiring), with the recipient field next to the
			// URL — tool parameters render as space-free JSON.
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
					ID: session.ToolCallID("tc-at-" + tc.name), Tool: "http_request", Status: "success",
					Parameters: map[string]any{"url": tc.rawURL, "to": recipient},
					Result:     map[string]any{"text": tc.rawURL},
				}},
			}
			if appendErr := shared.AppendTranscript(meta.ID, entry); appendErr != nil {
				t.Fatalf("AppendTranscript: %v", appendErr)
			}
			window := al.goalSessionWindowText(meta.ID, agentID)
			if window == "" {
				t.Fatal("goalSessionWindowText returned empty; the window channel was not exercised")
			}
			if strings.Contains(window, tc.forbidden) {
				t.Errorf("transcript window exposes password bytes %q:\n%s", tc.forbidden, window)
			}
			if n := strings.Count(window, recipient); n < 1 {
				t.Errorf("window lost the ordinary recipient %q from the tool parameter:\n%s", recipient, window)
			}

			// Channel 4: the assembled Judge prompt over both channels.
			prompt, err := buildJudgeUserContent(
				[]task.AcceptanceCriterion{{ID: "c-at", Kind: task.KindProse, Text: "The recipient was notified"}},
				[]task.EvidenceRecord{readBack[0]}, "", "", window, "")
			if err != nil {
				t.Fatalf("buildJudgeUserContent: %v", err)
			}
			if strings.Contains(prompt, tc.forbidden) {
				t.Errorf("Judge prompt exposes password bytes %q", tc.forbidden)
			}
			if !strings.Contains(prompt, recipient) {
				t.Errorf("Judge prompt lost the ordinary recipient %q", recipient)
			}
			if !strings.Contains(prompt, "[REDACTED]") {
				t.Errorf("a scrubbed span must stay visible to the Judge as [REDACTED]")
			}
		})
	}
}

// TestJudgeEvidenceURLAtPassword_PlainEmailStaysVisible: the fix narrows
// nothing. A plain e-mail address in prose and in a tool parameter, next to
// an "@"-password URL with NO path (so URL and recipient field sit directly
// adjacent in space-free JSON), stays visible everywhere while the password
// stays hidden.
func TestJudgeEvidenceURLAtPassword_PlainEmailStaysVisible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	al := mustNewAgentLoop(t, &config.Config{}, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	redact := al.judgeEvidenceRedactor()

	const recipient = "supplier@x.com"
	// No path and no port on purpose: the userinfo match must end at the
	// closing quote, never run on into the following JSON field.
	const atPasswordURL = "postgres://app:@ReviewPassword9@db.example"
	const password = "ReviewPassword9"

	// The scrubber: password gone, e-mail untouched.
	scrubbed := redact("notify " + recipient + " url=" + atPasswordURL + " done")
	if strings.Contains(scrubbed, password) {
		t.Errorf("scrubber exposes password bytes: %q", scrubbed)
	}
	if !strings.Contains(scrubbed, recipient) {
		t.Errorf("plain e-mail address was masked (Q14 B keeps it visible): %q", scrubbed)
	}

	// The transcript window and the assembled prompt.
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
		Role:    "assistant",
		Content: "Sending the order confirmation to " + recipient + ".",
		Type:    session.EntryTypeToolCall,
		ToolCalls: []session.ToolCall{{
			ID: session.ToolCallID("tc-email-adjacent"), Tool: "notify", Status: "success",
			Parameters: map[string]any{"to": recipient, "url": atPasswordURL},
			Result:     map[string]any{"status": "sent", "to": recipient},
		}},
	}
	if appendErr := shared.AppendTranscript(meta.ID, entry); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}
	window := al.goalSessionWindowText(meta.ID, agentID)
	if window == "" {
		t.Fatal("goalSessionWindowText returned empty; the window channel was not exercised")
	}

	rec, err := al.evidenceStore().Record("url-at-email-visible", "c-at", 1,
		"notify --to "+recipient+" --url "+atPasswordURL, "sent to "+recipient, 0, false, false)
	if err != nil {
		t.Fatalf("evidenceStore().Record: %v", err)
	}
	prompt, err := buildJudgeUserContent(
		[]task.AcceptanceCriterion{{ID: "c-at", Kind: task.KindProse, Text: "The order reached " + recipient}},
		[]task.EvidenceRecord{*rec}, "", "", window, "")
	if err != nil {
		t.Fatalf("buildJudgeUserContent: %v", err)
	}

	for name, text := range map[string]string{"scrubber": scrubbed, "window": window, "prompt": prompt} {
		if strings.Contains(text, password) {
			t.Errorf("%s exposes password bytes %q", name, password)
		}
	}
	for name, text := range map[string]string{"window": window, "prompt": prompt, "record_output": rec.Output} {
		if !strings.Contains(text, recipient) {
			t.Errorf("%s lost the plain e-mail address %q (Q14 B keeps it visible)", name, recipient)
		}
	}
	if !strings.Contains(prompt, "[REDACTED]") {
		t.Errorf("a scrubbed span must stay visible to the Judge as [REDACTED]")
	}
}
