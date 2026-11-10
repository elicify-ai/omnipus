// Round-4 pinning cases follow the brief's G1-G4 exact filename examples.
// Expected reasons are independent literals or the specified unchanged-input
// invariant, never values obtained from the formatter.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectionFileNames_OverlappingReportedNamesStayDistinct(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, ".omnipus-vault", "records", "x.yaml")
	second := first + ".bak"
	names := newCollectionFileNames(root)
	reason := "duplicate names: " + first + " and " + second
	const want = "duplicate names: .omnipus-vault/records/x.yaml and .omnipus-vault/records/x.yaml.bak"
	// The shorter spelling is deliberately supplied first; both complete
	// filenames and their extensions must remain in the resulting reason.
	assert.Equal(t, want, names.text(reason, []string{first, second}),
		"overlapping reported names must retain both exact vault-relative filenames")
}

func TestCollectionFileNames_UnreportedNonASCIINameStaysWhole(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".omnipus-vault", "records", "café.yaml")
	names := newCollectionFileNames(root)
	const want = "open .omnipus-vault/records/café.yaml: not found"
	assert.Equal(t, want, names.text("open "+path+": not found", nil),
		"an unreported non-ASCII filename under the root must stay whole and vault-relative")
}

func TestCollectionFileNames_UnreportedInRootNameIsRelative(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".omnipus-vault", "records", "a.yaml")
	names := newCollectionFileNames(root)
	const want = "open .omnipus-vault/records/a.yaml: no such file"
	assert.Equal(t, want, names.text("open "+path+": no such file", nil),
		"an unreported filename under the root must use its exact vault-relative name")
}

func TestCollectionFileNames_EmbeddedRootSpellingIsUnchanged(t *testing.T) {
	root := t.TempDir()
	sep := string(filepath.Separator)
	names := newCollectionFileNames(root)
	cases := []struct {
		name   string
		reason string
	}{
		{"longer_word", "xx" + root + sep + "f.yaml"},
		{"other_path", sep + "other" + root + sep + "f.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// G4's oracle is input identity: this root spelling belongs to
			// a larger word or path, not a file beginning at a word start.
			assert.Equal(t, tc.reason, names.text(tc.reason, nil),
				"a root spelling inside a longer word or path must remain exactly unchanged")
		})
	}
}
