// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// security_test.go — tests for SecureString / SecureStrings redaction on
// serialization (pkg/config/security.go).
//
// SecureString wraps credential values (e.g. Google Chat WebhookURL,
// ServiceAccountJSON) that must never leave the process in plaintext via any
// serialization path. MarshalJSON has always redacted correctly; MarshalYAML
// previously returned the raw plaintext (s.resolved) — a leak for any code
// path that serializes a SecureString-containing Config to YAML. This file
// proves both marshal paths now redact identically.
//
// Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '.' -p 1 ./pkg/config/

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// SecureString — JSON and YAML redact identically
// ---------------------------------------------------------------------------

func TestSecureString_MarshalJSON_Redacts(t *testing.T) {
	s := NewSecureString("super-secret-webhook-token")

	out, err := json.Marshal(*s)
	require.NoError(t, err)
	require.Equal(t, `"[NOT_HERE]"`, string(out))
	require.NotContains(t, string(out), "super-secret-webhook-token")
}

func TestSecureString_MarshalYAML_Redacts(t *testing.T) {
	s := NewSecureString("super-secret-webhook-token")

	out, err := yaml.Marshal(*s)
	require.NoError(t, err)
	require.NotContains(t, string(out), "super-secret-webhook-token",
		"MarshalYAML must never emit the plaintext secret")
	require.Contains(t, string(out), "[NOT_HERE]")
}

// TestSecureString_MarshalJSON_MarshalYAML_Symmetric proves the two marshal
// paths agree on the redacted marker value (modulo JSON quoting vs YAML
// scalar encoding), closing the asymmetry where MarshalYAML used to return
// the raw plaintext while MarshalJSON redacted.
func TestSecureString_MarshalJSON_MarshalYAML_Symmetric(t *testing.T) {
	s := NewSecureString("another-secret-value")

	jsonOut, err := json.Marshal(*s)
	require.NoError(t, err)

	yamlOut, err := yaml.Marshal(*s)
	require.NoError(t, err)

	// json.Marshal produces the quoted JSON literal `"[NOT_HERE]"`.
	require.Equal(t, `"[NOT_HERE]"`, string(jsonOut))

	// yaml.Marshal produces the scalar [NOT_HERE] (quoting rules differ by
	// encoder, but the underlying value returned by MarshalYAML must be the
	// same redaction marker as MarshalJSON's payload, not the plaintext).
	var yamlDecoded string
	require.NoError(t, yaml.Unmarshal(yamlOut, &yamlDecoded))
	require.Equal(t, "[NOT_HERE]", yamlDecoded)

	var jsonDecoded string
	require.NoError(t, json.Unmarshal(jsonOut, &jsonDecoded))
	require.Equal(t, "[NOT_HERE]", jsonDecoded)

	// Both paths decode to the identical redaction marker.
	require.Equal(t, jsonDecoded, yamlDecoded)
}

// TestSecureString_MarshalYAML_EmptyValue confirms an empty SecureString is
// still redacted (not distinguishable from a populated one on the wire) —
// same behavior MarshalJSON already exhibits.
func TestSecureString_MarshalYAML_EmptyValue(t *testing.T) {
	s := NewSecureString("")

	out, err := yaml.Marshal(*s)
	require.NoError(t, err)

	var decoded string
	require.NoError(t, yaml.Unmarshal(out, &decoded))
	require.Equal(t, "[NOT_HERE]", decoded)
}

// TestSecureString_MarshalYAML_InStruct proves the redaction holds when a
// SecureString is embedded as a struct field (the real-world shape — e.g.
// GoogleChatConfig.WebhookURL / ServiceAccountJSON) and the whole struct is
// serialized, not just the bare SecureString value.
func TestSecureString_MarshalYAML_InStruct(t *testing.T) {
	type webhookHolder struct {
		WebhookURL SecureString `json:"webhook_url" yaml:"webhook_url"`
	}

	h := webhookHolder{WebhookURL: *NewSecureString("plaintext-webhook-secret")}

	out, err := yaml.Marshal(h)
	require.NoError(t, err)
	require.NotContains(t, string(out), "plaintext-webhook-secret")
	require.Contains(t, string(out), "[NOT_HERE]")

	jsonOut, err := json.Marshal(h)
	require.NoError(t, err)
	require.NotContains(t, string(jsonOut), "plaintext-webhook-secret")
	require.Contains(t, string(jsonOut), "[NOT_HERE]")
}

// ---------------------------------------------------------------------------
// Non-addressable values — the property the tests above do NOT guard
// ---------------------------------------------------------------------------
//
// MarshalJSON/MarshalYAML are deliberately VALUE receivers. That is what makes
// redaction apply however the caller happens to hold the value: a value
// receiver puts the method in both SecureString's and *SecureString's method
// sets, so encoding/json and yaml.v3 find the marshaler even on a value that
// is NOT addressable. A pointer receiver would make both marshalers callable
// only through *SecureString; for a non-addressable value both encoders then
// silently fall back to plain struct encoding (encoding/json builds a
// conditional-addressability encoder — newCondAddrEncoder — whose fallback
// is the ordinary struct path), emitting `{}` instead of "[NOT_HERE]": the
// unexported field leaks nothing, but the redaction marker disappears and
// any [NOT_HERE]-aware round-trip breaks.
//
// The tests above all marshal through values that stay copyable-but-typed;
// the two shapes below are the ones a pointer receiver breaks outright.
// They are the guard: change either marshaler to a pointer receiver and
// these go red.

// TestSecureString_MarshalJSON_NonAddressable_MapValue proves redaction for
// a SecureString held as a map value. Map values are never addressable in
// Go, so a pointer-receiver MarshalJSON would not be found by the encoder.
func TestSecureString_MarshalJSON_NonAddressable_MapValue(t *testing.T) {
	m := map[string]SecureString{"webhook": *NewSecureString("map-value-secret")}

	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.Equal(t, `{"webhook":"[NOT_HERE]"}`, string(out))
	require.NotContains(t, string(out), "map-value-secret")
}

// TestSecureString_MarshalYAML_NonAddressable_MapValue is the YAML twin of
// the map-value case above.
func TestSecureString_MarshalYAML_NonAddressable_MapValue(t *testing.T) {
	m := map[string]SecureString{"webhook": *NewSecureString("map-value-secret")}

	out, err := yaml.Marshal(m)
	require.NoError(t, err)
	require.NotContains(t, string(out), "map-value-secret")

	// Decode before comparing: the yaml encoder chooses quoting per scalar
	// ([NOT_HERE] starts with a flow indicator, so it is single-quoted), but
	// the decoded value must be exactly the redaction marker.
	var decoded map[string]string
	require.NoError(t, yaml.Unmarshal(out, &decoded))
	require.Equal(t, map[string]string{"webhook": "[NOT_HERE]"}, decoded)
}

// TestSecureString_MarshalJSON_NonAddressable_BareValue proves redaction for
// a bare SecureString passed by value — the value is copied into json.Marshal's
// interface parameter, so it is non-addressable from the encoder's point of
// view and only a value-receiver MarshalJSON can fire.
func TestSecureString_MarshalJSON_NonAddressable_BareValue(t *testing.T) {
	s := SecureString{resolved: "bare-value-secret"}

	out, err := json.Marshal(s)
	require.NoError(t, err)
	require.Equal(t, `"[NOT_HERE]"`, string(out))
	require.NotContains(t, string(out), "bare-value-secret")
}

// TestSecureString_MarshalYAML_NonAddressable_BareValue is the YAML twin of
// the bare-value case above.
func TestSecureString_MarshalYAML_NonAddressable_BareValue(t *testing.T) {
	s := SecureString{resolved: "bare-yaml-secret"}

	out, err := yaml.Marshal(s)
	require.NoError(t, err)
	require.NotContains(t, string(out), "bare-yaml-secret")

	var decoded string
	require.NoError(t, yaml.Unmarshal(out, &decoded))
	require.Equal(t, "[NOT_HERE]", decoded)
}

// TestSecureString_Marshal_AddressableStructViaPointer is the control case:
// a struct reached through a POINTER has addressable fields, so even a
// pointer-receiver marshaler would still be found here by encoding/json
// (its condAddr encoder takes the field's address). Under a MarshalJSON
// receiver flip this test stays green while the map/bare tests above go red
// — that asymmetry is why those tests, not this one, are the guard. yaml.v3
// has no such addressability fallback (verified by the same flip), so the
// YAML half of this test DOES depend on the value receiver.
func TestSecureString_Marshal_AddressableStructViaPointer(t *testing.T) {
	type holder struct {
		Token SecureString `json:"token" yaml:"token"`
	}
	h := holder{Token: *NewSecureString("addressable-secret")}

	jsonOut, err := json.Marshal(&h)
	require.NoError(t, err)
	require.Equal(t, `{"token":"[NOT_HERE]"}`, string(jsonOut))
	require.NotContains(t, string(jsonOut), "addressable-secret")

	yamlOut, err := yaml.Marshal(&h)
	require.NoError(t, err)
	require.NotContains(t, string(yamlOut), "addressable-secret")

	var decoded map[string]string
	require.NoError(t, yaml.Unmarshal(yamlOut, &decoded))
	require.Equal(t, map[string]string{"token": "[NOT_HERE]"}, decoded)
}
