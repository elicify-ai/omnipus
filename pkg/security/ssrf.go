// This file implements SEC-24 (SSRF protection) from the Omnipus BRD:
// blocking outbound HTTP requests to private/internal IP ranges, cloud metadata
// endpoints, and providing DNS rebinding protection.

package security

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Private and reserved IPv4 CIDR ranges that SSRF protection blocks.
var privateIPv4Ranges = []string{
	"10.0.0.0/8",      // RFC 1918
	"172.16.0.0/12",   // RFC 1918
	"192.168.0.0/16",  // RFC 1918
	"169.254.0.0/16",  // Link-local
	"127.0.0.0/8",     // Loopback
	"0.0.0.0/8",       // Current network
	"100.64.0.0/10",   // Shared address space (CGN)
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // Documentation (TEST-NET-1)
	"198.18.0.0/15",   // Benchmarking
	"198.51.100.0/24", // Documentation (TEST-NET-2)
	"203.0.113.0/24",  // Documentation (TEST-NET-3)
	// 224.0.0.0/4 (multicast) is not a meaningful unicast SSRF target, but is
	// blocked anyway for defense-in-depth: found during the web_fetch SSRF
	// consolidation (2026-07-07) as a class the old hand-rolled
	// isPrivateOrRestrictedIP checked (via net.IP.IsMulticast()) that this
	// checker's CIDR list did not yet cover.
	"224.0.0.0/4", // Multicast
}

// Private and reserved IPv6 ranges.
var privateIPv6Ranges = []string{
	"::/128",        // Unspecified address
	"::1/128",       // Loopback
	"fe80::/10",     // Link-local
	"fc00::/7",      // Unique local
	"ff00::/8",      // Multicast (see 224.0.0.0/4 comment above — same rationale)
	"::ffff:0:0/96", // IPv4-mapped — checked individually against IPv4 rules
}

// SSRFChecker validates IP addresses and hostnames against SSRF rules.
//
// The zero value (e.g. `var sc SSRFChecker`, or a struct literal that skips
// NewSSRFChecker) is safe to use directly: ipv4Nets/ipv6Nets/resolver are
// lazily populated with the built-in block-list and default DNS resolver on
// first use via initOnce (see ensureInit). This is a deliberate fail-closed
// property — a misconstructed checker must still enforce the full built-in
// private/reserved-range block list, never silently permit every private IP
// because its block-list slices happened to be nil. allowList/allowCIDRs are
// intentionally NOT lazily populated: they hold operator-supplied config
// that NewSSRFChecker reads from allowInternal, and correctly stay empty on
// a zero value since there is no config to read.
type SSRFChecker struct {
	initOnce sync.Once

	// ipv4Nets, ipv6Nets, allowList, allowCIDRs (TDA-2): READ-ONLY after
	// NewSSRFChecker (or ensureInit, for a zero-value checker) populates
	// them. Do NOT add a mutator (e.g. an "AddAllowEntry"/"AppendCIDR"
	// method) that appends or replaces these in place. CloneWithGatewayOrigin
	// aliases these slices/map BY REFERENCE across every clone it produces
	// AND the process-wide shared singleton (the one provider base_url and
	// skill-installer URL validation also consult) — they are never deep-
	// copied. An in-place mutation on any one checker would silently widen
	// (or narrow) the SSRF allow-list for every other checker sharing these
	// fields, including checkers the mutator's author never intended to
	// touch. If a genuinely new allow-list needs to be built, construct a
	// fresh SSRFChecker via NewSSRFChecker instead of mutating an existing
	// one's fields.
	ipv4Nets   []*net.IPNet
	ipv6Nets   []*net.IPNet
	allowList  map[string]bool // Allowlisted exact IPs and hostnames
	allowCIDRs []*net.IPNet    // Allowlisted CIDR ranges
	resolver   Resolver        // DNS resolver (injectable for testing)

	// gwMu guards gatewayHost/gatewayPort — the FR-018 / ADR-044 D2 gateway-
	// origin exception (see AllowGatewayOrigin). Zero value (gatewayHost=="")
	// means no exception is configured, which is fail-closed: isAllowedGatewayOrigin
	// always returns false and CheckURL falls through to the normal, fully
	// restrictive SSRF path. A separate mutex (rather than reusing initOnce)
	// because this is mutable operator/wiring state that may be set once at
	// boot and read on every navigation, not a one-time lazy default.
	gwMu        sync.RWMutex
	gatewayHost string // lower-cased; "" = no gateway origin configured
	gatewayPort int    // 0 = no gateway origin configured
}

// ensureInit lazily populates the built-in CIDR block-lists and default DNS
// resolver so that even a zero-value SSRFChecker enforces SSRF protection.
// Called at the top of every method that reads ipv4Nets, ipv6Nets, or
// resolver. Guarded by initOnce so a checker constructed via NewSSRFChecker
// (which already populates these fields) pays only a single no-op atomic
// check per instance, not a rebuild.
//
// Threat model: without this guard, `var sc SSRFChecker` has empty
// ipv4Nets/ipv6Nets slices, so CheckIP's range-membership loops never match
// and every private/loopback/RFC1918/link-local/cloud-metadata IP silently
// passes as "safe" (fail OPEN) — a silent SSRF-defense bypass. CheckHost
// would additionally panic on a nil resolver. Lazily building the same
// defaults NewSSRFChecker(nil) would have built closes both gaps: the zero
// value now fails CLOSED (blocks the full built-in range list) with no
// allowlist, exactly matching NewSSRFChecker(nil)'s behavior.
func (sc *SSRFChecker) ensureInit() {
	sc.initOnce.Do(func() {
		if sc.ipv4Nets == nil {
			for _, cidr := range privateIPv4Ranges {
				_, ipNet, err := net.ParseCIDR(cidr)
				if err != nil {
					panic(fmt.Sprintf("BUG: invalid hardcoded IPv4 CIDR %q: %v", cidr, err))
				}
				sc.ipv4Nets = append(sc.ipv4Nets, ipNet)
			}
		}
		if sc.ipv6Nets == nil {
			for _, cidr := range privateIPv6Ranges {
				_, ipNet, err := net.ParseCIDR(cidr)
				if err != nil {
					panic(fmt.Sprintf("BUG: invalid hardcoded IPv6 CIDR %q: %v", cidr, err))
				}
				sc.ipv6Nets = append(sc.ipv6Nets, ipNet)
			}
		}
		if sc.resolver == nil {
			sc.resolver = &defaultResolver{r: net.DefaultResolver}
		}
	})
}

// Resolver abstracts DNS resolution for testability.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// defaultResolver wraps net.Resolver.
type defaultResolver struct {
	r *net.Resolver
}

func (d *defaultResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return d.r.LookupIPAddr(ctx, host)
}

// NewSSRFChecker creates an SSRF checker with the given allowlist.
//
// Each entry in allowInternal may be:
//   - An exact IPv4 or IPv6 address (e.g. "192.168.1.5", "::1")
//   - A CIDR range (e.g. "10.0.0.0/8", "fc00::/7")
//   - A hostname (e.g. "localhost", "internal.corp")
//
// When a hostname is provided, CheckHost skips SSRF blocking for that exact
// hostname (case-insensitive). When a CIDR or IP is provided, CheckIP permits
// connections to addresses that fall within the range.
func NewSSRFChecker(allowInternal []string) *SSRFChecker {
	sc := &SSRFChecker{
		allowList: make(map[string]bool),
		resolver:  &defaultResolver{r: net.DefaultResolver},
	}

	for _, cidr := range privateIPv4Ranges {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("BUG: invalid hardcoded IPv4 CIDR %q: %v", cidr, err))
		}
		sc.ipv4Nets = append(sc.ipv4Nets, ipNet)
	}

	for _, cidr := range privateIPv6Ranges {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("BUG: invalid hardcoded IPv6 CIDR %q: %v", cidr, err))
		}
		sc.ipv6Nets = append(sc.ipv6Nets, ipNet)
	}

	// Parse each allowInternal entry as a CIDR, exact IP, or hostname.
	// CIDRs are stored for range-based IP checks; IPs and hostnames are stored
	// in the exact allowList map for O(1) lookups.
	for _, entry := range allowInternal {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		// Try CIDR first (e.g. "10.0.0.0/8").
		if _, ipNet, err := net.ParseCIDR(entry); err == nil {
			sc.allowCIDRs = append(sc.allowCIDRs, ipNet)
			continue
		}
		// Try exact IP (e.g. "127.0.0.1", "::1"). Normalise to canonical string
		// so "127.001" and "127.0.0.1" both match "127.0.0.1".
		if ip := net.ParseIP(entry); ip != nil {
			sc.allowList[ip.String()] = true
			continue
		}
		// Treat as a hostname (e.g. "localhost", "internal.corp").
		// Store in lower-case for case-insensitive matching in CheckHost.
		sc.allowList[strings.ToLower(entry)] = true
	}

	return sc
}

// SetResolver overrides the DNS resolver (for testing).
func (sc *SSRFChecker) SetResolver(r Resolver) {
	sc.resolver = r
}

// requiredGatewayOriginPathPrefix is the only path the gateway-origin SSRF
// exception permits (ADR-073). Before ADR-073 the exception matched on
// host:port alone, which — since it is evaluated before every other SSRF
// check, including the loopback CIDR block — let the agent's browser reach
// ANY path on the gateway's own origin, not just the /preview/ surface the
// exception exists for. That included the gateway's own internal REST API
// (e.g. /api/v1/config), which an agent could reach via browser_navigate
// even when explicitly denied the read_file/http tools that would otherwise
// be needed to read it, and — because a human's browser session to the live
// preview panel can carry standing gateway auth — potentially as an
// authenticated admin request. web_serve never needs the browser to reach
// anything outside /preview/: dev-mode servers are reverse-proxied through
// it (see rest_preview.go's proxyDevRequest) rather than navigated to
// directly, by design. Scoping the exception to this prefix closes that gap
// with no loss of legitimate function for either static or dev-mode
// previews.
const requiredGatewayOriginPathPrefix = "/preview/"

// AllowGatewayOrigin scopes a single, exact gateway host:port pair, AND the
// /preview/ path prefix on it (ADR-073), as an SSRF exception (FR-018,
// ADR-044 D2). It exists so the agent's built-in browser can reach the
// gateway's OWN preview URL (`http://localhost:<port>/preview/…`, the
// literal form `serve_web` emits when `gateway.public_url` is unset)
// without opening blanket loopback access to arbitrary local ports — the
// naive "allow localhost" the ADR explicitly rejects.
//
// host is matched case-insensitively; whitespace is trimmed. Passing an
// empty host or a non-positive/out-of-range port clears any previously
// configured exception, restoring the fail-closed default (no gateway
// origin allowed). There is only ever one gateway origin configured at a
// time — this is not a general-purpose allowlist mechanism, and calling it
// again replaces the prior value rather than adding to a set.
//
// See CheckURL / isAllowedGatewayOrigin for exactly what this permits: an
// exact host:port match, evaluated before any other SSRF check (including
// the 127.0.0.0/8 CIDR block). When host is "localhost" (the expected
// caller usage per D2 — the wiring lives in pkg/agent/loop.go), the
// resolved-loopback literal forms "127.0.0.1" and "::1" are ALSO accepted
// for the same port, matching r4 OBS-003's requirement to cover both the
// pre-resolution token and the resolved forms. A different configured host
// gets literal host:port matching only — no loopback expansion.
//
// Thread-safe (guarded by an internal RWMutex); intended to be called once
// at boot before the checker starts serving concurrent CheckURL calls, but
// safe to call at any time.
func (sc *SSRFChecker) AllowGatewayOrigin(host string, port int) {
	sc.gwMu.Lock()
	defer sc.gwMu.Unlock()

	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || port <= 0 || port > 65535 {
		// Invalid/cleared configuration — restore the fail-closed default
		// rather than storing a partial or nonsensical exception.
		sc.gatewayHost = ""
		sc.gatewayPort = 0
		return
	}
	sc.gatewayHost = host
	sc.gatewayPort = port
}

// CloneWithGatewayOrigin returns an INDEPENDENT SSRFChecker that shares this
// checker's immutable (post-construction) block-lists, allowlist, and resolver
// but carries its OWN gateway-origin exception. Setting or clearing the clone's
// exception never affects the receiver.
//
// Use this to grant a single tool (e.g. an agent's built-in browser) reach to
// the gateway's own origin via AllowGatewayOrigin WITHOUT widening the shared
// singleton that provider base_url and skill-installer URL validation also
// consult (ADR-044 D2). Calling AllowGatewayOrigin directly on the shared
// singleton would silently allow http://localhost:<gateway.port> on those
// unrelated CheckURL paths too — the "blanket loopback" the ADR rejected.
//
// The shared fields are read-only after NewSSRFChecker populates them, so
// aliasing them by reference across the receiver and the clone is safe for
// concurrent reads. The clone gets fresh sync primitives (its own initOnce and
// gwMu); ensureInit on the clone is a no-op because the aliased block-lists are
// already populated.
func (sc *SSRFChecker) CloneWithGatewayOrigin(host string, port int) *SSRFChecker {
	sc.ensureInit() // populate the shared block-lists/resolver before aliasing them
	clone := &SSRFChecker{
		ipv4Nets:   sc.ipv4Nets,
		ipv6Nets:   sc.ipv6Nets,
		allowList:  sc.allowList,
		allowCIDRs: sc.allowCIDRs,
		resolver:   sc.resolver,
	}
	clone.AllowGatewayOrigin(host, port)
	return clone
}

// isAllowedGatewayOrigin reports whether rawURL's literal (pre-resolution)
// host:port exactly matches the gateway origin configured via
// AllowGatewayOrigin, AND rawURL's path starts with
// requiredGatewayOriginPathPrefix (ADR-073). Fails closed: if no gateway
// origin has been configured, rawURL carries no explicit port, the port
// doesn't match, or the path isn't under /preview/, this always returns
// false and CheckURL falls through to the full SSRF path.
//
// The host:port match is on host:port only, NOT scheme: both http:// and
// https:// to the configured gateway host:port are accepted. This is
// intentional and harmless — a scheme mismatch against a plain-HTTP loopback
// gateway simply fails at the transport layer, and the exception exists only
// so the agent can reach its own gateway's preview origin, which is a single
// host:port regardless of scheme.
func (sc *SSRFChecker) isAllowedGatewayOrigin(rawURL string) bool {
	sc.gwMu.RLock()
	gwHost, gwPort := sc.gatewayHost, sc.gatewayPort
	sc.gwMu.RUnlock()

	if gwHost == "" || gwPort <= 0 {
		return false // no exception configured — fail closed
	}

	host, portStr, ok := extractHostPort(rawURL)
	if !ok {
		return false // no explicit port in the URL — never a gateway-origin match
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port != gwPort {
		return false
	}

	host = strings.ToLower(host)
	hostMatches := host == gwHost
	if !hostMatches {
		// Accept the resolved-loopback literal forms as equivalent to a
		// configured "localhost" gateway host (r4 OBS-003) — but ONLY when the
		// configured host is itself "localhost". This does not generalize to an
		// arbitrary configured host, so it can never be used to smuggle in a
		// blanket loopback allowance for a non-localhost gateway origin.
		hostMatches = gwHost == "localhost" && (host == "127.0.0.1" || host == "::1")
	}
	if !hostMatches {
		return false
	}

	// ADR-073: host:port alone is not enough — the exception exists only for
	// the preview surface, not the whole gateway origin (which includes the
	// internal REST API). Require the path to actually be under /preview/.
	return strings.HasPrefix(extractPath(rawURL), requiredGatewayOriginPathPrefix)
}

// extractPath returns rawURL's path component (query string and fragment
// stripped), or "" if rawURL has no path at all (e.g. a bare "host:port" or
// "host:port?query"). Mirrors stripURLToAuthority's manual, allocation-light
// parsing style rather than pulling in net/url for this narrow use.
func extractPath(rawURL string) string {
	u := rawURL
	if idx := strings.Index(u, "://"); idx != -1 {
		u = u[idx+3:]
	}
	idx := strings.IndexByte(u, '/')
	if idx == -1 {
		return ""
	}
	path := u[idx:]
	if qIdx := strings.IndexAny(path, "?#"); qIdx != -1 {
		path = path[:qIdx]
	}
	return path
}

// CheckIP verifies that an IP address is not in a private/reserved range.
// Returns an error if the IP is blocked, nil if it is safe.
func (sc *SSRFChecker) CheckIP(ip net.IP) error {
	sc.ensureInit()

	if ip == nil {
		return fmt.Errorf("SSRF: nil IP address rejected")
	}
	ipStr := ip.String()

	// Check exact-IP allowlist first (O(1)).
	if sc.allowList[ipStr] {
		return nil
	}

	// Check CIDR allowlist — caller has opted-in to these internal ranges.
	for _, allowNet := range sc.allowCIDRs {
		if allowNet.Contains(ip) {
			return nil
		}
		// Also check against the IPv4 form when the address is IPv4-mapped IPv6.
		if ip4 := ip.To4(); ip4 != nil && allowNet.Contains(ip4) {
			return nil
		}
	}

	// Cloud metadata endpoint (exact match — always blocked unless allowlisted above).
	if ipStr == "169.254.169.254" {
		return fmt.Errorf("SSRF: blocked cloud metadata endpoint %s", ipStr)
	}

	// Unwrap IPv4-mapped IPv6 addresses and check IPv4 ranges
	if ip4 := ip.To4(); ip4 != nil {
		for _, ipNet := range sc.ipv4Nets {
			if ipNet.Contains(ip4) {
				return fmt.Errorf("SSRF: blocked private IP range %s (%s)", ipStr, ipNet.String())
			}
		}
		return nil
	}

	// 6to4 addresses (RFC 3056 — 2002::/16): the embedded IPv4 address occupies
	// bytes [2:6] of the IPv6 address. Unwrap and re-check against IPv4 rules so
	// that e.g. 2002:7f00:0001:: (which encodes 127.0.0.1) is correctly blocked.
	if len(ip) == net.IPv6len && ip[0] == 0x20 && ip[1] == 0x02 {
		embedded4 := net.IPv4(ip[2], ip[3], ip[4], ip[5])
		if err := sc.CheckIP(embedded4); err != nil {
			return fmt.Errorf("SSRF: 6to4 address %s embeds blocked IPv4: %w", ipStr, err)
		}
		// embedded4 is safe; fall through to standard IPv6 checks below.
	}

	// Teredo addresses (RFC 4380 — 2001:0000::/32): the client's IPv4 address
	// occupies bytes [12:16], XOR-inverted with 0xFF. Unwrap and re-check.
	if len(ip) == net.IPv6len && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x00 && ip[3] == 0x00 {
		client4 := net.IPv4(ip[12]^0xff, ip[13]^0xff, ip[14]^0xff, ip[15]^0xff)
		if err := sc.CheckIP(client4); err != nil {
			return fmt.Errorf("SSRF: Teredo address %s embeds blocked client IPv4: %w", ipStr, err)
		}
		// client4 is safe; fall through to standard IPv6 checks below.
	}

	// Pure IPv6
	for _, ipNet := range sc.ipv6Nets {
		if ipNet.Contains(ip) {
			return fmt.Errorf("SSRF: blocked private IPv6 range %s (%s)", ipStr, ipNet.String())
		}
	}

	return nil
}

// CheckHost resolves a hostname and checks all resolved IPs against SSRF rules.
// This provides DNS rebinding protection (SEC-24).
//
// When a hostname is present in the allowInternal list supplied to NewSSRFChecker,
// SSRF blocking is skipped entirely for that hostname (the resolved IPs are not
// individually checked). This lets operators explicitly permit internal services
// by name (e.g. "localhost", "internal.corp") without having to enumerate IPs.
func (sc *SSRFChecker) CheckHost(ctx context.Context, host string) ([]net.IPAddr, error) {
	sc.ensureInit()

	// If host is already an IP, check it directly.
	if ip := net.ParseIP(host); ip != nil {
		if err := sc.CheckIP(ip); err != nil {
			return nil, err
		}
		return []net.IPAddr{{IP: ip}}, nil
	}

	// Check hostname allowlist (case-insensitive) before DNS resolution.
	// This lets operators allowlist by name without enumerating IPs.
	if sc.allowList[strings.ToLower(host)] {
		addrs, err := sc.resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("SSRF: DNS resolution failed for allowlisted host %s: %w", host, err)
		}
		return addrs, nil
	}

	// Resolve hostname
	addrs, err := sc.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("SSRF: DNS resolution failed for %s: %w", host, err)
	}

	if len(addrs) == 0 {
		return nil, fmt.Errorf("SSRF: no addresses found for %s", host)
	}

	// Check ALL resolved IPs
	for _, addr := range addrs {
		if err := sc.CheckIP(addr.IP); err != nil {
			return nil, fmt.Errorf("SSRF: hostname %s resolved to blocked IP %s: %w", host, addr.IP, err)
		}
	}

	return addrs, nil
}

// CheckURL validates a URL string against SSRF rules.
// Extracts the host, resolves it, and checks all resolved IPs.
func (sc *SSRFChecker) CheckURL(ctx context.Context, rawURL string) error {
	// FR-018 / ADR-044 D2: the gateway-origin exception is evaluated FIRST —
	// before host extraction and before the 127.0.0.0/8 CIDR block below —
	// so the built-in browser can reach the gateway's own preview URL. This
	// is a new, narrowly-scoped exact host:port match (see
	// isAllowedGatewayOrigin); every other loopback port, and every other
	// host, still runs the full SSRF path below.
	if sc.isAllowedGatewayOrigin(rawURL) {
		return nil
	}

	host := extractHost(rawURL)
	if host == "" {
		return fmt.Errorf("SSRF: cannot extract host from URL %q", rawURL)
	}

	_, err := sc.CheckHost(ctx, host)
	return err
}

// extractHost extracts the hostname (without port) from a URL.
func extractHost(rawURL string) string {
	host, _, _ := stripURLToAuthority(rawURL)
	return host
}

// extractHostPort extracts the literal host AND port from a URL's authority
// section, without any DNS resolution. Unlike extractHost (which discards
// the port), this preserves it — used exclusively by the gateway-origin
// exception (isAllowedGatewayOrigin), which must match on the literal,
// pre-resolution host:port token exactly as serve_web emits it (e.g.
// "localhost:5000"), before any resolution or CIDR check runs.
//
// ok is false when the URL has no explicit port (e.g. a bare
// "https://example.com" defaulting to 443) — the gateway-origin exception
// never matches a portless URL, since serve_web always emits an explicit
// port and a portless host can never be the literal token it produces.
func extractHostPort(rawURL string) (host, port string, ok bool) {
	_, authority, hadPort := stripURLToAuthority(rawURL)
	if !hadPort {
		return "", "", false
	}
	h, p, err := net.SplitHostPort(authority)
	if err != nil {
		return "", "", false
	}
	return h, p, true
}

// stripURLToAuthority strips scheme, path, and userinfo from rawURL, leaving
// just the host[:port] authority component. Returns the host-only form
// (port stripped, matching extractHost's historical behavior), the raw
// authority string (host[:port] as written, for extractHostPort), and
// whether an explicit port was present.
func stripURLToAuthority(rawURL string) (hostOnly, authority string, hadPort bool) {
	url := rawURL
	// Remove scheme
	if idx := strings.Index(url, "://"); idx != -1 {
		url = url[idx+3:]
	}
	// Remove path
	if idx := strings.IndexAny(url, "/?#"); idx != -1 {
		url = url[:idx]
	}
	// Remove userinfo
	if idx := strings.LastIndex(url, "@"); idx != -1 {
		url = url[idx+1:]
	}
	authority = url
	// Remove port
	host, _, err := net.SplitHostPort(url)
	if err != nil {
		return url, authority, false
	}
	return host, authority, true
}

// SafeTransport returns an http.Transport that enforces SSRF rules on all connections.
func (sc *SSRFChecker) SafeTransport() *http.Transport {
	return &http.Transport{
		DialContext:         sc.SafeDialContext(&net.Dialer{Timeout: 30 * time.Second}),
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// SafeDialContext returns a DialContext function that enforces SSRF rules —
// dial-time re-resolution of the target host followed by a CheckIP pass over
// every candidate address — using the given dialer's timeout/keep-alive
// settings. Re-resolving at connect time (rather than trusting a pre-flight
// resolution) closes the DNS-rebinding TOCTOU window where a hostname
// resolves to a public IP during an earlier check but a private IP by the
// time the connection is actually made.
//
// This exists as its own method (rather than being inlined into
// SafeTransport) so callers that need their own *http.Transport — e.g. one
// already configured for an upstream proxy, or with non-default dial
// timeouts — can reuse the exact same TOCTOU-safe SSRF enforcement instead of
// hand-rolling dial-time DNS-rebinding protection themselves. SafeTransport
// is a thin convenience wrapper around this method with a default 30s dial
// timeout and no keep-alive override.
//
// dialer must not be nil. A nil dialer is guarded against below rather than
// left to panic deep inside the returned closure the first time it's
// invoked — the same defensive treatment given to WebFetchTool.Execute's
// nil-ssrf guard (pkg/tools/web.go) during the same hardening pass. The
// check runs once here, at SafeDialContext's own call site, rather than on
// every dial: a nil dialer is a caller wiring bug, not a per-request
// condition, so every invocation of the returned closure can return the
// same clear error immediately instead of repeating the check after doing
// DNS resolution work first.
func (sc *SSRFChecker) SafeDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	if dialer == nil {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, fmt.Errorf("SSRF: SafeDialContext called with nil dialer")
		}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("SSRF: invalid address %q: %w", addr, err)
		}

		addrs, err := sc.CheckHost(ctx, host)
		if err != nil {
			return nil, err
		}

		var lastErr error
		for _, resolved := range addrs {
			target := net.JoinHostPort(resolved.IP.String(), port)
			conn, dialErr := dialer.DialContext(ctx, network, target)
			if dialErr != nil {
				lastErr = dialErr
				continue
			}
			return conn, nil
		}

		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("SSRF: no connectable address for %s", host)
	}
}

// CheckRedirect validates redirect targets against SSRF rules before following them.
func (sc *SSRFChecker) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("SSRF: too many redirects")
	}
	return sc.CheckURL(req.Context(), req.URL.String())
}

// SafeClient returns an http.Client configured with SSRF protection.
func (sc *SSRFChecker) SafeClient() *http.Client {
	return &http.Client{
		Transport:     sc.SafeTransport(),
		CheckRedirect: sc.CheckRedirect,
		Timeout:       30 * time.Second,
	}
}
