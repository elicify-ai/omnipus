package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// FR-239: git is a process-edge fixture; runGit and its environment are real.
func TestWU19_WorktreeGitChildEnvironment(t *testing.T) {
	if slices.Contains(os.Args, "--wu19-git") {
		got := map[string]string{}
		for _, key := range wu19GitKeys() {
			if value, ok := os.LookupEnv(key); ok {
				got[key] = value
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(got); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("OMNIPUS_HOME", filepath.Join(root, "data"))
	t.Setenv("OMNIPUS_CHILD_WU19_CONTROL", "allowed-control")
	for _, key := range wu19GitKeys()[5:] {
		t.Setenv(key, "wu19-innocuous-fixture")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "git"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if writeErr := os.WriteFile(filepath.Join(root, name), b, 0o700); writeErr != nil {
		t.Fatal(writeErr)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := runGit(context.Background(), root, "-test.run=^TestWU19_WorktreeGitChildEnvironment$", "--", "--wu19-git")
	if err != nil {
		t.Fatalf("positive control: runGit failed: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("child snapshot invalid: %v", err)
	}
	t.Run("positive_control", func(t *testing.T) {
		if got["HOME"] != root || got["PATH"] != os.Getenv("PATH") || got["OMNIPUS_CHILD_WU19_CONTROL"] != "allowed-control" {
			t.Fatal("git child lost exact allowlisted controls")
		}
		if got["GIT_TERMINAL_PROMPT"] != "0" || got["GIT_OPTIONAL_LOCKS"] != "0" {
			t.Fatal("git child lost explicit noninteractive additions")
		}
	})
	for _, key := range wu19GitKeys()[5:] {
		if _, exists := got[key]; exists {
			t.Errorf("FR-239: gateway-only key %s reached git child; want absent", key)
		}
	}
}

func wu19GitKeys() []string {
	return []string{"HOME", "PATH", "OMNIPUS_CHILD_WU19_CONTROL", "GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS", "OPENROUTER_API_KEY", "EMAIL_PASSWORD", "OMNIPUS_BEARER_TOKEN", "WU19_FUTURE_GATEWAY_VALUE"}
}
