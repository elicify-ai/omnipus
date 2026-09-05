// Omnipus — AskUserQuestion structured clarification tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// AskUserQuestion (askuserquestion-tool-spec.md v3, ADR-074 D4b): the
// owner-session structured clarification tool. Execute NEVER blocks — it
// validates, persists the pending question set via the registry (pkg/askuser,
// UnifiedMeta + in-process registry), emits the card via the registry's card
// sink, and returns the park-time pending stub with ToolResult.ParksTurn set
// (the message_parent(question:true) precedent — pkg/agent/loop.go honors
// ParksTurn as the single turn-park signal and ends the turn
// TurnEndStatusParked). The answer never returns as a tool result: the
// server-side submission path (askuser.Registry.Submit) starts the resume
// turn with the §0.2 correlated user-role answers message.
//
// Owner sessions have NO LifecycleRecord and this tool creates none (§0.4):
// ParksTurn alone drives the loop seam; the delegated-session boot sweep and
// list_jobs are untouched.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/askuser"
)

// AskUserQuestionToolName is the catalog name (allStaticToolNames member).
const AskUserQuestionToolName = "AskUserQuestion"

// webChannelName is the SPA session origin — the ONLY origin the card can
// render on (US-5, operator ruling 2026-09-05: the tool is web-only and
// permanently blocked on every other channel). The value is "webchat":
// that is the bus channel the gateway's WS/SSE chat handlers stamp on every
// SPA turn (pkg/gateway/websocket.go's bus.InboundMessage{Channel:
// "webchat"}), which is what ToolChannel(ctx) returns here via
// registry.ExecuteWithContext(..., ts.channel, ...). The W9a draft used
// "web", which no real turn ever carries — the gate would have blocked the
// tool on the SPA itself (fixed in the W9b wire commit).
const webChannelName = "webchat"

// AskUserQuestionRegistry is the narrow registry seam the tool needs
// (implemented by *askuser.Registry; the gateway wires the live instance).
type AskUserQuestionRegistry interface {
	CreatePending(set *askuser.PendingSet) error
	PendingForSession(sessionID string) (*askuser.PendingSet, bool)
	CancelOnSessionStop(key string) bool
}

// AskUserQuestionTool implements the AskUserQuestion tool.
type AskUserQuestionTool struct {
	BaseTool
	// registryFn resolves the live registry per call (late-bound, the
	// SetPlanStore-style seam: registerSharedTools registers this tool
	// before the gateway constructs the registry). A nil fn or nil result
	// fails CLOSED with a clear error — never a silent park.
	registryFn func() AskUserQuestionRegistry
	newCardID  func() string
}

// NewAskUserQuestionTool constructs the tool. registryFn may be nil for the
// metadata catalog (never Execute()d there); Execute fails closed on a nil/
// unwired registry.
func NewAskUserQuestionTool(registryFn func() AskUserQuestionRegistry) *AskUserQuestionTool {
	return &AskUserQuestionTool{
		registryFn: registryFn,
		newCardID:  askuser.NewCardID,
	}
}

// Name implements Tool.
func (t *AskUserQuestionTool) Name() string { return AskUserQuestionToolName }

// Description implements Tool.
func (t *AskUserQuestionTool) Description() string {
	return "Ask the human user up to 10 structured clarification questions on a single card, each with 2-6 options (plus an always-available free-text answer), and pause until they answer. Only usable on a web (SPA) session you own: on channel sessions ask conversationally in plain language instead, and as a delegated session use message_parent(kind=question, wait=true) toward your parent. Mark an option `recommended` to surface it first; add `default_safe: true` (requires `recommended`) to let it auto-resolve to the recommendation after 30 minutes without an answer."
}

// Scope implements Tool.
func (t *AskUserQuestionTool) Scope() ToolScope { return ScopeGeneral }

// Category implements Tool.
func (t *AskUserQuestionTool) Category() ToolCategory { return CategoryCommunication }

// Parameters implements Tool.
func (t *AskUserQuestionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    askuser.MaxQuestions,
				"description": "The questions to ask (1-10). The user answers all of them on one card.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"header": map[string]any{
							"type":        "string",
							"maxLength":   askuser.MaxHeaderChars,
							"description": "Short unique tab label for this question (max 16 chars).",
						},
						"question": map[string]any{
							"type":        "string",
							"maxLength":   askuser.MaxQuestionChars,
							"description": "The question text (max 500 chars).",
						},
						"options": map[string]any{
							"type":        "array",
							"minItems":    askuser.MinOptions,
							"maxItems":    askuser.MaxOptions,
							"description": "2-6 options; labels must be unique within the question.",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"label": map[string]any{
										"type":      "string",
										"maxLength": askuser.MaxOptionLabel,
									},
									"description": map[string]any{
										"type":      "string",
										"maxLength": askuser.MaxOptionDesc,
									},
								},
								"required": []string{"label"},
							},
						},
						"multi_select": map[string]any{
							"type":        "boolean",
							"description": "Allow selecting several options (answer becomes a list).",
						},
						"recommended": map[string]any{
							"type":        "string",
							"description": "Label of the recommended option (rendered first, badged; never pre-selected).",
						},
						"default_safe": map[string]any{
							"type":        "boolean",
							"description": "Requires `recommended`: after 30 minutes unanswered, this question auto-resolves to the recommendation.",
						},
						"context": map[string]any{
							"type":        "string",
							"maxLength":   askuser.MaxContextChars,
							"description": "Optional markdown context rendered above the options (display-only).",
						},
					},
					"required": []string{"header", "question", "options"},
				},
			},
		},
		"required": []string{"questions"},
	}
}

// Execute implements Tool. See the file doc comment for the park contract.
func (t *AskUserQuestionTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// --- parse + validate (spec §2 — before any gate, so a malformed call
	// gets the precise validation error rather than a liveness one) ---
	questions, err := parseAskQuestions(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if err := askuser.ValidateQuestions(questions); err != nil {
		return ErrorResult(err.Error())
	}

	// --- liveness / caller-scope gates (US-5, US-6 S4, EC-6/EC-9/EC-10) ---
	if ToolDelegationDepth(ctx) > 0 {
		return ErrorResult("AskUserQuestion is owner-session-only: a delegated session asks its PARENT, not the human — use message_parent(kind=\"question\", wait=true) instead (EC-9).")
	}
	if ToolAutoDenyAsk(ctx) {
		return ErrorResult("no_human_surface: this turn runs unattended (scheduled/headless) — nobody is there to answer. Proceed on your best judgment or record the open question in your result.")
	}
	if ch := ToolChannel(ctx); ch != webChannelName {
		// Permanent operator ruling (US-5, interview #5 2026-09-05): the tool
		// is web-only. On channel-origin sessions the agent asks its question
		// conversationally, in plain language — no card, no machinery.
		origin := ch
		if origin == "" {
			origin = "non-web"
		}
		return ErrorResult(fmt.Sprintf(
			"no_human_surface: AskUserQuestion is unavailable on %s sessions (web-app only, permanently). Ask your question conversationally instead — write it in plain language as your normal reply and wait for the user's answer.",
			origin))
	}

	reg := t.registry()
	if reg == nil {
		return ErrorResult("AskUserQuestion: no pending-question registry is wired on this deployment — ask your question conversationally in plain language instead.")
	}

	transcriptSessionID := ToolTranscriptSessionID(ctx)
	if transcriptSessionID == "" {
		return ErrorResult("AskUserQuestion: no session context — this tool needs a real, store-backed session to park.")
	}

	set := &askuser.PendingSet{
		CardID:              t.newCardID(),
		RoutingSessionKey:   ToolSessionKey(ctx),
		TranscriptSessionID: transcriptSessionID,
		AgentID:             ToolAgentID(ctx),
		Channel:             ToolChannel(ctx),
		ChatID:              ToolChatID(ctx),
		Owner:               ToolSessionOwner(ctx),
		Questions:           questions,
		Status:              askuser.StatusPending,
	}

	if err := reg.CreatePending(set); err != nil {
		switch {
		case errors.Is(err, askuser.ErrAlreadyPending):
			return ErrorResult("AskUserQuestion: a question set is already pending on this session (one card per chat surface) — wait for the user's answers before asking again.")
		case errors.Is(err, askuser.ErrSaturated):
			return ErrorResult("AskUserQuestion: the pending-question registry is at capacity — try again later or ask conversationally.")
		case errors.Is(err, askuser.ErrDelegatedChild):
			return ErrorResult("AskUserQuestion is owner-session-only: a delegated session asks its PARENT, not the human — use message_parent(kind=\"question\", wait=true) instead (EC-9).")
		default:
			return ErrorResult(fmt.Sprintf("AskUserQuestion: %v", err)).WithError(err)
		}
	}

	// Park-time pending stub (§0.1, C-R2-1): the tool_result is written
	// immediately — the transcript's tool_use/tool_result adjacency is never
	// left dangling. The MessageParentResponse precedent, mirrored.
	stub, merr := json.Marshal(map[string]any{
		"status":         "pending",
		"card_id":        set.CardID,
		"question_count": len(questions),
	})
	var result *ToolResult
	if merr != nil {
		result = NewToolResult(fmt.Sprintf("AskUserQuestion: pending (card_id=%s, question_count=%d)", set.CardID, len(questions)))
	} else {
		result = NewToolResult(string(stub))
	}
	// ParksTurn is set exclusively on the genuine success path (the C2
	// precedent, message_parent.go): the registry has durably persisted the
	// set, so the loop must end this turn TurnEndStatusParked NOW.
	result.ParksTurn = true
	return result
}

func (t *AskUserQuestionTool) registry() AskUserQuestionRegistry {
	if t.registryFn == nil {
		return nil
	}
	return t.registryFn()
}

// parseAskQuestions decodes the tool's `questions` argument into typed
// askuser.Questions. It rejects structurally malformed input (wrong types)
// with precise errors; the semantic table lives in askuser.ValidateQuestions.
func parseAskQuestions(args map[string]any) ([]askuser.Question, error) {
	raw, ok := args["questions"]
	if !ok {
		return nil, fmt.Errorf("questions is required")
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("questions must be an array")
	}
	out := make([]askuser.Question, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("questions[%d] must be an object", i)
		}
		q := askuser.Question{}
		q.Header, _ = m["header"].(string)
		q.Question, _ = m["question"].(string)
		q.Recommended, _ = m["recommended"].(string)
		q.Context, _ = m["context"].(string)
		q.MultiSelect, _ = m["multi_select"].(bool)
		q.DefaultSafe, _ = m["default_safe"].(bool)
		rawOpts, ok := m["options"].([]any)
		if !ok {
			return nil, fmt.Errorf("questions[%d].options must be an array", i)
		}
		for j, ro := range rawOpts {
			om, ok := ro.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("questions[%d].options[%d] must be an object", i, j)
			}
			var o askuser.Option
			o.Label, _ = om["label"].(string)
			o.Description, _ = om["description"].(string)
			q.Options = append(q.Options, o)
		}
		out = append(out, q)
	}
	return out, nil
}
