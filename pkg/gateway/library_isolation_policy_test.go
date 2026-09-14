// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-067 §10.3 (amended 2026-08-23 and 2026-09-14) — the policy BUILDER, at
// unit level.
//
// SCOPE, stated up front so a green here is not read as more than it is.
// rest_library_preview_test.go asserts the served header against the spec
// markdown, end to end through the real handlers. This file asserts the things
// that live UNDER that oracle and that it cannot localise:
//
//  1. THE SUBSTITUTION IS STRUCTURALLY SOUND. Twelve directives, in §10.3's
//     order, in BOTH substitutions — including the `'self'` fallback, which is
//     the branch nobody looks at. A directive that goes missing has no visible
//     symptom: the preview still renders and is simply no longer contained.
//  2. THE SOURCES LAND WHERE §10.3 PUTS THEM — alone in the six source
//     directives, with NO `'self'` beside them, and NOWHERE ELSE. `connect-src`
//     staying `'none'` is the measured requirement (FR-006).
//  3. THE ORIGIN → SOURCE-LIST RULES. Path stripping, the loopback alias set
//     and (since 2026-09-14) the preview-prefix confinement decide whether a
//     real deployment renders at all AND whether a preview can reach the API.
//  4. WHAT THE SOURCES ADMIT. The default install's policy is pinned as a
//     hand-typed literal, and an independent transcription of CSP3's
//     host-source path matching shows the confined sources admit the preview
//     prefix and refuse every API path — while a bare origin, the pre-fix
//     shape, admits them.
//
// WHY `'self'` IS GONE (2026-09-14). `'self'` spans the whole gateway, the
// authenticated API included. WebKit attaches the SameSite=Strict session
// cookie to a FRAMED preview's subresource requests (ADR-067 D15.8), so any
// source that reaches /api/ lets untrusted HTML make logged-in GET requests on
// Safari. Confining every source to /library-preview/ closes that on every
// engine. The price is recorded in library_isolation_policy.go: a reader on an
// address spelling the source list does not name now loses a preview's
// external assets on every engine, not only on Safari.
//
// The directive extractor these tests lean on is itself mutation-checked: a
// parser that could not see a dropped directive would make every assertion
// here vacuous, which is the false-green shape this suite is audited against.

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// libraryIsolationDirectiveOrder is §10.3's twelve directives, in the order
// the header carries them. Transcribed from the specification, never read off
// the builder: an expected value copied from the implementation agrees with
// any mistake the implementation makes.
//
// The six that carry the sources are NOT re-listed here — they are
// originBearingDirectives in rest_library_preview_test.go, transcribed from
// §10.3's substitution table, and one copy of that list is the point.
var libraryIsolationDirectiveOrder = []string{
	"sandbox",
	"default-src",
	"script-src",
	"style-src",
	"img-src",
	"font-src",
	"media-src",
	"frame-src",
	"connect-src",
	"form-action",
	"base-uri",
	"object-src",
}

// libraryIsolationPreviewPrefixFromSpec is the path every source is confined
// to, typed from §10.3 rather than read from libraryPreviewPathPrefix, so that
// a change to the router's prefix that is not also made in the spec fails here
// instead of silently moving the fence along with the route.
const libraryIsolationPreviewPrefixFromSpec = "/library-preview/"

// policyDirectives splits a policy header into its directives, in order.
//
// Returned as an ordered name slice PLUS a value map rather than a map alone
// because order is part of what is asserted, and because a map would silently
// swallow a duplicate directive — browsers honour the first occurrence and
// ignore the rest, which is a way to disable a directive without deleting it.
func policyDirectives(t *testing.T, policy string) (names []string, values map[string]string) {
	t.Helper()
	values = make(map[string]string)
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, _ := strings.Cut(part, " ")
		require.NotContains(t, names, name,
			"directive %q appears twice — browsers honour the first and ignore the rest, "+
				"which disables a directive without deleting it", name)
		names = append(names, name)
		values[name] = strings.TrimSpace(value)
	}
	return names, values
}

// libraryIsolationTestSources is a loopback source list of the shape §10.3's
// table produces, written out rather than computed so the assertions below do
// not agree with whatever libraryIsolationSources happens to return.
//
// TWO spellings, not the three §10.3 listed before 2026-09-09: `[::1]` was
// removed by defect HP-2 because CSP has no syntax for an IPv6 host at all.
// Each carries the preview prefix since 2026-09-14.
var libraryIsolationTestSources = []string{
	"http://127.0.0.1:5000/library-preview/",
	"http://localhost:5000/library-preview/",
}

// --- 0. The extractor can actually fail ------------------------------------

// TestPolicyDirectives_SeesADroppedDirective mutation-checks the parser every
// other test in this file depends on.
//
// Without it, a parser that quietly returned the expected list would make each
// assertion below pass on any mutation of the thing it guards.
func TestPolicyDirectives_SeesADroppedDirective(t *testing.T) {
	full := buildLibraryIsolationPolicy(libraryIsolationTestSources)
	names, _ := policyDirectives(t, full)
	require.Equal(t, libraryIsolationDirectiveOrder, names,
		"precondition: the unmutated policy must parse to §10.3's twelve directives")

	// Drop `connect-src 'none'` — the directive that closes fetch, XHR,
	// sendBeacon and WebSocket. Its absence changes nothing a reader can see.
	mutated := strings.Replace(full, "connect-src 'none'; ", "", 1)
	require.NotEqual(t, full, mutated, "the mutation must actually change the string")

	mutatedNames, _ := policyDirectives(t, mutated)
	assert.NotEqual(t, libraryIsolationDirectiveOrder, mutatedNames,
		"the extractor cannot see a dropped directive — every assertion in this file "+
			"that depends on it is vacuous")
	assert.NotContains(t, mutatedNames, "connect-src")
}

// --- 1. Twelve directives, in order, in both substitutions -----------------

// TestBuildLibraryIsolationPolicy_TwelveDirectivesInOrder covers the `'self'`
// fallback as well as the substituted form.
//
// A directive silently going missing is the failure this whole exercise exists
// to prevent, and the fallback is where it would go unnoticed longest: it is
// only reached on a wildcard bind with no gateway.public_url, i.e. in somebody
// else's container.
func TestBuildLibraryIsolationPolicy_TwelveDirectivesInOrder(t *testing.T) {
	cases := map[string][]string{
		"one confined source":         {"https://omnipus.acme.com/library-preview/"},
		"loopback alias set":          libraryIsolationTestSources,
		"no source ('self' fallback)": nil,
	}
	for name, sources := range cases {
		t.Run(name, func(t *testing.T) {
			names, _ := policyDirectives(t, buildLibraryIsolationPolicy(sources))
			assert.Equal(t, libraryIsolationDirectiveOrder, names)
		})
	}
}

// --- 2. Where the sources land, and where they must not --------------------

// TestBuildLibraryIsolationPolicy_SourcesStandAloneInTheSixDirectives.
//
// Per-directive, not "the source appears six times": six occurrences in the
// wrong places would satisfy a count, and five of six naming it would look
// identical in a diff while breaking exactly one class of subresource.
//
// The `'self'` assertions are the 2026-09-14 fix itself. Putting `'self'` back
// beside the sources is a one-token edit that makes every subresource directive
// admit the whole gateway again — /api/ included — and it would render every
// preview perfectly, so nothing but this assertion would notice.
func TestBuildLibraryIsolationPolicy_SourcesStandAloneInTheSixDirectives(t *testing.T) {
	joined := strings.Join(libraryIsolationTestSources, " ")
	policy := buildLibraryIsolationPolicy(libraryIsolationTestSources)
	_, values := policyDirectives(t, policy)

	for _, directive := range originBearingDirectives {
		assert.True(t, strings.HasPrefix(values[directive], joined),
			"§10.3: %s must begin with the path-confined gateway sources.\n"+
				"Without them Safari loads no external script or stylesheet inside the FR-005b\n"+
				"sandbox attribute, and — with no 'self' left — neither does any other engine.\n"+
				"got: %s %s", directive, directive, values[directive])
		assert.NotContains(t, values[directive], "'self'",
			"2026-09-14: %s must not carry 'self' when an origin is known. 'self' spans the whole "+
				"gateway, API included, and WebKit sends the session cookie on a framed preview's "+
				"subresource requests — so 'self' makes untrusted HTML an authenticated API caller "+
				"on Safari. got: %s", directive, values[directive])
	}
	assert.NotContains(t, policy, "'self'",
		"with a source list present, 'self' must appear nowhere in the policy")

	// The sources belong in those six and nowhere else. connect-src is called
	// out by name because it is the one a later reader would "fix".
	assert.Equal(t, "'none'", values["connect-src"],
		"FR-006: connect-src opens a CHANNEL rather than loading a subresource, and stays "+
			"'none' — no fetch, XHR, sendBeacon or WebSocket to any origin, the gateway's included")

	bearing := make(map[string]bool, len(originBearingDirectives))
	for _, d := range originBearingDirectives {
		bearing[d] = true
	}
	for _, name := range libraryIsolationDirectiveOrder {
		if bearing[name] {
			continue
		}
		assert.NotContains(t, values[name], "://",
			"%s must not name a host source", name)
	}
}

// TestBuildLibraryIsolationPolicy_KeepsBothMechanismsAndTheOmissions.
//
// Substituting sources changes which URLs the six directives match. It must
// change nothing else — and the parts it must not touch are exactly the parts
// that fail invisibly.
func TestBuildLibraryIsolationPolicy_KeepsBothMechanismsAndTheOmissions(t *testing.T) {
	for name, sources := range map[string][]string{
		"substituted":     libraryIsolationTestSources,
		"'self' fallback": nil,
	} {
		t.Run(name, func(t *testing.T) {
			policy := buildLibraryIsolationPolicy(sources)
			_, values := policyDirectives(t, policy)

			assert.Equal(t, "allow-scripts", values["sandbox"],
				"the sandbox half is required — source directives alone let window.open out")
			assert.Equal(t, "'none'", values["default-src"],
				"the source-directive half is required — sandbox alone let five of seven "+
					"egress vectors out")

			assert.NotContains(t, policy, "allow-same-origin",
				"§10.3: granting allow-same-origin beside allow-scripts hands the page the "+
					"session cookie — and the opaque origin it withholds is the same fact as "+
					"'self' not matching under WebKit")
			assert.NotContains(t, policy, "allow-popups",
				"§10.3: the sandbox is the only thing that closes window.open")
			assert.NotContains(t, policy, "allow-forms")
			assert.NotContains(t, policy, "allow-downloads")
			assert.NotContains(t, policy, "frame-ancestors",
				"§10.3: frame-ancestors was never measured here — do not add it on reasoning alone")

			assert.NotContains(t, policy, "${",
				"an unsubstituted placeholder would ship as a literal source nothing matches")
			assert.NotContains(t, policy, "  ",
				"a doubled space is what a naive empty substitution leaves behind")
		})
	}
}

// TestBuildLibraryIsolationPolicy_EmptyListFallsBackToSelf pins FR-005c's
// promise from the builder's side: a gateway that cannot resolve its own
// origin serves what it served before 2026-08-23, exactly.
//
// The byte-for-byte half is asserted in rest_library_preview_test.go against
// preAmendmentIsolationPolicy, the hand-transcribed pre-amendment literal.
// Here the property is the one a refactor would break: the fallback must be
// `'self'` and not an empty source list. An empty list would leave the six
// source directives with NO source at all — a policy that blocks every
// stylesheet and script on every engine, in the deployment least able to
// diagnose it.
//
// This fallback is also the ONE case the 2026-09-14 path confinement cannot
// reach (CSP has no path-only source), which is why freezeLibraryIsolationPolicy
// warns about it.
func TestBuildLibraryIsolationPolicy_EmptyListFallsBackToSelf(t *testing.T) {
	fallback := buildLibraryIsolationPolicy(nil)
	_, values := policyDirectives(t, fallback)

	for _, directive := range originBearingDirectives {
		assert.True(t, strings.HasPrefix(values[directive], "'self'"),
			"FR-005c: %s must name 'self' when no origin resolves; the substitution replaces the "+
				"placeholder, it never leaves the directive without a source", directive)
	}
	assert.NotContains(t, fallback, "://",
		"the fallback names no host source: there is no origin to name")

	// And the substituted form must actually differ, or the amendment is a
	// no-op every other assertion would accept.
	assert.NotEqual(t, fallback, buildLibraryIsolationPolicy(libraryIsolationTestSources),
		"a resolved origin must change the policy; equal strings mean the template lost its "+
			"placeholders and the preview is unconfined again")
}

// --- 3. Origin → origins → sources ------------------------------------------

// TestLibraryIsolationOrigins covers §10.3's substitution table below the path:
// normalisation and the loopback alias set. Both decide whether a real
// deployment renders, and both fail silently — the wrong list is a perfectly
// valid header that blocks every subresource.
func TestLibraryIsolationOrigins(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		want   []string
	}{{
		// The reverse-proxy case: the operator has told us the name the
		// browser uses, so aliases would be guesses.
		name:   "public_url is used alone",
		origin: "https://omnipus.acme.com",
		want:   []string{"https://omnipus.acme.com"},
	}, {
		// CanonicalGatewayOrigin returns gateway.public_url VERBATIM, so it can
		// arrive with a path or a trailing slash. The preview prefix is served at
		// the root of the host, so a source built on "https://host/omnipus"
		// would match no URL the browser ever requests.
		name:   "a path or trailing slash is stripped",
		origin: "https://omnipus.acme.com/omnipus/",
		want:   []string{"https://omnipus.acme.com"},
	}, {
		// The seeded default binds 127.0.0.1 and people open the SPA at
		// localhost. The preview iframe's src is a RELATIVE path, so it
		// resolves against whatever they typed. Measured on all three engines:
		// naming only 127.0.0.1 blocks every subresource when the browser
		// reached the same socket as localhost.
		name:   "a loopback bind names both of its expressible spellings",
		origin: "http://127.0.0.1:5000",
		want:   []string{"http://127.0.0.1:5000", "http://localhost:5000"},
	}, {
		name:   "the canonical spelling comes first, whichever it is",
		origin: "http://localhost:5000",
		want: []string{
			"http://localhost:5000",
			"http://127.0.0.1:5000",
		},
	}, {
		// HP-2 (2026-09-09). The canonical spelling is an IPv6 literal, which
		// CSP cannot express, so it is DROPPED FROM ITS OWN LIST — the one
		// place §10.3's "canonical origin first" rule has an exception. The
		// aliases still stand: they are other spellings of the same gateway,
		// they ARE expressible, and a reader who typed one of them is still
		// carried.
		name:   "an IPv6 loopback literal is dropped but keeps its expressible aliases",
		origin: "http://[::1]:5000",
		want: []string{
			"http://127.0.0.1:5000",
			"http://localhost:5000",
		},
	}, {
		// Go calls ::ffff:127.0.0.1 an IPv4 address (To4() != nil) AND a
		// loopback one, yet its URL host spelling still carries colons and is
		// no more expressible than ::1. This case is why the predicate asks
		// "does the hostname contain a colon" rather than "what family is it".
		name:   "an IPv4-mapped IPv6 literal is dropped the same way",
		origin: "http://[::ffff:127.0.0.1]:5000",
		want: []string{
			"http://127.0.0.1:5000",
			"http://localhost:5000",
		},
	}, {
		// A non-loopback IPv6 origin has nothing expressible left, so it takes
		// §10.3's Empty row and freezeLibraryIsolationPolicy WARNs.
		name:   "a non-loopback IPv6 origin yields no origin and degrades loudly",
		origin: "https://[2001:db8::1]:5000",
		want:   nil,
	}, {
		// 127.0.0.0/8 is loopback in full, not just the one address everybody
		// types — decided by net.IP rather than by the two spellings somebody
		// happened to think of.
		name:   "the rest of 127.0.0.0/8 is loopback too",
		origin: "http://127.0.0.2:5000",
		want: []string{
			"http://127.0.0.2:5000",
			"http://127.0.0.1:5000",
			"http://localhost:5000",
		},
	}, {
		name:   "a non-loopback LAN address is named alone",
		origin: "http://192.168.1.20:5000",
		want:   []string{"http://192.168.1.20:5000"},
	}, {
		// A wildcard bind with no gateway.public_url: CanonicalGatewayOrigin
		// returns "". This is FR-005c's degraded case, not a misconfiguration.
		name:   "no origin yields nothing at all",
		origin: "",
		want:   nil,
	}, {
		name:   "an unparseable origin yields nothing rather than a guess",
		origin: "not an origin",
		want:   nil,
	}, {
		name:   "a bare host with no scheme is not an origin",
		origin: "omnipus.acme.com",
		want:   nil,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, libraryIsolationOrigins(tc.origin))
		})
	}
}

// TestLibraryIsolationSources_AreConfinedToThePreviewPrefix is the 2026-09-14
// fix at the level of the source list: every source is an origin FOLLOWED BY
// the preview prefix, never a bare origin.
//
// A bare origin is exactly what shipped before, and it is the shape that let a
// framed preview reach /api/ with WebKit attaching the session cookie. It is
// asserted two ways: a table of hand-derived expected lists (which catches a
// wrong prefix or a lost alias) and a property over every source emitted (which
// catches a bare origin creeping back in for some origin shape the table does
// not list).
func TestLibraryIsolationSources_AreConfinedToThePreviewPrefix(t *testing.T) {
	// The spec's prefix and the router's prefix must be the same string, or the
	// fence and the route have drifted apart: either previews stop rendering
	// (the fence moved) or the fence admits a path the preview route does not
	// own (the route moved).
	require.Equal(t, libraryIsolationPreviewPrefixFromSpec, libraryPreviewPathPrefix,
		"the router serves previews under a different prefix from the one §10.3 confines "+
			"sources to — the two must move together")

	cases := []struct {
		origin string
		want   []string
	}{
		{"https://omnipus.acme.com", []string{"https://omnipus.acme.com/library-preview/"}},
		{"https://omnipus.acme.com/omnipus/", []string{"https://omnipus.acme.com/library-preview/"}},
		{"http://127.0.0.1:5000", libraryIsolationTestSources},
		{"http://[::1]:5000", []string{"http://127.0.0.1:5000/library-preview/", "http://localhost:5000/library-preview/"}},
		{"http://192.168.1.20:5000", []string{"http://192.168.1.20:5000/library-preview/"}},
		{"", nil},
		{"https://[2001:db8::1]:5000", nil},
		{"http://*", nil},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, libraryIsolationSources(tc.origin), "origin %q", tc.origin)
	}

	emitted := 0
	for _, origin := range []string{
		"https://omnipus.acme.com", "https://omnipus.acme.com/omnipus/", "http://127.0.0.1:5000",
		"http://localhost:5000", "http://127.0.0.2:5000", "http://[::1]:5000",
		"http://192.168.1.20:5000", "https://sub.example.co.uk:8443",
	} {
		for _, source := range libraryIsolationSources(origin) {
			emitted++
			u, err := url.Parse(source)
			require.NoError(t, err, "origin %q produced %q, which is not a URL", origin, source)
			assert.Equal(t, libraryIsolationPreviewPrefixFromSpec, u.Path,
				"origin %q produced %q — every source must be confined to exactly the preview "+
					"prefix. A bare origin admits the whole gateway, /api/ included", origin, source)
			assert.Empty(t, u.RawQuery, "a CSP source carries no query: %q", source)
			assert.Empty(t, u.Fragment, "a CSP source carries no fragment: %q", source)
			assert.True(t, strings.HasSuffix(source, "/"),
				"%q must end in '/': CSP3 §6.7.2.10 treats a path without a trailing slash as an "+
					"EXACT match, which would admit one URL and block every bundle asset", source)
		}
	}
	require.Positive(t, emitted,
		"no source was emitted for ANY origin — the property above passed having checked nothing")
}

// TestLibraryIsolationSources_AliasesShareSchemeAndPort states the property
// that makes the alias expansion a spelling fix rather than a widening.
//
// Every source denotes THIS gateway under another of its own names — same
// scheme, same port — so each still requires the path token to yield a byte,
// and connect-src 'none' means none of them can be used to open a channel. The
// seven measured egress vectors all target a DIFFERENT origin, which nothing
// here names.
// NOTE: this asserts scheme and port only, which is NOT the same as proving
// the aliases are this gateway — adversarial review showed a foreign process
// can bind [::1]:<port> while we listen on 127.0.0.1 alone, and "localhost"
// resolves to both families. The accepted residual is documented at
// libraryIsolationOrigins. Do not rename this test to claim more than it
// checks.
func TestLibraryIsolationSources_AliasesShareSchemeAndPort(t *testing.T) {
	sources := libraryIsolationSources("http://127.0.0.1:5000")
	require.Len(t, sources, 2)
	for _, source := range sources {
		u, err := url.Parse(source)
		require.NoError(t, err)
		assert.Equal(t, "http", u.Scheme,
			"%s changed scheme — an alias must be the same gateway, reachable the same way", source)
		assert.Equal(t, "5000", u.Port(),
			"%s changed port — an alias must be the same listener", source)
	}
}

// TestLibraryIsolationSources_NeverEmitsAnIPv6HostSource is defect HP-2's
// regression guard, written as a PROPERTY over every origin shape the resolver
// accepts rather than as another table of expected strings.
//
// WHY THE PROPERTY AND NOT A TABLE. The table above would go green again if
// somebody re-added `[::1]` to the alias list and to the expectations in one
// edit — which is exactly how the defect shipped in the first place. This
// asserts the reason the alias was wrong, so it fails on any re-introduction
// whatever the expectations say.
//
// WHY IT IS WRONG. CSP3 §2.3.1: `host-char = ALPHA / DIGIT / "-"`. An IPv6
// host has no host-source spelling, so the browser DISCARDS the source —
// Chromium and WebKit after logging "contains an invalid source … It will be
// ignored" ONCE PER DIRECTIVE, six times per preview, which is what buried the
// genuine violation errors during the HP-1 investigation. Measured 2026-09-09
// on Chromium 149, Firefox 151 and WebKit 26.5, across five candidate
// spellings (bracketed, bare, fully expanded, percent-encoded, portless), with
// `http://127.0.0.1:5177` and `http://localhost:5177` in the same header as
// the positive control. Nothing is lost by removing it: a source every engine
// discards grants exactly what no source grants.
func TestLibraryIsolationSources_NeverEmitsAnIPv6HostSource(t *testing.T) {
	origins := []string{
		"http://127.0.0.1:5000",
		"http://localhost:5000",
		"http://127.0.0.2:5000",
		"http://[::1]:5000",
		"http://[::ffff:127.0.0.1]:5000",
		"https://[2001:db8::1]:5000",
		"https://[fe80::1]",
		"https://omnipus.acme.com",
	}

	emitted := 0
	for _, origin := range origins {
		for _, source := range libraryIsolationSources(origin) {
			emitted++
			u, err := url.Parse(source)
			require.NoError(t, err, "origin %q produced %q, which is not even a URL", origin, source)
			assert.NotContains(t, u.Hostname(), ":",
				"origin %q produced %q — CSP3 §2.3.1's host-char is ALPHA/DIGIT/\"-\", so an "+
					"IPv6 host is discarded by every engine and costs one console error per "+
					"directive (defect HP-2). No spelling of it works, so it must not be "+
					"emitted at all", origin, source)
			assert.NotContains(t, source, "[",
				"origin %q produced %q — a bracketed IPv6 literal is not a CSP host-source",
				origin, source)
		}
	}

	// Positive control. Without it, a resolver that returned nothing for every
	// origin would satisfy every assertion above while disabling the whole
	// amendment.
	require.Positive(t, emitted,
		"no source was emitted for ANY origin — the assertions above passed having checked nothing")
}

// --- 4. What the frozen policy actually admits ------------------------------

// defaultInstallIsolationPolicy is the policy a default install (gateway bound
// to 127.0.0.1:5000, no public_url) must serve since 2026-09-14, typed out by
// hand from §10.3's template and substitution table. It is the Go pin of the
// new policy string's sources: a test that compared the frozen policy against
// buildLibraryIsolationPolicy's own output would agree with any mistake in it.
const defaultInstallIsolationPolicy = "sandbox allow-scripts; default-src 'none'; " +
	"script-src http://127.0.0.1:5000/library-preview/ http://localhost:5000/library-preview/ 'unsafe-inline'; " +
	"style-src http://127.0.0.1:5000/library-preview/ http://localhost:5000/library-preview/ 'unsafe-inline'; " +
	"img-src http://127.0.0.1:5000/library-preview/ http://localhost:5000/library-preview/ data: blob:; " +
	"font-src http://127.0.0.1:5000/library-preview/ http://localhost:5000/library-preview/; " +
	"media-src http://127.0.0.1:5000/library-preview/ http://localhost:5000/library-preview/; " +
	"frame-src http://127.0.0.1:5000/library-preview/ http://localhost:5000/library-preview/; " +
	"connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"

// reverseProxyIsolationPolicy is the same pin for a reverse-proxied deployment
// that sets gateway.public_url — one source, no aliases, still confined.
const reverseProxyIsolationPolicy = "sandbox allow-scripts; default-src 'none'; " +
	"script-src https://omnipus.acme.com/library-preview/ 'unsafe-inline'; " +
	"style-src https://omnipus.acme.com/library-preview/ 'unsafe-inline'; " +
	"img-src https://omnipus.acme.com/library-preview/ data: blob:; " +
	"font-src https://omnipus.acme.com/library-preview/; " +
	"media-src https://omnipus.acme.com/library-preview/; " +
	"frame-src https://omnipus.acme.com/library-preview/; " +
	"connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"

// TestLibraryIsolationPolicy_PinsTheConfinedSources pins the frozen policy
// byte for byte for the two deployment shapes that matter most.
func TestLibraryIsolationPolicy_PinsTheConfinedSources(t *testing.T) {
	t.Run("default install, loopback bind", func(t *testing.T) {
		freezeLibraryIsolationPolicyForTest(t, "http://127.0.0.1:5000")
		assert.Equal(t, defaultInstallIsolationPolicy, libraryIsolationPolicy(),
			"the default install must serve §10.3's template with both loopback spellings, each "+
				"confined to /library-preview/, and no 'self'")
	})
	t.Run("reverse proxy, gateway.public_url set", func(t *testing.T) {
		freezeLibraryIsolationPolicyForTest(t, "https://omnipus.acme.com/")
		assert.Equal(t, reverseProxyIsolationPolicy, libraryIsolationPolicy(),
			"gateway.public_url must yield exactly one confined source and no loopback aliases")
	})
}

// cspHostSourceAdmits reports whether a CSP host-source with an explicit
// scheme, host and port admits a URL on a request that has NOT been redirected.
//
// It is an independent transcription of CSP3 §6.7.2.9 ("does url match
// expression in origin with redirect count", redirect count 0) restricted to
// the one source shape this policy emits, plus §6.7.2.10 ("path-part matches
// path"), and it is written from the specification rather than from the
// builder so that the two cannot share a mistake. It deliberately does not
// model redirects: after one, CSP ignores the path entirely, which is why the
// gateway must never redirect on the preview prefix
// (library_preview_no_redirect_test.go), not something a matcher can express.
func cspHostSourceAdmits(t *testing.T, source, target string) bool {
	t.Helper()
	src, err := url.Parse(source)
	require.NoError(t, err)
	u, err := url.Parse(target)
	require.NoError(t, err)
	if !strings.EqualFold(src.Scheme, u.Scheme) || !strings.EqualFold(src.Host, u.Host) {
		return false
	}
	return cspPathPartMatches(src.EscapedPath(), u.EscapedPath())
}

// cspPathPartMatches is CSP3 §6.7.2.10, step by step.
func cspPathPartMatches(pathA, pathB string) bool {
	// 1. An empty path-part matches every path.
	if pathA == "" {
		return true
	}
	// 2. "/" matches the empty path.
	if pathA == "/" && pathB == "" {
		return true
	}
	// 3. A trailing "/" makes the match a prefix match; otherwise it is exact.
	exactMatch := !strings.HasSuffix(pathA, "/")
	// 4. Strictly split both on "/".
	listA := strings.Split(pathA, "/")
	listB := strings.Split(pathB, "/")
	// 5. A longer source path can never match.
	if len(listA) > len(listB) {
		return false
	}
	// 6. An exact match needs the same number of segments.
	if exactMatch && len(listA) != len(listB) {
		return false
	}
	// 7. A prefix match drops the source's final, empty segment.
	if !exactMatch {
		listA = listA[:len(listA)-1]
	}
	// 8. Every source segment must equal the target's, both percent-decoded.
	for i, pieceA := range listA {
		decodedA, errA := url.PathUnescape(pieceA)
		decodedB, errB := url.PathUnescape(listB[i])
		if errA != nil || errB != nil || decodedA != decodedB {
			return false
		}
	}
	return true
}

// TestLibraryIsolationPolicy_SourcesAdmitThePreviewPrefixAndNothingElse is the
// 2026-09-14 property in the terms a browser evaluates it: every host source in
// every subresource directive of the frozen default-install policy admits the
// preview route and refuses every other gateway path — the API above all.
//
// The positive control is the pre-fix shape. A BARE origin source must admit
// the API paths under the same matcher; otherwise "refused" below could just
// mean the matcher refuses everything.
func TestLibraryIsolationPolicy_SourcesAdmitThePreviewPrefixAndNothingElse(t *testing.T) {
	freezeLibraryIsolationPolicyForTest(t, "http://127.0.0.1:5000")
	_, values := policyDirectives(t, libraryIsolationPolicy())

	token := strings.Repeat("A", 43)
	admitted := []string{
		"/library-preview/" + token + "/site/index.html",
		"/library-preview/" + token + "/site/assets/app.js",
		"/library-preview/" + token + "/site/assets/logo.svg",
	}
	// Every gateway path a preview has no business requesting. /api/ is the one
	// the finding is about; the rest are the other authenticated or
	// cookie-reading surfaces on the same listener.
	refused := []string{
		"/api/v1/state",
		"/api/v1/library/ws/download?path=secret.txt",
		"/api/v1/media/workspace/ws/id",
		"/api/v1/chat/ws",
		"/",
		"/assets/index-abc123.js",
		"/preview/agent/" + token + "/",
		"/uploads/file.png",
		"/media/file.png",
		"/library-previewx/" + token + "/site/index.html",
	}

	checkedSources := 0
	for _, directive := range originBearingDirectives {
		for _, source := range strings.Fields(values[directive]) {
			if !strings.Contains(source, "://") {
				continue // 'unsafe-inline', data:, blob: — not host sources
			}
			checkedSources++
			base := source[:strings.Index(source, "/library-preview/")]
			for _, p := range admitted {
				assert.True(t, cspHostSourceAdmits(t, source, base+p),
					"%s source %s must admit its own preview asset %s, or bundles stop rendering",
					directive, source, p)
			}
			for _, p := range refused {
				assert.False(t, cspHostSourceAdmits(t, source, base+p),
					"%s source %s admits %s — a previewed page could request it, and WebKit would "+
						"attach the session cookie", directive, source, p)
			}
			// A different listener or scheme under the same prefix is not us.
			assert.False(t, cspHostSourceAdmits(t, source, "https://evil.example/library-preview/"+token+"/x.png"))
		}
	}
	assert.Equal(t, len(originBearingDirectives)*2, checkedSources,
		"every one of the six subresource directives must carry both loopback sources")

	// POSITIVE CONTROL: the pre-2026-09-14 shape, a bare origin, admits the API
	// under the very same matcher.
	for _, p := range []string{"/api/v1/state", "/api/v1/library/ws/download?path=secret.txt"} {
		assert.True(t, cspHostSourceAdmits(t, "http://127.0.0.1:5000", "http://127.0.0.1:5000"+p),
			"the matcher must admit %s under a bare origin source — otherwise its refusals above prove nothing", p)
	}
}

// --- 5. The frozen value ----------------------------------------------------

// freezeLibraryIsolationPolicyForTest freezes the policy for one test and puts
// the previous value back afterwards.
//
// Restoring matters: the frozen policy is package state shared by every test in
// this package, so a test that left its own origin behind would decide what a
// later test measures. A test-order dependency is indistinguishable from a real
// failure when it eventually bites.
func freezeLibraryIsolationPolicyForTest(t *testing.T, origin string) {
	t.Helper()
	previous := libraryIsolationFrozen.Load()
	t.Cleanup(func() { libraryIsolationFrozen.Store(previous) })
	freezeLibraryIsolationPolicy(origin)
}

// TestFreezeLibraryIsolationPolicy_ServesTheOriginItWasGiven.
func TestFreezeLibraryIsolationPolicy_ServesTheOriginItWasGiven(t *testing.T) {
	freezeLibraryIsolationPolicyForTest(t, "https://omnipus.acme.com")

	assert.Equal(t,
		buildLibraryIsolationPolicy([]string{"https://omnipus.acme.com/library-preview/"}),
		libraryIsolationPolicy())

	state := libraryIsolationFrozen.Load()
	require.NotNil(t, state)
	assert.Equal(t, []string{"https://omnipus.acme.com/library-preview/"}, state.sources)
}

// TestLibraryIsolationPolicy_IsIdenticalOnEveryRead is MV-13 reduced to the
// one thing that can break it in code.
//
// §10.3 requires every response on the preview-token path to carry the same
// bytes — the served file, the 404s, the 405 and the limiter's 429. A value
// rebuilt per response could differ between two of them; a frozen one cannot.
func TestLibraryIsolationPolicy_IsIdenticalOnEveryRead(t *testing.T) {
	freezeLibraryIsolationPolicyForTest(t, "http://127.0.0.1:5000")

	first := libraryIsolationPolicy()
	for i := 0; i < 3; i++ {
		assert.Equal(t, first, libraryIsolationPolicy(),
			"§10.3/MV-13: every response must carry the SAME policy, byte for byte")
	}
	assert.Contains(t, first, "http://localhost:5000/library-preview/",
		"the frozen value must carry the resolved source list, not a rebuilt guess")
}

// TestLibraryIsolationPolicy_UnfrozenIsTheSelfFallback covers the state a unit
// test reaches when it never registers routes — and the state the process would
// be in if a future refactor moved the freeze after the listener starts.
//
// Either way the answer is §10.3's `'self'` substitution, which is what a
// gateway with no derivable origin serves. It is not a fourth code path.
func TestLibraryIsolationPolicy_UnfrozenIsTheSelfFallback(t *testing.T) {
	previous := libraryIsolationFrozen.Load()
	t.Cleanup(func() { libraryIsolationFrozen.Store(previous) })
	libraryIsolationFrozen.Store(nil)

	assert.Equal(t, buildLibraryIsolationPolicy(nil), libraryIsolationPolicy())
	assert.Nil(t, libraryIsolationFrozen.Load())
}

// TestFreezeLibraryIsolationPolicy_WildcardBindDegradesVisibly is FR-005c's
// documented consequence: a 0.0.0.0 bind with no gateway.public_url keeps all
// twelve directives and the `'self'` fallback, and names no host source.
func TestFreezeLibraryIsolationPolicy_WildcardBindDegradesVisibly(t *testing.T) {
	// What middleware.CanonicalGatewayOrigin returns for host "0.0.0.0".
	freezeLibraryIsolationPolicyForTest(t, "")

	policy := libraryIsolationPolicy()
	names, _ := policyDirectives(t, policy)
	assert.Equal(t, libraryIsolationDirectiveOrder, names)
	assert.NotContains(t, policy, "://")

	state := libraryIsolationFrozen.Load()
	require.NotNil(t, state)
	assert.Empty(t, state.sources)
}

// TestFreezeLibraryIsolationPolicy_IPv6OriginTakesTheDocumentedBranch pins
// WHICH IPv6 cases degrade-and-warn and which do not, because defect HP-2's
// fix moved that line and a WARN set that drifts is a WARN set nobody trusts.
//
// freezeLibraryIsolationPolicy warns exactly when the source list is empty, so
// asserting the frozen sources IS asserting the WARN branch — there is no
// second condition between them.
//
//	non-loopback IPv6 → NO sources → 'self' fallback → WARN.
//	loopback IPv6     → TWO confined sources → substituted policy → NO warn.
func TestFreezeLibraryIsolationPolicy_IPv6OriginTakesTheDocumentedBranch(t *testing.T) {
	t.Run("a non-loopback IPv6 origin degrades and therefore warns", func(t *testing.T) {
		freezeLibraryIsolationPolicyForTest(t, "https://[2001:db8::1]:5000")

		state := libraryIsolationFrozen.Load()
		require.NotNil(t, state)
		assert.Empty(t, state.sources,
			"CSP cannot express an IPv6 host, so there is no source to name — and an empty "+
				"source list is exactly freezeLibraryIsolationPolicy's WARN condition")
		assert.Equal(t, buildLibraryIsolationPolicy(nil), libraryIsolationPolicy(),
			"the degraded case must be §10.3's 'self' fallback, not a fourth policy shape")
		assert.NotContains(t, libraryIsolationPolicy(), "://",
			"no host source may survive: the previous behaviour served an invalid one")
	})

	t.Run("an IPv6 loopback origin keeps its aliases and therefore does not warn", func(t *testing.T) {
		freezeLibraryIsolationPolicyForTest(t, "http://[::1]:5000")

		state := libraryIsolationFrozen.Load()
		require.NotNil(t, state)
		assert.Equal(t, []string{
			"http://127.0.0.1:5000/library-preview/",
			"http://localhost:5000/library-preview/",
		}, state.sources,
			"the canonical IPv6 spelling is dropped, but the two expressible spellings of the "+
				"SAME gateway remain — so this case must NOT join the WARN set")
		assert.NotContains(t, libraryIsolationPolicy(), "[",
			"no bracketed IPv6 literal may reach the served policy")
	})
}

// TestLibraryIsolationSources_NonConcreteHostFailsClosed pins the one way a
// config value could WIDEN this policy instead of breaking something visible.
//
// gateway.public_url is taken verbatim by CanonicalGatewayOrigin, and
// pkg/config/validator.go checks only that the scheme is http(s) and the host
// is non-empty. So "https://*.example.com" — and even "http://*" — passed
// validation and landed in six directives, including img-src and frame-src,
// which are two of the seven measured egress vectors. The preview kept
// rendering perfectly, so nothing would have told the operator. Every OTHER
// consumer of public_url fails closed on a malformed value: the browser
// rejects a wildcard Access-Control-Allow-Origin, and wsCheckOrigin does an
// exact compare.
//
// Found by adversarial review, 2026-08-23. Before that amendment no config
// value could weaken this policy at all — it was a compile-time constant.
func TestLibraryIsolationSources_NonConcreteHostFailsClosed(t *testing.T) {
	for _, bad := range []string{
		"https://*.example.com",
		"http://*",
		"https://*",
		"http://exa mple.com",
		"https://host;script-src *",
	} {
		require.Nil(t, libraryIsolationSources(bad),
			"%q is not one concrete host and MUST fall back to 'self' rather than widen the policy", bad)
	}

	// Positive control: without it, a function that refused everything would
	// satisfy the assertions above while disabling the fix entirely.
	for _, good := range []string{
		"https://example.com:5000",
		"http://127.0.0.1:5000",
		"https://sub.example.co.uk",
	} {
		require.NotEmpty(t, libraryIsolationSources(good),
			"%q is a concrete host and must still be named", good)
	}
}
