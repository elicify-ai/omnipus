// Omnipus — ADR-068 D24 / spec FR-131, FR-133: what stat() contributes to the
// properties index.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `file.mtime`, `file.ctime` and `file.size` are three of FR-130's thirteen
// virtual properties, and all three are FACTS ABOUT THE FILE rather than facts
// about its contents. Nothing in the note's bytes can produce them, so they are
// stat()ed at index time and stored on the `notes` row (FR-131).
//
// FR-133 governs the one of the three that has a wrong answer readily
// available. `file.ctime` is the file's BIRTH time — the moment the file was
// created. POSIX's `st_ctime` is NOT that: it is the inode CHANGE time, which
// moves every time a permission bit or a link count changes and is therefore
// routinely LATER than the modification time. The two share a letter and a
// colloquial name, and substituting one for the other is the kind of plausible
// wrong answer this design exists to remove.
//
// So: where the platform records a birth time, that is `file.ctime`. Where it
// does not, `file.ctime` is ABSENT — flagged under FR-007's rules, reported as
// unknown, and never quietly filled in from st_ctime.
// ---------------------------------------------------------------------------

// FileMeta is what stat() reported about one note's file.
//
// Every field is optional in the honest sense: `Known` false means stat() was
// never performed or failed, and the three columns are then NULL rather than
// zero. A zero modification time written as though it were a real one is a note
// that sorts to the beginning of `sort: file.mtime` forever.
type FileMeta struct {
	// Known reports that a stat() actually produced these values. False leaves
	// every metadata column NULL, which reads back as absent.
	Known bool

	// ModTime is the file's last modification time, in UTC.
	ModTime time.Time

	// Size is the file's size in bytes.
	Size int64

	// BirthTime is the file's CREATION time, in UTC, and HasBirthTime reports
	// whether the platform gave one at all.
	//
	// HasBirthTime false is FR-133's honest absence. It is never the POSIX
	// inode-change time: on Linux without statx birth-time support, on any
	// filesystem that does not record one, and on any platform whose stat
	// structure has no birth field, `file.ctime` simply has no value and says
	// so.
	BirthTime    time.Time
	HasBirthTime bool
}

// StatFile reads one note's file metadata.
//
// It returns a zero (Known == false) FileMeta and the error when stat() fails,
// so a caller that chooses to index the note anyway writes NULL metadata rather
// than a fabricated zero time.
func StatFile(path string) (FileMeta, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return FileMeta{}, fmt.Errorf("propindex: stat %q: %w", path, err)
	}
	return statFromInfo(path, fi), nil
}

// StatFromInfo builds the metadata from an os.FileInfo a caller already holds.
//
// The path is still required: on Linux the birth time comes from statx(2),
// which takes a path rather than an already-populated stat structure, and a
// caller that has walked a directory holds both anyway. Passing an empty path
// is legal and simply leaves the birth time absent, which is FR-133's rule
// rather than an error.
func StatFromInfo(path string, fi os.FileInfo) FileMeta {
	if fi == nil {
		return FileMeta{}
	}
	return statFromInfo(path, fi)
}

func statFromInfo(path string, fi os.FileInfo) FileMeta {
	m := FileMeta{
		Known:   true,
		ModTime: fi.ModTime().UTC(),
		Size:    fi.Size(),
	}
	if bt, ok := birthTime(path, fi); ok {
		m.BirthTime, m.HasBirthTime = bt.UTC(), true
	}
	return m
}

// ---------------------------------------------------------------------------
// STORAGE FORM
//
// The three columns follow the same rules as every other value column in this
// index (sqlite.go's SCHEMA comment): a time is strict ISO-8601 TEXT bytes and
// never an epoch integer, a number is its exact decimal DIGITS and never a
// REAL, and both live in BLOB columns so nothing invites SQLite to compare
// them. Ordering `file.mtime` is the Go comparator's job under R-1..R-13, which
// is exactly why the bytes here are stored in a form SQLite will not rank.
// ---------------------------------------------------------------------------

// nanoTimeColumn renders an instant held as Unix nanoseconds for storage, or
// nil for an absent one.
//
// RFC3339 with nanoseconds: whole seconds render without a fraction, so an
// mtime that happens to land on a second boundary is stored in the same shape a
// hand-written date would be, and both parse back through the same layouts
// records.ParseValue accepts.
//
// ZERO IS UNKNOWN, not 1970. A note the indexer never stat'ed has no
// modification time, and writing the epoch for it would put every such note at
// the front of `sort: file.mtime` — a plausible, stable, wrong ordering with no
// error anywhere. The cost is stated rather than hidden: a file whose real
// mtime is exactly 1970-01-01T00:00:00Z is indistinguishable from one that was
// never stat'ed, and it is stored as unknown.
func nanoTimeColumn(nanos int64) any {
	if nanos == 0 {
		return nil
	}
	return []byte(time.Unix(0, nanos).UTC().Format(time.RFC3339Nano))
}

// ctimeColumn renders a birth time for storage, or nil where the platform
// records none.
//
// The FLAG decides, not the number. FR-133's absence is a platform fact, and
// asking "is CtimeNanos zero?" would let a caller who filled the field from
// st_ctime write the very value FR-133 forbids while looking correct.
func ctimeColumn(nanos int64, has bool) any {
	if !has {
		return nil
	}
	return nanoTimeColumn(nanos)
}

// sizeColumn renders a byte count as exact decimal digits, or nil when unknown.
func sizeColumn(size int64, known bool) any {
	if !known {
		return nil
	}
	return []byte(strconv.FormatInt(size, 10))
}

// SetFileMeta copies a stat onto the row's four metadata fields.
//
// It exists so the two shapes cannot drift: FileMeta is what a stat() PRODUCES
// (with a flag on every value that a platform may not have), NoteRows is what
// the store WRITES, and a caller converting between them by hand is a caller
// who will one day forget HasCtime and store a birth time this platform never
// gave.
//
// An unknown stat sets nothing, leaving the row's fields zero and all three
// columns NULL — FR-133's honest absence, applied to all of the metadata rather
// than only to ctime.
func (r *NoteRows) SetFileMeta(m FileMeta) {
	if !m.Known {
		return
	}
	r.Size = m.Size
	if !m.ModTime.IsZero() {
		r.MtimeNanos = m.ModTime.UnixNano()
	}
	if m.HasBirthTime && !m.BirthTime.IsZero() {
		r.CtimeNanos, r.HasCtime = m.BirthTime.UnixNano(), true
	}
}

// decodeFileMeta reads the three columns back.
//
// A column that does not parse is treated as ABSENT rather than as a zero
// value, for the same reason FR-021b puts state in its own column: a parse
// failure written into the cell reserved for a real instant is a wrong answer
// with no error channel, and this index would rather say it does not know.
func decodeFileMeta(mtime, ctime, size []byte) FileMeta {
	var m FileMeta
	if len(mtime) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, string(mtime)); err == nil {
			m.Known, m.ModTime = true, t.UTC()
		}
	}
	if len(size) > 0 {
		if n, err := strconv.ParseInt(string(size), 10, 64); err == nil {
			m.Known, m.Size = true, n
		}
	}
	if len(ctime) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, string(ctime)); err == nil {
			m.Known, m.BirthTime, m.HasBirthTime = true, t.UTC(), true
		}
	}
	return m
}
