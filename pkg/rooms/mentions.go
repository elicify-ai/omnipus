// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Agent is a workspace conversation agent as the rooms see it.
type Agent struct {
	ID   string
	Name string
}

// mentionAll is the reserved @all value (picker and typed text).
const mentionAll = "all"

// typedMentions is the outcome of §6.13's typed-text resolution.
type typedMentions struct {
	ids []string
	all bool
	// ambiguous names a typed @Name that matched several members.
	ambiguous string
}

// resolveTyped applies §6.13 to typed text: at every "@" the longest exact,
// case-insensitive match of a member's FULL current name followed by a word
// boundary wins; a longest match shared by several members is ambiguous; a
// name matching nobody stays unresolved text. "@all" is reserved: a member
// named "all" is reachable only through the picker.
func resolveTyped(text string, members []Agent) typedMentions {
	var out typedMentions
	seen := map[string]bool{}
	lower := strings.ToLower(text)
	for i := 0; i < len(lower); i++ {
		if lower[i] != '@' || (i > 0 && isWordByte(lower, i-1)) {
			continue
		}
		rest := lower[i+1:]
		bestLen, bestIDs, bestName := 0, []string(nil), ""
		for _, m := range members {
			name := strings.ToLower(strings.TrimSpace(m.Name))
			if name == "" || name == mentionAll || !strings.HasPrefix(rest, name) || !boundaryAt(rest, len(name)) {
				continue
			}
			switch {
			case len(name) > bestLen:
				bestLen, bestIDs, bestName = len(name), []string{m.ID}, strings.TrimSpace(m.Name)
			case len(name) == bestLen:
				bestIDs = append(bestIDs, m.ID)
			}
		}
		if bestLen == 0 {
			if strings.HasPrefix(rest, mentionAll) && boundaryAt(rest, len(mentionAll)) {
				out.all = true
			}
			continue
		}
		if len(bestIDs) > 1 {
			if out.ambiguous == "" {
				out.ambiguous = bestName
			}
			continue
		}
		if !seen[bestIDs[0]] {
			seen[bestIDs[0]] = true
			out.ids = append(out.ids, bestIDs[0])
		}
		i += bestLen
	}
	return out
}

// boundaryAt reports whether s ends at n or continues with a non-word rune.
func boundaryAt(s string, n int) bool {
	if n >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[n:])
	return !isWordRune(r)
}

func isWordByte(s string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(s[:i+1])
	return isWordRune(r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
