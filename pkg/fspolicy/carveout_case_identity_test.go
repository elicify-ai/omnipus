//go:build goolm && stdjson

package fspolicy

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests cover the APFS case-insensitivity bypass: $OMNIPUS_HOME/CLI.TOKEN
// and $OMNIPUS_HOME/cli.token are ONE file to the kernel and two strings to a
// byte-comparing guard, and filepath.EvalSymlinks does not canonicalise case.
// Measured through the real EffectiveFSPolicy + ResolvePath + ReadFile before
// the fix, the uppercase spelling returned the live gateway bearer token, the
// master key and the decrypted credential store; with a workspace mount it also
// wrote config.json and destroyed master.key. See pathidentity.go's header.
//
// Every denial case here is a TABLE OVER SPELLINGS OF THE SAME FILE, with the
// lowercase spelling as the control, because a single-spelling assertion is
// exactly what let this through: the lowercase test passed the whole time.

// caseProbe reports what the volume backing dir actually does, rather than
// assuming. Both properties were measured on this project's macOS APFS volume
// (case-insensitive, normalization-insensitive) but neither is guaranteed:
// macOS can be formatted case-sensitive, and Linux CI certainly is.
type caseProbe struct {
	caseInsensitive          bool
	normalizationInsensitive bool
}

func probeVolume(t *testing.T, dir string) caseProbe {
	t.Helper()
	var p caseProbe

	if err := os.WriteFile(filepath.Join(dir, "caseprobe.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatalf("case probe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CASEPROBE.TMP")); err == nil {
		p.caseInsensitive = true
	}

	// "cafe" + U+0301 (NFD) written; "caf" + U+00E9 (NFC) read back.
	if err := os.WriteFile(filepath.Join(dir, "cafe\u0301probe.tmp"), []byte("x"), 0o600); err != nil {
		t.Fatalf("normalization probe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "caf\u00e9probe.tmp")); err == nil {
		p.normalizationInsensitive = true
	}
	return p
}

// newSecretHome lays out a realistic $OMNIPUS_HOME on disk. Real files matter:
// the fix judges containment by device+inode, so a test over paths that do not
// exist would exercise only the string fallback and prove nothing about the
// primitive that actually closes the hole.
func newSecretHome(t *testing.T) (home string, probe caseProbe) {
	t.Helper()
	base := t.TempDir()
	probe = probeVolume(t, base)
	home = filepath.Join(base, ".omnipus")

	for _, d := range []string{
		filepath.Join("agents", "mia"),
		filepath.Join("agents", "other"),
		filepath.Join("entities", "agents"),
		filepath.Join("workspaces", "w1"),
	} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range map[string]string{
		"cli.token":        "LIVE-GATEWAY-BEARER-TOKEN",
		"master.key":       "0123456789abcdef",
		"credentials.json": `{"openrouter":"sk-or-REAL"}`,
		"config.json":      `{"sandbox":{"mode":"on"}}`,
		filepath.Join("entities", "agents", "mia.json"): `{"locked":true}`,
		filepath.Join("agents", "other", "SOUL.md"):     "other agent soul",
		filepath.Join("agents", "mia", "notes.md"):      "my own notes",
	} {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, probe
}

func miaPolicy(t *testing.T, home string) FSPolicy {
	t.Helper()
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return FSPolicy{
		WorkDir:   filepath.Join(realHome, "agents", "mia"),
		Scope:     FSScopeConfined,
		CarveOuts: buildCarveOuts(realHome),
	}
}

// TestCarveOut_CaseVariantSpellings_OfTheSameSecret is the core regression: for
// each secret, every spelling that names THE SAME FILE on this volume must be
// denied, not merely the lowercase one.
func TestCarveOut_CaseVariantSpellings_OfTheSameSecret(t *testing.T) {
	home, probe := newSecretHome(t)
	policy := miaPolicy(t, home)
	realHome := filepath.Dir(filepath.Dir(policy.WorkDir))

	cases := []struct {
		name     string
		spelling string
	}{
		{"control: lowercase cli.token", "cli.token"},
		{"UPPERCASE cli.token", "CLI.TOKEN"},
		{"MixedCase cli.token", "Cli.Token"},
		{"control: lowercase master.key", "master.key"},
		{"UPPERCASE master.key", "MASTER.KEY"},
		{"MixedCase master.key", "Master.Key"},
		{"control: lowercase credentials.json", "credentials.json"},
		{"MixedCase credentials.json", "Credentials.json"},
		{"control: lowercase config.json", "config.json"},
		{"UPPERCASE config.json", "CONFIG.JSON"},
		// Directory-SEGMENT variants: the bypass is not limited to the leaf.
		{"control: entities/agents/mia.json", filepath.Join("entities", "agents", "mia.json")},
		{"dir-segment Entities/agents/mia.json", filepath.Join("Entities", "agents", "mia.json")},
		{"dir-segment ENTITIES/Agents/mia.json", filepath.Join("ENTITIES", "Agents", "mia.json")},
		{"control: agents/other/SOUL.md", filepath.Join("agents", "other", "SOUL.md")},
		{"dir-segment Agents/other/SOUL.md", filepath.Join("Agents", "other", "SOUL.md")},
		{"dir-segment agents/OTHER/SOUL.md", filepath.Join("agents", "OTHER", "SOUL.md")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := filepath.Join(realHome, tc.spelling)

			// Volume-honest precondition. On a case-SENSITIVE volume a
			// case-variant spelling names a DIFFERENT, non-existent file, so
			// "not a carve-out" is the correct answer and there is nothing to
			// leak. Assert that explicitly rather than letting the case pass
			// vacuously.
			_, statErr := os.Stat(target)
			sameFileAsSecret := statErr == nil
			if !sameFileAsSecret {
				if probe.caseInsensitive {
					t.Fatalf("volume reports case-insensitive but %q does not exist — probe and reality disagree", target)
				}
				t.Skipf("case-sensitive volume: %q is a different, non-existent file, nothing to leak", tc.spelling)
			}

			if !IsCarveOut(target, policy) {
				t.Errorf("IsCarveOut(%q) = false; this spelling IS the secret file on this volume, so it must be denied", tc.spelling)
			}
		})
	}
}

// TestCarveOut_OwnTreeException_SurvivesTheFix guards the other direction: the
// fix must not lock an agent out of its own home. It also pins the asymmetry —
// the own-tree exception is GRANT-side, so it must never be widened by folding
// into re-admitting a DIFFERENT agent's home.
func TestCarveOut_OwnTreeException_SurvivesTheFix(t *testing.T) {
	home, _ := newSecretHome(t)
	policy := miaPolicy(t, home)
	realHome := filepath.Dir(filepath.Dir(policy.WorkDir))

	ownFile := filepath.Join(realHome, "agents", "mia", "notes.md")
	if IsCarveOut(ownFile, policy) {
		t.Errorf("IsCarveOut(%q) = true; an agent must still reach its own home", ownFile)
	}

	// A not-yet-created file in the agent's own home (the ordinary write case,
	// where identity cannot answer for the leaf and the ancestor chain must).
	ownNew := filepath.Join(realHome, "agents", "mia", "new-file.md")
	if IsCarveOut(ownNew, policy) {
		t.Errorf("IsCarveOut(%q) = true; a new file in the agent's own home must be writable", ownNew)
	}

	// Another agent's home stays denied in EVERY spelling.
	for _, spelling := range []string{
		filepath.Join("agents", "other", "SOUL.md"),
		filepath.Join("Agents", "other", "SOUL.md"),
		filepath.Join("agents", "OTHER", "SOUL.md"),
	} {
		p := filepath.Join(realHome, spelling)
		if _, err := os.Stat(p); err != nil {
			continue // case-sensitive volume: different, non-existent file
		}
		if !IsCarveOut(p, policy) {
			t.Errorf("IsCarveOut(%q) = false; another agent's home must never be re-admitted by the own-tree exception", spelling)
		}
	}
}

// TestCarveOut_HardLinkToSecret_IsDenied proves the primitive is identity and
// not a cleverer string comparison. A hard link inside the agent's own working
// directory has an unrelated path and the SAME inode; no amount of case folding
// or normalization could catch it.
func TestCarveOut_HardLinkToSecret_IsDenied(t *testing.T) {
	home, _ := newSecretHome(t)
	policy := miaPolicy(t, home)
	realHome := filepath.Dir(filepath.Dir(policy.WorkDir))

	link := filepath.Join(realHome, "agents", "mia", "innocent.txt")
	if err := os.Link(filepath.Join(realHome, "credentials.json"), link); err != nil {
		t.Skipf("hard links unavailable on this filesystem: %v", err)
	}
	if !IsCarveOut(link, policy) {
		t.Errorf("IsCarveOut(%q) = false; a hard link to credentials.json is the same file and must be denied", link)
	}
}

// TestCarveOut_NonASCIISpelling_IsDenied is the case a strings.ToLower fold
// would have MISSED. ToLower leaves NFD and NFC forms distinct, so an install
// whose $OMNIPUS_HOME contains a non-ASCII component (a macOS account named
// "José") stayed exploitable under a fold-only fix — the attacker simply
// supplies the other normalization form. Identity is immune.
func TestCarveOut_NonASCIISpelling_IsDenied(t *testing.T) {
	base := t.TempDir()
	probe := probeVolume(t, base)
	if !probe.normalizationInsensitive {
		t.Skip("volume is normalization-sensitive: NFC and NFD names are genuinely different files here")
	}

	// Home directory created in the NFD spelling.
	home := filepath.Join(base, "cafe\u0301.omnipus")
	if err := os.MkdirAll(filepath.Join(home, "agents", "mia"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "cli.token"), []byte("LIVE-TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	policy := FSPolicy{
		WorkDir:   filepath.Join(realHome, "agents", "mia"),
		Scope:     FSScopeConfined,
		CarveOuts: buildCarveOuts(realHome),
	}

	// Attacker supplies the NFC spelling of the same directory.
	nfc := filepath.Join(base, "caf\u00e9.omnipus", "cli.token")
	if !IsCarveOut(nfc, policy) {
		t.Errorf("IsCarveOut(%q) = false; an NFC spelling of an NFD home is the same file — a case fold alone would miss this", nfc)
	}
}

// TestCarveOut_ResidualIsDocumented pins the residual documented in
// pathidentity.go's header, in BOTH directions.
//
// It used to t.Log whichever branch it took and assert nothing about the
// residual itself, so it could not fail whatever IsCarveOut did — a residual
// "documented" by a test that cannot fail is not documented. It now asserts the
// actual boundary:
//
//	secret ABSENT  + non-ASCII variant spelling -> NOT matched (the residual)
//	secret PRESENT + the same spelling          -> matched (identity closes it)
//
// Both halves have to bite. Without the first, a future change that quietly
// widened the deny side (denying by basename alone, say) would go unnoticed
// here and the header would describe a residual that no longer exists. Without
// the second, the residual could silently grow to cover existing files too.
func TestCarveOut_ResidualIsDocumented(t *testing.T) {
	base := t.TempDir()
	probe := probeVolume(t, base)
	if !probe.normalizationInsensitive {
		t.Skip("volume is normalization-sensitive: the residual does not arise here")
	}

	home := filepath.Join(base, "cafe\u0301.omnipus") // NFD
	if err := os.MkdirAll(filepath.Join(home, "agents", "mia"), 0o755); err != nil {
		t.Fatal(err)
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	policy := FSPolicy{
		WorkDir:   filepath.Join(realHome, "agents", "mia"),
		Scope:     FSScopeConfined,
		CarveOuts: buildCarveOuts(realHome),
	}
	nfc := filepath.Join(base, "caf\u00e9.omnipus", "master.key") // NFC spelling

	// master.key does NOT exist yet: identity cannot answer, the fold fallback
	// does not bridge NFD/NFC, so this spelling is NOT recognised. Asserted,
	// not logged — if this ever starts passing, the residual has narrowed and
	// pathidentity.go's header is now WRONG and must be updated in the same
	// change that narrowed it.
	if IsCarveOut(nfc, policy) {
		t.Fatalf("IsCarveOut(%q) = true for a NON-EXISTENT secret in a non-ASCII variant spelling. "+
			"That is stricter than pathidentity.go's documented residual #1 claims. This is not a "+
			"failure of the product — it is a failure of the DOCUMENTATION: update the header's "+
			"residual list (and this test) to describe what the code now does.", nfc)
	}

	// The moment the secret exists, identity answers and the same spelling is
	// denied. This is the assertion that must never regress.
	if err := os.WriteFile(filepath.Join(realHome, "master.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsCarveOut(nfc, policy) {
		t.Errorf("IsCarveOut(%q) = false once master.key exists; identity must close the residual as soon as the file is real", nfc)
	}
}

// TestIsSecretName_FoldsCase covers the name-level check the Linux
// sibling-granting walk uses. It compares names, not paths, so there is no
// inode to consult and folding is the whole mechanism.
func TestIsSecretName_FoldsCase(t *testing.T) {
	for _, name := range []string{
		"master.key", "MASTER.KEY", "Master.Key",
		"credentials.json", "Credentials.JSON",
		"config.json", "CONFIG.json",
		"cli.token", "CLI.TOKEN",
		"entities", "Entities", "ENTITIES",
		"agents", "Agents",
		"workspaces", "WorkSpaces",
		"config.json.bak-123", "CONFIG.JSON.bak-123",
		"master.key.old", "MASTER.KEY.OLD",
		// skills — ADR-072 D10 Part A / D10.1 (SecretEntriesAlwaysPathOnly):
		// path-denied like every entry above, but deliberately excluded from
		// pkg/tools/shell.go's literal-text guard because "skills" is an
		// ordinary English word — see TestSecretEntries_SkillsDeniedForPathsNotTextGuard.
		"skills", "SKILLS", "Skills",
	} {
		if !IsSecretName(name) {
			t.Errorf("IsSecretName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"notes.md", "SOUL.md", "logs"} {
		if IsSecretName(name) {
			t.Errorf("IsSecretName(%q) = true, want false", name)
		}
	}
}
