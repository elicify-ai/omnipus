package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

const (
	// Synthetic transport authentication only; never a real user's credential.
	cardMatrixHTTPBearer = "FAKE-WU4-HTTP-TEST-AUTH"
	cardMatrixHTTPValue  = "FAKE-WU4-K5-SECRET"
)

type cardMatrixHTTP struct {
	api   *restAPI
	mux   *http.ServeMux
	al    *agent.AgentLoop
	bus   *bus.MessageBus
	store *credentials.Store
	rec   secretrequest.Record
}

// The real authenticated K5 route, real registry and real disk stores are
// under test. No agent loop is started: its real bus is an observable process
// edge, so no-resume assertions are deterministic rather than timed sleeps.
func newCardMatrixHTTP(t *testing.T, input secretrequest.Input, prepare func(*config.Config)) *cardMatrixHTTP {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", cardMatrixHTTPBearer)
	withEdition(t, config.EditionCore)
	cfg := secretCardAgentConfig("omnipus", "Omnipus", false)
	cfg.Gateway.DevModeBypass = false
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	if prepare != nil {
		prepare(cfg)
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x33}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	meta, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	owner := "_env_token" // identity of the REAL authenticated fallback, auth.go
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{Owner: &owner}))
	if input.Purpose == "" {
		input = secretrequest.Input{Purpose: "OpenAI key", Endpoint: secretCardEndpoint}
	}
	rec, err := al.SecretRequests().Create(context.Background(), meta.ID, "omnipus", "http-card", "", input)
	require.NoError(t, err)
	api := &restAPI{agentLoop: al, homePath: home, credStore: store, allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return &cardMatrixHTTP{api: api, mux: mux, al: al, bus: msgBus, store: store, rec: rec}
}

func (f *cardMatrixHTTP) call(t *testing.T, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/secret-requests/" + f.rec.Card.RequestId
	if method == http.MethodPost {
		path += "/submit"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cardMatrixHTTPBearer)
	req.RemoteAddr = uniqueTestSourceIP()
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, f.al.GetConfig()))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	secretRouteBlocked(t, rec, method+" K5", "FR-079/084, BDD-6.16")
	return rec
}

func (f *cardMatrixHTTP) valuesBody(t *testing.T, values map[string]string) string {
	t.Helper()
	raw, err := json.Marshal(gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	return string(raw)
}

func requireCardMatrixState(t *testing.T, response *httptest.ResponseRecorder, status int, id string, state gen.SecretRequestStateState) gen.SecretRequestState {
	t.Helper()
	require.Equal(t, status, response.Code, "K5 response: %s", response.Body.String())
	var got gen.SecretRequestState
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
	require.Equal(t, id, got.RequestId)
	require.Equal(t, state, got.State)
	require.False(t, got.UpdatedAt.IsZero(), "state timestamp is required by K5")
	if state != gen.SecretRequestStateStateSubmitted {
		require.Nil(t, got.ReferenceNames, "non-submitted states never carry references")
	}
	require.NotContains(t, response.Body.String(), cardMatrixHTTPValue)
	return got
}

func (f *cardMatrixHTTP) requireNoCommitOrResume(t *testing.T) {
	t.Helper()
	names, err := f.store.List()
	require.NoError(t, err)
	require.Empty(t, names)
	_, exists, err := f.store.CardReceipt(f.rec.Card.RequestId)
	require.NoError(t, err)
	require.False(t, exists)
	select {
	case msg := <-f.bus.InboundChan():
		t.Fatalf("refused submit dispatched an inbound: %+v", msg)
	default:
	}
}

func (f *cardMatrixHTTP) takeResume(t *testing.T) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-f.bus.InboundChan():
		require.Equal(t, f.rec.Card.SessionId, msg.SessionID)
		require.Equal(t, "webchat", msg.Channel)
		require.Equal(t, f.rec.Card.RequestId, msg.Metadata["secret_request_resume"])
		require.NotContains(t, msg.Content, cardMatrixHTTPValue)
		return msg
	default:
		t.Fatal("committed card did not enqueue its reference-only continuation")
	}
	return bus.InboundMessage{}
}
