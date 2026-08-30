// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// vault_edit_schema_test.go — coverage for knowledgeEditResolveSchema's own
// contract: an unparsable frontmatter block resolves to "no schema applies"
// (ok == false) rather than propagating the parse error, and the composed
// NoteEdit built on top of it (knowledgeEditSetPropertyEdit) still refuses the
// whole write with ErrFrontmatterUnterminated — from the splice function it
// calls immediately afterward, not from schema validation itself. Written
// while restructuring vault_edit_schema.go to resolve a nilerr finding: the
// finding was a false positive (nothing is silently swallowed end-to-end),
// and this is the regression test that makes the "nothing is swallowed"
// half of that claim checked rather than merely argued in a comment.

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeEditResolveSchema_UnparsableFrontmatterIsNotApplicable is the
// direct white-box case for the function's overall contract: unparsable
// frontmatter resolves to ok == false, schema == nil, typeName == "".
//
// Mutation note: deleting the `ferr != nil` branch inside
// knowledgeEditResolveSchema does NOT fail this test (or any other in this
// file) — it was tried, in a detached worktree, and survived. Under
// records.ParseFrontmatter's current implementation every error return
// already carries an empty Values map, so rec.TypeName() == "" regardless
// of whether the ferr branch runs, and the very next check catches it
// either way. See that function's own doc comment for why the branch is
// kept anyway. This test asserts the OBSERVABLE contract, which is real and
// worth pinning, even though it cannot by itself distinguish which of the
// two checks inside the function produced ok == false.
func TestKnowledgeEditResolveSchema_UnparsableFrontmatterIsNotApplicable(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	schema, typeName, ok := knowledgeEditResolveSchema(set, unparsable, "title")
	assert.False(t, ok, "unparsable frontmatter must resolve to no schema applies")
	assert.Nil(t, schema)
	assert.Equal(t, "", typeName)
}

// TestKnowledgeEditSetPropertyEdit_UnparsableFrontmatterStillRefusesTheWrite is
// the end-to-end proof that knowledgeEditValidateValue's swallowed parse error
// does not become a silently-accepted write: the very next call in the same
// NoteEdit closure (SetPropertyScalarChecked -> SetProperty -> fmParse)
// re-parses the same bytes and refuses with ErrFrontmatterUnterminated. If
// this test is ever the ONLY thing standing between the two questions
// ("does a schema apply" and "can this be spliced at all") disagreeing, it
// must fail loudly rather than let an unparsable note be treated as
// accepted.
func TestKnowledgeEditSetPropertyEdit_UnparsableFrontmatterStillRefusesTheWrite(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	edit := knowledgeEditSetPropertyEdit(set, "title", []string{"New"}, false)
	_, err := edit(unparsable)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFrontmatterUnterminated,
		"the write must still be refused, even though schema validation itself found nothing to check")
}

// TestKnowledgeEditPropertyDeclared_UnparsableFrontmatterIsNotDeclared covers
// the sibling caller of knowledgeEditResolveSchema (the link operation's arity
// lookup), which must agree with knowledgeEditValidateValue on this case.
func TestKnowledgeEditPropertyDeclared_UnparsableFrontmatterIsNotDeclared(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	declared, many := knowledgeEditPropertyDeclared(set, unparsable, "title")
	assert.False(t, declared)
	assert.False(t, many)
}
