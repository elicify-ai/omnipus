// This file implements SEC-24 (SSRF protection) from the Omnipus BRD:
// blocking outbound HTTP requests to private/internal IP ranges, cloud metadata
// endpoints, and providing DNS rebinding protection.

package security

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
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
type SSRFChecker struct {
	ipv4Nets   []*net.IPNet
	ipv6Nets   []*net.IPNet
	allowList  map[string]bool // Allowlisted exact IPs and hostnames
	allowCIDRs []*net.IPNet    // Allowlisted CIDR ranges
	resolver   Resolver        // DNS resolver (injectable for testing)
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

// CheckIP verifies that an IP address is not in a private/reserved range.
// Returns an error if the IP is blocked, nil if it is safe.
func (sc *SSRFChecker) CheckIP(ip net.IP) error {
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
	host := extractHost(rawURL)
	if host == "" {
		return fmt.Errorf("SSRF: cannot extract host from URL %q", rawURL)
	}

	_, err := sc.CheckHost(ctx, host)
	return err
}

// extractHost extracts the hostname (without port) from a URL.
func extractHost(rawURL string) string {
	// Remove scheme
	url := rawURL
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
	// Remove port
	host, _, err := net.SplitHostPort(url)
	if err != nil {
		return url
	}
	return host
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
// dialer must not be nil.
func (sc *SSRFChecker) SafeDialContext(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
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
