// Omnipus — fuzzy tool-name matching for ToolSearch error hints (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"sort"
	"strings"
)

// minFuzzyInputLen is the minimum number of characters an unknown name must
// have before fuzzy matching is attempted. Very short inputs ("x", "se", "ex")
// produce too many false-positive suggestions — a 1–2 char string can match
// almost any tool name via Levenshtein on a 60% threshold.
const minFuzzyInputLen = 4

// FindClosestToolName returns the name of the tool in allTools whose name is
// closest to unknown, provided the similarity is above a liberal threshold.
//
// Two similarity signals are combined:
//  1. Levenshtein edit distance ≤ 60% of max(len(unknown), len(candidate)).
//  2. Word-token set equality after splitting on '_': e.g. "task_update" and
//     "update_task" share the same tokens {task, update} and score as a match
//     regardless of levenshtein distance.
//
// Comparison is case-insensitive: both unknown and the candidate name are
// lowercased before distance and token calculations.
//
// Returns "" when no candidate is close enough or unknown is shorter than
// minFuzzyInputLen characters.
func FindClosestToolName(allTools []Tool, unknown string) string {
	if len(unknown) == 0 || len(allTools) == 0 {
		return ""
	}
	// Minimum-input-length guard: very short inputs ("x", "se") produce too
	// many false-positive suggestions via the 60% Levenshtein threshold.
	if len(unknown) < minFuzzyInputLen {
		return ""
	}

	unknownLower := strings.ToLower(unknown)
	unknownTokens := sortedTokens(unknownLower)

	best := ""
	bestScore := -1.0 // higher is better

	for _, t := range allTools {
		name := t.Name()
		nameLower := strings.ToLower(name)
		if nameLower == unknownLower {
			return name // exact case-insensitive match
		}

		score := similarity(unknownLower, nameLower, unknownTokens)
		if score > bestScore {
			bestScore = score
			best = name
		}
	}

	// A score > 0 means at least one signal fired.
	if bestScore > 0 && best != "" {
		return best
	}
	return ""
}

// similarity returns a score in (0, 1] when the candidate is "close enough"
// to unknown (either by edit distance or by token-set equality), or 0 when
// the candidate should not be suggested.
func similarity(unknown, candidate string, unknownTokens []string) float64 {
	// Signal 1: word-token set equality (catches word-order transpositions).
	if sameTokenSet(unknownTokens, sortedTokens(candidate)) {
		return 1.0
	}

	// Signal 2: Levenshtein edit distance relative to the longer string.
	longer := len(unknown)
	if len(candidate) > longer {
		longer = len(candidate)
	}
	if longer == 0 {
		return 0
	}
	d := levenshtein(unknown, candidate)
	// Allow up to 60% of the longer string length as edits.
	threshold := int(float64(longer)*0.6) + 1
	if d <= threshold {
		// Score inversely proportional to edit distance.
		return 1.0 - float64(d)/float64(longer+1)
	}
	return 0
}

// sortedTokens splits name on '_' and returns a sorted slice of tokens.
func sortedTokens(name string) []string {
	parts := strings.Split(name, "_")
	sort.Strings(parts)
	return parts
}

// sameTokenSet returns true when two sorted token slices are identical.
func sameTokenSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// levenshtein computes the Levenshtein edit distance between a and b using the
// standard O(min(m,n)) space DP algorithm.
func levenshtein(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	// Ensure ra is the shorter string (saves memory).
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	m, n := len(ra), len(rb)
	if m == 0 {
		return n
	}
	if n == 0 {
		return m
	}

	// prev[j] = distance(ra[:0], rb[:j]) initially; updated to distance(ra[:i], rb[:j]).
	prev := make([]int, n+1)
	curr := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j
	}

	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = minInt(del, minInt(ins, sub))
		}
		prev, curr = curr, prev
	}
	return prev[n]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
