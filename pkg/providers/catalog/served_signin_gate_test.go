package catalog

// Provider hotfix H1: WithSignInGate filters `sign_in` out of the SERVED
// projection for rows the gate rejects, never out of the parsed Document.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const signInGateDoc = `{
	"schema_version": "2.0.0", "version": "v2026.10.13",
	"updated_at": "2026-10-13T06:00:00Z",
	"source": "models.dev@0123456789abcdef0123456789abcdef01234567",
	"default_resize_limits": {"long_edge_px": 7680, "max_bytes": 10485760},
	"providers": [
		{"id": "dual-no-mech", "name": "Dual", "company": "V", "api": "https://a.example.com/v1",
		 "protocol": "openai-compatible", "env": [], "tier": "standard",
		 "auth_methods": ["api_key", "sign_in"], "models": []},
		{"id": "dual-mech", "name": "Dual Mech", "company": "V", "api": "https://b.example.com/v1",
		 "protocol": "openai-compatible", "env": [], "tier": "standard",
		 "auth_methods": ["api_key", "sign_in"], "models": []},
		{"id": "signin-only-no-mech", "name": "Only", "company": "V", "api": "https://c.example.com/v1",
		 "protocol": "openai-compatible", "env": [], "tier": "standard",
		 "auth_methods": ["sign_in"], "models": []}
	]
}`

func servedAuthMethodsByID(t *testing.T, c *Catalog) map[string][]AuthMethod {
	t.Helper()
	served, ok := c.Served()
	require.True(t, ok)
	var env struct {
		Providers []struct {
			ID          string       `json:"id"`
			AuthMethods []AuthMethod `json:"auth_methods"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(served.Body, &env))
	out := map[string][]AuthMethod{}
	for _, p := range env.Providers {
		out[p.ID] = p.AuthMethods
	}
	return out
}

func TestServed_SignInGateFiltersProjectionOnly(t *testing.T) {
	gate := func(p Provider) bool { return p.ID == "dual-mech" }
	c, err := NewCatalog([]byte(signInGateDoc), WithSignInGate(gate))
	require.NoError(t, err)

	got := servedAuthMethodsByID(t, c)
	assert.Equal(t, []AuthMethod{AuthAPIKey}, got["dual-no-mech"], "sign_in dropped: no mechanism")
	assert.Equal(t, []AuthMethod{AuthAPIKey, AuthSignIn}, got["dual-mech"], "a row with a mechanism keeps it")
	assert.Equal(t, []AuthMethod{AuthSignIn}, got["signin-only-no-mech"],
		"never served empty (contract minItems: 1) — the sign-in routes refuse it instead")

	row, ok := c.Provider("dual-no-mech")
	require.True(t, ok)
	assert.Equal(t, []AuthMethod{AuthAPIKey, AuthSignIn}, row.AuthMethods,
		"the parsed document stays a faithful registry mirror")
}

func TestServed_NoGateServesDeclaredMethods(t *testing.T) {
	c, err := NewCatalog([]byte(signInGateDoc))
	require.NoError(t, err)

	got := servedAuthMethodsByID(t, c)
	assert.Equal(t, []AuthMethod{AuthAPIKey, AuthSignIn}, got["dual-no-mech"])
}
