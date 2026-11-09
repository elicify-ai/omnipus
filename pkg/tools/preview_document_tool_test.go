// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Office-document preview — preview_document tool surface (office-preview
// spec AD-1/AD-5, FR-001…FR-004/FR-014; TDD rows T-01 input/page rules,
// T-15 read authorization + audit leg, T-45 thumbnail storage leg, T-70
// size boundaries; BDD B-03/B-15).
//
// Active in ordinary tagged Go runs after the wave-2 constructor landed.
// Oracles are AD-1/AD-5/MV-1, not observed implementation output.
// Clamp arithmetic is also checked in pkg/preview's clamp table; the actual
// tool path is exercised by TestPreviewDocument_RealPDFPageClamps.

import (
	"context"

	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestPreviewDocument_InputPageSyntaxAndClamps (T-01, AD-1/AD-5, B-03/B-15):
// pages syntax, default range, clamping, and every refusal message.
func TestPreviewDocument_InputPageSyntaxAndClamps(t *testing.T) {
	ws := t.TempDir()
	tool := NewPreviewDocumentTool(ws, false)
	ctx := testContext(t)

	writeFixture := func(name string, size int) string {
		p := filepath.Join(ws, name)
		data := make([]byte, size)
		for i := range data {
			data[i] = byte('x')
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// MV-1 refusals — Dataset B (R1, R2, R3, R4, R8); refusal starts nothing.
	cases := []struct {
		name  string
		file  string
		pages string
		want  string
	}{
		{"txt refused", writeFixture("notes.txt", 32), "", "preview_document: unsupported file type; supported: .docx, .xlsx, .pptx, .pdf"},
		{"png-as-docx refused", writeFixture("fake.docx", 32), "", "preview_document: file content does not match its .docx extension"},
		{"50MiB+1 refused", writeFixture("big.pdf", 50<<20+1), "", "preview_document: file exceeds the 50 MiB preview limit"},
		{"pages 0 refused", "", "0", "preview_document: pages must be \"N\" or \"N-M\" with 1 <= N <= M <= 9999 and at most 20 pages"},
		{"pages 5-3 refused", "", "5-3", "preview_document: pages must be \"N\" or \"N-M\" with 1 <= N <= M <= 9999 and at most 20 pages"},
		{"pages 1-21 refused", "", "1-21", "preview_document: pages must be \"N\" or \"N-M\" with 1 <= N <= M <= 9999 and at most 20 pages"},
		{"pages a refused", "", "a", "preview_document: pages must be \"N\" or \"N-M\" with 1 <= N <= M <= 9999 and at most 20 pages"},
		{"pages 10000 refused", "", "10000", "preview_document: pages must be \"N\" or \"N-M\" with 1 <= N <= M <= 9999 and at most 20 pages"},
		{"docm refused", writeFixture("m.docm", 32), "", "preview_document: unsupported file type; supported: .docx, .xlsx, .pptx, .pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"file": tc.file}
			if tc.pages != "" {
				args["pages"] = tc.pages
			}
			result := tool.Execute(ctx, args)
			if !result.IsError {
				t.Fatalf("refusal must be an error result, got success: %s", result.ForLLM)
			}
			if !strings.HasPrefix(result.ForLLM, tc.want) {
				t.Fatalf("refusal message = %q, want prefix %q (MV-1, Dataset B)", result.ForLLM, tc.want)
			}
		})
	}

	// AD-5 uses en dashes in display text (pages 25–30, requested 25–40),
	// and ASCII hyphens only in the pages argument. See the real PDF table.
}

// TestPreviewDocument_ReadAuthorizationAndAudit (T-15, FR-003): the source
// is read only through the caller's read authorization, audited like
// read_file; outside-policy paths refuse with read_file's result.
func TestPreviewDocument_ReadAuthorizationAndAudit(t *testing.T) {
	ws := t.TempDir()
	tool := NewPreviewDocumentTool(ws, true)
	// restrict alone does not confine reads: use the real per-turn read
	// confinement flag, as the filesystem authorization contract specifies.
	ctx := WithAgentID(WithReadConfined(testContext(t), true), "office-preview-test")
	auditDir := t.TempDir()
	log, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	tool.SetAuditLogger(log)

	secret := filepath.Join(t.TempDir(), "outside-secret.pdf")
	if writeErr := os.WriteFile(secret, []byte("%PDF-1.4"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	// read_file's audit contract records the canonical existing path. On
	// macOS the default temporary prefix /var is an alias of /private/var;
	// keep the supplied alias in the call, but derive the expected identity
	// independently from the filesystem rather than from an audit row.
	canonicalSecret, err := filepath.EvalSymlinks(secret)
	if err != nil {
		t.Fatal(err)
	}
	result := tool.Execute(ctx, map[string]any{"file": secret})
	if !result.IsError {
		t.Fatalf("read outside the caller's policy must refuse, got success: %s", result.ForLLM)
	}
	// MV-1: "read denied = read_file's result" — the refusal text matches
	// the read_file tool's refusal for the same path.
	readTool := NewReadFileTool(ws, true, MaxReadFileSize)
	readTool.SetAuditLogger(log)
	want := readTool.Execute(ctx, map[string]any{"path": secret})
	if !want.IsError || strings.Contains(want.ForLLM, "%PDF-1.4") {
		t.Fatalf("authorization negative control failed: read_file must refuse without content, got %+v", want)
	}
	if result.ForLLM != want.ForLLM {
		t.Fatalf("preview read-denial = %q, want read_file's refusal %q", result.ForLLM, want.ForLLM)
	}
	// Positive control: the same policy still reads inside the workspace.
	inside := filepath.Join(ws, "inside.pdf")
	if err := os.WriteFile(inside, []byte("%PDF-inside-control"), 0o600); err != nil {
		t.Fatal(err)
	}
	allowed := readTool.Execute(ctx, map[string]any{"path": inside})
	if allowed.IsError || !strings.Contains(allowed.ForLLM, "%PDF-inside-control") {
		t.Fatalf("workspace positive control failed: %+v", allowed)
	}
	rows := readAuditRows(t, log, auditDir)
	denials := map[string]int{}
	for _, row := range rows {
		if row.Event == PathAccessDeniedEvent {
			if row.AgentID != "office-preview-test" || row.SessionID != "session-office-preview" || row.Decision != audit.DecisionDeny || row.detail("path") != canonicalSecret {
				t.Fatalf("incorrect preview/read denial attribution: %+v", row)
			}
			denials[row.Tool]++
		}
	}
	if denials["preview_document"] != 1 || denials["read_file"] != 1 || len(denials) != 2 {
		t.Fatalf("want one attributed denial per tool; got %v (rows %+v)", denials, rows)
	}
}

// TestPreviewDocument_ThumbnailStorageFollowsScreenshotContract (T-45,
// FR-020, §5.1): stored thumbnails ride the session uploads folder under
// the tool:inline:session scope with forget-only cleanup — the same
// contract browser screenshots already satisfy — and never enter the
// model-media admission path. T-63's archive/window/next-request leg is
// exercised separately in pkg/agent, not claimed by this storage test.
func TestPreviewDocument_ThumbnailStorageFollowsScreenshotContract(t *testing.T) {
	assertOfficeThumbnailStorage(t)
}

// testContext uses the same transcript-session writer as the turn engine.
func testContext(t *testing.T) context.Context {
	t.Helper()
	return WithTranscriptSessionID(context.Background(), "session-office-preview")
}
