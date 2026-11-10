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
	return WithApprovalFacts(args, provider.ApprovalContext(ctx, args))
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

// ApprovalPreparer is implemented by an ask-gated tool that, BEFORE its
// approval card is shown, must run every validity check it can, read the
// facts the person needs to decide from the server and pin the state it
// checked (Channels Q2-B: set_channel_outbound reads the current setting
// itself). One call yields all three from one read, so what was checked and
// what is shown cannot differ. A non-nil refusal is the call's final result:
// the loop records it and neither asks nor executes. Otherwise facts are
// added to the approval request's arguments only (never to the executed
// arguments) and pin rides the execution context (WithApprovalPin), never
// the arguments. A tool implementing this is not also asked through
// PreApprovalChecker or ApprovalContextProvider.
type ApprovalPreparer interface {
	PrepareForApproval(ctx context.Context, args map[string]any) (facts map[string]any, pin any, refusal *ToolResult)
}

// WithApprovalFacts returns a copy of args with facts added (facts win on a
// name clash), or args unchanged when there are none.
func WithApprovalFacts(args, facts map[string]any) map[string]any {
	if len(facts) == 0 {
		return args
	}
	// No len(args)+len(facts) capacity. That add inside make is the
	// go/allocation-size-overflow pattern; the hint is not semantic.
	out := make(map[string]any)
	for k, v := range args {
		out[k] = v
	}
	for k, v := range facts {
		out[k] = v
	}
	return out
}

type approvalPinKey struct{}

// WithApprovalPin carries the pin an ApprovalPreparer returned for the
// approved call onto its execution context. A nil pin leaves ctx unchanged.
func WithApprovalPin(ctx context.Context, pin any) context.Context {
	if pin == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalPinKey{}, pin)
}

// ApprovalPinFrom returns the pin WithApprovalPin stored, or nil.
func ApprovalPinFrom(ctx context.Context) any {
	return ctx.Value(approvalPinKey{})
}
