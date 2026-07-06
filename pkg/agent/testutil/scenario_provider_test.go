package testutil_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// TestScenarioProviderChainableBuilder verifies the builder API returns steps in order
// and ErrNoMoreResponses on exhaustion.
//
// Traces to: temporal-puzzling-melody.md §1 acceptance contracts — ScenarioProvider sanity
func TestScenarioProviderChainableBuilder(t *testing.T) {
	p := testutil.NewScenario().
		WithText("step-1").
		WithText("step-2").
		WithText("step-3").
		WithText("step-4")

	ctx := context.Background()

	for i, want := range []string{"step-1", "step-2", "step-3", "step-4"} {
		resp, err := p.Chat(ctx, nil, nil, "", nil)
		require.NoError(t, err, "step %d must not error", i+1)
		require.NotNil(t, resp, "step %d response must not be nil", i+1)
		assert.Equal(t, want, resp.Content, "step %d content mismatch", i+1)
	}

	// 5th call must return ErrNoMoreResponses.
	_, err := p.Chat(ctx, nil, nil, "", nil)
	assert.ErrorIs(t, err, testutil.ErrNoMoreResponses,
		"exhausted provider must return ErrNoMoreResponses")

	assert.Equal(t, 4, p.CallCount(),
		"CallCount must equal the number of successful steps consumed")
	assert.Equal(t, 0, p.Remaining(),
		"Remaining must be 0 after all steps consumed")
}

// TestScenarioProviderConcurrentChat verifies ScenarioProvider is safe under
// concurrent access and that every Chat call is counted exactly once.
//
// Traces to: temporal-puzzling-melody.md §1 acceptance contracts — ScenarioProvider concurrency
func TestScenarioProviderConcurrentChat(t *testing.T) {
	const totalSteps = 100
	const goroutines = 10

	// Build a 100-step scenario.
	p := testutil.NewScenario()
	for i := 0; i < totalSteps; i++ {
		p = p.WithText("response")
	}

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for {
				_, err := p.Chat(ctx, nil, nil, "", nil)
				if err != nil {
					if errors.Is(err, testutil.ErrNoMoreResponses) {
						return
					}
					t.Errorf("unexpected error from Chat: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// All 100 steps must have been consumed exactly once.
	assert.Equal(t, totalSteps, p.CallCount(),
		"total CallCount must equal totalSteps — no double-counting or lost updates")
	assert.Equal(t, 0, p.Remaining(),
		"all steps must be consumed")
}

// TestScenarioProviderRespectsCtxCancel verifies that a canceled context causes
// Chat to return ctx.Err() immediately.
//
// Traces to: temporal-puzzling-melody.md §1 acceptance contracts — ScenarioProvider ctx.Done
func TestScenarioProviderRespectsCtxCancel(t *testing.T) {
	p := testutil.NewScenario().WithText("should not be reached")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before calling Chat

	_, err := p.Chat(ctx, nil, nil, "", nil)
	assert.ErrorIs(t, err, context.Canceled,
		"Chat with an already-canceled context must return context.Canceled")

	// The step was not consumed.
	assert.Equal(t, 0, p.CallCount(), "CallCount must be 0 — canceled Chat must not consume a step")
	assert.Equal(t, 1, p.Remaining(), "the step must still be available after canceled Chat")
}
