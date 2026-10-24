package task

import (
	"strings"
	"unicode"
)

// NormalizeTodoActiveForm removes directional controls and surrounding whitespace.
// Format-only copy is absent; joiners in otherwise visible text are preserved.
func NormalizeTodoActiveForm(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
			r == 0x200e, r == 0x200f:
			return -1
		default:
			return r
		}
	}, value)
	value = strings.TrimSpace(value)
	for _, r := range value {
		if !unicode.IsSpace(r) && !unicode.In(r, unicode.Cf) {
			return value
		}
	}
	return ""
}
