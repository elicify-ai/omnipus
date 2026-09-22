package systools

import (
	"fmt"
	"strings"
	"testing"

	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// TestWorkspaceRevisionError_UnreadableStoreGuidanceIsNonDestructive pins the
// write-side recovery text: inspect/restore, never "remove the file" as the
// default advice. Codes and retry semantics stay with the SE-B classifier.
func TestWorkspaceRevisionError_UnreadableStoreGuidanceIsNonDestructive(t *testing.T) {
	home := t.TempDir()
	writeWorkspaceRecord(t, home, "w1")
	corruptDelegationStore(t, home, "w1")
	_, err := workspacepkg.CheckRevisionLocked(home, "w1", wellFormedRevision)
	if err == nil {
		t.Fatal("setup: expected unreadable delegation store")
	}
	block := revisionErrorBlock(t, "w1", err)
	if block["code"] != "DELEGATION_STORE_UNREADABLE" {
		t.Fatalf("code=%v want DELEGATION_STORE_UNREADABLE (codes unchanged)", block["code"])
	}
	suggestion := fmt.Sprint(block["suggestion"])
	if strings.Contains(strings.ToLower(suggestion), "remove") {
		t.Fatalf("unreadable-store guidance must not default to deletion: %q", suggestion)
	}
	if !strings.Contains(suggestion, "entities/delegation") || !strings.Contains(suggestion, "w1") {
		t.Fatalf("guidance must still name the store: %q", suggestion)
	}
	if strings.Contains(suggestion, "retry with its current revision") {
		t.Fatalf("retry semantics must stay off this branch: %q", suggestion)
	}
}
