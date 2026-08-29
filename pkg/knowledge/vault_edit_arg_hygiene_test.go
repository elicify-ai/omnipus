// Omnipus — tests for vault_edit's argument-envelope hygiene (code review B
// finding 7): unknown arguments must be refused rather than silently
// dropped, and replace_body's 'body' argument must be required rather than
// defaulting to "" and deleting whatever the anchor/line_range matched.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVaultEditAppendSection_UnknownArgument_Refused is code review B
// finding 7's own reproduction: a caller who means to send append_section's
// 'body' but misspells the key ("bodyy") used to get a SUCCESSFUL reply
// ("APPEND_SECTION ... (appended)") with an EMPTY section silently created
// — the misspelled value was simply never read. It must now be refused
// before append_section ever runs, and the note must be untouched.
func TestVaultEditAppendSection_UnknownArgument_Refused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	orig := "---\nstatus: draft\n---\nBody.\n"
	a4Note(t, root, "Note.md", orig)
	v := a4Version(t, root, "Note.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "append_section", "path": "Note.md",
		"heading": "Notes", "bodyy": "this text should have gone in, but the key is misspelled",
		"expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("a misspelled argument must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "bodyy") {
		t.Fatalf("the refusal must name the unknown argument, got: %s", res.ForLLM)
	}
	if got := a4Read(t, root, "Note.md"); got != orig {
		t.Fatalf("a refused call must never create the section, got: %s", got)
	}
}

// TestVaultEditReplaceBody_MissingBody_Refused is code review B finding 7's
// second reproduction: replace_body with an anchor and NO 'body' argument
// used to match the anchor text and splice in an empty string — deleting
// it — while reporting "REPLACE_BODY (changed)", indistinguishable from an
// intentional replacement. It must now be refused, leaving the note
// byte-identical.
func TestVaultEditReplaceBody_MissingBody_Refused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	orig := "---\nstatus: draft\n---\n\nImportant paragraph that must survive.\n"
	a4Note(t, root, "Real.md", orig)
	v := a4Version(t, root, "Real.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "replace_body", "path": "Real.md",
		"anchor": "Important paragraph that must survive.", "expect_version": v,
	})
	if !res.IsError {
		t.Fatalf("replace_body with no 'body' argument must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "'body' is required") {
		t.Fatalf("the refusal must name the missing argument, got: %s", res.ForLLM)
	}
	if got := a4Read(t, root, "Real.md"); got != orig {
		t.Fatalf("a refused call must leave the note byte-identical, got: %s", got)
	}
}

// TestVaultEditReplaceBody_ExplicitEmptyBody_StillDeletes proves the
// presence check added for the finding above draws the line in the right
// place: an explicit body: "" is a caller who deliberately wants the
// matched text gone, and that must still work — only an ABSENT 'body' is
// refused.
func TestVaultEditReplaceBody_ExplicitEmptyBody_StillDeletes(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	a4Note(t, root, "Real.md", "---\nstatus: draft\n---\n\nDelete this paragraph.\n")
	v := a4Version(t, root, "Real.md")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "replace_body", "path": "Real.md",
		"anchor": "Delete this paragraph.", "body": "", "expect_version": v,
	})
	require.False(t, res.IsError, "an explicit empty body must be honoured, not refused: %s", res.ForLLM)
	if got := a4Read(t, root, "Real.md"); strings.Contains(got, "Delete this paragraph.") {
		t.Fatalf("an explicit empty body must delete the matched anchor text, got: %s", got)
	}
}

// TestVaultEditCreate_UnknownArgument_Refused proves the check is not
// scoped to a single op — a stray field on create is caught the same way.
func TestVaultEditCreate_UnknownArgument_Refused(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create", "path": "New.md",
		"titel": "Typo'd Title",
	})
	if !res.IsError {
		t.Fatalf("a misspelled argument on create must be refused, got success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "titel") {
		t.Fatalf("the refusal must name the unknown argument, got: %s", res.ForLLM)
	}
	if _, err := os.Stat(filepath.Join(root, "New.md")); err == nil {
		t.Fatalf("a refused create must never write the file")
	}
}
