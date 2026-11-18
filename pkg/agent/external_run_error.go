// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// external_run_error.go — #1857 (notifications spec §13.8): how the external
// CLI drain handles one runner error event. Split out of drainExternalRun so
// the classification, the C11 nonfatal rule and the fatal run error live in
// one place.
package agent

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
)

// renderForRun fills the sanitized sentence's slots with the facts the
// dispatch site knows — the CLI's display name and the model Omnipus passed —
// and computes the technical-details text (cli display name · exit status ·
// kind). A slot with no fact falls back to the generic catalogue sentence
// (renderMessageSlots), never a literal slot.
func (s SanitizedRunnerError) renderForRun(cli, model string) SanitizedRunnerError {
	cliName := CLIDisplayName(cli)
	s.AssistantText = renderMessageSlots(s.AssistantText, messageSlotFacts{
		cli:   cliName,
		model: model,
		n:     s.failure.maxTurns,
	})
	s.technicalDetail = cliTechnicalDetail(cliName, s.failure)
	return s
}

// handleExternalRunError classifies one runner error event and returns the
// fatal run error, or nil for a nonfatal diagnostic.
//
// C11 (§13.8.5): a nonfatal diagnostic (Fatal:false) is neither emitted as a
// user-facing error line nor persisted — a run that then succeeds shows no
// failure at all. Its classified kind and raw text go to gateway.log only.
//
// A fatal error is sanitized (raw text to gateway.log only — E1 B / E3 A),
// emitted on the wire, persisted, and returned as an externalCLIRunError that
// carries the typed code and rendered sentence to every surface.
func handleExternalRunError(
	al *AgentLoop,
	childTS *turnState,
	runID, cli string,
	runnerErr *runner.ErrorEvent,
) error {
	sanitized := sanitizeRunnerErrorTyped(runnerErr.Message, runnerErr.Type)
	if !runnerErr.Fatal {
		slog.Warn("external-cli dispatch: nonfatal runner diagnostic",
			"run_id", runID, "cli", cli,
			"code", string(sanitized.Code),
			"variant", sanitized.failure.variant,
			"raw_log_text", sanitized.LogText)
		return nil
	}
	model := ""
	if childTS != nil && childTS.agent != nil {
		model = strings.TrimSpace(childTS.agent.Model)
	}
	sanitized = sanitized.renderForRun(cli, model)
	if childTS != nil {
		childTS.appendIntermediateAssistantTranscript(
			"[external-cli error] "+sanitized.AssistantText,
			transcriptModelFor(childTS.agent),
		)
	}
	// Surface the EventKindError for the WS forwarder — the structured
	// frame carries the code, the rendered sentence and the safe technical
	// details; the raw text stays in LogText / gateway.log.
	emitExternalCLIErrorEvent(al, childTS, runnerErr, sanitized)
	// The raw text is retained ONLY in this structured log line for
	// operator triage via gateway.log (ADR-051 B2).
	slog.Warn("external-cli dispatch: fatal runner error",
		"run_id", runID, "cli", cli,
		"assistant_text", sanitized.AssistantText,
		"raw_log_text", sanitized.LogText,
		"code", string(sanitized.Code),
		"variant", sanitized.failure.variant,
		"retryable", sanitized.Retryable)
	return &externalCLIRunError{code: sanitized.Code, text: sanitized.AssistantText}
}
