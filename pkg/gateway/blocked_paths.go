// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
)

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

var blockedPaths = []config.ConfigKey{
	"sandbox",
	"credentials",
	"security",
	config.GatewayUsers,
	config.GatewayDevModeBypass,
	// ADR-054 §11 checklist item 7: block agents.list SPECIFICALLY, not the
	// whole "agents" key. agents.defaults (a SETTING, D1 — including
	// agents.defaults.default_agent_id, D6.4) must remain writable via this
	// endpoint; blocking the "agents" ancestor would make
	// {"agents":{"defaults":{...}}} match matchBlockedPath's ancestor rule
	// and reject every agents.defaults write too. Agent CRUD now goes
	// exclusively through the agent store / dedicated /api/v1/agents
	// endpoints.
	config.AgentsList,
	// #904 D8/D15/D21: the global tool-iteration limit and its env-import
	// marker have one write path, PUT /api/v1/performance (step-up re-auth,
	// D11/D16 lowering consent, audit). agents.defaults itself stays
	// writable; updateConfig preserves these two leaves across its
	// one-level merge (preserveProtectedAgentDefaults).
	config.AgentsDefaultsMaxToolIterations,
	config.AgentsDefaultsMaxToolIterationsEnvImported,
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

func matchBlockedPath(body map[string]any, blocked []config.ConfigKey) (string, bool) {
	if len(body) == 0 || len(blocked) == 0 {
		return "", false
	}
	// Collect every dotted path present in body. This handles both nested
	// objects (recursive walk) and dot-path literal keys (leaf keys with
	// dots are emitted verbatim as part of the path).
	//
	// Matching folds case EXACTLY the way encoding/json does: config.json is
	// decoded with encoding/json, which binds an object key to a struct field
	// when bytes.EqualFold says they are equal — Unicode simple folding, not
	// just ASCII. So {"agents":{"defaults":{"MAX_TOOL_ITERATIONS":5}}} and
	// {"gateway":{"uſers":[…]}} (U+017F LONG S ≡ 's') reach the same field as
	// the plain spelling. strings.ToLower alone missed the second form (#904
	// gate round 2, security-lead N1). collectPaths folds every path it
	// records with jsonFoldKey; the blocked entry is folded the same way.
	present := collectPaths(body)
	for _, bp := range blocked {
		if _, ok := present[jsonFoldKey(string(bp))]; ok {
			return string(bp), true
		}
	}
	return "", false
}

// collectPaths walks body and returns the set of dotted paths it contains,
// folded with jsonFoldKey (see matchBlockedPath for why matching folds case).
// Each leaf and each intermediate map key contributes a path. Keys that
// themselves contain dots (dot-path literals) are treated as already-dotted
// paths and are merged with any prefix from their ancestors.
//
// KNOWN LIMIT, stated because a security walker's gaps must not be discovered.
// A dot-containing key is recorded as ONE already-joined path and is never
// split, so its own prefixes are not emitted: {"security.platform_auth.keys":…}
// yields exactly "security.platform_auth.keys" and therefore does NOT match the
// blocked ancestor "security". A dotted key that IS a blocked entry
// ({"gateway.users": …}) still matches, because that match is exact; the gap is
// only for a literal deeper than a blocked entry.
//
// It is not a way past the gate today, and the reason is outside this file:
// PUT /api/v1/config's mutator writes such a key VERBATIM, so it becomes a
// top-level config.json member with a dot in its name and unmarshals onto no
// field of config.Config at all. Make that mutator dot-aware and this becomes a
// real hole. pkg/gateway/rest_platform_auth_test.go's
// TestTrustAnchor_DotPathLiteralDeeperThanABlockedEntry pins both halves.
//

func collectPaths(body map[string]any) map[string]struct{} {
	out := make(map[string]struct{})
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if prefix != "" {
			out[jsonFoldKey(prefix)] = struct{}{}
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
			// merging with any ancestor prefix so {"gateway":{"users.username":...}}
			// is recorded as "gateway.users.username" (no special-case needed;
			// standard string concatenation does the right thing).
			walk(next, child)
		}
	}
	walk("", body)
	return out
}

// jsonFoldKey folds s so that jsonFoldKey(x) == jsonFoldKey(y) exactly when
// bytes.EqualFold(x, y) — the equivalence encoding/json uses to bind an object
// key to a struct field (encoding/json/fold.go::appendFoldedName): ASCII
// letters are upper-cased, every other rune maps to the smallest rune of its
// Unicode simple-fold orbit. A '.' is ASCII and never folds, so folding a
// dotted path folds each segment independently.
func jsonFoldKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < utf8.RuneSelf {
			if 'a' <= r && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			continue
		}
		b.WriteRune(foldRuneLikeJSON(r))
	}
	return b.String()
}

// foldRuneLikeJSON returns the smallest rune in r's simple-fold orbit
// (encoding/json/fold.go::foldRune).
func foldRuneLikeJSON(r rune) rune {
	for {
		r2 := unicode.SimpleFold(r)
		if r2 <= r {
			return r2
		}
		r = r2
	}
}

// nonASCIIKeyNearBlockedPath reports the first object key containing a
// non-ASCII rune that sits in a map holding a blocked path — the body root
// or any ancestor of a blocked entry (gateway, agents, agents.defaults, …).
// Every config.Config key is ASCII, so such a key there is either garbage or
// an attempt to reach a protected field through a case-folding quirk; it is
// refused outright, independent of jsonFoldKey (defence in depth).
func nonASCIIKeyNearBlockedPath(body map[string]any, blocked []config.ConfigKey) (string, bool) {
	ancestors := map[string]struct{}{"": {}}
	for _, bp := range blocked {
		p := string(bp)
		for i := strings.IndexByte(p, '.'); i >= 0; i = nextDot(p, i) {
			ancestors[jsonFoldKey(p[:i])] = struct{}{}
		}
	}
	var found string
	var walk func(prefix string, m map[string]any) bool
	walk = func(prefix string, m map[string]any) bool {
		_, protected := ancestors[jsonFoldKey(prefix)]
		for k, child := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if protected && !isASCII(k) {
				found = path
				return true
			}
			if cm, ok := child.(map[string]any); ok && walk(path, cm) {
				return true
			}
		}
		return false
	}
	if walk("", body) {
		return found, true
	}
	return "", false
}

func nextDot(p string, i int) int {
	j := strings.IndexByte(p[i+1:], '.')
	if j < 0 {
		return -1
	}
	return i + 1 + j
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
