package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
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

// The yes line has to be in the messages the provider receives, not only in
// the inner formatter (BS-6.16 / TDD row 21).
func TestBuildMessages_UserWatchingYes(t *testing.T) {
	cb := NewContextBuilder(t.TempDir()).WithWatchingInjector(func(sessionID string) bool {
		return sessionID == "sess-yes"
	})
	yes := cb.BuildMessagesForSession("sess-yes", 0, nil, "hello", nil, "W", "webchat", "chat-1", "", "", "", nil)
	if !assembledLine(yes, "User watching: yes") {
		t.Fatalf("assembled watched web turn has no %q (BS-6.16)", "User watching: yes")
	}
	other := cb.BuildMessagesForSession("sess-no", 0, nil, "hello", nil, "W", "webchat", "chat-1", "", "", "", nil)
	if assembledLine(other, "User watching: yes") || !assembledLine(other, "User watching: no") {
		t.Fatalf("assembled unwatched web turn must say no and not yes (BS-6.16)")
	}
	child := cb.BuildMessagesForSession("sess-yes", 0, nil, "hello", nil, "W", "telegram", "chat-1", "", "", "", nil)
	if assembledLine(child, "User watching: yes") || assembledLine(child, "User watching: no") {
		t.Fatal("assembled non-web turn included the watching line")
	}
}

// assembledLine is an exact line, not a substring. The static instructions
// mention both phrases inside a sentence; only the dynamic context line
// stands alone (BS-6.16).
func assembledLine(msgs []providers.Message, line string) bool {
	for _, m := range msgs {
		if hasExactLine(m.Content, line) {
			return true
		}
		for _, block := range m.SystemParts {
			if hasExactLine(block.Text, line) {
				return true
			}
		}
	}
	return false
}

func hasExactLine(text, line string) bool {
	for _, row := range strings.Split(text, "\n") {
		if row == line {
			return true
		}
	}
	return false
}
