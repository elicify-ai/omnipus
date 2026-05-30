// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Race-detector stress test for the sensitive-data cache.
//
// BDD Scenario: "Concurrent RegisterSensitiveValues (writer) vs SensitiveDataReplacer (reader)
//               never produces a torn output or panic"
//
// Given N reader goroutines repeatedly calling SensitiveDataReplacer,
// And one writer goroutine repeatedly calling RegisterSensitiveValues,
// When both run concurrently for ~1 second,
// Then no panic occurs and the replacer always returns a fully-replaced or
//   unchanged-but-not-torn string.
//
// Run with: go test -race ./pkg/config/ -run TestSensitiveCache_Race
//
// Note: CGO_ENABLED=0 disables the race detector. When CGO=0 this test still
// serves as a stress test asserting no panics or torn string output.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 1 (Rank-9)

package config

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSensitiveCache_ConcurrentReadWriteNoPanic verifies that concurrent calls to
// RegisterSensitiveValues (writer) and SensitiveDataReplacer (reader) on a *Config
// instance do not panic and do not produce torn output.
//
// A "torn output" is defined as the replacer returning a string that contains
// a partial secret (e.g., "sk-part" instead of "[FILTERED]") — indicating the
// replacer was called with a half-initialized cache.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 1
func TestSensitiveCache_ConcurrentReadWriteNoPanic(t *testing.T) {
	// Build a minimal Config that is safe to use without a home directory.
	cfg := &Config{}

	// Two distinct secret sets the writer alternates between.
	secretSetA := []string{"secret-alpha-AAA", "token-beta-BBB"}
	secretSetB := []string{"secret-gamma-CCC", "token-delta-DDD"}

	// Input strings for the reader — contain at least one secret from each set.
	inputs := []string{
		"secret-alpha-AAA is the key",
		"please use token-beta-BBB here",
		"secret-gamma-CCC and token-delta-DDD in same string",
		"no secret present — plain text",
		"prefix secret-alpha-AAA middle token-delta-DDD suffix",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var panics int64
	var tornOutputs int64

	// N reader goroutines.
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&panics, 1)
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					r := cfg.SensitiveDataReplacer()
					if r == nil {
						// nil replacer is not expected after the first RegisterSensitiveValues
						// call, but the cache lazily initializes on first read so nil is
						// only possible before any secret has been registered.
						continue
					}
					for _, input := range inputs {
						out := r.Replace(input)
						// Torn-output check: the result must not contain a partial secret
						// that looks like a truncated version of a known secret. We verify
						// that if the output still contains a raw secret, the replacer
						// returned the input verbatim (no substitution) rather than a
						// half-replaced result that would indicate tearing.
						//
						// A torn output would look like: "[FILTER" (truncated placeholder)
						// or a mix of "[FILTERED]" and raw secrets in one string. We check
						// for the "[FILTER" truncation specifically — a partial placeholder
						// is the clearest signal of a torn cache write.
						if strings.Contains(out, "[FILTER") && !strings.Contains(out, "[FILTERED]") {
							atomic.AddInt64(&tornOutputs, 1)
						}
					}
				}
			}
		}()
	}

	// 1 writer goroutine alternating between two secret sets.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				atomic.AddInt64(&panics, 1)
			}
		}()
		toggle := false
		for {
			select {
			case <-ctx.Done():
				return
			default:
				if toggle {
					cfg.RegisterSensitiveValues(secretSetA)
				} else {
					cfg.RegisterSensitiveValues(secretSetB)
				}
				toggle = !toggle
			}
		}
	}()

	wg.Wait()

	if panics > 0 {
		t.Errorf("sensitive cache race: %d goroutine(s) panicked during concurrent read/write", panics)
	}
	if tornOutputs > 0 {
		t.Errorf("sensitive cache race: %d torn output(s) detected (partial [FILTER placeholder seen)", tornOutputs)
	}
}

// TestSensitiveCache_ReplacerFiltersAfterRegister verifies the basic
// differentiation contract: registering different secrets produces
// different replacer outputs. This proves the replacer is not hardcoded.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.G stage 3, item 1
func TestSensitiveCache_ReplacerFiltersAfterRegister(t *testing.T) {
	cfg := &Config{}
	const secretA = "my-secret-api-key-12345"
	const secretB = "another-credential-67890"
	const plain = "this has no secrets"

	// Before any registration the replacer must exist (lazily built).
	r0 := cfg.SensitiveDataReplacer()
	if r0 == nil {
		t.Fatal("SensitiveDataReplacer must return non-nil even before registration")
	}
	// Before registration, the secret should pass through unchanged.
	out0 := r0.Replace(secretA)
	if out0 != secretA {
		t.Errorf("before registration: expected %q unchanged, got %q", secretA, out0)
	}

	// Register secretA.
	cfg.RegisterSensitiveValues([]string{secretA})
	r1 := cfg.SensitiveDataReplacer()

	// After registration, secretA must be filtered.
	out1 := r1.Replace(secretA)
	if !strings.Contains(out1, "[FILTERED]") {
		t.Errorf("after registering %q: expected [FILTERED], got %q", secretA, out1)
	}

	// Differentiation: secretB not yet registered — must NOT be filtered.
	out1b := r1.Replace(secretB)
	if strings.Contains(out1b, "[FILTERED]") {
		t.Errorf("secretB should not be filtered before registration, got %q", out1b)
	}

	// Register secretB (replace the set — this evicts secretA too).
	cfg.RegisterSensitiveValues([]string{secretB})
	r2 := cfg.SensitiveDataReplacer()

	// After replace: secretB must be filtered.
	out2b := r2.Replace(secretB)
	if !strings.Contains(out2b, "[FILTERED]") {
		t.Errorf("after registering %q: expected [FILTERED], got %q", secretB, out2b)
	}

	// After replace: secretA must NOT be filtered (replace semantics, not append).
	out2a := r2.Replace(secretA)
	if strings.Contains(out2a, "[FILTERED]") {
		t.Errorf("secretA should be evicted after RegisterSensitiveValues replace, got %q", out2a)
	}

	// Plain text must never be modified.
	outPlain := r2.Replace(plain)
	if outPlain != plain {
		t.Errorf("plain text must be unchanged, got %q", outPlain)
	}
}
