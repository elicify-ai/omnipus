package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type activeRequestCountingProvider struct {
	calls atomic.Int64
}

func (p *activeRequestCountingProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (*activeRequestCountingProvider) GetDefaultModel() string { return "test-model" }

// A turn can be between provider calls when shutdown starts. The next call
// must be refused before it reaches the provider; otherwise an ungated
// request registration can race the shutdown waiter or outlive store teardown.
func TestActiveRequests_StopRejectsProviderCallBetweenRounds(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	provider := &activeRequestCountingProvider{}
	agent := &AgentInstance{ID: "test-agent", Provider: provider}
	al := &AgentLoop{}
	rt := &agentLoopRunTurn{
		al:             al,
		ts:             newTurnState(agent, processOptions{}, turnEventScope{}),
		turnCtx:        context.Background(),
		activeProvider: provider,
		llmModel:       provider.GetDefaultModel(),
	}

	if _, err := rt.callProviderOnce(nil, nil); err != nil {
		t.Fatalf("first provider call: %v", err)
	}
	al.Stop()

	if _, err := rt.callProviderOnce(nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider call after Stop = %v, want context.Canceled", err)
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1; shutdown admitted a call between rounds", got)
	}
	logs := readLog()
	if got := strings.Count(logs, `"site":"callProviderOnce"`); got != 1 {
		t.Fatalf("refused provider admission WARNs naming callProviderOnce = %d, want 1; logs:\n%s", got, logs)
	}
}

func TestActiveRequests_DrainClosesIntakeAndSignalsLastCompletion(t *testing.T) {
	al := &AgentLoop{}
	if !al.beginActiveRequest() {
		t.Fatal("initial request was refused before shutdown")
	}

	drained, alreadyWaiting := al.activeRequests.startWait()
	if alreadyWaiting {
		t.Fatal("first drain was reported as an existing wait")
	}
	if al.beginActiveRequest() {
		t.Fatal("request intake remained open after drain started")
	}
	select {
	case <-drained:
		t.Fatal("drain completed while one request remained active")
	default:
	}

	al.endActiveRequest()
	select {
	case <-drained:
	default:
		t.Fatal("last completion did not signal the drain")
	}
}

func TestActiveRequests_CanceledGatewayWaitIsNotRepeatedByClose(t *testing.T) {
	al := &AgentLoop{}
	if !al.beginActiveRequest() {
		t.Fatal("initial request was refused before shutdown")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("canceled gateway wait reported a completed drain")
	}
	_, alreadyWaiting := al.activeRequests.startWait()
	if !alreadyWaiting {
		t.Fatal("Close would repeat the gateway's already-attempted drain")
	}

	al.endActiveRequest()
	if !al.WaitForActiveRequestsContext(context.Background()) {
		t.Fatal("completed request did not close the existing drain signal")
	}
}
