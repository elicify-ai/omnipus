// Package security — prompt injection defenses (SEC-25).
//
// PromptGuard tags, sanitizes, or summarizes untrusted content before it
// reaches the LLM agent loop.  Three strictness levels:
//
//   - Low    — wraps untrusted content in [UNTRUSTED_CONTENT] delimiters.
//   - Medium — additionally escapes known injection patterns; default.
//   - High   — replaces untrusted content with a static placeholder; the
//     caller is responsible for summarizing via a separate LLM
//     call before passing to the main agent.
package security

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// Strictness controls how aggressively PromptGuard sanitizes content.
type Strictness string

const (
	// StrictnessLow tags untrusted content but does not modify it.
	StrictnessLow Strictness = "low"
	// StrictnessMedium (default) tags and escapes known injection phrases.
	StrictnessMedium Strictness = "medium"
	// StrictnessHigh replaces untrusted content with a summarization placeholder.
	StrictnessHigh Strictness = "high"
)

const (
	untrustedOpen  = "[UNTRUSTED_CONTENT]"
	untrustedClose = "[/UNTRUSTED_CONTENT]"
	// highPlaceholder is what StrictnessHigh emits for untrusted content.
	// The surrounding application must replace this with an LLM summary.
	highPlaceholder = "[UNTRUSTED_CONTENT_REDACTED_FOR_SUMMARIZATION]"
)

// injectionPhrases contains known prompt-injection trigger patterns.
// These are escaped at Medium. At High, all untrusted content is replaced regardless of individual phrases.
var injectionPhrases = []string{
	"ignore previous instructions",
	"ignore all previous",
	"disregard previous",
	"forget previous instructions",
	"disregard all instructions",
	"override previous",
	"you are now",
	"new instructions:",
	"system prompt:",
	"[system]",
	"</s>",
	"<|im_start|>",
	"<|im_end|>",
}

// PromptGuard sanitizes untrusted content before it enters the agent loop.
type PromptGuard struct {
	strictness Strictness
}

// NewPromptGuard creates a PromptGuard with the given strictness.
// An empty or unrecognized value defaults to Medium.
func NewPromptGuard(s Strictness) *PromptGuard {
	switch s {
	case StrictnessLow, StrictnessMedium, StrictnessHigh:
	default:
		s = StrictnessMedium
	}
	return &PromptGuard{strictness: s}
}

// Sanitize applies the guard to content and returns the processed string.
//
//   - If trusted is true the content is returned unchanged (verified skill output).
//   - If trusted is false, the guard applies its strictness level.
//
// The returned string always preserves the untrusted tag when strictness is
// Low or Medium so downstream code can identify the trust boundary.
func (g *PromptGuard) Sanitize(content string, trusted bool) string {
	if trusted {
		return content
	}

	switch g.strictness {
	case StrictnessLow:
		return g.wrapUntrusted(content)
	case StrictnessHigh:
		return highPlaceholder
	default: // Medium
		return g.wrapUntrusted(g.escapeInjectionPhrases(content))
	}
}

// wrapUntrusted surrounds content with untrusted-content delimiters.
func (g *PromptGuard) wrapUntrusted(content string) string {
	return fmt.Sprintf("%s\n%s\n%s", untrustedOpen, content, untrustedClose)
}

// escapeInjectionPhrases neutralizes known injection trigger phrases by
// inserting a zero-width non-joiner (U+200C) after the first rune of each
// match so the phrase no longer matches downstream pattern checks.
//
// strings.Index returns a byte offset; we convert to a rune offset with
// utf8.RuneCountInString before indexing into the []rune slice — otherwise
// multi-byte UTF-8 characters (emoji, CJK, etc.) produce wrong offsets and
// can cause index-out-of-range panics or missed replacements.
func (g *PromptGuard) escapeInjectionPhrases(content string) string {
	result := []rune(content)

	for _, phrase := range injectionPhrases {
		lower := strings.ToLower(string(result))
		byteIdx := strings.Index(lower, phrase)
		for byteIdx >= 0 {
			// Convert byte offset → rune offset.
			runeIdx := utf8.RuneCountInString(lower[:byteIdx])

			// Insert zero-width non-joiner after the first rune of the phrase.
			// CodeQL go/allocation-size-overflow flags the len(result)+1 sum
			// even though prompt-guard input is hard-capped upstream and the
			// sum cannot realistically approach math.MaxInt. The explicit
			// bound below makes the safety obvious to the analyser and adds
			// a defensive ceiling for runaway inputs.
			const maxPromptGuardRunes = 1 << 24
			capHint := len(result) + 1
			if capHint < 0 || capHint > maxPromptGuardRunes {
				capHint = maxPromptGuardRunes
			}
			replacement := make([]rune, 0, capHint)
			replacement = append(replacement, result[:runeIdx+1]...)
			replacement = append(replacement, '\u200C')
			replacement = append(replacement, result[runeIdx+1:]...)
			result = replacement

			// Re-compute lowercase string and search for next occurrence,
			// starting after the inserted non-joiner (+1 for the ZWNJ rune).
			lower = strings.ToLower(string(result))
			phraseRunes := utf8.RuneCountInString(phrase)
			searchFrom := runeIdx + phraseRunes + 1 // +1 for the inserted ZWNJ
			if searchFrom >= len(result) {
				break
			}
			// Convert rune search position back to a byte position for Index.
			byteSearchFrom := len(string(result[:searchFrom]))
			next := strings.Index(lower[byteSearchFrom:], phrase)
			if next < 0 {
				break
			}
			byteIdx = byteSearchFrom + next
		}
	}
	return string(result)
}

// Strictness returns the configured strictness level.
func (g *PromptGuard) Strictness() Strictness {
	return g.strictness
}

// NewPromptGuardFromConfig creates a PromptGuard from a policy.PromptGuardConfig.
// Defaults to Medium strictness when the config field is empty (W-3).
func NewPromptGuardFromConfig(cfg policy.PromptGuardConfig) *PromptGuard {
	var s Strictness
	switch cfg.Strictness {
	case "low":
		s = StrictnessLow
	case "high":
		s = StrictnessHigh
	default:
		s = StrictnessMedium
	}
	return NewPromptGuard(s)
}
