// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// gatedProvider answers every chat only after release is closed, counting
// calls so a test can see how many task runs actually reached the model.
type gatedProvider struct {
	calls   atomic.Int32
	once    sync.Once
	release chan struct{}
}

func (p *gatedProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	select {
	case <-p.release:
		return &providers.LLMResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *gatedProvider) GetDefaultModel() string { return "test-model" }
func (p *gatedProvider) open()                   { p.once.Do(func() { close(p.release) }) }

// The task start path is the session launcher (S1 M9, 978c232bc / 674840dd3).
// The old dispatch semaphore answered a full cap with 409; the launcher's own
// admission gate (steerAdmission, the same Performance.EffectiveMaxParallelAgents
// authority) now bounds concurrent runs and QUEUES the overflow (D9: the
// existing running/queued/refused admission disposition; session-core spec
// BDD-05.2). This pins that the cap still holds: with room for one run, a
// second PATCH in_progress is accepted (200), does NOT reach the model while
// the first runs, and starts once the first finishes.
func TestHandleTaskPatch_RunTask_AdmissionCapQueuesTheOverflowRun(t *testing.T) {
	prov := &gatedProvider{release: make(chan struct{})}
	api := newTestRestAPIAlignedStoresWithProvider(t, prov)
	t.Cleanup(prov.open)
	api.agentLoop.GetConfig().Performance.MaxParallelAgents = 1
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})

	start := func(title string) string {
		tsk := createTaskViaAPI(t, api, title, wsID)
		advanceTaskToNext(t, api, tsk.Id)
		require.Equal(t, http.StatusOK, patchTask(t, api, tsk.Id, `{"agent_id":"mia"}`).Code)
		return tsk.Id
	}
	first, second := start("CapFirst"), start("CapSecond")

	w1 := patchTask(t, api, first, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w1.Code, w1.Body.String())
	require.Eventually(t, func() bool { return prov.calls.Load() >= 1 }, 10*time.Second, 20*time.Millisecond,
		"instrument check: the first run reaches the model")

	w2 := patchTask(t, api, second, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusOK, w2.Code, "an overflow run is queued, not refused with 409: %s", w2.Body.String())
	time.Sleep(500 * time.Millisecond)
	assert.EqualValues(t, 1, prov.calls.Load(), "the overflow run must not reach the model while the cap is full")

	prov.open()
	require.Eventually(t, func() bool { return prov.calls.Load() >= 2 }, 15*time.Second, 50*time.Millisecond,
		"the queued run starts once the first one frees its slot")
	api.taskExecutor.Drain(30 * time.Second)
}
