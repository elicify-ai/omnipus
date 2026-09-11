package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestUserProfileIsGlobalForEveryAgent is the regression guard for the defect
// this file was added to close: USER.md was resolved from the AGENT's own
// directory, so each agent read a private copy while Settings wrote exactly
// one. The default Assistant ("mia") reads agents/mia/ — so the control the UI
// calls "shared context available to all agents" reached no agent except the
// legacy "main" singleton.
//
// This test fails if USER.md is ever resolved per-agent again.
func TestUserProfileIsGlobalForEveryAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const content = "# About Me\n\nCall me Daniel.\n"
	if err := os.WriteFile(config.UserProfilePath(), []byte(content), 0o600); err != nil {
		t.Fatalf("seed global USER.md: %v", err)
	}

	// Three agents with three different homes, including the default Assistant
	// and one that resolves to the legacy workspace directory.
	for _, agentID := range []string{"mia", "main", "researcher"} {
		t.Run(agentID, func(t *testing.T) {
			defaults := &config.AgentDefaults{Workspace: filepath.Join(home, "workspace")}
			agentHome := resolveAgentWorkspace(&config.AgentConfig{ID: agentID}, defaults)

			def := loadAgentDefinition(agentHome)
			if def.User == nil {
				t.Fatalf("agent %q read no USER.md; it resolved its own home %q instead of the global profile", agentID, agentHome)
			}
			if def.User.Content != content {
				t.Errorf("agent %q read wrong content\n got: %q\nwant: %q", agentID, def.User.Content, content)
			}
			if def.User.Path != config.UserProfilePath() {
				t.Errorf("agent %q read %q, want the global %q", agentID, def.User.Path, config.UserProfilePath())
			}
		})
	}
}

// TestUserProfilePerAgentFileIsIgnored proves the fix is not accidentally
// satisfied by a file that happens to sit in the agent's own directory: a
// stale agents/<id>/USER.md must NOT win over the global one.
func TestUserProfilePerAgentFileIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	if err := os.WriteFile(config.UserProfilePath(), []byte("global"), 0o600); err != nil {
		t.Fatalf("seed global: %v", err)
	}

	defaults := &config.AgentDefaults{Workspace: filepath.Join(home, "workspace")}
	agentHome := resolveAgentWorkspace(&config.AgentConfig{ID: "mia"}, defaults)
	if err := os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatalf("mkdir agent home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentHome, "USER.md"), []byte("STALE PER-AGENT COPY"), 0o600); err != nil {
		t.Fatalf("seed stale per-agent copy: %v", err)
	}

	def := loadAgentDefinition(agentHome)
	if def.User == nil || def.User.Content != "global" {
		got := "<nil>"
		if def.User != nil {
			got = def.User.Content
		}
		t.Fatalf("a stale per-agent USER.md won over the global profile: got %q", got)
	}
}

// TestUserProfileLegacyFallback covers content written before the fix. It is
// read, never written, so an existing install is not silently orphaned.
func TestUserProfileLegacyFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	legacyDir := filepath.Join(home, "workspace")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "USER.md"), []byte("legacy content"), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	path, content, err := config.ReadUserProfile()
	if err != nil {
		t.Fatalf("ReadUserProfile: %v", err)
	}
	if content != "legacy content" {
		t.Errorf("legacy content not returned: got %q", content)
	}
	if path != filepath.Join(legacyDir, "USER.md") {
		t.Errorf("path = %q, want the legacy location", path)
	}

	// The global file must win the moment it exists.
	if err := os.WriteFile(config.UserProfilePath(), []byte("new content"), 0o600); err != nil {
		t.Fatalf("seed global: %v", err)
	}
	if _, content, err = config.ReadUserProfile(); err != nil || content != "new content" {
		t.Errorf("global did not take precedence: content=%q err=%v", content, err)
	}
}

// TestUserProfileMissingIsNotAnError — a fresh install has no profile.
func TestUserProfileMissingIsNotAnError(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	path, content, err := config.ReadUserProfile()
	if err != nil || path != "" || content != "" {
		t.Errorf("missing profile should be silent: path=%q content=%q err=%v", path, content, err)
	}
}
