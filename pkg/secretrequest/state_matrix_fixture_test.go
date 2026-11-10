package secretrequest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Oracles for this pack were derived before reading the implementation:
// omnipus-system-agent-spec: BDD-6.3–6.5/6.9–6.14/6.16–6.18/6.24,
// FR-063/066/067/069/071/078/079/087, datasets D1/D2.
// Chief ruling 2026-10-16: no request expiry; consumption is internal only.
// This pack owns no production files; GREEN and mutations belong to CHECK.
const (
	matrixOwner     = "card-owner"
	matrixWorkspace = "sales"
	matrixPurpose   = "OpenAI key"
	matrixEndpoint  = "https://api.openai.com/v1"
	matrixValue     = "FAKE-WU4-STATE-VALUE"
)

type matrixEnv struct {
	t         *testing.T
	home      string
	cfg       *config.Config
	store     *credentials.Store
	sessions  *session.UnifiedStore
	reg       *secretrequest.Registry
	stopped   map[string]bool
	attempts  []secretrequest.Record
	resumeErr error
}

func newMatrixEnv(t *testing.T) *matrixEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	sessions, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	// This unrelated stats timer must not wake millions of times during the
	// virtual-clock no-expiry test. No secret-request timer is changed.
	sessions.SetStatsFlushInterval(100 * 365 * 24 * time.Hour)
	t.Cleanup(func() { require.NoError(t, sessions.Close()) })
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x44}, 32)))
	t.Cleanup(store.Close)
	f := &matrixEnv{t: t, home: home, sessions: sessions, store: store, stopped: make(map[string]bool)}
	f.cfg = &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore},
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
		{ID: "jim", Name: "Jim", Type: config.AgentTypeCore},
		{ID: "helper", Name: "Setup helper", Type: config.AgentTypeWorker},
	}}}
	f.reg = secretrequest.NewRegistry(f.options())
	return f
}

func (f *matrixEnv) options() secretrequest.Options {
	return secretrequest.Options{
		Sessions:    f.sessions,
		Config:      func() *config.Config { return f.cfg },
		Credentials: func() (*credentials.Store, error) { return f.store, nil },
		Stopped: func(id string) (bool, error) {
			if _, err := f.sessions.GetMeta(id); err != nil {
				return false, err
			}
			return f.stopped[id], nil
		},
		// Fake only the continuation transport, not the registry or durable
		// consumer. Tests explicitly drive the REAL ClaimResume boundary.
		Resume: func(rec secretrequest.Record) error {
			f.attempts = append(f.attempts, rec)
			return f.resumeErr
		},
	}
}

func (f *matrixEnv) newSession(agentID, parent string) string {
	f.t.Helper()
	kind := session.SessionTypeChat
	if parent != "" {
		kind = session.SessionTypeDelegate
	}
	meta, err := f.sessions.NewSession(kind, "webchat", agentID)
	require.NoError(f.t, err)
	owner, workspace := matrixOwner, matrixWorkspace
	patch := session.MetaPatch{Owner: &owner, WorkspaceID: &workspace}
	if parent != "" {
		patch.ParentSessionID = &parent
	}
	require.NoError(f.t, f.sessions.SetMeta(meta.ID, patch))
	return meta.ID
}

func (f *matrixEnv) create(sessionID, agentID string, input secretrequest.Input) secretrequest.Record {
	f.t.Helper()
	rec, err := f.reg.Create(context.Background(), sessionID, agentID, "web-card", "", input)
	require.NoError(f.t, err, "fixture must reach a real pending card")
	require.Equal(f.t, "pending", rec.Card.State)
	return rec
}

func (f *matrixEnv) pending() secretrequest.Record {
	f.t.Helper()
	return f.create(f.newSession("omnipus", ""), "omnipus", providerInput())
}

func providerInput() secretrequest.Input {
	return secretrequest.Input{Purpose: matrixPurpose, Endpoint: matrixEndpoint}
}

func valuesInput(values map[string]string) gen.SecretSubmitRequest {
	return gen.SecretSubmitRequest{Values: &values}
}

func cancelInput() gen.SecretSubmitRequest {
	cancel := gen.SecretSubmitRequestCancelTrue
	return gen.SecretSubmitRequest{Cancel: &cancel}
}

func submitMatrix(t *testing.T, f *matrixEnv, rec secretrequest.Record) secretrequest.Record {
	t.Helper()
	out, err := f.reg.Submit(rec.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": matrixValue}))
	require.NoError(t, err)
	require.Equal(t, "submitted", out.Card.State, "FR-063: committed request is submitted")
	require.Len(t, out.Card.ReferenceNames, 1, "one requested field must produce exactly one reference")
	require.Regexp(t, `^.+\.v1$`, out.Card.ReferenceNames[0], "FR-063/072: a fresh family starts at version 1")
	names, err := f.store.List()
	require.NoError(t, err)
	require.Equal(t, out.Card.ReferenceNames, names, "submitted references must enumerate the complete committed write")
	receipt, exists, err := f.store.CardReceipt(rec.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, rec.Card.SessionId, receipt.SessionID)
	require.Equal(t, out.Card.ReferenceNames, receipt.References)
	return out
}

func requireMatrixFailure(t *testing.T, err error, status int, state string) {
	t.Helper()
	require.Error(t, err, "expected a value-free refusal")
	require.IsType(t, &secretrequest.RequestError{}, err, "refusal must have the exact concrete type, not a wrapped error")
	var failure *secretrequest.RequestError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, status, failure.Status)
	if state != "" {
		require.NotNil(t, failure.State, "409 must carry true current state (K5)")
		require.Equal(t, gen.SecretRequestStateState(state), failure.State.State)
	} else {
		require.Nil(t, failure.State)
	}
	require.NotContains(t, failure.Message, matrixValue)
}

func requireNoMatrixCommit(t *testing.T, f *matrixEnv, id string) {
	t.Helper()
	names, err := f.store.List()
	require.NoError(t, err)
	require.Empty(t, names, "refused/cancelled submit must store no values")
	_, exists, err := f.store.CardReceipt(id)
	require.NoError(t, err)
	require.False(t, exists, "no receipt may exist without a committed value")
	require.Empty(t, f.attempts, "refusal must not dispatch a continuation")
}

func requireValueFree(t *testing.T, label string, data any, sentinels ...string) {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "%s leak search must inspect evidence, not empty input", label)
	for _, sentinel := range sentinels {
		require.NotContains(t, string(raw), sentinel, "%s must not re-serve a submitted value", label)
	}
}

func requireMatrixNotFound(t *testing.T, reg *secretrequest.Registry, id string) {
	t.Helper()
	_, err := reg.Get(id, matrixOwner)
	requireMatrixFailure(t, err, http.StatusNotFound, "")
}
