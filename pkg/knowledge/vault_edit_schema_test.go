// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// vault_edit_schema_test.go — coverage for vaultEditResolveSchema's own
// contract: an unparsable frontmatter block resolves to "no schema applies"
// (ok == false) rather than propagating the parse error, and the composed
// NoteEdit built on top of it (vaultEditSetPropertyEdit) still refuses the
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

// TestVaultEditResolveSchema_UnparsableFrontmatterIsNotApplicable is the
// direct white-box case: no end-to-end fixture distinguishes "frontmatter
// failed to parse" from "note declares no type" at this function's own
// boundary — both must return ok == false, and only a call directly on the
// function proves which branch was actually taken.
func TestVaultEditResolveSchema_UnparsableFrontmatterIsNotApplicable(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	schema, typeName, ok := vaultEditResolveSchema(set, unparsable, "title")
	assert.False(t, ok, "unparsable frontmatter must resolve to no schema applies")
	assert.Nil(t, schema)
	assert.Equal(t, "", typeName)
}

// TestVaultEditSetPropertyEdit_UnparsableFrontmatterStillRefusesTheWrite is
// the end-to-end proof that vaultEditValidateValue's swallowed parse error
// does not become a silently-accepted write: the very next call in the same
// NoteEdit closure (SetPropertyScalarChecked -> SetProperty -> fmParse)
// re-parses the same bytes and refuses with ErrFrontmatterUnterminated. If
// this test is ever the ONLY thing standing between the two questions
// ("does a schema apply" and "can this be spliced at all") disagreeing, it
// must fail loudly rather than let an unparsable note be treated as
// accepted.
func TestVaultEditSetPropertyEdit_UnparsableFrontmatterStillRefusesTheWrite(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	edit := vaultEditSetPropertyEdit(set, "title", []string{"New"}, false)
	_, err := edit(unparsable)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrFrontmatterUnterminated,
		"the write must still be refused, even though schema validation itself found nothing to check")
}

// TestVaultEditPropertyDeclared_UnparsableFrontmatterIsNotDeclared covers
// the sibling caller of vaultEditResolveSchema (the link operation's arity
// lookup), which must agree with vaultEditValidateValue on this case.
func TestVaultEditPropertyDeclared_UnparsableFrontmatterIsNotDeclared(t *testing.T) {
	set := records.NewSchemaSet()
	unparsable := []byte("---\ntitle: Old\n\nbody with no closing fence\n")

	declared, many := vaultEditPropertyDeclared(set, unparsable, "title")
	assert.False(t, declared)
	assert.False(t, many)
}
