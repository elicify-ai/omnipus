// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import "github.com/dapicom-ai/omnipus/pkg/config"

// blockedPaths lists dotted configuration paths that the generic
// PUT /api/v1/config endpoint must refuse to mutate at any nesting depth.
// Each entry must be routed through its dedicated endpoint so that policy
// validation, admin-only guards, and audit logging are applied.
//
// This replaces the former flat blockedKeys map that only matched TOP-LEVEL
// keys. The former shape allowed an attacker holding an admin token to ship
//
//	PUT /api/v1/config {"gateway":{"users":[...new admin...]}}
//
// and win the whole deployment, because "gateway.users" is one level below
// the "gateway" top-level key that the flat map guarded.
//
//nolint:unused // used in rest.go (!cgo build); lint with goolm,stdjson excludes !cgo files
var blockedPaths = []config.ConfigKey{
	"sandbox",
	"credentials",
	"security",
	config.GatewayUsers,
	config.GatewayDevModeBypass,
}

// matchBlockedPath reports whether body contains any entry in blocked at any
// nesting depth. It returns the first blocked path that matches, in the order
// of the blocked slice.
//
// Two body shapes are handled:
//
//  1. Nested objects — e.g. {"gateway":{"users":[...]}} matches "gateway.users".
//     The walker descends into map[string]any children, building a dotted
//     path, and checks whether that path is blocked (either exactly or as
//     a prefix of a blocked path's ancestor — e.g. {"gateway":{}} matches
//     nothing, but {"sandbox":{}} matches the top-level "sandbox" entry).
//
//  2. Dot-path literal keys — e.g. {"gateway.users":[...]} matches
//     "gateway.users". Callers that build request bodies with dotted keys
//     instead of nested objects must not be able to bypass the walker.
//
// A blocked path matches when any path reachable in body equals that blocked
// path exactly. A blocked path with ancestors (e.g. "gateway.users") also
// matches when the request nests the ancestor and sets the leaf
// (body["gateway"]["users"]).
//
//nolint:unused // used in rest.go (!cgo build); lint with goolm,stdjson excludes !cgo files
func matchBlockedPath(body map[string]any, blocked []config.ConfigKey) (string, bool) {
	if len(body) == 0 || len(blocked) == 0 {
		return "", false
	}
	// Collect every dotted path present in body. This handles both nested
	// objects (recursive walk) and dot-path literal keys (leaf keys with
	// dots are emitted verbatim as part of the path).
	present := collectPaths(body)
	for _, bp := range blocked {
		if _, ok := present[string(bp)]; ok {
			return string(bp), true
		}
	}
	return "", false
}

// collectPaths walks body and returns the set of dotted paths it contains.
// Each leaf and each intermediate map key contributes a path. Keys that
// themselves contain dots (dot-path literals) are treated as already-dotted
// paths and are merged with any prefix from their ancestors.
//
//nolint:unused // used in matchBlockedPath; rest.go (!cgo build) is excluded by lint with goolm,stdjson
func collectPaths(body map[string]any) map[string]struct{} {
	out := make(map[string]struct{})
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if prefix != "" {
			out[prefix] = struct{}{}
		}
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, child := range m {
			next := k
			if prefix != "" {
				next = prefix + "." + k
			}
			// Keys that already contain dots are treated as dotted paths —
			// merging with any ancestor prefix so {"gateway":{"users.role":...}}
			// is recorded as "gateway.users.role" (no special-case needed;
			// standard string concatenation does the right thing).
			walk(next, child)
		}
	}
	walk("", body)
	return out
}
