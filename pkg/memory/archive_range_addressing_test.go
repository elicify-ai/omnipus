//go:build goolm && stdjson

package memory

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// F1 archive-addressing RED pack (C context-window review finding F1;
// verifier report treated as untrusted evidence, oracle re-derived from the
// pinned sources read in this task).
//
// Oracle sources on pin 698b682357f817a482a234a630ecbe3300469996:
//   - jsonl.go::ScanArchive / ::readMessages: a corrupt nonempty line is
//     logged and skipped WITHOUT consuming an index, "so ScanArchive and
//     ReadArchive agree on every message's archive index".
//   - window.go::snapshotWindowLocked: "Archive addresses index decoded
//     records, just like ReadArchive/ScanArchive"; meta.Count = len(archive).
//   - jsonl.go::TruncateHistory: "Cursor and projection addresses count
//     decoded archive records, exactly like ReadArchive. Malformed physical
//     lines do not shift these identities."
//   - pkg/memory/CLAUDE.md "Durability contract": malformed lines are "logged
//     and skipped, never fatal — readMessages and ScanArchive keep index
//     parity".
// Every citation the model receives (breadcrumb archive_line and range,
// projection marks) is born from that decoded slice, so ScanArchiveRange's
// record addresses must identify decoded records, not nonempty physical
// lines. ScanJSONLRange's own doc claiming the physical space is the defect
// under repair, never an oracle for these tests.
//
// Residual design question (disclosed in the RED handoff, not resolved by
// weakening): for a malformed line INSIDE the selected range these tests
// assert the decoded contract above (skip + log, record delivered). A
// loud-error design would contradict the written durability contract and
// needs an explicit authority ruling first. "Any error is fine" is not an
// accepted oracle in either direction.

// cwArchiveAddressLines is five physical nonempty lines whose third line is a
// truncated crash write (valid UTF-8, unterminated JSON — json.Unmarshal
// rejects it). The decoded archive is four records: D0 question one, D1
// answer one, D2 question two, D3 answer two.
var cwArchiveAddressLines = []string{
	`{"role":"user","content":"question one","ts":100}` + "\n",
	`{"role":"assistant","content":"answer one","ts":101}` + "\n",
	`{"role":"assistant","content":"answ` + "\n",
	`{"role":"user","content":"question two","ts":200}` + "\n",
	`{"role":"assistant","content":"answer two","ts":201}` + "\n",
}

// cwDecodedToPhysical maps decoded record index to physical fixture line. It
// is derived from the fixture comment above (the corrupt line consumes no
// index), never from an observation of the code under test.
var cwDecodedToPhysical = [4]int{0, 1, 3, 4}

func cwWriteArchiveAddressFixture(t *testing.T, key string, lines []string) *JSONLStore {
	t.Helper()
	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("fixture store: %v", err)
	}
	if err := os.WriteFile(store.jsonlPath(key), []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatalf("fixture archive: %v", err)
	}
	return store
}

// cwArchiveRecord captures what one callback invocation received. The raw
// slice is borrowed until fn returns (ArchiveRangeScanner contract), so it is
// copied to a string inside the callback.
type cwArchiveRecord struct {
	raw     string
	role    string
	content string
}

func cwCollectOne(t *testing.T, store *JSONLStore, ctx context.Context, key string, from, to int) (cwArchiveRecord, int, error) {
	t.Helper()
	var got cwArchiveRecord
	calls := 0
	err := store.ScanArchiveRange(ctx, key, from, to, func(_ int, raw []byte, msg ArchivedMessage) error {
		calls++
		got = cwArchiveRecord{raw: string(raw), role: msg.Role, content: msg.Content}
		return nil
	})
	return got, calls, err
}

// TestScanArchiveRange_AddressesDecodedRecords is the F1 regression pack: a
// citation that survives a malformed archive line must still identify the
// same record the decoded-space surfaces (ScanArchive, window Skip,
// breadcrumbs, marks) address. Physical-line counting silently shifts (or
// loudly refuses) exactly the addresses every other surface speaks.
func TestScanArchiveRange_AddressesDecodedRecords(t *testing.T) {
	ctx := context.Background()
	const key = "cw-address"

	t.Run("cited_identity_after_corrupt_line", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		// Citation 3 is decoded record 3 ("answer two"): breadcrumbs and
		// marks cite decoded indices, and TruncateHistory pins Skip=Count=4
		// in that same space.
		got, calls, err := cwCollectOne(t, store, ctx, key, 3, 3)
		if err != nil {
			t.Fatalf("archive_range [3,3] must return the cited decoded record 3, got error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("archive_range [3,3] callback count = %d, want 1", calls)
		}
		// Raw bytes are the archive's literal line including its newline.
		if got.raw != cwArchiveAddressLines[4] {
			t.Fatalf("archive_range [3,3] raw = %q, want the cited record's literal line %q (silently returning a neighbouring physical line is the F1 defect)", got.raw, cwArchiveAddressLines[4])
		}
		if got.role != "assistant" || got.content != "answer two" {
			t.Fatalf("archive_range [3,3] decoded = {%s, %q}, want {assistant, answer two}", got.role, got.content)
		}
	})

	t.Run("decoded_parity_every_index", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		// ScanArchive is the decoded-space authority; every single-record
		// range must deliver exactly the same record with its literal line.
		var decoded []ArchivedMessage
		if err := store.ScanArchive(ctx, key, func(_ int, msg ArchivedMessage) bool {
			decoded = append(decoded, msg)
			return true
		}); err != nil {
			t.Fatalf("ScanArchive: %v", err)
		}
		if len(decoded) != 4 {
			t.Fatalf("fixture decoded count = %d, want 4 (the corrupt line consumes no index)", len(decoded))
		}
		for i := 0; i < 4; i++ {
			got, calls, err := cwCollectOne(t, store, ctx, key, i, i)
			if err != nil {
				t.Fatalf("archive_range [%d,%d] error: %v", i, i, err)
			}
			if calls != 1 {
				t.Fatalf("archive_range [%d,%d] callback count = %d, want 1", i, i, calls)
			}
			wantRaw := cwArchiveAddressLines[cwDecodedToPhysical[i]]
			if got.raw != wantRaw {
				t.Fatalf("archive_range [%d,%d] raw = %q, want %q (decoded record %d must be its own literal archive line)", i, i, got.raw, wantRaw, i)
			}
			if got.role != decoded[i].Role || got.content != decoded[i].Content {
				t.Fatalf("archive_range [%d,%d] decoded = {%s, %q}, want ScanArchive[%d] = {%s, %q}", i, i, got.role, got.content, i, decoded[i].Role, decoded[i].Content)
			}
		}
	})

	t.Run("header_range_full_prefix", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		// The breadcrumb header's own suggested call is archive_range={from:0,
		// to:Skip-1} with Skip=4. It must deliver all four decoded records in
		// order — the malformed line is skipped and logged (durability
		// contract), never a fatal decode error and never a shifted prefix.
		type seen struct {
			idx int
			raw string
		}
		var got []seen
		err := store.ScanArchiveRange(ctx, key, 0, 3, func(idx int, raw []byte, _ ArchivedMessage) error {
			got = append(got, seen{idx: idx, raw: string(raw)})
			return nil
		})
		if err != nil {
			t.Fatalf("header range [0,3] must deliver all decoded records, got error: %v", err)
		}
		if len(got) != 4 {
			t.Fatalf("header range [0,3] callback count = %d, want 4 decoded records", len(got))
		}
		for i, rec := range got {
			wantRaw := cwArchiveAddressLines[cwDecodedToPhysical[i]]
			if rec.idx != i || rec.raw != wantRaw {
				t.Fatalf("header range record %d = {idx:%d, raw:%q}, want {idx:%d, raw:%q}", i, rec.idx, rec.raw, i, wantRaw)
			}
		}
	})

	t.Run("corrupt_line_inside_range", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		// Decoded record 2 is "question two" (physical line 3); the malformed
		// physical line consumes no index and is never selected under decoded
		// addressing (logged and skipped, never fatal). See the residual
		// design question in the file header before changing this expectation.
		got, calls, err := cwCollectOne(t, store, ctx, key, 2, 2)
		if err != nil {
			t.Fatalf("archive_range [2,2] must return decoded record 2, got error: %v", err)
		}
		if calls != 1 {
			t.Fatalf("archive_range [2,2] callback count = %d, want 1", calls)
		}
		if got.raw != cwArchiveAddressLines[3] {
			t.Fatalf("archive_range [2,2] raw = %q, want %q", got.raw, cwArchiveAddressLines[3])
		}
		if got.role != "user" || got.content != "question two" {
			t.Fatalf("archive_range [2,2] decoded = {%s, %q}, want {user, question two}", got.role, got.content)
		}
	})

	t.Run("clean_archive_positive_control", func(t *testing.T) {
		// Same fixture minus the corrupt line: physical and decoded agree, so
		// [3,3] must be "answer two". Control proving the instrument observes
		// record identity rather than mere success.
		clean := []string{cwArchiveAddressLines[0], cwArchiveAddressLines[1], cwArchiveAddressLines[3], cwArchiveAddressLines[4]}
		store := cwWriteArchiveAddressFixture(t, key, clean)
		got, calls, err := cwCollectOne(t, store, ctx, key, 3, 3)
		if err != nil {
			t.Fatalf("clean-archive control [3,3] error: %v", err)
		}
		if calls != 1 || got.raw != cwArchiveAddressLines[4] || got.role != "assistant" || got.content != "answer two" {
			t.Fatalf("clean-archive control [3,3] = (calls:%d, raw:%q, {%s, %q}), want the literal final line {assistant, answer two}", calls, got.raw, got.role, got.content)
		}
	})

	t.Run("invalid_range_reversed_is_visible_error", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		calls := 0
		err := store.ScanArchiveRange(ctx, key, 2, 1, func(int, []byte, ArchivedMessage) error {
			calls++
			return nil
		})
		if err == nil {
			t.Fatal("archive_range [2,1] (from > to) must be a visible error, not a silent empty page")
		}
		if calls != 0 {
			t.Fatalf("archive_range [2,1] callback count = %d, want 0", calls)
		}
	})

	t.Run("out_of_range_beyond_end_is_visible_error", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		calls := 0
		err := store.ScanArchiveRange(ctx, key, 4, 9, func(int, []byte, ArchivedMessage) error {
			calls++
			return nil
		})
		if err == nil {
			t.Fatal("archive_range [4,9] beyond the 4 decoded records must be a visible error, not silent clamping")
		}
		if calls != 0 {
			t.Fatalf("archive_range [4,9] callback count = %d, want 0", calls)
		}
	})

	t.Run("missing_archive_file_is_visible_error", func(t *testing.T) {
		store, err := NewJSONLStore(t.TempDir())
		if err != nil {
			t.Fatalf("fixture store: %v", err)
		}
		calls := 0
		err = store.ScanArchiveRange(ctx, "cw-missing", 0, 0, func(int, []byte, ArchivedMessage) error {
			calls++
			return nil
		})
		if err == nil {
			t.Fatal("archive_range [0,0] on a missing archive must be a visible error, not silent success")
		}
		if calls != 0 {
			t.Fatalf("archive_range [0,0] on missing archive callback count = %d, want 0", calls)
		}
	})

	t.Run("skip_counts_decoded_records", func(t *testing.T) {
		store := cwWriteArchiveAddressFixture(t, key, cwArchiveAddressLines)
		// TruncateHistory is the commit-time identity authority: keepLast=0
		// must report Skip=Count=4 — four decoded records, the malformed line
		// uncounted. This pins the fixture arithmetic the citation
		// expectations above derive from.
		if err := store.TruncateHistory(ctx, key, 0); err != nil {
			t.Fatalf("TruncateHistory: %v", err)
		}
		encoded, err := os.ReadFile(store.metaPath(key))
		if err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Skip  int `json:"skip"`
			Count int `json:"count"`
		}
		if err := json.Unmarshal(encoded, &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Skip != 4 || meta.Count != 4 {
			t.Fatalf("persisted window metadata = Skip:%d Count:%d, want 4/4 decoded records (the malformed physical line must not count)", meta.Skip, meta.Count)
		}
	})
}
