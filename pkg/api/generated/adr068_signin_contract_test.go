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
// Traces to:
//   contracts/components/schemas/SignInStartResponseCliLogin.yaml   (FR-008)
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
// SignInStartResponse became a discriminated union (ADR-034 pattern, same as
// AgentCreateRequest) 2026-08-23: cli_login for codex-cli/github-copilot,
// device_code for openai-chatgpt (and xai once configured) — there is no
// longer a flat "SignInStartResponse.yaml" component file to validate
// against directly; test each variant file instead (mirrors the
// AgentCreateRequestMain/Subagent/Subagent3p section below). SignInStatus's
// state enum gained `pending`.
//
// Generated enum const names (oapi-codegen v2.7.0 drops the type prefix when
// the value is unique across the spec, and qualifies EVERY sibling of a type
// once any one value collides elsewhere in the spec — `pending` collides, so
// all four SignInStatusState members are qualified now, unlike before):
// CliLogin, DeviceCode, SignInStatusState{NotSignedIn,Pending,SignedIn,Expired}.
// The union accessors are Discriminator() / AsSignInStartResponseCliLogin() /
// AsSignInStartResponseDeviceCode() / AsOnboardingProviderApiKey() /
// AsOnboardingProviderSignIn().

func TestContract_SignInStartResponseCliLogin_Populated(t *testing.T) {
	mustPassComponent(t, "SignInStartResponseCliLogin", SignInStartResponseCliLogin{
		Method:       CliLogin,
		Command:      "codex login",
		Instructions: "Run `codex login` in a terminal, then click Check sign-in.",
	})
}

func TestContract_SignInStartResponseCliLogin_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInStartResponseCliLogin", SignInStartResponseCliLogin{},
		"method const, command and instructions are all required and non-empty")
}

func TestContract_SignInStartResponseCliLogin_DeviceCodeFieldsRejected(t *testing.T) {
	raw := []byte(`{"method":"cli_login","command":"codex login","instructions":"x","user_code":"ABCD-EFGH"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseCliLogin", raw),
		"no device-code fields — additionalProperties: false")
}

func TestContract_SignInStartResponseCliLogin_WrongDiscriminatorRejected(t *testing.T) {
	raw := []byte(`{"method":"device_code","command":"codex login","instructions":"x"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseCliLogin", raw),
		"method is pinned to the cli_login literal on this variant")
}

func TestContract_SignInStartResponseDeviceCode_Populated(t *testing.T) {
	exp := time.Date(2026, 8, 23, 12, 15, 0, 0, time.UTC)
	mustPassComponent(t, "SignInStartResponseDeviceCode", SignInStartResponseDeviceCode{
		Method:          DeviceCode,
		VerificationUrl: "https://auth.openai.com/codex/device",
		UserCode:        "WDJB-MJHT",
		DeviceAuthId:    "das_9f3a2b1c",
		ExpiresAt:       exp,
		IntervalSeconds: 5,
	})
}

func TestContract_SignInStartResponseDeviceCode_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInStartResponseDeviceCode", SignInStartResponseDeviceCode{},
		"every field is required on this variant")
}

func TestContract_SignInStartResponseDeviceCode_IntervalOutOfRangeRejected(t *testing.T) {
	raw := []byte(`{"method":"device_code","verification_url":"https://x.example/device","user_code":"AB-12","device_auth_id":"das_1","expires_at":"2026-08-23T12:15:00Z","interval_seconds":31}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseDeviceCode", raw),
		"interval_seconds maximum is 30")
}

func TestContract_SignInStartResponseDeviceCode_WrongDiscriminatorRejected(t *testing.T) {
	raw := []byte(`{"method":"cli_login","verification_url":"https://x.example/device","user_code":"AB-12","device_auth_id":"das_1","expires_at":"2026-08-23T12:15:00Z","interval_seconds":5}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponseDeviceCode", raw),
		"method is pinned to the device_code literal on this variant")
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
// account_label?, expires_at?}.

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
		Id:         "codex-cli",
		Model:      &model,
	})
}

func TestContract_OnboardingProviderSignIn_ApiKeyRejected(t *testing.T) {
	raw := []byte(`{"auth_method":"sign_in","id":"codex-cli","api_key":"k"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingProviderSignIn", raw),
		"api_key not allowed with sign_in")
}

// OnboardingCompleteRequest — the wrapper's provider is the oneOf; a body
// without auth_method matches neither variant.

func onboardingAdminJSON() string {
	return `"admin":{"username":"admin","password":"s3cr3tpassword"}`
}

func TestContract_OnboardingCompleteRequest_ApiKeyVariantPasses(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"api_key","id":"anthropic","api_key":"sk-ant","model":"claude-sonnet-4-6"},` + onboardingAdminJSON() + `}`)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw))
}

func TestContract_OnboardingCompleteRequest_SignInVariantPasses(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"sign_in","id":"codex-cli","model":"gpt-5.4"},` + onboardingAdminJSON() + `}`)
	assert.NoError(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw))
}

func TestContract_OnboardingCompleteRequest_MissingAuthMethodRejected(t *testing.T) {
	raw := []byte(`{"provider":{"id":"anthropic","api_key":"sk-ant"},` + onboardingAdminJSON() + `}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw),
		"auth_method discriminator is required — the historical omit default is retired")
}

func TestContract_OnboardingCompleteRequest_SignInWithKeyRejected(t *testing.T) {
	raw := []byte(`{"provider":{"auth_method":"sign_in","id":"codex-cli","api_key":"k"},` + onboardingAdminJSON() + `}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "OnboardingCompleteRequest", raw),
		"api_key not allowed with sign_in")
}

func TestContract_OnboardingCompleteRequest_UnionRoundTrip(t *testing.T) {
	// The generated wrapper must carry the variant through the union accessors.
	raw := []byte(`{"provider":{"auth_method":"sign_in","id":"codex-cli"},` + onboardingAdminJSON() + `}`)
	var req OnboardingCompleteRequest
	require.NoError(t, json.Unmarshal(raw, &req))
	disc, err := req.Provider.Discriminator()
	require.NoError(t, err)
	assert.Equal(t, "sign_in", disc)
	v, err := req.Provider.AsOnboardingProviderSignIn()
	require.NoError(t, err)
	assert.Equal(t, "codex-cli", v.Id)
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
