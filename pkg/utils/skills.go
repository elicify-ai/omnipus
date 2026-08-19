package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// SkillIdentifierPattern is the canonical shape of a skill identifier — a skill
// slug or a registry name.
//
// It is the SAME expression as pkg/skills' unexported namePattern
// (pkg/skills/loader.go), which the loader, the manifest validator and
// SkillWriter.resolveSkillDir all enforce. pkg/skills imports pkg/utils, so the
// constant cannot be imported in that direction without a cycle; it is
// duplicated here and pinned by TestSkillIdentifierPattern_MatchesNamePattern
// (pkg/skills/registry_test.go), which fails if the two ever drift.
//
// SECURITY: an identifier validated by this package is used directly as a
// single filesystem path segment — filepath.Join(skillsDir, slug) in
// install_skill, which then os.RemoveAll's that path on force=true — and as a
// URL path segment against a registry. The previous rule here was a denylist
// ("no /", "no \\", "no ..") and it missed the one-character case: a lone ".".
// filepath.Join(skillsDir, ".") is skillsDir itself, so install_skill with
// slug="." deleted every installed skill on the box, before the registry was
// even resolved. An allowlist is the only form that closes the class rather
// than the demonstrated payload: this pattern admits exactly one well-formed
// name and rejects ".", "..", "", separators, absolute paths, leading or
// trailing hyphens, whitespace and NUL bytes by construction.
const SkillIdentifierPattern = `^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`

// MaxSkillIdentifierLength mirrors skills.MaxNameLength. A skill identifier
// becomes a directory name, so it is bounded for the same reason the loader
// bounds a skill name.
const MaxSkillIdentifierLength = 64

var skillIdentifierRE = regexp.MustCompile(SkillIdentifierPattern)

// ValidateSkillIdentifier validates that the given skill identifier (a slug or
// a registry name) is a single, well-formed name: non-empty, at most
// MaxSkillIdentifierLength characters, and alphanumeric with single interior
// hyphens (SkillIdentifierPattern).
//
// The identifier is checked exactly as given, NOT trimmed, because callers use
// the raw value as a path segment: " github " would otherwise pass validation
// and then create a directory whose name has spaces in it. Whitespace is only
// stripped to decide whether the caller supplied anything at all, so a blank
// string still gets the "required" message rather than a pattern complaint.
func ValidateSkillIdentifier(identifier string) error {
	if strings.TrimSpace(identifier) == "" {
		return fmt.Errorf("identifier is required and must be a non-empty string")
	}
	if len(identifier) > MaxSkillIdentifierLength {
		return fmt.Errorf("identifier exceeds %d characters", MaxSkillIdentifierLength)
	}
	if !skillIdentifierRE.MatchString(identifier) {
		return fmt.Errorf(
			"identifier %q must be alphanumeric with single hyphens (e.g. \"docker-compose\") — "+
				"path separators, \".\", \"..\" and whitespace are refused to prevent directory traversal",
			identifier,
		)
	}
	return nil
}
