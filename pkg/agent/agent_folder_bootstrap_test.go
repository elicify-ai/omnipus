package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestAgentFolderBootstrap pins the 2026-10-21 founder decision: an agent's
// own folder supplies SOUL.md, not AGENT.md, AGENTS.md, or IDENTITY.md.
// USER.md continues to come from the installation-global profile.
func TestAgentFolderBootstrap(t *testing.T) {
	t.Run("prompt sources", testAgentFolderPromptSources)
	t.Run("ignored file lifecycle", testAgentFolderIgnoredFileLifecycle)
}

func testAgentFolderPromptSources(t *testing.T) {
	t.Helper()
	const (
		agentMarker    = "AGENT_FOLDER_SINGULAR_MARKER_20261021"
		agentsMarker   = "AGENT_FOLDER_PLURAL_MARKER_20261021"
		identityMarker = "AGENT_FOLDER_IDENTITY_MARKER_20261021"
		soulMarker     = "RETAINED_SOUL_MARKER_20261021"
		userMarker     = "RETAINED_GLOBAL_USER_MARKER_20261021"
	)
	cases := []struct {
		name  string
		files map[string]string
	}{
		{
			name: "all retired files",
			files: map[string]string{
				"AGENT.md": agentMarker, "AGENTS.md": agentsMarker,
				"IDENTITY.md": identityMarker, "SOUL.md": soulMarker,
			},
		},
		{
			name: "plural and identity without singular",
			files: map[string]string{
				"AGENTS.md": agentsMarker, "IDENTITY.md": identityMarker, "SOUL.md": soulMarker,
			},
		},
		{
			name: "identity without either agent file",
			files: map[string]string{
				"IDENTITY.md": identityMarker, "SOUL.md": soulMarker,
			},
		},
		{
			name:  "soul without retired files",
			files: map[string]string{"SOUL.md": soulMarker},
		},
		{
			name: "missing soul uses default identity",
			files: map[string]string{
				"AGENT.md": agentMarker, "AGENTS.md": agentsMarker, "IDENTITY.md": identityMarker,
			},
		},
		{
			name: "empty soul uses default identity",
			files: map[string]string{
				"AGENT.md": agentMarker, "AGENTS.md": agentsMarker,
				"IDENTITY.md": identityMarker, "SOUL.md": "",
			},
		},
		{
			name: "whitespace soul uses default identity",
			files: map[string]string{
				"AGENT.md": agentMarker, "AGENTS.md": agentsMarker,
				"IDENTITY.md": identityMarker, "SOUL.md": " \n\t ",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			t.Setenv(config.EnvBuiltinSkills, t.TempDir())
			if err := os.WriteFile(config.UserProfilePath(), []byte(userMarker), 0o600); err != nil {
				t.Fatalf("seed global USER.md: %v", err)
			}
			agentHome := t.TempDir()
			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(agentHome, name), []byte(content), 0o600); err != nil {
					t.Fatalf("seed %s: %v", name, err)
				}
			}
			cb := NewContextBuilder(agentHome).WithAgentInfo("custom-bootstrap", "Custom Bootstrap").WithMemoryEnabled(false)
			prompt := cb.BuildSystemPromptWithCache()
			for _, marker := range []string{agentMarker, agentsMarker, identityMarker} {
				if strings.Contains(prompt, marker) {
					t.Errorf("retired agent-folder marker %q reached the system prompt", marker)
				}
			}
			if got := strings.Count(prompt, userMarker); got != 1 {
				t.Errorf("global USER.md marker count = %d, want 1", got)
			}
			def := cb.LoadAgentDefinition()
			soul, soulExists := tc.files["SOUL.md"]
			if !soulExists {
				if def.Soul != nil {
					t.Errorf("missing SOUL.md resolved to %+v, want nil", def.Soul)
				}
			} else if def.Soul == nil {
				t.Error("existing SOUL.md was not resolved")
			} else {
				if def.Soul.Path != filepath.Join(agentHome, "SOUL.md") || def.Soul.Content != soul {
					t.Errorf("SOUL.md = %+v, want path %q and exact content %q", def.Soul, filepath.Join(agentHome, "SOUL.md"), soul)
				}
			}
			const defaultIdentity = "You are Custom Bootstrap, a helpful AI assistant powered by Omnipus."
			if strings.TrimSpace(soul) == "" {
				if !strings.Contains(prompt, defaultIdentity) {
					t.Error("custom agent without a non-empty SOUL.md lost its default identity")
				}
			} else {
				if got := strings.Count(prompt, soulMarker); got != 1 {
					t.Errorf("SOUL.md marker count = %d, want 1", got)
				}
				if strings.Contains(prompt, defaultIdentity) {
					t.Error("custom SOUL.md must replace the default personality")
				}
			}
		})
	}
}

func testAgentFolderIgnoredFileLifecycle(t *testing.T) {
	t.Helper()
	for _, file := range []string{"AGENT.md", "AGENTS.md", "IDENTITY.md"} {
		t.Run(file, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			t.Setenv(config.EnvBuiltinSkills, t.TempDir())
			agentHome := t.TempDir()
			if err := os.WriteFile(filepath.Join(agentHome, "SOUL.md"), []byte("Retained soul."), 0o600); err != nil {
				t.Fatalf("seed SOUL.md: %v", err)
			}
			cb := NewContextBuilder(agentHome).WithMemoryEnabled(false)
			original := cb.BuildSystemPromptWithCache()
			path := filepath.Join(agentHome, file)
			assertUnchanged := func(t *testing.T) {
				t.Helper()
				cb.systemPromptMutex.RLock()
				changed := cb.sourceFilesChangedLocked()
				cb.systemPromptMutex.RUnlock()
				if changed {
					t.Errorf("ignored %s invalidated the system-prompt cache", file)
				}
				if got := cb.BuildSystemPromptWithCache(); got != original {
					t.Errorf("ignored %s changed the cached prompt", file)
				}
			}
			t.Run("create", func(t *testing.T) {
				if err := os.WriteFile(path, []byte("IGNORED_CREATE_MARKER"), 0o600); err != nil {
					t.Fatalf("create %s: %v", file, err)
				}
				assertUnchanged(t)
			})
			t.Run("modify", func(t *testing.T) {
				if err := os.WriteFile(path, []byte("IGNORED_MODIFY_MARKER"), 0o600); err != nil {
					t.Fatalf("modify %s: %v", file, err)
				}
				future := time.Now().Add(2 * time.Second)
				if err := os.Chtimes(path, future, future); err != nil {
					t.Fatalf("set %s mtime: %v", file, err)
				}
				assertUnchanged(t)
			})
			t.Run("delete", func(t *testing.T) {
				if err := os.Remove(path); err != nil {
					t.Fatalf("delete %s: %v", file, err)
				}
				assertUnchanged(t)
			})
		})
	}
}
