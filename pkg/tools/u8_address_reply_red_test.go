// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// u8_address_reply_red_test.go — RED pack for session-core U8
// (PLAN.md row U8; docs/internal/specs/session-core-spec.md, C-ADDRESS/C-REPLY,
// FR-027/FR-028/FR-045; BDD-08.2/08.3/08.4/08.9; T10 PerSourcePeerReturnAddressing).
//
// Every expected value here comes from the spec, never from the implementation:
//
//   - C-REPLY / FR-028: "Existing send_message adds optional reply_to:string.
//     Reply form requires content+reply_to and excludes supplied
//     channel/chat/recipient destination."
//   - C-ADDRESS / FR-045: "Recipient | `{workspace_id, agent_id}` pair in
//     current message tool/intake/MessageFrame ... Invalid pair refuses."
//   - BDD-08.4: mixed-source output with a missing/null usable return
//     correlation refuses visibly, with zero guessed/default outbound send.
//
// These are the message-tool half of U8's C-ADDRESS/C-REPLY wire surface; the
// intake/connector half (request-id minting, source-owner return adapter, live
// egress binding check) needs the T18 integration harness and is out of a
// pkg/tools unit pack's reach.
//
// On this branch the whole unit is unimplemented (UNIT-AUDIT-20261010, "U8:
// missing = everything"), so each test below fails on its named spec reason,
// not on a compile error: every assertion uses only the tool's existing public
// surface (Parameters(), SetSendCallback, Execute) and the existing tool
// context helpers.
package tools

import (
	"context"
	"strings"
	"testing"
)

// u8TurnCtx is the inbound turn every case below is executed inside: the agent
// named in the turn's own conversation. The turn's channel/chat is a valid
// destination today, which is exactly why "the reply form must not fall back to
// it" is a real oracle rather than a trivially-refused one.
func u8TurnCtx() context.Context {
	ctx := WithToolContext(context.Background(), "webchat", "chat-1")
	ctx = WithAgentID(ctx, "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	return ctx
}

// u8Props returns send_message's JSON-schema property map. The tool's parameter
// schema IS the wire shape the model sees, and C-REPLY/C-ADDRESS both describe
// their changes as additions to "the current message tool".
func u8Props(t *testing.T) map[string]any {
	t.Helper()
	params := NewMessageTool().Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("send_message Parameters() has no properties object; got %T for %q", params["properties"], "properties")
	}
	return props
}

// u8CollectPropertyKeys walks a JSON-schema subtree and returns every property
// name declared anywhere inside it (including nested objects, array items and
// anyOf branches), so a caller that nests the recipient pair under a wrapper
// key still satisfies the pair-presence check.
func u8CollectPropertyKeys(node any, out map[string]struct{}) {
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, sub := range props {
			out[name] = struct{}{}
			u8CollectPropertyKeys(sub, out)
		}
	}
	for _, key := range []string{"items", "additionalProperties"} {
		if sub, ok := m[key]; ok {
			u8CollectPropertyKeys(sub, out)
		}
	}
	if branches, ok := m["anyOf"].([]any); ok {
		for _, b := range branches {
			u8CollectPropertyKeys(b, out)
		}
	}
}

// TestSendMessageParameters_ExposeReplyToSelector covers C-REPLY / FR-028:
// "Existing send_message adds optional reply_to:string." The selector must be a
// declared string parameter, and it must be OPTIONAL (the spec says "optional";
// ordinary sends keep working without it).
func TestSendMessageParameters_ExposeReplyToSelector(t *testing.T) {
	props := u8Props(t)

	raw, ok := props["reply_to"]
	if !ok {
		t.Fatalf("BLOCKED: send_message declares no `reply_to` parameter — C-REPLY/FR-028 "+
			"require the reply selector `reply_to:string` so the model can address its "+
			"answer at the original sender. Declared properties: %s", u8Sorted(props))
	}

	replyTo, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("send_message `reply_to` is %T, want a JSON-schema object with a string type", raw)
	}
	if got, _ := replyTo["type"].(string); got != "string" {
		t.Errorf("send_message `reply_to` type = %q, want \"string\" (C-REPLY: reply_to:string)", got)
	}

	// "optional reply_to:string" — an ordinary send must not be forced to name one.
	if required := NewMessageTool().Parameters()["required"]; required != nil {
		if list, ok := required.([]string); ok {
			for _, name := range list {
				if name == "reply_to" {
					t.Errorf("send_message lists `reply_to` as required (%v); C-REPLY says it is optional", list)
				}
			}
		}
	}
}

// TestSendMessageParameters_ExposePeerRecipientPair covers C-ADDRESS / FR-045:
// the recipient is the `{workspace_id, agent_id}` pair, carried by the current
// message tool. The spec fixes both field names; it does not fix the container
// key, so this asserts the two field names appear as declared properties
// anywhere in the schema, accepting either a top-level pair or a nested one.
func TestSendMessageParameters_ExposePeerRecipientPair(t *testing.T) {
	keys := map[string]struct{}{}
	u8CollectPropertyKeys(NewMessageTool().Parameters(), keys)

	for _, field := range []string{"workspace_id", "agent_id"} {
		if _, ok := keys[field]; !ok {
			t.Errorf("BLOCKED: send_message declares no `%s` property — C-ADDRESS/FR-045 address a "+
				"peer by the `{workspace_id, agent_id}` pair. Declared properties: %s",
				field, u8SortedKeys(keys))
		}
	}
}

// TestSendMessage_ReplyFormRefusesExplicitDestination covers C-REPLY / FR-028:
// the reply form "requires content+reply_to and excludes supplied
// channel/chat/recipient destination." Supplying a destination alongside
// reply_to is the mixed-source mistake FR-028 exists to stop, so the tool must
// refuse it outright — a refusal the send callback can prove, because a refusal
// that still sent would be no refusal at all.
//
// The supplied destination here is the turn's OWN conversation, which today's
// tool accepts and delivers to. That is deliberate: a destination today's code
// already refuses would make this test pass for the wrong reason.
func TestSendMessage_ReplyFormRefusesExplicitDestination(t *testing.T) {
	tool := NewMessageTool()
	sent := false
	tool.SetSendCallback(func(_, _, _ string, _ SendOrigin) error {
		sent = true
		return nil
	})

	result := tool.Execute(u8TurnCtx(), map[string]any{
		"content":  "the answer to your request",
		"reply_to": "q1",
		"channel":  "webchat",
		"chat_id":  "chat-1",
	})

	if !result.IsError {
		t.Errorf("reply form with an explicit channel/chat_id was accepted (ForLLM=%q). C-REPLY/FR-028: "+
			"the reply form excludes supplied channel/chat/recipient destination — the destination comes "+
			"from reply_to alone.", result.ForLLM)
	}
	if sent {
		t.Error("reply form with an explicit destination invoked the send callback. FR-028/BDD-08.4 forbid " +
			"any guessed/default outbound send: an unusable or over-specified reply must send nothing.")
	}
}

// TestSendMessage_ReplyFormRefusesUnusableCorrelation covers BDD-08.4 / FR-028:
// a reply whose return correlation is not usable must refuse visibly, with zero
// guessed/default outbound send. A blank reply_to is exactly the "missing/null
// usable return correlation" case.
//
// The tool must not fall back to the turn's own conversation: that fallback IS
// the last-sender/default-destination behaviour FR-028 deletes.
func TestSendMessage_ReplyFormRefusesUnusableCorrelation(t *testing.T) {
	tool := NewMessageTool()
	sent := false
	tool.SetSendCallback(func(_, _, _ string, _ SendOrigin) error {
		sent = true
		return nil
	})

	result := tool.Execute(u8TurnCtx(), map[string]any{
		"content":  "the answer to your request",
		"reply_to": "   ",
	})

	if !result.IsError {
		t.Errorf("reply form with a blank reply_to was accepted (ForLLM=%q). BDD-08.4/FR-028: a missing or "+
			"unusable return correlation must refuse visibly instead of guessing a destination.", result.ForLLM)
	}
	if sent {
		t.Error("reply form with an unusable correlation invoked the send callback. FR-028/BDD-08.4: " +
			"zero guessed/default outbound send — no fallback to the turn's own conversation.")
	}
}

// TestSendMessage_PeerTargetRefusesInvalidPair covers C-ADDRESS / FR-045:
// "Invalid pair refuses." An empty agent_id addresses nobody. The tool must
// refuse rather than fall through to the turn's own conversation — the
// default-destination fallback FR-045 also forbids.
func TestSendMessage_PeerTargetRefusesInvalidPair(t *testing.T) {
	tool := NewMessageTool()
	sent := false
	tool.SetSendCallback(func(_, _, _ string, _ SendOrigin) error {
		sent = true
		return nil
	})

	result := tool.Execute(u8TurnCtx(), map[string]any{
		"content":      "peer message",
		"workspace_id": "ws-2",
		"agent_id":     "",
	})

	if !result.IsError {
		t.Errorf("peer send with an empty agent_id was accepted (ForLLM=%q). C-ADDRESS/FR-045: an invalid "+
			"`{workspace_id, agent_id}` pair refuses; it must not fall back to the turn's conversation.", result.ForLLM)
	}
	if sent {
		t.Error("peer send with an invalid pair invoked the send callback. FR-045: a refused recipient " +
			"admits nothing — no no-fallback/default-destination send.")
	}
}

// u8Sorted lists declared property names for a failure message.
func u8Sorted(props map[string]any) string {
	keys := map[string]struct{}{}
	for k, v := range props {
		keys[k] = struct{}{}
		u8CollectPropertyKeys(v, keys)
	}
	return u8SortedKeys(keys)
}

func u8SortedKeys(keys map[string]struct{}) string {
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	// Deterministic order so a failure message is reproducible.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return "[" + strings.Join(names, ", ") + "]"
}
