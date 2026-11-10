package agent

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A cue must start a sentence or line, not be an incidental "use for" phrase.
var delegationUseForCue = regexp.MustCompile(`(?im)(?:^|[.!?]["'”’)]*\s+|\n\s*)(?:[-*]\s+)?use\s+(?:for|when)\b\s*:?\s*`)

// delegationUseFor selects existing text only; it never summarizes a role.
func delegationUseFor(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "(no description)"
	}
	if cue := delegationUseForCue.FindStringIndex(description); cue != nil {
		if excerpt := strings.TrimSpace(description[cue[1]:]); excerpt != "" {
			description = excerpt
		}
	}
	return firstDelegationSentence(description)
}

// firstDelegationSentence normalizes wrapping to one line and retains closing
// quotes after sentence punctuation, without splitting dotted words or versions.
func firstDelegationSentence(description string) string {
	text := strings.Join(strings.Fields(description), " ")
	for i, r := range text {
		if !strings.ContainsRune(".!?。！？", r) {
			continue
		}
		end := i + utf8.RuneLen(r)
		for end < len(text) {
			closing, size := utf8.DecodeRuneInString(text[end:])
			if !strings.ContainsRune(`"'”’)]}`, closing) {
				break
			}
			end += size
		}
		if end == len(text) || strings.ContainsRune("。！？", r) {
			return text[:end]
		}
		if text[end] == ' ' {
			if r == '.' && delegationAbbreviationContinues(text, i, end) {
				continue
			}
			return text[:end]
		}
	}
	return text
}

// delegationAbbreviationContinues handles bounded English abbreviation classes.
// Titles and explanatory abbreviations introduce more text; suffixes and dotted
// initials continue before a lowercase word/number, but stop before a new
// capitalized sentence. This is not a general natural-language segmenter.
func delegationAbbreviationContinues(text string, dot, boundary int) bool {
	start := dot
	for start > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:start])
		if r != '.' && !unicode.IsLetter(r) {
			break
		}
		start -= size
	}
	token := strings.ToLower(text[start : dot+1])
	switch token {
	case "dr.", "mr.", "mrs.", "ms.", "prof.", "e.g.", "i.e.":
		return true
	case "etc.", "inc.":
		// These can occur at either an internal or a real sentence boundary.
	default:
		if !isDelegationInitialism(token) {
			return false
		}
	}

	for boundary < len(text) {
		r, size := utf8.DecodeRuneInString(text[boundary:])
		if unicode.IsSpace(r) || strings.ContainsRune(`"'“‘([{`, r) {
			boundary += size
			continue
		}
		return unicode.IsLower(r) || unicode.IsDigit(r)
	}
	return false
}

func isDelegationInitialism(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) < 3 || parts[len(parts)-1] != "" {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		if utf8.RuneCountInString(part) != 1 {
			return false
		}
		r, _ := utf8.DecodeRuneInString(part)
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
