package generated

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ── ADR-068 own contract files (T068-06, amended 2026-08-23 SS8b/T068-34) ────
// ── ADR-068 own contract files (T068-06, amended 2026-08-23 §8b/T068-34) ────
// Traces to:
//   contracts/openapi.yaml#/components/schemas/SignInStartResponse (FR-008; a
//     direct reference to the device-code shape since WU-24 removed the
//     vendor-CLI variant with the CLI-backed model providers)
//   contracts/components/schemas/SignInStartResponseDeviceCode.yaml (FR-008/FR-044)
//   contracts/components/schemas/SignInStatus.yaml                  (FR-009)
//   contracts/components/schemas/SignInPollRequest.yaml             (FR-044)
//   contracts/components/schemas/SignInPollResponse.yaml            (FR-044)
//   contracts/components/schemas/OnboardingProviderApiKey.yaml (MAJ-014)
//   contracts/components/schemas/OnboardingProviderSignIn.yaml (MAJ-014)
//   contracts/components/schemas/OnboardingCompleteRequest.yaml (provider oneOf)
//   contracts/components/schemas/ProbeProviderResponse.yaml    (FR-036 probed_model)
//   contracts/components/schemas/Provider.yaml                 (FR-038 / SC-012)
//
// SignInStartResponse was a discriminated union (ADR-034 pattern) between
// the vendor-CLI and device_code shapes from 2026-08-23; WU-24 (founder
// 2026-10-14) deleted the vendor-CLI variant with the CLI-backed model
// providers, and the response is now the device-code shape directly. SignInStatus's state enum
// gained `pending` on 2026-08-23.
//
// Generated enum const names (oapi-codegen v2.7.0 drops the type prefix when
// the value is unique across the spec, and qualifies EVERY sibling of a type
// once any one value collides elsewhere in the spec — `pending` collides, so
// all four SignInStatusState members are qualified now, unlike before):
// DeviceCode, SignInStatusState{NotSignedIn,Pending,SignedIn,Expired}. The
// remaining union accessors are on OnboardingCompleteRequest's provider
// oneOf: AsOnboardingProviderApiKey() / AsOnboardingProviderSignIn().

func deviceCodeFixture() SignInStartResponseDeviceCode {
	return SignInStartResponseDeviceCode{
		Method:          SignInStartResponseDeviceCodeMethodDeviceCode,
		VerificationUrl: "https://auth.openai.com/codex/device",
		UserCode:        "WDJB-MJHT",
		DeviceAuthId:    "das_9f3a2b1c",
		ExpiresAt:       time.Date(2026, 8, 23, 12, 15, 0, 0, time.UTC),
		IntervalSeconds: 5,
	}
}

func TestContract_SignInStartResponseDeviceCode_Populated(t *testing.T) {
	mustPassComponent(t, "SignInStartResponseDeviceCode", deviceCodeFixture())
}

func TestContract_SignInStartResponseDeviceCode_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInStartResponseDeviceCode", SignInStartResponseDeviceCode{},
		"method const, verification_url, user_code, device_auth_id, expires_at and interval_seconds are all required")
}

func TestContract_SignInStartResponseDeviceCode_IntervalBounds(t *testing.T) {
	low := deviceCodeFixture()
	low.IntervalSeconds = 0
	mustFailComponent(t, "SignInStartResponseDeviceCode", low, "interval_seconds minimum 1")
	high := deviceCodeFixture()
	high.IntervalSeconds = 31
	mustFailComponent(t, "SignInStartResponseDeviceCode", high, "interval_seconds maximum 30 (FR-045)")
}

func TestContract_SignInStartResponse_DeviceCodeRoundTrip(t *testing.T) {
	// WU-24: one variant left — the response IS the device-code shape (a
	// plain struct, no union accessors), so the round trip is a plain
	// marshal/unmarshal.
	b, err := json.Marshal(deviceCodeFixture())
	require.NoError(t, err)
	var back SignInStartResponse
	require.NoError(t, json.Unmarshal(b, &back))
	assert.Equal(t, "das_9f3a2b1c", back.DeviceAuthId)
	assert.Equal(t, SignInStartResponseMethodDeviceCode, back.Method)
}

// SignInPollRequest / SignInPollResponse (FR-044).

func TestContract_SignInPollRequest_Populated(t *testing.T) {
	mustPassComponent(t, "SignInPollRequest", SignInPollRequest{DeviceAuthId: "das_9f3a2b1c"})
}

func TestContract_SignInPollRequest_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInPollRequest", SignInPollRequest{}, "device_auth_id is required and non-empty")
}

func TestContract_SignInPollResponse_AllStates(t *testing.T) {
	for _, st := range []SignInPollResponseState{
		SignInPollResponseStatePending, SignInPollResponseStateSignedIn,
		SignInPollResponseStateExpired, SignInPollResponseStateDenied,
	} {
		mustPassComponent(t, "SignInPollResponse", SignInPollResponse{State: st})
	}
}

func TestContract_SignInPollResponse_SlowDownInterval(t *testing.T) {
	interval := 10
	mustPassComponent(t, "SignInPollResponse", SignInPollResponse{
		State:           SignInPollResponseStatePending,
		IntervalSeconds: &interval,
	})
}

func TestContract_SignInPollResponse_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInPollResponse", SignInPollResponse{}, "state is required")
}

// SignInStatus — {state: not_signed_in|pending|signed_in|expired,
// account_label?, expires_at?}; `pending` added by the 2026-08-23 §8b
// amendment for open device-code sessions.

func TestContract_SignInStatus_AllStates(t *testing.T) {
	for _, st := range []SignInStatusState{
		SignInStatusStateNotSignedIn, SignInStatusStatePending,
		SignInStatusStateSignedIn, SignInStatusStateExpired,
	} {
		mustPassComponent(t, "SignInStatus", SignInStatus{State: st})
	}
}

func TestContract_SignInStatus_Populated(t *testing.T) {
	label := "user_abc123"
	exp := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mustPassComponent(t, "SignInStatus", SignInStatus{
		State:        SignInStatusStateSignedIn,
		AccountLabel: &label,
		ExpiresAt:    &exp,
	})
}

func TestContract_SignInStatus_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInStatus", SignInStatus{}, "state is required")
}

func TestContract_SignInStatus_UnknownStateRejected(t *testing.T) {
	raw := []byte(`{"state":"refreshing"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStatus", raw),
		"state is a closed enum of exactly four values")
}

func TestContract_SignInStatus_AccountLabelOver128Rejected(t *testing.T) {
	label := strings.Repeat("a", 129)
	mustFailComponent(t, "SignInStatus", SignInStatus{State: SignInStatusStateSignedIn, AccountLabel: &label},
		"account_label maxLength 128")
}

// OnboardingProviderApiKey / OnboardingProviderSignIn — the two discriminated
// variants of OnboardingCompleteRequest.provider.

func TestContract_OnboardingProviderApiKey_Populated(t *testing.T) {
	model := "claude-sonnet-4-6"
	mustPassComponent(t, "OnboardingProviderApiKey", OnboardingProviderApiKey{
		AuthMethod: OnboardingProviderApiKeyAuthMethodApiKey,
		Id:         "anthropic",
		ApiKey:     "sk-ant-test",
		Model:      &model,
	})
}

func TestContract_OnboardingProviderApiKey_MissingKeyRejected(t *testing.T) {
	raw := []byte(`{"auth_method":"api_key","id":"anthropic"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingProviderApiKey", raw),
		"api_key is required on the api_key variant")
}

func TestContract_OnboardingProviderApiKey_WrongDiscriminatorRejected(t *testing.T) {
	raw := []byte(`{"auth_method":"sign_in","id":"anthropic","api_key":"k"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingProviderApiKey", raw),
		"auth_method is pinned to api_key")
}

func TestContract_OnboardingProviderSignIn_Populated(t *testing.T) {
	model := "gpt-5.4"
	mustPassComponent(t, "OnboardingProviderSignIn", OnboardingProviderSignIn{
		AuthMethod: OnboardingProviderSignInAuthMethodSignIn,
		Id:         "openai-chatgpt",
		Model:      &model,
	})
}

func TestContract_OnboardingProviderSignIn_ApiKeyRejected(t *testing.T) {
	raw := []byte(`{"auth_method":"sign_in","id":"openai-chatgpt","api_key":"k"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingProviderSignIn", raw),
		"api_key not allowed with sign_in")
}

// OnboardingCompleteRequest — the wrapper's provider is the oneOf; a body
// without auth_method matches neither variant.
//
// Every fixture below also carries `preferences`, which became REQUIRED when
// onboarding gained step 1 (FR-OB-010..-018). These tests are about the
// provider discriminator, so the field is fixture noise here — but it is not
// optional on the wire, and TestContract_OnboardingCompleteRequest_MissingPreferencesRejected
// is what pins that.

func TestContract_OnboardingCompleteRequest_ApiKeyVariantPasses(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"api_key","id":"anthropic","api_key":"sk-ant","model":"claude-sonnet-4-6"},"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw))
}

func TestContract_OnboardingCompleteRequest_SignInVariantPasses(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"sign_in","id":"openai-chatgpt","model":"gpt-5.4"},"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw))
}

// TestContract_OnboardingCompleteRequest_MissingPreferencesAccepted pins the
// WP5 (ADR-0010) redesign on the wire: commit f232d1755 made `preferences`
// optional in BOTH modes (OnboardingCompleteRequest.yaml's own description:
// "`preferences` ... is optional in BOTH modes and, when present, is written
// into the global USER.md after the transaction succeeds"). This test used to
// assert the OPPOSITE (a pre-WP5 invariant, when step 1 was the only path to
// completion) and was left stale when the schema changed under it — a body
// without preferences must be ACCEPTED, not rejected.
func TestContract_OnboardingCompleteRequest_MissingPreferencesAccepted(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"api_key","id":"anthropic","api_key":"sk-ant","model":"claude-sonnet-4-6"}}`)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw),
		"preferences is optional in both modes (WP5/ADR-0010) — omitting it must not be rejected")
}

func TestContract_OnboardingCompleteRequest_MissingAuthMethodRejected(t *testing.T) {
	raw := []byte(`{"provider":{"id":"anthropic","api_key":"sk-ant"},"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw),
		"auth_method discriminator is required — the historical omit default is retired")
}

func TestContract_OnboardingCompleteRequest_SignInWithKeyRejected(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"sign_in","id":"openai-chatgpt","api_key":"k"}}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw),
		"api_key not allowed with sign_in")
}

func TestContract_OnboardingCompleteRequest_UnionRoundTrip(t *testing.T) {
	// The generated wrapper must carry the variant through the union accessors.
	raw := []byte(`{"provider":{"auth_method":"sign_in","id":"openai-chatgpt"}}`)
	var req OnboardingCompleteRequest
	require.NoError(t, json.Unmarshal(raw, &req))
	disc, err := req.Provider.Discriminator()
	require.NoError(t, err)
	assert.Equal(t, "sign_in", disc)
	v, err := req.Provider.AsOnboardingProviderSignIn()
	require.NoError(t, err)
	assert.Equal(t, "openai-chatgpt", v.Id)
}

// ProbeProviderResponse.probed_model (FR-036).

func TestContract_ProbeProviderResponse_ProbedModel(t *testing.T) {
	pm := "gpt-5.4"
	r := ProbeProviderResponse{Success: true, ProbedModel: &pm}
	mustPassComponent(t, "ProbeProviderResponse", r)
	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"probed_model":"gpt-5.4"`)
	over := strings.Repeat("m", 257)
	mustFailComponent(t, "ProbeProviderResponse", ProbeProviderResponse{Success: true, ProbedModel: &over},
		"probed_model maxLength 256")
}

// SC-012 — Provider.status generated Go consts are exactly six and equal the
// ADR-067/068 list (FR-038).

func TestContract_ProviderStatus_ExactlySix(t *testing.T) {
	want := []string{"connected", "disconnected", "error", "unknown-provider", "signed_in", "expired"}
	got := []ProviderStatus{
		ProviderStatusConnected, ProviderStatusDisconnected, ProviderStatusError,
		ProviderStatusUnknownProvider, ProviderStatusSignedIn, ProviderStatusExpired,
	}
	for i, g := range got {
		assert.Equal(t, want[i], string(g))
	}

	data, err := os.ReadFile(filepath.Join(contractsDir(), "components", "schemas", "Provider.yaml"))
	require.NoError(t, err)
	var doc struct {
		Properties struct {
			Status struct {
				Enum []string `yaml:"enum"`
			} `yaml:"status"`
		} `yaml:"properties"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	assert.ElementsMatch(t, want, doc.Properties.Status.Enum, "Provider.status enum must be exactly six values")
	assert.Len(t, doc.Properties.Status.Enum, 6)
}
