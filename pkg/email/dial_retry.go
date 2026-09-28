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
	dnsRetryAttempts     = 3
	dnsRetryBaseDelay    = 250 * time.Millisecond
	addressFallbackDelay = 300 * time.Millisecond
)

// dialResolver is the package DNS seam (round-8 F2): the resolver the
// production dial paths consult. Tests stub it to pin the bounded DNS retry
// at the production entry points (FR-037/MC-33/B-42) without real DNS.
var dialResolver Resolver = net.DefaultResolver

var dialTCPContext = func(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
}

// resolveHostBounded resolves host with the MC-33 bounded DNS-retry policy:
// at most dnsRetryAttempts lookups, dnsRetryBaseDelay << (attempt-1) between
// attempts (250 ms then 500 ms), the caller's context bounding the whole
// step. IP-literal hosts pass through with zero lookups. It returns every
// resolved address in resolver order so production dials can fall back across
// them without re-resolving, while TLS ServerName keeps the ORIGINAL hostname
// (owned by the caller's TLS config, not by this step).
func resolveHostBounded(ctx context.Context, resolver Resolver, host string) ([]string, error) {
	if isIPLiteral(host) {
		return []string{host}, nil
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
				return nil, cerr
			}
			lastErr = err
			if attempt < dnsRetryAttempts {
				if serr := sleepCtx(ctx, dnsRetryBaseDelay<<(attempt-1)); serr != nil {
					return nil, serr
				}
				continue
			}
			return nil, fmt.Errorf("email transport: resolve %s: %w", host, lastErr)
		}
		return addrs, nil
	}
	// Unreachable (the loop returns on every branch).
	return nil, fmt.Errorf("email transport: resolve %s: %w", host, lastErr)
}

// dialTCPDNSRetry is the production TCP dial edge (round-8 F2, FR-037): it
// resolves addr's host part through resolver with the bounded MC-33 policy
// and dials the RESOLVED addresses in order, bounded by one dialTimeout and
// ctx. A name-resolution failure surfaces a dns-class error and spends no
// dial attempt; a non-DNS dial failure (refused, refused-after-resolution) is
// not re-resolved — resolution ran exactly once (or never, for IP literals).
func dialTCPDNSRetry(ctx context.Context, resolver Resolver, addr string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	targets := []string{addr}
	host, port, sperr := net.SplitHostPort(addr)
	if sperr == nil && !isIPLiteral(host) {
		resolved, rerr := resolveHostBounded(dialCtx, resolver, host)
		if rerr != nil {
			return nil, rerr
		}
		targets = make([]string, 0, len(resolved))
		for _, resolvedAddr := range resolved {
			targets = append(targets, net.JoinHostPort(resolvedAddr, port))
		}
	}

	return dialAddressCandidates(dialCtx, targets, "dial", dialTCPContext, func(conn net.Conn) {
		_ = conn.Close()
	})
}

type addressDialResult[T any] struct {
	index int
	value T
	err   error
}

// dialAddressCandidates starts resolved addresses in resolver order, staggered
// by net.Dialer's 300 ms fallback cadence. Earlier attempts remain live while
// later fallbacks start, so a slow-but-valid primary can still win. Every
// attempt shares ctx; after one succeeds, all losing successes are closed.
func dialAddressCandidates[T any](
	ctx context.Context,
	targets []string,
	label string,
	dial func(context.Context, string) (T, error),
	closeValue func(T),
) (T, error) {
	var zero T
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan addressDialResult[T], len(targets))
	for i, target := range targets {
		go func() {
			if i > 0 {
				timer := time.NewTimer(time.Duration(i) * addressFallbackDelay)
				defer timer.Stop()
				select {
				case <-runCtx.Done():
					results <- addressDialResult[T]{index: i, err: runCtx.Err()}
					return
				case <-timer.C:
				}
			}

			value, err := dial(runCtx, target)
			if err != nil {
				err = fmt.Errorf("email transport: %s %s: %w", label, target, err)
			}
			results <- addressDialResult[T]{index: i, value: value, err: err}
		}()
	}

	errs := make([]error, len(targets))
	ctxDone := ctx.Done()
	expired := false
	for received := 0; received < len(targets); {
		select {
		case <-ctxDone:
			expired = true
			cancel()
			ctxDone = nil
		case result := <-results:
			received++
			if result.err != nil {
				errs[result.index] = result.err
				continue
			}
			if ctx.Err() != nil {
				closeValue(result.value)
				expired = true
				cancel()
				ctxDone = nil
				continue
			}

			cancel()
			for received < len(targets) {
				loser := <-results
				received++
				if loser.err == nil {
					closeValue(loser.value)
				}
			}
			return result.value, nil
		}
	}
	if expired {
		return zero, ctx.Err()
	}
	return zero, errs[len(errs)-1]
}

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
// cannot drift — then ordered dials of the RESOLVED addresses built from
// addr's port.
func DialWithRetry(ctx context.Context, resolver Resolver, host, addr string) (net.Conn, error) {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		addr = net.JoinHostPort(host, port)
	}
	return dialTCPDNSRetry(ctx, resolver, addr)
}
