package email

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Resolver is the DNS edge for bounded retry. net.Resolver satisfies it.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

const (
	dnsRetryAttempts  = 3
	dnsRetryBaseDelay = 250 * time.Millisecond
)

// dialResolver is the package DNS seam (round-8 F2): the resolver the
// production dial paths consult. Tests stub it to pin the bounded DNS retry
// at the production entry points (FR-037/MC-33/B-42) without real DNS.
var dialResolver Resolver = net.DefaultResolver

// resolveHostBounded resolves host with the MC-33 bounded DNS-retry policy:
// at most dnsRetryAttempts lookups, dnsRetryBaseDelay << (attempt-1) between
// attempts (250 ms then 500 ms), the caller's context bounding the whole
// step. IP-literal hosts pass through with zero lookups. It returns the
// first resolved address — production dials dial the RESOLVED address (the
// dial itself never re-resolves), while TLS ServerName keeps the ORIGINAL
// hostname (owned by the caller's TLS config, not by this step).
func resolveHostBounded(ctx context.Context, resolver Resolver, host string) (string, error) {
	if isIPLiteral(host) {
		return host, nil
	}
	var lastErr error
	for attempt := 1; attempt <= dnsRetryAttempts; attempt++ {
		addrs, err := resolver.LookupHost(ctx, host)
		if err == nil && len(addrs) == 0 {
			// A name that resolves to nothing is a resolution failure like
			// any other — retry it on the same ladder.
			err = fmt.Errorf("no addresses for %s", host)
		}
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return "", cerr
			}
			lastErr = err
			if attempt < dnsRetryAttempts {
				if serr := sleepCtx(ctx, dnsRetryBaseDelay<<(attempt-1)); serr != nil {
					return "", serr
				}
				continue
			}
			return "", fmt.Errorf("email transport: resolve %s: %w", host, lastErr)
		}
		return addrs[0], nil
	}
	// Unreachable (the loop returns on every branch).
	return "", fmt.Errorf("email transport: resolve %s: %w", host, lastErr)
}

// dialTCPDNSRetry is the production TCP dial edge (round-8 F2, FR-037): it
// resolves addr's host part through resolver with the bounded MC-33 policy
// and dials the RESOLVED address, bounded by dialTimeout and ctx. A
// name-resolution failure surfaces a dns-class error and spends no dial
// attempt; a non-DNS dial failure (refused, refused-after-resolution) is NOT
// retried — resolution ran exactly once (or never, for IP literals).
func dialTCPDNSRetry(ctx context.Context, resolver Resolver, addr string) (net.Conn, error) {
	host, port, sperr := net.SplitHostPort(addr)
	if sperr == nil && !isIPLiteral(host) {
		resolved, rerr := resolveHostBounded(ctx, resolver, host)
		if rerr != nil {
			return nil, rerr
		}
		addr = net.JoinHostPort(resolved, port)
	}
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("email transport: dial %s: %w", addr, err)
	}
	return conn, nil
}

// DialWithRetry is the spec-level DNS-retry dial (MC-33/B-42): bounded
// resolution of host (3 lookups, 250/500 ms) through the passed resolver —
// the SAME resolveHostBounded core, constants and ladder as the production
// dial paths (dialSMTPRaw, dialIMAP), so the spec oracle and production
// cannot drift — then a dial of the RESOLVED address built from addr's port.
// sleepCtx sleeps d under ctx: returns ctx's error when the context ends
// first (a retry ladder cannot outlive its bound).
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// isIPLiteral reports whether host is an IP literal (v4 or v6 form, with
// brackets stripped) — a dial target that needs no name resolution.
func isIPLiteral(host string) bool {
	h := strings.Trim(host, "[]")
	return net.ParseIP(h) != nil
}

// DialWithRetry is the spec-level DNS-retry dial (MC-33/B-42): bounded
// resolution of host (3 lookups, 250/500 ms) through the passed resolver —
// the SAME resolveHostBounded core, constants and ladder as the production
// dial paths (dialSMTPRaw, dialIMAP), so the spec oracle and production
// cannot drift — then a dial of the RESOLVED address built from addr's port.
func DialWithRetry(ctx context.Context, resolver Resolver, host, addr string) (net.Conn, error) {
	resolved, err := resolveHostBounded(ctx, resolver, host)
	if err != nil {
		return nil, err
	}
	if resolved != host {
		if _, port, perr := net.SplitHostPort(addr); perr == nil {
			addr = net.JoinHostPort(resolved, port)
		}
	}
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("email transport: dial %s: %w", addr, err)
	}
	return conn, nil
}
