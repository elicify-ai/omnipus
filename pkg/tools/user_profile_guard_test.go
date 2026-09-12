package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// Making USER.md global raises the stakes on writing it: before the change an
// agent could only overwrite its own private copy, after it one agent could
// rewrite what every agent believes about the user. Reads stay allowed — the
// content is already in every agent's prompt.
func TestUserProfileWriteIsBlockedReadIsNot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	agentWorkspace := filepath.Join(home, "agents", "mia")
	if err := os.MkdirAll(agentWorkspace, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(config.UserProfilePath(), []byte("profile"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if denied := guardMetadataPath(agentWorkspace, config.UserProfilePath(), "write"); denied == nil {
		t.Error("a write to the global USER.md was allowed; one agent can now rewrite every agent's view of the user")
	} else if !strings.Contains(denied.ForLLM, "USER_PROFILE_READ_ONLY") {
		t.Errorf("wrong refusal: %s", denied.ForLLM)
	}

	if denied := guardMetadataPath(agentWorkspace, config.UserProfilePath(), "read"); denied != nil {
		t.Errorf("reading USER.md was blocked, but its content is already in every prompt: %s", denied.ForLLM)
	}
}

// The guard must resolve the path, not string-match it: "../../USER.md" from an
// agent's own directory is the same file.
func TestUserProfileWriteBlockedViaTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	agentWorkspace := filepath.Join(home, "agents", "mia")
	if err := os.MkdirAll(agentWorkspace, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(config.UserProfilePath(), []byte("profile"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if denied := guardMetadataPath(agentWorkspace, "../../USER.md", "write"); denied == nil {
		t.Error("a traversal write to the global USER.md was allowed")
	}
}

// A file merely NAMED USER.md inside the agent's own directory is not the
// global profile and must stay writable — the guard must not over-block.
func TestUnrelatedUserMDStaysWritable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	agentWorkspace := filepath.Join(home, "agents", "mia")
	if err := os.MkdirAll(agentWorkspace, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if denied := guardMetadataPath(agentWorkspace, "USER.md", "write"); denied != nil {
		t.Errorf("a per-agent USER.md was blocked; only the global profile should be: %s", denied.ForLLM)
	}
}
