// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "github.com/elicify-ai/omnipus/pkg/security"

// ExactActionTools lists the tools whose approval card covers one exact
// action only (Skills & Tools C-26 point 9, founder Q5 A): the card offers
// Approve once and Deny, never a timed allowance. The list itself lives in
// pkg/security, which keys their stored answers by exact arguments and
// cannot import this package.
func ExactActionTools() []string {
	return security.ExactActionToolNames()
}

// IsExactActionTool reports whether name is one of ExactActionTools.
func IsExactActionTool(name string) bool {
	return security.IsExactActionTool(name)
}
