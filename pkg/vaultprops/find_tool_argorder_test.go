// Omnipus — knowledge_find refuses an unknown argument BEFORE it resolves scope.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestFindTool_UnknownArgumentRefusedBeforeScope is a REGRESSION GUARD ON
// ORDERING, and the ordering is the entire defect it exists for.
//
// knowledge_find has no `collection` argument, but knowledge_describe and
// knowledge_read both do, so an agent that has just used one of those tries it
// here. Before this guard, that attempt reached the scope gate first and came
// back with a refusal listing the collections in scope — which reads as "you
// named the wrong one" and invites another attempt at naming one. There is no
// argument to name one with, so every retry failed identically. A UAT agent
// made 24 such calls in one turn before the turn budget ran out.
//
// WHY A BARE CONTEXT PROVES THE ORDER. ResolveTurnScope on an empty context
// finds no workspace, so scope.Select("") CANNOT succeed here. If the argument
// check ran after the scope gate — the old order — this call would return the
// scope refusal. Getting the argument refusal instead is only possible if the
// check runs first. The test therefore fails on a revert without needing a
// vault, a workspace, or an index.
func TestFindTool_UnknownArgumentRefusedBeforeScope(t *testing.T) {
	res := NewFindTool(t.TempDir()).Execute(context.Background(), map[string]any{
		"collection": "kb",
		"type":       "company",
	})
	if res == nil {
		t.Fatal("Execute returned nil")
	}
	got := resultText(t, res)

	if !strings.Contains(got, "is not an argument") {
		t.Errorf("the refusal does not tell the caller the argument is unknown, so it cannot act on it.\ngot: %s", got)
	}
	if !strings.Contains(got, "collection") {
		t.Errorf("the refusal does not name the offending argument.\ngot: %s", got)
	}
	// The accepted set must be listed, or "not an argument" leaves the caller
	// guessing at what IS one — which is the same dead end in a new sentence.
	if !strings.Contains(got, "accepted:") || !strings.Contains(got, "words") {
		t.Errorf("the refusal does not list the accepted arguments.\ngot: %s", got)
	}
	// The load-bearing assertion. The scope refusal reaching the caller here
	// means the gate ran first and the old, unrecoverable behaviour is back.
	if strings.Contains(got, "no single knowledge base") {
		t.Fatalf("ORDERING REGRESSION: the scope gate ran before the argument check, so an agent passing an unknown argument is told to name a collection it has no argument to name.\ngot: %s", got)
	}
}

// TestFindTool_ValidArgumentsStillReachTheScopeGate is the other half, and
// without it the guard above is satisfiable by refusing everything.
//
// A call whose arguments are all legal must still be judged on scope. With a
// bare context there is no workspace, so the scope refusal is the correct
// answer — and it must now name the remedy rather than only the obstacle,
// because listing collections at a caller who cannot name one is what made the
// original refusal read as recoverable when it was not.
func TestFindTool_ValidArgumentsStillReachTheScopeGate(t *testing.T) {
	res := NewFindTool(t.TempDir()).Execute(context.Background(), map[string]any{
		"type": "company",
	})
	if res == nil {
		t.Fatal("Execute returned nil")
	}
	got := resultText(t, res)

	if strings.Contains(got, "is not an argument") {
		t.Fatalf("a call with only legal arguments was refused as if one were unknown — the check is over-broad and would refuse every real query.\ngot: %s", got)
	}
	if !strings.Contains(got, "no single knowledge base") {
		t.Fatalf("expected the scope refusal for a context with no workspace.\ngot: %s", got)
	}
	if !strings.Contains(got, "NO `collection` argument") {
		t.Errorf("the scope refusal does not say this tool has no collection argument, so it still reads as 'you named the wrong one'.\ngot: %s", got)
	}
}

// resultText returns the text the MODEL reads. ForLLM is deliberately the
// field asserted on: a refusal that is correct in some internal field but
// absent from what the model sees has not refused anything the model can act
// on, which is the entire subject of these tests.
func resultText(t *testing.T, res *tools.ToolResult) string {
	t.Helper()
	return res.ForLLM
}
