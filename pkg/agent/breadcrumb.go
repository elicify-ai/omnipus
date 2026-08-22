// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// breadcrumbTokenCap is the maximum token budget for the entire breadcrumb
// block injected into the system message (FR-007).
const breadcrumbTokenCap = 1000

// breadcrumbCharsPerToken is the chars-per-token divisor used by
// estimateBreadcrumbTokens. We use 4 (i.e. ~250 tokens/kB), a deliberately
// coarser estimate than context_budget.go's 2.5 chars/token heuristic, so the
// ~1000-token cap remains honest for ASCII breadcrumb prose (pure prose has
// longer average token length than mixed code+prose).
const breadcrumbCharsPerToken = 4

// breadcrumbNowFn is the clock used by buildBreadcrumb for relative timestamps.
// Tests replace this to get deterministic output; production uses time.Now.
var breadcrumbNowFn = time.Now

// buildBreadcrumb constructs the prominent evicted-context breadcrumb block
// (FR-007, US-3). It is entirely LLM-free.
//
// archive is the full archived log (including evicted lines at indices < Skip),
// as returned by JSONLStore.ReadArchive. window is the current post-Skip live
// slice returned by GetHistory. The evicted turns are the prefix of archive not
// present in window.
//
// Returns "" when nothing has been evicted (len(archive) <= len(window)).
// Returns a populated markdown block otherwise, capped at tokenCap tokens,
// listing evicted turn-ranges most-recent-first with a relative timestamp,
// a ≤80-char snippet, and cheap entity extraction, and naming
// recall_conversation as the retrieval tool.
func buildBreadcrumb(archive []memory.ArchivedMessage, window []providers.Message, tokenCap int) string {
	if len(archive) == 0 {
		return ""
	}

	// Determine the split point: evicted = archive[:evictedCount].
	// The live window starts at archive[evictedCount:].
	// Guard against len mismatch (e.g. window has more messages than archive
	// because something was added mid-flight): if archive is not longer than
	// window, nothing has been evicted.
	evictedCount := len(archive) - len(window)
	if evictedCount <= 0 {
		return ""
	}
	evicted := archive[:evictedCount]

	// Extract plain providers.Message from evicted for parseTurnBoundaries.
	evictedMsgs := make([]providers.Message, len(evicted))
	for i, am := range evicted {
		evictedMsgs[i] = am.Message
	}

	// Group evicted messages into Turns via parseTurnBoundaries.
	turnStarts := parseTurnBoundaries(evictedMsgs)
	if len(turnStarts) == 0 {
		// No user messages in the evicted prefix — nothing meaningful to show.
		return ""
	}

	// Build per-turn ranges. A Turn is a user-message group (user → assistant
	// → tool → …). Each chunk represents exactly one Turn. turnOrdinal is the
	// 1-based Turn ordinal over the FULL archive, matching the ordinal that
	// recall_conversation(turn_range:"N-M") uses — so the model can copy a
	// range directly from the breadcrumb into the tool call.
	//
	// Because the archive starts at line 0 and the evicted prefix contains the
	// oldest Turns (Turns 1…K), chunk[i] is always Turn i+1.
	type turnChunk struct {
		turnOrdinal int // 1-based Turn ordinal in the full archive
		ts          int64
		snippet     string
		entities    string
	}

	chunks := make([]turnChunk, len(turnStarts))
	for i, start := range turnStarts {
		end := evictedCount - 1
		if i+1 < len(turnStarts) {
			end = turnStarts[i+1] - 1
		}
		// Timestamps: use the TS of the first message in the chunk (FR-007).
		ts := evicted[start].TS
		// Snippet: first role:"user" line in the chunk, verbatim, ≤80 chars.
		// Entities are extracted from the FULL content so truncation of the
		// display snippet does not clip multi-word runs like "John Doe".
		snippet := ""
		fullUserContent := ""
		for j := start; j <= end; j++ {
			if evicted[j].Role == "user" && evicted[j].Content != "" {
				fullUserContent = evicted[j].Content
				snippet = truncateSnippet(fullUserContent, 80)
				break
			}
		}
		// Entities from the full user content (not the truncated snippet).
		ents := extractEntities(fullUserContent)

		chunks[i] = turnChunk{
			turnOrdinal: i + 1, // 1-based: chunk 0 → Turn 1, chunk 1 → Turn 2, …
			ts:          ts,
			snippet:     snippet,
			entities:    ents,
		}
	}

	// Render most-recent-first (reverse order).
	header := "## Earlier in this conversation (evicted from the live window)\nUse the recall_conversation tool to read any of these verbatim."

	// Estimate token budget consumed by the header.
	budgetUsed := estimateBreadcrumbTokens(header)

	var lines []string
	truncatedAt := -1
	for i := len(chunks) - 1; i >= 0; i-- {
		c := chunks[i]
		// Use the 1-based Turn ordinal so the model can copy it directly into
		// recall_conversation(turn_range:"N-M"). Each chunk is exactly one Turn,
		// so turnOrdinal is both the start and end of the range.
		turnOrdinal := c.turnOrdinal

		var relTime string
		if c.ts == 0 {
			relTime = "earlier"
		} else {
			relTime = formatRelTime(time.Unix(c.ts, 0), breadcrumbNowFn())
		}

		entPart := ""
		if c.entities != "" {
			entPart = " · " + c.entities
		}

		line := fmt.Sprintf("- turn %d · %s · %q%s", turnOrdinal, relTime, c.snippet, entPart)

		lineTokens := estimateBreadcrumbTokens(line)
		if budgetUsed+lineTokens > tokenCap {
			// This line would exceed the cap. Record how many we dropped.
			truncatedAt = i
			break
		}
		budgetUsed += lineTokens
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		// Even the header + one entry doesn't fit; emit header + truncation note
		// so the model still knows something was evicted and can recall.
		return header + "\n+many earlier ranges"
	}

	// Build final block.
	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteByte('\n')
	// lines are most-recent-first; we built them in reverse-of-reverse so they
	// are already in most-recent-first order.
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}

	if truncatedAt >= 0 {
		// truncatedAt is the highest-index chunk we did not fit.
		// Number of ranges skipped = truncatedAt + 1.
		fmt.Fprintf(&sb, "+%d earlier ranges", truncatedAt+1)
		sb.WriteByte('\n')
	}

	return strings.TrimRight(sb.String(), "\n")
}

// truncateSnippet returns the first line of s (up to newline) truncated to
// maxChars runes. Ellipsis is appended only when the text was actually cut.
func truncateSnippet(s string, maxChars int) string {
	// Take only the first line.
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars-1]) + "…"
}

// formatRelTime returns a human-readable relative time string such as "2h ago",
// "5m ago", "3d ago", or "just now". Both t and now are in local time.
func formatRelTime(t, now time.Time) string {
	diff := now.Sub(t)
	if diff < 0 {
		diff = -diff
	}
	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(diff.Hours()/24))
	}
}

// extractEntities extracts cheap heuristic entities from text (FR-007/A-4):
//   - Quoted strings (text between " or ").
//   - File paths (tokens containing '/' or a '.ext' pattern).
//   - Multi-word runs of ≥2 consecutive Capitalized tokens that are NOT
//     sentence-initial (MIN-02): a Capitalized word is excluded if the
//     immediately preceding character in the original string is '.', '!', '?',
//     or is the very first word of the snippet.
//
// Returns a comma-joined string of unique entities in order of first appearance.
func extractEntities(text string) string {
	if text == "" {
		return ""
	}

	seen := map[string]struct{}{}
	var result []string

	addEntity := func(e string) {
		e = strings.TrimSpace(e)
		if e == "" {
			return
		}
		if _, ok := seen[e]; ok {
			return
		}
		seen[e] = struct{}{}
		result = append(result, e)
	}

	// 1. Quoted strings: find all "…" and "…" pairs.
	// We scan for both ASCII " and Unicode " " pairs.
	remaining := text
	for {
		start := strings.IndexAny(remaining, "\"“")
		if start < 0 {
			break
		}
		closeQuote := '"'
		if rune(remaining[start]) == '“' {
			closeQuote = '”'
		}
		end := strings.IndexRune(remaining[start+1:], closeQuote)
		if end < 0 {
			break
		}
		quoted := remaining[start+1 : start+1+end]
		quoted = strings.TrimSpace(quoted)
		if quoted != "" && !strings.ContainsAny(quoted, "\n") {
			addEntity("\"" + quoted + "\"")
		}
		remaining = remaining[start+1+end+1:]
	}

	// 2. Tokenize the text by whitespace for path and capitalized-run detection.
	words := strings.Fields(text)

	// 2a. File paths: tokens containing '/' or matching *.ext pattern.
	for _, w := range words {
		// Strip punctuation wrappers for matching purposes.
		clean := strings.TrimFunc(w, func(r rune) bool {
			return r == ',' || r == '.' || r == ';' || r == ':' || r == '"' || r == '\'' || r == ')' || r == '('
		})
		if isFilePath(clean) {
			addEntity(clean)
		}
	}

	// 2b. Multi-word runs of ≥2 consecutive Capitalized tokens that are not
	// sentence-initial.
	// "Capitalized" means the first rune is Unicode upper-case.
	// "Sentence-initial" means the immediately preceding token ends with '.', '!', or
	// '?', OR the token is the first word in the text.
	// Single Capitalized words are excluded (MIN-02).
	type capWord struct {
		word            string
		sentenceInitial bool
	}
	taggedWords := make([]capWord, len(words))
	for i, w := range words {
		sentInit := false
		if i == 0 {
			sentInit = true
		} else {
			prev := words[i-1]
			if len(prev) > 0 {
				lastRune := rune(prev[len(prev)-1])
				if lastRune == '.' || lastRune == '!' || lastRune == '?' {
					sentInit = true
				}
			}
		}
		cleanW := strings.TrimFunc(w, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		taggedWords[i] = capWord{word: cleanW, sentenceInitial: sentInit}
	}

	i := 0
	for i < len(taggedWords) {
		// Check if this word is a non-sentence-initial Capitalized word.
		tw := taggedWords[i]
		r := firstRune(tw.word)
		if r == 0 || !unicode.IsUpper(r) || tw.sentenceInitial {
			i++
			continue
		}
		// Start of a potential run. Collect as many consecutive Capitalized,
		// non-sentence-initial words as possible.
		run := []string{tw.word}
		j := i + 1
		for j < len(taggedWords) {
			next := taggedWords[j]
			nr := firstRune(next.word)
			if nr != 0 && unicode.IsUpper(nr) && !next.sentenceInitial {
				run = append(run, next.word)
				j++
			} else {
				break
			}
		}
		if len(run) >= 2 {
			addEntity(strings.Join(run, " "))
		}
		i = j
	}

	return strings.Join(result, ", ")
}

// firstRune returns the first rune of s, or 0 on empty/invalid input.
func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// isFilePath returns true when token looks like a file path: contains '/' or
// has a dot-extension pattern (at least one '.' with a short suffix of 1–6 chars).
func isFilePath(token string) bool {
	if token == "" {
		return false
	}
	if strings.Contains(token, "/") {
		return true
	}
	// dot-extension: something like "file.go", "image.png", ".env"
	dot := strings.LastIndex(token, ".")
	if dot >= 0 && dot < len(token)-1 {
		ext := token[dot+1:]
		// Extension must be short (1–6 alpha/num chars) and the token must have
		// some non-dot content before it.
		if len(ext) >= 1 && len(ext) <= 6 && isAlphaNum(ext) && dot > 0 {
			return true
		}
	}
	return false
}

// isAlphaNum returns true when all runes in s are letters or digits.
func isAlphaNum(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return len(s) > 0
}

// estimateBreadcrumbTokens estimates the token cost of s for the breadcrumb cap
// (FR-007). Uses breadcrumbCharsPerToken (4 chars/token), which is coarser than
// context_budget.go's 2.5 chars/token because breadcrumb prose is mostly ASCII
// with long words — the coarser divisor keeps the ~1000-token cap honest.
func estimateBreadcrumbTokens(s string) int {
	n := len(s) / breadcrumbCharsPerToken
	if n < 1 && len(s) > 0 {
		return 1
	}
	return n
}
