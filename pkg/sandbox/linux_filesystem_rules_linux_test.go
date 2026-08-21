//go:build linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func linuxTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"agents", "sessions", "entities"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	for _, f := range []string{"master.key", "credentials.json", "config.json", "cli.token"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return home
}

func hasRule(rules []PathRule, path string) bool {
	for _, r := range rules {
		if r.Path == path {
			return true
		}
	}
	return false
}

// TestLinuxFilesystemRules_GatewayKeepsItsOwnVault is the asymmetry that makes
// the Linux half work at all. The gateway restricts ITSELF here (unlike macOS,
// where sandbox-exec confines only children), so applying the child exclusion
// at boot would lock the gateway out of the vault it has to unlock — a failure
// that presents as a corrupt install rather than as a policy mistake.
func TestLinuxFilesystemRules_GatewayKeepsItsOwnVault(t *testing.T) {
	lb := &LinuxBackend{}
	home := linuxTestHome(t)
	policy := DefaultPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil)

	gatewayRules, err := lb.linuxFilesystemRules(policy, false)
	if err != nil {
		t.Fatalf("gateway rules: %v", err)
	}
	if !hasRule(gatewayRules, filepath.Clean(home)) {
		t.Error("the gateway must keep a whole-tree grant on $OMNIPUS_HOME — " +
			"it has to read master.key to unlock the vault and write config.json on a settings change")
	}
}

// TestLinuxFilesystemRules_ChildLosesTheSecretSet is the other half: the same
// policy, rendered for a child, must not reach any secret.
func TestLinuxFilesystemRules_ChildLosesTheSecretSet(t *testing.T) {
	lb := &LinuxBackend{}
	home := linuxTestHome(t)
	policy := DefaultPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil)

	childRules, err := lb.linuxFilesystemRules(policy, true)
	if err != nil {
		t.Fatalf("child rules: %v", err)
	}

	if hasRule(childRules, filepath.Clean(home)) {
		t.Fatal("the child must NOT get a whole-tree grant on $OMNIPUS_HOME — it contains every secret")
	}
	if hasRule(childRules, "/") {
		t.Fatal("the child must NOT get a whole-tree grant on / — " +
			"reads-open is expressed by granting the siblings of the secret set, not the root itself")
	}
	for _, secret := range SecretPaths(home) {
		if hasRule(childRules, secret) {
			t.Errorf("child was granted secret %q", secret)
		}
	}
	// The exclusion must not have simply broken the workspace.
	if !hasRule(childRules, filepath.Join(home, "agents")) {
		t.Error("agents/ must stay granted for the child — it holds agent workspaces")
	}
	if hasRule(childRules, filepath.Join(home, "entities")) {
		t.Error("entities/ must NOT be granted for the child — it holds per-agent tool policy")
	}
}

// TestLinuxFilesystemRules_OpenModelGrantsRootReadNotWrite covers how "reads
// open" is expressed on a grant-only kernel. Landlock has no unfiltered allow,
// so the model becomes a grant on "/" — and that grant must never carry write,
// or the open model would silently become write-open too.
func TestLinuxFilesystemRules_OpenModelGrantsRootReadNotWrite(t *testing.T) {
	lb := &LinuxBackend{}
	policy := DefaultPolicyForModel(FilesystemModelOpen, t.TempDir(), nil, nil, nil, nil)

	rules, err := lb.linuxFilesystemRules(policy, false)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	var found bool
	for _, r := range rules {
		if r.Path != "/" {
			continue
		}
		found = true
		if r.Access&AccessWrite != 0 {
			t.Error("the root grant must never include write — " +
				"the filesystem model governs reading and running, never writing")
		}
		if r.Access&AccessRead == 0 || r.Access&AccessExecute == 0 {
			t.Errorf("root grant access = %#x, want read|execute", r.Access)
		}
	}
	if !found {
		t.Error("the open model must grant / on Linux; without it reads are not actually open")
	}
}

// TestLinuxFilesystemRules_ConfinedAddsNoRootGrant: confined must stay exactly
// as it was. If this fails the safety net is gone.
func TestLinuxFilesystemRules_ConfinedAddsNoRootGrant(t *testing.T) {
	lb := &LinuxBackend{}
	home := linuxTestHome(t)
	policy := DefaultPolicyForModel(FilesystemModelConfined, home, nil, nil, nil, nil)

	rules, err := lb.linuxFilesystemRules(policy, false)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	if hasRule(rules, "/") {
		t.Error("confined must not grant / — that would silently make it the open model")
	}
	if len(rules) != len(policy.FilesystemRules) {
		t.Errorf("confined gateway rules = %d, want the policy's %d unchanged",
			len(rules), len(policy.FilesystemRules))
	}
}
