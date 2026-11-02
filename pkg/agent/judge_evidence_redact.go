// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_evidence_redact.go holds the ONE scrubber every channel of Judge
// evidence goes through (issue #721): the machine-check evidence record
// (judge.go's evidenceStore) and the session transcript window
// (verifier_adjudication.go's sessionWindowText, via tierOneRedactFn).

package agent

import (
	"regexp"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// judgeEvidenceRedactor returns the unconditional scrubber for text that
// reaches the Judge's prompt. Plain e-mail addresses are deliberately NOT
// scrubbed (founder decision Q14 B, 2026-10-30): a goal such as "email the
// order to supplier@x.com" must be verifiable from the tool's parameters and
// output. Four layers, in order:
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
//     but NOT e-mail addresses: audit.NewSecretShapeRedactor). This catches a
//     credential that was never registered because the agent only just read
//     it out of a file — layer 1 cannot see it by construction.
//  3. A URL userinfo that carries a password is replaced WHOLE, user part
//     included (judgeUserinfoLoginPattern): there the user is a login, and
//     with e-mail redaction gone an e-mail login such as
//     https://name@corp.example:pw@host would otherwise stay visible — and
//     its raw "@" defeats layer 4's userinfo pattern, leaking the password.
//  4. The audit logger's credential set (audit.RedactCredentials): the same
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
		s = judgeUserinfoLoginPattern.ReplaceAllString(s, "${1}[REDACTED]@")
		return audit.RedactCredentials(s)
	}
}

// judgeEvidencePatternRedactor is layer 2 of judgeEvidenceRedactor, built
// once. Its pattern set is hardcoded in pkg/audit, so a construction failure
// is a programming bug and panics at init.
//
//nolint:gochecknoglobals // immutable, process-wide; mirrors envcontext.defaultRedactor.
var judgeEvidencePatternRedactor = audit.NewSecretShapeRedactor()

// judgeUserinfoLoginPattern is layer 3 of judgeEvidenceRedactor: the whole
// userinfo of a URL that carries a password (scheme://USER:PASSWORD@host),
// replaced through the LAST "@" before the host. That is where Go's URL
// parser ends the userinfo too (net/url splits the authority at the final
// "@"), so a raw "@" inside the password ("postgres://app:p@ss9word@db") or
// one starting it is masked WHOLE instead of leaking its tail or defeating
// the match — security review 6b, 2026-10-30. The user part may contain a
// raw "@" (an e-mail login); the greedy userinfo match backtracks to that
// LAST "@" before the first "/", whitespace, quote, backslash, angle
// bracket, "?" or "#", so "https://host:8080/" (no "@" before the "/") and
// "https://user@host:8080/" (port, then "/") are left alone — a userinfo
// without a password is a bare login, not a secret. Quotes, backslashes,
// angle brackets, "?" and "#" end the match too: tool parameters are
// rendered as space-free JSON, and without that stop a URL with no path
// would run on into the next field and swallow a following
// "to":"supplier@x.com". Group 1 keeps "scheme://".
//
//nolint:gochecknoglobals // immutable, process-wide compiled pattern.
var judgeUserinfoLoginPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^\s/"'\\<>?#]*:[^\s/"'\\<>?#]+@`)
