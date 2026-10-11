// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"
)

// AnswerRequestTool is how an agent answers a request it was sent (a peer's
// ask, a connector message in its main). Answering is an explicit tool call so
// the contract is unambiguous: a plain reply in the conversation reaches
// nobody. It is a thin front for the SAME reply admission send_message's
// reply_to form uses (MessageTool.executeReplyForm), so there is exactly one
// reply path, one authorization and one round-send tracker.
type AnswerRequestTool struct {
	msg *MessageTool
}

// NewAnswerRequestTool returns the tool over msg's reply seam. msg must be the
// agent's own registered MessageTool so the accepted reply counts as that
// round's send (no second, automatic reply from the same turn).
func NewAnswerRequestTool(msg *MessageTool) *AnswerRequestTool {
	return &AnswerRequestTool{msg: msg}
}

func (t *AnswerRequestTool) Name() string { return "answer_request" }

func (t *AnswerRequestTool) Description() string {
	return "Answer a request you were sent. Use the request id shown in the request. " +
		"A plain reply in this conversation does not reach the sender."
}

func (t *AnswerRequestTool) Scope() ToolScope       { return ScopeGeneral }
func (t *AnswerRequestTool) Category() ToolCategory { return CategoryCommunication }

func (t *AnswerRequestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "The id of the request you are answering.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Your answer.",
			},
		},
		"required": []string{"request_id", "text"},
	}
}

func (t *AnswerRequestTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	id, idOK := args["request_id"].(string)
	text, textOK := args["text"].(string)
	if !idOK || !textOK {
		return &ToolResult{ForLLM: "request_id and text are required strings", IsError: true}
	}
	if t.msg == nil {
		return addrRefuseWith(ErrReplyForm, "answering a request is not configured here; nothing was sent")
	}
	if denied := t.msg.denySteeredAddressing(ctx); denied != nil {
		return denied
	}
	res := t.msg.executeReplyForm(ctx, text, id, false, false)
	if !res.IsError {
		res.ForLLM = fmt.Sprintf("Answer to request %s accepted for sending.", id)
	}
	return res
}
