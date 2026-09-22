// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// platform_auth.go — the `security.platform_auth.*` config subtree.
//
// It carries what this instance needs in order to let omnipus.ai vouch for
// whoever is signing in (ADR-0008 "the account is the login";
// docs/specs/platform-auth-instance-spec.md §1 and §4).
//
// Everything here is EMPTY by default, and empty means "not configured yet"
// rather than "off". The sign-in route answers 503 when Issuer is empty (see
// pkg/gateway/rest_platform_auth.go) instead of falling back to anything —
// there is no local password to fall back to.

// SecurityConfig is the root of the `security.` subtree. Deliberately a struct
// with one member rather than a flat set of keys: both config-blocking tables
// deny the subtree by ancestor, so anything added under here inherits that
// protection without a new deny entry.
type SecurityConfig struct {
	PlatformAuth PlatformAuthConfig `json:"platform_auth,omitempty" yaml:"-"`
}

// PlatformAuthConfig names the omnipus.ai issuer and the key material this
// instance verifies its tokens against.
//
// The trust anchor is a STATIC key list rather than a fetched JWKS URL, and
// that is a decision (platform-auth-instance-spec.md WA-3 / O-2): a configured
// key verifies a token even when the platform is unreachable, which on desktop
// is the ordinary case and not the failure case. It is also the smaller of the
// two implementations — no boot-time fetch, no cache, no staleness or rotation
// story to get wrong. If CP-04 lands on a JWKS URL instead, this grows a
// `jwks_url` sibling and a cache; nothing else in the flow changes.
type PlatformAuthConfig struct {
	// Issuer is the omnipus.ai issuer URL — the value every token's `iss`
	// claim must equal EXACTLY, and the base the RFC 8414 metadata document is
	// fetched from (/.well-known/oauth-authorization-server).
	//
	// It is NOT an endpoint prefix. The authorize and token endpoints are read
	// from that metadata, because on the platform they sit under a versioned
	// path (/v1/oauth/...) while `iss` does not — one value cannot be both, and
	// deriving the endpoints from this one used to make them wrong.
	//
	// Empty disables platform sign-in: the start route answers 503 rather than
	// building a sign-in that points nowhere.
	Issuer string `json:"issuer,omitempty" env:"OMNIPUS_SECURITY_PLATFORM_AUTH_ISSUER"`

	// ClientID identifies this application to the issuer. A public client:
	// there is no client secret, because a desktop application cannot keep one
	// (RFC 8252 §8.5). PKCE is what replaces it.
	ClientID string `json:"client_id,omitempty" env:"OMNIPUS_SECURITY_PLATFORM_AUTH_CLIENT_ID"`

	// InstanceID is this instance's id, and it is the tenant boundary: every
	// access token's `aud` must equal it exactly, so a token minted for
	// somebody else's instance cannot be replayed against this one
	// (FR-PA-009).
	//
	// Empty fails closed: the start route answers 503 and verification
	// refuses the token (errPlatformNoInstanceID). An instance that cannot
	// name itself cannot tell a token minted for it from one minted for
	// another instance, so there is no half-configured state to skip into.
	InstanceID string `json:"instance_id,omitempty" env:"OMNIPUS_SECURITY_PLATFORM_AUTH_INSTANCE_ID"`

	// Scope is the OAuth scope requested at the authorize endpoint. Empty
	// sends no scope parameter at all, which lets the issuer apply its own
	// default rather than having one invented here.
	Scope string `json:"scope,omitempty" env:"OMNIPUS_SECURITY_PLATFORM_AUTH_SCOPE"`

	// Keys is the trust anchor: the public keys whose signatures this instance
	// will accept, selected by the token header's `kid`. A `kid` matching no
	// entry is a hard refusal — never "try them all", and never a fetch
	// (FR-PA-007).
	//
	// PROVISIONING ROUTE (code-review finding 8): the four sibling fields
	// above each have an `env` tag that env.Parse (config.go, ~line 4400)
	// applies after config.json loads, and Keys had none — leaving no
	// supported way to seed the trust anchor on a fresh install, since both
	// config-write surfaces (blocked_paths.go's PUT /api/v1/config,
	// sysagent/tools/config.go's set_config tool) refuse the whole
	// `security.*` subtree by design (ADR-0008, "Carrying ADR-0005 E3
	// forward"). Hand-editing config.json before first boot was the only
	// route that worked.
	//
	// OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS closes that gap. Its value is a
	// comma-separated list of one or more keys, each written as
	// "kid:alg:base64_public_key" (colon-separated, exactly 3 fields, no
	// embedded commas or colons in any field). Example, one key:
	//
	//	OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS="prod-2026-09:EdDSA:MCowBQYDK2VwAyEA...=="
	//
	// and two:
	//
	//	OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS="k1:EdDSA:base64one,k2:EdDSA:base64two"
	//
	// caarlos0/env splits on the comma and calls PlatformAuthKey.UnmarshalText
	// once per piece (see that method below) — a malformed piece fails the
	// whole config load rather than silently dropping a key or starting with
	// a partial anchor.
	Keys []PlatformAuthKey `json:"keys,omitempty" env:"OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS"`
}

// PlatformAuthKey is one entry in the trust anchor.
type PlatformAuthKey struct {
	// KID matches the JWT header's `kid`. Required and unique.
	KID string `json:"kid"`

	// Alg is the signature algorithm. Only "EdDSA" (Ed25519) is accepted; the
	// verifier pins it as an allow-list of one and refuses every other value,
	// `none`, `HS*`, `RS*` and `ES*` included (ADR-0005 E2 / FR-PA-005).
	Alg string `json:"alg"`

	// PublicKey is the raw 32-byte Ed25519 public key, base64 encoded. Both
	// the URL-safe and the standard alphabet are accepted, with or without
	// padding, because the value is pasted by a human from whatever the
	// platform printed.
	PublicKey string `json:"public_key"`
}

// UnmarshalText parses one "kid:alg:base64_public_key" triple — the format
// documented on PlatformAuthConfig.Keys above. It exists only so
// caarlos0/env (which checks encoding.TextUnmarshaler on a slice element type
// before falling back to a plain parser) can provision the trust anchor from
// OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS.
//
// It is NOT the JSON decode path, and merely declaring it is enough to BREAK
// that path unless UnmarshalJSON below exists — see the comment there. An
// earlier version of this comment asserted the opposite ("nothing in the JSON
// decode path calls it"), which is true but irrelevant: encoding/json refuses
// a JSON object outright the moment the destination type is a
// TextUnmarshaler, without ever calling this method.
//
// Splits on the first two colons only, so a base64 public key is never
// truncated even though standard base64 does not itself contain ':'; this
// is defensive, not load-bearing. A field that comes out empty after
// trimming is a hard parse error — never a silently half-built key.
func (k *PlatformAuthKey) UnmarshalText(text []byte) error {
	parts := strings.SplitN(string(text), ":", 3)
	if len(parts) != 3 {
		return fmt.Errorf("platform_auth key %q: want \"kid:alg:base64_public_key\"", string(text))
	}
	kid := strings.TrimSpace(parts[0])
	alg := strings.TrimSpace(parts[1])
	pub := strings.TrimSpace(parts[2])
	if kid == "" || alg == "" || pub == "" {
		return fmt.Errorf("platform_auth key %q: kid, alg and public_key must all be non-empty", string(text))
	}
	k.KID = kid
	k.Alg = alg
	k.PublicKey = pub
	return nil
}

// UnmarshalJSON decodes the object form — {"kid":…,"alg":…,"public_key":…} —
// which is what config.json actually stores and what every writer in this
// package produces.
//
// It has to be written out by hand PRECISELY BECAUSE UnmarshalText exists
// above. encoding/json checks the destination type for json.Unmarshaler
// first and encoding.TextUnmarshaler second, and its object decoder refuses
// to descend into a TextUnmarshaler at all: given `{"kid":…}` it fails with
// "json: cannot unmarshal object into Go struct field
// PlatformAuthConfig.security.platform_auth.keys of type
// config.PlatformAuthKey" and never calls either method. So adding the env
// provisioning route silently broke reading the trust anchor back off disk —
// a configured instance loaded its own config.json and got that error,
// which surfaced as "Omnipus could not record the sign-in on this machine"
// at the end of an otherwise successful sign-in (pkg/gateway's
// TestPlatformAuthCallback_* and TestPlatformAuthClaim_*).
//
// Declaring UnmarshalJSON restores the object decode and wins the precedence
// check, leaving UnmarshalText reachable only where caarlos0/env calls it
// directly. The `keyJSON` alias is the standard recursion break: it has the
// same fields and tags but no methods, so the nested Unmarshal uses the
// ordinary struct decoder.
//
// The string form is deliberately NOT accepted here. config.json has exactly
// one shape for a key, and quietly taking a second one would be a
// compatibility shim for a format nothing has ever written.
func (k *PlatformAuthKey) UnmarshalJSON(data []byte) error {
	type keyJSON PlatformAuthKey
	var raw keyJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*k = PlatformAuthKey(raw)
	return nil
}
