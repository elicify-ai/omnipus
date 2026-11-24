// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package testseam holds the test-build-only observation and fault points
// that the connected-programs (#1960 S1) acceptance tests use on real product
// paths (seam requests H43, H44, H45).
//
// A normal build (no build tag) compiles only seams_off.go: every function is
// an empty pass-through (the input value comes straight back), so a release
// binary has no observation, no fault injection, and no way to switch either
// on. The registration API in seams_on.go exists only when the binary is built
// with the build tag "s1seams" (go test -tags goolm,stdjson,s1seams ...).
//
// No seam ever receives a secret: the egress-token seam reports only that a
// mint started and which kind; the capture seam sees the bytes that enter the
// foreground output buffer, which programs.Attach has already masked; the
// pipe-read seam sees only a byte count.
package testseam

// Egress-token kinds reported to EgressTokenMinted.
const (
	// EgressTokenOnce is a single-use run token (tools.newOnceEgressToken).
	EgressTokenOnce = "once"
	// EgressTokenSession is a session-scoped token, minted the first time a
	// session needs one (security.ApprovalGrantStore.SessionEgressToken).
	EgressTokenSession = "session"
)
