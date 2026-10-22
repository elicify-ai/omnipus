package agent

import "github.com/elicify-ai/omnipus/pkg/bus"

func (al *AgentLoop) roomWakeCanRestart(msg bus.InboundMessage) bool {
	_, allowed := al.roomRestartPrincipal(msg)
	return allowed
}
