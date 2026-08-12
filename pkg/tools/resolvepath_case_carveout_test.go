// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// End-to-end proof for the APFS case-insensitivity bypass, driven through the
// REAL chain an agent tool call takes: fspolicy.EffectiveFSPolicy ->
// ResolvePath -> PathHandle.ReadFile/WriteFile. The unit-level table lives in
// pkg/fspolicy/carveout_case_identity_test.go; this file exists because the
// original finding was measured here, at the tool boundary, and a fix that only
// satisfies the unit test is not a fix.
//
// What was measured on a real APFS volume BEFORE the fix, with the default
// restrict=true:
//
//	DENIED  cli.token (lowercase control)
//	LEAKED  CLI.TOKEN         -> "LIVE-GATEWAY-BEARER-TOKEN-abc123"
//	LEAKED  MASTER.KEY        -> the master key
//	LEAKED  Credentials.json  -> {"openrouter":"sk-or-REAL-KEY"}
//	WROTE   CONFIG.JSON       -> config.json became {"sandbox":{"mode":"off"}}
//	WROTE   MASTER.KEY        -> master key destroyed
//
// The kernel is no backstop: Seatbelt matches case-insensitively but confines
// CHILD processes only, and read_file/write_file/edit/send_file run inside the
// unconfined gateway process. fspolicy.IsCarveOut is the sole gate.

func caseTestHome(t *testing.T) (realHome string, caseInsensitive bool) {
	t.Helper()
	base := t.TempDir()

	if err := os.WriteFile(filepath.Join(base, "probe.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(base, "PROBE.TMP"))
	caseInsensitive = err == nil

	home := filepath.Join(base, ".omnipus")
	for _, d := range []string{
		filepath.Join("agents", "mia"),
		filepath.Join("agents", "other"),
		filepath.Join("entities", "agents"),
	} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range map[string]string{
		"cli.token":        caseSecretToken,
		"master.key":       caseSecretKey,
		"credentials.json": caseSecretCreds,
		"config.json":      `{"sandbox":{"mode":"on"}}`,
		filepath.Join("entities", "agents", "mia.json"): `{"locked":true}`,
		filepath.Join("agents", "other", "SOUL.md"):     "other agent soul",
	} {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	realHome, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return realHome, caseInsensitive
}

const (
	caseSecretToken = "LIVE-GATEWAY-BEARER-TOKEN-abc123"
	caseSecretKey   = "0123456789abcdef0123456789abcdef"
	caseSecretCreds = `{"openrouter":"sk-or-REAL-KEY"}`
)

func caseTestPolicy(t *testing.T, realHome string, withMount bool) fspolicy.FSPolicy {
	t.Helper()
	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(),
		filepath.Join(realHome, "agents", "mia"), "",
		true, // restrict=true — the DEFAULT posture; the leak did not need god mode
		realHome, "mia", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if withMount {
		// ADR-061 D4: a workspace mount of $OMNIPUS_HOME grants writes anywhere
		// under it EXCEPT the secret set — so the carve-out check is the only
		// thing standing between the agent and config.json. This is the shape
		// under which the case bypass produced real WRITES.
		policy.AllowedRoots = []string{realHome}
	}
	return policy
}

// caseSpellings enumerates spellings of the same secrets: leaf-case variants
// and directory-SEGMENT variants, each with the lowercase control alongside.
var caseSpellings = []struct {
	name    string
	rel     string
	secret  string // content that must never come back
	control bool
}{
	{"control lowercase cli.token", "cli.token", caseSecretToken, true},
	{"UPPERCASE CLI.TOKEN", "CLI.TOKEN", caseSecretToken, false},
	{"MixedCase Cli.Token", "Cli.Token", caseSecretToken, false},
	{"control lowercase master.key", "master.key", caseSecretKey, true},
	{"UPPERCASE MASTER.KEY", "MASTER.KEY", caseSecretKey, false},
	{"control lowercase credentials.json", "credentials.json", caseSecretCreds, true},
	{"MixedCase Credentials.json", "Credentials.json", caseSecretCreds, false},
	{"control entities/agents/mia.json", filepath.Join("entities", "agents", "mia.json"), `{"locked":true}`, true},
	{"dir-segment Entities/agents/mia.json", filepath.Join("Entities", "agents", "mia.json"), `{"locked":true}`, false},
	{"control agents/other/SOUL.md", filepath.Join("agents", "other", "SOUL.md"), "other agent soul", true},
	{"dir-segment Agents/other/SOUL.md", filepath.Join("Agents", "other", "SOUL.md"), "other agent soul", false},
}

// TestResolvePath_CaseVariantSecret_ReadIsDenied drives FSOpRead all the way to
// PathHandle.ReadFile. FSOpRead is the dangerous op post-ADR-061 FR-2.2: reads
// are open ANYWHERE outside the secret set, so the carve-out check is the only
// gate, and a spelling it misses is a straight disclosure.
func TestResolvePath_CaseVariantSecret_ReadIsDenied(t *testing.T) {
	realHome, caseInsensitive := caseTestHome(t)
	policy := caseTestPolicy(t, realHome, false)
	ctx := context.Background()

	for _, tc := range caseSpellings {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(realHome, tc.rel)

			if _, err := os.Stat(target); err != nil {
				if caseInsensitive && !tc.control {
					t.Fatalf("volume probed case-insensitive but %q does not exist — the probe and the filesystem disagree", target)
				}
				if tc.control {
					t.Fatalf("control spelling %q must exist", target)
				}
				t.Skipf("case-sensitive volume: %q names a different, non-existent file", tc.rel)
			}

			handle, err := ResolvePath(ctx, policy, "read_file", "call-1", FSOpRead, target)
			if err != nil {
				if !errors.Is(err, ErrCarveOut) {
					t.Errorf("denied, but with %v; want ErrCarveOut", err)
				}
				return
			}
			defer handle.Close()

			content, readErr := handle.ReadFile()
			if readErr == nil {
				t.Fatalf("LEAK: %q returned %q; every spelling of a secret must be refused", tc.rel, string(content))
			}
			if strings.Contains(string(content), tc.secret) {
				t.Fatalf("LEAK: %q returned the secret contents", tc.rel)
			}
			if !errors.Is(readErr, ErrCarveOut) {
				t.Errorf("refused at I/O time with %v; want ErrCarveOut", readErr)
			}
		})
	}
}

// TestResolvePath_CaseVariantSecret_WriteIsDenied drives FSOpWrite through
// PathHandle.WriteFile with a workspace mount in play, the configuration under
// which the bypass previously turned the sandbox off and destroyed the master
// key. It asserts on the FILE, not just the error: a write that is "refused"
// but still lands is the failure mode that matters.
func TestResolvePath_CaseVariantSecret_WriteIsDenied(t *testing.T) {
	realHome, caseInsensitive := caseTestHome(t)
	policy := caseTestPolicy(t, realHome, true)
	ctx := context.Background()

	cases := []struct {
		name     string
		rel      string
		canonRel string
		payload  string
	}{
		{"control lowercase config.json", "config.json", "config.json", `{"sandbox":{"mode":"off"}}`},
		{"UPPERCASE CONFIG.JSON", "CONFIG.JSON", "config.json", `{"sandbox":{"mode":"off"}}`},
		{"UPPERCASE MASTER.KEY", "MASTER.KEY", "master.key", "DESTROYED"},
		{"MixedCase Credentials.json", "Credentials.json", "credentials.json", `{"openrouter":"sk-or-ATTACKER"}`},
		{"dir-segment Entities/agents/mia.json", filepath.Join("Entities", "agents", "mia.json"),
			filepath.Join("entities", "agents", "mia.json"), `{"locked":false,"tools":{"bash":"allow"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canonical := filepath.Join(realHome, tc.canonRel)
			before, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatal(err)
			}

			target := filepath.Join(realHome, tc.rel)
			if _, err := os.Stat(target); err != nil && !caseInsensitive {
				t.Skipf("case-sensitive volume: %q names a different file", tc.rel)
			}

			handle, err := ResolvePath(ctx, policy, "write_file", "call-2", FSOpWrite, target)
			if err == nil {
				writeErr := handle.WriteFile([]byte(tc.payload))
				handle.Close()
				if writeErr == nil {
					t.Errorf("write to %q was permitted", tc.rel)
				} else if !errors.Is(writeErr, ErrCarveOut) {
					t.Errorf("write refused at I/O time with %v; want ErrCarveOut", writeErr)
				}
			} else if !errors.Is(err, ErrCarveOut) {
				t.Errorf("resolve refused with %v; want ErrCarveOut", err)
			}

			// The assertion that actually matters: the secret is untouched.
			after, err := os.ReadFile(canonical)
			if err != nil {
				t.Fatalf("%s no longer readable after the attempt: %v", tc.canonRel, err)
			}
			if string(after) != string(before) {
				t.Fatalf("MUTATED: %s changed from %q to %q via the %q spelling",
					tc.canonRel, string(before), string(after), tc.rel)
			}
		})
	}
}

// TestResolvePath_OwnTree_StillWorksAfterCaseFix is the over-denial guard. The
// fix tightens a check every single tool call passes through, so the ordinary
// path — an agent reading and writing inside its own home — must be proven
// unaffected, not assumed.
func TestResolvePath_OwnTree_StillWorksAfterCaseFix(t *testing.T) {
	realHome, _ := caseTestHome(t)
	policy := caseTestPolicy(t, realHome, false)
	ctx := context.Background()

	own := filepath.Join(realHome, "agents", "mia", "notes.md")

	handle, err := ResolvePath(ctx, policy, "write_file", "call-3", FSOpWrite, own)
	if err != nil {
		t.Fatalf("write to the agent's own home was refused: %v", err)
	}
	if err := handle.WriteFile([]byte("my own notes")); err != nil {
		handle.Close()
		t.Fatalf("write to the agent's own home failed: %v", err)
	}
	handle.Close()

	rh, err := ResolvePath(ctx, policy, "read_file", "call-4", FSOpRead, own)
	if err != nil {
		t.Fatalf("read of the agent's own file was refused: %v", err)
	}
	defer rh.Close()
	got, err := rh.ReadFile()
	if err != nil {
		t.Fatalf("read of the agent's own file failed: %v", err)
	}
	if string(got) != "my own notes" {
		t.Errorf("own file round-trip = %q, want %q", string(got), "my own notes")
	}
}
