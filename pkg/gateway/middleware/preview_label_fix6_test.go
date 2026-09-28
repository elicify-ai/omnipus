// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// Gate wave-2 fix6 pins (PR #950, #798 ADR-094):
//
//   - SF-4 (silent-failure-hunter): PreviewIsolatedURL must REFUSE a base it
//     cannot parse — return "" so the caller mints NO isolated_url — instead
//     of degrading to a portless <label>.localhost URL that can never
//     dispatch (previewPortAgrees refuses portless-vs-explicit-port) and
//     would leave the user on the SPA with no error anywhere. The refusals
//     here are unreachable through ClassifyPreviewOrigin (it only returns
//     parseable bases) — the test pins the refusal for a FUTURE caller, the
//     same fail-closed shape the rest of the mint path has.
//
//   - TDA-1 (type-design-analyzer): the planted_cookie_cleared discriminant
//     is a duplicated Go↔TS literal with no contract binding; a drift
//     tripwire pins the two sides together (planted_cookie_fix6_test.go).

import (
	"net/url"
	"testing"
)

// TestPreviewFix6_PreviewIsolatedURL_RefusesUnparseableBase pins both the
// happy path (explicit port preserved) and the refusal contract: an
// unparseable or scheme-less base returns "", never a minted URL.
func TestPreviewFix6_PreviewIsolatedURL_RefusesUnparseableBase(t *testing.T) {
	// Happy path: ClassifyPreviewOrigin's Mode 1 base for an explicit-port
	// origin, and the URL the mint must emit (port preserved).
	token := "token-abc"
	want := "http://" + PreviewLabelForToken(token) + ".localhost:5000/"
	if got := PreviewIsolatedURL("http://localhost:5000", token); got != want {
		t.Fatalf("PreviewIsolatedURL(happy path) = %q, want %q", got, want)
	}

	// Refusal contract: no parse, no scheme — no URL. The previous fallback
	// minted a PORTLESS label URL here, which never dispatches and fails
	// silently in the browser.
	for _, base := range []string{
		"",             // empty base
		"not a url",    // parses to Scheme=="" — no scheme
		"://no-scheme", // url.Parse error (missing protocol scheme)
		"http://%zz",   // url.Parse error (invalid URL escape)
	} {
		if got := PreviewIsolatedURL(base, token); got != "" {
			t.Fatalf("PreviewIsolatedURL(%q) = %q, want \"\" (refusal — no URL may be minted from an unparseable base)", base, got)
		}
	}
}

// TestPreviewFix6_PreviewIsolatedURL_HappyPathShapes pins the remaining mint
// shapes so the refusal change cannot silently narrow the happy path:
// implicit-80 stays portless, query/fragment never survive, root path.
func TestPreviewFix6_PreviewIsolatedURL_HappyPathShapes(t *testing.T) {
	token := "token-abc"
	label := PreviewLabelForToken(token)

	cases := []struct {
		name string
		base string
		want string
	}{
		{"implicit 80 stays portless", "http://localhost", "http://" + label + ".localhost/"},
		{"explicit port preserved", "http://localhost:5000", "http://" + label + ".localhost:5000/"},
		{"upper-case host lower-cased by classification input", "http://LOCALHOST:8123", "http://" + label + ".localhost:8123/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PreviewIsolatedURL(tc.base, token)
			if got != tc.want {
				t.Fatalf("PreviewIsolatedURL(%q) = %q, want %q", tc.base, got, tc.want)
			}
			u, err := url.Parse(got)
			if err != nil || u.Scheme == "" || u.Host == "" {
				t.Fatalf("minted URL %q is not a parseable absolute URL", got)
			}
		})
	}
}
