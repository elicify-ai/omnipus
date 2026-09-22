// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-090 visual-file-reading reader contract tests
// (spec datasets A/B rows not already covered by read_image_test.go and
// read_image_reader_matrix_test.go: pixel boundaries P-1/P/P+1 through both
// registered readers, counting-reader bounded acquisition, cancellation
// between bounded reads, authorized-handle snapshots under path replacement,
// access/audit/metadata privacy for images, and the unchanged
// text/document/SVG/binary reading contracts).
//
// The oracles here are independent of the code under test: fixtures are
// encoded with the standard library, expected windows/dimensions are computed
// from the fixture inputs, and expected consumption bounds come from the
// ADR-090 M+1 rule rather than from whatever the reader happens to read.
package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// --- fixture builders -------------------------------------------------------

// writePalettedPNGFile encodes a constant-color paletted PNG of the exact
// given dimensions. One byte per decoded pixel keeps the P-boundary fixtures
// at ~16 MiB decoded instead of ~64 MiB for RGBA, per the spec's guidance to
// keep full-decode boundary runs affordable without lowering the limit.
func writePalettedPNGFile(path string, width, height int) error {
	palette := color.Palette{color.RGBA{R: 0x2A, G: 0x6E, B: 0xF4, A: 0xFF}}
	img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	return enc.Encode(f, img)
}

// headerOnlyPNGBytes builds a PNG signature plus one IHDR chunk advertising
// the given dimensions, with no image data. A file like this proves a
// dimension refusal happened before full decode: the header decodes, the
// pixels do not exist.
func headerOnlyPNGBytes(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&buf, binary.BigEndian, uint32(13))
	buf.WriteString("IHDR")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8], ihdr[9] = 8, 6
	buf.Write(ihdr)
	_ = binary.Write(&buf, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	return buf.Bytes()
}

// encodeGradientJPEG encodes a JPEG whose bytes exceed the 512-byte sniff
// window, so the authorized-handle fixture exercises a real tail read. The
// gradient defeats JPEG's run-length compression enough for that.
func encodeGradientJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: uint8((x + y) * 7), A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makePptxBytes synthesizes a minimal valid .pptx (OOXML zip) with one slide
// of text, routed through the OOXML extractor by the presentation content
// type in [Content_Types].xml (the signature docextract's detectOOXML keys
// on).
func makePptxBytes(t *testing.T, slideText string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	contentTypes := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>` +
		`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>` +
		`</Types>`
	ct, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, werr := ct.Write([]byte(contentTypes)); werr != nil {
		t.Fatal(werr)
	}
	slide := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody><a:p><a:r><a:t>` + slideText + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	w, err := zw.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, werr := w.Write([]byte(slide)); werr != nil {
		t.Fatal(werr)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makePDFBytes assembles a one-page PDF with a correct xref table so the real
// PDF text extractor (ledongthuc/pdf via docextract) can read its text.
func makePDFBytes(t *testing.T, lines ...string) []byte {
	t.Helper()
	var content strings.Builder
	content.WriteString("BT /F1 12 Tf 72 720 Td 14 TL\n")
	for i, line := range lines {
		if i == 0 {
			fmt.Fprintf(&content, "(%s) Tj\n", line)
		} else {
			fmt.Fprintf(&content, "T* (%s) Tj\n", line)
		}
	}
	content.WriteString("ET")
	stream := content.String()

	var b strings.Builder
	offsets := make(map[int]int)
	writeObj := func(num int, body string) {
		offsets[num] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", num, body)
	}
	b.WriteString("%PDF-1.4\n")
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	writeObj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return []byte(b.String())
}

// --- authorized-handle fakes -------------------------------------------------

// fakeRegularInfo is the minimal regular-file fs.FileInfo the inspection
// reader consults (Mode().IsRegular, Size).
type fakeRegularInfo struct{ size int64 }

func (fakeRegularInfo) Name() string       { return "fixture" }
func (i fakeRegularInfo) Size() int64      { return i.size }
func (fakeRegularInfo) Mode() fs.FileMode  { return 0o600 }
func (fakeRegularInfo) ModTime() time.Time { return time.Time{} }
func (fakeRegularInfo) IsDir() bool        { return false }
func (fakeRegularInfo) Sys() any           { return nil }

// countingTailFile stands in for the already-authorized regular file at the
// point inspectionImageResult receives it: the sniff prefix has been consumed
// by the caller, and only tail reads reach this handle. data == nil means the
// source "grew" — reads yield filler forever. onRead, when set, fires at the
// top of every Read so a test can act between bounded reads.
type countingTailFile struct {
	data     []byte
	statSize int64
	count    int64
	onRead   func()
}

func (f *countingTailFile) Read(p []byte) (int, error) {
	if f.onRead != nil {
		f.onRead()
	}
	if f.data == nil {
		for i := range p {
			p[i] = 'A'
		}
		f.count += int64(len(p))
		return len(p), nil
	}
	if len(f.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	f.count += int64(n)
	return n, nil
}

func (f *countingTailFile) Stat() (fs.FileInfo, error) { return fakeRegularInfo{size: f.statSize}, nil }
func (f *countingTailFile) Close() error               { return nil }

// pngSniff returns n bytes that begin with the PNG signature, mirroring the
// caller-side sniff the reader receives.
func pngSniff(n int) []byte {
	s := make([]byte, n)
	copy(s, "\x89PNG\r\n\x1a\n")
	for i := 8; i < n; i++ {
		s[i] = 'B'
	}
	return s
}

func noopReauthorize(context.Context) error { return nil }

// sha256Hex is the test's own digest helper, independent of the reader.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// --- pixel boundaries (spec dataset B5/B6, SC-004) ---------------------------

// TestReadImage_PixelBoundaries_BothReaders pins the exact P boundary from
// ADR-090 (16 Mi pixels): P-1 (4095x4097) and P (4096x4096) decode and
// return the fixture's independently known dimensions; P+1 (4096x4097) and
// 65535x65535 are refused by their advertised header alone, through BOTH
// registered readers.
func TestReadImage_PixelBoundaries_BothReaders(t *testing.T) {
	const p = MaxInspectionImagePixels
	if p != 16*1024*1024 {
		t.Fatalf("pixel limit = %d, want 16 Mi pixels", p)
	}
	for _, library := range []bool{false, true} {
		readerName := "read_file"
		if library {
			readerName = "library_read"
		}
		t.Run(readerName, func(t *testing.T) {
			root := t.TempDir()
			dir := root
			if library {
				dir = filepath.Join(root, ".library")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := writePalettedPNGFile(filepath.Join(dir, "p_minus_1.png"), 4095, 4097); err != nil {
				t.Fatal(err)
			}
			if err := writePalettedPNGFile(filepath.Join(dir, "p_exact.png"), 4096, 4096); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "p_plus_1.png"), headerOnlyPNGBytes(t, 4096, 4097), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "overflow.png"), headerOnlyPNGBytes(t, 65535, 65535), 0o600); err != nil {
				t.Fatal(err)
			}
			execute := func(name string) *ToolResult {
				if library {
					return NewLibraryReadTool(root, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": name})
				}
				return NewReadFileTool(root, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": name})
			}
			for _, tc := range []struct {
				name    string
				wantErr string
				width   int
				height  int
			}{
				{name: "p_minus_1.png", width: 4095, height: 4097},
				{name: "p_exact.png", width: 4096, height: 4096},
				{name: "p_plus_1.png", wantErr: "image exceeds pixel limit"},
				{name: "overflow.png", wantErr: "image exceeds pixel limit"},
			} {
				result := execute(tc.name)
				if tc.wantErr != "" {
					if !result.IsError || !strings.Contains(result.ForLLM, tc.wantErr) || len(result.InspectionImages) != 0 {
						t.Fatalf("%s: %#v", tc.name, result)
					}
					continue
				}
				if result.IsError || len(result.InspectionImages) != 1 {
					t.Fatalf("%s: %#v", tc.name, result)
				}
				got := result.InspectionImages[0]
				// bytes, not read from removed struct fields.
				decoded, _, decodeErr := image.DecodeConfig(bytes.NewReader(got.Bytes))
				if decodeErr != nil || decoded.Width != tc.width || decoded.Height != tc.height {
					t.Fatalf("%s: decoded dimensions = %dx%d (err %v), want %dx%d", tc.name, decoded.Width, decoded.Height, decodeErr, tc.width, tc.height)
				}
				if !strings.Contains(result.ForLLM, fmt.Sprintf("original: %dx%d", tc.width, tc.height)) {
					t.Fatalf("%s: marker lacks dimensions: %q", tc.name, result.ForLLM)
				}
			}
		})
	}
}

// --- bounded acquisition (spec dataset B4, SC-004) ---------------------------

// TestReadImage_CountingReaderStopsAtLimitPlusOne proves the M+1 acquisition
// bound with an independent counter: the fake stat reports a size within the
// limit (a file that later "grows"), the stream yields bytes forever, and the
// reader must stop after exactly maxBytes+1 source bytes counting the sniff
// the caller already consumed — and refuse the rest.
func TestReadImage_CountingReaderStopsAtLimitPlusOne(t *testing.T) {
	const maxBytes = int64(4096)
	const sniffN = 512
	file := &countingTailFile{data: nil, statSize: 3000} // stat under limit, endless tail
	result, handled := inspectionImageResult(context.Background(), file, "growing.png", pngSniff(sniffN), false, maxBytes, noopReauthorize)
	if !handled || !result.IsError || !strings.Contains(result.ForLLM, "image exceeds byte limit") {
		t.Fatalf("growing source: handled=%v result=%#v", handled, result)
	}
	if len(result.InspectionImages) != 0 {
		t.Fatalf("oversized source returned an image: %#v", result)
	}
	if got, want := file.count+sniffN, maxBytes+1; got != want {
		t.Fatalf("consumed %d source bytes (sniff %d + tail %d), want exactly %d", got, sniffN, file.count, want)
	}
}

// TestReadImage_CancellationBetweenBoundedReads cancels the context after the
// first tail read and asserts the interrupted outcome: a context error, no
// image content, and acquisition still bounded by M+1 (spec dataset B16).
//
// The reader's countingTailFile never blocks on real I/O (it synthesizes
// bytes in a tight loop), so a buffered, non-blocking "first read started"
// signal is not enough to guarantee the test's cancel() actually runs before
// the read goroutine finishes: under GOMAXPROCS=1 in particular, a goroutine
// that never yields can run io.ReadAll to completion — including the final,
// in-limit chunk that ends in a clean io.EOF — before the scheduler ever
// gives the main goroutine a turn to call cancel(). That raced the assertion
// non-deterministically. To make "between bounded reads" a real happens-
// before relationship instead of a scheduling gamble, onRead blocks on
// resumeRead after signaling firstRead, and the test only closes
// resumeRead after cancel() has returned — so the context is guaranteed
// canceled before the read (and thus the rest of the bounded-read loop) is
// allowed to proceed.
func TestReadImage_CancellationBetweenBoundedReads(t *testing.T) {
	const maxBytes = int64(1 << 20)
	const sniffN = 512
	firstRead := make(chan struct{})
	resumeRead := make(chan struct{})
	var signaled sync.Once
	file := &countingTailFile{
		data:     nil,
		statSize: 4096,
		onRead: func() {
			signaled.Do(func() {
				close(firstRead)
				<-resumeRead
			})
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *ToolResult, 1)
	go func() {
		result, handled := inspectionImageResult(ctx, file, "canceled.png", pngSniff(sniffN), false, maxBytes, noopReauthorize)
		if !handled {
			done <- nil
			return
		}
		done <- result
	}()
	<-firstRead // the first tail read has begun and is blocked in onRead
	cancel()    // guaranteed to complete before the read is allowed to resume
	close(resumeRead)
	result := <-done
	if result == nil {
		t.Fatal("image branch did not handle a canceled regular-file read")
	}
	if !result.IsError || !strings.Contains(result.ForLLM, "context canceled") {
		t.Fatalf("canceled read: %#v", result)
	}
	if len(result.InspectionImages) != 0 {
		t.Fatalf("canceled read returned image content: %#v", result)
	}
	if got, bound := file.count+sniffN, maxBytes+1; got > bound {
		t.Fatalf("canceled read consumed %d source bytes, bound %d", got, bound)
	}
}

// --- authorized handle, not the raw path (spec BDD-06, dataset B12) ----------

// TestReadImage_UsesAuthorizedHandleNeverSourcePath feeds the reader a handle
// holding JPEG A while a DIFFERENT JPEG B sits at the source path. The
// snapshot must come from the authorized handle: A's bytes, dimensions and
// digest. Any raw-path reopen (the prohibited "reuse a raw filename to bypass
// an already authorized handle") returns B and fails this test.
func TestReadImage_UsesAuthorizedHandleNeverSourcePath(t *testing.T) {
	authorized := encodeGradientJPEG(t, 128, 96)
	if len(authorized) <= 512 {
		t.Fatalf("authorized fixture is %d bytes; need > 512 so a tail read happens", len(authorized))
	}
	sniff := authorized[:512]
	tail := append([]byte(nil), authorized[512:]...)

	dir := t.TempDir()
	replacementPath := filepath.Join(dir, "replaced.jpg")
	var different bytes.Buffer
	solid := image.NewRGBA(image.Rect(0, 0, 31, 17))
	for y := 0; y < 17; y++ {
		for x := 0; x < 31; x++ {
			solid.Set(x, y, color.RGBA{R: 0xEE, G: 0x30, B: 0x11, A: 0xFF})
		}
	}
	if err := jpeg.Encode(&different, solid, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	replacement := different.Bytes()
	if err := os.WriteFile(replacementPath, replacement, 0o600); err != nil {
		t.Fatal(err)
	}

	file := &countingTailFile{data: tail, statSize: int64(len(authorized))}
	result, handled := inspectionImageResult(context.Background(), file, replacementPath, sniff, false, int64(len(authorized))+4096, noopReauthorize)
	if !handled || result.IsError {
		t.Fatalf("authorized-handle read: handled=%v result=%#v", handled, result)
	}
	if len(result.InspectionImages) != 1 {
		t.Fatalf("inspection images = %d, want 1", len(result.InspectionImages))
	}
	got := result.InspectionImages[0]
	if !bytes.Equal(got.Bytes, authorized) {
		t.Fatal("snapshot is not the authorized handle's content (raw-path reopen?)")
	}
	if bytes.Equal(got.Bytes, replacement) {
		t.Fatal("snapshot came from the source path, not the authorized handle")
	}
	// re-derived from the snapshot bytes and checked against the durable
	// marker, replacing the removed struct fields.
	decoded, _, decodeErr := image.DecodeConfig(bytes.NewReader(got.Bytes))
	if decodeErr != nil || decoded.Width != 128 || decoded.Height != 96 {
		t.Fatalf("decoded dimensions = %dx%d (err %v), want 128x96", decoded.Width, decoded.Height, decodeErr)
	}
	if want := sha256Hex(authorized); sha256Hex(got.Bytes) != want {
		t.Fatalf("snapshot digest = %s, want the authorized handle's %s", sha256Hex(got.Bytes), want)
	}
	if !strings.Contains(result.ForLLM, "sha256: "+sha256Hex(authorized)) {
		t.Fatalf("durable marker lacks the authorized handle's digest: %q", result.ForLLM)
	}
}

// TestReadImage_HandleSnapshotStableAcrossPathReplacement runs the swap at
// the Execute level: the first read's snapshot reflects the file as
// authorized; replacing the path changes only LATER reads, which must see the
// current content (no path-keyed caching), and a replaced symlink is refused
// for a read-confined turn by the existing scope rule (spec BDD-06/B13).
func TestReadImage_HandleSnapshotStableAcrossPathReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMNIPUS_HOME", root)
	// Keep the workspace a subdirectory of the install home: OMNIPUS_HOME
	// equal to the work dir would put the master.key carve-out root inside
	// the workspace, and fspolicy rightly refuses to operate there.
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(ws, true, MaxReadFileSize)
	path := filepath.Join(ws, "swapped.png")

	first := writeKnownPNG(t, path, 21, 9)
	firstResult := tool.Execute(context.Background(), map[string]any{"path": path})
	if firstResult.IsError || len(firstResult.InspectionImages) != 1 {
		t.Fatalf("first read: %#v", firstResult)
	}
	firstGot := firstResult.InspectionImages[0]
	// checked in the durable marker, replacing the removed SHA256 field.
	if !bytes.Equal(firstGot.Bytes, first) || sha256Hex(firstGot.Bytes) != sha256Hex(first) {
		t.Fatal("first snapshot does not match the authorized fixture")
	}
	if !strings.Contains(firstResult.ForLLM, "sha256: "+sha256Hex(first)) {
		t.Fatalf("first durable marker lacks the fixture digest: %q", firstResult.ForLLM)
	}

	// Replace the pathname with different valid image content.
	var second bytes.Buffer
	secondImg := image.NewRGBA(image.Rect(0, 0, 9, 21))
	for y := 0; y < 21; y++ {
		for x := 0; x < 9; x++ {
			secondImg.Set(x, y, color.RGBA{R: 0x11, G: 0xBB, B: 0x99, A: 0xFF})
		}
	}
	if err := png.Encode(&second, secondImg); err != nil {
		t.Fatal(err)
	}
	secondPNG := second.Bytes()
	if err := os.WriteFile(path, secondPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	// bytes instead of reading a cached digest field.
	if sha256Hex(firstGot.Bytes) != sha256Hex(first) {
		t.Fatal("the already-captured snapshot mutated after path replacement")
	}
	secondResult := tool.Execute(context.Background(), map[string]any{"path": path})
	if secondResult.IsError || len(secondResult.InspectionImages) != 1 {
		t.Fatalf("fresh read after replacement: %#v", secondResult)
	}
	secondGot := secondResult.InspectionImages[0]
	secondCfg, _, secondDecodeErr := image.DecodeConfig(bytes.NewReader(secondGot.Bytes))
	if !bytes.Equal(secondGot.Bytes, secondPNG) || secondDecodeErr != nil || secondCfg.Width != 9 || secondCfg.Height != 21 {
		t.Fatalf("fresh read did not return the current content: decoded %dx%d (err %v)", secondCfg.Width, secondCfg.Height, secondDecodeErr)
	}

	// Replace with a symlink out of scope; a confined (Judge posture) turn
	// must get the existing access refusal, not the image.
	outside := filepath.Join(root, "outside.png")
	if err := os.WriteFile(outside, first, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	confined := WithReadConfined(context.Background(), true)
	denied := tool.Execute(confined, map[string]any{"path": path})
	if !denied.IsError || len(denied.InspectionImages) != 0 {
		t.Fatalf("confined fresh read after symlink swap: %#v", denied)
	}
	if !strings.Contains(denied.ForLLM, refusalMarkerOutsideScopeADR084) {
		t.Fatalf("confined fresh read lacks the scope refusal marker: %q", denied.ForLLM)
	}
	if strings.Contains(denied.ForLLM, hex.EncodeToString(sha256.New().Sum(nil))) {
		t.Fatal("refusal leaks digest-like content")
	}
}

// --- access, audit and metadata privacy (spec BDD-04/05, datasets B7-B11) ----

// TestReadImage_AccessAuditAndMetadataPrivacy runs a Judge-posture turn with
// a real audit logger over four image requests: an in-scope evidence image
// (allowed, audited as a correlated read, zero delivery), an out-of-scope
// image (refused, denial audited, nothing disclosed), a missing image
// (existing not-found marker), and PNG bytes hidden under a metadata filename
// (the metadata guard fires before any sniff, so no image inspection).
func TestReadImage_AccessAuditAndMetadataPrivacy(t *testing.T) {
	tr := newRefusalTextTree(t)
	t.Setenv("OMNIPUS_HOME", tr.home)

	inside := filepath.Join(tr.workDir, "evidence.png")
	writeKnownPNG(t, inside, 24, 12)
	outside := filepath.Join(filepath.Dir(tr.outsidePath), "leak.png")
	writeKnownPNG(t, outside, 8, 8)
	soul := filepath.Join(tr.workDir, "SOUL.md")
	if err := os.WriteFile(soul, headerOnlyPNGBytes(t, 4, 4), 0o600); err != nil {
		t.Fatal(err)
	}

	const judgeAgentID = "judge-images"
	const adjudicationID = "adj-img-7"
	const sessionID = "sess-img"
	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	tool := NewReadFileTool(tr.workDir, true, MaxReadFileSize)
	tool.SetAuditLogger(auditLogger)
	ctx := WithAgentID(context.Background(), judgeAgentID)
	ctx = WithTranscriptSessionID(ctx, sessionID)
	ctx = WithReadConfined(ctx, true)
	ctx = WithVerifierAdjudicationID(ctx, adjudicationID)

	allowed := tool.Execute(ctx, map[string]any{"path": inside})
	if allowed.IsError || len(allowed.InspectionImages) != 1 {
		t.Fatalf("in-scope image: %#v", allowed)
	}
	if len(allowed.Media) != 0 || allowed.ForUser != "" {
		t.Fatalf("inspection read produced delivery: media=%v user=%q", allowed.Media, allowed.ForUser)
	}

	refused := tool.Execute(ctx, map[string]any{"path": outside})
	if !refused.IsError || len(refused.InspectionImages) != 0 {
		t.Fatalf("out-of-scope image: %#v", refused)
	}
	if !strings.Contains(refused.ForLLM, refusalMarkerOutsideScopeADR084) {
		t.Fatalf("out-of-scope refusal text: %q", refused.ForLLM)
	}
	outsidePNG, rerr := os.ReadFile(outside)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if bytes.Contains([]byte(refused.ForLLM), outsidePNG[:min(32, len(outsidePNG))]) {
		t.Fatal("refusal disclosed out-of-scope image bytes")
	}

	missing := tool.Execute(ctx, map[string]any{"path": tr.missingPath + ".png"})
	if !missing.IsError || len(missing.InspectionImages) != 0 || !strings.Contains(missing.ForLLM, notFoundMarkerADR084) {
		t.Fatalf("missing image: %#v", missing)
	}

	guarded := tool.Execute(ctx, map[string]any{"path": soul})
	if !guarded.IsError || len(guarded.InspectionImages) != 0 {
		t.Fatalf("metadata-hidden image was not refused: %#v", guarded)
	}
	if !strings.Contains(guarded.ForLLM, "read_agent_metadata") {
		t.Fatalf("metadata guard text: %q", guarded.ForLLM)
	}

	rows := readAuditRows(t, auditLogger, auditDir)
	var reads, denials []auditRow
	for _, row := range rows {
		switch {
		case row.Event == audit.EventFileOp && row.detail("op") == "read":
			reads = append(reads, row)
		case row.Event == PathAccessDeniedEvent:
			denials = append(denials, row)
		}
	}
	if len(reads) != 1 {
		t.Fatalf("want exactly 1 image read audit row, got %d (all rows: %+v)", len(reads), rows)
	}
	if reads[0].detail("path") != inside || reads[0].Decision != audit.DecisionAllow || reads[0].Tool != "read_file" {
		t.Fatalf("image read audit row: %+v", reads[0])
	}
	if reads[0].detail("adjudication_id") != adjudicationID || reads[0].AgentID != judgeAgentID || reads[0].SessionID != sessionID {
		t.Fatalf("image read audit correlation: %+v", reads[0])
	}
	var denialNamed bool
	for _, row := range denials {
		if row.detail("path") == outside && row.detail("adjudication_id") == adjudicationID {
			denialNamed = true
		}
	}
	if !denialNamed {
		t.Fatalf("no correlated denial row names the out-of-scope image (rows: %+v)", rows)
	}
}

// --- unchanged reading contracts (spec BDD-03, datasets A7/A13, R1-R5) -------

// TestReadImage_ExistingReadingContracts pins the pre-ADR-090 behavior of the
// same Execute entry point now serving images: text pagination windows, the
// empty-file marker, direct SVG text reads, document extraction (DOCX, XLSX,
// PPTX, PDF), opaque-binary refusal, the 64 KiB text cap, and image
// identification by content rather than filename — each against an
// independently constructed expectation.
func TestReadImage_ExistingReadingContracts(t *testing.T) {
	root := t.TempDir()
	tool := NewReadFileTool(root, false, MaxReadFileSize)

	// R1: exact pagination window. The pattern makes off-by-one windows
	// visible: the window must end precisely at the requested boundary.
	text := strings.Repeat("abcdefghij", 10) // 100 chars
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	paged := tool.Execute(context.Background(), map[string]any{"path": "notes.txt", "offset": float64(10), "length": float64(25)})
	if paged.IsError {
		t.Fatalf("paginated text: %s", paged.ForLLM)
	}
	wantWindow := text[10 : 10+25]
	if !strings.HasSuffix(paged.ForLLM, wantWindow) {
		t.Fatalf("text window = %q, want suffix %q", paged.ForLLM, wantWindow)
	}
	if strings.HasSuffix(paged.ForLLM, wantWindow+"f") {
		t.Fatal("text window overshot the requested length")
	}

	// Edge: empty text stays the empty-file marker, not an image or an error.
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	empty := tool.Execute(context.Background(), map[string]any{"path": "empty.txt"})
	if empty.IsError || !strings.Contains(empty.ForLLM, "[END OF FILE - no content at this offset]") || len(empty.InspectionImages) != 0 {
		t.Fatalf("empty text: %#v", empty)
	}

	// A13: direct SVG read stays text (with pagination), never visual.
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 48"><rect x="4" y="4" width="56" height="40" fill="#0af"/></svg>`
	if err := os.WriteFile(filepath.Join(root, "diagram.svg"), []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	svgRead := tool.Execute(context.Background(), map[string]any{"path": "diagram.svg", "offset": float64(0), "length": float64(40)})
	if svgRead.IsError || len(svgRead.InspectionImages) != 0 {
		t.Fatalf("direct SVG read: %#v", svgRead)
	}
	if !strings.HasSuffix(svgRead.ForLLM, svg[:40]) || strings.Contains(svgRead.ForLLM, "[image:") {
		t.Fatalf("direct SVG read lost text semantics: %q", svgRead.ForLLM)
	}

	// A7: identification by bytes, not filename — PNG content under a
	// non-image extension is an image candidate; a NUL-bearing blob under a
	// .bin name is not (R4 contrast, same tree).
	wantImage := writeKnownPNG(t, filepath.Join(root, "asset.bin"), 16, 8)
	byContent := tool.Execute(context.Background(), map[string]any{"path": "asset.bin"})
	if byContent.IsError || len(byContent.InspectionImages) != 1 {
		t.Fatalf("PNG under .bin name: %#v", byContent)
	}
	if got := byContent.InspectionImages[0]; got.MIMEType != "image/png" || !bytes.Equal(got.Bytes, wantImage) {
		t.Fatalf("content-identified image: mime=%s bytes-match=%v", got.MIMEType, bytes.Equal(got.Bytes, wantImage))
	}
	if err := os.WriteFile(filepath.Join(root, "opaque.bin"), []byte{0x00, 0x01, 0x02, 0x00, 0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	opaque := tool.Execute(context.Background(), map[string]any{"path": "opaque.bin"})
	if !opaque.IsError || !strings.Contains(opaque.ForLLM, "binary file detected") {
		t.Fatalf("opaque binary: %#v", opaque)
	}

	// A8: spaces and non-ASCII in a permitted image name change nothing.
	unicodeName := "grafik mit spaces ünïcode ✓.png"
	wantUnicode := writeKnownPNG(t, filepath.Join(root, unicodeName), 12, 6)
	unicodeRead := tool.Execute(context.Background(), map[string]any{"path": unicodeName})
	if unicodeRead.IsError || len(unicodeRead.InspectionImages) != 1 || !bytes.Equal(unicodeRead.InspectionImages[0].Bytes, wantUnicode) {
		t.Fatalf("unicode-named image: %#v", unicodeRead)
	}

	// R3: document extraction unchanged and never visual.
	docs := []struct {
		name string
		data []byte
		mark string
	}{
		{"report.docx", makeDocxBytes(t, "DOCX regression marker"), "DOCX regression marker"},
		{"sheet.xlsx", makeXlsxBytes(t, [][]string{{"XLSX", "marker"}}), "XLSX"},
		{"deck.pptx", makePptxBytes(t, "PPTX regression marker"), "PPTX regression marker"},
		{"doc.pdf", makePDFBytes(t, "PDF regression marker", "second line"), "PDF regression marker"},
	}
	for _, doc := range docs {
		if err := os.WriteFile(filepath.Join(root, doc.name), doc.data, 0o600); err != nil {
			t.Fatal(err)
		}
		got := tool.Execute(context.Background(), map[string]any{"path": doc.name})
		if got.IsError || !strings.Contains(got.ForLLM, doc.mark) {
			t.Fatalf("%s extraction: %s", doc.name, got.ForLLM)
		}
		if len(got.InspectionImages) != 0 {
			t.Fatalf("%s extraction became visual: %#v", doc.name, got)
		}
	}

	// R2: text above the 64 KiB cap is capped with the truncation header.
	big := strings.Repeat("T", 70*1024)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	capped := tool.Execute(context.Background(), map[string]any{"path": "big.txt"})
	if capped.IsError {
		t.Fatalf("capped text: %s", capped.ForLLM)
	}
	if !strings.Contains(capped.ForLLM, "[TRUNCATED") || strings.Contains(capped.ForLLM, "END OF FILE") {
		t.Fatalf("capped text header: %q", capped.ForLLM[:200])
	}
	idx := strings.LastIndex(capped.ForLLM, "\n\n")
	if idx < 0 || capped.ForLLM[idx+2:] != strings.Repeat("T", MaxReadFileSize) {
		t.Fatalf("capped text body length = %d, want exactly %d", len(capped.ForLLM)-(idx+2), MaxReadFileSize)
	}
}

// TestLibraryRead_ExistingContractsKeepSharingTheReader pins the library
// wrapper's delegation for the non-image halves of the contract: document
// extraction and text pagination through library_read, whose image path the
// matrix test already covers for both readers.
func TestLibraryRead_ExistingContractsKeepSharingTheReader(t *testing.T) {
	root := t.TempDir()
	library := filepath.Join(root, ".library")
	if err := os.MkdirAll(library, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "report.docx"), makeDocxBytes(t, "Library DOCX marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewLibraryReadTool(root, false, MaxReadFileSize)
	got := tool.Execute(context.Background(), map[string]any{"path": "report.docx"})
	if got.IsError || !strings.Contains(got.ForLLM, "Library DOCX marker") || len(got.InspectionImages) != 0 {
		t.Fatalf("library docx: %s", got.ForLLM)
	}

	text := strings.Repeat("0123456789", 10)
	if err := os.WriteFile(filepath.Join(library, "page.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	paged := tool.Execute(context.Background(), map[string]any{"path": "page.txt", "offset": float64(5), "length": float64(10)})
	if paged.IsError || !strings.HasSuffix(paged.ForLLM, text[5:15]) {
		t.Fatalf("library text pagination: %q", paged.ForLLM)
	}
}
