package agent

import (
	"strconv"
	"strings"
)

// WithSetTodosEnabled gates checklist guidance independently of memory.
// Agent construction and shared-tool wiring set this from registered tools and
// effective policy; reloading config rebuilds that snapshot. Repeated wiring can
// revisit shared builders, so unchanged values preserve both prompt caches.
// This is not synchronized with per-request offered tool lists.
func (cb *ContextBuilder) WithSetTodosEnabled(enabled bool) *ContextBuilder {
	cb.systemPromptMutex.Lock()
	defer cb.systemPromptMutex.Unlock()
	if cb.setTodosEnabled.Load() == enabled {
		return cb
	}
	wc := &cb.workspacePromptCache
	wc.mu.Lock()
	defer wc.mu.Unlock()
	cb.setTodosEnabled.Store(enabled)
	cb.invalidateSystemPromptCacheLocked()
	for workspaceID := range wc.variants {
		cb.evictWorkspaceVariantLocked(workspaceID)
	}
	return cb
}

// renumberOperatingRules closes gaps left by optional checklist/memory rules.
// Only numbered lines inside the operating-rules block are changed; headings,
// instruction bodies, the memory guide and discovery guidance stay untouched.
func renumberOperatingRules(prompt string) string {
	before, rest, ok := strings.Cut(prompt, "<operating_rules>")
	if !ok {
		return prompt
	}
	body, after, ok := strings.Cut(rest, "</operating_rules>")
	if !ok {
		return prompt
	}
	lines := strings.Split(body, "\n")
	next := 1
	for i, line := range lines {
		number, text, numbered := strings.Cut(line, ". ")
		if !numbered {
			continue
		}
		if _, err := strconv.Atoi(number); err != nil {
			continue
		}
		lines[i] = strconv.Itoa(next) + ". " + text
		next++
	}
	return before + "<operating_rules>" + strings.Join(lines, "\n") + "</operating_rules>" + after
}
