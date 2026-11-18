package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyD16DoneProbe is a live hub connection that, at the exact moment the
// session's "done" frame is delivered (synchronously, on the turn goroutine,
// inside wsStreamer.Finalize's publishDone), counts the durable
// turn_canceled markers a reload taken at that moment would read. It never
// blocks the turn: the count is taken before publishDone returns, so it is
// exact without any timing.
type historyD16DoneProbe struct {
	store   *session.UnifiedStore
	sid     string
	turn    func() string
	once    sync.Once
	seen    chan struct{}
	markers int
	readErr error
	// readPage invokes the real REST handler synchronously inside enqueue.
	// No polling or later settlement can retroactively repair this snapshot.
	readPage   func() (int, []byte)
	pageStatus int
	pageBody   []byte
}

func (p *historyD16DoneProbe) enqueue(frame []byte) bool {
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(frame, &head) != nil || head.Type != "done" {
		return true
	}
	p.once.Do(func() {
		entries, err := p.store.ReadTranscript(p.sid)
		p.readErr = err
		for _, entry := range entries {
			if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == p.turn() {
				p.markers++
			}
		}
		p.pageStatus, p.pageBody = p.readPage()
		close(p.seen)
	})
	return true
}

// TestCancelledTool_DoneFrameAfterCancelRecord (#1524 red reproduction): a
// client that receives the Stop-all turn's "done" frame and reloads at once
// must read that turn's turn_canceled record.
func TestCancelledTool_DoneFrameAfterCancelRecord(t *testing.T) {
	home, cfg := historyD16Config(t)
	cfg.Tools.Manifest.Compressed = false
	cfg.Sandbox.ToolPolicies[historyD16ToolName] = "allow"
	provider := &historyD16Provider{replies: []historyD16Reply{{content: "D16 original target reply before Stop", calls: []providers.ToolCall{
		historyD16RequestedCall(historyD16CompletedID, "completed"),
		historyD16RequestedCall(historyD16CancelledID, "blocked"),
		historyD16RequestedCall(historyD16QueuedID, "queued"),
	}}}}
	h := newHistoryD16Harness(t, home, cfg, provider, true)
	tool := newHistoryD16Tool(false)
	h.loop.RegisterTool(tool)
	inst, ok := h.loop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{historyD16ToolName: "allow"}})
	sid := h.session()
	peer := h.connect()
	h.attach(peer, sid)
	var turnMu sync.Mutex
	turnID := ""
	probe := &historyD16DoneProbe{store: h.store, sid: sid, seen: make(chan struct{}),
		turn: func() string { turnMu.Lock(); defer turnMu.Unlock(); return turnID },
		readPage: func() (int, []byte) {
			response := httptest.NewRecorder()
			h.api.HandleSessions(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages", nil))
			return response.Code, append([]byte(nil), response.Body.Bytes()...)
		}}
	t.Cleanup(func() { h.cancel(); tool.unblock() })
	_, bound := h.handler.hubs.getOrCreate(sid).bindLive(probe)
	require.True(t, bound)
	h.send(peer, sid, "d16-target-real-instruction", "D16 complete one call, start another, leave the third queued")
	historyD16Await(t, tool.started, "native tool dispatch actually entered its blocked execution")
	started := h.event(func(event agent.Event) bool {
		payload, valid := event.Payload.(agent.ToolExecStartPayload)
		return event.Kind == agent.EventKindToolExecStart && valid && payload.SessionID == sid && string(payload.ToolCallID) == historyD16CancelledID
	})
	turnMu.Lock()
	turnID = started.Meta.TurnID
	turnMu.Unlock()

	historyD16NativeStop(t, h, tool, sid, started.Meta.TurnID, historyD16CancelMode{tree: true})
	historyD16Await(t, probe.seen, "the stopped turn's done frame reached a live client")
	require.NoError(t, probe.readErr)
	// Instrument check: historyD16NativeStop has waited for the record, so it
	// is not lost, only written after done.
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err)
	joined := 0
	for _, entry := range entries {
		if entry.Type == session.EntryTypeTurnCancelled && entry.TurnID == started.Meta.TurnID {
			joined++
		}
	}
	require.Equal(t, 1, joined, "the stopped turn still saves exactly one cancel record")
	assert.Equal(t, 1, probe.markers, "a reload taken when done reaches the client shows the turn as stopped")
	require.Equal(t, http.StatusOK, probe.pageStatus, "real immediate REST readback is available")
	var page generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(probe.pageBody, &page))
	require.Equal(t, sid, page.SessionId)
	immediate := 0
	for _, entry := range page.Entries {
		if entry.Type != nil && string(*entry.Type) == string(session.EntryTypeTurnCancelled) && entry.TurnId != nil && *entry.TurnId == started.Meta.TurnID {
			immediate++
		}
	}
	t.Logf("#1524 exact done boundary: canonical_cancel_records=%d REST_cancel_records=%d; after_settlement=%d", probe.markers, immediate, joined)
	assert.Equal(t, 1, immediate, "closes #1524: REST reload right at done must already include the stopped turn fact; a later test wait is not a product fix")
}
