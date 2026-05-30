// Concurrent-load test for MemoryRateLimiter. Run with `go test -race`.
//
// Verifies three properties under N-goroutine load:
//   - no data race (the test runs clean under -race)
//   - no panics inside Allow()
//   - the per-agent total-allowed never exceeds the configured budget
//
// Pattern cloned from pkg/audit/rotation_race_test.go.
//
// Traces to: v0.2 #155 final review (test-analyzer rated 8 — must close
// before merging the issue).

package tools_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestMemoryRateLimiter_ConcurrentLoad_NoRace verifies that 6 writer
// goroutines hammering a single limiter for 200ms produce no data race
// (when run under -race) and the per-agent ceiling is never exceeded.
func TestMemoryRateLimiter_ConcurrentLoad_NoRace(t *testing.T) {
	const (
		writers       = 6
		duration      = 200 * time.Millisecond
		perAgentLimit = 50
	)

	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  perAgentLimit,
		PerCallerLimit: 10_000, // effectively unbounded for this test
		Window:         5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var (
		wg         sync.WaitGroup
		allowed    int64
		denied     int64
		panicCount int64
		// Each writer hits the SAME agentID so the per-agent bucket is the
		// contention point. They use distinct callerIDs so the per-caller
		// bucket is not the trip — we are validating the per-agent gate
		// under contention.
		sharedAgent = "agent-load"
	)

	for i := range writers {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
					t.Errorf("writer %d panicked: %v", writerID, r)
				}
			}()
			callerID := fmt.Sprintf("caller-%d", writerID)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if d := limiter.Allow(sharedAgent, callerID); d.Allowed {
						atomic.AddInt64(&allowed, 1)
					} else {
						atomic.AddInt64(&denied, 1)
					}
				}
			}
		}(i)
	}
	wg.Wait()

	if panicCount > 0 {
		t.Fatalf("MemoryRateLimiter: %d goroutine(s) panicked under load", panicCount)
	}

	// Invariant: the per-agent ceiling MUST hold under any racing concurrency.
	// Total allowed across all writers cannot exceed perAgentLimit because
	// they all share the same agent bucket and the test window is < limiter
	// window. If allowed > perAgentLimit, the limiter has a race or the
	// eviction loop has an off-by-one.
	a := atomic.LoadInt64(&allowed)
	d := atomic.LoadInt64(&denied)
	if a > int64(perAgentLimit) {
		t.Errorf("per-agent budget violated: allowed=%d > perAgentLimit=%d (denied=%d)",
			a, perAgentLimit, d)
	}
	if a == 0 {
		t.Errorf("limiter rejected every request under load — likely a deadlock or initialization bug (denied=%d)", d)
	}
	t.Logf("MemoryRateLimiter under %d writers × %s: allowed=%d, denied=%d, perAgentLimit=%d",
		writers, duration, a, d, perAgentLimit)
}

// TestMemoryRateLimiter_ConcurrentLoad_PerCallerCeiling mirrors the
// per-agent test but stresses the per-caller bucket — many agents
// hitting the SAME caller bucket simultaneously must not exceed
// perCallerLimit.
func TestMemoryRateLimiter_ConcurrentLoad_PerCallerCeiling(t *testing.T) {
	const (
		writers        = 6
		duration       = 200 * time.Millisecond
		perCallerLimit = 50
	)

	limiter := tools.NewMemoryRateLimiter(tools.MemoryRateLimitConfig{
		PerAgentLimit:  10_000, // effectively unbounded for this test
		PerCallerLimit: perCallerLimit,
		Window:         5 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var (
		wg           sync.WaitGroup
		allowed      int64
		panicCount   int64
		sharedCaller = "caller-shared"
	)

	for i := range writers {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panicCount, 1)
					t.Errorf("writer %d panicked: %v", writerID, r)
				}
			}()
			agentID := fmt.Sprintf("agent-%d", writerID)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if d := limiter.Allow(agentID, sharedCaller); d.Allowed {
						atomic.AddInt64(&allowed, 1)
					}
				}
			}
		}(i)
	}
	wg.Wait()

	if panicCount > 0 {
		t.Fatalf("MemoryRateLimiter: %d goroutine(s) panicked", panicCount)
	}

	a := atomic.LoadInt64(&allowed)
	if a > int64(perCallerLimit) {
		t.Errorf("per-caller budget violated: allowed=%d > perCallerLimit=%d", a, perCallerLimit)
	}
	if a == 0 {
		t.Error("zero allowed across all writers — likely a deadlock")
	}
}
