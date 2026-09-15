// Tests for ADR-083 review C7 — the record-CREATE identity allocator.
//
// WHY THIS FILE EXISTS. The create door's happy path is covered
// (TestKnowledgeRecordCreate_MintsAndReturns201), but everything BENEATH it —
// the persisted `.seq` counter, the prefix-less identity shape, and the
// refusal that stops a corrupted counter from handing out an identifier twice
// — had zero assertions. `grep` for `nextSequenceValue|writeSequenceValue|
// identityFor|allocateRecordID|liveRecordIDs|\.seq` across every `*_test.go`
// in pkg/gateway returned nothing at all.
//
// The sharpest of those is the corrupted counter. nextSequenceValue's own
// comment says a silent reset "can silently reuse an identifier already given
// out", which is the D7 uniqueness invariant failing SILENTLY — two notes
// carrying WD-0007, no error anywhere, and the duplicate only discovered when
// a relation resolves to the wrong record. Nothing proved it refuses.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// widgetSeqPath is the on-disk counter the allocator persists for the fixture
// vault's `widget` type. Built from the same exported constants the
// implementation uses, so a rename of either moves the test with it rather
// than leaving it asserting against a path that no longer exists.
func widgetSeqPath(vault string) string {
	return filepath.Join(vault, records.VaultMarkerDirName, records.RecordsDirName, "widget.seq")
}

// createWidget posts one create for the fixture's widget type.
func createWidget(t *testing.T, api *restAPI, ws, path, name string) *httptest.ResponseRecorder {
	t.Helper()
	return knowledgePost(t, api, "/api/v1/library/"+ws+"/knowledge/records", map[string]any{
		"mode": "create",
		"type": "widget",
		"path": path,
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": name}}},
		},
	})
}

// ---------------------------------------------------------------------------
// nextSequenceValue — the counter read
// ---------------------------------------------------------------------------

// TestNextSequenceValue_AbsentOrEmptyCounterStartsAtOne pins the two states a
// brand-new record type is legitimately in. Both must yield 1, because the
// FIRST identifier D7 describes is <prefix>-0001, never <prefix>-0000.
func TestNextSequenceValue_AbsentOrEmptyCounterStartsAtOne(t *testing.T) {
	dir := t.TempDir()

	t.Run("a counter file that does not exist yet", func(t *testing.T) {
		n, err := nextSequenceValue(filepath.Join(dir, "never-written.seq"))
		require.NoError(t, err, "a type nobody has minted for is the ordinary first-create case, not a fault")
		assert.Equal(t, int64(1), n, "the first identifier of a type is -0001")
	})

	t.Run("a counter file that exists but is empty", func(t *testing.T) {
		p := filepath.Join(dir, "empty.seq")
		require.NoError(t, os.WriteFile(p, nil, 0o600))
		n, err := nextSequenceValue(p)
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
	})

	t.Run("a counter file holding only whitespace", func(t *testing.T) {
		p := filepath.Join(dir, "blank.seq")
		require.NoError(t, os.WriteFile(p, []byte("  \n\t\n"), 0o600))
		n, err := nextSequenceValue(p)
		require.NoError(t, err, "TrimSpace reduces this to the empty case")
		assert.Equal(t, int64(1), n)
	})

	// POSITIVE CONTROL. Without it every assertion above passes on an
	// implementation that returns a hardcoded 1 and never reads the file.
	t.Run("a counter holding a real value returns the NEXT one", func(t *testing.T) {
		p := filepath.Join(dir, "live.seq")
		require.NoError(t, os.WriteFile(p, []byte("41\n"), 0o600))
		n, err := nextSequenceValue(p)
		require.NoError(t, err)
		assert.Equal(t, int64(42), n, "the stored value is the LAST minted, so the next to try is +1")
	})
}

// TestNextSequenceValue_CorruptedCounterIsRefusedNeverSilentlyReset is the
// review's sharpest C7 case, and the reason this file leads with it.
//
// A counter that does not parse must be an ERROR. The tempting alternative —
// treat unreadable as zero and carry on — re-mints identifiers that were
// already handed out, and does it silently: the create succeeds, the note
// looks fine, and two records now share an id. The refusal is what converts
// an invisible data-integrity loss into a loud 500 an operator can act on.
//
// DIES ON: replacing the `perr != nil || n < 0` arm with `return 1, nil` or
// `return 0, nil` — every subtest below then reports NoError.
func TestNextSequenceValue_CorruptedCounterIsRefusedNeverSilentlyReset(t *testing.T) {
	dir := t.TempDir()

	corrupt := []struct {
		name     string
		contents string
	}{
		{"non-numeric text", "banana"},
		{"a negative number", "-5"},
		{"a decimal", "1.5"},
		{"an integer with trailing junk", "12abc"},
		{"a value past int64", "99999999999999999999"},
		{"a hex literal", "0x10"},
	}

	for _, tc := range corrupt {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".seq")
			require.NoError(t, os.WriteFile(p, []byte(tc.contents), 0o600))

			n, err := nextSequenceValue(p)

			require.Error(t, err,
				"a counter holding %q must be REFUSED — a silent reset re-mints an identifier already given out", tc.contents)
			// The returned value must not be usable as a sequence number.
			// Asserting it is zero is what stops a future refactor from
			// returning (1, err) and letting a caller that checks the value
			// before the error mint WD-0001 over the top of a live record.
			assert.Equal(t, int64(0), n, "a refused read must not also hand back a mintable number")
			// The operator has to be able to find the broken file, so the
			// message must name the path AND the offending contents.
			assert.Contains(t, err.Error(), p, "the error must name the counter file the operator has to fix")
			assert.Contains(t, err.Error(), tc.contents, "the error must quote what it actually found")
		})
	}
}

// ---------------------------------------------------------------------------
// identityFor — the rendered shape
// ---------------------------------------------------------------------------

// TestIdentityFor_ShapeWithAndWithoutAPrefix covers the untested branch: a
// schema declaring no identity_prefix mints a BARE zero-padded number.
//
// The two cases sit in one test because the property that matters is that
// they DIFFER — a single-case test passes on an implementation that ignores
// the prefix entirely, or on one that always prepends a hyphen.
func TestIdentityFor_ShapeWithAndWithoutAPrefix(t *testing.T) {
	withPrefix := &records.Schema{Type: "widget"}
	withPrefix.Identity.Prefix = "WD"
	noPrefix := &records.Schema{Type: "widget"}

	cases := []struct {
		n            int64
		wantPrefixed string
		wantBare     string
	}{
		{1, "WD-0001", "0001"},
		{42, "WD-0042", "0042"},
		{9999, "WD-9999", "9999"},
		// Past the pad width the number must GROW, not wrap or truncate —
		// "four digits MINIMUM", not "four digits exactly".
		{10000, "WD-10000", "10000"},
		{123456, "WD-123456", "123456"},
	}

	for _, tc := range cases {
		got := identityFor(withPrefix, tc.n)
		assert.Equal(t, tc.wantPrefixed, got)

		bare := identityFor(noPrefix, tc.n)
		assert.Equal(t, tc.wantBare, bare,
			"a schema with no identity_prefix mints a bare zero-padded number")
		assert.NotContains(t, bare, "-",
			"a missing prefix must not leave a leading hyphen behind")
		assert.NotEqual(t, got, bare,
			"the prefix must actually reach the rendered identifier")
	}
}

// ---------------------------------------------------------------------------
// The counter as a PERSISTED artefact, through the real create door
// ---------------------------------------------------------------------------

// TestKnowledgeRecordCreate_PersistsTheCounterAndAdvancesIt proves the `.seq`
// file is a real durable artefact rather than an in-memory number that
// happens to be right within one process.
//
// Without the on-disk assertion, an implementation that never calls
// writeSequenceValue at all still passes every create test in the suite: the
// FIRST create mints correctly (the collision scan alone gets it to WD-0002),
// and the duplicate only appears on a later create in a fresh process.
func TestKnowledgeRecordCreate_PersistsTheCounterAndAdvancesIt(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)
	seq := widgetSeqPath(vault)

	// Precondition, asserted rather than assumed: no counter exists yet.
	_, statErr := os.Stat(seq)
	require.True(t, os.IsNotExist(statErr), "the fixture must start with no counter, or this test proves nothing")

	first := createWidget(t, api, ws, "a.md", "First")
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	firstID := decodeJSON[gen.VaultRecord](t, first).Id
	// WD-0001 is live in the fixture, so the allocator must advance past it.
	require.Equal(t, "WD-0002", firstID)

	onDisk, err := os.ReadFile(seq)
	require.NoError(t, err, "the counter must be PERSISTED, not merely computed in memory")
	assert.Equal(t, "2", strings.TrimSpace(string(onDisk)),
		"the counter records the number actually minted, so the next process starts from it")

	second := createWidget(t, api, ws, "b.md", "Second")
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	secondID := decodeJSON[gen.VaultRecord](t, second).Id

	assert.Equal(t, "WD-0003", secondID)
	assert.NotEqual(t, firstID, secondID, "two creates must never share an identifier")

	after, err := os.ReadFile(seq)
	require.NoError(t, err)
	assert.Equal(t, "3", strings.TrimSpace(string(after)), "the counter advanced with the mint")
}

// TestKnowledgeRecordCreate_AdvancesPastAHandWrittenLiveIdentifier is FR-038a
// with a gap the counter cannot know about: an operator imports (or restores)
// a note carrying an id the counter has not reached. The allocator must scan
// the live set and skip it, not mint a duplicate.
func TestKnowledgeRecordCreate_AdvancesPastAHandWrittenLiveIdentifier(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)

	// The counter says the next free number is 2; these two notes take
	// WD-0002 and WD-0003 without ever touching it.
	writeNote(t, vault, "imported-2.md", recordTestWidgetNote("WD-0002", "Imported Two", "open"))
	writeNote(t, vault, "imported-3.md", recordTestWidgetNote("WD-0003", "Imported Three", "open"))

	w := createWidget(t, api, ws, "fresh.md", "Fresh")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	got := decodeJSON[gen.VaultRecord](t, w).Id

	assert.Equal(t, "WD-0004", got,
		"the allocator must skip every LIVE identifier, not just trust the counter")

	// The point of the skip is that nothing was overwritten. Both imported
	// notes must still hold their own ids.
	for path, wantID := range map[string]string{
		"imported-2.md": "WD-0002",
		"imported-3.md": "WD-0003",
	} {
		raw, err := os.ReadFile(filepath.Join(vault, path))
		require.NoError(t, err)
		assert.Contains(t, string(raw), "id: "+wantID, "%s must be untouched by the mint", path)
	}
}

// TestKnowledgeRecordCreate_CorruptedCounterRefusesAndWritesNothing is the
// end-to-end half of the corrupted-counter refusal: the door must answer an
// error and leave the vault exactly as it found it.
//
// A create that half-succeeds — note written, counter not advanced — is the
// state that produces the duplicate on the NEXT create, so "nothing was
// written" is the assertion that matters, not merely the status code.
func TestKnowledgeRecordCreate_CorruptedCounterRefusesAndWritesNothing(t *testing.T) {
	api, ws, vault := buildRecordTestVault(t)
	seq := widgetSeqPath(vault)

	require.NoError(t, os.MkdirAll(filepath.Dir(seq), 0o700))
	require.NoError(t, os.WriteFile(seq, []byte("not-a-number\n"), 0o600))

	w := createWidget(t, api, ws, "should-not-exist.md", "Doomed")

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a corrupted counter is a SERVER fault the operator must fix, not a silent reset")
	assert.NotEqual(t, http.StatusCreated, w.Code,
		"minting past a counter it could not read is the duplicate-identifier bug this refusal prevents")

	_, statErr := os.Stat(filepath.Join(vault, "should-not-exist.md"))
	assert.True(t, os.IsNotExist(statErr),
		"a refused create must not leave a note behind")

	after, err := os.ReadFile(seq)
	require.NoError(t, err)
	assert.Equal(t, "not-a-number", strings.TrimSpace(string(after)),
		"the refusal must not overwrite the evidence the operator needs to repair the counter")

	// POSITIVE CONTROL, in the same test: repair the counter and the very
	// same request succeeds. Without this the assertions above would pass on
	// a door that refuses every create for any reason at all.
	require.NoError(t, os.WriteFile(seq, []byte("7\n"), 0o600))
	ok := createWidget(t, api, ws, "should-not-exist.md", "Doomed")
	require.Equal(t, http.StatusCreated, ok.Code, ok.Body.String())
	assert.Equal(t, "WD-0008", decodeJSON[gen.VaultRecord](t, ok).Id,
		"a repaired counter resumes from its stored value")
}
