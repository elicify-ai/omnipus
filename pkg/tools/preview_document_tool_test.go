// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build officepreview

package tools

// Office-document preview — preview_document tool surface (office-preview
// spec AD-1/AD-5, FR-001…FR-004/FR-014; TDD rows T-01 input/page rules,
// T-15 read authorization + audit leg, T-45 thumbnail storage leg, T-70
// size boundaries; BDD B-03/B-15).
//
// THIS FILE IS THE PARKED RED TIER: it does not compile until the tool
// lands, so it is kept on the author's local branch and pushed together
// with its fix (landing-train rule: a push must not break the branch's
// build/tests). Unlock symbol: tools.NewPreviewDocumentTool — repo
// constructor convention (NewReadFileTool(workspace, restrict, deps…)),
// per spec AD-13's "registered at the read_file site". qa-lead A owns the
// reconciliation if backend-lead names the constructor differently.
//
// Oracles below are the spec texts verbatim (AD-1/AD-5/MV-1), never code.
//
// Build tag officepreview: excluded from default builds/tests until GREEN
// activates it (drop the tag in the same commit as the constructor).

import (
	"context"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreviewDocument_InputPageSyntaxAndClamps (T-01, AD-1/AD-5, B-03/B-15):
// pages syntax, default range, clamping, and every refusal message.
func TestPreviewDocument_InputPageSyntaxAndClamps(t *testing.T) {
	ws := t.TempDir()
	tool := NewPreviewDocumentTool(ws, false) // UNLOCK: constructor lands with GREEN
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

	// AD-5 clamps on a 30-page PDF (FX-PDF30 shape): "3" → one page;
	// "2-5" → those; default → first 10; "11-20" → that batch; "25-40" →
	// 25–30 with "pages 25-30 of 30 (requested 25-40)".
	// UNLOCK: page-count plumbing (renderer info op) lands with GREEN;
	// these cases drive the real tool against the fixture set recorded at
	// RED (see omnipus-scratch/office-preview-build-20261030).
}

// TestPreviewDocument_ReadAuthorizationAndAudit (T-15, FR-003): the source
// is read only through the caller's read authorization, audited like
// read_file; outside-policy paths refuse with read_file's result.
func TestPreviewDocument_ReadAuthorizationAndAudit(t *testing.T) {
	ws := t.TempDir()
	tool := NewPreviewDocumentTool(ws, false) // UNLOCK
	ctx := testContext(t)

	secret := filepath.Join(ws, "..", "outside-secret.pdf")
	if err := os.WriteFile(secret, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := tool.Execute(ctx, map[string]any{"file": secret})
	if !result.IsError {
		t.Fatalf("read outside the caller's policy must refuse, got success: %s", result.ForLLM)
	}
	// MV-1: "read denied = read_file's result" — the refusal text matches
	// the read_file tool's refusal for the same path.
	readTool := NewReadFileTool(ws, false, MaxReadFileSize)
	want := readTool.Execute(ctx, map[string]any{"path": secret})
	if result.ForLLM != want.ForLLM {
		t.Fatalf("preview read-denial = %q, want read_file's refusal %q", result.ForLLM, want.ForLLM)
	}
}

// TestPreviewDocument_ThumbnailStorageFollowsScreenshotContract (T-45,
// FR-020, §5.1): stored thumbnails ride the session uploads folder under
// the tool:inline:session scope with forget-only cleanup — the same
// contract browser screenshots already satisfy — and never enter the
// model-media admission path (T-63's archive/window assertion rides the
// same execution).
func TestPreviewDocument_ThumbnailStorageFollowsScreenshotContract(t *testing.T) {
	t.Fatal("UNLOCK: executes once NewPreviewDocumentTool lands (parked RED tier) — asserts SessionUploadsDir placement, SessionInlineScopePrefix scope, CleanupPolicyForgetOnly, and that the window Message after Execute carries no thumbnail data URLs (FR-036)")
}

// testContext mints a tool context carrying a session id so storage and
// audit behave as in a live turn. Reconciled at GREEN with the real
// session-id context key (ToolTranscriptSessionID's writer).
func testContext(t *testing.T) context.Context {
	t.Helper()
	return context.WithValue(context.Background(), testSessionIDKey{}, "session-office-preview")
}

type testSessionIDKey struct{}
