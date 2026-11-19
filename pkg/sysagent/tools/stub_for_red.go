// stub_for_red.go — compile-only RED stubs for epic #1094 slice S1a tasks T-3
// and T-4 in this package.
//
// DELETE when GREEN lands. The T-4 symbols are implemented in
// workspace_membership.go; what remains are the T-3 symbols, which return
// errors.New("not implemented") or a zero value until T-3 lands.
package systools

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ApplySharedConfigArgs is applySharedConfigArgs exported unchanged (T-3):
// import stores voice, memory_enabled and context_window_override with the
// create_agent tool's rule.
func ApplySharedConfigArgs(a *config.AgentConfig, args map[string]any) error {
	return errors.New("not implemented")
}

// AgentCreateArgNames returns a sorted copy of agentCreateArgNames (sign-off
// C-8), for TestImportFieldCoverage.
func AgentCreateArgNames() []string { return nil } // RED stub: not implemented

// AgentUpdateArgNames returns a sorted copy of agentUpdateArgNames (C-8).
func AgentUpdateArgNames() []string { return nil } // RED stub: not implemented
