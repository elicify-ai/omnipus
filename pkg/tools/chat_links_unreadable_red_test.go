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
	if os.Geteuid() == 0 {
		t.Fatal("this test needs a non-root user; root can read a mode 000 file")
	}
	ctx, dir := chatLinksOwnFile(t)
	secret := filepath.Join(dir, "notes", "private.md")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })

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
