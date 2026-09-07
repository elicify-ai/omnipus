// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-067 §10.3 — the isolation policy, and the ONE place it is built.
//
// WHY THIS FILE EXISTS. Until 2026-08-23 the policy was a compile-time
// constant, byte-identical to §10.3's literal. It is now a TEMPLATE with one
// placeholder, substituted from the gateway's canonical origin, because the
// fixed literal DID NOT RENDER IN SAFARI.
//
// THE DEFECT, measured one engine at a time (the amendment at the end of
// §10.3 has the full table):
//
//	WebKit, top level, no frame ................ script ran, stylesheet applied
//	WebKit, embedded WITH sandbox="allow-scripts" .... NEITHER
//	WebKit, embedded WITHOUT the attribute ..... both, again
//	Chromium and Firefox ....................... both, in every cell
//
// The INLINE script still ran, so what broke was the EXTERNAL <script src> and
// <link rel=stylesheet>: under WebKit, `'self'` stops matching the serving
// origin once FR-005b's iframe sandbox ATTRIBUTE is layered on top of this
// policy's sandbox DIRECTIVE. Containment was never affected — the origin
// stayed opaque, document.cookie still threw, zero of seven egress vectors
// arrived. It was a rendering failure that broke US-1 AS-4, not a security
// one.
//
// THE FIX, and the two things about it that are load-bearing:
//
//  1. The six SOURCE directives name the gateway's origin IN ADDITION TO
//     `'self'`, never instead of it. CSP3 §6.7.2 host-source matching compares
//     the REQUEST URL against the source expression and never consults the
//     document's origin, so an explicit host source is immune to this entire
//     class of defect by construction. Keeping `'self'` beside it is what
//     makes a wrong or absent origin a Safari-only degradation to TODAY's
//     behaviour instead of an all-engine outage — measured: a policy naming
//     127.0.0.1 while the browser reached the same socket as localhost blocked
//     the bundle's script and stylesheet on ALL THREE engines.
//  2. `connect-src` stays `'none'` and MUST NOT gain the origin. FR-006
//     forbids fetch, XHR, sendBeacon and WebSocket to ANY origin, the
//     gateway's included, and that is measured rather than incidental. The six
//     directives that take the origin load SUBRESOURCES; the one that opens a
//     CHANNEL is deliberately not among them.
//
// WHAT IS STILL TRUE, and must stay true: both mechanisms ship together. The
// `sandbox` directive ALONE let five of seven egress vectors out; the source
// directives ALONE let window.open out, because no CSP directive covers popup
// navigation. `allow-same-origin` is absent deliberately — withholding it is
// what makes the origin opaque, which is what makes document.cookie and
// localStorage throw. `'unsafe-inline'` is deliberate and is not the boundary.
//
// THE STRING IS BUILT HERE AND NOWHERE ELSE. inline_serving_test.go parses the
// package's own source and fails the build if a second copy of it appears in
// any other file: two copies drift, the copy that drifts is the one nobody
// re-reads, and a dropped directive has NO VISIBLE SYMPTOM — the preview still
// renders and is simply no longer contained.

import (
	"net"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// libraryIsolationOriginPlaceholder is §10.3's named placeholder, spelled
// exactly as the specification spells it. The test oracle reads the template
// out of the spec markdown and substitutes it, so a change to this spelling
// that is not also made in §10.3 fails rather than silently diverging.
const libraryIsolationOriginPlaceholder = "${GATEWAY_ORIGIN}"

// libraryIsolationPolicyTemplate is ADR-067 §10.3's template, reproduced BYTE
// FOR BYTE. It is the whole of the P0 control for stage 1.
//
// It is assembled from concatenated fragments on purpose: the test's own
// transcription is a single unbroken literal, and the spec oracle is a third
// independent copy read from the markdown at test time, so a transcription
// error in any one of the three shows up as a mismatch rather than as two
// copies of the same mistake.
//
// DO NOT "TIDY" THIS STRING. Every part of it is load-bearing in a way that is
// invisible from reading it — see this file's header, and §10.3.
const libraryIsolationPolicyTemplate = "sandbox allow-scripts; default-src 'none'; " +
	"script-src 'self' ${GATEWAY_ORIGIN} 'unsafe-inline'; " +
	"style-src 'self' ${GATEWAY_ORIGIN} 'unsafe-inline'; " +
	"img-src 'self' ${GATEWAY_ORIGIN} data: blob:; " +
	"font-src 'self' ${GATEWAY_ORIGIN}; media-src 'self' ${GATEWAY_ORIGIN}; " +
	"frame-src 'self' ${GATEWAY_ORIGIN}; connect-src 'none'; form-action 'none'; " +
	"base-uri 'none'; object-src 'none'"

// buildLibraryIsolationPolicy substitutes §10.3's placeholder, applying the
// only two substitution rules that section allows.
//
// sources is a source LIST, not one origin: on a loopback bind the same
// gateway is reachable under three spellings and all three are named (see
// libraryIsolationSources). The list is joined with single spaces, in the
// caller's order, which is fixed and byte-stable — MV-13 asserts one identical
// string on every response, so an order that varied between calls would break
// the contract without breaking any page.
//
// THE EMPTY CASE IS THE ONE THAT IS EASY TO GET SUBTLY WRONG. §10.3: "the
// placeholder AND THE SINGLE SPACE PRECEDING IT are removed", collapsing
// `'self' ${GATEWAY_ORIGIN}` to `'self'` and reproducing the pre-amendment
// string exactly. Substituting the empty string alone would leave `'self'
// 'unsafe-inline'` with a doubled space — a policy that still WORKS, on every
// engine, while failing a byte-oracle for a reason nobody can see. That is
// why the space is part of what is removed, and why the test asserts the
// collapsed form against an independent transcription of the old literal
// rather than against this function's own output.
func buildLibraryIsolationPolicy(sources []string) string {
	joined := strings.Join(sources, " ")
	if joined == "" {
		return strings.ReplaceAll(
			libraryIsolationPolicyTemplate,
			" "+libraryIsolationOriginPlaceholder,
			"",
		)
	}
	return strings.ReplaceAll(libraryIsolationPolicyTemplate, libraryIsolationOriginPlaceholder, joined)
}

// libraryIsolationSources turns the gateway's canonical origin into the source
// list §10.3's placeholder stands for.
//
// canonicalOrigin is middleware.CanonicalGatewayOrigin(cfg) and nothing else —
// the boot-frozen origin the BROWSER actually reaches, which a reverse-proxy
// operator sets through gateway.public_url. There is deliberately no second
// origin computation anywhere in this package: CORS, the WebSocket
// CheckOrigin gate and web_serve's preview URLs all resolve it the same way,
// and a preview whose policy disagreed with the origin CORS enforces would be
// a defect with no symptom on two engines out of three.
//
// WHY A LOOPBACK BIND YIELDS THREE SOURCES, not one. The preview <iframe>'s
// src is a RELATIVE path, so it resolves against whatever the reader typed in
// the address bar. The seeded default binds 127.0.0.1, and people open the SPA
// at localhost — the same socket, a spelling nobody thinks about. Measured on
// all three engines: a policy naming 127.0.0.1 while the browser reached
// localhost blocked the bundle's script and stylesheet. Naming one spelling
// would therefore have left the default install broken in Safari for the most
// ordinary reason imaginable, which is the hazard `'self'` exists to cover on
// the other two engines and cannot cover here.
//
// IT IS A SMALL WIDENING, AND SAYING OTHERWISE WOULD BE FALSE. This comment
// claimed "not a widening" until adversarial review disproved it on
// 2026-08-23 with a live socket test, so the honest statement is:
//
// The gateway binds ONE address. On the seeded default that is 127.0.0.1 —
// IPv4 ONLY (gateway.go builds "host:port" from cfg.Gateway.Host, default
// pkg/config/defaults.go's "127.0.0.1"). We nevertheless name localhost and
// [::1]. Nothing stops another unprivileged local process from binding
// [::1]:<our port>, and "localhost" resolves to BOTH families, so a browser
// doing happy-eyeballs may reach that socket first. Those two sources are
// therefore not guaranteed to be us, and they appear in img-src and frame-src
// — two of the seven measured egress vectors.
//
// WHY WE ACCEPT IT. Exploiting it needs code already running on the operator's
// machine, which can read their files directly and needs no help from a
// preview. Against that: without the aliases, a user who types localhost:5000
// on a default install — the same server, a spelling nobody thinks about —
// gets a blank preview in Safari with nothing naming the cause. Measured, not
// theorised. The founder's constraint is that previews work in Safari, so the
// alias set stays and the residual is written down here rather than denied.
//
// What genuinely does NOT widen: the path token is still required to read a
// byte, connect-src stays 'none' so no source here can open a channel, and a
// document under this policy could already reach all of these at top level.
// A non-loopback origin gets exactly one source and no aliases at all.
//
// Returns nil — which collapses the policy to the pre-amendment `'self'` form
// — for an empty origin (a 0.0.0.0 or :: bind with no gateway.public_url) and
// for an origin that does not parse as scheme://host. Both are reported by
// freezeLibraryIsolationPolicy's WARN, never silently.
func libraryIsolationSources(canonicalOrigin string) []string {
	trimmed := strings.TrimSpace(canonicalOrigin)
	if trimmed == "" {
		return nil
	}

	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}

	// FAIL CLOSED ON A HOST SHAPE THAT IS NOT A SINGLE CONCRETE HOST.
	//
	// This is the ONLY consumer of gateway.public_url where a malformed value
	// RELAXES a control instead of breaking something visible. A wildcard in
	// Access-Control-Allow-Origin is rejected by the browser and wsCheckOrigin
	// does an exact compare, so an operator SEES those fail. Here a wildcard is
	// a legal CSP source expression: the preview would keep rendering perfectly
	// while script-src, img-src and frame-src silently admitted origins we do
	// not own — and img and iframe are two of the seven measured egress
	// vectors. Before this amendment no config value could weaken this policy
	// at all; it was a compile-time constant.
	//
	// pkg/config/validator.go checks only scheme and non-empty host, so
	// "https://*.example.com" and even "http://*" reach here intact. Verified,
	// not assumed. Returning nil collapses to 'self' alone and logs the
	// degradation WARN: the operator gets today's behaviour plus a message,
	// never a widened policy.
	if !libraryIsolationHostIsConcrete(u.Hostname()) {
		return nil
	}

	// Normalised to scheme://authority: gateway.public_url is taken verbatim by
	// CanonicalGatewayOrigin, so it can arrive with a trailing slash or a path.
	// A CSP host-source carrying a path is a PATH-MATCH — "https://host/app"
	// would stop matching "https://host/library-preview/…" and every
	// subresource would be blocked, on every engine, for a value that looks
	// perfectly reasonable in config.json.
	canonical := u.Scheme + "://" + u.Host

	hostname := u.Hostname()
	if !libraryIsolationHostIsLoopback(hostname) {
		return []string{canonical}
	}

	// Fixed order, canonical first, so the built policy is byte-stable across
	// calls and across processes with the same config (MV-13).
	sources := []string{canonical}
	for _, alias := range []string{"127.0.0.1", "localhost", "::1"} {
		host := alias
		if strings.Contains(alias, ":") {
			host = "[" + alias + "]"
		}
		if port := u.Port(); port != "" {
			host += ":" + port
		}
		candidate := u.Scheme + "://" + host
		if candidate != canonical {
			sources = append(sources, candidate)
		}
	}
	return sources
}

// libraryIsolationHostIsLoopback reports whether a hostname denotes this
// machine over the loopback interface. "localhost" is matched by name because
// it is a name, not an address; everything else is decided by net.IP, so the
// whole of 127.0.0.0/8 and ::1 are covered rather than the two spellings
// somebody happened to think of.
// libraryIsolationHostIsConcrete reports whether hostname names exactly ONE
// host and is therefore safe to emit as a CSP host-source.
//
// "*" is the shape that turns this policy from a fence into a door, and
// url.Parse accepts it happily. Everything that is not a plain DNS name or an
// IP literal is refused on the same principle: if we cannot name exactly one
// origin, we name none and fall back to 'self'.
func libraryIsolationHostIsConcrete(hostname string) bool {
	if hostname == "" || strings.Contains(hostname, "*") {
		return false
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return true
	}
	for _, r := range hostname {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

func libraryIsolationHostIsLoopback(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// libraryIsolationState is the frozen policy and the inputs it was built from.
// Kept together in one immutable value so a reader can never observe a policy
// built from one origin beside a record of another.
type libraryIsolationState struct {
	origin  string
	sources []string
	policy  string
}

// libraryIsolationFrozen holds the policy for the life of the process.
//
// A package-level value rather than a field is deliberate, and the reason is
// the requirement itself: §10.3 says every response carrying Library bytes
// carries the SAME policy, byte for byte — the token route, its 404s, its
// 405s, the rate limiter's 429, the media route and the uploads route. Those
// live in four files behind three different receivers, and the shared helpers
// they all funnel through (inline_serving.go) take no receiver at all. One
// frozen value is what makes "the same one" structural instead of a thing four
// call sites have to agree about.
//
// It is an atomic pointer rather than a plain string so the boot-time write
// and the per-request reads cannot race, and so a test can freeze an origin
// and restore the previous state without a lock discipline of its own.
var libraryIsolationFrozen atomic.Pointer[libraryIsolationState]

// libraryIsolationDegradedWarned makes the degradation WARN one-shot per
// process. A per-response warning on a path that legitimately fires one
// request per stylesheet, script, font and image would bury the very log the
// operator needs to read.
var libraryIsolationDegradedWarned atomic.Bool

// freezeLibraryIsolationPolicy resolves the source list once and pins the
// policy for the process.
//
// Called from registerLibraryPreviewRoutes, i.e. during route registration and
// therefore before the listener accepts anything. The origin it is given is
// middleware.CanonicalGatewayOrigin(a.agentLoop.GetConfig()) — read at
// route construction, NOT restAPI.allowedOrigin. Both resolve to the same
// value at boot today; naming the wrong one here sent an auditor asking
// "is this boot-frozen?" to a field that is not the source. Corrected
// 2026-08-23 after adversarial review found this file and
// rest_library_preview.go asserting opposite rationales.
// restart-gated (ADR-044) precisely so this value can be frozen.
//
// THE EMPTY CASE DEGRADES, LOUDLY, AND DOES NOT REFUSE. CanonicalGatewayOrigin
// returns "" for a 0.0.0.0 or :: bind with no gateway.public_url — an ordinary
// Docker or LAN deployment, not a misconfiguration. Failing closed there would
// leave those operators with no preview at all, a larger regression than the
// defect being fixed, and containment is IDENTICAL either way: the sandbox
// directive, the opaque origin and connect-src 'none' do not depend on the
// origin. What is lost is exactly one thing — external subresources inside an
// attribute-sandboxed frame on WebKit — so the policy collapses to the
// pre-amendment `'self'` form, which is correct on Chromium and Firefox and is
// today's behaviour on Safari. It is a RENDERING degradation, and §10.3
// requires it to be visible in the log rather than silent, which is what the
// WARN below is for. There is no fallback code path and no second policy
// shape: one template, two substitutions.
func freezeLibraryIsolationPolicy(canonicalOrigin string) {
	sources := libraryIsolationSources(canonicalOrigin)
	libraryIsolationFrozen.Store(&libraryIsolationState{
		origin:  canonicalOrigin,
		sources: sources,
		policy:  buildLibraryIsolationPolicy(sources),
	})

	if len(sources) > 0 {
		return
	}
	if libraryIsolationDegradedWarned.Swap(true) {
		return
	}
	logger.WarnCF("gateway",
		"library preview: no usable canonical gateway origin — previews will render "+
			"without their stylesheets and scripts in Safari (other browsers are "+
			"unaffected). Set gateway.public_url to the URL the BROWSER reaches, or "+
			"give gateway.host a concrete address instead of a wildcard bind",
		map[string]any{
			// The raw value matters: this fires for THREE different causes — an
			// empty origin (wildcard bind, no public_url), an unparseable one,
			// and a non-concrete host such as a wildcard. Telling every operator
			// to "set public_url" is wrong for the second and third, where
			// public_url or host is set but malformed. The value shows which.
			"canonical_origin": canonicalOrigin,
			"fix":              "gateway.public_url (or a concrete gateway.host)",
		})
}

// libraryIsolationPolicy returns the §10.3 policy every Library byte response
// carries.
//
// Before the freeze — in a unit test that never registers routes — it is the
// collapsed `'self'` form, which is byte-identical to the string this package
// shipped before the amendment. That is the correct unfrozen answer and not a
// fallback: it is one of §10.3's two substitutions, produced by the same
// template through the same function, and it is exactly what a gateway with no
// derivable origin serves.
func libraryIsolationPolicy() string {
	if state := libraryIsolationFrozen.Load(); state != nil {
		return state.policy
	}
	return buildLibraryIsolationPolicy(nil)
}

// libraryIsolationPolicySources reports the source list the frozen policy was
// built from, for tests and for anything that needs to state WHICH origins the
// running gateway named rather than re-derive them.
//
// A copy is returned. The frozen state is read by every response on the
// preview path, so handing out the backing array would let one caller's
// append or in-place edit change what every subsequent response advertises —
// with no error and no way to notice.
func libraryIsolationPolicySources() []string {
	state := libraryIsolationFrozen.Load()
	if state == nil || len(state.sources) == 0 {
		return nil
	}
	return append([]string(nil), state.sources...)
}
