package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D2b's oracle is the dispatcher's 2026-10-06 ruling, not the base's behaviour:
// completing a root turn is not archiving its chat, and a scheduled/heartbeat
// entry may revive COMPLETED (never STOPPED) into one new round. ADR-093 D4
// still supplies the human-continuation and immutable-history invariants.
const d2bReply = "The requested D2b round finished."

// The only double is the paid provider. Admission, registration, execution,
// disposition, transcripts, and both stores stay real. Reading at this seam
// proves the new round was admitted BEFORE work, rather than repaired afterwards.
type d2bProviderObservation struct {
	record        *session.LifecycleRecord
	readErr       error
	registered    bool
	userInitiated bool
	userID        string
	autoDenyAsk   bool
	lastUser      string
	job           ScheduledJobInfo
	jobPresent    bool
}

type d2bProvider struct {
	al           *AgentLoop
	id           string
	mu           sync.Mutex
	observations []d2bProviderObservation
}

func (p *d2bProvider) GetDefaultModel() string { return "d2b-provider-seam" }

func (p *d2bProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	o := d2bProviderObservation{}
	// Reopen the durable store; do not infer admission from a cached record.
	o.record, o.readErr = session.NewLifecycleStore(p.al.GetSessionLifecycleStore().Dir()).Load(p.id)
	// Ordinary turns register under the owner-qualified session key, not the
	// raw session ID used by steered dispatch.
	key := fmt.Sprintf("agent:%s:session:%s", testDefaultAgentID, p.id)
	if ts := p.al.getActiveTurnState(key); ts != nil {
		ts.mu.RLock()
		o.registered = true
		o.userInitiated = ts.opts.UserInitiated
		o.userID = ts.opts.UserID
		o.autoDenyAsk = ts.opts.AutoDenyAsk
		ts.mu.RUnlock()
	}
	for _, m := range messages {
		if m.Role == "user" {
			o.lastUser = m.Content
		}
	}
	o.job, o.jobPresent = scheduledJobContextFrom(ctx)
	p.mu.Lock()
	p.observations = append(p.observations, o)
	p.mu.Unlock()
	return &providers.LLMResponse{Content: d2bReply}, nil
}

func (p *d2bProvider) calls() []d2bProviderObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]d2bProviderObservation(nil), p.observations...)
}

type d2bRoot struct {
	al       *AgentLoop
	id       string
	provider *d2bProvider
	prior    []session.TranscriptEntry
}

func newD2bRoot(t *testing.T) *d2bRoot {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t) // real messaging stores and a genuinely minted boot epoch
	wireSteerCompletionDeps(t, al)
	id := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	owner, title := "d2b-owner", "D2b reusable chat"
	require.NoError(t, al.GetSessionStore().SetMeta(id, session.MetaPatch{Owner: &owner, Title: &title}))
	p := &d2bProvider{al: al, id: id}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "SETUP: real root agent must exist")
	inst.Provider = p
	h := &d2bRoot{al: al, id: id, provider: p}
	h.prior = []session.TranscriptEntry{
		{ID: "d2b-prior-question", Role: "user", AgentID: testDefaultAgentID, Content: "Keep this earlier question.", Timestamp: time.Now().UTC()},
		{ID: "d2b-prior-answer", Role: "assistant", AgentID: testDefaultAgentID, Content: "Keep this earlier answer.", Timestamp: time.Now().UTC()},
	}
	for _, entry := range h.prior {
		require.NoError(t, al.GetSessionStore().AppendTranscript(id, entry))
	}
	return h
}

// Seed the requested prior state through the real store, independently of the
// missing normal-completion writer. Otherwise that first defect would mask all
// completed-root admission tests. This is a saved-state fixture, not a hook.
func (h *d2bRoot) seedState(t *testing.T, state session.LifecycleState) {
	t.Helper()
	meta, err := h.al.GetSessionStore().GetMeta(h.id)
	require.NoError(t, err)
	rec := &session.LifecycleRecord{
		SessionID: h.id, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, OwnerScopeID: meta.Owner,
		// session-core U1 / DEL-11: the record's acting agent is the session's
		// IMMUTABLE owner; ActiveAgentID (the retired handover owner) is empty
		// on any session that was never switched.
		WorkspaceID: meta.WorkspaceID, AgentID: meta.AgentID,
		Origin: &session.Origin{Kind: session.OriginKindChat},
	}
	ls := h.al.GetSessionLifecycleStore()
	require.NoError(t, ls.Persist(rec))
	require.NoError(t, ls.Mutate(h.id, func(saved *session.LifecycleRecord) error {
		saved.State = state
		if state == session.LifecycleStopped {
			saved.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:d2b-owner", Seq: 1, Cause: session.StopCauseStop}
		}
		return nil
	}))
	require.Equal(t, state, h.load(t).State, "SETUP: saved prior state must be real")
}

func (h *d2bRoot) load(t *testing.T) *session.LifecycleRecord {
	t.Helper()
	rec, err := session.NewLifecycleStore(h.al.GetSessionLifecycleStore().Dir()).Load(h.id)
	require.NoError(t, err, "root's durable lifecycle must remain readable")
	return rec
}

func (h *d2bRoot) journal(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.al.GetSessionLifecycleStore().Dir(), h.id+".jsonl"))
	require.NoError(t, err)
	return raw
}

func (h *d2bRoot) humanTurn(t *testing.T, content string) (string, error) {
	t.Helper()
	// The web gateway persists the human entry before processMessage. Use that
	// real store write; do not bypass ordinary admission by calling runTurn.
	require.NoError(t, h.al.GetSessionStore().AppendTranscript(h.id, session.TranscriptEntry{
		ID: "d2b-human-request", Role: "user", AgentID: testDefaultAgentID,
		Content: content, Timestamp: time.Now().UTC(),
	}))
	msg := bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-" + h.id, Content: content,
		SessionID: h.id, SessionKey: fmt.Sprintf("agent:%s:session:%s", testDefaultAgentID, h.id),
		GatewayUserID: "d2b-owner", UserInitiated: true,
		Sender: bus.SenderInfo{CanonicalID: "human:d2b-owner"},
	}
	reply, _, err := h.al.processMessage(context.Background(), msg)
	return reply, err
}

func (h *d2bRoot) scheduledTurn(kind, content string) (string, error) {
	name := "D2b recurring job"
	if kind == "heartbeat" {
		name = "heartbeat:" + testHarnessWorkspaceMembershipID + ":" + testDefaultAgentID
	}
	ctx := WithScheduledJobContext(context.Background(), "d2b-"+kind, name)
	// The production gateway runner uses empty delivery addresses for BOTH
	// job kinds. They enter ProcessScheduled, not a forged human message.
	return h.al.ProcessScheduled(ctx, testDefaultAgentID, h.id, content, "", "")
}

func (h *d2bRoot) assertHistoryPreserved(t *testing.T, before []byte) {
	t.Helper()
	assert.True(t, bytes.HasPrefix(h.journal(t), before), "D2b/ADR-093: new round must append, not rewrite prior lifecycle history")
	entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), len(h.prior), "same chat must retain its earlier conversation")
	assert.Equal(t, h.prior, entries[:len(h.prior)], "D2b: earlier conversation must be preserved exactly")
}
