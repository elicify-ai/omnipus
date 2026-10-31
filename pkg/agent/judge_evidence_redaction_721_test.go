// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_redaction_721_test.go — issue #721: the Judge's evidence
// redaction must be UNCONDITIONAL. Registered credential values AND known
// secret shapes are scrubbed from every channel of evidence the Judge's
// prompt carries (the machine-check evidence record and the session
// transcript window), whatever the operator's tools.filter_sensitive_data
// flag says and however short the value is.
//
// The oracle is the threat, not the implementation: each secret below is a
// shape the SEC-16 pattern set (pkg/audit) names, plus one value the operator
// registered at boot. None of them may appear anywhere in the assembled
// Judge prompt.

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

// judgeSecretShapes returns the secret values the test feeds in. Built from
// pieces so the literal source never carries a scanner-shaped credential.
func judgeSecretShapes(registered string) map[string]string {
	return map[string]string{
		"openai_key":      "sk-" + strings.Repeat("Ab3d", 6),
		"github_pat":      "ghp_" + strings.Repeat("a1B2c3", 6),
		"github_oauth":    "gho_" + strings.Repeat("Z9y8X7", 6),
		"aws_access_key":  "AKIA" + strings.Repeat("Q7", 8),
		"aws_temp_key":    "ASIA" + strings.Repeat("W3", 8),
		"slack_bot":       "xoxb-" + "1234567890" + "-" + "AbCdEfGh12",
		"jwt":             "eyJ" + "hbGciOiJIUzI1NiJ9" + ".eyJ" + "zdWIiOiIxMjM0NTY3ODkwIn0" + ".c2lnbmF0dXJl",
		"google_oauth":    "ya29." + "a0AfH6SMBx" + strings.Repeat("q", 12),
		"generic_key":     "key-" + strings.Repeat("k3Y", 8),
		"bearer_token":    "tok" + strings.Repeat("Bz", 10),
		"url_password":    "hunter2" + "Pass" + "word",
		"registered_cred": registered,
	}
}

// secretCarrier embeds every secret in realistic surrounding text.
func secretCarrier(secrets map[string]string) string {
	var sb strings.Builder
	for name, v := range secrets {
		switch name {
		case "bearer_token":
			sb.WriteString("Authorization: Bearer " + v + "\n")
		case "url_password":
			sb.WriteString("DATABASE_URL=postgres://app:" + v + "@db.internal:5432/main\n")
		default:
			sb.WriteString(name + "=" + v + "\n")
		}
	}
	return sb.String()
}

func TestJudgeEvidence721_SecretShapesNeverReachTheJudge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		filterFlag bool
	}{
		// The release behaviour this issue reports: the flag defaults to false
		// on any config that does not set it, and FilterSensitiveData then
		// passes everything through.
		{name: "filter_flag_off", filterFlag: false},
		// Flag on: the registered value was scrubbed before, but no secret
		// SHAPE was — a credential the agent just read out of a file.
		{name: "filter_flag_on", filterFlag: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)

			cfg := &config.Config{}
			cfg.Tools.FilterSensitiveData = tc.filterFlag
			al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
			t.Cleanup(func() { al.Close() })

			// Short on purpose: below the flag's default 8-byte length
			// threshold would also have bypassed the old filter.
			registered := "Rg7#qz"
			al.GetConfig().RegisterSensitiveValues([]string{registered})
			secrets := judgeSecretShapes(registered)
			carrier := secretCarrier(secrets)

			// Channel 1: machine-check evidence (judge.go's evidenceStore).
			rec, err := al.evidenceStore().Build("", "c-721", 1,
				"curl -H 'Authorization: Bearer "+secrets["bearer_token"]+"' https://api.example", carrier, 0, false, false)
			if err != nil {
				t.Fatalf("evidenceStore().Build: %v", err)
			}

			// Channel 2: the session transcript window (narration, tool
			// parameters, tool error and tool output).
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
			entries := []session.TranscriptEntry{
				{Role: "assistant", Content: "I read the env file:\n" + carrier},
				{
					Role: "assistant", Type: session.EntryTypeToolCall,
					ToolCalls: []session.ToolCall{
						{
							ID: "tc-params", Tool: "http_request", Status: "error",
							Parameters: map[string]any{"headers": "Authorization: Bearer " + secrets["bearer_token"], "body": carrier},
							Error:      "upstream rejected " + carrier,
						},
						{
							ID: "tc-output", Tool: "read_file", Status: "success",
							Result: map[string]any{"text": carrier},
						},
					},
				},
			}
			for _, e := range entries {
				if appendErr := shared.AppendTranscript(meta.ID, e); appendErr != nil {
					t.Fatalf("AppendTranscript: %v", appendErr)
				}
			}
			window := al.goalSessionWindowText(meta.ID, agentID)
			if window == "" {
				t.Fatal("goalSessionWindowText returned empty; the window channel was not exercised")
			}

			prompt, err := buildJudgeUserContent(
				[]task.AcceptanceCriterion{{ID: "p1", Kind: task.KindProse, Text: "No secrets appear in the output"}},
				[]task.EvidenceRecord{*rec}, "", "", window, "")
			if err != nil {
				t.Fatalf("buildJudgeUserContent: %v", err)
			}

			for name, v := range secrets {
				if strings.Contains(prompt, v) {
					t.Errorf("secret %q (%s) reached the Judge prompt", name, v)
				}
			}
			if !strings.Contains(prompt, "[REDACTED]") {
				t.Errorf("a scrubbed span must stay visible to the Judge as a marker; prompt carries no [REDACTED]")
			}
		})
	}
}

// TestJudgeEvidence721_RedactionPrecedesTruncation: a secret straddling the
// evidence output cap must not survive half-scrubbed. A partial credential is
// still a credential.
func TestJudgeEvidence721_RedactionPrecedesTruncation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	al := mustNewAgentLoop(t, &config.Config{}, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	secret := "ghp_" + strings.Repeat("a1B2c3", 6)
	es := al.evidenceStore()
	es.OutputCap = 64
	// Put the secret across the cap boundary.
	output := strings.Repeat("x", es.OutputCap-10) + secret + strings.Repeat("y", 40)
	rec, err := es.Build("", "c-cap", 1, "true", output, 0, false, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// The cut keeps the first 10 bytes of the secret ("ghp_" + 6) when
	// truncation runs before redaction.
	if strings.Contains(rec.Output, secret[:10]) {
		t.Fatalf("a secret prefix survived truncation un-scrubbed: %q", rec.Output)
	}
}
