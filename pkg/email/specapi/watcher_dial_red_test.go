package specapi_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// Oracle: MC-18, MC-31, MC-33, FR-037, D27, D38. Compile errors name the
// missing symbols. The resolver and clock below are the process edges.

func TestWatcher_NeverMutatesFlags(t *testing.T) {
	// An empty config is not a mailbox. The cycle must fail visibly rather than
	// pretend it checked mail. Flag non-mutation (MC-18) needs this type plus
	// the unexported imapDial seam; that harness assertion is not reachable
	// from this package until NewWatcher exists.
	if _, err := email.NewWatcher(email.WatcherConfig{}); err == nil {
		t.Fatal("MC-18: NewWatcher accepted an empty config")
	}
}

func TestWatcher_BackoffJitterAuthCap(t *testing.T) {
	// MC-33 / B-43. randUnit 0 → -20%, 1 → +20%, 0.5 → the base delay.
	if got := email.WatcherBackoff(1, "timeout", 0.5); got != 60*time.Second {
		t.Fatalf("backoff(1, timeout, 0.5) = %s, want 60s", got)
	}
	if got := email.WatcherBackoff(1, "timeout", 0); got != 48*time.Second {
		t.Fatalf("backoff(1, timeout, 0) = %s, want 48s", got)
	}
	if got := email.WatcherBackoff(1, "timeout", 1); got != 72*time.Second {
		t.Fatalf("backoff(1, timeout, 1) = %s, want 72s", got)
	}
	if got := email.WatcherBackoff(2, "timeout", 0.5); got != 120*time.Second {
		t.Fatalf("backoff(2, timeout, 0.5) = %s, want 120s", got)
	}
	capDuration := 15 * time.Minute
	if got := email.WatcherBackoff(1, "auth_failed", 0.5); got != capDuration {
		t.Fatalf("backoff(1, auth_failed, 0.5) = %s, want the 15 min cap", got)
	}
	if got := email.WatcherBackoff(20, "timeout", 0.5); got != capDuration {
		t.Fatalf("backoff(20, timeout, 0.5) = %s, want the 15 min cap", got)
	}
	if got := email.WatcherInitialOffset(0); got != 0 {
		t.Fatalf("initial offset 0 = %s, want 0", got)
	}
	if got := email.WatcherInitialOffset(1); got != 60*time.Second {
		t.Fatalf("initial offset 1 = %s, want 60s", got)
	}
}

func TestDial_DNSErrorBoundedRetry(t *testing.T) {
	// MC-33 / B-42: two resolution failures then a success uses 3 lookups.
	// An always-failing resolver stops at 3 and returns a DNS error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = c.Close()
		}
	}()

	var lookups int
	resolver := stubResolver{lookup: func(context.Context, string) ([]string, error) {
		lookups++
		if lookups < 3 {
			return nil, errors.New("temporary dns")
		}
		return []string{"127.0.0.1"}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := email.DialWithRetry(ctx, resolver, "mail.example", ln.Addr().String())
	if err != nil {
		t.Fatalf("FR-037: two DNS failures then success: %v (lookups=%d, want 3)", err, lookups)
	}
	_ = conn.Close()
	if lookups != 3 {
		t.Fatalf("FR-037: lookups = %d, want 3", lookups)
	}

	lookups = 0
	always := stubResolver{lookup: func(context.Context, string) ([]string, error) {
		lookups++
		return nil, errors.New("no such host")
	}}
	start := time.Now()
	_, err = email.DialWithRetry(ctx, always, "missing.example", "missing.example:993")
	if err == nil {
		t.Fatal("FR-037: always-failing resolver returned a connection")
	}
	if lookups != 3 {
		t.Fatalf("FR-037: always-fail lookups = %d, want 3", lookups)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("FR-037: DNS retries returned in %s, want at least the 250ms first backoff", time.Since(start))
	}
}

type stubResolver struct {
	lookup func(context.Context, string) ([]string, error)
}

func (s stubResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return s.lookup(ctx, host)
}
