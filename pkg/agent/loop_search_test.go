// loop_search_test.go: tests for dynamic tool load and search promotion

package agent

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- moved from loop.go tests 2026-09-15 ---

// ─── Session state tests ────────────────────────────────────────────────────

// TestMarkToolsLoaded_BasicRoundTrip proves that markToolsLoaded records names
// and sessionLoadedTools returns them for the same session.
func TestMarkToolsLoaded_BasicRoundTrip(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-1", []string{"create_agent", "list_agents"})
	loaded := al.sessionLoadedTools("sess-1")
	assert.True(t, loaded["create_agent"])
	assert.True(t, loaded["list_agents"])
}

// TestSessionLoadedTools_IsolatedAcrossSessions proves that a different session
// does not inherit another session's loaded set.
func TestSessionLoadedTools_IsolatedAcrossSessions(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-A", []string{"create_agent"})
	loaded := al.sessionLoadedTools("sess-B")
	assert.Empty(t, loaded, "sess-B must not inherit sess-A's loaded set")
}

// TestSessionLoadedTools_ReturnsCopy proves the returned map is a copy: mutations
// do not affect the internal state.
func TestSessionLoadedTools_ReturnsCopy(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	al.markToolsLoaded("sess-1", []string{"create_agent"})
	copy1 := al.sessionLoadedTools("sess-1")
	copy1["injected"] = true // mutate the returned copy
	copy2 := al.sessionLoadedTools("sess-1")
	assert.False(t, copy2["injected"], "mutating returned copy must not affect internal state")
}

// TestMarkToolsLoaded_EmptySessionID proves nil-safe behavior.
func TestMarkToolsLoaded_EmptySessionID(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	// Must not panic.
	al.markToolsLoaded("", []string{"create_agent"})
	loaded := al.sessionLoadedTools("")
	assert.Empty(t, loaded)
}

// TestMarkToolsLoaded_Concurrency proves no data race when two goroutines write
// to different session IDs simultaneously.
func TestMarkToolsLoaded_Concurrency(t *testing.T) {
	al := &AgentLoop{loadedTools: make(map[string]map[string]bool)}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			al.markToolsLoaded("sess-A", []string{"create_agent"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			al.markToolsLoaded("sess-B", []string{"list_agents"})
		}
	}()
	wg.Wait()
	assert.True(t, al.sessionLoadedTools("sess-A")["create_agent"])
	assert.True(t, al.sessionLoadedTools("sess-B")["list_agents"])
}
