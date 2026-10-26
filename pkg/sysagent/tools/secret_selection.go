// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// secretSelectionAudit receives the normal registry-injected logger, including
// after reload. Shared tools may be registered on several agents concurrently.
type secretSelectionAudit struct {
	logger atomic.Pointer[audit.Logger]
}

func (a *secretSelectionAudit) SetAuditLogger(logger *audit.Logger) {
	a.logger.Store(logger)
}

func (a *secretSelectionAudit) selected(ctx context.Context, tool, resource string, refs []string) {
	if len(refs) == 0 {
		return
	}
	audit.Emit(ctx, a.logger.Load(), "secret.request.selected", audit.SeverityInfo, map[string]any{
		"tool": tool, "resource": resource, "reference_names": append([]string(nil), refs...),
		"agent_id": tools.ToolAgentID(ctx), "session_id": tools.ToolTranscriptSessionID(ctx),
	})
}

// referenceSelectionError distinguishes a no-write recipient refusal from a
// failed config publication, without exposing credential-store error contents.
type referenceSelectionError struct{ err error }

func (e *referenceSelectionError) Error() string { return e.err.Error() }
func (e *referenceSelectionError) Unwrap() error { return e.err }

func checkSecretReference(store *credentials.Store, ref string, recipient credentials.CardRecipient) error {
	if err := secretrequest.ValidateReference(store, ref, recipient); err != nil {
		return &referenceSelectionError{err: err}
	}
	return nil
}

func referenceSelectionRefusal(err error) (*tools.ToolResult, bool) {
	var refusal *referenceSelectionError
	if !errors.As(err, &refusal) {
		return nil, false
	}
	return tools.ErrorResult(errorJSON("INVALID_REFERENCE", refusal.Error(), "Use request_secret for the current recipient")), true
}

func providerReferenceRecipient(row *config.ModelConfig) (credentials.CardRecipient, error) {
	ep, err := effectiveProviderEndpoint(row)
	if err != nil {
		return credentials.CardRecipient{}, &referenceSelectionError{err: errors.New("provider recipient could not be resolved; nothing was selected")}
	}
	endpoint, err := secretrequest.NormalizeEndpoint(ep.API)
	if err != nil {
		return credentials.CardRecipient{}, &referenceSelectionError{err: errors.New("provider recipient is not a safe endpoint; nothing was selected")}
	}
	return credentials.CardRecipient{Kind: "provider_key", Provider: providerNameOf(row), Endpoint: endpoint,
		Protocol: string(ep.Protocol), Field: "api_key"}, nil
}
