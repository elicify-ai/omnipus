package utils

import "testing"

// TestValidateSkillIdentifier_DestructiveIdentifiersRefused pins the class the
// old denylist missed. A skill identifier becomes a single path segment:
// install_skill does filepath.Join(skillsDir, slug) and, with force=true,
// os.RemoveAll on the result. The old rule rejected "/", "\\" and ".." — so a
// lone "." sailed through, filepath.Join collapsed it to skillsDir itself, and
// the call deleted every installed skill on the box before the registry was
// even resolved.
//
// Only "." was exploitable, which is the signature of a denylist patched at the
// payload rather than at the class: "./", "..", "a/../.." were all refused
// while the one spelling nobody had written down was not. The allowlist below
// is what makes the property hold for spellings nobody has thought of yet.
func TestValidateSkillIdentifier_DestructiveIdentifiersRefused(t *testing.T) {
	for _, identifier := range []string{
		".",   // the demonstrated wipe: Join(dir, ".") == dir
		"..",  // the parent directory
		"./",  //
		"../", //
		"...", //
		".hidden",
		"pdf/..",
		"a/../..",
		"../etc/passwd",
		"path/traversal",
		`path\traversal`,
		"/absolute",
		`C:\windows`,
		"~",
		"~/.ssh",
		"skill name",    // whitespace: becomes a directory with a space in it
		" github",       // leading space, same
		"github ",       // trailing space, same
		"skill\x00null", // NUL byte
		"skill\nname",   // newline
		"-leading-hyphen",
		"trailing-hyphen-",
		"double--hyphen",
		"under_score",
		"dot.separated",
		"semi;colon",
		"star*",
		"$var",
	} {
		if err := ValidateSkillIdentifier(identifier); err == nil {
			t.Errorf("ValidateSkillIdentifier(%q) = nil, want refusal — this value is used "+
				"directly as a filesystem path segment that install_skill os.RemoveAll's",
				identifier)
		}
	}
}

// TestValidateSkillIdentifier_EmptyIdentifier keeps the caller-facing message
// for a blank identifier distinct from a malformed one. Existing tools assert
// on this exact wording.
func TestValidateSkillIdentifier_EmptyIdentifier(t *testing.T) {
	for _, identifier := range []string{"", "   ", "\t"} {
		err := ValidateSkillIdentifier(identifier)
		if err == nil {
			t.Fatalf("ValidateSkillIdentifier(%q) = nil, want refusal", identifier)
		}
		if got, want := err.Error(), "identifier is required and must be a non-empty string"; got != want {
			t.Errorf("ValidateSkillIdentifier(%q) = %q, want %q", identifier, got, want)
		}
	}
}

// TestValidateSkillIdentifier_TooLong bounds the directory name.
func TestValidateSkillIdentifier_TooLong(t *testing.T) {
	long := ""
	for len(long) <= MaxSkillIdentifierLength {
		long += "a"
	}
	if err := ValidateSkillIdentifier(long); err == nil {
		t.Errorf("ValidateSkillIdentifier(<%d chars>) = nil, want refusal", len(long))
	}
}

// TestValidateSkillIdentifier_RealIdentifiersAccepted is the positive control.
// Tightening a validator is only correct if the values the product actually
// uses still pass: real ClawHub slugs, and the registry names shipped in the
// default config (pkg/config/defaults.go seeds "clawhub") plus the "github"
// marketplace type. Without this test, a validator that refuses everything
// would look like a successful fix.
func TestValidateSkillIdentifier_RealIdentifiersAccepted(t *testing.T) {
	for _, identifier := range []string{
		"github",
		"clawhub",
		"docker-compose",
		"pdf",
		"a",
		"skill-with-many-hyphens-in-it",
		"MixedCase",
		"v2",
		"web3-tools",
	} {
		if err := ValidateSkillIdentifier(identifier); err != nil {
			t.Errorf("ValidateSkillIdentifier(%q) = %v, want nil — this is a legitimate "+
				"skill slug or registry name", identifier, err)
		}
	}
}
