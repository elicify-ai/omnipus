// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_redact.go holds the ONE scrubber every channel of Judge
// evidence goes through (issue #721): the machine-check evidence record
// (judge.go's evidenceStore) and the session transcript window
// (verifier_adjudication.go's sessionWindowText, via tierOneRedactFn).

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// judgeEvidenceRedactor returns the unconditional scrubber for text that
// reaches the Judge's prompt. Three layers, in order:
//
//  1. The operator's REGISTERED credential values (config.
//     RegisterSensitiveValues at boot: credential-store values, provider
//     keys, channel tokens), applied through SensitiveDataReplacer DIRECTLY.
//     cfg.FilterSensitiveData is deliberately NOT used: it passes text
//     through unchanged when the operator-settable tools.filter_sensitive_data
//     flag is off (the zero value on any config that does not set it) and
//     when the text is shorter than tools.filter_min_length. A general
//     tool-output display toggle must not decide whether a credential is put
//     in front of the Judge.
//  2. The SEC-16 secret SHAPES (audit.NewRedactor's default set: sk-…,
//     key-…, Bearer …, ghp_/gho_…, xoxb-/xoxp-…, AKIA/ASIA…, JWTs, ya29.…,
//     e-mail addresses). This catches a credential that was never registered
//     because the agent only just read it out of a file — layer 1 cannot see
//     it by construction.
//  3. The audit logger's credential set (audit.RedactCredentials): the same
//     shapes anchored at a separator, a case-insensitive "bearer", and the
//     password in a URL's userinfo (scheme://user:PASSWORD@host), which
//     layer 2 does not cover.
//
// Every layer replaces rather than deletes, so a scrubbed span stays VISIBLE
// to the Judge as a marker. The built-in floor criterion "No secrets or
// credentials appear in the output" needs that: a silently deleted secret
// would read as a clean output.
//
// The config is resolved on every call, so a reload's newly registered
// values apply at once. A nil config or nil loop degrades to the pattern
// layers, never to a passthrough.
func (al *AgentLoop) judgeEvidenceRedactor() func(string) string {
	return func(s string) string {
		if al != nil {
			if cfg := al.GetConfig(); cfg != nil {
				s = cfg.SensitiveDataReplacer().Replace(s)
			}
		}
		s = judgeEvidencePatternRedactor.Redact(s)
		return audit.RedactCredentials(s)
	}
}

// judgeEvidencePatternRedactor is layer 2 of judgeEvidenceRedactor, built
// once. The pattern set is hardcoded in pkg/audit, so a construction error is
// a programming bug; it is logged loudly and the redactor falls back to
// disabled for this layer only — layers 1 and 3 still run.
//
//nolint:gochecknoglobals // immutable, process-wide; mirrors envcontext.defaultRedactor.
var judgeEvidencePatternRedactor = func() *audit.Redactor {
	r, err := audit.NewRedactor(nil)
	if err != nil {
		logger.ErrorCF("agent", "judge: could not build the evidence secret-pattern redactor; "+
			"Judge evidence keeps registered-value and audit credential redaction only",
			map[string]any{"error": err.Error()})
		return audit.DisabledRedactor()
	}
	return r
}()
