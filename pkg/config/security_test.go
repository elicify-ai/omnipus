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
