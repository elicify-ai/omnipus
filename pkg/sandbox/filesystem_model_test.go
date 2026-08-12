// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseFilesystemModel covers spec FR-1.1/FR-1.2 and dataset rows 1-4.
// Case folding and aliases are deliberately NOT accepted: the key is new, has
// no legacy spellings to honour, and a typo that resolved to a weaker model
// would hand the operator a posture they never chose.
func TestParseFilesystemModel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		want    FilesystemModel
		wantErr bool
	}{
		{"confined", "confined", FilesystemModelConfined, false},
		{"open", "open", FilesystemModelOpen, false},
		{"empty falls back", "", FilesystemModelConfined, false},
		{"uppercase rejected", "OPEN", "", true},
		{"typo rejected", "opne", "", true},
		{"whitespace not trimmed", " open", "", true},
		{"legacy alias rejected", "enabled", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFilesystemModel(tc.in, FilesystemModelConfined)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseFilesystemModel(%q) = %q, want error", tc.in, got)
				}
				// The error must name both valid values — an operator who
				// mistyped needs to be told what to type instead.
				for _, want := range []string{"confined", "open"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q must name valid value %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFilesystemModel(%q): unexpected error %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseFilesystemModel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDefaultPolicy_ConfinedIsUnchanged is the safety net the whole of ADR-060
// rests on (spec FR-2.5). DefaultPolicy must remain exactly the confined model.
// If this fails, nothing else in the ADR is safe to land: the baseline every
// other assertion is measured against has moved.
func TestDefaultPolicy_ConfinedIsUnchanged(t *testing.T) {
	home := t.TempDir()
	legacy := DefaultPolicy(home, []string{"/srv/data"}, []string{"/opt/tools"}, nil, []uint16{5173})
	confined := DefaultPolicyForModel(FilesystemModelConfined, home,
		[]string{"/srv/data"}, []string{"/opt/tools"}, nil, []uint16{5173})

	if !reflect.DeepEqual(legacy.FilesystemRules, confined.FilesystemRules) {
		t.Errorf("DefaultPolicy must be identical to the confined model\n legacy: %+v\n confined: %+v",
			legacy.FilesystemRules, confined.FilesystemRules)
	}
	if legacy.ReadsOpen || legacy.ExecOpen {
		t.Error("DefaultPolicy must not report an open model")
	}
}

// TestDefaultPolicyForModel_Open covers FR-2.1/2.2/2.3/2.4 and dataset rows
// 6, 9 and 10 at the policy layer.
func TestDefaultPolicyForModel_Open(t *testing.T) {
	home := t.TempDir()
	policy := DefaultPolicyForModel(FilesystemModelOpen, home,
		[]string{"/srv/data"}, []string{"/opt/tools"}, nil, nil)

	if !policy.ReadsOpen || !policy.ExecOpen {
		t.Fatalf("open model must set both ReadsOpen and ExecOpen; got %v/%v",
			policy.ReadsOpen, policy.ExecOpen)
	}

	has := func(path string) bool {
		for _, r := range policy.FilesystemRules {
			if filepath.Clean(r.Path) == filepath.Clean(path) {
				return true
			}
		}
		return false
	}

	// FR-2.1: the enumerated read-only system list is the defect being removed.
	for _, gone := range []string{"/usr/lib", "/usr/bin", "/etc/ssl", "/proc"} {
		if has(gone) {
			t.Errorf("open model must not enumerate read-only system path %q — "+
				"enumeration is what the model removes", gone)
		}
	}

	// FR-2.2: exec paths stop mattering, and an operator who configured them is
	// not punished for it — the key stays valid, it just no longer contributes.
	if has("/opt/tools") {
		t.Error("open model must not emit allowed_exec_paths rules")
	}

	// FR-2.3: writes are untouched. The model governs reading and running only.
	for _, kept := range []string{home, "/tmp", "/dev/null", "/dev/shm", "/srv/data"} {
		if !has(kept) {
			t.Errorf("open model must keep write grant %q — the model never widens or narrows writes", kept)
		}
	}
}

// TestDefaultPolicy_DeniedPathsUnderBothModels covers FR-9.4. The exposure
// these close exists in shipped code and is not introduced by the open model,
// so gating the fix on the model would leave it alive behind confined — where
// an operator would least expect it.
func TestDefaultPolicy_DeniedPathsUnderBothModels(t *testing.T) {
	home := t.TempDir()
	for _, model := range []FilesystemModel{FilesystemModelConfined, FilesystemModelOpen} {
		policy := DefaultPolicyForModel(model, home, nil, nil, nil, nil)
		want := SecretPaths(home)
		if !reflect.DeepEqual(policy.DeniedPaths, want) {
			t.Errorf("model %q: DeniedPaths = %v, want %v", model, policy.DeniedPaths, want)
		}
	}
}

// TestSecretPaths pins the set itself. agents/ being absent is the assertion
// that matters most: an earlier draft listed it, which would have made every
// agent's own working directory unwritable while appearing to harden.
func TestSecretPaths(t *testing.T) {
	got := SecretPaths("/home/x/.omnipus")
	want := []string{
		"/home/x/.omnipus/master.key",
		"/home/x/.omnipus/credentials.json",
		"/home/x/.omnipus/config.json",
		"/home/x/.omnipus/cli.token",
		"/home/x/.omnipus/entities",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SecretPaths = %v, want %v", got, want)
	}
	if IsSecretEntry("agents") {
		t.Error("agents/ must NOT be a secret entry — it holds agent workspaces, " +
			"which must stay writable; entities/ holds the policy")
	}
	if !IsSecretEntry("entities") {
		t.Error("entities/ must be a secret entry — it holds per-agent tool policy")
	}
	if SecretPaths("") != nil {
		t.Error("SecretPaths(\"\") must be nil, not denies rooted at /")
	}
}

// TestSecretPaths_CoversConfigBackups closes a hole found by inspecting a REAL
// $OMNIPUS_HOME rather than reasoning about the layout.
//
// A live install carried config.json.bak-20260811-224607 next to config.json,
// written when a migration rewrote the config. The backup contained the gateway
// CLI token and the user account list — everything the original holds. Denying
// config.json while leaving a byte-identical copy beside it readable is a deny
// that reads as correct and protects nothing.
func TestSecretPaths_CoversConfigBackups(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{
		"config.json",
		"config.json.bak-20260811-224607",
		"credentials.json.bak-20260101-000000",
		"master.key.old",
		"notes.txt",        // control: an ordinary file must NOT be swept in
		"configuration.md", // control: shares a prefix fragment, not the prefix
		"config.json2",     // control: no dot separator, so not a backup form
	} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	got := SecretPaths(home)
	has := func(name string) bool {
		want := filepath.Join(home, name)
		for _, p := range got {
			if p == want {
				return true
			}
		}
		return false
	}

	for _, mustDeny := range []string{
		"config.json",
		"config.json.bak-20260811-224607",
		"credentials.json.bak-20260101-000000",
		"master.key.old",
	} {
		if !has(mustDeny) {
			t.Errorf("%q must be in the secret set; a copy of a secret is a secret", mustDeny)
		}
	}
	for _, mustAllow := range []string{"notes.txt", "configuration.md", "config.json2"} {
		if has(mustAllow) {
			t.Errorf("%q must NOT be swept into the secret set — over-denying breaks "+
				"ordinary files and teaches operators to distrust the deny list", mustAllow)
		}
	}
}
