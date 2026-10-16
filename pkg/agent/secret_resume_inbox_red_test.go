package agent

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

// FR-087 / BDD-6.24: PublishInbound succeeding is not proof that the session
// worker accepted the continuation. A rejected resume must remain retriable
// on attach without restarting the process, and must not be consumed twice.
// Only scheduling is controlled: both dispatch stages and the queue are real.
func TestSecretResume_FullWorkerInboxDoesNotStrandCommittedRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), DefaultModel: config.DefaultModel{Model: "test-model"}},
		List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore}},
	}}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x61}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	reg := al.SecretRequests()
	rec, err := reg.Create(context.Background(), meta.ID, "mia", "resume-inbox", "", secretrequest.Input{Purpose: "API key", Endpoint: "https://api.openai.com/v1"})
	require.NoError(t, err)
	values := map[string]string{"api_key": "FAKE-FULL-INBOX-VALUE"}
	out, err := reg.Submit(rec.Card.RequestId, "", gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err, "initial publication to bus has not yet faced worker admission")
	require.Equal(t, "submitted", out.Card.State)
	var resume bus.InboundMessage
	select {
	case resume = <-msgBus.InboundChan():
		require.Equal(t, rec.Card.RequestId, resume.Metadata[secretResumeMetadataKey])
	default:
		t.Fatal("fixture: production resume dispatcher did not publish to the bus")
	}
	scope := "agent:mia:session:" + meta.ID
	worker := newSessionWorker(scope, al, func() {})
	// No runLoop: deterministic full-inbox admission, not a timing race.
	al.sessionWorkers.Store(scope, worker)
	t.Cleanup(func() { al.sessionWorkers.Delete(scope); worker.cancel() })
	for range MaxQueueSize {
		require.True(t, worker.pushInbox(bus.InboundMessage{Channel: "webchat", SessionID: meta.ID, Content: "queued filler"}))
	}
	require.Equal(t, MaxQueueSize, worker.waitingInboxCount(), "capacity control")
	al.dispatchSessionWorker(scope, resume)
	// Free capacity and retain any continuation the production path chose to
	// preserve. These are real messages, not fabricated acceptance receipts.
	waiting := drainSecretProbeWorker(worker)
	var continuations []bus.InboundMessage
	for _, msg := range waiting {
		if msg.Metadata[secretResumeMetadataKey] == rec.Card.RequestId {
			continuations = append(continuations, msg)
		}
	}
	require.NoError(t, reg.RetryResumes(), "attach retries a submitted but unconsumed request")
	select {
	case retry := <-msgBus.InboundChan():
		require.Equal(t, rec.Card.RequestId, retry.Metadata[secretResumeMetadataKey])
		al.dispatchSessionWorker(scope, retry)
	default:
	}
	for _, msg := range drainSecretProbeWorker(worker) {
		if msg.Metadata[secretResumeMetadataKey] == rec.Card.RequestId {
			continuations = append(continuations, msg)
		}
	}
	require.Len(t, continuations, 1, "FR-087: full inbox must not drop the resume then keep inFlight forever; attach must recover exactly one executable continuation")
	require.Equal(t, rec.Card.SessionId, continuations[0].SessionID)
	require.NotContains(t, continuations[0].Content, values["api_key"])
	_, claimed, err := reg.ClaimResume(rec.Card.RequestId, meta.ID)
	require.NoError(t, err)
	require.True(t, claimed)
	_, claimed, err = reg.ClaimResume(rec.Card.RequestId, meta.ID)
	require.NoError(t, err)
	require.False(t, claimed)
}

func drainSecretProbeWorker(worker *sessionWorker) []bus.InboundMessage {
	worker.inboxMu.Lock()
	defer worker.inboxMu.Unlock()
	var waiting []bus.InboundMessage
	for {
		select {
		case msg := <-worker.inbox:
			waiting = append(waiting, msg)
		default:
			waiting = append(waiting, worker.overflow...)
			worker.overflow = nil
			return waiting
		}
	}
}
