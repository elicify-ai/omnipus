package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
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
			defaults := &config.AgentDefaults{Home: filepath.Join(home, "workspace")}
			agentHome := resolveAgentHome(&config.AgentConfig{ID: agentID}, defaults)

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

	defaults := &config.AgentDefaults{Home: filepath.Join(home, "workspace")}
	agentHome := resolveAgentHome(&config.AgentConfig{ID: "mia"}, defaults)
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

// TestUserProfileLegacyPathIsIgnored is the regression guard for ADR-067
// SC-009 in pkg/config/userprofile.go. Before the guard, ReadUserProfile fell
// back to <OMNIPUS_HOME>/workspace/USER.md when the global file did not exist —
// exactly the legacy migration machinery ADR-067 retired from pkg/config
// (check-greenfield-providers.sh). The new behavior is that the legacy location
// is ignored: a USER.md sitting in <OMNIPUS_HOME>/workspace/ must not be
// returned, the global path wins whenever the global file exists, and a fresh
// install with neither file is silent (covered separately by
// TestUserProfileMissingIsNotAnError below). If this test ever starts passing
// content or path from the legacy location, the greenfield guard has been
// bypassed — fix in the tree, not by exempt-listing this file.
func TestUserProfileLegacyPathIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	legacyDir := filepath.Join(home, "workspace")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "USER.md"), []byte("legacy content"), 0o600); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	// (a) Legacy file exists, no global — ReadUserProfile must return
	//     ("", "", nil). Empty content, no error, the legacy path MUST NOT
	//     appear in the returned path. A return of ("legacy content" or
	//     the legacy path) is the bypass.
	path, content, err := config.ReadUserProfile()
	if err != nil {
		t.Fatalf("ReadUserProfile with legacy-only file: %v", err)
	}
	if content != "" {
		t.Errorf("legacy-only: legacy content leaked into ReadUserProfile: got %q, want \"\"", content)
	}
	if path != "" {
		t.Errorf("legacy-only: legacy path leaked into ReadUserProfile: got %q, want \"\"", path)
	}
	if path == filepath.Join(legacyDir, "USER.md") {
		t.Fatalf("legacy-only: ReadUserProfile returned the legacy path %q — the ADR-067 fallback is back", path)
	}

	// (b) Global file also present — global wins on both content and path.
	if writeErr := os.WriteFile(config.UserProfilePath(), []byte("new content"), 0o600); writeErr != nil {
		t.Fatalf("seed global: %v", writeErr)
	}
	path, content, err = config.ReadUserProfile()
	if err != nil {
		t.Fatalf("ReadUserProfile with both files: %v", err)
	}
	if content != "new content" {
		t.Errorf("global did not win on content: got %q, want %q", content, "new content")
	}
	if path != config.UserProfilePath() {
		t.Errorf("global did not win on path: got %q, want %q", path, config.UserProfilePath())
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
