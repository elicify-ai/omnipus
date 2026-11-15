package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SF-1 / SEC-9: an existing regular file the process cannot read is the same
// not-found result as a missing file. Stat succeeding is not enough.
func TestShowToUser_UnreadableMatchesMissing(t *testing.T) {
	ctx, dir := chatLinksOwnFile(t)
	secret := filepath.Join(dir, "notes", "private.md")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })
	// Mode 000 does not deny reading for root, or on Windows (the call only
	// sets the read-only attribute). Skip instead of failing a readable file.
	if probe, err := os.Open(secret); err == nil {
		_ = probe.Close()
		t.Skip("chmod 000 did not deny reading this file (root, or a platform such as Windows where that mode is not a read denial)")
	}

	tool := chatLinksShowToUser(t, "SF-1 / SEC-9")
	missing := tool.Execute(ctx, map[string]any{"ref": "notes/missing.md"})
	unreadable := tool.Execute(ctx, map[string]any{"ref": "notes/private.md"})
	if missing == nil || unreadable == nil || missing.ForLLM != chatLinksNotFound || unreadable.ForLLM != missing.ForLLM {
		t.Fatalf("missing %q unreadable %q, want both %q (SF-1 / SEC-9)", missingText(missing), missingText(unreadable), chatLinksNotFound)
	}
	if !unreadable.IsError || unreadable.OpenInLibrary != nil || strings.Contains(unreadable.ForLLM, secret) || strings.Contains(unreadable.ForLLM, "private.md") {
		t.Fatalf("unreadable result = %+v, want an error with no open target and no path", unreadable)
	}
}

func missingText(res *ToolResult) string {
	if res == nil {
		return "<nil>"
	}
	return res.ForLLM
}
