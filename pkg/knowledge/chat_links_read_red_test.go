// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for knowledge link lines (BS-2.1, BS-2.3, BS-2.4, BS-1.7b, FR-011, FR-012).
package knowledge

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestKnowledgeRead_LinkLine(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)
	wsID := tools.ToolWorkspaceID(ctx)
	if wsID == "" {
		t.Fatal("fixture did not record a workspace id")
	}
	ctx = tools.WithToolContext(ctx, "webchat", "chat-1")

	res := NewReadTool(deps).Execute(ctx, map[string]any{"path": "Notes/Ordinary.md"})
	if res == nil || res.IsError {
		text := ""
		if res != nil {
			text = res.ForLLM
		}
		t.Fatalf("knowledge_read failed before the link line could be checked: %s", text)
	}
	// Mount name in readCtxAndDeps is "workbench" (scope fixture). The first
	// Library segment is that mount name (BS-2.3).
	want := "link: omnipus://file/" + wsID + "/workbench/Notes/Ordinary.md"
	if !strings.Contains(res.ForLLM, want) {
		t.Fatalf("knowledge_read result = %q, want it to contain %q (BS-2.1 / BS-2.3)", res.ForLLM, want)
	}

	off := tools.WithToolContext(ctx, "telegram", "chat-ext")
	offRes := NewReadTool(deps).Execute(off, map[string]any{"path": "Notes/Ordinary.md"})
	if offRes == nil || offRes.IsError {
		text := ""
		if offRes != nil {
			text = offRes.ForLLM
		}
		t.Fatalf("telegram knowledge_read failed: %s", text)
	}
	if strings.Contains(offRes.ForLLM, "link:") || strings.Contains(offRes.ForLLM, "omnipus://") {
		t.Fatalf("telegram knowledge_read leaked a link: %q (BS-1.7b)", offRes.ForLLM)
	}
}

func TestKnowledgeFindList_NoLink(t *testing.T) {
	root := t.TempDir()
	readFixtureVault(t, root)
	ctx, deps := readCtxAndDeps(t, root)
	ctx = tools.WithToolContext(ctx, "webchat", "chat-1")

	// The live retrieval tool is knowledge_search (the spec's knowledge_find name).
	// There is no NewSearchTool constructor; the package builds &SearchTool{deps}.
	found := (&SearchTool{deps: deps}).Execute(ctx, map[string]any{"query": "Ordinary"})
	listed := NewListTool(deps).Execute(ctx, map[string]any{})
	for _, res := range []*tools.ToolResult{found, listed} {
		if res == nil {
			t.Fatal("knowledge tool returned nil")
		}
		if strings.Contains(res.ForLLM, "link:") || strings.Contains(res.ForLLM, "omnipus://") {
			t.Fatalf("%s added a link line: %q (BS-2.4 / FR-012)", res.ForLLM[:min(40, len(res.ForLLM))], res.ForLLM)
		}
	}
}
