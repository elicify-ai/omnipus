package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type signInRoundTripFunc func(*http.Request) (*http.Response, error)

func (f signInRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestSignInPoll_SignedInCreatesUsableProvider reproduces issue #799 at the
// user-visible boundary. A successful device-code poll must do more than save
// OAuth material: it must leave a listed provider row that the runtime factory
// can resolve, and a real model call through that resolved provider must use
// the encrypted-store credential successfully.
func TestSignInPoll_SignedInCreatesUsableProvider(t *testing.T) {
	api := newProbeAPI(t)
	signedOutCodexHome(t) // the device-code credential must stand on its own

	state := "signed_in"
	vendor := httptest.NewServer(deviceCodeVendorMux(t, &state))
	defer vendor.Close()
	withDeviceCodeVendor(t, vendor)

	startW := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in", nil)
	require.Equal(t, http.StatusOK, startW.Code, startW.Body.String())
	var startResp gen.SignInStartResponse
	require.NoError(t, json.Unmarshal(startW.Body.Bytes(), &startResp))
	deviceCode, err := startResp.AsSignInStartResponseDeviceCode()
	require.NoError(t, err)

	pollW := doJSON(t, api, http.MethodPost, "/api/v1/providers/openai-chatgpt/sign-in/poll",
		gen.SignInPollRequest{DeviceAuthId: deviceCode.DeviceAuthId})
	require.Equal(t, http.StatusOK, pollW.Code, pollW.Body.String())
	var pollResp gen.SignInPollResponse
	require.NoError(t, json.Unmarshal(pollW.Body.Bytes(), &pollResp))
	require.Equal(t, gen.SignInPollResponseStateSignedIn, pollResp.State)

	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))
	_, err = store.Get(credentials.OAuthEntryName("openai"))
	require.NoError(t, err, "the successful poll must persist one encrypted OAuth entry")

	cfg := api.agentLoop.GetConfig()
	var configured *config.ModelConfig
	for _, row := range cfg.Providers {
		if row.Provider == "openai-chatgpt" && row.AuthMethod == config.AuthMethodSignIn {
			configured = row
			break
		}
	}
	require.NotNil(t, configured,
		"a successful sign-in must create the configured row GET /providers and model resolution consume")
	require.NotEmpty(t, configured.Model, "the created row must name a resolvable model")

	listed := getProviders(t, api)
	require.Condition(t, func() bool {
		for _, row := range listed {
			if row.Id == "openai-chatgpt" {
				return true
			}
		}
		return false
	}, "OpenAI must appear in GET /providers immediately after sign-in")

	configBytes, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	if bytes.Contains(configBytes, []byte("vendor-access-token")) ||
		bytes.Contains(configBytes, []byte("vendor-refresh-token")) {
		t.Fatal("config.json contains OAuth token material; credentials must remain encrypted")
	}
	filtered := api.agentLoop.GetConfig().FilterSensitiveData(
		"vendor-access-token vendor-refresh-token",
	)
	if strings.Contains(filtered, "vendor-access-token") ||
		strings.Contains(filtered, "vendor-refresh-token") {
		t.Fatal("the post-sign-in runtime scrubber does not contain both OAuth tokens")
	}

	providers.SetDefaultCredentialStore(store)
	t.Cleanup(func() { providers.SetDefaultCredentialStore(nil) })

	oldTransport := http.DefaultTransport
	sawEncryptedStoreCredential := false
	http.DefaultTransport = signInRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "chatgpt.com" || req.URL.Path != "/backend-api/codex/responses" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("not found")),
				Request:    req,
			}, nil
		}
		if req.Header.Get("Authorization") != "Bearer vendor-access-token" {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("credential rejected")),
				Request:    req,
			}, nil
		}
		sawEncryptedStoreCredential = true
		body := "event: response.completed\n" +
			`data: {"type":"response.completed","sequence_number":1,"response":{"id":"resp_issue_799","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"usable"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}` +
			"\n\ndata: [DONE]\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	resolved, model, err := providers.CreateProviderFromConfig(configured)
	require.NoError(t, err, "the signed-in row must resolve to a runtime provider")
	resp, err := resolved.Chat(t.Context(), []providers.Message{{Role: "user", Content: "ping"}}, nil, model, map[string]any{})
	require.NoError(t, err, "a model call through the resolved provider must succeed")
	assert.Equal(t, "usable", resp.Content)
	assert.True(t, sawEncryptedStoreCredential,
		"the model call must authenticate from the encrypted OAuth entry")
}
