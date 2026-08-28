// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-066 D4 — the two bounds that keep the thrash guard unreachable by
// construction (spec FR-015 / FR-016):
//
//   - A user message is bounded where an inbound message becomes a turn —
//     processMessage, before turn registration and before anything is
//     persisted. Over the bound it is answered on the originating channel
//     with a plain, non-fatal reply quoting the size and the live limit; no
//     transcript entry, no turn, no error frame. Media refs ride in
//     bus.InboundMessage.Media, never in Content, so they are not counted.
//   - Tool-call arguments are bounded at dispatch in runTurn: serialised
//     arguments over the cap produce the ADR-060-family structured refusal
//     (tools.ToolArgumentRefusalResult) through the choke point instead of
//     executing, and the turn continues.
//
// Both bounds TRACK the builtin-success cap (config.ContextSettings.
// BuiltinSuccessCap) rather than owning a setting of their own (US-4.AC3 /
// A-2): a single result, a single user message and a single argument set all
// fit the same "one result fits in half the budget" figure, and changing the
// cap changes the quoted limit. Neither bound is budget-clamped the way a
// tool RESULT is (FR-011) — the model-window clamp is about what is
// re-sent every call; a refused message is never sent at all.

// userMessageBound returns the live user-message bound in characters
// (runes): the configured builtin-success cap, or its shipped default when
// the setting is unset (capPolicyFor applies the same fallback for results).
func userMessageBound(cfg *config.Config) int {
	var cs config.ContextSettings
	if cfg != nil {
		cs = cfg.Context
	}
	return capPolicyFor(cs, 0).BuiltinSuccessCap
}

// UserMessageBound is the exported form for the intakes that persist a user
// message BEFORE it reaches processMessage (the WebSocket handler writes the
// transcript entry ahead of the bus publish) — they skip that write for a
// message processMessage is about to refuse, so "no transcript entry" holds
// on every intake. Enforcement itself stays in processMessage.
func (al *AgentLoop) UserMessageBound() int {
	if al == nil {
		return userMessageBound(nil)
	}
	return userMessageBound(al.GetConfig())
}

// UserMessageChars is the one measure both the enforcement site and the
// early-persisting intakes use: characters (runes), media refs excluded.
func UserMessageChars(content string) int {
	return utf8.RuneCountInString(content)
}

// userMessageRefusalReply is the non-fatal reply for an over-bound message.
// It states N and the live limit and tells the user what to do instead; it
// is deliberately plain prose on the user's channel, not an error frame.
func userMessageRefusalReply(sizeChars, boundChars int) string {
	return "That message is " + strconv.Itoa(sizeChars) + " characters; the limit is " +
		strconv.Itoa(boundChars) + ". Attach it as a file or shorten it and send again."
}

// refuseOversizedUserMessage is the processMessage gate (FR-015). It returns
// the reply and true when msg.Content exceeds the bound; the caller returns
// that reply as the turn's response WITHOUT routing, registering or
// persisting anything, and the ordinary response path delivers it on the
// originating channel.
func (al *AgentLoop) refuseOversizedUserMessage(msg bus.InboundMessage) (string, bool) {
	bound := al.UserMessageBound()
	n := UserMessageChars(msg.Content)
	if n <= bound {
		return "", false
	}
	return userMessageRefusalReply(n, bound), true
}

// toolArgumentsBound is the live cap on the serialised arguments of one
// tool call (FR-016); the same figure as the user-message bound.
func toolArgumentsBound(cfg *config.Config) int {
	return userMessageBound(cfg)
}

// serialisedToolArgsChars measures a tool call's arguments the way the
// bound is defined — the length in characters of the JSON serialisation. A
// map that cannot be marshalled (a NaN, a channel) measures 0: the tool's
// own argument validation owns that case, not the size bound.
func serialisedToolArgsChars(args map[string]any) int {
	if len(args) == 0 {
		return 0
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return 0
	}
	return utf8.RuneCount(encoded)
}
