// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// #1882 guard (concurrency). The agent loop calls GetOrCreate with the
// CURRENT configured limit on every turn, then Allow on the returned window.
// When an operator changes the limit, goroutines of different turns call
// GetOrCreate with different limits while others call Allow on the same
// window. The change must never panic, and the count must stay consistent:
// inside one hour-long window the number of allowed calls is at least the
// smallest limit ever configured (the first calls are inside every limit) and
// at most the largest (an allowed call is only recorded while below the limit
// in force at that moment, and no limit above the maximum was ever set). Every
// denied call reports a positive wait and a policy rule.
// Run with -count=3. This is a guard: it passes on the current code (which
// keeps the first limit) and must keep passing once a changed limit reaches
// the existing window.
func TestRateLimiterRegistry_ConcurrentLimitChange_CountStaysConsistent_1882(t *testing.T) {
	const (
		goroutines = 16
		iterations = 50
		lowLimit   = 3
		highLimit  = 8
		key        = "agent:guard-agent:llm_call"
	)
	reg := security.NewRateLimiterRegistry()

	var allowed, denied, badDenial atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				limit := lowLimit
				if (g+i)%2 == 0 {
					limit = highLimit
				}
				sw := reg.GetOrCreate(key, limit, time.Hour, security.ScopeAgent, "guard-agent", "llm_call")
				res := sw.Allow()
				if res.Allowed {
					allowed.Add(1)
					continue
				}
				denied.Add(1)
				if res.RetryAfterSeconds <= 0 || res.PolicyRule == "" {
					badDenial.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	total := int64(goroutines * iterations)
	if allowed.Load()+denied.Load() != total {
		t.Fatalf("allowed %d + denied %d != %d calls made", allowed.Load(), denied.Load(), total)
	}
	if got := allowed.Load(); got < lowLimit || got > highLimit {
		t.Errorf("allowed = %d, want between the smallest limit %d and the largest %d", got, lowLimit, highLimit)
	}
	if n := badDenial.Load(); n != 0 {
		t.Errorf("%d denied calls had no positive RetryAfterSeconds or no PolicyRule", n)
	}
}
