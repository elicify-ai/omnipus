// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FilteredMarker replaces every masked run of token bytes.
const FilteredMarker = "[FILTERED]"

// MaxAccountLabelLen is the account label bound (FR-046): 1–128 printable
// characters.
const MaxAccountLabelLen = 128

// ErrNoAccountLabel is returned when the check output has no usable first
// non-empty line (empty output, too long or not printable).
var ErrNoAccountLabel = errors.New("the account check printed no account name")

// LabelCarriesTokenError is the FR-046 refusal: the masked first line holds
// [FILTERED] or an 8-or-more-byte fragment of a token. Its message is the
// spec's and never quotes the token.
type LabelCarriesTokenError struct{ Program string }

func (e *LabelCarriesTokenError) Error() string {
	return "The account check printed the token instead of an account name. Fix the account check for " + e.Program + "."
}

// AccountLabel derives the account label from the MASKED check output
// (FR-046): the first non-empty stdout line, trimmed, of 1–128 printable
// characters. A line containing [FILTERED] or an 8-or-more-byte fragment of
// any current or retired token is refused with LabelCarriesTokenError and no
// label.
func AccountLabel(program, maskedOutput string, tokens []string) (string, error) {
	line := ""
	for _, l := range strings.Split(maskedOutput, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			line = t
			break
		}
	}
	if line == "" {
		return "", ErrNoAccountLabel
	}
	if strings.Contains(line, FilteredMarker) || containsTokenFragment(line, tokens) {
		return "", &LabelCarriesTokenError{Program: program}
	}
	if utf8.RuneCountInString(line) > MaxAccountLabelLen || !utf8.ValidString(line) {
		return "", ErrNoAccountLabel
	}
	for _, r := range line {
		if !unicode.IsPrint(r) {
			return "", ErrNoAccountLabel
		}
	}
	return line, nil
}

// containsTokenFragment reports whether s holds any substring of 8 or more
// bytes of any token (every such substring contains an 8-byte window).
func containsTokenFragment(s string, tokens []string) bool {
	grams := tokenGrams(tokens)
	for i := 0; i+gramLen <= len(s); i++ {
		if _, ok := grams[s[i:i+gramLen]]; ok {
			return true
		}
	}
	return false
}
