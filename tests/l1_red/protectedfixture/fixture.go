// Package protectedfixture is test-only support. Its oracle is the binding
// L1 amendment §2 D2/D3, never the implementation's SecretEntries list.
package protectedfixture

import (
	"os"
	"path/filepath"
	"testing"
)

const Sentinel = "WU23_PROTECTED_SENTINEL\n"

// ProtectedRelative enumerates every D2 named class plus the D1 unknown class.
// A4's acting/root uploads and A7 helper assets are separately positive cases.
func ProtectedRelative() []string {
	return []string{
		"master.key", "credentials.json", "config.json", "cli.token", "auth.json",
		"config.json.backup-fixture", "credentials.json.copy-fixture", "master.key.copy-fixture", "backups/archive.txt",
		"system/audit.jsonl", "system/audit-chain-checkpoint.json", "system/state.json", "logs/gateway.txt",
		"gateway.pid", "gateway.port", "boot_epoch-fixture",
		"entities/agents/self.json", "workspaces/alpha.json",
		"skills/fixture/SKILL.md", "skills/fixture/AGENT.md", "skills/fixture/AGENTS.md", "USER.md",
		"agents/self/SOUL.md", "agents/self/AGENT.md", "agents/self/HEARTBEAT.md", "agents/self/MEMORY.md", "agents/self/skills/private.txt",
		"sessions/private.txt", "session_messages/private.txt", "session_lifecycle/private.txt", "tool_results/private.txt",
		"tasks/private.txt", "plans/private.txt", "pins/private.txt", "milestones/private.txt", "uploads/other/private.txt",
		"agents/self/.omnipus/last-session.md", "agents/self/.omnipus/private/memory.txt", "agents/self/memory/private.txt", "agents/self/sessions/private.txt",
		"workspaces/alpha/.omnipus/carry-forward/self.md", "workspaces/alpha/.omnipus/shared-memory.txt",
		"channels/private.txt", "notifications/private.txt", "browser/private.txt", "cache/private.txt",
		"inbox/private.txt", "email-watch/private.txt", "delivery/private.txt", "cron/private.txt",
		"agents/other/work.txt", "workspaces/beta/work/private.txt",
	}
}

type Layout struct {
	Home, Work, AgentHome, Shared, Outside string
}

func New(t *testing.T) Layout {
	t.Helper()
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	f := Layout{Home: filepath.Join(real, "data"), Work: filepath.Join(real, "data", "workspaces", "alpha", "work"), AgentHome: filepath.Join(real, "data", "agents", "self"), Shared: filepath.Join(real, "shared"), Outside: filepath.Join(real, "outside")}
	for _, dir := range []string{f.Home, f.Work, f.AgentHome, f.Shared, f.Outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", real)
	t.Setenv("USERPROFILE", real)
	t.Setenv("OMNIPUS_HOME", f.Home)
	for _, rel := range ProtectedRelative() {
		Write(t, filepath.Join(f.Home, filepath.FromSlash(rel)), Sentinel)
	}
	for _, p := range []string{
		filepath.Join(f.Work, "allowed.txt"), filepath.Join(f.AgentHome, "allowed.txt"), filepath.Join(f.Shared, "allowed.txt"),
		filepath.Join(f.Home, "media", "allowed.txt"), filepath.Join(f.Home, "uploads", "acting", "allowed.txt"), filepath.Join(f.Home, "uploads", "root", "allowed.txt"),
		filepath.Join(f.Home, "skills", "fixture", "helper.txt"),
	} {
		Write(t, p, "WU23_ALLOWED_CONTROL\n")
	}
	return f
}

func Write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
