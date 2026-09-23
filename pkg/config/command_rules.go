// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// MaxCommandRules bounds sandbox.command_rules. Mirrors the maxItems on
// contracts/components/schemas/SandboxConfigUpdate.yaml's command_rules so a
// hand-edited config.json cannot carry more rules than the REST write path
// would accept.
const MaxCommandRules = 1000

// maxCommandRuleFieldLen bounds binary and arg_prefix, matching the
// CommandRule schema's maxLength.
const maxCommandRuleFieldLen = 4096

// commandRuleBinaryForbidden lists characters a rule's binary may not
// contain: whitespace and the shell metacharacters that would make the value
// something other than one program name or path. A rule is matched against
// the RESOLVED executable (ADR-092 FR-040), never against command text, so a
// value holding any of these could never match anything and is an operator
// mistake worth rejecting loudly rather than a rule that silently never fires.
const commandRuleBinaryForbidden = " \t\r\n;|&$<>()`'\"*?[]{}!#~\\"

// ValidateCommandRules checks an ADR-092 D3 operator rule list. It is the one
// validator for both writers of sandbox.command_rules: config load
// (validateBootConfig) and the sandbox-config PUT handler. Every rule must
// carry a valid action (allow/ask/deny), a binary that is a bare command name
// or an absolute path with no whitespace or shell metacharacters, and an
// optional arg_prefix with no control characters. An invalid rule rejects the
// whole list: a typo such as "Deny" must fail loudly, never load as a rule the
// matcher silently ignores.
func ValidateCommandRules(rules []shellrule.Rule) error {
	if len(rules) > MaxCommandRules {
		return fmt.Errorf("command_rules: %d rules exceeds the maximum of %d", len(rules), MaxCommandRules)
	}
	for i, r := range rules {
		if err := validateCommandRule(r); err != nil {
			return fmt.Errorf("command_rules[%d]: %w", i, err)
		}
	}
	return nil
}

func validateCommandRule(r shellrule.Rule) error {
	if !r.Action.Valid() {
		return fmt.Errorf("action %q must be one of allow, ask, deny", string(r.Action))
	}
	if r.Binary == "" {
		return fmt.Errorf("binary is required")
	}
	if len(r.Binary) > maxCommandRuleFieldLen {
		return fmt.Errorf("binary is longer than %d characters", maxCommandRuleFieldLen)
	}
	if strings.ContainsAny(r.Binary, commandRuleBinaryForbidden) {
		return fmt.Errorf("binary %q must be one command name or absolute path, "+
			"without spaces or shell metacharacters", r.Binary)
	}
	if strings.ContainsRune(r.Binary, '/') && !filepath.IsAbs(r.Binary) {
		return fmt.Errorf("binary %q must be a bare command name or an absolute path, not a relative path", r.Binary)
	}
	if len(r.ArgPrefix) > maxCommandRuleFieldLen {
		return fmt.Errorf("arg_prefix is longer than %d characters", maxCommandRuleFieldLen)
	}
	for _, c := range r.ArgPrefix {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("arg_prefix must not contain control characters")
		}
	}
	return nil
}
