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
