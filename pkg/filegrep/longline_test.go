// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"bufio"
	"io"
	"strings"
	"testing"
)

// repeatReader is an io.Reader that emits `remaining` copies of byte `b`,
// generating content ON THE FLY rather than from a pre-allocated backing
// buffer. It never emits a '\n'. This lets a test exercise a pathologically
// long line (tens of MiB) without the TEST ITSELF having to allocate a
// same-sized fixture up front — the allocation under scrutiny is
// readBoundedLine's, not this generator's.
type repeatReader struct {
	remaining int64
	b         byte
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = r.b
	}
	r.remaining -= int64(n)
	return n, nil
}

// TestReadBoundedLine_BoundsMemoryOnLongLine pins review finding C1 at the
// unit level: readBoundedLine must never retain more than the caller's
// maxLineBytes bound, no matter how much further away the real '\n' (or EOF)
// actually is. The source here is a 50 MiB line with NO newline anywhere —
// exactly the "single-line JSON export / minified bundle / base64 blob /
// CR-only CSV" shape the review named — generated on the fly by
// repeatReader so this test's own memory footprint stays tiny regardless of
// how large `totalSize` is set.
//
// This test has teeth: reverting readBoundedLine to the old
// br.ReadBytes('\n')-equivalent behavior (accumulate without limit until
// the delimiter or EOF) makes `len(line) > maxLine` fail immediately, since
// line would be the full 50 MiB rather than <= maxLine.
func TestReadBoundedLine_BoundsMemoryOnLongLine(t *testing.T) {
	const maxLine = 8 << 10    // 8 KiB bound, deliberately tiny for a fast test
	const totalSize = 50 << 20 // 50 MiB "line" — far larger than maxLine
	src := &repeatReader{remaining: totalSize, b: 'a'}
	br := bufio.NewReaderSize(src, 64<<10)

	line, consumed, tooLong, err := readBoundedLine(br, maxLine)

	if !tooLong {
		t.Fatalf("tooLong = false, want true for a %d-byte line against an %d-byte bound", totalSize, maxLine)
	}
	if len(line) > maxLine {
		t.Fatalf("readBoundedLine retained %d bytes, want <= %d — this is the whole point of "+
			"the bound: memory must be capped BEFORE the line finishes, not after", len(line), maxLine)
	}
	if int64(len(line)) != maxLine {
		t.Errorf("len(line) = %d, want exactly %d (the retained prefix should fill the bound, "+
			"not stop short of it)", len(line), maxLine)
	}
	if consumed != totalSize {
		t.Errorf("consumed = %d, want %d — even though most of the line's bytes were discarded "+
			"rather than retained, they were still read off the underlying reader and must be "+
			"charged to BytesScanned honestly", consumed, totalSize)
	}
	if err != io.EOF {
		t.Errorf("err = %v, want io.EOF (the source ends without ever emitting '\\n')", err)
	}
	for i, c := range line {
		if c != 'a' {
			t.Fatalf("line[%d] = %q, want 'a' — the retained prefix must be the file's real "+
				"leading bytes, not garbage", i, c)
		}
	}
}

// TestReadBoundedLine_OrdinaryShortLineUnaffected is the negative case: a
// line well under the bound must come back byte-for-byte identical to what
// bufio.Reader.ReadBytes('\n') would have returned, delimiter included.
func TestReadBoundedLine_OrdinaryShortLineUnaffected(t *testing.T) {
	src := strings.NewReader("hello world\nsecond line\n")
	br := bufio.NewReaderSize(src, 64<<10)

	line, consumed, tooLong, err := readBoundedLine(br, 1<<20)
	if tooLong {
		t.Fatal("tooLong = true for an ordinary short line")
	}
	if err != nil {
		t.Fatalf("err = %v, want nil (delimiter found)", err)
	}
	if string(line) != "hello world\n" {
		t.Fatalf("line = %q, want %q", line, "hello world\n")
	}
	if consumed != int64(len(line)) {
		t.Fatalf("consumed = %d, want %d (nothing discarded on a short line)", consumed, len(line))
	}

	line2, _, tooLong2, err2 := readBoundedLine(br, 1<<20)
	if tooLong2 || err2 != nil || string(line2) != "second line\n" {
		t.Fatalf("second readBoundedLine call = (%q, tooLong=%v, err=%v), want (%q, false, nil)",
			line2, tooLong2, err2, "second line\n")
	}

	_, _, _, err3 := readBoundedLine(br, 1<<20)
	if err3 != io.EOF {
		t.Fatalf("third call err = %v, want io.EOF", err3)
	}
}

// TestFileGrep_LongLineWithoutNewlineIsSkippedNotBallooned pins review
// finding C1 at the public Search() level: a file consisting of one huge
// line with no newline anywhere (a single-line JSON export is the review's
// own example) must be recognized as a bounded, counted skip — never a
// silent, unbounded read.
//
// This test has teeth: with readBoundedLine reverted to the old unbounded
// br.ReadBytes('\n') behavior, this file's one "line" is read to EOF as a
// single 6 MiB allocation, fileBytes then exceeds PerFileContentCap (4 MiB)
// via the EXISTING FilesSkippedFileCap path — so FilesSkippedLongLine stays
// 0 and this assertion fails, even though the file is still (eventually,
// after the full read) skipped. The new counter existing and firing is
// exactly the proof that the bound fired BEFORE the whole line was
// allocated, not after.
func TestFileGrep_LongLineWithoutNewlineIsSkippedNotBallooned(t *testing.T) {
	body := strings.Repeat("a", 6<<20) // 6 MiB, no newline anywhere — bigger than PerFileContentCap (4 MiB)
	fsys := buildFS(map[string]string{"huge.txt": body})

	res := mustSearch(t, oneRoot(fsys), Options{Query: "nomatch"})

	if res.Stats.FilesSkippedLongLine != 1 {
		t.Fatalf("Stats.FilesSkippedLongLine = %d, want 1 (huge.txt's single line exceeds the "+
			"per-line bound); full stats: %+v", res.Stats.FilesSkippedLongLine, res.Stats)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("want zero hits (no match, and the pathological line was never scanned for one), got %+v", res.Hits)
	}
	if res.Truncated {
		t.Fatalf("a per-file skip must not be a request-level truncation, got reason=%v", res.TruncatedReason)
	}
	// The file was still reached and name-checked, exactly like every other
	// skip category in this package (FilesSkippedBinary's own convention).
	if res.Stats.FilesVisited != 1 {
		t.Fatalf("Stats.FilesVisited = %d, want 1", res.Stats.FilesVisited)
	}
}

// TestFileGrep_LongLineSkip_KeepsEarlierHitsInSameFile proves the
// "remainder skip" framing is real: matches found on lines BEFORE the
// pathological one are not thrown away just because a later line in the
// same file trips the bound — the same behavior PerFileContentCap's own
// remainder-skip already guarantees.
func TestFileGrep_LongLineSkip_KeepsEarlierHitsInSameFile(t *testing.T) {
	body := "needle appears right here\n" + strings.Repeat("a", 6<<20)
	fsys := buildFS(map[string]string{"mixed.txt": body})

	res := mustSearch(t, oneRoot(fsys), Options{Query: "needle"})

	if res.Stats.FilesSkippedLongLine != 1 {
		t.Fatalf("Stats.FilesSkippedLongLine = %d, want 1", res.Stats.FilesSkippedLongLine)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("want the earlier match kept despite the later pathological line, got %+v", res.Hits)
	}
	if res.Hits[0].Line != 1 || res.Hits[0].Kind != KindContent {
		t.Fatalf("hit = %+v, want line 1 content hit", res.Hits[0])
	}
}

// TestFileGrep_LongLineSkip_DistinctFromPerFileCap proves FilesSkippedLongLine
// and FilesSkippedFileCap are never conflated: a file whose TOTAL bytes
// exceed PerFileContentCap across many ORDINARY short lines (no single line
// anywhere near maxLineBytes) must still report via the existing
// FilesSkippedFileCap counter, not the new one — the new counter means
// specifically "one line alone blew the bound", not "the file overall is
// too big".
func TestFileGrep_LongLineSkip_DistinctFromPerFileCap(t *testing.T) {
	var b strings.Builder
	line := strings.Repeat("x", 100) + "\n"
	for int64(b.Len()) <= PerFileContentCap {
		b.WriteString(line)
	}
	fsys := buildFS(map[string]string{"manylines.txt": b.String()})

	res := mustSearch(t, oneRoot(fsys), Options{Query: "nomatch"})

	if res.Stats.FilesSkippedFileCap != 1 {
		t.Fatalf("Stats.FilesSkippedFileCap = %d, want 1", res.Stats.FilesSkippedFileCap)
	}
	if res.Stats.FilesSkippedLongLine != 0 {
		t.Fatalf("Stats.FilesSkippedLongLine = %d, want 0 — no single line here is anywhere near "+
			"maxLineBytes, only the file's cumulative total is over PerFileContentCap", res.Stats.FilesSkippedLongLine)
	}
}
