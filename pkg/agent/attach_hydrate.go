// Package agent — attach_hydrate.go
//
// HydrateAgentHistoryFromTranscript bridges the gap between the shared
// transcript store (used for UI replay, recap, and audit) and the per-agent
// session.SessionStore that holds the providers.Message history fed to the
// LLM each turn.
//
// Without this bridge, "open past session" only repopulates the SPA chat UI:
// the agent's in-memory history (keyed by routing scope, e.g.
// "agent:<id>:session:<sessionID>") stays empty for the new WS connection, so
// the next LLM call sees no prior context and the agent answers as if the
// conversation just started.

package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// HydrateAgentHistoryFromTranscript reads the transcript for sessionID and
// rebuilds each owning agent's session.SessionStore history under the key
// "agent:<agentID>:session:<sessionID>".
//
// The mapping is best-effort: messages with unknown roles or unresolvable
// agent IDs are skipped. SubTurn entries (orchestrator hand-offs) are ignored
// at this layer — they are reconstructed by the agent loop's own subturn
// machinery on demand.
func (al *AgentLoop) HydrateAgentHistoryFromTranscript(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("agent: HydrateAgentHistoryFromTranscript: sessionID required")
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return fmt.Errorf("agent: HydrateAgentHistoryFromTranscript: no session store for %s", sessionID)
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		return fmt.Errorf("agent: HydrateAgentHistoryFromTranscript: read transcript: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	registry := al.GetRegistry()
	if registry == nil {
		return fmt.Errorf("agent: HydrateAgentHistoryFromTranscript: registry not available")
	}

	// The session's owning agent, used for entries that carry no AgentID of
	// their own. This general fallback stays: it is how a session whose
	// original agent is gone still hydrates. What was removed with the "main"
	// sentinel is the agent-of-last-resort behind it — naming a specific
	// agent when metadata resolves nothing would reattribute one agent's
	// history to another.
	sessionOwner := ""
	if meta, err := store.GetMeta(sessionID); err == nil && meta != nil {
		if meta.ActiveAgentID != "" {
			sessionOwner = meta.ActiveAgentID
		} else if meta.AgentID != "" {
			sessionOwner = meta.AgentID
		}
	}

	// Group provider messages per owning agent.
	perAgent := make(map[string][]providers.Message)

	// First pass — discover every agent that has any presence in this
	// transcript so handoff broadcasts can reach all of them, including
	// agents that have not yet produced a turn (e.g. Ray after Mia
	// handed off to him in the previous turn).
	knownAgents := make(map[string]struct{})
	if sessionOwner != "" {
		knownAgents[sessionOwner] = struct{}{}
	}
	for i := range entries {
		if id := entries[i].AgentID; id != "" {
			knownAgents[id] = struct{}{}
		}
	}

	for i := range entries {
		e := &entries[i]
		owner := e.AgentID
		if owner == "" {
			owner = sessionOwner
		}
		// Neither the entry nor the session names an owner. Skip it rather
		// than inventing one.
		if owner == "" {
			continue
		}
		// Handoff audit entries are written by HandoffTool with Type=System
		// and Content="Handoff: <from> → <to>. Context: <brief>". Broadcast
		// them to every agent seen in the transcript so the target agent
		// receives the brief on its first turn — without this, the new
		// agent's history is empty and the handoff context is lost.
		if e.Type == session.EntryTypeSystem && strings.HasPrefix(e.Content, "Handoff:") {
			msg := providers.Message{Role: "user", Content: e.Content}
			for id := range knownAgents {
				perAgent[id] = append(perAgent[id], msg)
			}
			continue
		}
		switch e.Role {
		case "user":
			if e.Content == "" {
				continue
			}
			// User messages are universal context: every agent participating
			// in this transcript should see what the user asked, regardless
			// of which agent the entry is attributed to (the AgentID on a
			// user entry tracks "which agent the message was directed to",
			// not "which agent owns the question"). Without this, a handed-
			// off agent sees only the handoff announcement and never the
			// user's actual request.
			userMsg := providers.Message{Role: "user", Content: e.Content}
			if len(knownAgents) > 0 {
				for id := range knownAgents {
					perAgent[id] = append(perAgent[id], userMsg)
				}
			} else {
				perAgent[owner] = append(perAgent[owner], userMsg)
			}
		case "assistant":
			msg := providers.Message{Role: "assistant", Content: e.Content}
			for j := range e.ToolCalls {
				tc := &e.ToolCalls[j]
				if tc.ID == "" {
					continue
				}
				args := tc.Parameters
				msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
					ID:   string(tc.ID),
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      tc.Tool,
						Arguments: marshalToolArgs(args),
					},
					Name:      tc.Tool,
					Arguments: args,
				})
			}
			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				continue
			}
			perAgent[owner] = append(perAgent[owner], msg)
			// Emit a tool result per recorded tool call so the next LLM
			// call sees a balanced tool_use / tool_result sequence
			// (Anthropic and others reject orphan tool_use blocks).
			for j := range e.ToolCalls {
				tc := &e.ToolCalls[j]
				if tc.ID == "" {
					continue
				}
				// ADR-066 D4: a hydrated attachment is a builtin-success
				// surface result and passes the choke point's cap like any
				// other (FR-009, no exemption — B-11 row "hydrated
				// attachment"). The transcript `result` is already the
				// bounded form the choke point wrote (FR-046), so the cut
				// only fires for pre-ADR-066 transcripts. SetHistory below
				// stores the window form as the archive line, so no
				// projection state is needed: archive and window agree.
				perAgent[owner] = append(perAgent[owner], al.hydratedToolResultMessage(owner, tc))
			}
		default:
			// system / interruption / unknown — skip; system parts are
			// rebuilt fresh by the ContextBuilder on each turn.
			continue
		}
	}

	for agentID, msgs := range perAgent {
		ag, ok := registry.GetAgent(agentID)
		if !ok || ag == nil || ag.Sessions == nil {
			logger.WarnCF("agent.attach", "skip hydration; agent or session store unavailable",
				map[string]any{"agent_id": agentID, "msg_count": len(msgs)})
			continue
		}
		key := fmt.Sprintf("agent:%s:session:%s", agentID, sessionID)
		ag.Sessions.SetHistory(key, msgs)
		if err := ag.Sessions.Save(key); err != nil {
			logger.WarnCF("agent.attach", "save hydrated history failed",
				map[string]any{"agent_id": agentID, "session_key": key, "error": err.Error()})
		} else {
			logger.InfoCF("agent.attach", "hydrated agent history from transcript",
				map[string]any{
					"agent_id":      agentID,
					"session_key":   key,
					"session_id":    sessionID,
					"message_count": len(msgs),
				})
		}
	}
	return nil
}

func marshalToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func marshalToolResult(tc *session.ToolCall) string {
	// Prefer the result map; fall back to status so the LLM at least sees
	// something tool-shaped instead of an empty string (which some
	// providers reject).
	if len(tc.Result) > 0 {
		if b, err := json.Marshal(tc.Result); err == nil {
			return string(b)
		}
	}
	if tc.Status != "" {
		return fmt.Sprintf(`{"status":%q}`, tc.Status)
	}
	return "{}"
}

// hydratedToolResultMessage builds the tool message for one recorded call,
// capped at the builtin-success surface through the choke point's pure cap.
// The mark cites no archive line (the line does not exist until SetHistory
// writes it) and the turn number of a transcript-rebuilt window is not
// recoverable, so the mark's hint points at recall by id only — which
// D5.5's hydrated flag answers with "rebuilt from the transcript" anyway.
func (al *AgentLoop) hydratedToolResultMessage(owner string, tc *session.ToolCall) providers.Message {
	var cs config.ContextSettings
	if cfg := al.GetConfig(); cfg != nil {
		cs = cfg.Context
	}
	budget := 0
	if registry := al.GetRegistry(); registry != nil {
		if ag, ok := registry.GetAgent(owner); ok && ag != nil {
			budget = agentContextBudget(ag)
		}
	}
	policy := capPolicyFor(cs, budget)
	content, _ := projectToolResult(marshalToolResult(tc), policy.effectiveCap(surfaceBuiltinSuccess, 1),
		func(full string) string { return capMarkOrEmpty(tc.Tool, string(tc.ID), -1, full, nil) })
	return toolResultMessage(string(tc.ID), content, nil)
}
