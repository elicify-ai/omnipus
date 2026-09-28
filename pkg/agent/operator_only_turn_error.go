// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// operator_only_turn_error.go is the ONE classification of turn errors that
// only an operator can clear. Two consumers render it, each in its own words:
//
//   - the Judge's dispatch (verifier_adjudication.go::judgeDispatchNeedsOperator,
//     UAT E-7): an adjudication refused this way is withheld at once instead of
//     retried on judgeRetryBackoff;
//   - a task run (task_run_loop.go::finishRunTurn, founder decision 2026-09-15):
//     a worker turn refused this way ends the task Failed at once, with the
//     reason and how to fix it — no task attempt used, no restart.
//
// Waiting or re-running clears none of these: the same request is refused
// the same way until someone changes a setting. Everything else — a rate
// limit, a network drop or 5xx, a provider that went silent, a timeout, an
// unclassified failure — is not in this set and keeps its caller's retry or
// restart behaviour.
//
// The set is read off TranslateTurnError's typed codes, never off error text.
// Each member is a code the contract attributes to an operator setting
// (contracts/components/schemas/LLMError.yaml x-user-messages: attribution
// `config`, or `user` for an expired sign-in) and isRetryable reports false for.
package agent

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// operatorFixCause names which operator-only refusal a turn error is. The zero
// value, operatorFixNone, means the error is not operator-only.
type operatorFixCause int

const (
	operatorFixNone operatorFixCause = iota
	// operatorFixCredentialsRejected: the provider answered 401/403
	// (CodeProviderAuthFailed) — the API key is wrong, revoked or lacks access.
	operatorFixCredentialsRejected
	// operatorFixSignInExpired: a device-code provider's stored sign-in could
	// not be refreshed (CodeNeedsProvider via providers.ErrProviderNeedsSignIn).
	operatorFixSignInExpired
	// operatorFixProviderNotConfigured: the agent's primary provider id is
	// unknown (CodeNeedsProvider via ErrAgentNeedsProvider).
	operatorFixProviderNotConfigured
	// operatorFixModelUnassigned: the agent has no model to call
	// (CodeModelUnassigned).
	operatorFixModelUnassigned
	// operatorFixContextWindowUnknown: a local endpoint reported no context
	// window and no override is set (CodeContextWindowUnknown).
	operatorFixContextWindowUnknown
	// operatorFixAgentNotOnWorkspace: the agent is on no workspace team, so the
	// turn has nowhere to run (CodeAgentNotConfigured).
	operatorFixAgentNotOnWorkspace
	// operatorFixWorkDirUnavailable: the agent's working folder could not be
	// opened — disk, permissions, or a home that could not be created
	// (CodeWorkspaceUnavailable).
	operatorFixWorkDirUnavailable
	// operatorFixQuotaBilling (C-5/D15): the account is out of credit
	// (CodeQuotaBilling) — waiting and retrying clear nothing; the
	// operator tops up the account or swaps providers.
	operatorFixQuotaBilling
	// operatorFixModelRetired (C-24/D15): the model has been withdrawn
	// (CodeModelRetired) — the operator picks a new model in the agent's
	// settings.
	operatorFixModelRetired
)

// classifyOperatorOnlyTurnError reports err's translated code and, when only an
// operator can clear it, which cause. A nil err, and every error outside the
// set, returns operatorFixNone.
//
// TranslateTurnError's precedence is inherited on purpose: a turn that was
// cancelled or timed out classifies as that typed exit first, so a Stop that
// races a refusal still ends the way a Stop does.
func classifyOperatorOnlyTurnError(err error) (LLMErrorCode, operatorFixCause) {
	if err == nil {
		return "", operatorFixNone
	}
	code := TranslateTurnError(err).Code
	switch code {
	case CodeProviderAuthFailed:
		return code, operatorFixCredentialsRejected
	case CodeNeedsProvider:
		// Two causes share this code (TranslateTurnError keeps both checks);
		// their fixes differ, so they are told apart by their sentinels.
		if errors.Is(err, providers.ErrProviderNeedsSignIn) {
			return code, operatorFixSignInExpired
		}
		return code, operatorFixProviderNotConfigured
	case CodeModelUnassigned:
		return code, operatorFixModelUnassigned
	case CodeContextWindowUnknown:
		return code, operatorFixContextWindowUnknown
	case CodeAgentNotConfigured:
		return code, operatorFixAgentNotOnWorkspace
	case CodeWorkspaceUnavailable:
		return code, operatorFixWorkDirUnavailable
	case CodeQuotaBilling:
		return code, operatorFixQuotaBilling
	case CodeModelRetired:
		return code, operatorFixModelRetired
	}
	return code, operatorFixNone
}
