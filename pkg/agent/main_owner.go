// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// noOperatorLogOnce keeps the "no operator configured" note to one Debug line
// per process: every connector message, peer request and room request
// resolves the owner, and repeating the same fact on each would be noise.
var noOperatorLogOnce sync.Once

// systemMainOwner is the owner recorded on a main that no person creates
// (connector intake, peer and room requests): the operator, i.e. the first
// configured account. With no account configured a main created now stays
// ownerless; that is noted once at Debug level.
func (al *AgentLoop) systemMainOwner() string {
	owner := al.GetConfig().OperatorUsername()
	if owner == "" {
		noOperatorLogOnce.Do(func() {
			logger.DebugCF("agent", "main session owner: no account is configured; mains created now stay ownerless", nil)
		})
	}
	return owner
}
