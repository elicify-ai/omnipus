// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import "testing"

// TestToolCallSignature_IgnoresDocumentationOnlyArgs pins the fix for the
// 2026-09-12 CI finding: bash's `description` is declared documentation-only,
// so two calls that differ ONLY in it are the same retry and must share a
// signature. DIES ON: removing "description" from nonSemanticToolArgs, or
// hashing the raw args map again.
func TestToolCallSignature_IgnoresDocumentationOnlyArgs(t *testing.T) {
	a := toolCallSignature("bash", map[string]any{
		"command": `git commit -m "evidence"`, "description": "Commit staged evidence (third attempt)",
	})
	b := toolCallSignature("bash", map[string]any{
		"command": `git commit -m "evidence"`, "description": "Commit staged evidence (one hundred eighty-first attempt)",
	})
	c := toolCallSignature("bash", map[string]any{
		"command": `git commit -m "evidence"`,
	})
	if a != b || a != c {
		t.Fatalf("bash calls differing only in the documentation-only description must share a signature:\n a=%s\n b=%s\n c=%s", a, b, c)
	}

	// The command itself still distinguishes calls — a genuinely different
	// retry must NOT inherit the streak.
	d := toolCallSignature("bash", map[string]any{
		"command": `git -c user.name=Jim commit -m "evidence"`, "description": "Commit staged evidence (third attempt)",
	})
	if d == a {
		t.Fatalf("different command must yield a different signature; got %s for both", a)
	}

	// The exclusion is per tool: for a tool that has NOT declared
	// `description` cosmetic, it stays part of the identity.
	e := toolCallSignature("other_tool", map[string]any{"description": "x", "arg": 1})
	f := toolCallSignature("other_tool", map[string]any{"description": "y", "arg": 1})
	if e == f {
		t.Fatalf("description must remain semantic for tools that do not declare it documentation-only")
	}
}
