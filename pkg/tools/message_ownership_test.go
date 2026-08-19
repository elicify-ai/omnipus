// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// fakeOwnership is a hand-built ChannelOwnership over a literal table.
//
// Deliberately not a mock framework: the whole rule is "which pair owns this
// instance", and a table makes the fixture readable as the ownership diagram
// it is.
type fakeOwnership struct {
	// owner maps instance id -> "workspace/agent". Absent means not bound.
	owner map[string]string
}

func (f fakeOwnership) OwnerOf(instanceID string) (string, string, bool) {
	v, ok := f.owner[instanceID]
	if !ok {
		return "", "", false
	}
	parts := strings.SplitN(v, "/", 2)
	return parts[0], parts[1], true
}

func (f fakeOwnership) OwnedBy(workspaceID, agentID string) []string {
	var out []string
	for inst, v := range f.owner {
		if v == workspaceID+"/"+agentID {
			out = append(out, inst)
		}
	}
	sort.Strings(out)
	return out
}

// ownershipFixture: mia owns telegram in W1, ava owns slack in W1, and jim owns
// telegram.beta in a DIFFERENT workspace. "discord" is configured but unbound.
func ownershipFixture() fakeOwnership {
	return fakeOwnership{owner: map[string]string{
		"telegram":      "W1/mia",
		"slack":         "W1/ava",
		"telegram.beta": "W2/jim",
	}}
}

func newOwnedTool(t *testing.T, sent *[]string) *MessageTool {
	t.Helper()
	mt := NewMessageTool()
	mt.SetChannelOwnership(ownershipFixture())
	mt.SetSendCallback(func(channel, chatID, content string, _ SendOrigin) error {
		*sent = append(*sent, channel+":"+chatID)
		return nil
	})
	return mt
}

func turnCtx(agentID, workspaceID, channel, chatID string) context.Context {
	ctx := WithToolContext(context.Background(), channel, chatID)
	ctx = WithAgentID(ctx, agentID)
	return WithWorkspaceID(ctx, workspaceID)
}

// TestSendMessage_RepliesIntoItsOwnConversation is the ordinary path, and the
// one that must never be broken by ownership: the turn's conversation is
// always reachable.
func TestSendMessage_RepliesIntoItsOwnConversation(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"), map[string]any{"content": "hi"})
	if res.IsError {
		t.Fatalf("replying into the turn's own conversation must always work: %s", res.ForLLM)
	}
	if len(sent) != 1 || sent[0] != "telegram:chat-1" {
		t.Fatalf("wrong destination: %v", sent)
	}
}

// TestSendMessage_AgentPicksAmongItsOwn pins the operator's decision: the agent
// names the channel, because only it knows in context which one fits.
func TestSendMessage_AgentPicksAmongItsOwn(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	// ava acts, names her own slack instance, with no turn conversation.
	res := mt.Execute(turnCtx("ava", "W1", "", ""), map[string]any{
		"content": "hi", "channel": "slack", "chat_id": "c9",
	})
	if res.IsError {
		t.Fatalf("an agent naming a channel it OWNS must be allowed: %s", res.ForLLM)
	}
	if len(sent) != 1 || sent[0] != "slack:c9" {
		t.Fatalf("wrong destination: %v", sent)
	}
}

// TestSendMessage_CannotUseAnotherAgentsChannelSameWorkspace is the core
// requirement. Team membership on the same workspace confers nothing, exactly
// as it confers no access to another agent's inbox.
func TestSendMessage_CannotUseAnotherAgentsChannelSameWorkspace(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	// mia acts, and targets ava's slack instance. Same workspace, same team.
	res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"), map[string]any{
		"content": "hi", "channel": "slack", "chat_id": "c9",
	})
	if !res.IsError {
		t.Fatal("an agent must NOT reach a teammate's channel — this is the whole point of ADR-065")
	}
	if len(sent) != 0 {
		t.Fatalf("refused send must not reach the bus, got %v", sent)
	}
	if !strings.Contains(res.ForLLM, "ava") {
		t.Errorf("refusal should name the owning agent so the caller understands why; got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "telegram") {
		t.Errorf("refusal should name what the agent DOES own, so it can retry correctly; got: %s", res.ForLLM)
	}
}

// TestSendMessage_CannotReachAnotherWorkspacesChannel: the cross-workspace case
// the ADR exists to close. The target's own credentials would otherwise carry
// the message.
func TestSendMessage_CannotReachAnotherWorkspacesChannel(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"), map[string]any{
		"content": "hi", "channel": "telegram.beta", "chat_id": "c1",
	})
	if !res.IsError {
		t.Fatal("cross-workspace egress must be refused")
	}
	if len(sent) != 0 {
		t.Fatalf("refused send must not reach the bus, got %v", sent)
	}
}

// TestSendMessage_UnboundChannelIsUnchanged pins FR-9 AND the operator's
// webchat ruling in one place: anything not workspace-bound is unowned,
// unenforced and untouched. "webchat" is the shared case that every agent may
// use; "discord" is an instance an operator configured and left unbound.
func TestSendMessage_UnboundChannelIsUnchanged(t *testing.T) {
	for _, target := range []string{"webchat", "discord"} {
		t.Run(target, func(t *testing.T) {
			var sent []string
			mt := newOwnedTool(t, &sent)
			res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"), map[string]any{
				"content": "hi", "channel": target, "chat_id": "c1",
			})
			if res.IsError {
				t.Fatalf("%s is not workspace-bound, so nobody owns it and nothing is enforced: %s",
					target, res.ForLLM)
			}
			if len(sent) != 1 {
				t.Fatalf("expected the send to go through, got %v", sent)
			}
		})
	}
}

// TestSendMessage_ProactiveResolvesTheAgentsOwnChannel: no conversation, no
// named channel — resolve the acting agent's own instance (FR-2).
func TestSendMessage_ProactiveResolvesTheAgentsOwnChannel(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	res := mt.Execute(turnCtx("mia", "W1", "", ""), map[string]any{
		"content": "beat", "chat_id": "c1",
	})
	if res.IsError {
		t.Fatalf("a proactive send must resolve the agent's own channel: %s", res.ForLLM)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "telegram:") {
		t.Fatalf("expected mia's own telegram instance, got %v", sent)
	}
}

// TestSendMessage_ProactiveRefusesWhenAmbiguous: several owned channels and no
// named target. Refusing and naming them is the operator's decision — choosing
// silently is how a message reaches somewhere nobody intended.
func TestSendMessage_ProactiveRefusesWhenAmbiguous(t *testing.T) {
	var sent []string
	mt := NewMessageTool()
	mt.SetChannelOwnership(fakeOwnership{owner: map[string]string{
		"slack": "W1/mia", "telegram": "W1/mia",
	}})
	mt.SetSendCallback(func(c, ch, _ string, _ SendOrigin) error {
		sent = append(sent, c+":"+ch)
		return nil
	})
	res := mt.Execute(turnCtx("mia", "W1", "", ""), map[string]any{"content": "beat", "chat_id": "c1"})
	if !res.IsError {
		t.Fatal("two owned channels and no named target must refuse, not pick one")
	}
	if !strings.Contains(res.ForLLM, "slack") || !strings.Contains(res.ForLLM, "telegram") {
		t.Errorf("refusal must name the candidates so the agent can choose; got: %s", res.ForLLM)
	}
	if len(sent) != 0 {
		t.Fatalf("nothing should have been sent, got %v", sent)
	}
}

// TestSendMessage_ProactiveRefusesWhenAgentOwnsNothing: FR-2's other branch,
// and the visible consequence of the operator's hand-off decision (Q1) — a
// handed-to agent owning no channel goes quiet, audibly.
func TestSendMessage_ProactiveRefusesWhenAgentOwnsNothing(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	res := mt.Execute(turnCtx("nobody", "W1", "", ""), map[string]any{"content": "hi", "chat_id": "c1"})
	if !res.IsError {
		t.Fatal("an agent owning no channel here must be told so, not silently succeed")
	}
	if !strings.Contains(res.ForLLM, "W1") {
		t.Errorf("refusal should name the workspace so an operator can fix it; got: %s", res.ForLLM)
	}
}

// TestSendMessage_NoWorkspaceRefuses: an unbound turn has not established which
// workspace's channels it is entitled to.
func TestSendMessage_NoWorkspaceRefuses(t *testing.T) {
	var sent []string
	mt := newOwnedTool(t, &sent)
	res := mt.Execute(turnCtx("mia", "", "", ""), map[string]any{"content": "hi", "chat_id": "c1"})
	if !res.IsError {
		t.Fatal("no workspace on the turn must refuse rather than guess one")
	}
}

// TestSendMessage_NilOwnershipRefusesAnyOtherTarget pins the fail-CLOSED
// posture. Before SetChannelOwnership runs, the tool cannot check anything —
// and an unanswerable ownership question must not read as permission.
func TestSendMessage_NilOwnershipRefusesAnyOtherTarget(t *testing.T) {
	var sent []string
	mt := NewMessageTool() // no ownership installed
	mt.SetSendCallback(func(c, ch, _ string, _ SendOrigin) error {
		sent = append(sent, c+":"+ch)
		return nil
	})

	// Its own conversation still works — otherwise every agent is mute until
	// wiring completes.
	if res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"),
		map[string]any{"content": "hi"}); res.IsError {
		t.Fatalf("the turn's own conversation must work even without ownership: %s", res.ForLLM)
	}
	// Anything else is refused.
	res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"), map[string]any{
		"content": "hi", "channel": "slack", "chat_id": "c9",
	})
	if !res.IsError {
		t.Fatal("without ownership information the tool must refuse other targets, not assume permission")
	}
}

// TestSendMessage_OriginTravelsWithTheSend pins FR-6's carrier: a send is
// attributable, so a violation is detectable rather than invisible.
func TestSendMessage_OriginTravelsWithTheSend(t *testing.T) {
	var got SendOrigin
	mt := NewMessageTool()
	mt.SetChannelOwnership(ownershipFixture())
	mt.SetSendCallback(func(_, _, _ string, o SendOrigin) error {
		got = o
		return nil
	})
	if res := mt.Execute(turnCtx("mia", "W1", "telegram", "chat-1"),
		map[string]any{"content": "hi"}); res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if got.AgentID != "mia" || got.WorkspaceID != "W1" {
		t.Fatalf("origin must carry the ACTING agent and workspace, got %+v", got)
	}
}
