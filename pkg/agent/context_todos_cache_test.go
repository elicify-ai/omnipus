package agent

import "testing"

// Repeated shared-tool wiring can revisit an existing builder on the fast
// single-agent update path. Both prompt caches must follow an actual flag change.
func TestContextBuilderSetTodosRewireCaches(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	t.Cleanup(cb.Memory().Close)
	inst := &AgentInstance{ContextBuilder: cb}
	assertTodosGuidancePrompt(t, inst, true, true)
	for _, enabled := range []bool{false, false, true, true} {
		cb.WithSetTodosEnabled(enabled)
		assertTodosGuidancePrompt(t, inst, enabled, true)
	}
}
