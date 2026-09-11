// Omnipus — ADR-068 D3.2 / ADR-083 EMB-085 / ADR-083 review M8: RemoveProperty
// had zero tests before this file, and it deletes bytes from an operator's
// note. The oracle for every "want" below is either (a) the literal fixture
// text with the exact substring the spec says gets removed cut out by hand
// (never by running RemoveProperty and pasting its output), or (b) a manual
// trace of fmParse/fmFindKey against the exact fixture bytes, written out in
// the case's own comment so the derivation can be checked independently of
// the code under test.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestRemoveProperty' -p 1 ./pkg/knowledge/
package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRemoveProperty_Splices covers every documented behaviour of
// RemoveProperty (author.go): a scalar key removed, a multi-line/list value
// removed WHOLE, the two idempotent no-op cases (key absent; no frontmatter
// at all), the single-key-only fence-collapse edge case, non-interference
// with a body line that merely looks like a frontmatter key, and the
// key-shape refusal.
func TestRemoveProperty_Splices(t *testing.T) {
	type rpCase struct {
		name string
		src  string
		key  string
		// want is the exact expected output. Ignored when wantErr != nil.
		want string
		// wantErr is the sentinel the returned error must wrap. Nil means
		// RemoveProperty must succeed.
		wantErr error
	}

	cases := []rpCase{
		{
			// a1Frontmatter/a1Body are author_test.go's shared fixture
			// (package-level consts in this same package). "title" is a
			// single-line scalar whose next line ("aliases:") starts at
			// column zero, so fmFindKey's continuation scan stops
			// immediately — only the "title: ..." line itself is the
			// key's span.
			name: "simple scalar key removed; rest of frontmatter and body survive byte-for-byte",
			src:  a1Frontmatter + a1Body,
			key:  "title",
			// Independent oracle: cut exactly the one line the spec says
			// a scalar key occupies out of the fixture text by hand.
			want: strings.Replace(a1Frontmatter, "title: \"Old: notes\"\n", "", 1) + a1Body,
		},
		{
			// Highest-value row: "tags" is a block-sequence (list) value.
			// RemoveProperty's doc comment claims it removes "its key line
			// and every continuation line" — if that were false and only
			// the "tags:" line were cut, the two "- ..." lines below it
			// would be left as orphaned, syntactically-broken YAML
			// (a bare sequence item with no owning key). The oracle here
			// is the literal 3-line span the fixture's own layout says
			// belongs to "tags:" (a "tags:" line followed by two lines each
			// indented two spaces, i.e. each matching fmFindKey's
			// continuation predicate strings.HasPrefix(line, " ")),
			// carved out by hand — not computed by calling RemoveProperty.
			name: "multi-line list value removed whole, no orphan list item left behind",
			src:  a1Frontmatter + a1Body,
			key:  "tags",
			want: strings.Replace(a1Frontmatter,
				"tags:\n  - project/alpha\n  - \"weird: tag\"\n", "", 1) + a1Body,
		},
		{
			// Documented idempotent case: a key that was never there reads
			// back as "already cleared" (same state RemoveProperty defines
			// removal as reaching), so src must come back byte-identical.
			name: "key not present: src returned unchanged (idempotent)",
			src:  a1Frontmatter + a1Body,
			key:  "does-not-exist",
			want: a1Frontmatter + a1Body,
		},
		{
			// fmParse only recognises a frontmatter block when the file's
			// FIRST line is exactly "---"; this fixture's first line is a
			// heading, so block.present is false and RemoveProperty's own
			// "!block.present" branch returns src unchanged with no error.
			name: "note with no frontmatter block at all: src unchanged, no error",
			src:  "# Just a heading\n\nAnd a paragraph.\nstatus: fake\n",
			key:  "status",
			want: "# Just a heading\n\nAnd a paragraph.\nstatus: fake\n",
		},
		{
			// The key is the ONLY property in the block. Traced by hand
			// against fmParse/fmFindKey/RemoveProperty (not by running the
			// code): for src = "---\nonly: value\n---\n\nBody text\n",
			// fmParse sets innerStart = 4 (just past the opening "---\n")
			// and innerEnd = 16 (the offset where the second "---" line
			// begins, i.e. innerStart + len("only: value\n") = 4+12).
			// fmFindKey matches "only:" at innerStart; the very next line
			// IS the closing fence, so the continuation loop's
			// `consumed < block.innerEnd` guard is already false and no
			// continuation is consumed — the matched span is exactly
			// [innerStart, innerEnd), i.e. "only: value\n" alone.
			// RemoveProperty then returns src[:4] + src[16:], which is
			// "---\n" immediately followed by "---\n\nBody text\n": the two
			// fences end up ADJACENT, with no blank line inserted between
			// them and no property line surviving inside — an empty block,
			// not a deleted one.
			name: "key is the only key in the frontmatter: fences collapse adjacent, block left empty",
			src:  "---\nonly: value\n---\n\nBody text\n",
			key:  "only",
			want: "---\n---\n\nBody text\n",
		},
		{
			// A body line ("status: open") that is lexically identical to a
			// frontmatter "key: value" line must never be touched: fmFindKey
			// only ever scans between block.innerStart and block.innerEnd,
			// which ends at the closing "---" fence. Also proves
			// RemoveProperty is selective — the SAME key removed from the
			// frontmatter leaves an identically-spelled body line alone,
			// which a test that only used "key absent everywhere" could not
			// show.
			name: "a body line that merely looks like the key is not touched",
			src:  "---\nstatus: draft\ntitle: X\n---\n\n# Notes\n\nstatus: open\n",
			key:  "status",
			want: "---\ntitle: X\n---\n\n# Notes\n\nstatus: open\n",
		},
		{
			name:    "invalid property key (contains a colon) is refused",
			src:     a1Frontmatter + a1Body,
			key:     "bad: key",
			wantErr: ErrInvalidProperty,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srcBytes := []byte(tc.src)
			// Defensive: RemoveProperty must never mutate the slice it was
			// handed, success or failure. Keep an independent copy to
			// compare against after the call.
			srcCopy := append([]byte(nil), srcBytes...)

			out, err := RemoveProperty(tc.key)(srcBytes)

			assert.Equal(t, srcCopy, srcBytes, "RemoveProperty must not mutate its input slice")

			if tc.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
		})
	}
}

// TestRemoveProperty_RefusesRecordIdentityKeys is ADR-083 review M8's guard
// (authorRefuseRecordIdentityKey, author.go): removing a record's `id`
// silently succeeds at the YAML level and is catastrophic above it — a
// record with no `id` becomes permanently unreachable through every record
// door (findVaultRecordByID has nothing to match) AND invisible to the
// identity allocator's collision check, which then mints that same
// identifier to a second, unrelated record — breaking the "unique within its
// type" invariant the allocator's own comment claims to hold. Removing `type`
// is symmetric: a record with no `type` is not a record at all (D1).
//
// All three record-identity spellings (bare "id", the ADR-068 D8-namespaced
// "omni_id", and "type") must be refused before RemoveProperty ever touches
// the file's bytes.
func TestRemoveProperty_RefusesRecordIdentityKeys(t *testing.T) {
	src := []byte(a1Frontmatter + a1Body)
	srcCopy := append([]byte(nil), src...)

	for _, key := range []string{"id", "type", "omni_id"} {
		t.Run(key, func(t *testing.T) {
			out, err := RemoveProperty(key)(src)
			require.Error(t, err, "RemoveProperty(%q) must be refused", key)
			assert.ErrorIs(t, err, ErrInvalidProperty)
			assert.Nil(t, out, "a refused edit must not return partial output")
			assert.Equal(t, srcCopy, src, "a refused edit must not mutate the input slice")
		})
	}
}
