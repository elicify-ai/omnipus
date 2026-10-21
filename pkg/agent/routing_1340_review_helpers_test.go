package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Only the external provider is substituted. Selection, request hooks,
// recovery, error emission and durable transcript handling remain real.
type routing1340ReviewFixture struct {
	al        *AgentLoop
	rt        *agentLoopRunTurn
	rf        *agentLoopRunTurnFallbacks
	ri        *agentLoopRunTurnIteration
	rq        *agentLoopRunTurnRequest
	rr        *agentLoopRunTurnResponse
	rp        *agentLoopRunTurnPrepare
	rc        *agentLoopRunTurnConductor
	store     *session.UnifiedStore
	sessionID string
	mu        sync.Mutex
	events    []Event
}

func newRouting1340ReviewFixture(t *testing.T, cfg *config.Config, a *AgentInstance) *routing1340ReviewFixture {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	meta, err := store.NewSession(session.SessionTypeChat, "web", a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		cfg = &config.Config{}
	}
	f := &routing1340ReviewFixture{store: store, sessionID: meta.ID}
	f.al = &AgentLoop{cfg: cfg, eventBus: NewEventBus(), fallback: providers.NewFallbackChain(providers.NewCooldownTracker())}
	f.al.eventBus.SetSyncTap(func(e Event) { f.mu.Lock(); defer f.mu.Unlock(); f.events = append(f.events, e) })
	ts := newTurnState(a, processOptions{NoHistory: true}, turnEventScope{})
	ts.transcriptStore, ts.transcriptSessionID = store, meta.ID
	ts.userMessage = "hi"
	ts.channel = "webchat"
	f.rt = &agentLoopRunTurn{al: f.al, ts: ts, turnCtx: context.Background()}
	f.rf = &agentLoopRunTurnFallbacks{rt: f.rt, callMessages: []providers.Message{{Role: "user", Content: "hi"}}}
	f.ri = &agentLoopRunTurnIteration{rf: f.rf, cfg: cfg, messages: f.rf.callMessages}
	f.rq = &agentLoopRunTurnRequest{ri: f.ri}
	f.rr = &agentLoopRunTurnResponse{rq: f.rq}
	f.rc = &agentLoopRunTurnConductor{rx: &agentLoopRunTurnTools{rr: f.rr}}
	f.rp = &agentLoopRunTurnPrepare{rc: f.rc}
	return f
}

func (f *routing1340ReviewFixture) selectProvider(t *testing.T) {
	t.Helper()
	if flow := f.rp.selectTurnProvider(); flow != agentLoopRunTurnConductorNext {
		t.Fatalf("selection flow=%v, want next", flow)
	}
	f.rt.llmModel = f.ri.activeModel
}

func (f *routing1340ReviewFixture) errorPayload(t *testing.T) ErrorPayload {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var payloads []ErrorPayload
	for _, e := range f.events {
		if e.Kind == EventKindError {
			p, ok := e.Payload.(ErrorPayload)
			if !ok {
				t.Fatalf("actual error event has payload %T", e.Payload)
			}
			payloads = append(payloads, p)
		}
	}
	if len(payloads) != 1 {
		t.Fatalf("actual error events=%d, want exactly1", len(payloads))
	}
	return payloads[0]
}

func (f *routing1340ReviewFixture) errorEntry(t *testing.T) session.TranscriptEntry {
	t.Helper()
	entries, err := f.store.ReadTranscript(f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != session.EntryTypeSystem || entries[0].Status != "error" {
		t.Fatalf("stored error entries=%+v, want one system error", entries)
	}
	return entries[0]
}
