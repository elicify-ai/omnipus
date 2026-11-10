// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the standalone addressed archive path — session-core C-ARCHIVE / U2
// Decisions B and C (spec FR-004/FR-005/FR-006/FR-008). Oracles come from the
// spec (the C-ARCHIVE shape, Decision B's "address, no body", Decision C's
// bounded addressed read), never from the implementation.
package session

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func mustStore(t *testing.T, baseDir string) *ArchiveDayStore {
	t.Helper()
	s, err := NewArchiveDayStore(baseDir, "sess-1")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

func atDay(s *ArchiveDayStore, day string) {
	t, _ := time.Parse(transcriptDayLayout, day)
	s.now = func() time.Time { return t }
}

func payloadRecord(t *testing.T, id, day, content string) ArchiveRecord {
	t.Helper()
	mp, err := EncodeModelPayload(providers.Message{Role: "user", Content: content})
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	ts, _ := time.Parse(transcriptDayLayout, day)
	return ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: id, Role: "user", Content: content, AgentID: "mia",
			ViewMembership: ViewMembershipChat, Timestamp: ts},
		ModelMessage: &mp,
		Source:       &EntrySource{Kind: "user", Principal: "daniel", RequestID: "req-" + id},
	}
}

// Decision A/FR-004: the full envelope round-trips faithfully through the
// archive. Every private member (model_message, source) and the provider-only
// tool-call members survive the disk write and the addressed read.
func TestU2Archive_FaithfulEnvelopeRoundTrip(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")

	mp, err := EncodeModelPayload(populatedMessage())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec := ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: "e-1", Role: "assistant", Content: "calling a tool",
			AgentID: "mia", ViewMembership: ViewMembershipBoth, Timestamp: time.Unix(1, 0).UTC()},
		ModelMessage: &mp,
		Source:       &EntrySource{Kind: "agent", Principal: "mia"},
	}
	addr, err := store.Append(rec)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := store.ReadAt(addr)
	if err != nil {
		t.Fatalf("read at %+v: %v", addr, err)
	}
	if got.ModelMessage == nil {
		t.Fatal("private model_message lost through the archive round trip")
	}
	dec, err := DecodeModelPayload(*got.ModelMessage)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(dec, populatedMessage()) {
		t.Fatalf("model_message not faithfully preserved\n got: %#v\nwant: %#v", dec, populatedMessage())
	}
	if got.Source == nil || got.Source.Principal != "mia" {
		t.Fatalf("trusted source lost: %#v", got.Source)
	}
}

// Decision B: at the consumption fence a model_ref is appended that carries the
// saved payload's exact address and NO body copy. The raw persisted line must
// contain the address and must NOT contain the source's content or a second
// model_message.
func TestU2Archive_ModelRefPlacedAtFenceCarriesAddressNoBody(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")

	srcAddr, err := store.Append(payloadRecord(t, "in-1", "2026-03-04", "SECRET-INPUT-BODY"))
	if err != nil {
		t.Fatalf("append source: %v", err)
	}
	refAddr, err := PlaceModelRef(store, srcAddr)
	if err != nil {
		t.Fatalf("place model_ref: %v", err)
	}

	ref, err := store.ReadAt(refAddr)
	if err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if ref.Type != EntryTypeModelRef {
		t.Fatalf("placement record type = %q, want %q", ref.Type, EntryTypeModelRef)
	}
	if ref.ViewMembership != ViewMembershipModel {
		t.Fatalf("placement view_membership = %q, want %q (no chat bubble)", ref.ViewMembership, ViewMembershipModel)
	}
	if ref.ModelMessage != nil {
		t.Fatal("placement copied a model_message body; it must be body-free")
	}
	if ref.Content != "" {
		t.Fatalf("placement copied content %q; it must carry no body", ref.Content)
	}
	if ref.ModelRef == nil || ref.ModelRef.EntryID != srcAddr.EntryID ||
		ref.ModelRef.PartitionKey != srcAddr.PartitionKey || ref.ModelRef.ByteOffset != srcAddr.ByteOffset {
		t.Fatalf("placement must name the exact source address; got %#v want %+v", ref.ModelRef, srcAddr)
	}

	// Raw-line control: the persisted placement names the address and carries no
	// source body (a body copy would appear as the source's content text).
	raw := rawLine(t, store, refAddr)
	if !strings.Contains(raw, "model_ref") || !strings.Contains(raw, srcAddr.EntryID) {
		t.Fatalf("placement line must name the model_ref and source id: %s", raw)
	}
	if strings.Contains(raw, "SECRET-INPUT-BODY") {
		t.Fatalf("placement line copied the source body: %s", raw)
	}
	if strings.Contains(raw, `"model_message"`) {
		t.Fatalf("placement line carries a model_message body: %s", raw)
	}
}

// Decision B: one accepted input gets at most one committed model placement.
// A second ConsumeOnce of the same source is refused.
func TestU2Archive_ConsumeOnceRefusesSecondPlacement(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")
	srcAddr, err := store.Append(payloadRecord(t, "in-2", "2026-03-04", "body"))
	if err != nil {
		t.Fatalf("append source: %v", err)
	}

	var w AddressedWindow
	refAddr, w, err := w.ConsumeOnce(store, srcAddr)
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if len(w.Model) != 1 || w.Model[0] != refAddr {
		t.Fatalf("window must record the placement slot; got %#v", w.Model)
	}
	if _, _, err := w.ConsumeOnce(store, srcAddr); !errors.Is(err, ErrAlreadyConsumed) {
		t.Fatalf("second consume error = %v, want ErrAlreadyConsumed", err)
	}
}

// Decision C / FR-005: an addressed read seeks straight to the record and never
// decodes the prefix. Proven by corrupting an earlier record's bytes and showing
// a later addressed read still succeeds — while an addressed read OF the
// corrupted record fails, so the corruption is real and detectable.
func TestU2Archive_BoundedReadSeeksToOffsetWithoutDecodingPrefix(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")

	var addrs []ArchiveAddress
	for i := 0; i < 5; i++ {
		a, err := store.Append(payloadRecord(t, "r"+string(rune('0'+i)), "2026-03-04", "content-"+string(rune('0'+i))))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		addrs = append(addrs, a)
	}

	// The last address must equal the exact encoded byte length of the four
	// preceding lines — the offset is byte-accurate, not a record index.
	path := filepath.Join(store.dir(), archiveCurrentFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	var wantOff int64
	for i := 0; i < 4; i++ {
		wantOff += int64(len(lines[i]))
	}
	if addrs[4].ByteOffset != wantOff {
		t.Fatalf("byte offset = %d, want %d (sum of preceding encoded lines)", addrs[4].ByteOffset, wantOff)
	}

	// Corrupt the FIRST record's bytes in place (same length → later offsets
	// unchanged). A reader that decoded from zero would now fail.
	first := []byte(lines[0])
	for i := range first {
		if first[i] != '\n' {
			first[i] = 'Z'
		}
	}
	if err = os.WriteFile(path, append(first, []byte(strings.Join(lines[1:], ""))...), 0o600); err != nil {
		t.Fatalf("corrupt prefix: %v", err)
	}

	// Instrument control: the corrupted record is genuinely unreadable, so a
	// success below is a real bounded read, not a reader that ignores damage.
	if _, err = store.ReadAt(addrs[0]); err == nil {
		t.Fatal("control failed: the corrupted first record read as valid")
	}
	got, err := store.ReadAt(addrs[4])
	if err != nil {
		t.Fatalf("bounded read of a later record failed after prefix corruption: %v", err)
	}
	if got.ID != "r4" {
		t.Fatalf("bounded read returned %q, want r4", got.ID)
	}
}

// Decision C / FR-005: the partition key is stable through the current-file
// rename, so an address taken before a day rollover still resolves afterwards.
func TestU2Archive_AddressStableAcrossRolloverRename(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")
	a0, err := store.Append(payloadRecord(t, "d1", "2026-03-04", "day one"))
	if err != nil {
		t.Fatalf("append day1: %v", err)
	}
	if a0.PartitionKey != "2026-03-04" {
		t.Fatalf("key = %q, want the day it was written under", a0.PartitionKey)
	}
	atDay(store, "2026-03-05")
	a1, err := store.Append(payloadRecord(t, "d2", "2026-03-05", "day two"))
	if err != nil {
		t.Fatalf("append day2: %v", err)
	}
	if a0.PartitionKey == a1.PartitionKey {
		t.Fatalf("rollover did not start a new partition: both keys %q", a0.PartitionKey)
	}
	// Both addresses still resolve — a0 through the rolled file, a1 through the
	// live file — after the rename.
	if got, err := store.ReadAt(a0); err != nil || got.ID != "d1" {
		t.Fatalf("address before rollover no longer resolves: rec=%q err=%v", got.ID, err)
	}
	if got, err := store.ReadAt(a1); err != nil || got.ID != "d2" {
		t.Fatalf("address after rollover does not resolve: rec=%q err=%v", got.ID, err)
	}
}

// Decision C: the window reconstructs model order from its persisted addresses
// and reads ONLY the active slots — an evicted prefix is never decoded.
func TestU2Archive_AddressedWindowReadsOnlyActiveSlots(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")

	evicted, err := store.Append(payloadRecord(t, "evicted", "2026-03-04", "old"))
	if err != nil {
		t.Fatalf("append evicted: %v", err)
	}
	srcAddr, err := store.Append(payloadRecord(t, "keep", "2026-03-04", "kept-content"))
	if err != nil {
		t.Fatalf("append keep: %v", err)
	}
	refAddr, err := PlaceModelRef(store, srcAddr)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	w := AddressedWindow{HasStart: true, Start: srcAddr, Model: []ArchiveAddress{refAddr}}

	// Corrupt the evicted record; the window never reads it.
	corruptLineAt(t, store, evicted)

	msgs, err := w.ModelMessages(store)
	if err != nil {
		t.Fatalf("window model messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Content != "kept-content" {
		t.Fatalf("window reconstructed %#v, want the single kept source message", msgs)
	}
}

// Decision C: the window state persists as addresses only — content-free — and
// still reconstructs the model message by seeking to the referenced source.
func TestU2Archive_WindowStatePersistsContentFree(t *testing.T) {
	store := mustStore(t, t.TempDir())
	atDay(store, "2026-03-04")
	srcAddr, err := store.Append(payloadRecord(t, "in-3", "2026-03-04", "PERSIST-SECRET"))
	if err != nil {
		t.Fatalf("append source: %v", err)
	}
	refAddr, err := PlaceModelRef(store, srcAddr)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	w := AddressedWindow{HasStart: true, Start: srcAddr, HasEnd: true, End: refAddr,
		Model: []ArchiveAddress{refAddr}}

	if err = store.SaveWindow(w); err != nil {
		t.Fatalf("save window: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(store.dir(), archiveWindowFile))
	if err != nil {
		t.Fatalf("read window file: %v", err)
	}
	if strings.Contains(string(raw), "PERSIST-SECRET") {
		t.Fatalf("window state leaked provider content: %s", raw)
	}
	got, found, err := store.LoadWindow()
	if err != nil || !found {
		t.Fatalf("load window: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(got, w) {
		t.Fatalf("window state round trip mismatch\n got %#v\nwant %#v", got, w)
	}
	msgs, err := got.ModelMessages(store)
	if err != nil {
		t.Fatalf("window messages after reload: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "PERSIST-SECRET" {
		t.Fatalf("reloaded window reconstructed %#v", msgs)
	}
}

// Decision A/B write gate: a malformed envelope is refused before any byte is
// written.
func TestU2Archive_ValidateRefusesMalformedEnvelope(t *testing.T) {
	cases := map[string]ArchiveRecord{
		"missing view_membership": {TranscriptEntry: TranscriptEntry{ID: "x"}},
		"model_ref without ref": {TranscriptEntry: TranscriptEntry{ID: "x", Type: EntryTypeModelRef,
			ViewMembership: ViewMembershipModel}},
		"model_ref with body": {TranscriptEntry: TranscriptEntry{ID: "x", Type: EntryTypeModelRef,
			ViewMembership: ViewMembershipModel, Content: "leak"},
			ModelRef: &ModelRef{EntryID: "s", PartitionKey: "d"}},
		"tool payload without tool_result_for": {TranscriptEntry: TranscriptEntry{ID: "x",
			ViewMembership: ViewMembershipBoth},
			ModelMessage: &ModelPayload{Role: "tool", ToolCallID: "c"}},
	}
	for name, rec := range cases {
		if err := rec.Validate(); err == nil {
			t.Errorf("%s: Validate accepted a malformed envelope", name)
		}
	}
}

func rawLine(t *testing.T, store *ArchiveDayStore, addr ArchiveAddress) string {
	t.Helper()
	path, err := store.partitionPathForKeyLocked(addr.PartitionKey)
	if err != nil {
		t.Fatalf("partition path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read partition: %v", err)
	}
	if addr.ByteOffset >= int64(len(data)) {
		t.Fatalf("offset %d beyond file (%d bytes)", addr.ByteOffset, len(data))
	}
	rest := string(data[addr.ByteOffset:])
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func corruptLineAt(t *testing.T, store *ArchiveDayStore, addr ArchiveAddress) {
	t.Helper()
	path, err := store.partitionPathForKeyLocked(addr.PartitionKey)
	if err != nil {
		t.Fatalf("partition path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read partition: %v", err)
	}
	start := int(addr.ByteOffset)
	end := start
	for end < len(data) && data[end] != '\n' {
		end++
	}
	for i := start; i < end; i++ {
		data[i] = 'Z'
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("corrupt line: %v", err)
	}
	// Control: the corruption is now unreadable through the addressed reader.
	if _, err := store.ReadAt(addr); err == nil {
		t.Fatalf("corruption at %+v did not take effect", addr)
	}
}
