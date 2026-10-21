package agent

import (
	"regexp"
	"strings"
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
		if end == len(text) || text[end] == ' ' || strings.ContainsRune("。！？", r) {
			return text[:end]
		}
	}
	return text
}
