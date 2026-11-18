package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// #1823 review: a bare IPv6 gateway.host must give a bracketed URL authority.
func TestResolveMainOrigin_IPv6Host_1823(t *testing.T) {
	for host, want := range map[string]string{
		"::1":         "http://[::1]:5000",
		"[::1]":       "http://[::1]:5000",
		"2001:db8::1": "http://[2001:db8::1]:5000",
		"127.0.0.1":   "http://127.0.0.1:5000",
	} {
		cfg := &config.Config{Gateway: config.GatewayConfig{Host: host, Port: 5000}}
		if got := resolveMainOrigin(cfg); got != want {
			t.Errorf("host %q: %q, want %q", host, got, want)
		}
	}
}
