// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// validate_bedrock_classification_test.go — orchestrator review round 2, D3.
//
// A dummy Bedrock API key was probed against a real onboarding gateway and
// came back success:true, validation.outcome "restricted" — "Your Amazon
// Bedrock key works, but Amazon Bedrock blocked this request...". Verified
// against real AWS via curl: Bedrock answers EVERY invalid-key case with
// HTTP 403 AccessDeniedException (the same status/type genuine model-access
// denial also uses) — only the body's Message distinguishes them. The
// generic classify() (validate.go) never sees a real message here at all:
// probeBedrockCompletion hands it "Code Message" as a plain string, which
// parseErrorBody's JSON decode silently fails on, so step 4's marker check
// never runs and step 5's status switch alone decides — 403 always lands on
// Restricted, whatever AWS actually said.
package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestValidateKey_BedrockAuthFailureBodies_ClassifyAsInvalidKey drives the
// exact bodies/headers the coordinator captured against a real gateway (and
// verified against real AWS via curl) through ValidateKey with
// ProviderID: "amazon-bedrock" end-to-end — proving the WHOLE probe path
// (probeBedrockCompletion -> its classifier), not just a helper in
// isolation.
func TestValidateKey_BedrockAuthFailureBodies_ClassifyAsInvalidKey(t *testing.T) {
	tests := []struct {
		name      string
		errorType string // X-Amzn-Errortype header value, colon-suffixed as AWS sends it
		body      string
	}{
		{
			name:      "bad format",
			errorType: "AccessDeniedException:http://internal.amazon.com/coral/com.amazon.coral.service/#AccessDeniedException",
			body:      `{"Message":"Invalid API Key format: Must start with pre-defined prefix"}`,
		},
		{
			name:      "well-formed but wrong key",
			errorType: "AccessDeniedException:http://internal.amazon.com/coral/com.amazon.coral.service/#AccessDeniedException",
			body:      `{"Message":"Authentication failed: Please make sure your API Key is valid."}`,
		},
		{
			name:      "expired key",
			errorType: "AccessDeniedException:http://internal.amazon.com/coral/com.amazon.coral.service/#AccessDeniedException",
			body:      `{"Message":"Bearer Token has expired"}`,
		},
		{
			name:      "security token invalid (UnrecognizedClientException)",
			errorType: "UnrecognizedClientException:http://internal.amazon.com/coral/com.amazon.coral.service/#UnrecognizedClientException",
			body:      `{"message":"The security token included in the request is invalid"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Amzn-Errortype", tt.errorType)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			result := ValidateKey(context.Background(), ValidateInput{
				ProviderID:   "amazon-bedrock",
				ProviderName: "Amazon Bedrock",
				BaseURL:      server.URL,
				APIKey:       "dummy-bedrock-key",
				ProbeModels:  []string{"anthropic.claude-opus-4-6-v1"},
			}, nil)

			if result.Outcome != OutcomeInvalidKey {
				t.Fatalf("Outcome = %q (%s), want %q; raw=%s",
					result.Outcome, result.Message, OutcomeInvalidKey, result.RawDetail)
			}
			if !result.Blocks() {
				t.Fatalf("Blocks() = false for outcome %q, want true (only invalid_key blocks)", result.Outcome)
			}
		})
	}
}

// TestValidateKey_BedrockModelAccessDenial_StaysRestricted is the required
// negative case: a GENUINE 403 AccessDeniedException that names a model
// access problem, not a credential problem, must keep classifying as
// Restricted — the fix must discriminate on the MESSAGE, not blanket-treat
// every Bedrock 403 as an invalid key.
func TestValidateKey_BedrockModelAccessDenial_StaysRestricted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Amzn-Errortype",
			"AccessDeniedException:http://internal.amazon.com/coral/com.amazon.coral.service/#AccessDeniedException")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"Message":"You don't have access to the model with the specified model ID."}`))
	}))
	defer server.Close()

	result := ValidateKey(context.Background(), ValidateInput{
		ProviderID:   "amazon-bedrock",
		ProviderName: "Amazon Bedrock",
		BaseURL:      server.URL,
		APIKey:       "real-but-unentitled-key",
		ProbeModels:  []string{"anthropic.claude-opus-4-6-v1"},
	}, nil)

	if result.Outcome != OutcomeRestricted {
		t.Fatalf("Outcome = %q (%s), want %q; raw=%s",
			result.Outcome, result.Message, OutcomeRestricted, result.RawDetail)
	}
	if result.Blocks() {
		t.Fatal("Blocks() = true for a model-access denial — only invalid_key may block")
	}
}
