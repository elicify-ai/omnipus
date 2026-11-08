// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package common

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// Provider endpoint guard (hosted edition).
//
// On a hosted tenant every outbound call to a provider endpoint — the REST
// key checks and model listings, and the run-time provider clients — must
// refuse loopback, private and link-local targets whatever sandbox.ssrf says:
// that setting defaults off, and a tenant's engine shares a private network
// with the platform. The guard resolves the host at DIAL time and checks
// every resolved address (security.SSRFChecker.SafeDialContext), so a name
// that resolves public during a check and private at connect time (DNS
// rebinding) is still refused.
//
// The only exemptions are the operator's own allow list — the same
// sandbox.ssrf.allow_internal + sandbox.egress_allow_cidrs pair the agent
// loop's SSRF checker merges — installed by SetEndpointAllowlist from the
// loaded config. Every other edition gets a nil guard and keeps its existing
// behaviour (sandbox.ssrf decides).

type endpointGuardState struct {
	entries []string
	checker *security.SSRFChecker
}

var (
	endpointGuardInstalled atomic.Pointer[endpointGuardState] //nolint:gochecknoglobals // boot-installed allow list
	endpointGuardStrict    = &endpointGuardState{checker: security.NewSSRFChecker(nil)}
)

// EndpointGuardActive reports whether this build enforces the provider
// endpoint guard: the hosted edition, decided by the stamped edition and
// never by a config key.
func EndpointGuardActive() bool {
	return config.Edition == config.EditionHosted
}

// SetEndpointAllowlist installs the operator's SSRF allow list (exact IPs,
// CIDRs and host names) the guard honours. The gateway calls it with
// sandbox.ssrf.allow_internal + sandbox.egress_allow_cidrs every time it
// builds the startup provider (boot and reload). nil or empty restores the
// strict default (no exemptions).
func SetEndpointAllowlist(entries []string) {
	clean := make([]string, 0, len(entries))
	for _, e := range entries {
		if e = strings.TrimSpace(e); e != "" {
			clean = append(clean, e)
		}
	}
	if len(clean) == 0 {
		endpointGuardInstalled.Store(nil)
		return
	}
	endpointGuardInstalled.Store(&endpointGuardState{entries: clean, checker: security.NewSSRFChecker(clean)})
}

func currentEndpointGuard() *endpointGuardState {
	if st := endpointGuardInstalled.Load(); st != nil {
		return st
	}
	return endpointGuardStrict
}

// EndpointGuard returns the SSRF checker every provider endpoint must pass on
// this build, or nil when the build does not enforce one (every edition but
// hosted).
func EndpointGuard() *security.SSRFChecker {
	if !EndpointGuardActive() {
		return nil
	}
	return currentEndpointGuard().checker
}

// EndpointExplicitlyAllowed reports whether rawURL's host is named by the
// installed allow list itself — an exact host name or IP entry, or an IP
// literal inside a CIDR entry. No DNS lookup: an operator exempts a target by
// naming it, never by what some name happens to resolve to.
func EndpointExplicitlyAllowed(rawURL string) bool {
	st := endpointGuardInstalled.Load()
	if st == nil {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	for _, entry := range st.entries {
		if _, cidr, cerr := net.ParseCIDR(entry); cerr == nil {
			if ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		if eip := net.ParseIP(entry); eip != nil {
			if ip != nil && eip.Equal(ip) {
				return true
			}
			continue
		}
		if strings.EqualFold(entry, host) {
			return true
		}
	}
	return false
}

// CatalogBaseOverrideRefused reports whether an api_base override on a
// CATALOG provider id must be refused (on a write) or ignored (on use): the
// guard is active, a base is set, and the operator's allow list does not
// name its host. A catalog id's endpoint is the catalog's; on a hosted tenant
// pointing it elsewhere is what lets a stranger receive the owner's key.
func CatalogBaseOverrideRefused(apiBase string) bool {
	return EndpointGuardActive() && strings.TrimSpace(apiBase) != "" && !EndpointExplicitlyAllowed(apiBase)
}

// GuardTransport makes t dial only addresses the endpoint guard accepts,
// re-resolving and checking the host on every dial. A no-op when the guard is
// not active.
func GuardTransport(t *http.Transport) {
	g := EndpointGuard()
	if g == nil || t == nil {
		return
	}
	t.DialContext = g.SafeDialContext(&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second})
}

// GuardedHTTPClient returns a client whose transport is a clone of
// http.DefaultTransport behind GuardTransport, or nil when the guard is not
// active (callers then keep their own default client).
func GuardedHTTPClient() *http.Client {
	if EndpointGuard() == nil {
		return nil
	}
	var t *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		t = base.Clone()
	} else {
		t = &http.Transport{}
	}
	GuardTransport(t)
	return &http.Client{Transport: t}
}
