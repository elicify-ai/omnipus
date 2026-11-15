package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// A target on the payload must not open the Library for an error, a
// cancellation, the wrong tool, or a show_to_user result with no target.
func TestHubToolExecEnd_ShowToUser_NoOpenUnlessSuccess(t *testing.T) {
	reg := newHubRegistry("chat-links-negative")
	handler := &WSHandler{hubs: reg}
	cases := []struct {
		name string
		edit func(*agent.ToolExecEndPayload)
	}{
		{"error", func(p *agent.ToolExecEndPayload) { p.IsError = true }},
		{"cancelled", func(p *agent.ToolExecEndPayload) { p.Cancelled = true }},
		{"wrong tool", func(p *agent.ToolExecEndPayload) { p.Tool = "write_file" }},
		{"nil target", func(p *agent.ToolExecEndPayload) { p.OpenInLibrary = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const sid = "root-negative"
			hub := reg.getOrCreate(sid + tc.name)
			conn := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
			if _, ok := hub.bindLive(conn); !ok {
				t.Fatal("bindLive")
			}
			payload := agent.ToolExecEndPayload{
				ToolCallID: "call-show",
				SessionID:  sid + tc.name,
				Tool:       "show_to_user",
				Result:     "Asked the Library to open plan.md.",
			}
			if !chatLinksStampOpenTarget(&payload) && tc.name != "nil target" {
				t.Fatal("OpenInLibrary field missing")
			}
			tc.edit(&payload)
			handler.hubToolExecEnd(agent.Event{Kind: agent.EventKindToolExecEnd, Payload: payload})
			if got := chatLinksOpenFrames(chatLinksFrames(conn)); len(got) != 0 {
				t.Fatalf("%s delivered %d open_in_library frames, want 0", tc.name, len(got))
			}
		})
	}
}
