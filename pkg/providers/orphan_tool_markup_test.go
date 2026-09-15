// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// orphan_tool_markup_test.go — unit coverage for the detector and the
// streaming filter that keep residual native tool-call markup out of a user's
// chat.
//
// Oracle: the strings under uatLeaks are VERBATIM assistant messages from the
// UAT session that exposed this (openrouter → z-ai/glm-5.3,
// build/uat-home/sessions/session_01M2CVMBJGKMGNXHBVYGDSF430/transcript.jsonl).
// Expected values are derived from what a user should see, not from what the
// implementation happens to produce.

package providers

import (
	"strings"
	"testing"
)

// uatLeaks are the four captured leaks. Every one begins partway through a
// tool call — the upstream parser consumed the opening <tool_call> before
// giving up, which is why no opening tag is ever present in the wild.
var uatLeaks = []struct {
	name       string
	content    string
	wantProse  string
	wantMarker string
}{
	{
		name:       "cut_mid_arg_value",
		content:    "two\nthree\nfour\nfive\n</arg_value><arg_key>path</arg_key><arg_value>count.txt</arg_value></tool_call>",
		wantProse:  "two\nthree\nfour\nfive\n",
		wantMarker: "</arg_value>",
	},
	{
		name: "prose_then_residue",
		content: "The previous write was malformed and never landed. Writing the file properly now." +
			"content</arg_key><arg_value>one\ntwo\nthree\nfour\nfive\n</arg_value>" +
			"<arg_key>path</arg_key><arg_value>count.txt</arg_value></tool_call>",
		// "content" is the write_file PARAMETER NAME, flushed out when the
		// upstream parser gave up mid-key — not part of the sentence. It is
		// trimmed because an unmatched </arg_key> proves what it is.
		wantProse:  "The previous write was malformed and never landed. Writing the file properly now.",
		wantMarker: "</arg_key>",
	},
	{
		name:       "closing_tag_first",
		content:    "</arg_key><arg_value>count.txt</arg_value></tool_call>",
		wantProse:  "",
		wantMarker: "</arg_key>",
	},
	{
		name:       "tool_name_list_argument",
		content:    `["write_file", "read_file", "edit_file", "list_directory"]</arg_value></tool_call>`,
		wantProse:  `["write_file", "read_file", "edit_file", "list_directory"]`,
		wantMarker: "</arg_value>",
	},
}

// tagFragments are spelled out here rather than read from the production
// marker list, so a test cannot be made to pass by editing that list.
var tagFragments = []string{
	"<tool_call>", "</tool_call>",
	"<arg_key>", "</arg_key>",
	"<arg_value>", "</arg_value>",
}

func assertNoTags(t *testing.T, label, text string) {
	t.Helper()
	for _, frag := range tagFragments {
		if strings.Contains(text, frag) {
			t.Fatalf("%s still carries tool-call markup %q: %q", label, frag, text)
		}
	}
}

// TestDetectOrphanToolCallMarkup_LiveLeaks asserts that every leak captured in
// production is detected, split at the right place, and yields prose a user
// can safely be shown.
func TestDetectOrphanToolCallMarkup_LiveLeaks(t *testing.T) {
	for _, tc := range uatLeaks {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DetectOrphanToolCallMarkup(tc.content)
			if !ok {
				t.Fatalf("residual markup went undetected: %q", tc.content)
			}
			if got.Prose != tc.wantProse {
				t.Errorf("prose:\n got %q\nwant %q", got.Prose, tc.wantProse)
			}
			if got.Marker != tc.wantMarker {
				t.Errorf("marker: got %q, want %q", got.Marker, tc.wantMarker)
			}
			if got.Prose+got.Markup != tc.content {
				t.Errorf("prose+markup must reconstruct the input exactly")
			}
			assertNoTags(t, "prose", got.Prose)
		})
	}
}

// TestDetectOrphanToolCallMarkup_CleanText is the negative control. A false
// positive costs a re-prompt, so the detector must stay quiet on ordinary
// assistant output — including text that merely mentions tools, or contains
// unrelated angle brackets, HTML, or comparison operators.
func TestDetectOrphanToolCallMarkup_CleanText(t *testing.T) {
	clean := []string{
		"",
		"I wrote count.txt with ONE through FIVE, one per line.",
		"Use write_file with a path and content argument.",
		"The condition is a < b && b > c, so the loop never runs.",
		"<html><body><p>hello</p></body></html>",
		"```go\nfunc main() { fmt.Println(\"hi\") }\n```",
		"<tool_result>ok</tool_result>",
		"arg_key and arg_value are the argument names",
	}
	for _, s := range clean {
		if got, ok := DetectOrphanToolCallMarkup(s); ok {
			t.Errorf("false positive on %q (marker %q)", s, got.Marker)
		}
	}
}

// TestStreamTextFilter_SuppressesResidueAcrossChunks reproduces the live
// mechanism: the residue arrives split across SSE chunks. Nothing the filter
// forwards may ever contain a tag, the forwarded value must never shrink, and
// the genuine prose must still get through.
func TestStreamTextFilter_SuppressesResidueAcrossChunks(t *testing.T) {
	for _, tc := range uatLeaks {
		for _, chunkSize := range []int{1, 3, 7, 13, 64} {
			t.Run(tc.name, func(t *testing.T) {
				var f StreamTextFilter
				var acc strings.Builder
				prev := ""
				for i := 0; i < len(tc.content); i += chunkSize {
					end := i + chunkSize
					if end > len(tc.content) {
						end = len(tc.content)
					}
					acc.WriteString(tc.content[i:end])
					visible := f.Visible(acc.String())

					assertNoTags(t, "a forwarded stream prefix", visible)
					if len(visible) < len(prev) {
						t.Fatalf("forwarded value shrank: %q then %q", prev, visible)
					}
					if !strings.HasPrefix(visible, prev) {
						t.Fatalf("forwarded value is not an extension of the previous: %q then %q", prev, visible)
					}
					if !strings.HasPrefix(tc.content, visible) {
						t.Fatalf("forwarded value is not a prefix of the real text: %q", visible)
					}
					prev = visible
				}
				// The end-of-stream reconciliation the agent loop performs.
				final := tc.content
				if om, ok := DetectOrphanToolCallMarkup(final); ok {
					final = om.Prose
				}
				if final != tc.wantProse {
					t.Fatalf("final reconciled text: got %q, want %q", final, tc.wantProse)
				}
				// The streamed value and the final value must agree as far as
				// the shorter one goes. They can differ in length in ONE
				// direction: a dangling argument name may already have been
				// forwarded before the tag that proves what it is arrived, and
				// bytes cannot be un-sent. The final value is the authority
				// and is what gets persisted.
				if !strings.HasPrefix(final, prev) && !strings.HasPrefix(prev, final) {
					t.Fatalf("streamed and final text diverge: streamed %q, final %q", prev, final)
				}
			})
		}
	}
}

// TestStreamTextFilter_PassesCleanTextThrough pins that ordinary streaming is
// unaffected end to end: every byte arrives, in order, once the stream closes.
// A filter that quietly ate a trailing character would be the same class of
// silent degradation this whole change exists to remove.
func TestStreamTextFilter_PassesCleanTextThrough(t *testing.T) {
	texts := []string{
		"Hello — the file is written.",
		"a < b, and 3 > 2",
		"trailing angle bracket <",
		"partial looking tail </arg",
		strings.Repeat("lorem ipsum ", 200),
	}
	for _, text := range texts {
		for _, chunkSize := range []int{1, 2, 5, 11} {
			var f StreamTextFilter
			var acc strings.Builder
			visible := ""
			for i := 0; i < len(text); i += chunkSize {
				end := i + chunkSize
				if end > len(text) {
					end = len(text)
				}
				acc.WriteString(text[i:end])
				visible = f.Visible(acc.String())
				if !strings.HasPrefix(text, visible) {
					t.Fatalf("forwarded %q is not a prefix of %q", visible, text)
				}
			}
			// Mid-stream the filter may hold back a possible partial tag; the
			// agent loop's end-of-stream reconciliation delivers it, because
			// the full text carries no residue.
			if _, ok := DetectOrphanToolCallMarkup(text); ok {
				t.Fatalf("test input %q unexpectedly looks like residue", text)
			}
			if len(text)-len(visible) >= maxOrphanToolCallMarkerLen {
				t.Fatalf("filter held back %d bytes, more than one marker's worth", len(text)-len(visible))
			}
		}
	}
}

// TestStreamTextFilter_NilAndShrinkingInput covers the defensive paths: a nil
// receiver forwards unchanged, and an input that violates the accumulate-only
// contract must not panic a live turn.
func TestStreamTextFilter_NilAndShrinkingInput(t *testing.T) {
	var nilFilter *StreamTextFilter
	if got := nilFilter.Visible("anything"); got != "anything" {
		t.Errorf("nil filter must forward unchanged, got %q", got)
	}

	var f StreamTextFilter
	long := "hello </arg_value> tail"
	if got := f.Visible(long); got != "hello " {
		t.Fatalf("expected the residue to be cut, got %q", got)
	}
	if got := f.Visible("hi"); got != "hi" {
		t.Errorf("a shrinking input must be forwarded rather than panic; got %q", got)
	}
}

// TestPartialMarkerTailLen pins the hold-back rule: only a genuine prefix of a
// marker is withheld, and never more than one marker's worth.
func TestPartialMarkerTailLen(t *testing.T) {
	cases := map[string]int{
		"plain text":      0,
		"ends with <":     1,
		"ends with </":    2,
		"ends with </arg": 5,
		"ends with <tool": 5,
		"a < b":           0, // the bracket is not at the end; nothing to hold
		"a <":             1,
		"":                0,
		"</arg_value>":    0, // a complete marker is not a partial one
	}
	for in, want := range cases {
		if got := partialMarkerTailLen(in); got != want {
			t.Errorf("partialMarkerTailLen(%q) = %d, want %d", in, got, want)
		}
	}
}
