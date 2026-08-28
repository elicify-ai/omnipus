// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-12 — the shared admission gate. FR-019 (a `tier: unsupported`
// row is refused with the catalog's OWN reason), FR-035 (an id the catalog
// does not carry is admitted only as a custom row carrying both api_base and
// a base-URL-describable protocol), and E7 (no document loaded → nothing is
// classified).

package providers

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

func TestAdmit_AgainstTheEmbeddedSnapshot(t *testing.T) {
	cases := []struct {
		name       string
		id         string
		apiBase    string
		protocol   string
		wantCustom bool
		wantErr    error
		wantMsg    string
	}{
		{
			name: "a catalog id is admitted as a catalog row",
			id:   "zai",
		},
		{
			name:    "a retired spelling is unknown, named and unhinted",
			id:      "z-ai",
			wantErr: ErrUnknownProvider,
			wantMsg: `unknown provider "z-ai"`,
		},
		{
			name:    "a cloud-IAM row carries the catalog's reason",
			id:      "amazon-bedrock",
			wantErr: ErrUnsupportedProvider,
			wantMsg: "cloud-iam",
		},
		{
			name:       "an unknown id with a base and a protocol is a custom row",
			id:         "my-proxy",
			apiBase:    "https://my-proxy.example.com/v1",
			protocol:   "openai-compatible",
			wantCustom: true,
		},
		{
			name:     "a custom row needs a base",
			id:       "my-proxy",
			protocol: "anthropic",
			wantErr:  ErrUnknownProvider,
		},
		{
			name:    "a custom row needs a protocol",
			id:      "my-proxy",
			apiBase: "https://my-proxy.example.com/v1",
			wantErr: ErrUnknownProvider,
		},
		{
			name:     "ollama is not a protocol a base URL fully describes",
			id:       "my-proxy",
			apiBase:  "http://127.0.0.1:11434/v1",
			protocol: "ollama",
			wantErr:  ErrUnknownProvider,
		},
		{
			name:    "an empty id is unknown",
			id:      "",
			wantErr: ErrUnknownProvider,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			custom, err := Admit(tc.id, tc.apiBase, tc.protocol)
			if tc.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, tc.wantCustom, custom)
				return
			}
			require.Error(t, err)
			assert.Truef(t, errors.Is(err, tc.wantErr),
				"want %v, got %v", tc.wantErr, err)
			if tc.wantMsg != "" {
				assert.Contains(t, err.Error(), tc.wantMsg)
			}
			assert.False(t, custom, "a rejected id is never a custom row")
		})
	}
}

// TestAdmitIn_NoDocumentClassifiesNothing pins E7: with a corrupt or absent
// snapshot the gate cannot tell a known id from an unknown one, and refusing
// every configuration would make a bad document unrecoverable through the UI
// or the wizard.
func TestAdmitIn_NoDocumentClassifiesNothing(t *testing.T) {
	for _, cat := range []*catalog.Catalog{nil, catalog.New()} {
		custom, err := AdmitIn(cat, "anything-at-all", "", "")
		require.NoError(t, err)
		assert.False(t, custom)
	}
}
