// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-083 D-C / D9, EMB-075 / EMB-079 / EMB-080 / EMB-081 — the external video
// allow-list, and the ONE place it is decided.
//
// WHY THIS FILE EXISTS AT ALL, rather than two lists that happen to agree.
// The allow-list is consumed by two completely different surfaces:
//
//	1. the `frame-src` directive of the SPA's own Content-Security-Policy
//	   (embed.go) — what the BROWSER will permit;
//	2. `video_embed_hosts` on GET /api/v1/state — what the READER uses to
//	   decide whether to draw a play control at all.
//
// If those two ever disagree, the symptom is one of the two shapes this
// project has explicitly banned: a blank frame with nothing naming the cause
// (policy narrower than the reader thinks), or a play control that does
// nothing when pressed (reader offering what the policy refuses). embed.go's
// own comment records the precedent — shipping `connect-src 'self'` without the
// ICE schemes broke the live browser view and surfaced as a SERVER-side
// "capture/encoder/ICE" error, with CSP named nowhere.
//
// So both surfaces call ResolveVideoEmbedHosts and neither has its own copy of
// the rules. EMB-080's test still asserts the equality by parsing the two
// SERVED responses — a structural guarantee you have not watched hold is not
// evidence.

// DefaultVideoEmbedHosts is the shipped allow-list: exactly one entry
// (EMB-075).
//
// WHY `www.youtube-nocookie.com` AND NOT `www.youtube.com`. It is YouTube's own
// privacy-enhanced embed domain: the player served from it does not write
// YouTube's persistent tracking cookies into the viewer's browser until the
// viewer actually plays a video, and it serves the same `/embed/<id>` player
// path, so nothing is lost by preferring it. For a no-telemetry product whose
// operators are the reason this list is a config key at all, the domain that
// tracks less is the correct default and the more permissive one has to earn
// its place. An operator who wants `www.youtube.com` can add it; the shipped
// default does not make that choice for them.
//
// STORED AS A BARE HOSTNAME, not an origin, because that is what crosses the
// wire (contracts/components/schemas/AppState.yaml: "Allow-listed video-embed
// hostnames … whose URL's host EXACTLY matches an entry here"). The `https://`
// scheme is added when — and only when — the value is rendered into a CSP
// source, by videoEmbedFrameSources below. Keeping the scheme out of the stored
// value is what makes the reader's host comparison a plain string equality
// rather than a URL parse it could get subtly wrong.
var DefaultVideoEmbedHosts = []string{"www.youtube-nocookie.com"}

// maxVideoEmbedHosts matches AppState.yaml's `maxItems: 16`.
//
// The wire schema is the contract; a Go-side list longer than the schema
// permits would serve a policy the reader is then refused permission to
// receive — the drift this file exists to prevent, arriving through the
// validator instead of through a typo.
const maxVideoEmbedHosts = 16

// maxVideoEmbedHostLength matches AppState.yaml's `maxLength: 253`, which is
// the maximum length of a DNS name.
const maxVideoEmbedHostLength = 253

// ResolveVideoEmbedHosts returns the effective, validated video-embed
// allow-list for cfg.
//
// THE THREE STATES (config.GatewayConfig.VideoEmbedHosts documents them in
// full): a nil pointer means the key is absent and the shipped default applies;
// a non-nil pointer to an empty slice is the operator declining the external
// host outright; anything else is exactly those hosts, validated.
//
// The return is ALWAYS non-nil, so a caller can serve it as a JSON array
// without an absent/empty ambiguity reaching the reader: `[]` on the wire means
// "measured off", not "the server did not say".
//
// WHY INVALID ENTRIES ARE DROPPED RATHER THAN REFUSED AT BOOT. Two reasons, and
// the second is the one that matters. First, dropping is the fail-CLOSED
// direction for an allow-list: fewer framable hosts, never more. Second, and
// decisively, this function is the single validator BOTH surfaces use, so a
// dropped entry is dropped from the policy and from the reader's list in the
// same breath — they still agree, and the reader still renders an honest link
// for the host it was not given. A boot refusal would be louder but would also
// take a whole gateway down over a mistyped video hostname; a per-entry WARN
// naming the entry is the proportionate loud, and it is emitted every time the
// value is resolved rather than once at boot.
//
// The validation is not cosmetic. Every entry is concatenated into an HTTP
// response header, so an entry containing a space, a semicolon or a quote would
// not "look wrong" — it would silently restructure the Content-Security-Policy
// around it. `frame-src 'self' evil.example; script-src *` is one config edit
// away from a policy that grants everything, and the operator who typed it
// would see no error.
func ResolveVideoEmbedHosts(cfg *config.Config) []string {
	if cfg == nil || cfg.Gateway.VideoEmbedHosts == nil {
		return validVideoEmbedHosts(DefaultVideoEmbedHosts)
	}
	return validVideoEmbedHosts(*cfg.Gateway.VideoEmbedHosts)
}

// validVideoEmbedHosts normalises, validates and de-duplicates raw, preserving
// the operator's ordering for the entries that survive.
func validVideoEmbedHosts(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, entry := range raw {
		host := strings.ToLower(strings.TrimSpace(entry))
		if host == "" {
			continue
		}
		if reason := videoEmbedHostRejection(host); reason != "" {
			slog.Warn("gateway: ignoring an invalid gateway.video_embed_hosts entry",
				"entry", entry,
				"reason", reason,
				"effect", "this host is absent from the served Content-Security-Policy AND from "+
					"/api/v1/state, so a note embedding it renders as a link rather than a frame")
			continue
		}
		if seen[host] {
			continue
		}
		if len(out) >= maxVideoEmbedHosts {
			slog.Warn("gateway: gateway.video_embed_hosts is longer than the wire contract permits",
				"limit", maxVideoEmbedHosts,
				"dropped", entry,
				"effect", "AppState.yaml caps the list at maxItems, and a longer list would be "+
					"served in the policy but refused to the reader")
			break
		}
		seen[host] = true
		out = append(out, host)
	}
	return out
}

// videoEmbedHostRejection returns "" when host is an acceptable DNS hostname,
// or a human sentence explaining the refusal.
//
// Deliberately stricter than "does not break the header": a wildcard is
// refused, an IP literal is refused, and a scheme, port or path is refused
// rather than silently stripped. An operator who wrote `https://example.com/`
// meant something specific, and quietly reinterpreting it is how an allow-list
// ends up permitting a host nobody chose.
func videoEmbedHostRejection(host string) string {
	if len(host) > maxVideoEmbedHostLength {
		return "longer than the 253-character DNS limit the wire schema enforces"
	}
	if strings.Contains(host, "*") {
		return "wildcards are refused — a wildcard frame source permits every sub-domain, " +
			"including any an attacker can register"
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, "/:?#@\\") {
		return "must be a bare hostname — no scheme, port, path or credentials. The https:// " +
			"scheme is added when the value is rendered into the policy"
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "must be a dotted DNS name with at least two labels"
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "each DNS label must be between 1 and 63 characters"
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "a DNS label may not begin or end with a hyphen"
		}
		for _, r := range label {
			isLetter := r >= 'a' && r <= 'z'
			isDigit := r >= '0' && r <= '9'
			if !isLetter && !isDigit && r != '-' {
				return "only letters, digits and hyphens are permitted in a DNS label — " +
					"anything else would be concatenated verbatim into the response header"
			}
		}
	}
	// An all-numeric final label makes this an IPv4 literal rather than a name.
	// Refused so the list stays what the schema calls it — hostnames — and so a
	// literal never becomes the thing the reader compares a URL's host against.
	last := labels[len(labels)-1]
	allDigits := true
	for _, r := range last {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return "an IP literal is not a hostname; the allow-list holds DNS names"
	}
	return ""
}

// videoEmbedFrameSources renders hosts as CSP source expressions.
//
// HTTPS ONLY, and not negotiable through configuration: a frame source is a
// document this application's origin invites in, and permitting it over
// cleartext would let anything on the path replace it. The stored value is a
// bare hostname precisely so this decision lives here, in one line, rather than
// in whatever an operator happened to type.
func videoEmbedFrameSources(hosts []string) []string {
	sources := make([]string, 0, len(hosts))
	for _, host := range hosts {
		sources = append(sources, "https://"+host)
	}
	return sources
}
