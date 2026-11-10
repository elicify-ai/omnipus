package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

// BDD-6.16's nontrivial rows use an existing REAL pending card, not an
// unknown-id 404 that could hide the very validation under test.
func TestSecretK5_NontrivialRefusalsHaveNoCommitOrResume(t *testing.T) {
	cases := []struct {
		event  string
		status int
		state  gen.SecretRequestStateState
	}{
		{"cancelled", http.StatusConflict, gen.SecretRequestStateStateCancelled},
		{"session_stopped", http.StatusConflict, gen.SecretRequestStateStateStopped},
		{"session_deleted", http.StatusNotFound, ""},
		{"agent_deleted", http.StatusConflict, gen.SecretRequestStateStateRefused},
		{"recipient_changed", http.StatusConflict, gen.SecretRequestStateStateRefused},
		{"store_write_failed", http.StatusInternalServerError, gen.SecretRequestStateStatePending},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			f := newCardMatrixHTTP(t, secretrequest.Input{Purpose: "OpenAI key", Provider: "openai"}, func(cfg *config.Config) {
				cfg.Providers = []*config.ModelConfig{{Provider: "openai", APIBase: secretCardEndpoint}}
			})
			// Passing control confirms authentication, routing and state decoding
			// before changing the state that MUST refuse the later submit.
			requireCardMatrixState(t, f.call(t, http.MethodGet, ""), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStatePending)
			switch tc.event {
			case "cancelled":
				requireCardMatrixState(t, f.call(t, http.MethodPost, `{"cancel":true}`), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateCancelled)
			case "session_stopped":
				result, err := f.al.StopSession(context.Background(), agent.StopRequest{
					SessionID: f.rec.Card.SessionId, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "_env_token"}, Channel: "webchat",
				})
				require.NoError(t, err)
				require.NoError(t, result.RootErr)
				require.Empty(t, result.Report.Unreachable)
			case "session_deleted":
				require.NoError(t, f.al.GetSessionStore().DeleteSession(f.rec.Card.SessionId))
			case "agent_deleted":
				require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error { cfg.Agents.List = nil; return nil }))
			case "recipient_changed":
				require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error { cfg.Providers[0].APIBase = "https://evil.example/v1"; return nil }))
			case "store_write_failed":
				require.NoError(t, os.Mkdir(f.store.Path(), 0o700))
			}
			response := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}))
			require.Equal(t, tc.status, response.Code, "body: %s", response.Body.String())
			require.NotContains(t, response.Body.String(), cardMatrixHTTPValue)
			if tc.status == http.StatusConflict {
				requireCardMatrixState(t, response, tc.status, f.rec.Card.RequestId, tc.state)
			}
			if tc.event == "store_write_failed" {
				require.NoError(t, os.Remove(f.store.Path()))
			}
			f.requireNoCommitOrResume(t)
			read := f.call(t, http.MethodGet, "")
			if tc.event == "session_deleted" {
				require.Equal(t, http.StatusNotFound, read.Code, "absence is never submitted success")
			} else {
				requireCardMatrixState(t, read, http.StatusOK, f.rec.Card.RequestId, tc.state)
			}
		})
	}
}

// BDD-6.10/6.7, FR-079/087: response/frame loss is recovered by the value-free
// K5 read. A replay cannot write a replacement or enqueue another resume.
func TestSecretK5_ReplayAndReadRemainSubmittedWithoutValues(t *testing.T) {
	f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
	body := f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue})
	response := f.call(t, http.MethodPost, body)
	saved := requireCardMatrixState(t, response, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
	require.NotNil(t, saved.ReferenceNames)
	require.Len(t, *saved.ReferenceNames, 1)
	resume := f.takeResume(t)
	require.Contains(t, resume.Content, (*saved.ReferenceNames)[0])
	before, err := os.ReadFile(f.store.Path())
	require.NoError(t, err)
	response = f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": "FAKE-REPLAY-NEW-VALUE"}))
	replayed := requireCardMatrixState(t, response, http.StatusConflict, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
	require.Equal(t, saved, replayed)
	require.NotContains(t, response.Body.String(), "FAKE-REPLAY-NEW-VALUE")
	after, err := os.ReadFile(f.store.Path())
	require.NoError(t, err)
	require.Equal(t, before, after, "duplicate is zero writes, including ciphertext")
	_, claimed, err := f.al.SecretRequests().ClaimResume(f.rec.Card.RequestId, f.rec.Card.SessionId)
	require.NoError(t, err)
	require.True(t, claimed)
	read := requireCardMatrixState(t, f.call(t, http.MethodGet, ""), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
	require.Equal(t, saved, read, "consumption is not a new wire state")
	require.NoError(t, f.al.SecretRequests().RetryResumes())
	select {
	case msg := <-f.bus.InboundChan():
		t.Fatalf("duplicate/recovery enqueued a second continuation: %+v", msg)
	default:
	}
	raw, err := json.Marshal(f.al.SecretRequests().Snapshot("_env_token", f.rec.Card.SessionId))
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	require.NotContains(t, string(raw), cardMatrixHTTPValue)
}

// BDD-6.9: a genuine Slack card has TWO server-chosen mandatory fields.
func TestSecretK5_SlackPartialSubmitIs400AndFullSubmitIsAtomic(t *testing.T) {
	f := newCardMatrixHTTP(t, secretrequest.Input{Purpose: "Connect Slack", Instance: "slack-1", Field: "bot_token"}, func(cfg *config.Config) {
		cfg.Channels = map[string]config.ChannelInstanceConfig{"slack-1": {Type: "slack"}}
	})
	require.Len(t, f.rec.Card.Fields, 2)
	partial := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"bot_token": "FAKE-SLACK-BOT"}))
	require.Equal(t, http.StatusBadRequest, partial.Code, "missing mandatory app_token: %s", partial.Body.String())
	f.requireNoCommitOrResume(t)
	requireCardMatrixState(t, f.call(t, http.MethodGet, ""), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStatePending)
	complete := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"bot_token": "FAKE-SLACK-BOT", "app_token": "FAKE-SLACK-APP"}))
	saved := requireCardMatrixState(t, complete, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
	require.NotNil(t, saved.ReferenceNames)
	require.Len(t, *saved.ReferenceNames, 2)
	names, err := f.store.List()
	require.NoError(t, err)
	require.ElementsMatch(t, *saved.ReferenceNames, names)
	values := make([]string, 0, 2)
	for _, ref := range names {
		value, err := f.store.Get(ref)
		require.NoError(t, err)
		values = append(values, value)
	}
	require.ElementsMatch(t, []string{"FAKE-SLACK-BOT", "FAKE-SLACK-APP"}, values)
	for _, value := range values {
		require.NotContains(t, complete.Body.String(), value)
	}
	f.takeResume(t)
}
