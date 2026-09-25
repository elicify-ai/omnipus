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

// DialWithRetry dials addr over TCP, retrying only name-resolution failures
// for host: 3 lookups with 250 ms then 500 ms between attempts (MC-33), IP
// literals skip resolution. The dial is bounded by dialTimeout and ctx.
func DialWithRetry(ctx context.Context, resolver Resolver, host, addr string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !isIPLiteral(host) {
		var lastErr error
		for attempt := 1; attempt <= dnsRetryAttempts; attempt++ {
			if _, err := resolver.LookupHost(ctx, host); err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
				lastErr = err
				if attempt < dnsRetryAttempts {
					delay := dnsRetryBaseDelay << (attempt - 1)
					if serr := sleepCtx(ctx, delay); serr != nil {
						return nil, serr
					}
					continue
				}
				return nil, fmt.Errorf("email transport: resolve %s: %w", host, lastErr)
			}
			break
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

// isIPLiteral reports whether host is an IP address literal (v4 or v6 form,
// with brackets stripped).
func isIPLiteral(host string) bool {
	h := strings.Trim(host, "[]")
	return net.ParseIP(h) != nil
}
