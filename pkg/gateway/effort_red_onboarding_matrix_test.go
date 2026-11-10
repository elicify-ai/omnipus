package gateway

// CHECK-12/16 / S-61: the real completion handler, both auth modes and both
// generated variants. Only the key-probe HTTP peer is scripted. The existing
// write registry observes distinct commits; no test callback persists effort.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type effortOnboardingMatrix struct {
	api            *restAPI
	home, endpoint string
	platform       bool
}

func newEffortOnboardingMatrix(t *testing.T, platform, validate bool) *effortOnboardingMatrix {
	t.Helper()
	f := &effortOnboardingMatrix{platform: platform}
	if platform {
		withEdition(t, config.EditionHosted)
		f.api, f.home = newAuthMethodOnboardingAPI(t)
		f.endpoint = startFakeProviderUpstream(t)
		// The platform account already exists before completion. Completion
		// must preserve this exact row, not create a second local account.
		user := config.UserConfig{Username: "operator@example.com"}
		f.api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{user}
		m := diskConfig(t, f.home)
		m["gateway"] = map[string]any{"users": []any{map[string]any{"username": user.Username}}}
		raw, err := json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(f.home, "config.json"), raw, 0600))
	} else {
		e := newLocalOnboardEnv(t)
		f.api, f.home, f.endpoint = e.api, e.home, e.upstream
	}
	f.api.agentLoop.GetConfig().Gateway.ValidateInbound = validate
	f.api.selfWriteReg = &configSelfWriteRegistry{hashes: make(map[[32]byte]struct{})}
	return f
}

func (f *effortOnboardingMatrix) body(t *testing.T, method string, value any, present bool) string {
	t.Helper()
	provider := map[string]any{"auth_method": method, "id": "openai-chatgpt", "model": "gpt-5"}
	if method == "api_key" {
		provider = map[string]any{"auth_method": method, "id": "openai", "model": "gpt-4o", "api_key": "test-placeholder", "endpoint": f.endpoint}
	}
	if present {
		provider["reasoning_effort"] = value
	}
	body := map[string]any{"provider": provider, "preferences": map[string]any{"name": "Effort test", "tone": "direct", "detail": "brief"}}
	if !f.platform {
		body["admin"] = map[string]any{"username": "effort-admin", "password": "test-password-7731"}
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return string(raw)
}

func (f *effortOnboardingMatrix) post(t *testing.T, body string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "203.0.113.77:54321"
	if f.platform && authenticated {
		r = signedIn(r)
	}
	w := httptest.NewRecorder()
	f.api.HandleCompleteOnboarding(w, r)
	return w
}

func TestT_GW_S61_BothModesVariantsAtomicPairAndAccount(t *testing.T) {
	for _, platform := range []bool{false, true} {
		for _, method := range []string{"api_key", "sign_in"} {
			for _, choice := range []string{"absent", "high"} {
				for _, validate := range []bool{false, true} {
					t.Run(fmt.Sprintf("platform=%v/%s/%s/validate=%v", platform, method, choice, validate), func(t *testing.T) {
						f := newEffortOnboardingMatrix(t, platform, validate)
						before := diskConfig(t, f.home)["gateway"]
						w := f.post(t, f.body(t, method, "high", choice == "high"), true)
						require.Equal(t, http.StatusOK, w.Code, "S-61 valid effort belongs on BOTH variants: %s", w.Body.String())
						m, loaded := readOnboardedConfig(t, f.home)
						provider, model := "openai-chatgpt", "gpt-5"
						if method == "api_key" {
							provider, model = "openai", "gpt-4o"
						}
						want := map[string]any{"provider": provider, "model": model}
						if choice == "high" {
							want["reasoning_effort"] = "high"
						}
						assert.Equal(t, want, egwDiskDefaultModel(t, f.home))
						raw, err := json.Marshal(loaded.Agents.Defaults.DefaultModel)
						require.NoError(t, err)
						var reopened map[string]any
						require.NoError(t, json.Unmarshal(raw, &reopened))
						assert.Equal(t, want, reopened, "reopened codec retains exact pair and effort")
						assert.NotContains(t, providerRow(t, m, provider, model), "reasoning_effort")
						var response map[string]any
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
						wantWrites := 1 // The existing single completion config mutation.
						if platform {
							assert.Equal(t, before, m["gateway"], "existing platform account is untouched")
							assert.NotContains(t, response, "token")
							assert.Equal(t, "operator@example.com", response["username"])
						} else {
							// Local onboarding additionally records its session cookie;
							// that existing write is not a second default-model write.
							wantWrites = 2
							users := effortRequireType[[]any](t, effortRequireType[map[string]any](t, m["gateway"])["users"])
							require.Len(t, users, 1)
							assert.Equal(t, "effort-admin", effortRequireType[map[string]any](t, users[0])["username"])
							assert.NotEmpty(t, effortRequireType[map[string]any](t, users[0])["password_hash"])
							assert.Equal(t, "effort-admin", response["username"])
							assert.NotEmpty(t, response["token"])
						}
						f.api.selfWriteReg.mu.Lock()
						writes := len(f.api.selfWriteReg.hashes)
						f.api.selfWriteReg.mu.Unlock()
						assert.Equal(t, wantWrites, writes, "no extra post-completion effort/default write")
					})
				}
			}
		}
	}
}

func TestT_GW_S61_RefusalsAreAtomicAcrossModesAndVariants(t *testing.T) {
	for _, platform := range []bool{false, true} {
		for _, method := range []string{"api_key", "sign_in"} {
			for _, validate := range []bool{false, true} {
				for _, bad := range []struct {
					name  string
					value any
				}{{"null", nil}, {"number", 7}, {"over32", strings.Repeat("x", 33)}} {
					t.Run(fmt.Sprintf("platform=%v/%s/%s/validate=%v", platform, method, bad.name, validate), func(t *testing.T) {
						f := newEffortOnboardingMatrix(t, platform, validate)
						path := filepath.Join(f.home, "config.json")
						before, err := os.ReadFile(path)
						require.NoError(t, err)
						w := f.post(t, f.body(t, method, bad.value, true), true)
						assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
						assert.Contains(t, w.Body.String(), "reasoning_effort")
						if !validate {
							var payload map[string]any
							require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
							switch bad.name {
							case "null":
								assert.Equal(t, `reasoning_effort must be a string; send "" to clear it`, payload["error"])
							case "over32":
								assert.Equal(t, "reasoning_effort must be at most 32 characters", payload["error"])
							case "number":
								assert.Contains(t, payload["error"], "cannot unmarshal number", "strict string decode, not unknown-field refusal")
								assert.Contains(t, payload["error"], "string")
							}
						}
						after, err := os.ReadFile(path)
						require.NoError(t, err)
						assert.Equal(t, before, after, "no provider/default/admin side effect on refusal")
						assert.False(t, f.api.onboardingMgr.IsComplete())
					})
				}
			}
		}
	}
}

func TestT_GW_S61_InvalidPlacementsKeepWholeTransactionUnchanged(t *testing.T) {
	for _, platform := range []bool{false, true} {
		for _, validate := range []bool{false, true} {
			for _, placement := range []string{"wrapper-effort", "sign-in-key", "api-key-device-code"} {
				t.Run(fmt.Sprintf("platform=%v/validate=%v/%s", platform, validate, placement), func(t *testing.T) {
					f := newEffortOnboardingMatrix(t, platform, validate)
					method := "api_key"
					if placement == "sign-in-key" {
						method = "sign_in"
					}
					var body map[string]any
					require.NoError(t, json.Unmarshal([]byte(f.body(t, method, nil, false)), &body))
					field := "reasoning_effort"
					switch placement {
					case "wrapper-effort":
						body[field] = "high"
					case "sign-in-key":
						field = "api_key"
						effortRequireType[map[string]any](t, body["provider"])[field] = "test-placeholder"
					case "api-key-device-code":
						field = "device_code"
						effortRequireType[map[string]any](t, body["provider"])[field] = "test-device-code"
					}
					raw, err := json.Marshal(body)
					require.NoError(t, err)
					before, err := os.ReadFile(filepath.Join(f.home, "config.json"))
					require.NoError(t, err)
					w := f.post(t, string(raw), true)
					assert.Equal(t, http.StatusBadRequest, w.Code)
					assert.Contains(t, w.Body.String(), field)
					if !validate && placement == "sign-in-key" {
						var payload map[string]any
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
						assert.Equal(t, `field not allowed on provider auth_method "sign_in": json: unknown field "api_key" — see the OnboardingProviderSignIn schema`, payload["error"])
					}
					after, err := os.ReadFile(filepath.Join(f.home, "config.json"))
					require.NoError(t, err)
					assert.Equal(t, before, after)
					assert.False(t, f.api.onboardingMgr.IsComplete())
				})
			}
		}
	}
}

func TestT_GW_S61_AuthorityPinsWithValidEffortShape(t *testing.T) {
	t.Run("platform requires preexisting sign-in", func(t *testing.T) {
		f := newEffortOnboardingMatrix(t, true, false)
		before, err := os.ReadFile(filepath.Join(f.home, "config.json"))
		require.NoError(t, err)
		w := f.post(t, f.body(t, "api_key", "high", true), false)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.JSONEq(t, `{"error":"authentication required"}`, w.Body.String())
		after, err := os.ReadFile(filepath.Join(f.home, "config.json"))
		require.NoError(t, err)
		assert.Equal(t, before, after)
	})
	t.Run("local still requires admin block", func(t *testing.T) {
		f := newEffortOnboardingMatrix(t, false, false)
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(f.body(t, "api_key", "high", true)), &body))
		delete(body, "admin")
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		w := f.post(t, string(raw), false)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Empty(t, egwDiskDefaultModel(t, f.home))
		assert.False(t, f.api.onboardingMgr.IsComplete())
	})
}
