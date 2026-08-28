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
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// hydrationLock serializes concurrent HydrateAgentHistoryFromTranscript
// attempts for the same per-agent session key (M5). Two inbound messages for
// the same session can both reach the "self-heal" hydrate trigger in
// processMessage concurrently; without this, both goroutines could run the
// FR-045 empty check before either has written, both proceed, and the loser's
// SetHistory silently no-ops against SessionWriter's fire-and-forget contract
// (ErrArchiveNotEmpty swallowed inside pkg/session — see the write-landed
// check below for why that matters here). Reuses the project's canonical
// striped-mutex pattern (pkg/task/lock.go::StripedLock, already used by
// pkg/memory's sessionLock) rather than inventing a new one.
var hydrationLock = &task.StripedLock{}

// hydrateSessionKey is the per-agent SessionStore key for a transcript session.
func hydrateSessionKey(agentID, sessionID string) string {
	return fmt.Sprintf("agent:%s:session:%s", agentID, sessionID)
}

// sessionOwnerAgent resolves the agent a transcript session belongs to
// (ActiveAgentID, else AgentID) — "" when the meta names nobody.
func sessionOwnerAgent(store *session.UnifiedStore, sessionID string) string {
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		return ""
	}
	if meta.ActiveAgentID != "" {
		return meta.ActiveAgentID
	}
	return meta.AgentID
}

// AgentArchiveNonEmpty reports whether the session's owning agent already
// holds ≥ 1 archive line for sessionID — the attach path's pre-check
// (ADR-066 D5.5, FR-045): hydration may only FILL an empty archive, never
// touch an existing one. Anything unresolvable answers false; hydration
// then runs and its own per-agent guard (and SetHistory's refusal) still
// protects a non-empty file.
func (al *AgentLoop) AgentArchiveNonEmpty(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	store := al.ResolveSessionStore(sessionID)
	if store == nil {
		return false
	}
	owner := sessionOwnerAgent(store, sessionID)
	if owner == "" {
		return false
	}
	registry := al.GetRegistry()
	if registry == nil {
		return false
	}
	ag, ok := registry.GetAgent(owner)
	if !ok || ag == nil || ag.Sessions == nil {
		return false
	}
	hasLines, known := agentArchiveHasLines(ag, hydrateSessionKey(owner, sessionID))
	if !known {
		// Unreadable is not empty. Answering "non-empty" here keeps the
		// attach path off a session whose archive we cannot inspect.
		return true
	}
	return hasLines
}

// agentArchiveHasLines counts the archive from line 0 (ReadArchive ignores
// Skip), so a fully evicted window still counts as a non-empty archive.
//
// The second return is whether the answer is KNOWN. A read failure is NOT
// "empty": an archive can be temporarily unreadable (e.g. an over-long line
// trips bufio.Scanner) while being a real, live record. Reporting it empty is
// how hydration used to overwrite-and-flag a live session — see
// HydrateAgentHistoryFromTranscript. Callers must treat unknown as
// "do not hydrate".
func agentArchiveHasLines(ag *AgentInstance, key string) (hasLines, known bool) {
	archived, err := ag.Sessions.ReadArchive(context.Background(), key)
	if err != nil {
		logger.WarnCF("agent.attach", "archive read failed; treating as UNKNOWN (never as empty)",
			map[string]any{"session_key": key, "error": err.Error()})
		return false, false
	}
	return len(archived) > 0, true
}

// openAssistant points at the assistant message a standalone tool_call entry
// attaches to: the most recent assistant message of the same agent in the
// current turn, valid only while nothing but its own tool results follow it.
type openAssistant struct {
	idx    int
	turnID string
}

// HydrateAgentHistoryFromTranscript reads the transcript for sessionID and
// rebuilds each owning agent's session.SessionStore history under the key
// "agent:<agentID>:session:<sessionID>".
//
// ADR-066 D5.5 (FR-045, FR-046): hydration only ever FILLS an empty archive.
// An agent whose archive already has ≥ 1 line is skipped — bytes, Skip and
// the hydrated flag untouched. When it does run, tool calls are rebuilt from
// the real transcript shape: standalone `type: "tool_call"` entries are
// attached as ToolCalls to the preceding assistant message of the same
// turn/agent (a synthetic, empty assistant message with a WARN when the turn
// has none — the model emitted only tool calls), each followed by exactly one
// role:"tool" line carrying the entry's bounded result through the choke
// point. The rebuilt archive is flagged hydrated: true (FR-048).
//
// The mapping is otherwise best-effort: messages with unknown roles or
// unresolvable agent IDs are skipped. SubTurn entries (orchestrator
// hand-offs) are ignored at this layer — they are reconstructed by the agent
// loop's own subturn machinery on demand.
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
	sessionOwner := sessionOwnerAgent(store, sessionID)

	// Group provider messages per owning agent.
	perAgent := make(map[string][]providers.Message)
	// The assistant message a standalone tool_call entry attaches to, per
	// agent (FR-046). Reset whenever a user-role message starts a new turn.
	open := make(map[string]*openAssistant)
	resetOpen := func() {
		for id := range open {
			delete(open, id)
		}
	}
	// appendToolCallTranscript may append a second entry for one call id
	// (the approval-gate placeholder fallthrough); only the LAST record of an
	// id is hydrated so the archive carries exactly one tool line per call.
	lastRecord := make(map[string]int)
	for i := range entries {
		if entries[i].Type != session.EntryTypeToolCall {
			continue
		}
		for j := range entries[i].ToolCalls {
			if id := string(entries[i].ToolCalls[j].ID); id != "" {
				lastRecord[id] = i
			}
		}
	}

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
			resetOpen()
			continue
		}
		// FR-046: a standalone tool_call entry (the real transcript shape —
		// Type "tool_call", no Role) becomes a ToolCall on the open
		// assistant message of its turn plus one tool-result line.
		if e.Type == session.EntryTypeToolCall {
			for j := range e.ToolCalls {
				tc := &e.ToolCalls[j]
				id := string(tc.ID)
				if id == "" || lastRecord[id] != i {
					continue
				}
				al.attachStandaloneToolCall(perAgent, open, owner, e.TurnID, tc, sessionID)
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
			resetOpen()
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
			open[owner] = &openAssistant{idx: len(perAgent[owner]) - 1, turnID: e.TurnID}
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
		key := hydrateSessionKey(agentID, sessionID)
		al.hydrateOneAgent(ag, agentID, sessionID, key, msgs)
	}
	return nil
}

// hydrateOneAgent runs the FR-045/FR-048 check-write-verify-mark sequence for
// one agent's reconstructed history. M5: the whole sequence is serialized per
// session key via hydrationLock, so two concurrent hydration attempts for the
// SAME session (e.g. two inbound messages both tripping processMessage's
// self-heal trigger) cannot interleave their empty-check and write.
func (al *AgentLoop) hydrateOneAgent(ag *AgentInstance, agentID, sessionID, key string, msgs []providers.Message) {
	mu := hydrationLock.Get(key)
	mu.Lock()
	defer mu.Unlock()

	// FR-045: fill only an empty archive. A non-empty one is the live
	// record — bytes, Skip and flags stay exactly as they are. An
	// UNREADABLE one is not empty either: hydrating it would flag a real
	// session hydrated on the strength of a write that SetHistory refuses.
	hasLines, known := agentArchiveHasLines(ag, key)
	if hasLines || !known {
		logger.InfoCF("agent.attach", "skip hydration; agent archive is non-empty or unreadable",
			map[string]any{
				"agent_id": agentID, "session_key": key,
				"session_id": sessionID, "archive_readable": known,
			})
		return
	}
	ag.Sessions.SetHistory(key, msgs)
	// M5: SessionWriter.SetHistory is fire-and-forget by interface contract
	// (pkg/session logs and swallows ErrArchiveNotEmpty internally), so this
	// call site has NO direct signal of whether ITS OWN write actually
	// landed. hydrationLock above closes the hydrate-vs-hydrate half of the
	// race entirely (two concurrent hydration attempts for this key can no
	// longer interleave); it does not close a race against a DIFFERENT actor
	// — e.g. this session's own first live turn, running on another
	// goroutine, appending its user message to the same key between the
	// FR-045 check above and here. A bare "is the archive non-empty now"
	// re-read cannot tell the two apart: ANY write by ANYONE flips it — which
	// is exactly how a live archive used to get permanently MarkHydrated'd
	// (every later recall_conversation(tool_call_id) then answers "session
	// was rebuilt", turning every real [capped]/[emptied] mark into a dead
	// pointer, while the operator log claims hydration succeeded).
	// Comparing the read-back CONTENT against the exact payload this call
	// intended to write is a far stronger signal — it is fooled only by
	// another writer producing byte-identical role/content/tool_call_id
	// sequences, which a live turn's own message never will. This is still
	// an inference from a re-read, not a true write acknowledgement from
	// SetHistory: the fully authoritative fix is SessionWriter.SetHistory
	// itself reporting success/failure — a pkg/session interface change,
	// out of scope where this function lives (pkg/agent).
	archived, err := ag.Sessions.ReadArchive(context.Background(), key)
	if err != nil || !archiveMatchesHydratedMessages(archived, msgs) {
		logger.ErrorCF("agent.attach", "hydration write did not land; not marking hydrated",
			map[string]any{
				"agent_id": agentID, "session_key": key,
				"session_id": sessionID, "message_count": len(msgs),
				"archive_readable": err == nil,
			})
		return
	}
	// FR-048: recall by tool_call_id cannot promise the original result
	// bytes for a transcript-rebuilt archive.
	ag.Sessions.MarkHydrated(key)
	if err := ag.Sessions.Save(key); err != nil {
		logger.WarnCF("agent.attach", "save hydrated history failed",
			map[string]any{"agent_id": agentID, "session_key": key, "error": err.Error()})
		return
	}
	logger.InfoCF("agent.attach", "hydrated agent history from transcript",
		map[string]any{
			"agent_id":      agentID,
			"session_key":   key,
			"session_id":    sessionID,
			"message_count": len(msgs),
		})
}

// archiveMatchesHydratedMessages reports whether archived is EXACTLY the
// hydration payload this call intended to write — same length, and every
// message's Role, Content and ToolCallID equal (M5). Used in place of a bare
// non-empty check to tell "my write landed" apart from "someone else's write
// landed first" — see hydrateOneAgent's doc comment for the full rationale.
func archiveMatchesHydratedMessages(archived []memory.ArchivedMessage, msgs []providers.Message) bool {
	if len(archived) != len(msgs) {
		return false
	}
	for i, m := range msgs {
		a := archived[i]
		if a.Role != m.Role || a.Content != m.Content || a.ToolCallID != m.ToolCallID {
			return false
		}
	}
	return true
}

// attachStandaloneToolCall implements FR-046 for one recorded call: the call
// joins the open assistant message of the same turn/agent — or a synthetic
// one when the turn has no assistant text before the call (the model emitted
// only tool calls), logged at WARN — and exactly one role:"tool" line with the
// entry's bounded result follows.
//
// The open message is valid only when every message after it is one of its
// own tool results (provider ordering: results must directly follow the
// assistant message that issued the calls) and, when both sides carry a turn
// id, the ids agree. Entries written before tool_call entries were stamped
// with a turn id fall back to the turn boundary (the last user message).
func (al *AgentLoop) attachStandaloneToolCall(
	perAgent map[string][]providers.Message,
	open map[string]*openAssistant,
	owner, turnID string,
	tc *session.ToolCall,
	sessionID string,
) {
	msgs := perAgent[owner]
	o := open[owner]
	valid := o != nil && o.idx < len(msgs) && msgs[o.idx].Role == "assistant" &&
		(turnID == "" || o.turnID == "" || turnID == o.turnID)
	if valid {
		for _, m := range msgs[o.idx+1:] {
			if m.Role != "tool" {
				valid = false
				break
			}
		}
	}
	if !valid {
		logger.WarnCF("agent.attach", "standalone tool_call has no preceding assistant message in its turn; synthesising one",
			map[string]any{
				"agent_id":     owner,
				"session_id":   sessionID,
				"turn_id":      turnID,
				"tool_call_id": string(tc.ID),
				"tool":         tc.Tool,
			})
		msgs = append(msgs, providers.Message{Role: "assistant"})
		o = &openAssistant{idx: len(msgs) - 1, turnID: turnID}
		open[owner] = o
	}
	args := tc.Parameters
	msgs[o.idx].ToolCalls = append(msgs[o.idx].ToolCalls, providers.ToolCall{
		ID:   string(tc.ID),
		Type: "function",
		Function: &providers.FunctionCall{
			Name:      tc.Tool,
			Arguments: marshalToolArgs(args),
		},
		Name:      tc.Tool,
		Arguments: args,
	})
	msgs = append(msgs, al.hydratedToolResultMessage(owner, tc))
	perAgent[owner] = msgs
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
	// No archive available here; line is always -1, which makes
	// turnNumberForArchiveLine return 1 regardless of what archive is
	// passed — this call site's turn is always reported as 1 by
	// construction, matching current behavior.
	content, _ := projectToolResult(marshalToolResult(tc), policy.effectiveCap(surfaceBuiltinSuccess, 1),
		func(full string) string {
			return capMarkOrEmpty(tc.Tool, string(tc.ID), -1, full, turnNumberForArchiveLine(nil, -1))
		})
	return toolResultMessage(string(tc.ID), content, nil)
}
