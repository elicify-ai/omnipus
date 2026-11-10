package agent

// CHECK-13 / S-97: the complete candidate order, deadline, retry, and actual
// heuristic-write outcomes. A recording paid-provider edge honors cancellation;
// its safety timer only keeps the missing pre-change deadline from hanging RED.
import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recapTimingEvent struct {
	model           string
	start, deadline time.Time
}
type recapTimingScript struct {
	mu           sync.Mutex
	events       []recapTimingEvent
	mode         string
	primaryCalls int
}

func (*recapTimingScript) GetDefaultModel() string { return "timing-primary" }
func (p *recapTimingScript) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	dl, _ := ctx.Deadline()
	p.events = append(p.events, recapTimingEvent{model, time.Now(), dl})
	if model == "timing-primary" {
		p.primaryCalls++
	}
	n := p.primaryCalls
	p.mu.Unlock()
	ok := func() (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: recapJSONResponse, FinishReason: "stop"}, nil
	}
	reset := errors.New("streaming read error: http2: response body closed")
	if model == "timing-fallback" {
		return ok()
	}
	switch p.mode {
	case "nontransient", "allocation":
		return nil, errors.New("401 primary refused")
	case "cross", "fits":
		if n == 1 {
			timer := time.NewTimer(250 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
			return nil, reset
		}
		if p.mode == "fits" {
			return ok()
		}
		return nil, reset
	case "resets_then_hang":
		if n <= 2 {
			return nil, reset
		}
	}
	timer := time.NewTimer(350 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errors.New("context deadline exceeded: RED safety receipt")
	}
}
func (p *recapTimingScript) snapshot() []recapTimingEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recapTimingEvent(nil), p.events...)
}

func TestT_L8_S97_RealCandidateOrderAndHeuristicOutcomes(t *testing.T) {
	for _, row := range []struct {
		mode      string
		retries   int
		pause     time.Duration
		want      []string
		heuristic bool
	}{
		{"cross", 1, 200 * time.Millisecond, []string{"timing-primary"}, true},
		{"fits", 1, 20 * time.Millisecond, []string{"timing-primary", "timing-primary"}, false},
		{"hang", 0, 0, []string{"timing-primary"}, true},
		{"nontransient", 0, 0, []string{"timing-primary", "timing-fallback"}, false},
		{"resets_then_hang", 2, 20 * time.Millisecond, []string{"timing-primary", "timing-primary", "timing-primary"}, true},
		{"allocation", 0, 0, []string{"timing-primary", "timing-fallback"}, false},
	} {
		t.Run(row.mode, func(t *testing.T) {
			captureFileLog(t)
			cfg := &config.Config{}
			cfg.Agents.Defaults.RecapModel = "timing-primary"
			// Empty provider pins deliberately use this paid-edge implementation
			// for both candidates, so BOTH received contexts can be observed.
			cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{{Model: "timing-fallback"}}
			peer := &recapTimingScript{mode: row.mode}
			al, sid, ag, _ := buildEffortRecapLoop(t, cfg, peer)
			rr := &agentLoopRunRecap{al: al, sessionID: sid, trigger: "test", agentInst: ag, userTurns: []string{"deadline-probe-7731"}, carryForward: "deadline-probe-7731"}
			budget := 300 * time.Millisecond
			if row.mode == "allocation" {
				budget = 60 * time.Second
			}
			start := time.Now()
			var heuristic bool
			if row.mode == "allocation" {
				// Real 60-second default; not shortened through the seam.
				heuristic = rr.callCandidates()
			} else {
				heuristic = rr.callCandidatesWithTiming(budget, row.retries, row.pause)
			}
			elapsed := time.Since(start)
			events := peer.snapshot()
			got := []string{}
			for _, event := range events {
				got = append(got, event.model)
				tolerance := 25 * time.Millisecond
				if row.mode == "allocation" {
					tolerance = time.Second
				}
				assert.InDelta(t, start.Add(budget).UnixMilli(), event.deadline.UnixMilli(), float64(tolerance.Milliseconds()), "every received context uses the same recap deadline")
				assert.True(t, event.start.Before(start.Add(budget)), "no provider call starts at or after the deadline")
			}
			assert.Equal(t, row.want, got, "S-97 exact candidate/attempt order")
			assert.Equal(t, row.heuristic, heuristic, "actual heuristic path, not just return latency")
			if row.mode == "nontransient" && len(events) > 1 {
				assert.LessOrEqual(t, events[1].start.Sub(start), 25*time.Millisecond, "quick failure leaves the full deadline for fallback")
			}
			if row.mode != "allocation" {
				assert.LessOrEqual(t, elapsed, budget+50*time.Millisecond, "S-97 elapsed bound includes the local fallback write")
			}
			if row.heuristic {
				raw, err := os.ReadFile(wu21ReaimPairPath(wu21ReaimHome(ag), ag.ID))
				require.NoError(t, err, "heuristic carry-forward was actually written")
				assert.Contains(t, string(raw), "deadline-probe-7731")
				assert.Contains(t, string(raw), "Fallback reason:")
			}
			if row.mode == "cross" {
				assert.Contains(t, slogBufForTest.String(), "recap deadline exceeded, stopping retries")
				assert.Contains(t, slogBufForTest.String(), "http2: response body closed", "the heuristic uses the last primary error, not a fabricated fallback error")
				// Existing classification is a characterization pin, not a new
				// error-vocabulary requirement of the effort feature.
				raw, err := os.ReadFile(wu21ReaimPairPath(wu21ReaimHome(ag), ag.ID))
				require.NoError(t, err)
				assert.Contains(t, string(raw), "Fallback reason: llm_error.")
			}
		})
	}
}
