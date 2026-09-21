// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// Issue #800 (Bedrock region contract) — the region must be user-selectable,
// never hard-wired to the catalog's default. This file proves, end to end
// through CreateProviderFromConfig, that:
//
//   - region precedence is row setting -> AWS_REGION -> catalog default;
//   - the endpoint is derived from the selected region, or the custom
//     api_base override when one is set;
//   - the model id sent upstream is resolved per the contract's exact
//     rules, reading only catalog data (regions[].group,
//     models[].inference_profiles) — never a hand-typed Go list.
const bedrockRegionFixture = `{
  "schema_version": "2.0.0",
  "version": "v2026.9.21",
  "updated_at": "2026-09-21T00:00:00Z",
  "source": "test-fixture",
  "default_resize_limits": {"long_edge_px": 7680, "max_bytes": 10485760},
  "providers": [
    {
      "id": "amazon-bedrock",
      "name": "Amazon Bedrock",
      "company": "Amazon Bedrock",
      "api": "https://bedrock-runtime.us-east-1.amazonaws.com",
      "protocol": "bedrock",
      "region": "us-east-1",
      "regions": [
        {"id": "us-east-1", "group": "us"},
        {"id": "eu-central-1", "group": "eu"},
        {"id": "us-gov-west-1", "group": ""}
      ],
      "tier": "standard",
      "auth_methods": ["api_key"],
      "aliases": ["bedrock"],
      "resize_limits": {"long_edge_px": 7680, "max_bytes": 10485760},
      "models": [
        {
          "id": "anthropic.claude-sonnet-4-5-20250929-v1:0",
          "name": "Claude Sonnet 4.5",
          "context_window": 200000,
          "max_output_tokens": 64000,
          "input_modalities": ["text", "image"],
          "tool_call": true,
          "status": "active",
          "inference_profiles": ["us", "eu"]
        },
        {
          "id": "amazon.nova-pro-v1:0",
          "name": "Nova Pro",
          "context_window": 300000,
          "max_output_tokens": 8192,
          "input_modalities": ["text", "image"],
          "tool_call": true,
          "status": "active"
        },
        {
          "id": "global-only.model-v1:0",
          "name": "Global-only test model",
          "context_window": 128000,
          "max_output_tokens": 8192,
          "input_modalities": ["text"],
          "tool_call": true,
          "status": "active",
          "inference_profiles": ["global"]
        }
      ]
    }
  ]
}`

func withBedrockRegionFixture(t *testing.T) {
	t.Helper()
	c, err := catalog.NewCatalog([]byte(bedrockRegionFixture))
	if err != nil {
		t.Fatalf("parse bedrock region fixture: %v", err)
	}
	SetCatalog(c)
	t.Cleanup(func() { SetCatalog(nil) })
}

func TestCreateProviderFromConfig_Bedrock_RegionPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		rowRegion string
		envRegion string
		wantHost  string
	}{
		{name: "row setting wins", rowRegion: "eu-central-1", envRegion: "us-gov-west-1", wantHost: "https://bedrock-runtime.eu-central-1.amazonaws.com"},
		{name: "AWS_REGION env wins when row unset", rowRegion: "", envRegion: "us-gov-west-1", wantHost: "https://bedrock-runtime.us-gov-west-1.amazonaws.com"},
		{name: "catalog default wins when both unset", rowRegion: "", envRegion: "", wantHost: "https://bedrock-runtime.us-east-1.amazonaws.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withBedrockRegionFixture(t)
			if tc.envRegion != "" {
				t.Setenv("AWS_REGION", tc.envRegion)
			} else {
				t.Setenv("AWS_REGION", "")
			}
			p, _, err := CreateProviderFromConfig(&config.ModelConfig{
				Provider:  "amazon-bedrock",
				Model:     "amazon.nova-pro-v1:0",
				Region:    tc.rowRegion,
				APIKeyRef: keyRef(t, "FACTORY_BEDROCK_REGION_TEST_KEY"),
			})
			if err != nil {
				t.Fatalf("CreateProviderFromConfig: %v", err)
			}
			bp, ok := p.(*bedrock.Provider)
			if !ok {
				t.Fatalf("provider = %T, want *bedrock.Provider", p)
			}
			if bp.Endpoint() != tc.wantHost {
				t.Fatalf("endpoint = %q, want %q", bp.Endpoint(), tc.wantHost)
			}
		})
	}
}

func TestCreateProviderFromConfig_Bedrock_CustomEndpointOverrideWins(t *testing.T) {
	withBedrockRegionFixture(t)
	t.Setenv("AWS_REGION", "")
	const customEndpoint = "https://vpce-bedrock.example.internal"
	p, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider:  "amazon-bedrock",
		Model:     "amazon.nova-pro-v1:0",
		Region:    "eu-central-1",
		APIBase:   customEndpoint,
		APIKeyRef: keyRef(t, "FACTORY_BEDROCK_CUSTOM_ENDPOINT_TEST_KEY"),
	})
	if err != nil {
		t.Fatalf("CreateProviderFromConfig: %v", err)
	}
	bp, ok := p.(*bedrock.Provider)
	if !ok {
		t.Fatalf("provider = %T, want *bedrock.Provider", p)
	}
	if bp.Endpoint() != customEndpoint {
		t.Fatalf("endpoint = %q, want the custom override %q (region must not win)", bp.Endpoint(), customEndpoint)
	}
}

func TestCreateProviderFromConfig_Bedrock_ModelIDResolution(t *testing.T) {
	tests := []struct {
		name      string
		rowRegion string
		model     string
		wantModel string
	}{
		{
			name:      "group prefix applied when present in inference_profiles",
			rowRegion: "eu-central-1", // group eu
			model:     "anthropic.claude-sonnet-4-5-20250929-v1:0",
			wantModel: "eu.anthropic.claude-sonnet-4-5-20250929-v1:0",
		},
		{
			name:      "bare id when the group is absent from inference_profiles",
			rowRegion: "us-east-1", // group us; Nova Pro has no inference_profiles
			model:     "amazon.nova-pro-v1:0",
			wantModel: "amazon.nova-pro-v1:0",
		},
		{
			name:      "bare id when the selected region's group is empty",
			rowRegion: "us-gov-west-1", // group ""
			model:     "anthropic.claude-sonnet-4-5-20250929-v1:0",
			wantModel: "anthropic.claude-sonnet-4-5-20250929-v1:0",
		},
		{
			name:      "never auto-selects global: a global-only model stays bare outside an explicit global. id",
			rowRegion: "us-east-1", // group us; model only carries "global"
			model:     "global-only.model-v1:0",
			wantModel: "global-only.model-v1:0",
		},
		{
			name:      "already-prefixed id passes through unchanged",
			rowRegion: "eu-central-1",
			model:     "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
			wantModel: "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		},
		{
			name:      "arn passes through unchanged",
			rowRegion: "eu-central-1",
			model:     "arn:aws:bedrock:us-east-1:111122223333:inference-profile/my-profile",
			wantModel: "arn:aws:bedrock:us-east-1:111122223333:inference-profile/my-profile",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withBedrockRegionFixture(t)
			t.Setenv("AWS_REGION", "")
			_, modelID, err := CreateProviderFromConfig(&config.ModelConfig{
				Provider:  "amazon-bedrock",
				Model:     tc.model,
				Region:    tc.rowRegion,
				APIKeyRef: keyRef(t, "FACTORY_BEDROCK_MODELID_TEST_KEY"),
			})
			if err != nil {
				t.Fatalf("CreateProviderFromConfig: %v", err)
			}
			if modelID != tc.wantModel {
				t.Fatalf("modelID = %q, want %q", modelID, tc.wantModel)
			}
		})
	}
}
