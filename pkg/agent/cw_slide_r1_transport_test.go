//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// The recorder replaces ONLY the paid HTTP endpoint, not the loop or adapter.
// It captures the final bytes after the real adapter has serialized them.
type cwR1Recorder struct {
	mu       sync.Mutex
	bodies   [][]byte
	paths    []string
	readErr  error
	rejects  int
	response string
	server   *httptest.Server
}

func cwR1Server(t *testing.T, rejects int, response string) *cwR1Recorder {
	t.Helper()
	r := &cwR1Recorder{rejects: rejects, response: response}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		r.mu.Lock()
		r.readErr = err
		r.bodies = append(r.bodies, body)
		r.paths = append(r.paths, req.URL.Path)
		attempt := len(r.bodies)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if attempt <= r.rejects {
			w.WriteHeader(http.StatusBadRequest)
			_, writeErr := fmt.Fprintf(w,
				`{"error":{"message":"cw-r1-reject-%d: context_length_exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`, attempt)
			if writeErr != nil {
				r.mu.Lock()
				r.readErr = writeErr
				r.mu.Unlock()
			}
			return
		}
		_, writeErr := io.WriteString(w, r.response)
		if writeErr != nil {
			r.mu.Lock()
			r.readErr = writeErr
			r.mu.Unlock()
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

const cwR1OpenAIReply = `{"id":"r1-response","choices":[{"index":0,"message":{"role":"assistant","content":"r1-success"},"finish_reason":"stop"}]}`

func cwR1OpenAI(t *testing.T, rejects int) (*cwR1Recorder, providers.LLMProvider) {
	t.Helper()
	r := cwR1Server(t, rejects, cwR1OpenAIReply)
	p, err := providers.NewHTTPProviderWithTimeouts("r1-inert-fixture-key", r.server.URL, "", "max_tokens", 10, 0, nil)
	require.NoError(t, err, "real OpenAI-compatible adapter must construct")
	return r, p
}

func (r *cwR1Recorder) requests(t *testing.T) []map[string]json.RawMessage {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NoError(t, r.readErr, "request recorder must not hide transport errors")
	out := make([]map[string]json.RawMessage, len(r.bodies))
	for i, body := range r.bodies {
		require.NoError(t, json.Unmarshal(body, &out[i]), "serialized request %d must decode", i)
	}
	return out
}

func cwR1Messages(t *testing.T, body map[string]json.RawMessage) []providers.Message {
	t.Helper()
	var messages []providers.Message
	require.NoError(t, json.Unmarshal(body["messages"], &messages), "decode final OpenAI messages")
	return messages
}

func cwR1Flow(h *cwR1Harness, ts *turnState, messages []providers.Message, p providers.LLMProvider) *agentLoopRunTurnResponse {
	rt := &agentLoopRunTurn{
		al: h.al, ts: ts, turnCtx: context.Background(),
		activeProvider: p, llmModel: h.agent.Model, iteration: 1,
		llmOpts: map[string]any{"max_tokens": h.agent.MaxTokens},
	}
	rf := &agentLoopRunTurnFallbacks{rt: rt, callMessages: messages}
	ri := &agentLoopRunTurnIteration{
		rf: rf, messages: messages, cfg: h.cfg, activeModel: h.agent.Model,
	}
	return &agentLoopRunTurnResponse{rq: &agentLoopRunTurnRequest{ri: ri}}
}

func cwR1Prepare(t *testing.T, rr *agentLoopRunTurnResponse) {
	t.Helper()
	rr.rq.prepareCallMessages()
	require.Equal(t, agentLoopRunTurnRequestNext, rr.rq.prepareLLMRequest(),
		"real final assembly must not silently stop the fixture")
}

func cwR1Send(t *testing.T, rr *agentLoopRunTurnResponse) {
	t.Helper()
	cwR1Prepare(t, rr)
	// The sole production send seam (loop_run_turn_response.go::
	// callLLMWithRetries closure): one post-assembly pre-send checkpoint
	// (checkpointRequest(true)) runs immediately before callProvider —
	// measure and relieve the assembled request, then send what remains.
	require.NoError(t, rr.rq.checkpointRequest(true))
	response, err := rr.rq.ri.rf.rt.callProvider(rr.rq.ri.rf.callMessages, rr.rq.ri.rf.providerToolDefs)
	require.NoError(t, err, "structurally valid request must cross the real provider boundary")
	require.Equal(t, "r1-success", response.Content, "provider progress must be observed")
}

func cwR1FinishResult(t *testing.T, h *cwR1Harness, rr *agentLoopRunTurnResponse, id, full string, parallelN, index int) {
	t.Helper()
	admitted := h.al.admitToolResult(rr.rq.ri.rf.rt.ts, toolResultAdmission{
		Tool: "r1_tool", ToolCallID: id, Content: full, ParallelN: parallelN,
	})
	rx := &agentLoopRunTurnTools{ctx: context.Background(), rr: rr}
	ex := &agentLoopRunTurnToolsExecute{
		rx: rx, toolName: "r1_tool", toolCallID: id,
		toolResult: &tools.ToolResult{ForLLM: full}, contentForLLM: full,
		admitted: admitted, toolResultMsg: admitted.Message,
		tcRecord: session.ToolCall{ID: session.ToolCallID(id), Tool: "r1_tool", Status: "success"},
	}
	flow := ex.finishCall(index)
	require.NoError(t, rx.midTurnGuardErr,
		"MAJ-CW-004: real post-admission checkpoint cannot return a size-only error")
	require.Equal(t, agentLoopRunTurnToolsExecuteNext, flow,
		"real result checkpoint must continue the long turn")
}

func cwR1AssertComplete(t *testing.T, messages []providers.Message) {
	t.Helper()
	// Identity is scoped to one assistant step, not a global id set.
	for i, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		var got []string
		for j := i + 1; j < len(messages) && messages[j].Role == "tool"; j++ {
			got = append(got, messages[j].ToolCallID)
		}
		var want []string
		for _, call := range m.ToolCalls {
			want = append(want, call.ID)
		}
		require.Equal(t, want, got, "final request must keep the whole group at assistant index %d", i)
	}
}
