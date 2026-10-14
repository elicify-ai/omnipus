// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// WithCurrentRevisionForTest returns a copy of args carrying the live
// expected_revision of the shared resource the write tool targets (FR-085).
// It lets tests written before the precondition existed exercise their own
// behaviour unchanged: the token equals the live one, so the precondition
// passes and the rest of the call is what the test observes. Tests OF the
// precondition pass expected_revision explicitly instead.
func WithCurrentRevisionForTest(tool tools.Tool, args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	var d *Deps
	var kind sharedResourceKind
	var key string
	switch tt := tool.(type) {
	case *ProviderConfigureTool:
		d, kind = tt.deps, sharedProvider
		key, _ = args["name"].(string)
	case *MCPAddTool:
		d, kind = tt.deps, sharedMCPServer
		key, _ = args["name"].(string)
	case *MCPRemoveTool:
		d, kind = tt.deps, sharedMCPServer
		key, _ = args["name"].(string)
	case *ChannelEnableTool:
		d, kind = tt.deps, sharedChannel
		key, _ = args["id"].(string)
	case *ChannelConfigureTool:
		d, kind = tt.deps, sharedChannel
		key, _ = args["id"].(string)
	case *ChannelDisableTool:
		d, kind = tt.deps, sharedChannel
		key, _ = args["id"].(string)
	default:
		panic("WithCurrentRevisionForTest: not a shared-resource write tool")
	}
	out[expectedRevisionParam] = sharedRevision(d.GetCfg(), kind, key)
	return out
}

// SetAgentIDGeneratorForTest makes create_agent use gen for new agent ids for
// the rest of the test, so a test that must plant a fault at the new agent's
// paths (or assert an id collision) knows the id in advance. Production ids
// are random UUIDs.
func SetAgentIDGeneratorForTest(t interface{ Cleanup(func()) }, gen func() string) {
	prev := newAgentID
	newAgentID = gen
	t.Cleanup(func() { newAgentID = prev })
}
