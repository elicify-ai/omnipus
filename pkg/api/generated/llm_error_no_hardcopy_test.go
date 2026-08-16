// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package generated_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestLLMErrorMessages_NoHandCopiesOutsideTheCatalogue is a trip-wire against a
// trap this repo fell into three separate times in one pass.
//
// The user-facing copy for an LLMError code is contract data, generated into
// exactly two artifacts (the Go catalogue beside this file, and its TypeScript
// twin). Anywhere else, the same sentence written out by hand is a copy that
// nothing keeps in step: it does not fail to compile, it does not fail review,
// and it goes stale silently the moment the copy is reworded. Every instance
// found so far was discovered by a test failing for a reason unrelated to what
// it was written to check:
//
//   - pkg/api/generated/fixtures.go pinned the rate_limited copy, and had gone
//     stale carrying the retired "From the model:" prefix.
//   - pkg/gateway/rest_executor_smoketest_test.go pinned the unknown copy, and
//     turned a message rewrite into a red CI gate that read as a code
//     regression.
//   - The two catalogues themselves were hand-maintained mirrors of each
//     other, with nothing asserting they matched. That is what generating them
//     from the contract fixed.
//
// A test asserting the message is CORRECT must read it from the catalogue
// (agent.UserMessageForCode, or gen.LLMErrorUserMessages). A test asserting a
// message is ABSENT — no support desk, no retired prefix — is fine and is not
// what this catches, because it embeds no catalogue sentence.
func TestLLMErrorMessages_NoHandCopiesOutsideTheCatalogue(t *testing.T) {
	root := repoRootFromHere(t)

	// The two generated artifacts are the legitimate homes for this text.
	allowed := map[string]bool{
		filepath.Join("pkg", "api", "generated", "llm_error_messages.gen.go"):    true,
		filepath.Join("src", "lib", "api", "generated", "llm-error-messages.ts"): true,
	}

	// A short prefix is enough to identify a hand-copy and keeps the scan cheap.
	// Full sentences would also match, but a truncated paste would slip through.
	needles := make(map[string]string, len(gen.LLMErrorUserMessages))
	for code, msg := range gen.LLMErrorUserMessages {
		if n := firstNWords(msg, 6); n != "" {
			needles[code] = n
		}
	}

	for _, dir := range []string{"pkg", "src", "cmd", "internal"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				if info != nil && info.IsDir() && (info.Name() == "node_modules" || info.Name() == "spa") {
					return filepath.SkipDir
				}
				return nil
			}
			switch filepath.Ext(path) {
			case ".go", ".ts", ".tsx":
			default:
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil || allowed[rel] {
				return nil
			}
			// This test file names none of the messages itself, but skip it
			// anyway so a future edit that quotes one cannot self-trigger.
			if strings.HasSuffix(rel, "llm_error_no_hardcopy_test.go") {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			text := string(body)
			for code, needle := range needles {
				if strings.Contains(text, needle) {
					t.Errorf("%s hand-copies the %q message (%q…).\n"+
						"Read it from the catalogue instead — agent.UserMessageForCode(agent.Code…) in Go, "+
						"or codeToDisplay in TypeScript. A pasted literal goes stale silently when the copy "+
						"is reworded, and has already turned a wording change into a red gate that read as a "+
						"code regression.", rel, code, needle)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
}

// firstNWords returns the first n whitespace-separated words of s, or "" if s
// has fewer than n. Six words is long enough that an ordinary sentence in an
// unrelated comment will not collide, and short enough that a truncated paste
// of a message still matches.
func firstNWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) < n {
		return ""
	}
	return strings.Join(fields[:n], " ")
}

// repoRootFromHere walks up from the package directory until it finds go.mod.
func repoRootFromHere(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate repo root (no go.mod found walking up)")
	return ""
}
