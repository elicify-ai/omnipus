// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package entity_test holds the ADR-054 Wave 4 performance gate (B1, B2, B3)
// — see docs/internal/architecture/ADR-054-entity-config-separation.md §5
// (D3 concurrency contract) and §12 ("Real contention numbers for the config
// lock and audit mutex. The perf gate at the end of this work closes this."
// and "Expected steady-state agent count — drives whether O(N) listing ever
// needs the existing AgentRegistry cache to be formalised.").
//
// This is a black-box (external test package) suite: it only calls the
// exported entity.Store[T] API, exactly as a real caller (a future
// config.AgentConfig store) would.
package entity_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// TestMain silences the global logger's per-call INFO noise (config.SaveConfig
// logs "saving config to <path>" on every single call via logger.Infof) before
// running anything in this file. At N=32 x -benchtime=200x this is 6,400+ log
// lines per sub-benchmark — pure measurement noise, not a production concern
// (test-only global logger reconfiguration, not a change to pkg/config or
// pkg/entity themselves).
func TestMain(m *testing.M) {
	logger.SetLevel(logger.WARN)
	os.Exit(m.Run())
}

// --- shared fixture: a synthetic agent-shaped entity ------------------------

// benchAgent stands in for a future config.AgentConfig entity record. Field
// count and content are chosen to land in the ~0.6-1.3KB per-record range
// ADR-054 §1 measured on real installs ("agents.list ... 30,454" / "5,338"
// bytes across install-dependent roster sizes) — this is NOT the real
// AgentConfig type (that migration is later-wave work per the entity.go
// package doc), just a size-realistic stand-in for measuring the store
// primitive Wave 1 shipped.
type benchAgent struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Home        string            `json:"workspace,omitempty"`
	Model       string            `json:"model,omitempty"`
	Skills      []string          `json:"skills,omitempty"`
	Tools       map[string]string `json:"tools,omitempty"`
	Color       string            `json:"color,omitempty"`
	Icon        string            `json:"icon,omitempty"`
	// Counter exists solely for B2 (same-entity contention): each concurrent
	// Update increments it, so a post-hoc read proves (or disproves) that
	// every update actually landed — a correctness check, not just "no error".
	Counter   int       `json:"counter,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

var benchAccessors = entity.Accessors[benchAgent]{
	GetID:        func(t *benchAgent) string { return t.ID },
	SetID:        func(t *benchAgent, id string) { t.ID = id },
	GetCreatedAt: func(t *benchAgent) time.Time { return t.CreatedAt },
	SetCreatedAt: func(t *benchAgent, ts time.Time) { t.CreatedAt = ts },
}

func newBenchAgent(id string) *benchAgent {
	return &benchAgent{
		ID:   id,
		Name: "Benchmark Agent " + id,
		Description: "A synthetic agent record used for the ADR-054 Wave 4 perf-gate " +
			"benchmarks (B1-B3). This description field pads the record toward the " +
			"~0.6-1.3KB per-agent size ADR-054 §1 measured on real installs, so " +
			"write-amplification and List() scan-cost numbers are representative " +
			"rather than measuring near-empty files.",
		Home:   "agents/" + id,
		Model:  "anthropic/claude-sonnet-4.6",
		Skills: []string{"web_search", "code_review", "file_write", "delegate"},
		Tools: map[string]string{
			"bash": "allow", "write_file": "allow", "read_file": "allow",
			"web_search": "allow", "delegate": "ask", "browser": "deny",
		},
		Color: "#22C55E",
		Icon:  "robot",
	}
}

// concurrencyLevels is the writer fan-out the task brief asks B1/B2 to cover.
var concurrencyLevels = []int{1, 2, 4, 8, 16, 32}

// listScaleLevels is the roster-size sweep ADR-054 §12 asks B3 to answer
// ("at what N does [O(N) ReadDir listing] hurt").
var listScaleLevels = []int{10, 100, 1000, 5000}

// ============================================================================
// B1 — parallel entity writes (new path) vs whole-file config rewrite (old
// path baseline). THE headline claim of the ADR: per-entity writes should
// parallelize where the old path's single global lock + whole-file rewrite
// cannot.
//
// Design note (applies to B1, B2, B4-equivalent in pkg/audit): each b.N
// "op" is a WHOLE BATCH of N concurrent writers, not a single write. This
// makes testing.B's own ns/op equal to the batch wall-clock the task asks
// for explicitly ("the batch number is what shows the lock convoy, per-op
// averages can hide it") — no separate manual timer needed. Divide by N in
// the results writeup to recover a per-single-write estimate.
// ============================================================================

// BenchmarkEntityCreate_Concurrent is the NEW path: N goroutines each
// creating a DISTINCT agent record via entity.Store[T].Create.
func BenchmarkEntityCreate_Concurrent(b *testing.B) {
	for _, n := range concurrencyLevels {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			store := entity.New[benchAgent](dir, benchAccessors)

			var failed int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				wg.Add(n)
				for g := 0; g < n; g++ {
					go func(iter, g int) {
						defer wg.Done()
						id := fmt.Sprintf("agent-%d-%d", iter, g)
						if err := store.Create(newBenchAgent(id)); err != nil {
							atomic.AddInt64(&failed, 1)
						}
					}(i, g)
				}
				wg.Wait()
			}
			b.StopTimer()

			if got := atomic.LoadInt64(&failed); got > 0 {
				b.Fatalf("BLOCKED: %d entity Create() calls failed under N=%d fan-out — "+
					"cannot report a throughput number for a lossy write path", got, n)
			}
		})
	}
}

// baselineAgentRoster returns a fresh 8-agent config.AgentsConfig.List,
// matching the "8 agents" install ADR-054 §1 measured ("~/.omnipus (8
// agents) 17,856 bytes total"). A realistic pre-existing roster, not an
// empty file — write amplification is about rewriting THIS, not a blank
// config.
func baselineAgentRoster() []config.AgentConfig {
	list := make([]config.AgentConfig, 0, 8)
	for i := 0; i < 8; i++ {
		list = append(list, config.AgentConfig{
			ID:   fmt.Sprintf("baseline-agent-%d", i),
			Name: fmt.Sprintf("Baseline Agent %d", i),
			Description: "A synthetic pre-existing agent record used as the steady-state " +
				"install baseline for the ADR-054 Wave 4 B1 whole-file-rewrite comparison.",
			Home:              "agents/baseline-" + fmt.Sprint(i),
			MaxToolIterations: 200,
			Skills:            []string{"web_search", "code_review", "file_write", "delegate"},
			Color:             "#22C55E",
			Icon:              "robot",
		})
	}
	return list
}

// cloneBaselineConfig returns an independent *config.Config carrying a copy
// of roster (never the shared backing array) so concurrent-across-BATCHES
// reuse of the same template cannot let one batch's appends leak into
// another's starting state.
func cloneBaselineConfig(roster []config.AgentConfig) *config.Config {
	list := make([]config.AgentConfig, len(roster))
	copy(list, roster)
	return &config.Config{
		Version: config.CurrentVersion,
		Agents:  config.AgentsConfig{List: list},
	}
}

// BenchmarkConfigSave_WholeFile_Concurrent is the OLD path baseline: N
// goroutines each want to create one distinct agent, but — exactly as
// production's Deps.WithConfig does today (holds al.mu across mutate-and-
// persist) — they share ONE config.Config object protected by ONE mutex, and
// each write serializes a call to the REAL config.SaveConfig (config.go:3567)
// over the WHOLE file.
//
// Each b.N iteration starts from a fresh 8-agent baseline copy (not letting
// the file grow across the whole benchmark run) so every batch measures "N
// concurrent agent-creations landing on a representative ~8-agent install",
// not a confounded growth curve from 8 agents to (b.N * n) agents.
func BenchmarkConfigSave_WholeFile_Concurrent(b *testing.B) {
	roster := baselineAgentRoster()
	for _, n := range concurrencyLevels {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "config.json")

			var failed int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cfg := cloneBaselineConfig(roster)
				var globalLock sync.Mutex // stands in for the real gateway's
				// al.mu (agent.Loop's config mutex), held across mutate-and-
				// persist exactly as Deps.WithConfig does today
				// (pkg/sysagent/tools/deps.go:239) — ADR-054 §1.

				var wg sync.WaitGroup
				wg.Add(n)
				for g := 0; g < n; g++ {
					go func(iter, g int) {
						defer wg.Done()
						globalLock.Lock()
						defer globalLock.Unlock()
						cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
							ID:   fmt.Sprintf("agent-%d-%d", iter, g),
							Name: fmt.Sprintf("Benchmark Agent %d-%d", iter, g),
							Description: "A synthetic agent record used for the ADR-054 Wave 4 " +
								"B1 whole-file-rewrite comparison benchmark.",
							Home:              fmt.Sprintf("agents/agent-%d-%d", iter, g),
							MaxToolIterations: 200,
							Skills:            []string{"web_search", "code_review", "file_write", "delegate"},
							Color:             "#22C55E",
							Icon:              "robot",
						})
						if err := config.SaveConfig(path, cfg); err != nil {
							atomic.AddInt64(&failed, 1)
						}
					}(i, g)
				}
				wg.Wait()
			}
			b.StopTimer()

			if got := atomic.LoadInt64(&failed); got > 0 {
				b.Fatalf("BLOCKED: %d config.SaveConfig() calls failed under N=%d fan-out", got, n)
			}
		})
	}
}

// ============================================================================
// B2 — same-entity contention. N goroutines updating the SAME record.
// Expected to serialize on the entity's stripe (and its sidecar flock) — the
// task asks us to quantify how badly, since this is the worst case for the
// striped-lock design.
// ============================================================================

func BenchmarkEntityUpdate_SameID_Concurrent(b *testing.B) {
	for _, n := range concurrencyLevels {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			store := entity.New[benchAgent](dir, benchAccessors)
			if err := store.Create(newBenchAgent("contended-agent")); err != nil {
				b.Fatalf("seed Create: %v", err)
			}

			var failed int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				wg.Add(n)
				for g := 0; g < n; g++ {
					go func() {
						defer wg.Done()
						_, err := store.Update("contended-agent", func(a *benchAgent) error {
							a.Counter++
							return nil
						})
						if err != nil {
							atomic.AddInt64(&failed, 1)
						}
					}()
				}
				wg.Wait()
			}
			b.StopTimer()

			if got := atomic.LoadInt64(&failed); got > 0 {
				b.Fatalf("BLOCKED: %d entity Update() calls failed under N=%d same-ID contention", got, n)
			}
		})
	}
}

// ============================================================================
// B3 — List() at scale. ADR §12 flags "O(N) ReadDir listing" as an UNKNOWN
// and asks at what N it hurts. This directly answers whether the
// AgentRegistry cache (a later wave's work) is load-bearing or merely nice.
// ============================================================================

func BenchmarkEntityList_Scale(b *testing.B) {
	for _, n := range listScaleLevels {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			store := entity.New[benchAgent](dir, benchAccessors)
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("agent-%05d", i)
				if err := store.Create(newBenchAgent(id)); err != nil {
					b.Fatalf("seed Create(%s): %v", id, err)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				items, skipped, err := store.List()
				if err != nil {
					b.Fatalf("List: %v", err)
				}
				if len(items) != n {
					b.Fatalf("List() returned %d items, want %d (skipped=%d) — "+
						"a scan that silently drops records is not a valid List() to benchmark",
						len(items), n, len(skipped))
				}
			}
		})
	}
}

// ============================================================================
// -race correctness companions (the task rule: "a benchmark that races is
// measuring nothing"). Run with:
//   go test -race -run 'Concurrent' -count=1 ./pkg/entity/...
// ============================================================================

// TestConcurrentEntityCreate_DistinctIDs proves B1's new path is actually
// correct under real goroutine fan-out: every one of N concurrent Create()
// calls with a distinct ID must succeed, and a subsequent List() must return
// exactly those N records — not fewer (a lost write) and not more (a
// duplicate/phantom write).
func TestConcurrentEntityCreate_DistinctIDs(t *testing.T) {
	dir := t.TempDir()
	store := entity.New[benchAgent](dir, benchAccessors)

	const n = 32
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			errs[g] = store.Create(newBenchAgent(fmt.Sprintf("distinct-%02d", g)))
		}(g)
	}
	wg.Wait()

	for g, err := range errs {
		if err != nil {
			t.Fatalf("Create() goroutine %d failed: %v", g, err)
		}
	}

	items, skipped, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("List() skipped %d files unexpectedly: %v", len(skipped), skipped)
	}
	if len(items) != n {
		t.Fatalf("List() returned %d items after %d concurrent distinct creates, want %d", len(items), n, n)
	}
	seen := make(map[string]bool, n)
	for _, it := range items {
		seen[it.ID] = true
	}
	for g := 0; g < n; g++ {
		want := fmt.Sprintf("distinct-%02d", g)
		if !seen[want] {
			t.Errorf("expected id %q in List() result, not found", want)
		}
	}
}

// TestConcurrentEntityUpdate_SameID proves B2's worst case is SAFE, not just
// slow: N concurrent Update() calls on the SAME record, each incrementing
// Counter by 1, must produce a FINAL Counter == N. If the striped lock (or
// its sidecar flock) had a read-modify-write race, this would undercount —
// silently losing updates is a correctness bug, not a performance number.
func TestConcurrentEntityUpdate_SameID(t *testing.T) {
	dir := t.TempDir()
	store := entity.New[benchAgent](dir, benchAccessors)
	if err := store.Create(newBenchAgent("contended")); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	const n = 32
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			_, err := store.Update("contended", func(a *benchAgent) error {
				a.Counter++
				return nil
			})
			errs[g] = err
		}(g)
	}
	wg.Wait()

	for g, err := range errs {
		if err != nil {
			t.Fatalf("Update() goroutine %d failed: %v", g, err)
		}
	}

	final, err := store.Get("contended")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Counter != n {
		t.Fatalf("BLOCKED: Counter = %d after %d concurrent same-ID updates, want %d — "+
			"the striped lock + sidecar flock lost at least one update under contention",
			final.Counter, n, n)
	}
}
