package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

const gate1ReadCallID = "gate1-real-read-file-boundary"
const gate1ReadContents = "GATE1-REAL-READ-OK"

// Only the paid provider is substituted. Its first response requests the
// registered real read_file, and its next request parks AFTER the production
// post-tool steering poll. No tool, store or injection helper is mocked.
type gate1LiveSteerProvider struct {
	mu       sync.Mutex
	path     string
	requests [][]providers.Message
	entered  chan int
	release  [2]chan struct{}
	once     [2]sync.Once
}

func newGate1LiveSteerProvider(path string) *gate1LiveSteerProvider {
	return &gate1LiveSteerProvider{path: path, entered: make(chan int, 4), release: [2]chan struct{}{make(chan struct{}), make(chan struct{})}}
}

func (p *gate1LiveSteerProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if index < len(p.release) {
		p.entered <- index
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.release[index]:
		}
	}
	if index == 0 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: gate1ReadCallID, Name: "read_file", Arguments: map[string]any{"path": p.path},
		}}, FinishReason: "tool_calls"}, nil
	}
	return &providers.LLMResponse{Content: "Real tool boundary finished.", FinishReason: "stop"}, nil
}

func (*gate1LiveSteerProvider) GetDefaultModel() string { return "gate1-real-tool-boundary" }

func (p *gate1LiveSteerProvider) open(index int) {
	p.once[index].Do(func() { close(p.release[index]) })
}

func (p *gate1LiveSteerProvider) requestsSnapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		out[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return out
}

func gate1AwaitLiveProvider(t *testing.T, p *gate1LiveSteerProvider, index int) {
	t.Helper()
	select {
	case got := <-p.entered:
		if got != index {
			t.Fatalf("SETUP: provider boundary=%d, want exactly %d", got, index)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(fmt.Sprintf("SETUP: provider did not reach real boundary %d", index))
	}
}
