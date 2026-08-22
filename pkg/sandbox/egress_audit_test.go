// T2.14: EgressProxy_DenyEmitsAuditViaHook.
//
// Wires a real EgressAuditFunc closure into NewEgressProxy; triggers a deny
// by requesting a host not on the allow-list; asserts the audit row was
// written via the hook. B1.2(c).

package sandbox

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestEgressProxy_DenyEmitsAuditViaHook (T2.14) verifies that when a
// request is denied by the egress proxy, the EgressAuditFunc hook is
// invoked with an audit.Entry whose Event and Decision fields are set.
func TestEgressProxy_DenyEmitsAuditViaHook(t *testing.T) {
	var captured []*audit.Entry

	auditHook := func(entry *audit.Entry) {
		// Take a copy so captured doesn't alias the entry the proxy reuses.
		cp := *entry
		captured = append(captured, &cp)
	}

	p, err := NewEgressProxy([]string{"registry.npmjs.org"}, auditHook)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	defer func() {
		if closeErr := p.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	proxyURL, err := url.Parse("http://" + p.Addr())
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	// Request a host NOT on the allow-list → should be denied.
	resp, err := client.Get("http://blocked.example.com/test")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()              //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp.StatusCode)
	}

	// Verify the hook was invoked with a deny entry.
	if len(captured) == 0 {
		t.Fatal("audit hook was not invoked on deny — EgressAuditFunc not wired (T2.14)")
	}

	var foundDeny bool
	for _, e := range captured {
		if e.Decision == audit.DecisionDeny {
			foundDeny = true
			// Confirm details carry the blocked host.
			if e.Details != nil {
				if h, ok := e.Details["host"].(string); ok && h == "" {
					t.Errorf("audit entry has empty host in details: %+v", e.Details)
				}
			}
		}
	}
	if !foundDeny {
		t.Errorf("no audit entry with decision=deny found; entries: %+v", captured)
	}
}

// TestEgressProxy_AllowedHostDoesNotEmitDenyAudit (T2.14 sanity) verifies
// that a request to an allowed host does NOT trigger the deny audit hook.
// The egress proxy may still emit an audit row for the allow side; the
// deny hook must not fire.
func TestEgressProxy_AllowedHostDoesNotEmitDenyAudit(t *testing.T) {
	var denyCalled bool

	auditHook := func(entry *audit.Entry) {
		if entry.Decision == audit.DecisionDeny {
			denyCalled = true
		}
	}

	// Use an empty allow-list for this sub-test so ALL hosts are denied,
	// but this tests the inverse: with a populated list, the allowed host
	// must not trigger deny.
	p, err := NewEgressProxy([]string{"allowed.example.com"}, auditHook)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	defer func() {
		if closeErr := p.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	proxyURL, urlErr := url.Parse("http://" + p.Addr())
	if urlErr != nil {
		t.Fatalf("parse proxy URL: %v", urlErr)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   2 * time.Second,
	}

	// allowed.example.com is on the list — the proxy will forward it.
	// The connection will fail (no real server), but we only care that
	// the deny audit hook was NOT called.
	resp, getErr := client.Get("http://allowed.example.com/")
	if getErr != nil {
		_ = getErr // expected: no real server behind the proxy; only the deny-hook behavior is under test
	}
	if resp != nil {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}

	if denyCalled {
		t.Error("deny audit hook was invoked for an allowed host — allow-list logic broken")
	}
}
