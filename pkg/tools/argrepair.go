// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"regexp"
	"strings"
)

// argrepair.go — recovering tool-call arguments corrupted by a model that
// LEAKS its own tool-call template control tokens verbatim into the JSON
// string value of an argument instead of using them as delimiters.
//
// # The failure this repairs (A1)
//
// Observed against z-ai/glm-5.3-flash (via OpenRouter). The model emits a
// perfectly well-formed tool-call arguments JSON — most fields decode
// correctly (arrays, nested objects) — but for a run of scalar fields it
// serialises its INTERNAL tool-call template into the string value of the
// FIRST field of the run and drops the field boundaries. A real captured
// knowledge_configure call decoded to:
//
//	{
//	  "op":   "create_view</arg_value><5b656597><arg_key><2b53f23f>type</arg_key><ac7a3bd7><arg_value><b88a6f17>note",
//	  "view": "jim-tooltest--recent",
//	  "kind": "table", "columns": [...], "filter": {...}
//	}
//
// The intended call was op="create_view", type="note". The template leaked
// two token families into op's value: the structural tags <arg_key> /
// </arg_key> / <arg_value> / </arg_value>, and per-call random 8-hex
// SENTINEL tokens (<5b656597>, <2b53f23f>, ...). Because the JSON itself was
// valid, every downstream layer accepted op verbatim, and the tool-argument
// enum validator then correctly rejected the garbage — "property \"op\":
// value create_view</arg_value>... is not in enum" — a true error with an
// unusable message, and a valid call (create_view) lost.
//
// # Why repair rather than reject
//
// The corruption is deterministic and losslessly reversible: the true value
// is the text BEFORE the first control token, and any trailing
// <arg_key>K</arg_key>...<arg_value>V run re-encodes the fields the model
// dropped. Recovering them turns a hard failure into the call the model
// meant, for every op and every tool — not just create_view. A recovered
// field is only filled in when the argument map does not already carry it,
// so a correctly-emitted field is never clobbered.
//
// # Scope of the trigger (kept deliberately narrow — A1 hardening)
//
// This repair runs registry-wide, before validation, for EVERY tool. Its
// trigger must therefore be a fingerprint of the actual leak, not merely of
// text that resembles one — otherwise a legitimate argument that only
// MENTIONS the tag (a bash command echoing "</arg_value>", note/body text
// discussing the grammar) would be silently truncated and then executed,
// which is worse than the enum error the repair exists to fix.
//
// Repair fires ONLY when a string value contains BOTH:
//
//	1. a literal structural tag (<arg_key>/<arg_value>, open or close); AND
//	2. at least one per-call 8-hex sentinel token (e.g. <5b656597>).
//
// The sentinels are the model's random per-call separators — they are the
// distinctive fingerprint of a genuine template leak and are effectively
// never present in real tool-argument text. Requiring them alongside the
// structural tags means:
//   - the structural tag ALONE no longer triggers repair, so a legitimate
//     value that merely mentions "</arg_value>" is left untouched; and
//   - a hex-looking token ALONE ("<deadbeef>") still never triggers repair.
//
// A false positive now requires a caller to legitimately send a structural
// tag AND a random 8-hex token in the same string value, which no real tool
// argument does. Every captured leak carries the sentinels (see the header
// example); the template that produced them always emits them.

var (
	// leakedArgTag matches the STRUCTURAL template tags.
	leakedArgTag = regexp.MustCompile(`</?arg_(?:key|value)>`)

	// leakedSentinel matches a per-call 8-hex sentinel token. Its presence
	// alongside a structural tag is what confirms a genuine template leak.
	leakedSentinel = regexp.MustCompile(`<[0-9a-fA-F]{8}>`)

	// leakedAnyToken matches every control token — the structural tags AND
	// the per-call 8-hex sentinels — used to tokenise a value once it has
	// been identified as leaked.
	leakedAnyToken = regexp.MustCompile(`</?arg_(?:key|value)>|<[0-9a-fA-F]{8}>`)
)

// isLeakedValue reports whether s carries the fingerprint of a genuine
// tool-call template leak: a structural tag AND at least one hex sentinel.
// Both are required so that legitimate content mentioning only one of them is
// never mistaken for a leak (A1).
func isLeakedValue(s string) bool {
	return leakedArgTag.MatchString(s) && leakedSentinel.MatchString(s)
}

// repairLeakedToolArgs recovers values corrupted by leaked tool-call template
// tokens (see the file header). It returns the arguments to use downstream and
// true when it changed anything, so the caller can log that a repair fired.
//
// Safety is ENFORCED, not conventional: the caller's map is never mutated. On
// the common no-op path (no leak in any value) the very same map is returned
// with no copy and no allocation. Only when a repair actually fires is the map
// defensively cloned and the recovery written to the clone — so a caller that
// (now or in future) shares its map with transcript/session state can never
// have that state truncated in place on a rare leak match.
func repairLeakedToolArgs(args map[string]any) (map[string]any, bool) {
	if len(args) == 0 {
		return args, false
	}

	// out and recovered stay nil until the first leak is seen, keeping the
	// no-op path allocation-free.
	var out map[string]any
	var recovered map[string]string

	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if !isLeakedValue(s) {
			continue
		}
		clean, pairs := splitLeakedValue(s)
		if out == nil {
			out = cloneArgsMap(args)
			recovered = map[string]string{}
		}
		out[k] = clean
		for pk, pv := range pairs {
			// A pair recovered from an EARLIER key wins over a later one only
			// by map iteration order, which is nondeterministic — but the
			// leaked grammar never re-encodes the same key twice in practice,
			// and an already-present real argument (checked below) always
			// wins regardless, so this is safe.
			recovered[pk] = pv
		}
	}

	if out == nil {
		// No leak fired: caller's map is returned untouched.
		return args, false
	}

	// Fill recovered fields only where the model did not also send a real one.
	// A correctly-emitted argument is authoritative over anything unpacked
	// from a leaked blob.
	for pk, pv := range recovered {
		if pk == "" {
			continue
		}
		if _, exists := out[pk]; !exists {
			out[pk] = pv
		}
	}

	return out, true
}

// cloneArgsMap returns a shallow copy of src. A shallow copy suffices for the
// repair: it only ever reassigns top-level string entries and adds top-level
// keys, never mutating a nested slice/map value, so the caller's original map
// (and any structure it shares) is left fully intact.
func cloneArgsMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// splitLeakedValue separates a leaked string into its true leading value and
// any key/value pairs the leak re-encoded in a trailing
// <arg_key>KEY</arg_key> ... <arg_value>VALUE run.
//
// The true value is the text before the first control token. The remainder
// is walked as a small state machine over (token, following-text) segments:
// <arg_key> opens a KEY capture, <arg_value> opens a VALUE capture, the
// closing tags end them, and hex sentinels are skipped without changing the
// mode (a real captured key/value text can follow a sentinel, as in
// "<arg_key><2b53f23f>type").
func splitLeakedValue(s string) (clean string, pairs map[string]string) {
	pairs = map[string]string{}

	locs := leakedAnyToken.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return strings.TrimSpace(s), pairs
	}

	clean = strings.TrimSpace(s[:locs[0][0]])

	var mode string // "" | "key" | "value"
	var pendingKey string

	for i, loc := range locs {
		tok := s[loc[0]:loc[1]]

		// Text between this token and the next (or end of string).
		textEnd := len(s)
		if i+1 < len(locs) {
			textEnd = locs[i+1][0]
		}
		text := strings.TrimSpace(s[loc[1]:textEnd])

		switch tok {
		case "<arg_key>":
			mode = "key"
			pendingKey = ""
		case "</arg_key>":
			mode = ""
		case "<arg_value>":
			mode = "value"
		case "</arg_value>":
			mode = ""
			// default: a hex sentinel — skip the token, keep the current mode
			// so the text that follows it is still captured.
		}

		if text == "" {
			continue
		}
		switch mode {
		case "key":
			pendingKey = text
		case "value":
			if pendingKey != "" {
				pairs[pendingKey] = text
			}
		}
	}

	return clean, pairs
}
