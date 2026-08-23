// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-067 §10.3 (amended 2026-08-23) — the policy BUILDER, at unit level.
//
// SCOPE, stated up front so a green here is not read as more than it is.
// rest_library_preview_test.go asserts the served header against the spec
// markdown, end to end through the real handlers. This file asserts the three
// things that live UNDER that oracle and that it cannot localise:
//
//  1. THE SUBSTITUTION IS STRUCTURALLY SOUND. Twelve directives, in §10.3's
//     order, in BOTH substitutions — including the collapsed one, which is the
//     branch nobody looks at. A directive that goes missing has no visible
//     symptom: the preview still renders and is simply no longer contained.
//  2. THE ORIGIN LANDS WHERE §10.3 PUTS IT — beside `'self'` in the six source
//     directives, and NOWHERE ELSE. `connect-src` staying `'none'` is the
//     measured requirement (FR-006); giving it the origin would look like a
//     consistency fix and would reopen the channel the requirement exists for.
//  3. THE ORIGIN → SOURCE-LIST RULES. Path stripping and the loopback alias
//     set decide whether a real deployment renders at all, and both fail
//     silently: the wrong list produces a perfectly valid header that blocks
//     every subresource.
//
// WHY `'self'` IS STILL HERE, since the whole exercise was about it not
// matching. `'self'` is what Chromium and Firefox match when the reader
// spelled the URL differently from the configured origin; the explicit host
// source is what WebKit can match inside an FR-005b attribute-sandboxed frame.
// Measured: a policy naming 127.0.0.1 while the browser reached the same
// socket as localhost blocked the bundle's script and stylesheet on ALL THREE
// engines. Keeping both is what makes a wrong or absent origin a Safari-only
// degradation instead of an all-engine outage — so "tidying" either one away
// is a one-line edit with no visible symptom, and each is pinned below.
//
// The directive extractor these tests lean on is itself mutation-checked: a
// parser that could not see a dropped directive would make every assertion
// here vacuous, which is the false-green shape this suite is audited against.

import (
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
// The six that carry the gateway origin are NOT re-listed here — they are
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
var libraryIsolationTestSources = []string{
	"http://127.0.0.1:5000",
	"http://localhost:5000",
	"http://[::1]:5000",
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

// TestBuildLibraryIsolationPolicy_TwelveDirectivesInOrder covers the collapsed
// branch as well as the substituted one.
//
// A directive silently going missing is the failure this whole exercise exists
// to prevent, and the collapsed branch is where it would go unnoticed longest:
// it is only reached on a wildcard bind with no gateway.public_url, i.e. in
// somebody else's container.
func TestBuildLibraryIsolationPolicy_TwelveDirectivesInOrder(t *testing.T) {
	cases := map[string][]string{
		"one explicit origin":        {"https://omnipus.acme.com"},
		"loopback alias set":         libraryIsolationTestSources,
		"no origin (collapsed form)": nil,
	}
	for name, sources := range cases {
		t.Run(name, func(t *testing.T) {
			names, _ := policyDirectives(t, buildLibraryIsolationPolicy(sources))
			assert.Equal(t, libraryIsolationDirectiveOrder, names)
		})
	}
}

// --- 2. Where the origin lands, and where it must not ----------------------

// TestBuildLibraryIsolationPolicy_OriginSitsBesideSelfInTheSixDirectives.
//
// Per-directive, not "the origin appears six times": six occurrences in the
// wrong places would satisfy a count, and five of six naming it would look
// identical in a diff while breaking exactly one class of subresource.
func TestBuildLibraryIsolationPolicy_OriginSitsBesideSelfInTheSixDirectives(t *testing.T) {
	joined := strings.Join(libraryIsolationTestSources, " ")
	policy := buildLibraryIsolationPolicy(libraryIsolationTestSources)
	_, values := policyDirectives(t, policy)

	for _, directive := range originBearingDirectives {
		assert.True(t, strings.HasPrefix(values[directive], "'self' "+joined),
			"§10.3: %s must name 'self' AND the gateway sources, in that order.\n"+
				"Dropping 'self' breaks Chromium and Firefox whenever the reader spelled the\n"+
				"URL differently from the configured origin; dropping the sources leaves Safari\n"+
				"with an unstyled, inert preview inside the FR-005b sandbox attribute.\n"+
				"got: %s %s", directive, directive, values[directive])
	}

	// The origin belongs in those six and nowhere else. connect-src is called
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
// Substituting an origin changes which sources the six directives match. It
// must change nothing else — and the parts it must not touch are exactly the
// parts that fail invisibly.
func TestBuildLibraryIsolationPolicy_KeepsBothMechanismsAndTheOmissions(t *testing.T) {
	for name, sources := range map[string][]string{
		"substituted": libraryIsolationTestSources,
		"collapsed":   nil,
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

// TestBuildLibraryIsolationPolicy_CollapsedFormKeepsSelf pins FR-005c's
// promise from the builder's side: a gateway that cannot resolve its own
// origin serves what it served yesterday, exactly.
//
// The byte-for-byte half is asserted in rest_library_preview_test.go against
// preAmendmentIsolationPolicy, the hand-transcribed pre-amendment literal.
// Here the property is the one a refactor would break: `'self'` must survive
// the collapse. A collapsed form that dropped it would leave the six source
// directives with NO source at all — a policy that blocks every stylesheet and
// script on every engine, in the deployment least able to diagnose it.
func TestBuildLibraryIsolationPolicy_CollapsedFormKeepsSelf(t *testing.T) {
	collapsed := buildLibraryIsolationPolicy(nil)
	_, values := policyDirectives(t, collapsed)

	for _, directive := range originBearingDirectives {
		assert.True(t, strings.HasPrefix(values[directive], "'self'"),
			"FR-005c: %s must still name 'self' when no origin resolves; the collapse removes "+
				"the placeholder, not the fallback that keeps two engines working", directive)
	}
	assert.NotContains(t, collapsed, "://",
		"the collapsed form names no host source: there is no origin to name")

	// And the substituted form must actually differ, or the amendment is a
	// no-op every other assertion would accept.
	assert.NotEqual(t, collapsed, buildLibraryIsolationPolicy(libraryIsolationTestSources),
		"a resolved origin must change the policy; equal strings mean the template lost its "+
			"placeholders and Safari renders blank previews again")
}

// --- 3. Origin → source list ------------------------------------------------

// TestLibraryIsolationSources covers §10.3's substitution table: normalisation
// and the loopback alias set. Both decide whether a real deployment renders,
// and both fail silently — the wrong list is a perfectly valid header that
// blocks every subresource.
func TestLibraryIsolationSources(t *testing.T) {
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
		// arrive with a path or a trailing slash. A CSP host-source carrying a
		// path is a PATH MATCH and would stop matching /library-preview/… — a
		// blank preview, from a value that looks perfectly reasonable in
		// config.json.
		name:   "a path or trailing slash is stripped",
		origin: "https://omnipus.acme.com/omnipus/",
		want:   []string{"https://omnipus.acme.com"},
	}, {
		// The seeded default binds 127.0.0.1 and people open the SPA at
		// localhost. The preview iframe's src is a RELATIVE path, so it
		// resolves against whatever they typed. Measured on all three engines:
		// naming only 127.0.0.1 blocks every subresource when the browser
		// reached the same socket as localhost.
		name:   "a loopback bind names all three of its own spellings",
		origin: "http://127.0.0.1:5000",
		want:   libraryIsolationTestSources,
	}, {
		name:   "the canonical spelling comes first, whichever it is",
		origin: "http://localhost:5000",
		want: []string{
			"http://localhost:5000",
			"http://127.0.0.1:5000",
			"http://[::1]:5000",
		},
	}, {
		name:   "an IPv6 loopback literal expands the same way",
		origin: "http://[::1]:5000",
		want: []string{
			"http://[::1]:5000",
			"http://127.0.0.1:5000",
			"http://localhost:5000",
		},
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
			"http://[::1]:5000",
		},
	}, {
		name:   "a non-loopback LAN address is named alone",
		origin: "http://192.168.1.20:5000",
		want:   []string{"http://192.168.1.20:5000"},
	}, {
		// A wildcard bind with no gateway.public_url: CanonicalGatewayOrigin
		// returns "". This is FR-005c's degraded case, not a misconfiguration.
		name:   "no origin yields no source at all",
		origin: "",
		want:   nil,
	}, {
		name:   "an unparseable origin yields no source rather than a guess",
		origin: "not an origin",
		want:   nil,
	}, {
		name:   "a bare host with no scheme is not an origin",
		origin: "omnipus.acme.com",
		want:   nil,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, libraryIsolationSources(tc.origin))
		})
	}
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
// can bind [::1]:<port> while we listen on 127.0.0.1 alone. The accepted
// residual is documented at libraryIsolationSources. Do not rename this test
// to claim more than it checks.
func TestLibraryIsolationSources_AliasesShareSchemeAndPort(t *testing.T) {
	sources := libraryIsolationSources("http://127.0.0.1:5000")
	require.Len(t, sources, 3)
	for _, source := range sources {
		assert.True(t, strings.HasPrefix(source, "http://"),
			"%s changed scheme — an alias must be the same gateway, reachable the same way", source)
		assert.True(t, strings.HasSuffix(source, ":5000"),
			"%s changed port — an alias must be the same listener", source)
	}
}

// --- 4. The frozen value ----------------------------------------------------

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
		buildLibraryIsolationPolicy([]string{"https://omnipus.acme.com"}),
		libraryIsolationPolicy())

	state := libraryIsolationFrozen.Load()
	require.NotNil(t, state)
	assert.Equal(t, []string{"https://omnipus.acme.com"}, state.sources)
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
	assert.Contains(t, first, "http://localhost:5000",
		"the frozen value must carry the resolved source list, not a rebuilt guess")
}

// TestLibraryIsolationPolicy_UnfrozenIsTheCollapsedForm covers the state a unit
// test reaches when it never registers routes — and the state the process would
// be in if a future refactor moved the freeze after the listener starts.
//
// Either way the answer is §10.3's collapsed substitution, which is what a
// gateway with no derivable origin serves. It is not a fourth code path.
func TestLibraryIsolationPolicy_UnfrozenIsTheCollapsedForm(t *testing.T) {
	previous := libraryIsolationFrozen.Load()
	t.Cleanup(func() { libraryIsolationFrozen.Store(previous) })
	libraryIsolationFrozen.Store(nil)

	assert.Equal(t, buildLibraryIsolationPolicy(nil), libraryIsolationPolicy())
	assert.Nil(t, libraryIsolationFrozen.Load())
}

// TestFreezeLibraryIsolationPolicy_WildcardBindDegradesVisibly is FR-005c's
// documented consequence: a 0.0.0.0 bind with no gateway.public_url keeps all
// twelve directives and the collapsed `'self'` form, and names no host source.
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
			"%q is not one concrete host and MUST collapse to 'self' rather than widen the policy", bad)
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
