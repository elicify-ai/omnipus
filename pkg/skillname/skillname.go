// Package skillname is the one source of the skill name grammar (the Agent
// Skills name rule): letters and digits joined by single hyphens, at most
// MaxLength characters. It has no dependencies so every package that must
// recognise a skill name — pkg/skills, pkg/fspolicy's skills-shelf rules and
// kernel exemption, pkg/utils' identifier check — reads the same rule and
// cannot drift (skills code CHECK 1, CK14).
package skillname

import "regexp"

// Pattern is the skill name expression. It admits exactly one well-formed
// path segment: no separators, no dot entries, no leading, trailing or
// repeated hyphens, no whitespace.
const Pattern = `^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`

// MaxLength is the longest valid skill name, in bytes.
const MaxLength = 64

var patternRE = regexp.MustCompile(Pattern)

// Valid reports whether s is a valid skill name.
func Valid(s string) bool {
	return s != "" && len(s) <= MaxLength && patternRE.MatchString(s)
}
