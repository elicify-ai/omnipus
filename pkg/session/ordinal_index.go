// ordinal_index.go: the content-free model-slot ordinal index of the addressed
// archive — session-core C-ARCHIVE / U2 Decision C ("address-only day marks for
// ordinal/recall lookup"; caller-migration step 1).
//
// WHAT it is. One row per MODEL PLACEMENT, in placement order, naming where the
// slot's record sits in the day archive. A placement is either a payload record
// whose view_membership is model or both, or a model_ref (which takes its own
// ordinal and resolves to its source). A chat-only saved input has NO ordinal
// until its model_ref is placed. The ordinal is the stable public `archive_line`
// recall selector: it is never renumbered by rollback, sweep or clear.
//
// A row holds addresses, the role, the tool_call_id (a tool result) or the issued
// call ids (an assistant) and the running user-turn count — an integer, never
// provider content. It is not a second history: every byte of content stays in
// the one archive.
//
// WHY two files. Rows are variable length, so ordinal → row needs a lookup:
//
//	ordinal_rows.log      append-only newline-framed JSON rows, one per ordinal
//	ordinal_offsets.bin   fixed 8-byte big-endian row offsets, ordinal*8
//
// A bounded window read seeks to offsets[Skip] and reads rows to the end; an
// evicted-range read seeks to offsets[from]. Neither touches the archive
// partitions. Neither file ends in ".jsonl", so the archive scanner never takes
// them for a day partition.
//
// PUBLICATION. The row is appended and fsynced first; the offset is appended
// second and NOT fsynced, because the offsets table is derived from the row log
// and rebuilt from it when it disagrees. A crash between them leaves one
// complete row with no offset; the next open completes it from the saved tail —
// a read of the last offset entry and the file end, never a lifetime recount. A
// torn trailing row (no newline) was never published: it is cut off. If a power
// loss leaves the offsets table inconsistent with the row log (a lost or
// zero-filled entry), the next read detects it (every row carries its own
// ordinal) and rebuilds the table from the rows in one pass. An archive record
// whose row was never published is retained-but-excluded residue: it has no
// ordinal, so no window can reach it.
package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	ordinalRowsFile    = "ordinal_rows.log"
	ordinalOffsetsFile = "ordinal_offsets.bin"
	ordinalOffsetSize  = 8
	// ordinalRowBound caps one index row. A row carries ids and counters only;
	// an assistant's issued call ids are the only unbounded member.
	ordinalRowBound = 1 << 20
)

// ordinalRow is one content-free model slot.
type ordinalRow struct {
	Ordinal int `json:"o"`
	// Slot is the record that occupies the slot: the payload itself, or the
	// model_ref placement.
	Slot ArchiveAddress `json:"a"`
	// Source is the referenced payload of a model_ref slot. Nil for a payload.
	Source *ArchiveAddress `json:"s,omitempty"`
	Role   string          `json:"r"`
	// ToolCallID is set on a tool result. CallIDs lists the calls an assistant
	// issued, so a result's issuing assistant is found from rows alone.
	ToolCallID string   `json:"c,omitempty"`
	CallIDs    []string `json:"k,omitempty"`
	// UserTurn counts the user messages at ordinals <= Ordinal.
	UserTurn int `json:"u"`
	// TS is the payload record's timestamp, unix seconds.
	TS int64 `json:"t,omitempty"`
}

// content resolves the address whose payload carries the slot's model message.
func (r ordinalRow) content() ArchiveAddress {
	if r.Source != nil {
		return *r.Source
	}
	return r.Slot
}

func (s *ArchiveDayStore) ordinalRowsPath() string { return filepath.Join(s.dir(), ordinalRowsFile) }
func (s *ArchiveDayStore) ordinalOffsetsPath() string {
	return filepath.Join(s.dir(), ordinalOffsetsFile)
}

// OrdinalCount reports how many model slots are published.
func (s *ArchiveDayStore) OrdinalCount() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ordinalCountLocked()
}

func (s *ArchiveDayStore) ordinalCountLocked() (int, error) {
	if err := s.repairOrdinalIndexLocked(); err != nil {
		return 0, err
	}
	fi, err := os.Stat(s.ordinalOffsetsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("ordinal index: stat offsets: %w", err)
	}
	return int(fi.Size() / ordinalOffsetSize), nil
}

// readOffsetLocked returns the row offset of ordinal o.
func (s *ArchiveDayStore) readOffsetLocked(o int) (int64, error) {
	f, err := os.Open(s.ordinalOffsetsPath())
	if err != nil {
		return 0, fmt.Errorf("ordinal index: open offsets: %w", err)
	}
	defer f.Close()
	var buf [ordinalOffsetSize]byte
	if _, err := f.ReadAt(buf[:], int64(o)*ordinalOffsetSize); err != nil {
		return 0, fmt.Errorf("ordinal index: read offset %d: %w", o, err)
	}
	return int64(binary.BigEndian.Uint64(buf[:])), nil
}

// readRowAtLocked reads the single newline-framed row starting at off.
func readRowAt(f *os.File, off int64) (ordinalRow, int, error) {
	br := bufio.NewReaderSize(io.NewSectionReader(f, off, ordinalRowBound+1), 4096)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return ordinalRow{}, 0, fmt.Errorf("row at %d is not newline-framed: %w", off, err)
	}
	var row ordinalRow
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &row); err != nil {
		return ordinalRow{}, 0, fmt.Errorf("decode row at %d: %w", off, err)
	}
	return row, len(line), nil
}

// repairOrdinalIndexLocked makes the row log and the offsets table agree, from
// the saved tail forward. It reads the last offset entry and the end of the row
// log; it never walks earlier rows.
func (s *ArchiveDayStore) repairOrdinalIndexLocked() error {
	rows, err := os.OpenFile(s.ordinalRowsPath(), os.O_RDWR, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ordinal index: open rows: %w", err)
	}
	defer rows.Close()
	size, err := rows.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("ordinal index: size rows: %w", err)
	}
	offs, err := os.OpenFile(s.ordinalOffsetsPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("ordinal index: open offsets: %w", err)
	}
	defer offs.Close()
	osize, err := offs.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("ordinal index: size offsets: %w", err)
	}
	if rem := osize % ordinalOffsetSize; rem != 0 {
		osize -= rem // a torn offset entry was never published
		if err := offs.Truncate(osize); err != nil {
			return fmt.Errorf("ordinal index: trim torn offset: %w", err)
		}
	}
	n := int(osize / ordinalOffsetSize)
	// Where the next unindexed row would start: the end of the last indexed row.
	next := int64(0)
	if n > 0 {
		var buf [ordinalOffsetSize]byte
		if _, err := offs.ReadAt(buf[:], int64(n-1)*ordinalOffsetSize); err != nil {
			return fmt.Errorf("ordinal index: read last offset: %w", err)
		}
		last := int64(binary.BigEndian.Uint64(buf[:]))
		row, l, err := readRowAt(rows, last)
		if err != nil || row.Ordinal != n-1 {
			// The table disagrees with the row log: it is derived data, so it is
			// rebuilt from the rows rather than trusted.
			rows.Close()
			offs.Close()
			return s.rebuildOffsetsLocked()
		}
		next = last + int64(l)
	}
	if next == size {
		return nil
	}
	if next > size {
		return fmt.Errorf("ordinal index: row log (%d bytes) is shorter than its published rows (%d)", size, next)
	}
	// Complete a row that was appended but not yet given an offset; cut a torn tail.
	for next < size {
		row, l, err := readRowAt(rows, next)
		if err != nil || row.Ordinal != n {
			if err := rows.Truncate(next); err != nil {
				return fmt.Errorf("ordinal index: cut torn row log: %w", err)
			}
			return rows.Sync()
		}
		var buf [ordinalOffsetSize]byte
		binary.BigEndian.PutUint64(buf[:], uint64(next))
		if _, err := offs.WriteAt(buf[:], int64(n)*ordinalOffsetSize); err != nil {
			return fmt.Errorf("ordinal index: complete offset %d: %w", n, err)
		}
		n++
		next += int64(l)
	}
	return offs.Sync()
}

// rebuildOffsetsLocked rewrites the offsets table from the row log: one pass over
// the rows, keeping rows only while their ordinals run 0,1,2,.. and cutting the
// log at the first row that does not (a torn or out-of-sequence tail was never
// published).
func (s *ArchiveDayStore) rebuildOffsetsLocked() error {
	rows, err := os.OpenFile(s.ordinalRowsPath(), os.O_RDWR, 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ordinal index: open rows for rebuild: %w", err)
	}
	defer rows.Close()
	br := bufio.NewReaderSize(rows, 64*1024)
	var table []byte
	var off int64
	for n := 0; ; n++ {
		line, err := br.ReadBytes('\n')
		if err != nil {
			break // a torn tail without a newline was never published
		}
		var row ordinalRow
		if json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &row) != nil || row.Ordinal != n {
			break
		}
		var buf [ordinalOffsetSize]byte
		binary.BigEndian.PutUint64(buf[:], uint64(off))
		table = append(table, buf[:]...)
		off += int64(len(line))
	}
	if err := rows.Truncate(off); err != nil {
		return fmt.Errorf("ordinal index: cut row log: %w", err)
	}
	tmp := s.ordinalOffsetsPath() + ".tmp"
	if err := os.WriteFile(tmp, table, 0o600); err != nil {
		return fmt.Errorf("ordinal index: write rebuilt offsets: %w", err)
	}
	if err := os.Rename(tmp, s.ordinalOffsetsPath()); err != nil {
		return fmt.Errorf("ordinal index: install rebuilt offsets: %w", err)
	}
	return nil
}

// lastOrdinalRowLocked returns the final published row, or ok=false when empty.
func (s *ArchiveDayStore) lastOrdinalRowLocked() (row ordinalRow, n int, ok bool, err error) {
	n, err = s.ordinalCountLocked()
	if err != nil || n == 0 {
		return ordinalRow{}, n, false, err
	}
	row, err = s.readOrdinalRowLocked(n - 1)
	if errors.Is(err, errOrdinalTable) {
		if rerr := s.rebuildOffsetsLocked(); rerr != nil {
			return ordinalRow{}, n, false, errors.Join(err, rerr)
		}
		if n, err = s.ordinalCountLocked(); err != nil || n == 0 {
			return ordinalRow{}, n, false, err
		}
		row, err = s.readOrdinalRowLocked(n - 1)
	}
	return row, n, err == nil, err
}

func (s *ArchiveDayStore) readOrdinalRowLocked(o int) (ordinalRow, error) {
	off, err := s.readOffsetLocked(o)
	if err != nil {
		return ordinalRow{}, err
	}
	f, err := os.Open(s.ordinalRowsPath())
	if err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: open rows: %w", err)
	}
	defer f.Close()
	row, _, err := readRowAt(f, off)
	if err != nil {
		return ordinalRow{}, fmt.Errorf("%w: ordinal %d: %v", errOrdinalTable, o, err)
	}
	if row.Ordinal != o {
		return ordinalRow{}, fmt.Errorf("%w: offset %d holds row %d, not %d", errOrdinalTable, off, row.Ordinal, o)
	}
	return row, nil
}

// readOrdinalRows returns the published rows [from, to), clamped to the count.
// It seeks straight to offsets[from] and reads sequentially, so the work is the
// width of the range, never the lifetime.
func (s *ArchiveDayStore) readOrdinalRows(from, to int) ([]ordinalRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.readOrdinalRowsLocked(from, to)
	if errors.Is(err, errOrdinalTable) {
		// The offsets table is derived data that was not fsynced: rebuild it from
		// the row log once and read again.
		if rerr := s.rebuildOffsetsLocked(); rerr != nil {
			return nil, errors.Join(err, rerr)
		}
		rows, err = s.readOrdinalRowsLocked(from, to)
	}
	return rows, err
}

// errOrdinalTable marks an offsets-table disagreement a rebuild can repair.
var errOrdinalTable = errors.New("ordinal index: offsets table disagrees with the row log")

func (s *ArchiveDayStore) readOrdinalRowsLocked(from, to int) ([]ordinalRow, error) {
	n, err := s.ordinalCountLocked()
	if err != nil {
		return nil, err
	}
	if to > n {
		to = n
	}
	if from < 0 || from > to {
		return nil, fmt.Errorf("ordinal index: invalid range [%d,%d) over %d slots", from, to, n)
	}
	if from == to {
		return nil, nil
	}
	off, err := s.readOffsetLocked(from)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(s.ordinalRowsPath())
	if err != nil {
		return nil, fmt.Errorf("ordinal index: open rows: %w", err)
	}
	defer f.Close()
	br := bufio.NewReaderSize(io.NewSectionReader(f, off, 1<<62-off), 64*1024)
	out := make([]ordinalRow, 0, to-from)
	for o := from; o < to; o++ {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("%w: row %d: %v", errOrdinalTable, o, err)
		}
		var row ordinalRow
		if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &row); err != nil {
			return nil, fmt.Errorf("%w: decode row %d: %v", errOrdinalTable, o, err)
		}
		if row.Ordinal != o {
			return nil, fmt.Errorf("%w: expected row %d, found %d", errOrdinalTable, o, row.Ordinal)
		}
		out = append(out, row)
	}
	return out, nil
}

// appendOrdinalRowLocked publishes the next ordinal. row.Ordinal and
// row.UserTurn are assigned here from the saved tail.
func (s *ArchiveDayStore) appendOrdinalRowLocked(row ordinalRow) (ordinalRow, error) {
	last, n, ok, err := s.lastOrdinalRowLocked()
	if err != nil {
		return ordinalRow{}, err
	}
	row.Ordinal = n
	row.UserTurn = 0
	if ok {
		row.UserTurn = last.UserTurn
	}
	if row.Role == "user" {
		row.UserTurn++
	}
	line, err := json.Marshal(row)
	if err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: encode row %d: %w", n, err)
	}
	if len(line) > ordinalRowBound {
		return ordinalRow{}, fmt.Errorf("ordinal index: row %d is %d bytes, over the %d-byte bound", n, len(line), ordinalRowBound)
	}
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: create dir: %w", err)
	}
	rows, err := os.OpenFile(s.ordinalRowsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: open rows: %w", err)
	}
	defer rows.Close()
	off, err := rows.Seek(0, io.SeekEnd)
	if err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: seek rows: %w", err)
	}
	if _, err := rows.Write(append(line, '\n')); err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: write row %d: %w", n, err)
	}
	if err := rows.Sync(); err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: fsync rows: %w", err)
	}
	offs, err := os.OpenFile(s.ordinalOffsetsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: open offsets: %w", err)
	}
	defer offs.Close()
	var buf [ordinalOffsetSize]byte
	binary.BigEndian.PutUint64(buf[:], uint64(off))
	if _, err := offs.Write(buf[:]); err != nil {
		return ordinalRow{}, fmt.Errorf("ordinal index: write offset %d: %w", n, err)
	}
	return row, nil
}

// holdsModelSlot reports whether rec takes a model ordinal when appended: a
// payload whose membership is model or both, or a model_ref placement. A
// chat-only payload (a saved, not yet consumed input) does not.
func (r ArchiveRecord) holdsModelSlot() bool {
	if r.ModelRef != nil {
		return true
	}
	return r.ModelMessage != nil && r.ViewMembership != ViewMembershipChat
}

// AppendIndexed appends rec and, when it takes a model slot, publishes its
// ordinal row. The archive append comes first: a crash before the row leaves a
// retained-but-excluded record that no window can reach (never a published
// slot without content). It returns the record address and, for a model slot,
// its published row (nil otherwise).
func (s *ArchiveDayStore) AppendIndexed(rec ArchiveRecord) (ArchiveAddress, *ordinalRow, error) {
	var src *ArchiveRecord
	if rec.ModelRef != nil {
		ref, ok := rec.ReferenceAddress()
		if !ok {
			return ArchiveAddress{}, nil, errors.New("archive: model_ref without an address")
		}
		resolved, err := s.ReadAt(ref)
		if err != nil {
			return ArchiveAddress{}, nil, fmt.Errorf("archive: resolve model_ref source: %w", err)
		}
		if resolved.ModelMessage == nil {
			return ArchiveAddress{}, nil, fmt.Errorf("archive: model_ref source %s carries no model payload", ref.EntryID)
		}
		src = &resolved
	}
	line, err := encodeArchiveLine(rec)
	if err != nil {
		return ArchiveAddress{}, nil, err
	}
	// One lock spans the archive append and the row, so concurrent appenders
	// can never publish ordinals in an order that differs from archive order.
	s.mu.Lock()
	defer s.mu.Unlock()
	addr, err := s.appendLineLocked(rec.ID, line)
	if err != nil {
		return ArchiveAddress{}, nil, err
	}
	if !rec.holdsModelSlot() {
		return addr, nil, nil
	}
	row := ordinalRow{Slot: addr}
	payload := rec.ModelMessage
	ts := rec.Timestamp
	if src != nil {
		ref, _ := rec.ReferenceAddress()
		row.Source = &ref
		payload = src.ModelMessage
		ts = src.Timestamp
	}
	row.Role = payload.Role
	row.ToolCallID = payload.ToolCallID
	for _, tc := range payload.ToolCalls {
		if tc.ID != "" {
			row.CallIDs = append(row.CallIDs, tc.ID)
		}
	}
	if !ts.IsZero() {
		row.TS = ts.Unix()
	}
	published, err := s.appendOrdinalRowLocked(row)
	if err != nil {
		return addr, nil, err
	}
	return addr, &published, nil
}
