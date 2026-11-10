package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestLoadAgentDefinitionLoadsGlobalUserMarkdown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	if err := os.WriteFile(config.UserProfilePath(), []byte("# User\nGlobal preferences."), 0o600); err != nil {
		t.Fatalf("seed global USER.md: %v", err)
	}

	tmpDir := setupWorkspace(t, map[string]string{
		"SOUL.md": "# Soul\nStructured agent.",
	})
	defer cleanupWorkspace(t, tmpDir)

	cb := NewContextBuilder(tmpDir)
	definition := cb.LoadAgentDefinition()

	if definition.User == nil {
		t.Fatal("expected USER.md to be loaded")
	}
	if definition.User.Path != config.UserProfilePath() {
		t.Fatalf("expected the global USER.md path %q, got %q", config.UserProfilePath(), definition.User.Path)
	}
	if !strings.Contains(definition.User.Content, "Global preferences") {
		t.Fatalf("expected global USER.md content, got %q", definition.User.Content)
	}
}

func TestLoadBootstrapFilesIncludesGlobalUserMarkdown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	if err := os.WriteFile(config.UserProfilePath(), []byte("# User\nShared profile."), 0o600); err != nil {
		t.Fatalf("seed global USER.md: %v", err)
	}

	tmpDir := setupWorkspace(t, map[string]string{
		"SOUL.md": "# Soul\nSpeak plainly.",
	})
	defer cleanupWorkspace(t, tmpDir)

	cb := NewContextBuilder(tmpDir)
	bootstrap := cb.LoadBootstrapFiles()

	if !strings.Contains(bootstrap, "Shared profile") {
		t.Fatalf("expected the global USER.md in bootstrap, got %q", bootstrap)
	}
	if !strings.Contains(bootstrap, "## USER.md") {
		t.Fatalf("expected USER.md heading in bootstrap, got %q", bootstrap)
	}
}

func TestAgentUserChangesInvalidateCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	userPath := config.UserProfilePath()
	if err := os.WriteFile(userPath, []byte("# User\nInitial workspace preferences."), 0o600); err != nil {
		t.Fatalf("seed global USER.md: %v", err)
	}

	tmpDir := setupWorkspace(t, map[string]string{
		"SOUL.md": "# Soul\nVersion one.",
	})
	defer cleanupWorkspace(t, tmpDir)

	cb := NewContextBuilder(tmpDir)

	promptV1 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(promptV1, "Initial workspace preferences") {
		t.Fatalf("expected the global USER.md in prompt, got %q", promptV1)
	}

	if err := os.WriteFile(userPath, []byte("# User\nUpdated workspace preferences."), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(userPath, future, future); err != nil {
		t.Fatal(err)
	}

	cb.systemPromptMutex.RLock()
	changed := cb.sourceFilesChangedLocked()
	cb.systemPromptMutex.RUnlock()
	if !changed {
		t.Fatal("workspace USER.md changes should invalidate cache")
	}

	promptV2 := cb.BuildSystemPromptWithCache()
	if !strings.Contains(promptV2, "Updated workspace preferences") {
		t.Fatalf("expected updated workspace USER.md in prompt, got %q", promptV2)
	}
}

func cleanupWorkspace(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatalf("failed to clean up workspace %s: %v", path, err)
	}
}
