// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"strings"
	"unicode/utf8"
)

// FoldedName returns the entry of names that word spells when letter case is
// ignored, or "" when it spells none of them. It is the one program-name
// matcher shared by this package's interpreter classification and by
// pkg/tools' shell-name recognition. Case is compared under the same simple
// Unicode case folding as strings.EqualFold, not ASCII only: a
// case-insensitive filesystem, such as the macOS default, runs the same
// program for spellings that differ in letter case, including non-ASCII case
// folds of an ASCII letter.
//
// Recognising a name this way only ever widens a check (an interpreter is
// inspected, a shell's script is scanned). It never makes a spelling satisfy
// an allow rule: allow matching stays on the resolved path and the
// normalised-head restriction (matchRules, classifySegment).
func FoldedName(word string, names []string) string {
	for _, name := range names {
		if strings.EqualFold(word, name) {
			return name
		}
	}
	return ""
}

// hasFoldedPrefix reports whether word starts with prefix under FoldedName's
// case folding. Simple folding maps one rune to one rune, so the comparison
// takes exactly as many runes from word as prefix has.
func hasFoldedPrefix(word, prefix string) bool {
	n := utf8.RuneCountInString(prefix)
	end := 0
	for n > 0 {
		if end >= len(word) {
			return false
		}
		_, size := utf8.DecodeRuneInString(word[end:])
		end += size
		n--
	}
	return FoldedName(word[:end], []string{prefix}) != ""
}
