// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestLaunch_RequestedSkillAllowed_InstructionsInChildFirstMessage: a
// delegation loads an allowed requested_skill FOR the child (C-11 E5,
// team-lead Reading A), so the child's persisted first message, which its
// first model request is built from, carries the skill's instructions after
// the task. The same launch without requested_skill carries the task only.
func TestLaunch_RequestedSkillAllowed_InstructionsInChildFirstMessage(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "finance-news")
	al, cleanup := newSteerALWithSkills(t, home, []string{"finance-news"})
	defer cleanup()
	l := NewSteerLauncher(al)

	firstMessage := func(requested, callID string) string {
		t.Helper()
		res, err := l.Launch(context.Background(), steer.LaunchRequest{
			TargetAgentID:  testDefaultAgentID,
			Task:           "pull the latest finance headlines",
			Origin:         steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
			RequestedSkill: requested,
		})
		if err != nil {
			t.Fatalf("Launch(requested_skill=%q) = %v, want nil error", requested, err)
		}
		history := al.GetSessionStore().GetHistory(res.SessionID)
		if len(history) == 0 {
			t.Fatalf("child %q has no history", res.SessionID)
		}
		return history[0].Content
	}

	with := firstMessage("finance-news", "call-skill-loaded")
	if !strings.HasPrefix(with, "pull the latest finance headlines") {
		t.Fatalf("first message does not start with the task: %q", with)
	}
	if !strings.Contains(with, "# finance-news\n\nBody.") {
		t.Fatalf("first message does not carry the skill's instructions: %q", with)
	}

	without := firstMessage("", "call-no-skill")
	if without != "pull the latest finance headlines" {
		t.Fatalf("control: a launch without requested_skill carries the task only, got %q", without)
	}
}
