package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const historyUS16Instruction = "Use write_file to create us16-approved.txt with the requested proof, then keep working until I press Stop."
const historyUS16Filename = "us16-approved.txt"
const historyUS16Proof = "US1.6 actual approved builtin write\n"
const historyUS16CallID = "us16-genuine-write"
const historyUS16ClientID = "us16-ordinary-send-once"
const historyUS16Token = "us16-controlled-test-token"

// Only this process-edge model transport is controlled. Its two planned calls
// are fixed independently of the implementation: real write_file, then block
// on the real running turn's context. No pending approval, result or Stop fake.
type historyUS16Provider struct {
	calls              atomic.Int64
	mu                 sync.Mutex
	definitions        []providers.ToolDefinition
	toolMessages       []providers.Message
	resumed, cancelled chan struct{}
}

func (p *historyUS16Provider) GetDefaultModel() string { return "test-model" }
func (p *historyUS16Provider) Chat(ctx context.Context, messages []providers.Message, definitions []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	switch p.calls.Add(1) {
	case 1:
		p.mu.Lock()
		p.definitions = append([]providers.ToolDefinition(nil), definitions...)
		p.mu.Unlock()
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: historyUS16CallID, Type: "function", Name: "write_file", Arguments: map[string]any{"path": historyUS16Filename, "content": historyUS16Proof}}}, FinishReason: "tool_calls"}, nil
	case 2:
		p.mu.Lock()
		for _, message := range messages {
			if message.Role == "tool" {
				p.toolMessages = append(p.toolMessages, message)
			}
		}
		p.mu.Unlock()
		close(p.resumed)
		<-ctx.Done()
		close(p.cancelled)
		return nil, ctx.Err()
	default:
		return nil, errors.New("US1.6 unexpected extra provider invocation: Retry/reconnect must not resend or relaunch")
	}
}

func (p *historyUS16Provider) snapshot() ([]providers.ToolDefinition, []providers.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]providers.ToolDefinition(nil), p.definitions...), append([]providers.Message(nil), p.toolMessages...)
}

// One actual network connection; closure is joined by WSHandler.Wait in the
// harness. A single reader/writer in the test avoids independent timing probes.
type historyUS16Peer struct {
	conn      *websocket.Conn
	closeOnce sync.Once
	closeErr  error
}

func (p *historyUS16Peer) close(t *testing.T) {
	t.Helper()
	p.closeOnce.Do(func() { p.closeErr = p.conn.Close() })
	require.NoError(t, p.closeErr)
}

func (p *historyUS16Peer) send(t *testing.T, frame any) {
	t.Helper()
	require.NoError(t, p.conn.SetWriteDeadline(time.Now().Add(15*time.Second)))
	require.NoError(t, p.conn.WriteJSON(frame))
}

func historyUS16Await(t *testing.T, ready <-chan struct{}, stage string) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(15 * time.Second):
		t.Fatalf("US1.6 %s did not reach its actual channel barrier", stage)
	}
}
