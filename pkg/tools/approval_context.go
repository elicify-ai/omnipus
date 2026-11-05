// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "context"

// ApprovalContextProvider is implemented by an ask-gated tool that must show
// the person server-derived, value-free facts BEFORE they approve (for
// example remove_secret's consumer list, FR-077 / BDD-15.1). The returned
// keys are added to the approval request's arguments only — never to the
// arguments the tool executes with — and override any same-named key.
type ApprovalContextProvider interface {
	ApprovalContext(ctx context.Context, args map[string]any) map[string]any
}

// WithApprovalContext returns a copy of args with the provider's facts added,
// or args unchanged when tool provides none.
func WithApprovalContext(ctx context.Context, tool Tool, args map[string]any) map[string]any {
	provider, ok := tool.(ApprovalContextProvider)
	if !ok {
		return args
	}
	extra := provider.ApprovalContext(ctx, args)
	if len(extra) == 0 {
		return args
	}
	out := make(map[string]any, len(args)+len(extra))
	for k, v := range args {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// PreApprovalChecker is implemented by an ask-gated tool that can refuse a
// call from its arguments and its caller alone, before any approval card is
// shown (channels-spec-r8 FD-11 and founder Q4 for set_channel_outbound: an
// own-connection or unknown-connection request is refused without asking the
// person to approve something that can never run). A non-nil result is the
// call's final result: the loop records it as the tool's result and neither
// asks for approval nor executes the tool. Nil means "ask as usual".
type PreApprovalChecker interface {
	CheckBeforeApproval(ctx context.Context, args map[string]any) *ToolResult
}

// RefuseBeforeApproval returns tool's pre-approval refusal for args, or nil
// when tool has no such check or accepts the call for approval.
func RefuseBeforeApproval(ctx context.Context, tool Tool, args map[string]any) *ToolResult {
	checker, ok := tool.(PreApprovalChecker)
	if !ok {
		return nil
	}
	return checker.CheckBeforeApproval(ctx, args)
}
