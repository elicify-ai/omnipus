package agent

// #1968 FIX5: spec FR-10/13/16/17. These tests retain the real attempt,
// fallback, recovery and serializers; only the HTTP peer and authorization
// edge are scripted. No new implementation seam is required.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func pdfFix5Flow(t *testing.T, p providers.LLMProvider, provider, model string, limits map[string]any) *routing1340ReviewFixture {
	t.Helper()
	a := &AgentInstance{ID: "pdf-fix5", Model: model, Candidates: []providers.FallbackCandidate{{Provider: provider, Model: model}}}
	a.StoreProviderPool(map[string]providers.LLMProvider{provider: p})
	f := newRouting1340ReviewFixture(t, nil, a)
	rows := []pdf1968ProviderRow{pdf1968CapableRow(provider, model, limits)}
	f.al.SetCapabilityCatalog(pdf1968Catalog(t, rows))
	pdf1968SetLimits(t, f.al, nil, rows)
	f.al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).WithRetryWait(func(context.Context, time.Duration) error { return nil })
	f.selectProvider(t)
	return f
}

func pdfFix5SetMessages(f *routing1340ReviewFixture, messages []providers.Message) {
	f.rf.callMessages = messages
	f.ri.messages = messages
}

func TestToolPDF_RetryReauthorizesSameCandidate(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(fmt.Sprintf("deny_on_retry=%t", deny), func(t *testing.T) {
			var revoked atomic.Bool
			peer := newPdf1968Peer(t,
				pdf1968Step{status: 429, errBody: `{"error":{"code":"rate_limit_exceeded","message":"rate limit"}}`, headers: map[string]string{"Retry-After": "1"}, onRequest: func() { revoked.Store(deny) }},
				pdf1968Step{content: "retry completed"})
			p, err := providers.NewHTTPProviderWithTimeouts("test-placeholder", peer.server.URL, "", "max_tokens", 10, 10, nil)
			require.NoError(t, err)
			f := pdfFix5Flow(t, p, "fx/retry", "fx/pdf", nil)
			root := t.TempDir()
			data := pdf1968TextPDF(t, 1, true)
			path := filepath.Join(root, "checked.pdf")
			require.NoError(t, os.WriteFile(path, data, 0o600))
			reader := tools.NewReadFileTool(root, false, tools.MaxReadFileSize)
			result := reader.Execute(context.Background(), map[string]any{"path": path})
			require.False(t, result.IsError, "%s", result.ForLLM)
			require.Len(t, result.InspectionPDFs, 1, "premise: actual read_file produced the private PDF")
			item := result.InspectionPDFs[0]
			original := item.Reauthorize
			require.NotNil(t, original)
			var checks atomic.Int32
			item.Reauthorize = func(ctx context.Context) error {
				checks.Add(1)
				if revoked.Load() {
					return errors.New("source access revoked")
				}
				return original(ctx)
			}
			f.rt.inspectionPDFs = map[string][]tools.InspectionPDF{"read-pdf": {item}}
			pdfFix5SetMessages(f, []providers.Message{
				{Role: "user", Content: "inspect checked.pdf"},
				{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "read-pdf", Type: "function", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"checked.pdf"}`}}}},
				{Role: "tool", ToolCallID: "read-pdf", Content: result.ForLLM},
			})
			f.rr.callLLMWithRetries()
			require.NoError(t, f.rf.err)
			bodies := peer.Bodies()
			require.Len(t, bodies, 2, "one rate retry, not a PDF recovery")
			require.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[0]), "premise: first authorized request is native")
			assert.Equal(t, int32(2), checks.Load(), "F2: reauthorization belongs to every actual send, including cache hits")
			if deny {
				assert.Empty(t, pdf1968BodyPDFs(t, bodies[1]), "F2: revoked bytes must not be replayed from the attachment cache")
				assert.Contains(t, bodies[1], "[PDF view unavailable: access denied]")
			} else {
				assert.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[1]), "positive control: authorized retry stays native")
				assert.NotContains(t, bodies[1], "access denied")
			}
			assert.False(t, f.rt.ts.mediaRetryDone.Load(), "rate retry never consumes PDF recovery")
			assert.NotContains(t, bodies[1], pdf1968P1Sentence)
		})
	}
}

type pdfFix5StreamPeer struct {
	mu     sync.Mutex
	bodies []string
	server *httptest.Server
}

func pdfFix5NewStreamPeer(t *testing.T, status int, code, message string) *pdfFix5StreamPeer {
	t.Helper()
	p := &pdfFix5StreamPeer{}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request", 500)
			return
		}
		p.mu.Lock()
		n := len(p.bodies)
		p.bodies = append(p.bodies, string(body))
		p.mu.Unlock()
		if n == 0 && status != 200 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":%q}}`, code, message)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 0 {
			_, _ = fmt.Fprintf(w, "data: {\"error\":{\"code\":%q,\"message\":%q}}\n\ndata: [DONE]\n\n", code, message)
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"recovery answer\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *pdfFix5StreamPeer) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.bodies...)
}

type pdfFix5WrappedStream struct {
	providers.LLMProvider
	stream providers.StreamingProvider
}

func (p pdfFix5WrappedStream) ChatStream(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, chunk func(string), progress providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	resp, err := p.stream.ChatStream(ctx, messages, defs, model, opts, chunk, progress)
	if err != nil {
		return resp, fmt.Errorf("network adapter: %w", err)
	}
	return resp, nil
}

func pdfFix5StreamingFlow(t *testing.T, peer *pdfFix5StreamPeer, wrapped bool) (*routing1340ReviewFixture, []byte) {
	t.Helper()
	p, err := providers.NewHTTPProviderWithTimeouts("test-placeholder", peer.server.URL, "", "max_tokens", 10, 10, nil)
	require.NoError(t, err)
	var provider providers.LLMProvider = p
	if wrapped {
		provider = pdfFix5WrappedStream{LLMProvider: p, stream: p}
	}
	f := pdfFix5Flow(t, provider, "fx/stream", "fx/pdf", nil)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	msgBus.SetStreamDelegate(&msgIDStreamDelegate{streamer: &identityStampRecordingStreamer{}})
	f.al.bus = msgBus
	data := pdf1968TextPDF(t, 1, true)
	pdfFix5SetMessages(f, []providers.Message{{Role: "user", Content: "inspect PDF", Media: []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data)}}})
	return f, data
}

func TestPDFRecovery_StreamedPolicyNeverRecovers(t *testing.T) {
	for _, code := range []string{"content_policy_violation", "content_filter"} {
		for _, wrapped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/wrapped=%t", code, wrapped), func(t *testing.T) {
				peer := pdfFix5NewStreamPeer(t, 200, code, "blocked")
				f, data := pdfFix5StreamingFlow(t, peer, wrapped)
				f.rr.callLLMWithRetries()
				bodies := peer.requests()
				require.NotEmpty(t, bodies, "premise: actual SSE parser was invoked")
				require.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[0]))
				var envelope map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(bodies[0]), &envelope))
				require.Equal(t, "true", string(envelope["stream"]), "premise: actual HTTP-200 SSE error, not the nonstreaming error path")
				assert.Len(t, bodies, 1, "F3: policy refusal must not resubmit the PDF as text")
				assert.Error(t, f.rf.err, "the actual refusal must surface, not a recovery answer")
				if f.rf.err != nil {
					assert.Equal(t, CodeContentPolicy, TranslateTurnError(f.rf.err).Code)
				}
				assert.False(t, f.rt.ts.mediaRetryDone.Load(), "policy error must not spend PDF recovery")
				assert.Nil(t, f.rf.response)
			})
		}
	}
}

func TestPDFRecovery_GenuineRejectionsStillRecoverOnce(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		code, message string
	}{
		{"stream-format", 200, "invalid_request_error", "PDF input is unsupported"},
		{"context", 400, "context_length_exceeded", "context window exceeded"},
		{"413", 413, "request_too_large", "body too large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := pdfFix5NewStreamPeer(t, tc.status, tc.code, tc.message)
			f, data := pdfFix5StreamingFlow(t, peer, false)
			f.rr.callLLMWithRetries()
			require.NoError(t, f.rf.err)
			require.NotNil(t, f.rf.response)
			assert.Equal(t, "recovery answer", f.rf.response.Content)
			bodies := peer.requests()
			require.Len(t, bodies, 2, "FR-16/17 allow exactly one native-to-text recovery")
			assert.Equal(t, [][]byte{data}, pdf1968BodyPDFs(t, bodies[0]))
			assert.Empty(t, pdf1968BodyPDFs(t, bodies[1]))
			assert.Contains(t, bodies[1], "PAGE 1 text")
			assert.Contains(t, bodies[1], pdf1968P1Sentence)
			assert.True(t, f.rt.ts.mediaRetryDone.Load())
		})
	}
}
