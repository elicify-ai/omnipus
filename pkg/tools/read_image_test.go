package tools

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKnownPNG(t *testing.T, path string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 0x31, G: 0x72, B: 0xA5, A: 0xFF})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestReadImage_ReturnsPrivateAuthorizedSnapshot(t *testing.T) {
	dir := t.TempDir()
	wantBytes := writeKnownPNG(t, filepath.Join(dir, "known.png"), 32, 24)
	result := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "known.png"})
	if result.IsError {
		t.Fatalf("read image: %s", result.ForLLM)
	}
	if len(result.InspectionImages) != 1 {
		t.Fatalf("inspection images = %d, want 1", len(result.InspectionImages))
	}
	got := result.InspectionImages[0]
	if got.MIMEType != "image/png" || got.OriginalWidth != 32 || got.OriginalHeight != 24 {
		t.Fatalf("metadata = %#v", got)
	}
	if !bytes.Equal(got.Bytes, wantBytes) {
		t.Fatal("snapshot differs from independently encoded fixture")
	}
	if len(result.Media) != 0 || result.ForUser != "" {
		t.Fatalf("private read became delivery: media=%v user=%q", result.Media, result.ForUser)
	}
	if !strings.Contains(result.ForLLM, "not retained; re-read to view") {
		t.Fatalf("marker = %q", result.ForLLM)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, wantBytes) || bytes.Contains(raw, []byte("inspection_images")) {
		t.Fatalf("durable JSON contains inspection payload: %s", raw)
	}
}

func TestReadImage_ExplicitPaginationIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeKnownPNG(t, filepath.Join(dir, "known.png"), 4, 3)
	for _, args := range []map[string]any{
		{"path": "known.png", "offset": int64(0)},
		{"path": "known.png", "offset": int64(-1)},
		{"path": "known.png", "length": int64(0)},
		{"path": "known.png", "length": int64(1)},
	} {
		result := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), args)
		if !result.IsError || !strings.Contains(result.ForLLM, "image pagination is not supported") || len(result.InspectionImages) != 0 {
			t.Fatalf("args=%v result=%#v", args, result)
		}
	}
}

func TestLibraryReadImage_UsesSameVisualContract(t *testing.T) {
	dir := t.TempDir()
	library := filepath.Join(dir, ".library")
	if err := os.MkdirAll(library, 0o700); err != nil {
		t.Fatal(err)
	}
	writeKnownPNG(t, filepath.Join(library, "chart.png"), 40, 30)
	result := NewLibraryReadTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "chart.png"})
	if result.IsError || len(result.InspectionImages) != 1 {
		t.Fatalf("library image result=%#v", result)
	}
	if got := result.InspectionImages[0]; got.OriginalWidth != 40 || got.OriginalHeight != 30 {
		t.Fatalf("dimensions=%dx%d", got.OriginalWidth, got.OriginalHeight)
	}
}

func TestReadImage_InvalidAndUnsupportedFormatsAreDistinct(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.png"), []byte("\x89PNG\r\n\x1a\ntruncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "photo.heic"), append([]byte{0, 0, 0, 24}, []byte("ftypheic00000000")...), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "broken.png"})
	if !broken.IsError || !strings.Contains(broken.ForLLM, "invalid image") {
		t.Fatalf("broken=%#v", broken)
	}
	unsupported := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "photo.heic"})
	if !unsupported.IsError || !strings.Contains(unsupported.ForLLM, "unsupported image format") {
		t.Fatalf("unsupported=%#v", unsupported)
	}
}

func TestReadImage_InputBudgetsAndRegularFiles(t *testing.T) {
	const specMaxBytes = 20 * 1024 * 1024 // ADR-090 constraint M.
	if MaxInspectionImageBytes != specMaxBytes {
		t.Fatalf("image byte limit = %d, want %d", MaxInspectionImageBytes, specMaxBytes)
	}
	dir := t.TempDir()
	oversized := filepath.Join(dir, "large.png")
	f, err := os.Create(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(specMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	large := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "large.png"})
	if !large.IsError || !strings.Contains(large.ForLLM, "image exceeds byte limit") {
		t.Fatalf("large=%#v", large)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	nonRegular := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "folder.png"})
	if !nonRegular.IsError || !strings.Contains(nonRegular.ForLLM, "image source must be a regular file") {
		t.Fatalf("nonregular=%#v", nonRegular)
	}
}

func TestReadImage_RejectsAdvertisedPixelsBeforeFullDecode(t *testing.T) {
	var pngHeader bytes.Buffer
	pngHeader.WriteString("\x89PNG\r\n\x1a\n")
	_ = binary.Write(&pngHeader, binary.BigEndian, uint32(13))
	pngHeader.WriteString("IHDR")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], 4097)
	binary.BigEndian.PutUint32(ihdr[4:8], 4096)
	ihdr[8], ihdr[9] = 8, 6
	pngHeader.Write(ihdr)
	_ = binary.Write(&pngHeader, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "huge.png"), pngHeader.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	result := NewReadFileTool(dir, true, MaxReadFileSize).Execute(context.Background(), map[string]any{"path": "huge.png"})
	if !result.IsError || !strings.Contains(result.ForLLM, "image exceeds pixel limit") {
		t.Fatalf("result=%#v", result)
	}
}
