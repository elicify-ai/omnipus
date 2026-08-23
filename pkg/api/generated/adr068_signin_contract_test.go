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

// ── ADR-068 own contract files (T068-06) ─────────────────────────────────────
// Traces to:
//   contracts/components/schemas/SignInStartResponse.yaml      (FR-009, MIN-005)
//   contracts/components/schemas/SignInStatus.yaml             (FR-009, MAJ-006)
//   contracts/components/schemas/OnboardingProviderApiKey.yaml (MAJ-014)
//   contracts/components/schemas/OnboardingProviderSignIn.yaml (MAJ-014)
//   contracts/components/schemas/OnboardingCompleteRequest.yaml (provider oneOf)
//   contracts/components/schemas/ProbeProviderResponse.yaml    (FR-036 probed_model)
//   contracts/components/schemas/Provider.yaml                 (FR-038 / SC-012)

// Generated enum const names (oapi-codegen v2.7.0 drops the type prefix when
// the value is unique across the spec): CliLogin, NotSignedIn, SignedIn,
// Expired. The union accessors are Discriminator() /
// AsOnboardingProviderApiKey() / AsOnboardingProviderSignIn().

// SignInStartResponse — {method: "cli_login", instructions, command}; no
// device-code fields, no other method value (spec: "the schema has no other
// `method` value").

func TestContract_SignInStartResponse_Populated(t *testing.T) {
	mustPassComponent(t, "SignInStartResponse", SignInStartResponse{
		Method:       CliLogin,
		Command:      "codex login",
		Instructions: "Run `codex login` in a terminal, then click Check sign-in.",
	})
}

func TestContract_SignInStartResponse_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInStartResponse", SignInStartResponse{},
		"method const, command and instructions are all required and non-empty")
}

func TestContract_SignInStartResponse_OtherMethodRejected(t *testing.T) {
	raw := []byte(`{"method":"device_code","command":"codex login","instructions":"x"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponse", raw),
		"cli_login is the only method value (MIN-005)")
}

func TestContract_SignInStartResponse_DeviceCodeFieldsRejected(t *testing.T) {
	raw := []byte(`{"method":"cli_login","command":"codex login","instructions":"x","user_code":"ABCD-EFGH"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStartResponse", raw),
		"no device-code fields — additionalProperties: false")
}

// SignInStatus — {state: not_signed_in|signed_in|expired, account_label?,
// expires_at?}; no `pending`.

func TestContract_SignInStatus_AllStates(t *testing.T) {
	for _, st := range []SignInStatusState{NotSignedIn, SignedIn, Expired} {
		mustPassComponent(t, "SignInStatus", SignInStatus{State: st})
	}
}

func TestContract_SignInStatus_Populated(t *testing.T) {
	label := "user_abc123"
	exp := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mustPassComponent(t, "SignInStatus", SignInStatus{
		State:        SignedIn,
		AccountLabel: &label,
		ExpiresAt:    &exp,
	})
}

func TestContract_SignInStatus_ZeroValue(t *testing.T) {
	mustFailComponent(t, "SignInStatus", SignInStatus{}, "state is required")
}

func TestContract_SignInStatus_PendingRejected(t *testing.T) {
	raw := []byte(`{"state":"pending"}`)
	assert.Error(t, validateAgainstComponentSchemaRawJSON(t, "SignInStatus", raw),
		"there is no pending state (MIN-005)")
}

func TestContract_SignInStatus_AccountLabelOver128Rejected(t *testing.T) {
	label := strings.Repeat("a", 129)
	mustFailComponent(t, "SignInStatus", SignInStatus{State: SignedIn, AccountLabel: &label},
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
