// loop_toolname.go: Suggest the nearest known tool name for an unknown one

package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// minSuggestInputLen mirrors pkg/tools' minFuzzyInputLen: names shorter than
// this match too much by distance to be worth a guess.
const minSuggestInputLen = 3

// suggestUnknownToolName answers "did you mean …" for a tool name the caller
// asked for that no registered tool carries (D-96, UAT 2026-09-13). It returns
// the best candidate and, when the answer is an op-dispatched tool, the `op`
// value that does what the caller asked for; ("", "") when no candidate is
// close enough to name — a wrong hint costs the caller a round trip, so no
// signal means no suggestion.
//
// # Why raw edit distance was the defect
//
// The observed case: asked for `knowledge_create`, the old ranking answered
// `knowledge_read` — edit distance 4 beats knowledge_edit's 6 — the one
// knowledge tool that cannot create anything. The name a model hallucinates is
// family + VERB ("knowledge_" + what it wants done), and the verb, not the
// character count, is the intent. So the ranking, strongest signal first:
//
//  1. Token-set equality (score 1.0) — `task_update` and `update_task` are the
//     same words in a different order; distance sees two edits, the caller
//     meant one tool.
//  2. Op-enum match (0.90 + 0.02 per suffix segment) — the longest "_"-joined
//     SUFFIX of the unknown that equals a value in the candidate's `op`
//     parameter enum, with every remaining prefix segment present in the
//     candidate's own name segments (the family). `knowledge_create` matches
//     knowledge_edit's op "create"; `knowledge_create_view` matches
//     knowledge_configure's op "create_view". This is the signal that also
//     produces the op hint.
//  3. Verb synonym (0.88) — the unknown's trailing segment is a synonym of the
//     candidate's trailing segment under toolVerbSynonyms, with the same first
//     segment (family). `knowledge_search` → knowledge_find: no lexical overlap
//     at all, and the intent is exact.
//  4. Edit distance (1 - d/(len+1), gated at 60% of the longer name) — the
//     typo signal, `knowledge_reed` → knowledge_read. Kept as the LAST
//     resort because it is the one signal that ignores intent.
//
// Ties keep the first candidate in registration order. The minimum-input
// guard mirrors pkg/tools' own (very short names suggest everything).
func suggestUnknownToolName(allTools []tools.Tool, unknown string) (name, hint string) {
	if len(unknown) < minSuggestInputLen || len(allTools) == 0 {
		return "", ""
	}
	unknownLower := strings.ToLower(unknown)
	unknownSegs := strings.Split(unknownLower, "_")

	best := ""
	bestHint := ""
	bestScore := 0.0
	for _, t := range allTools {
		cand := strings.ToLower(t.Name())
		if cand == unknownLower {
			return t.Name(), "" // case-only misspelling; the answer is exact
		}
		score, opHint := suggestScore(unknown, unknownLower, unknownSegs, cand, t)
		if score > bestScore {
			bestScore, best, bestHint = score, t.Name(), opHint
		}
	}
	if bestScore <= 0 {
		return "", ""
	}
	return best, bestHint
}

// toolVerbSynonyms is the closed table of tool-verb equivalences the suggester
// recognises (signal 3). It is deliberately tiny and hand-curated: every entry
// is a pair a model plausibly substitutes when hallucinating a name, and every
// entry risks a wrong hint in the other direction. Read: the key verb, asked
// for, is done by the mapped verb, which appears in a registered tool's name.
var toolVerbSynonyms = map[string]map[string]bool{
	"search": {"find": true},
	"find":   {"search": true},
	"query":  {"find": true},
	"lookup": {"find": true},
	"get":    {"read": true},
	"load":   {"read": true},
	"fetch":  {"read": true},
}

// suggestScore scores one candidate against an unknown name. A score of 0
// means "do not suggest this candidate"; the hint is non-empty only for the
// op-enum signal, where the caller can be told not just the tool but the op
// inside it that does what they asked.
func suggestScore(unknown, unknownLower string, unknownSegs []string, candLower string, t tools.Tool) (score float64, hint string) {
	candSegs := strings.Split(candLower, "_")

	// 1 — same words, different order.
	if sameSegmentSet(unknownSegs, candSegs) {
		return 1.0, ""
	}

	// 2 — the unknown's longest suffix is an op this tool dispatches, and the
	// rest of the unknown names the tool's family. Enum values may themselves
	// contain underscores ("create_view"), so suffixes are tried longest-first
	// and compared joined.
	ops := toolOpEnum(t)
	if len(ops) > 0 && len(unknownSegs) > 1 {
		for take := len(unknownSegs) - 1; take >= 1; take-- {
			suffix := strings.Join(unknownSegs[take:], "_")
			if !ops[suffix] {
				continue
			}
			if segmentsCovered(unknownSegs[:take], candSegs) {
				return 0.90 + 0.02*float64(len(unknownSegs)-take), `op "` + suffix + `"`
			}
		}
	}

	// 3 — the trailing verb is a synonym and the family agrees.
	if len(unknownSegs) > 1 && len(candSegs) > 1 &&
		unknownSegs[0] == candSegs[0] &&
		toolVerbSynonyms[unknownSegs[len(unknownSegs)-1]][candSegs[len(candSegs)-1]] {
		return 0.88, ""
	}

	// 4 — edit distance, the typo signal, gated exactly as pkg/tools gates it
	// (up to 60% of the longer name as edits, plus one).
	d := levenshteinToolName(unknownLower, candLower)
	longer := len(unknownLower)
	if len(candLower) > longer {
		longer = len(candLower)
	}
	if d <= 6*longer/10+1 {
		if s := 1.0 - float64(d)/float64(longer+1); s > 0 {
			return s, ""
		}
	}
	return 0, ""
}

// toolOpEnum reads the string values of a tool's `op` parameter enum from its
// parameter schema, lowercased. A tool with no `op` enum answers nil; the
// caller treats that as "not op-dispatched".
func toolOpEnum(t tools.Tool) map[string]bool {
	params := t.Parameters()
	if params == nil {
		return nil
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		return nil
	}
	op, ok := props["op"].(map[string]any)
	if !ok {
		return nil
	}
	// The enum arrives as []any when the schema came through JSON and as
	// []string when a Go-built schema put it there directly; both are read —
	// the shape of the container is not part of the question.
	var values []string
	switch enum := op["enum"].(type) {
	case []any:
		for _, v := range enum {
			if s, ok := v.(string); ok {
				values = append(values, s)
			}
		}
	case []string:
		values = enum
	default:
		return nil
	}
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for _, s := range values {
		if s != "" {
			out[strings.ToLower(s)] = true
		}
	}
	return out
}

// segmentsCovered answers whether every segment in want appears in have.
func segmentsCovered(want, have []string) bool {
	set := make(map[string]bool, len(have))
	for _, s := range have {
		set[s] = true
	}
	for _, s := range want {
		if !set[s] {
			return false
		}
	}
	return true
}

// sameSegmentSet answers whether two "_"-split names carry the same multiset
// of segments (order-insensitive).
func sameSegmentSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}

// levenshteinToolName is the plain Wagner-Fischer distance pkg/tools' own
// suggester uses, restated here because that one is unexported and this
// ranking must not reach into another package's private table.
func levenshteinToolName(a, b string) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minInt(curr[j-1]+1, minInt(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
