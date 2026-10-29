// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ApprovalProvenance returns the calling turn's current approval origin: the
// group reference (nil unless the step answers exactly one group's request,
// #1449 D23, D28) and the origin label. The agent loop installs it per tool
// call; the value is read when a question is created and never changes after
// (C-I6, FR-050).
type ApprovalProvenance func() (*generated.RoomRef, string)

type approvalProvenanceKey struct{}

// WithApprovalProvenance carries fn on the tool context.
func WithApprovalProvenance(ctx context.Context, fn ApprovalProvenance) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalProvenanceKey{}, fn)
}

// ToolApprovalProvenance returns the calling turn's group reference and
// origin label, or nil and "" when none is carried.
func ToolApprovalProvenance(ctx context.Context) (*generated.RoomRef, string) {
	fn, _ := ctx.Value(approvalProvenanceKey{}).(ApprovalProvenance)
	if fn == nil {
		return nil, ""
	}
	return fn()
}
