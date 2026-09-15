// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "strings"

// Orphan tool-call markup — what this file recognises and why.
//
// Several open-weight model families (GLM-4.x/5.x, Hermes, Qwen and their
// derivatives) do NOT emit tool calls as a structured `tool_calls` field.
// They emit an XML-ish dialect in the completion TEXT, which the hosting
// provider is then expected to parse back into `tool_calls` before it
// reaches an OpenAI-compatible client:
//
//	<tool_call>write_file
//	<arg_key>content</arg_key><arg_value>one
//	two</arg_value><arg_key>path</arg_key><arg_value>count.txt</arg_value>
//	</tool_call>
//
// When that upstream parse does not complete — observed live on
// openrouter → z-ai/glm-5.3, most reliably when the generation hits the
// output-token cap mid-call (finish_reason "length") — the unconsumed
// REMAINDER of the markup is flushed into `choices[].delta.content`
// instead. The opening `<tool_call>` is never present (upstream already
// consumed it), so what an Omnipus client receives is a text response that
// begins somewhere in the middle of a tool call and carries zero parsed
// tool calls:
//
//	two\nthree\nfour\nfive\n</arg_value><arg_key>path</arg_key>…</tool_call>
//
// Omnipus has no code that understands this dialect and must not grow any:
// a tool call is only ever accepted from the structured `tool_calls` field.
// What this file provides is DETECTION, so the agent loop can (a) refuse to
// render the residue to a user as if it were the assistant's answer, and
// (b) treat the round as a failed tool call it must repair or report,
// rather than as a finished answer.
//
// Known false positive, accepted deliberately: an assistant that legitimately
// writes one of these tags in prose (e.g. explaining this very format to a
// user) trips the detector. That is tolerated because the consequence is
// LOUD — the agent loop re-prompts and, if the text keeps arriving, ends the
// turn with a visible, typed error. The alternative (a narrower heuristic
// that occasionally lets residue through) fails silently, which this project
// treats as the more serious defect class.

// orphanToolCallMarkers is the closed set of tags that identify the dialect.
// Ordered longest-first only for readability; lookup does not depend on order.
//
// Keep this list closed and evidence-driven. Adding a marker widens the
// false-positive surface described above; do not add one speculatively.
var orphanToolCallMarkers = []string{
	"</arg_value>",
	"</tool_call>",
	"<arg_value>",
	"<tool_call>",
	"</arg_key>",
	"<arg_key>",
}

// maxOrphanToolCallMarkerLen is the byte length of the longest marker above.
// The streaming filter uses it to size its rescan overlap and its held-back
// tail, so a marker split across two SSE chunks is still caught.
const maxOrphanToolCallMarkerLen = 12

// OrphanToolMarkup describes residual tool-call markup found in a text
// response.
type OrphanToolMarkup struct {
	// Prose is everything before the first marker — the part of the response
	// that is genuinely the assistant talking, safe to show a user. NOT
	// trimmed: callers that need a trimmed value trim it themselves, so a
	// streaming caller can rely on Prose being a byte-exact prefix of the
	// text it was given.
	Prose string
	// Markup is the residue, from the first marker to the end. Diagnostic
	// only — it must never be rendered to a user and must never be fed back
	// to the model (echoing it invites the model to repeat it).
	Markup string
	// Marker is the specific tag that matched first, for logs and events.
	Marker string
}

// firstOrphanToolCallMarker returns the byte index of the earliest marker in
// text and the marker itself, or (-1, "") when text carries none.
func firstOrphanToolCallMarker(text string) (int, string) {
	best := -1
	marker := ""
	for _, m := range orphanToolCallMarkers {
		i := strings.Index(text, m)
		if i < 0 {
			continue
		}
		// On a tie prefer the longer marker so the reported Marker is the
		// most specific tag at that position.
		if best < 0 || i < best || (i == best && len(m) > len(marker)) {
			best, marker = i, m
		}
	}
	return best, marker
}

// DetectOrphanToolCallMarkup reports whether text contains residual native
// tool-call markup, and splits it into the leading prose and the residue.
//
// Callers should only act on a positive result for text that arrived WITHOUT
// a corresponding structured tool call — a round that produced real
// `tool_calls` and also happens to carry markup means the model emitted more
// calls than the upstream parser recovered, which is a different (and
// non-terminal) situation.
func DetectOrphanToolCallMarkup(text string) (OrphanToolMarkup, bool) {
	idx, marker := firstOrphanToolCallMarker(text)
	if idx < 0 {
		return OrphanToolMarkup{}, false
	}
	idx -= danglingArgNameLen(text, idx, marker)
	return OrphanToolMarkup{
		Prose:  text[:idx],
		Markup: text[idx:],
		Marker: marker,
	}, true
}

// maxDanglingArgNameLen bounds the identifier trimmed by danglingArgNameLen.
// Argument names are short; a long run is far more likely to be a real word
// the assistant wrote than a parameter name, so it is left alone.
const maxDanglingArgNameLen = 64

// danglingArgNameLen returns how many bytes immediately before an UNMATCHED
// `</arg_key>` are the argument's own name rather than prose.
//
// Why this case and no other: a closing `</arg_key>` whose opening tag is
// absent means the upstream parser flushed mid-key, so the identifier sitting
// directly against that tag is provably the parameter name. Observed live as
//
//	"…Writing the file properly now.content</arg_key><arg_value>…"
//
// where "content" is the write_file parameter, not part of the sentence.
//
// The sibling case has no equivalent answer and deliberately gets none: text
// before an unmatched `</arg_value>` is the tail of an argument VALUE, which
// is unbounded and byte-for-byte indistinguishable from prose. Guessing where
// it starts would mean silently deleting sentences the assistant really did
// write — a worse failure than showing a few stray words that the repair
// round then supersedes.
func danglingArgNameLen(text string, idx int, marker string) int {
	if marker != "</arg_key>" || idx <= 0 {
		return 0
	}
	if strings.Contains(text[:idx], "<arg_key>") {
		return 0 // a matched pair: this is quoted markup, not a flush remnant
	}
	n := 0
	for n < idx && n < maxDanglingArgNameLen {
		c := text[idx-n-1]
		isIdent := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_'
		if !isIdent {
			break
		}
		n++
	}
	if n >= maxDanglingArgNameLen {
		return 0
	}
	return n
}

// StreamTextFilter suppresses residual tool-call markup from a streaming text
// response BEFORE it is forwarded to a live view.
//
// Why it exists: the streamed text is not merely displayed, it is what the
// gateway's streamer accumulates and persists as the assistant transcript
// entry. Stripping the markup only at the end of the turn would still leak it
// to the user's screen mid-stream AND still persist it. Filtering at the
// forwarding seam closes both.
//
// Contract: Visible returns a prefix of the accumulated string, and the value
// it returns never shrinks across calls for a monotonically growing input, so
// a caller can keep deriving deltas by slicing off what it already sent.
//
// A mid-stream call holds back up to maxOrphanToolCallMarkerLen-1 trailing
// bytes when they could be the start of a marker split across SSE chunks. The
// held-back bytes are NOT lost: the caller reconciles against the provider's
// final Content once the stream ends (see pkg/agent/loop.go's ChatStream call
// site), which is also what covers a provider that never invoked the callback.
//
// The zero value is ready to use.
type StreamTextFilter struct {
	// markerAt is the index of the first marker once found, else -1. Set
	// through initialised so the zero value behaves as "nothing found yet".
	markerAt int
	// scanned is how many bytes of the accumulated string have been searched,
	// so each call scans only the new tail (plus an overlap) rather than the
	// whole buffer — the callback fires once per SSE chunk, and a full rescan
	// per chunk would be quadratic in the response size.
	scanned int
	// emitted is the length of the longest value already returned. It pins
	// the non-shrinking contract: the dangling-argument-name trim can want to
	// cut back behind bytes that have already been forwarded to a live view,
	// and those cannot be un-sent. Clamping here keeps the contract honest
	// rather than leaving the caller to absorb a shrink.
	emitted     int
	initialised bool
}

// Visible returns the portion of accumulated that is safe to forward.
func (f *StreamTextFilter) Visible(accumulated string) string {
	if f == nil {
		return accumulated
	}
	if !f.initialised {
		f.markerAt = -1
		f.initialised = true
	}
	if f.markerAt >= 0 {
		// Defensive: the accumulate-only contract says the input never
		// shrinks, but a provider bug must not panic a live turn.
		if f.markerAt > len(accumulated) {
			return accumulated
		}
		return accumulated[:f.markerAt]
	}

	from := f.scanned - (maxOrphanToolCallMarkerLen - 1)
	if from < 0 {
		from = 0
	}
	if from > len(accumulated) {
		from = len(accumulated)
	}
	if idx, marker := firstOrphanToolCallMarker(accumulated[from:]); idx >= 0 {
		at := from + idx
		// Apply the same dangling-argument-name trim the final strip does, so
		// the live view and the end-of-turn text agree from here on. Bytes
		// already forwarded before the tag arrived cannot be taken back — the
		// caller's non-shrinking guard simply stops sending — which is why
		// this seam is a best effort and DetectOrphanToolCallMarkup, applied
		// to the complete response, is the authority.
		f.markerAt = at - danglingArgNameLen(accumulated, at, marker)
		if f.markerAt < f.emitted {
			f.markerAt = f.emitted
		}
		f.scanned = len(accumulated)
		return accumulated[:f.markerAt]
	}
	f.scanned = len(accumulated)
	visible := accumulated[:len(accumulated)-partialMarkerTailLen(accumulated)]
	if len(visible) > f.emitted {
		f.emitted = len(visible)
	}
	return visible
}

// partialMarkerTailLen returns how many trailing bytes of s are a proper
// prefix of some marker, and therefore must be held back until the next
// chunk decides whether they complete one.
func partialMarkerTailLen(s string) int {
	maxK := maxOrphanToolCallMarkerLen - 1
	if maxK > len(s) {
		maxK = len(s)
	}
	for k := maxK; k >= 1; k-- {
		tail := s[len(s)-k:]
		for _, m := range orphanToolCallMarkers {
			if len(m) > k && strings.HasPrefix(m, tail) {
				return k
			}
		}
	}
	return 0
}
