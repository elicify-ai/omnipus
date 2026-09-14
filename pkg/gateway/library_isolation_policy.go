// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-067 §10.3 — the isolation policy, and the ONE place it is built.
//
// WHY THIS FILE EXISTS. Until 2026-08-23 the policy was a compile-time
// constant, byte-identical to §10.3's literal. It is now a TEMPLATE with one
// placeholder, substituted once at boot from the gateway's canonical origin.
// Two defects shaped the substitution, and each fix is load-bearing in a way
// that cannot be seen by reading the string.
//
// DEFECT 1 (2026-08-23): `'self'` DID NOT RENDER IN SAFARI. Measured one
// engine at a time (the amendment at the end of §10.3 has the full table):
//
//	WebKit, top level, no frame ................ script ran, stylesheet applied
//	WebKit, embedded WITH sandbox="allow-scripts" .... NEITHER
//	WebKit, embedded WITHOUT the attribute ..... both, again
//	Chromium and Firefox ....................... both, in every cell
//
// Under WebKit, `'self'` stops matching the serving origin once FR-005b's
// iframe sandbox ATTRIBUTE is layered on top of this policy's sandbox
// DIRECTIVE. The fix was to name the gateway's origin as an explicit host
// source: CSP3 §6.7.2 matches a host source against the REQUEST URL and never
// consults the document's origin, so it is immune to that class of defect.
//
// DEFECT 2 (2026-09-14): THE SOURCES REACHED THE AUTHENTICATED API, AND WEBKIT
// SENT THE SESSION COOKIE THERE. FR-006a accepts that a previewed page can load
// subresources from the gateway ON ONE CONDITION: they arrive unauthenticated.
// Measured in a frame (ADR-067 D15.8): WebKit attaches the SameSite=Strict
// omnipus-session cookie to a framed preview's requests, while itself labelling
// them Sec-Fetch-Site: cross-site, in every framed shape including the
// product's. SameSite cannot help, because site-for-cookies is computed from the
// top-level page and the top-level page IS Omnipus. Chromium and Firefox
// withhold the cookie everywhere. So `'self'` or a bare origin in img-src made
// `<img src="/api/v1/...">` in untrusted HTML a logged-in GET on Safari.
//
// THE FIX FOR DEFECT 2: every source the six directives name is PATH-CONFINED
// to the preview prefix — `http://127.0.0.1:5000/library-preview/`, never
// `http://127.0.0.1:5000` — and `'self'` is GONE whenever an origin is known.
// A host source whose path ends in "/" matches only URLs under that path (CSP3
// §6.7.2.10), so a preview loads its own bundle's assets and nothing else on the
// gateway: the authenticated API is not reachable from it at all, on any
// engine, with or without a cookie. Measured 2026-09-14 on Chromium, Firefox and
// WebKit, in a bare frame and in the product-shaped frame: the bundle's external
// script, stylesheet and image load, and an image aimed at /api/v1/state is
// refused before any request leaves, with an img-src violation naming it.
//
// WHAT THE PATH DOES NOT SURVIVE — READ THIS BEFORE ADDING ANY REDIRECT. CSP3
// §6.7.2.9 ignores a source's path once a request has been redirected.
// Measured the same day: a 302 from /library-preview/… to /api/v1/state was
// followed on all three engines, with the cookie attached on WebKit. The
// confinement is therefore exactly as strong as "nothing under
// /library-preview/ ever answers with a redirect". Today that holds by
// construction: the production mux (pkg/channels dynamicServeMux) matches on the
// decoded path and never cleans or redirects, neither wrapping middleware
// (config snapshot, CSRF) redirects, and the preview handler only ever writes an
// error page or http.ServeContent's output. library_preview_no_redirect_test.go
// drives that real chain with hostile paths and fails on any 3xx or Location
// header. A redirect added to this prefix reopens the hole with no other
// symptom; that test is the tripwire.
//
// THE COST, stated rather than hidden. `'self'` used to carry Chromium and
// Firefox whenever the reader spelled the gateway's address differently from
// the configured origin. With `'self'` gone, a reader on a spelling the source
// list does not name gets an unstyled, script-less preview on EVERY engine, not
// only on Safari. The loopback aliases below cover the default install
// (127.0.0.1 versus localhost); a reverse proxy or a LAN name needs
// gateway.public_url, which is already what CORS, the WebSocket origin check
// and web_serve's preview URLs require. Containment is never what degrades.
//
// THE ONE CONFIGURATION THE PATH CANNOT REACH. With no usable canonical origin
// (a 0.0.0.0 or :: bind with no gateway.public_url, a non-concrete host, a
// non-loopback IPv6 host) there is no host to hang a path on — CSP has no
// path-only source — so the placeholder becomes `'self'`, which reproduces the
// pre-amendment string byte for byte. Rendering there is unchanged, and so is
// the residual: `'self'` spans the whole gateway. freezeLibraryIsolationPolicy's
// WARN says so in the log.
//
// WHAT IS STILL TRUE, and must stay true: both mechanisms ship together. The
// `sandbox` directive ALONE let five of seven egress vectors out; the source
// directives ALONE let window.open out, because no CSP directive covers popup
// navigation. `allow-same-origin` is absent deliberately — withholding it is
// what makes the origin opaque, which is what makes document.cookie and
// localStorage throw. `'unsafe-inline'` is deliberate and is not the boundary.
// `connect-src` stays `'none'` and MUST NOT gain a source: FR-006 forbids
// fetch, XHR, sendBeacon and WebSocket to ANY origin, the gateway's included.
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

// libraryIsolationSourcesPlaceholder is §10.3's named placeholder, spelled
// exactly as the specification spells it. The test oracles read the template
// out of the spec markdown and substitute it, so a change to this spelling that
// is not also made in §10.3 fails rather than silently diverging.
//
// Renamed from ${GATEWAY_ORIGIN} on 2026-09-14 because it no longer stands for
// an origin: it stands for the PATH-CONFINED source list. A stale oracle still
// substituting the old name finds zero occurrences and fails loudly, which is
// the point of renaming rather than reusing the old spelling.
const libraryIsolationSourcesPlaceholder = "${PREVIEW_SOURCES}"

// libraryIsolationUnconfinedSource is what the placeholder becomes when no
// canonical origin can be named (§10.3's Empty row). It is the only way `'self'`
// can appear in the served policy, and it is the unconfined form: see this
// file's header, "THE ONE CONFIGURATION THE PATH CANNOT REACH".
const libraryIsolationUnconfinedSource = "'self'"

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
// invisible from reading it — see this file's header, and §10.3. In particular,
// DO NOT put `'self'` back beside the placeholder: it spans the whole gateway,
// API included, and undoes the 2026-09-14 confinement on every engine at once.
const libraryIsolationPolicyTemplate = "sandbox allow-scripts; default-src 'none'; " +
	"script-src ${PREVIEW_SOURCES} 'unsafe-inline'; " +
	"style-src ${PREVIEW_SOURCES} 'unsafe-inline'; " +
	"img-src ${PREVIEW_SOURCES} data: blob:; " +
	"font-src ${PREVIEW_SOURCES}; media-src ${PREVIEW_SOURCES}; " +
	"frame-src ${PREVIEW_SOURCES}; connect-src 'none'; form-action 'none'; " +
	"base-uri 'none'; object-src 'none'"

// buildLibraryIsolationPolicy substitutes §10.3's placeholder, applying the
// only two substitution rules that section allows.
//
// sources is a source LIST, not one source: on a loopback bind the same gateway
// is reachable under two spellings and both are named (see
// libraryIsolationSources). The list is joined with single spaces, in the
// caller's order, which is fixed and byte-stable — MV-13 asserts one identical
// string on every response, so an order that varied between calls would break
// the contract without breaking any page.
//
// An empty list substitutes `'self'` (§10.3's Empty row), which reproduces the
// pre-2026-08-23 string exactly. It is never an empty source list: a directive
// with no source at all would block every stylesheet and script on every
// engine, in the deployment least able to diagnose why.
func buildLibraryIsolationPolicy(sources []string) string {
	joined := strings.Join(sources, " ")
	if joined == "" {
		joined = libraryIsolationUnconfinedSource
	}
	return strings.ReplaceAll(libraryIsolationPolicyTemplate, libraryIsolationSourcesPlaceholder, joined)
}

// libraryIsolationSources turns the gateway's canonical origin into the
// path-confined source list §10.3's placeholder stands for: each origin from
// libraryIsolationOrigins, followed by the preview prefix.
//
// The prefix is libraryPreviewPathPrefix itself, not a second literal, so the
// path the policy admits and the path the router serves cannot drift apart. It
// ends in "/", which is what makes the source a PREFIX match (CSP3 §6.7.2.10)
// covering every file of every token rather than one exact URL.
//
// Returns nil exactly when libraryIsolationOrigins does, and then the policy
// falls back to the unconfined `'self'` form and freezeLibraryIsolationPolicy
// WARNs.
func libraryIsolationSources(canonicalOrigin string) []string {
	origins := libraryIsolationOrigins(canonicalOrigin)
	if len(origins) == 0 {
		return nil
	}
	sources := make([]string, 0, len(origins))
	for _, origin := range origins {
		sources = append(sources, origin+libraryPreviewPathPrefix)
	}
	return sources
}

// libraryIsolationOrigins turns the gateway's canonical origin into the origins
// (scheme://host[:port], no path) the source list is built on.
//
// canonicalOrigin is middleware.CanonicalGatewayOrigin(cfg) and nothing else —
// the boot-frozen origin the BROWSER actually reaches, which a reverse-proxy
// operator sets through gateway.public_url. There is deliberately no second
// origin computation anywhere in this package: CORS, the WebSocket
// CheckOrigin gate and web_serve's preview URLs all resolve it the same way,
// and a preview whose policy disagreed with the origin CORS enforces would be
// a defect with no symptom on some engines.
//
// WHY A LOOPBACK BIND YIELDS TWO ORIGINS, not one. The preview <iframe>'s
// src is a RELATIVE path, so it resolves against whatever the reader typed in
// the address bar. The seeded default binds 127.0.0.1, and people open the SPA
// at localhost — the same socket, a spelling nobody thinks about. Measured on
// all three engines: a policy naming 127.0.0.1 while the browser reached
// localhost blocked the bundle's script and stylesheet. Since 2026-09-14 there
// is no `'self'` beside these sources to rescue any engine, so without the
// aliases the default install would render blank previews EVERYWHERE for the
// most ordinary reason imaginable.
//
// AND WHY IT IS TWO RATHER THAN THE THREE §10.3 ORIGINALLY LISTED. The third
// spelling was `[::1]`, and CSP cannot express it — see
// libraryIsolationHostIsCSPExpressible. It was removed on 2026-09-09 rather
// than re-spelled because there is no spelling that works.
//
// IT IS A SMALL WIDENING, AND SAYING OTHERWISE WOULD BE FALSE. This comment
// claimed "not a widening" until adversarial review disproved it on
// 2026-08-23 with a live socket test, so the honest statement is:
//
// The gateway binds ONE address. On the seeded default that is 127.0.0.1 —
// IPv4 ONLY (gateway.go builds "host:port" from cfg.Gateway.Host, default
// pkg/config/defaults.go's "127.0.0.1"). We nevertheless name localhost.
// Nothing stops another unprivileged local process from binding
// [::1]:<our port>, and "localhost" resolves to BOTH families, so a browser
// doing happy-eyeballs may reach that socket first. That source is therefore
// not guaranteed to be us, and it appears in img-src and frame-src — two of
// the seven measured egress vectors. Such a process could also answer a
// preview-prefix request with a redirect, and CSP ignores a source's path
// after a redirect (see this file's header); that is one more thing the same
// local attacker can already do without a preview.
//
// AN EXPLICIT `http://[::1]:<port>` USED TO SIT BESIDE IT AND NO LONGER DOES
// (2026-09-09, defect HP-2). It never granted anything: CSP has no syntax for
// an IPv6 host, so every engine discarded it — Chromium and WebKit after
// logging one console error PER DIRECTIVE, six per preview, which is noise
// that hid the real violation errors underneath it. See
// libraryIsolationHostIsCSPExpressible for the grammar and the measurement.
//
// WHY WE ACCEPT IT. Exploiting it needs code already running on the operator's
// machine, which can read their files directly and needs no help from a
// preview. Against that: without the aliases, a user who types localhost:5000
// on a default install — the same server, a spelling nobody thinks about —
// gets a blank preview with nothing naming the cause. Measured, not
// theorised. The alias set stays and the residual is written down here rather
// than denied.
//
// What genuinely does NOT widen: the path token is still required to read a
// byte, every source is confined to the preview prefix, connect-src stays
// 'none' so no source here can open a channel, and a non-loopback origin gets
// exactly one origin and no aliases at all.
//
// Returns nil — which falls back to the unconfined `'self'` form — for an empty
// origin (a 0.0.0.0 or :: bind with no gateway.public_url), for an origin that
// does not parse as scheme://host, for a host that is not one concrete host,
// and (since 2026-09-09) for a NON-LOOPBACK IPv6 origin, whose single origin
// CSP cannot express. All four are reported by freezeLibraryIsolationPolicy's
// WARN, never silently. An IPv6 LOOPBACK origin does NOT return nil and does
// NOT warn: its canonical spelling is dropped but its two expressible loopback
// aliases still stand. A reader who actually types `[::1]` matches neither
// alias, and since 2026-09-14 no `'self'` covers them either, so their preview
// renders without external assets on every engine; reaching the gateway as
// localhost avoids it.
func libraryIsolationOrigins(canonicalOrigin string) []string {
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
	// not assumed. Returning nil falls back to 'self' and logs the degradation
	// WARN: the operator gets the pre-amendment behaviour plus a message, never
	// a policy that names hosts we do not own.
	if !libraryIsolationHostIsConcrete(u.Hostname()) {
		return nil
	}

	// Normalised to scheme://authority: gateway.public_url is taken verbatim by
	// CanonicalGatewayOrigin, so it can arrive with a trailing slash or a path.
	// libraryIsolationSources appends the preview prefix to what is returned
	// here, and the router serves that prefix at the root of the host, so an
	// operator's path ("https://host/app") would otherwise produce a source
	// ("https://host/app/library-preview/") that matches no URL the browser
	// ever requests — every subresource blocked, on every engine, for a value
	// that looks perfectly reasonable in config.json.
	canonical := u.Scheme + "://" + u.Host

	hostname := u.Hostname()

	// EMIT ONLY WHAT CSP CAN SPELL. An IPv6 host has no host-source spelling at
	// all (libraryIsolationHostIsCSPExpressible), so the canonical origin is
	// DROPPED when it is one. Dropped, not escaped: there is nothing to escape
	// it to — every candidate form was measured rejected, and a rejected source
	// grants exactly what no source grants while costing one console error per
	// directive.
	var origins []string
	if libraryIsolationHostIsCSPExpressible(hostname) {
		origins = append(origins, canonical)
	}

	if !libraryIsolationHostIsLoopback(hostname) {
		// nil for a non-loopback IPv6 origin: nothing expressible is left, so
		// the policy falls back to 'self' and freezeLibraryIsolationPolicy
		// WARNs. That is a DELIBERATE addition to the WARN's causes
		// (2026-09-09) and not a side effect — the previous behaviour emitted
		// one source every browser discarded, which is the same rendering
		// degradation with nothing in the log to notice it by.
		return origins
	}

	// Fixed order, canonical first, so the built policy is byte-stable across
	// calls and across processes with the same config (MV-13).
	//
	// "::1" IS NOT IN THIS LIST AND MUST NOT BE RE-ADDED — see
	// libraryIsolationHostIsCSPExpressible. An IPv6 loopback canonical origin
	// still reaches here and still gets both of these: they are the other
	// spellings a reader may have typed for this same gateway, and unlike the
	// canonical one they are expressible.
	for _, alias := range []string{"127.0.0.1", "localhost"} {
		host := alias
		if port := u.Port(); port != "" {
			host += ":" + port
		}
		candidate := u.Scheme + "://" + host
		if candidate != canonical {
			origins = append(origins, candidate)
		}
	}
	return origins
}

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

// libraryIsolationHostIsCSPExpressible reports whether hostname can be written
// as a CSP host-source AT ALL. It is a separate question from
// libraryIsolationHostIsConcrete's, and merging the two would blur two
// different reasons behind one boolean: "*" is refused because it names many
// hosts (a security reason); an IPv6 literal is refused because CSP has no
// syntax for it (a grammar reason).
//
// CSP3 §2.3.1's grammar is
//
//	host-source = [ scheme-part "://" ] host-part [ ":" port-part ] [ path-part ]
//	host-part   = "*" / [ "*." ] 1*host-char *( "." 1*host-char ) [ "." ]
//	host-char   = ALPHA / DIGIT / "-"
//
// — no colons and no brackets, so an IPv6 address has no spelling in it. The
// port separator is the only colon the production admits, which is why a
// bracketed literal is not merely unusual but unparseable.
//
// THIS IS MEASURED, NOT DEDUCED (2026-09-09, defect HP-2). One policy per
// candidate spelling, served over a real socket and read out of the console:
// `http://[::1]:5177`, `http://::1:5177`, `http://[0:0:0:0:0:0:0:1]:5177`,
// `http://%5B::1%5D:5177` and `http://[::1]` were ALL rejected by Chromium 149
// and WebKit 26.5 — "contains an invalid source … It will be ignored", one
// error per directive — and silently ignored by Firefox 151, with
// `http://127.0.0.1:5177` and `http://localhost:5177` accepted in the same
// header as the positive control. Confirmed functionally the same day: a
// document whose img-src named ONLY `http://[::1]:8200` could not load an image
// from `http://[::1]:8200` on any of the three engines. So no form of the
// source grants anything, and there is nothing to fix by re-spelling it.
//
// THE KNOWN LIMITATION THIS LEAVES, stated rather than dropped. A reader who
// reaches the gateway at `http://[::1]:<port>` cannot be named in this policy.
// Until 2026-09-14 `'self'` still carried them on Chromium and Firefox; with
// the sources path-confined and `'self'` gone, their preview's external
// script, stylesheet and assets are refused on every engine. Reaching the
// gateway by any spelling CSP can express — `localhost`, `127.0.0.1`, a DNS
// name — avoids it. Recorded in §10.3 as well as here.
//
// The test is "contains a colon" rather than `net.IP.To4() == nil`, because
// `::ffff:127.0.0.1` IS an IPv4 address to Go (To4 returns non-nil) and still
// has no CSP spelling. url.URL.Hostname() has already stripped any brackets, so
// a colon at this point means an IPv6 literal and nothing else.
func libraryIsolationHostIsCSPExpressible(hostname string) bool {
	return !strings.Contains(hostname, ":")
}

// libraryIsolationHostIsLoopback reports whether a hostname denotes this
// machine over the loopback interface. "localhost" is matched by name because
// it is a name, not an address; everything else is decided by net.IP, so the
// whole of 127.0.0.0/8 and ::1 are covered rather than the two spellings
// somebody happened to think of.
//
// Loopback-ness and CSP-expressibility are independent: ::1 is loopback and is
// NOT expressible. Answering one of those questions never answers the other.
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
// Called from newLibraryPreviewRoutes, i.e. during route construction and
// therefore before the listener accepts anything. The origin it is given is
// middleware.CanonicalGatewayOrigin(a.agentLoop.GetConfig()) — read at route
// construction, NOT restAPI.allowedOrigin. Both resolve to the same value at
// boot today; naming the wrong one here sent an auditor asking "is this
// boot-frozen?" to a field that is not the source. gateway.public_url is
// restart-gated (ADR-044) precisely so this value can be frozen.
//
// THE EMPTY CASE DEGRADES, LOUDLY, AND DOES NOT REFUSE. CanonicalGatewayOrigin
// returns "" for a 0.0.0.0 or :: bind with no gateway.public_url — an ordinary
// Docker or LAN deployment, not a misconfiguration. Failing closed there would
// leave those operators with no preview at all. The sandbox directive, the
// opaque origin and connect-src 'none' do not depend on the origin. What the
// empty case gives up is the 2026-09-14 path confinement — `'self'` spans the
// whole gateway, so a preview can again request API paths, unreadably — and,
// on Safari, external subresources inside an attribute-sandboxed frame. Both
// are named in the WARN below so neither is silent. There is no fallback code
// path and no second policy shape: one template, two substitutions.
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
		"library preview: no usable canonical gateway origin, so the preview isolation policy "+
			"falls back to 'self' and cannot confine previews to /library-preview/ — a previewed "+
			"page can request any gateway path (it cannot read the answers), and previews render "+
			"without their stylesheets and scripts in Safari. Set gateway.public_url to the URL "+
			"the BROWSER reaches, or give gateway.host a concrete address instead of a wildcard "+
			"bind. An IPv6 literal does not count: CSP's host-source grammar has no syntax for "+
			"one, so name a DNS host that resolves to it instead",
		map[string]any{
			// The raw value matters: this fires for FOUR different causes — an
			// empty origin (wildcard bind, no public_url), an unparseable one, a
			// non-concrete host such as a wildcard, and (added 2026-09-09) a
			// NON-LOOPBACK IPv6 origin, which CSP cannot express. Telling every
			// operator to "set public_url" is wrong for the last three, where
			// public_url or host is set and is simply not usable here. The value
			// shows which.
			//
			// An IPv6 LOOPBACK origin is deliberately NOT among them: it keeps
			// its two expressible aliases, so it never reaches this branch.
			"canonical_origin": canonicalOrigin,
			"fix":              "gateway.public_url (or a concrete, non-IPv6 gateway.host)",
		})
}

// libraryIsolationPolicy returns the §10.3 policy every Library byte response
// carries.
//
// Before the freeze — in a unit test that never registers routes — it is the
// `'self'` form, which is byte-identical to the string this package shipped
// before the 2026-08-23 amendment. That is the correct unfrozen answer and not
// a fallback: it is one of §10.3's two substitutions, produced by the same
// template through the same function, and it is exactly what a gateway with no
// derivable origin serves.
func libraryIsolationPolicy() string {
	if state := libraryIsolationFrozen.Load(); state != nil {
		return state.policy
	}
	return buildLibraryIsolationPolicy(nil)
}

// libraryIsolationPolicySources reports the path-confined source list the
// frozen policy was built from, for tests and for anything that needs to state
// WHICH sources the running gateway named rather than re-derive them.
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
