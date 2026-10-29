package agent

import (
	"strings"
	"testing"
)

// M12 / BS-6.16: a watched session renders the exact yes line, and another
// session on the same builder does not.
func TestDynamicContext_UserWatchingYes(t *testing.T) {
	cb := NewContextBuilder(t.TempDir()).WithWatchingInjector(func(sessionID string) bool {
		return sessionID == "sess-yes"
	})
	yes := cb.buildDynamicContextFor("sess-yes", 0, "W", "webchat", "chat-1", "", "")
	if !strings.Contains(yes, "User watching: yes") {
		t.Fatalf("watched web turn = %q, want %q (BS-6.16)", yes, "User watching: yes")
	}
	other := cb.buildDynamicContextFor("sess-no", 0, "W", "webchat", "chat-1", "", "")
	if strings.Contains(other, "User watching: yes") || !strings.Contains(other, "User watching: no") {
		t.Fatalf("unwatched web turn = %q, want no and not yes (BS-6.16)", other)
	}
	child := cb.buildDynamicContextFor("sess-yes", 0, "W", "telegram", "chat-1", "", "")
	if strings.Contains(child, "User watching") {
		t.Fatalf("non-web turn included the watching line: %q", child)
	}
}
