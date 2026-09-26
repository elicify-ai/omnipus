// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware — ADR-094 preview-isolation label helpers.
//
// This file is the ONE home of the Mode 1 preview-label vocabulary the
// ADR-094 preview-isolation feature shares across four packages:
//
//   - pkg/gateway — the host-dispatch mux resolves <label>.localhost to a
//     registration and serves the preview there (FR-004/FR-008);
//   - pkg/security — the SSRF layer admits exactly the label class
//     (FR-021), so the browser panel's fetches of previewed-app hosts are
//     not refused as SSRF;
//   - pkg/tools — web_serve mints the dual-URL result with isolated_url on
//     Mode 1 deployments (FR-001/FR-022) and refuses to mint on hostile
//     canonical origins (FR-003);
//   - pkg/agent and pkg/sandbox — the two registration stores resolve a
//     label back to their entry (LookupByLabel), so the Mode 1 label and
//     the Mode 2 token always map to the SAME registry entry (FR-008).
//
// The helpers live beside CanonicalGatewayOrigin because every consumer
// already imports this package, and because pkg/security must import the
// grammar while pkg/security is itself imported by pkg/gateway — the
// import graph stays acyclic only if the shared vocabulary sits at the
// bottom. The ADR-067 policy builder (libraryIsolationOrigins) is
// deliberately NOT renamed or moved: the spec (FR-019) keeps it where it
// is and adds these helpers alongside it.

package middleware

import (
	"crypto/sha256"
	"encoding/base32"
	"net/url"
	"regexp"
	"strings"
)

// previewLabelPattern is FR-005's label grammar: lower-case letters, digits
// and hyphens, with no leading or trailing hyphen (the inner-hyphen form is
// the only multi-segment shape, DS-1 rows 4–8).
var previewLabelPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// PreviewLabelMinLength / PreviewLabelMaxLength are DS-1's length bounds. The
// upper bound is a DNS label's 63-char limit; the lower bound matters only at
// admission checkers that want the normative entropy floor — the mint
// (PreviewLabelForToken) always emits PreviewLabelLength chars.
const (
	PreviewLabelMinLength = 1
	PreviewLabelMaxLength = 63
	// PreviewLabelLength is the exact length PreviewLabelForToken emits:
	// 26 lower-case base32 characters ≈ 130 bits of entropy, clearing
	// FR-005's 128-bit floor (DS-1 row 9).
	PreviewLabelLength = 26
)

// IsValidPreviewLabel reports whether label is a grammar-valid Mode 1 preview
// label: 1–63 chars of lower-case letters, digits and inner hyphens. It is a
// PURE grammar check — no entropy floor, no registry consult. The SSRF layer
// admits on this grammar alone (FR-021: admission is deployment-agnostic; the
// gateway's label registry is the real Mode-1 gate — spec F-1).
func IsValidPreviewLabel(label string) bool {
	if len(label) < PreviewLabelMinLength || len(label) > PreviewLabelMaxLength {
		return false
	}
	return previewLabelPattern.MatchString(label)
}

// previewBase32 is base32 with the RFC 4648 alphabet lower-cased. Its output
// (a–z, 2–7, no hyphens) is a subset of the label grammar, so a truncated
// encoding is always grammar-valid without further massaging.
var previewBase32 = base32.StdEncoding

// PreviewLabelForToken derives the Mode 1 label for a registration token:
// SHA-256 over the token string, base32 (lower-case), truncated to
// PreviewLabelLength characters. Deterministic by design — the label is a
// pure function of the token, which is what makes the FR-008 guarantee
// structural ("the label maps to the same registry entry as the Mode 2
// token") and what keeps the FR-029 lifecycle intact without any second
// store: renew-in-place keeps the token and therefore the label, while a
// rotation, replacement or revocation retires the token and therefore the
// label. Labels are minted lower-case (FR-005).
//
// The token is the 256-bit secret itself; hashing it does not weaken it, and
// the label reveals nothing about the token beyond "some token hashes to
// this" — which is already public knowledge for a label the agent handed
// out.
func PreviewLabelForToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	enc := strings.ToLower(previewBase32.EncodeToString(sum[:]))
	enc = strings.TrimRight(enc, "=")
	if len(enc) > PreviewLabelLength {
		enc = enc[:PreviewLabelLength]
	}
	return enc
}

// PreviewLabelHostSuffix is the fixed suffix of a Mode 1 preview host. The
// class is EXACTLY one label + ".localhost" (FR-021) — never a subdomain
// chain, never a foreign suffix.
const PreviewLabelHostSuffix = "localhost"

// ParsePreviewLabelHost splits a Host header (or URL host) of the shape
// <label>.localhost[:<port>] into its label and port. The host is matched
// case-insensitively and the label is returned lower-cased (spec F-2: the
// mux lower-cases Host before lookup). ok is false for anything that is not
// exactly one grammar-valid label followed by exactly ".localhost" — a
// second dot (a.b.localhost), a trailing dot (foo.localhost.), a missing
// dot (foolocalhost), an empty label part (ba..localhost), a hyphen-edge or
// otherwise grammar-invalid label, a non-numeric port, or an IPv6 literal —
// all of which must fall through to whatever treats the request.
func ParsePreviewLabelHost(hostport string) (label, port string, ok bool) {
	host := strings.ToLower(strings.TrimSpace(hostport))
	labelPart, portPart, hasPort := strings.Cut(host, ":")
	if hasPort {
		if portPart == "" || len(portPart) > 5 {
			return "", "", false
		}
		for _, r := range portPart {
			if r < '0' || r > '9' {
				return "", "", false
			}
		}
		port = portPart
	}
	parts := strings.Split(labelPart, ".")
	if len(parts) != 2 || parts[1] != PreviewLabelHostSuffix {
		return "", "", false
	}
	if !IsValidPreviewLabel(parts[0]) {
		return "", "", false
	}
	return parts[0], port, true
}

// PreviewOriginClass is the three-way verdict ClassifyPreviewOrigin returns
// for a canonical gateway origin (middleware.CanonicalGatewayOrigin's
// output): Mode 1 (the label-dispatch deployment), Mode 2 (the /preview/
// path deployment), or Refuse (a canonical origin so malformed that nothing
// may be minted from it — FR-003).
type PreviewOriginClass int

const (
	// PreviewOriginMode1: http to exactly "localhost" (any case), explicit
	// port or implicit 80. The deployment mints isolated_url and dispatches
	// <label>.localhost hosts.
	PreviewOriginMode1 PreviewOriginClass = iota
	// PreviewOriginMode2: every other usable origin — https loopback, IP
	// literals, real domains, Tailscale names. /preview/ path only; no
	// isolated_url is minted.
	PreviewOriginMode2
	// PreviewOriginRefuse: wildcard hosts and trailing-dot hosts (and
	// unparseable values). web_serve must REFUSE to mint (error result),
	// never degrade the policy (FR-003, DS-3 rows 6–7).
	PreviewOriginRefuse
)

// ClassifyPreviewOrigin classifies a canonical gateway origin for the whole
// Mode 1 surface. It is the shared origin-derivation helper of FR-019 —
// mint-side (pkg/tools), dispatch-side (pkg/gateway) and admission-side
// (pkg/security via its own port/grammar rules) all consult this one
// classification instead of re-deriving "is this Mode 1" locally.
//
// scheme/host parsing uses net/url so the wildcard and trailing-dot shapes
// are detected on the HOSTNAME, not on a substring match:
//
//	http://localhost:5000              → Mode1, base "http://localhost:5000"
//	http://LOCALHOST:5000              → Mode1, base lower-cased
//	http://localhost                   → Mode1, base "http://localhost" (implicit 80)
//	https://localhost:5000             → Mode2 (S-1.2: https is never Mode 1)
//	http://127.0.0.1:5000              → Mode2 (IP literal)
//	http://myhost.example.com:5000     → Mode2 (real domain)
//	http://*.wildcard.example:5000     → Refuse (FR-003)
//	http://localhost.:5000             → Refuse (trailing dot)
//
// The returned base is the lower-cased scheme://host[:port] the Mode 1 mint
// prefixes its label with; it is meaningful only for PreviewOriginMode1.
func ClassifyPreviewOrigin(origin string) (PreviewOriginClass, string) {
	trimmed := strings.TrimSpace(origin)
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return PreviewOriginRefuse, ""
	}
	hostname := u.Hostname() // strips brackets; lower-cases nothing
	if hostname == "" {
		return PreviewOriginRefuse, ""
	}
	// Wildcards name many hosts — refuse rather than mint a policy that
	// admits hosts we do not own (FR-003, the same fail-closed principle
	// libraryIsolationHostIsConcrete encodes for CSP sources).
	if strings.Contains(hostname, "*") {
		return PreviewOriginRefuse, ""
	}
	// A trailing dot is a distinct DNS root — "localhost." is not
	// "localhost", and minting <label>.localhost for it would advertise a
	// host the dispatch mux (which compares against "localhost" exactly,
	// lower-cased) would never serve.
	if strings.HasSuffix(hostname, ".") {
		return PreviewOriginRefuse, ""
	}
	if !strings.EqualFold(u.Scheme, "http") || !strings.EqualFold(hostname, PreviewLabelHostSuffix) {
		return PreviewOriginMode2, ""
	}
	base := u.Scheme + "://" + strings.ToLower(hostname)
	if u.Port() != "" {
		base += ":" + u.Port()
	}
	return PreviewOriginMode1, base
}

// PreviewIsolatedURL mints the Mode 1 URL for a registration token from a
// Mode 1 base returned by ClassifyPreviewOrigin ("http://localhost[:port]"):
// http://<label>.localhost[:port]/ — the token's label in place of the bare
// loopback host, root path, explicit port preserved, portless for an
// implicit-80 origin (FR-001/S-1.1, DS-3 row 10). The label is derived from
// the token (PreviewLabelForToken), so the URL is lower-case throughout
// (FR-005) and shares the token's lifecycle (FR-008/FR-029).
func PreviewIsolatedURL(mode1Base, token string) string {
	u, err := url.Parse(mode1Base)
	if err != nil || u.Scheme == "" {
		// mode1Base comes from ClassifyPreviewOrigin, which only ever returns
		// a parseable base; this branch is defense in depth for a future
		// caller passing an arbitrary string — degrade to the bare-label form
		// rather than mint a malformed URL.
		return "http://" + PreviewLabelForToken(token) + "." + PreviewLabelHostSuffix + "/"
	}
	host := PreviewLabelForToken(token) + "." + PreviewLabelHostSuffix
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	u.Host = host
	u.Path = "/"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
