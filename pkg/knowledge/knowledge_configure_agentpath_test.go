// Omnipus — reproduction & regression for A1: create_view rejected on the
// LIVE agent tool-call path (ToolRegistry.ExecuteWithContext) though it is a
// valid op.
//
// # What actually broke (from the live transcript, not a guess)
//
// z-ai/glm-5.3-flash (via OpenRouter) emitted a well-formed tool-call
// arguments JSON whose `op` value had the model's own tool-call TEMPLATE
// leaked into it, swallowing the `type` field:
//
//	"op": "create_view</arg_value><5b656597><arg_key><2b53f23f>type</arg_key><ac7a3bd7><arg_value><b88a6f17>note"
//
// (intended: op="create_view", type="note"). The JSON was valid, so every
// layer accepted op verbatim and the enum validator rejected the garbage:
// "property \"op\": value create_view</arg_value>... is not in enum". The
// direct-Execute tests never see this because they hand-build a clean args
// map — they are the wrong instrument. This file drives the SAME registry
// entrypoint the agent loop uses (pkg/agent/loop.go's
// ts.agent.Tools.ExecuteWithContext), feeding it the exact corrupted map the
// model produced.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeConfigure_AgentPath' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestKnowledgeConfigure_AgentPath_LeakedTemplate_Recovered is the A1
// regression: the exact corrupted arguments the live model emitted must be
// repaired into the intended call and succeed, instead of dying in the
// argument validator. Before the argrepair fix this FAILS with
// "...is not in enum".
func TestKnowledgeConfigure_AgentPath_LeakedTemplate_Recovered(t *testing.T) {
	tool, ws, root := cvFixture(t)

	// The captured blob names record type "note"; declare it so the recovered
	// call can succeed end-to-end (the fixture ships only "invoice").
	noteRes := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "note",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"name":   map[string]any{"type": "text"},
				"status": map[string]any{"type": "enum", "values": []any{"draft", "active"}},
			},
		},
	})
	require.False(t, noteRes.IsError, "note record type must be created: %s", noteRes.ForLLM)

	reg := tools.NewToolRegistry()
	reg.Register(tool)
	ctx := a4Ctx("mia", ws)

	// Verbatim from the captured transcript
	// (session_01M1TRGXTHQFX7KYYF3XC38H63, call_73a6c057f19946aeaa724aa4):
	// op carries the leaked template and `type` is absent.
	leakedArgs := map[string]any{
		"collection": "kb",
		"op":         "create_view</arg_value><5b656597><arg_key><2b53f23f>type</arg_key><ac7a3bd7><arg_value><b88a6f17>note",
		"view":       "jim-tooltest--recent",
		"kind":       "table",
		"columns":    []any{"name", "status"},
	}

	res := reg.ExecuteWithContext(ctx, "knowledge_configure", leakedArgs, "", "", nil)
	require.NotNil(t, res)

	require.NotContains(t, res.ForLLM, "is not in enum",
		"leaked op value must be repaired to create_view, not rejected by the validator: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "invalid arguments for tool",
		"the repaired call must pass argument validation: %s", res.ForLLM)
	require.False(t, res.IsError,
		"create_view (kind=table) should succeed end-to-end after repair: %s", res.ForLLM)

	// Proof it reached Execute with the recovered op AND the recovered
	// type=note: the view file exists and queries the note record type.
	raw := a4Read(t, root, ".omnipus-vault/views/jim-tooltest--recent.yaml")
	require.Contains(t, raw, "note", "recovered type=note must drive the view's record type: %s", raw)
}

// TestKnowledgeConfigure_AgentPath_CleanCreateViewValidates guards the tool
// SCHEMA on the registry path: a clean create_view call must pass argument
// validation (op enum contains create_view; `type` carries no enum). This
// would catch a regression that removed create_view from vaultConfigureOps or
// added an enum to `type`.
func TestKnowledgeConfigure_AgentPath_CleanCreateViewValidates(t *testing.T) {
	tool, ws, root := cvFixture(t)

	reg := tools.NewToolRegistry()
	reg.Register(tool)
	ctx := a4Ctx("mia", ws)

	res := reg.ExecuteWithContext(ctx, "knowledge_configure", map[string]any{
		"collection": "kb", "op": "create_view", "view": "agentpath-board",
		"kind": "board", "type": "invoice", "choice": "status",
	}, "", "", nil)

	require.NotNil(t, res)
	require.NotContains(t, res.ForLLM, "is not in enum", res.ForLLM)
	require.False(t, res.IsError, "clean create_view should succeed end-to-end: %s", res.ForLLM)
	require.Contains(t, a4Read(t, root, ".omnipus-vault/views/agentpath-board.yaml"), "choice: status")
}

// TestKnowledgeConfigure_AgentPath_EveryOpValidates guards every op in
// vaultConfigureOps against the same class of validator defect: no op value
// may be rejected by the enum validator on the agent path.
func TestKnowledgeConfigure_AgentPath_EveryOpValidates(t *testing.T) {
	tool, ws, _ := cvFixture(t)

	reg := tools.NewToolRegistry()
	reg.Register(tool)
	ctx := a4Ctx("mia", ws)

	for _, op := range vaultConfigureOps {
		t.Run(op, func(t *testing.T) {
			res := reg.ExecuteWithContext(ctx, "knowledge_configure", map[string]any{
				"collection": "kb", "op": op, "type": "invoice",
			}, "", "", nil)
			require.NotNil(t, res)
			if strings.Contains(res.ForLLM, "is not in enum") {
				t.Fatalf("op %q rejected by enum validator on agent path: %s", op, res.ForLLM)
			}
		})
	}
}
