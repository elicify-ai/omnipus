// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

import "testing"

// #1516: IsAllowlistedHost answers the same exact-name lookup CheckHost uses,
// so fetch_url's no-DNS pre-check can honour an operator-listed host name.
func TestSSRFChecker_IsAllowlistedHost(t *testing.T) {
	sc := NewSSRFChecker([]string{"LocalHost", "internal.corp", "127.0.0.2", "10.0.0.0/8"})
	cases := map[string]bool{
		"localhost":     true,
		"LOCALHOST.":    true,
		"internal.corp": true,
		"other.corp":    false,
		"sub.localhost": false,
		"127.0.0.2":     false, // IPs are CheckIP's job, never a host-name exemption
		"":              false,
	}
	for host, want := range cases {
		if got := sc.IsAllowlistedHost(host); got != want {
			t.Errorf("IsAllowlistedHost(%q) = %v, want %v", host, got, want)
		}
	}
	var nilChecker *SSRFChecker
	if nilChecker.IsAllowlistedHost("localhost") {
		t.Error("nil checker must report false")
	}
	var zero SSRFChecker
	if zero.IsAllowlistedHost("localhost") {
		t.Error("zero-value checker must report false")
	}
}
