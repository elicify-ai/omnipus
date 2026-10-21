package agent

import (
	"strconv"
	"strings"
)

// WithSetTodosEnabled gates checklist guidance independently of memory.
// Call once before prompt caching or shared use. The default is true, matching
// the prior unconditional guidance and the memory gate's construction pattern.
// This interim flag is not synchronized with per-request offered tool lists.
func (cb *ContextBuilder) WithSetTodosEnabled(enabled bool) *ContextBuilder {
	cb.setTodosEnabled = enabled
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
