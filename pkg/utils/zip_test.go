package utils

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractZipFileWithLimitsEnforcesEntryBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		entryCount int
		wantError  bool
	}{
		{name: "max minus one", entryCount: 1},
		{name: "max", entryCount: 2},
		{name: "max plus one", entryCount: 3, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := writeTestZip(t, tc.entryCount, []byte("x"))
			err := ExtractZipFileWithLimits(archive, t.TempDir(), ZipExtractionLimits{MaxEntries: 2, MaxExpandedBytes: 10})
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "too many entries") {
					t.Fatalf("error=%v, want too many entries", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("boundary archive rejected: %v", err)
			}
		})
	}
}

func TestExtractZipFileWithLimitsEnforcesExpandedByteBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		content   string
		wantError bool
	}{
		{name: "max minus one", content: "1234"},
		{name: "max", content: "12345"},
		{name: "max plus one", content: "123456", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := writeTestZip(t, 1, []byte(tc.content))
			target := t.TempDir()
			err := ExtractZipFileWithLimits(archive, target, ZipExtractionLimits{MaxEntries: 2, MaxExpandedBytes: 5})
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "expanded size") {
					t.Fatalf("error=%v, want expanded size rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("boundary archive rejected: %v", err)
			}
			got, readErr := os.ReadFile(filepath.Join(target, "file-0"))
			if readErr != nil || string(got) != tc.content {
				t.Fatalf("content=%q err=%v, want %q", got, readErr, tc.content)
			}
		})
	}
}

func TestCopyZipEntryEnforcesActualStreamedByteBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		content   string
		wantError bool
	}{
		{name: "max minus one", content: "1234"},
		{name: "max", content: "12345"},
		{name: "max plus one", content: "123456", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst bytes.Buffer
			written, err := copyZipEntry(&dst, strings.NewReader(tc.content), "asset", 5)
			if tc.wantError {
				if err == nil || err.Error() != "ZIP expanded size exceeds configured limit" {
					t.Fatalf("written=%d error=%v", written, err)
				}
				return
			}
			if err != nil || written != int64(len(tc.content)) || dst.String() != tc.content {
				t.Fatalf("written=%d content=%q error=%v", written, dst.String(), err)
			}
		})
	}
}

func TestExtractZipFileWithLimitsRejectsInvalidLimits(t *testing.T) {
	archive := writeTestZip(t, 0, nil)
	for _, limits := range []ZipExtractionLimits{
		{MaxEntries: 0, MaxExpandedBytes: 1},
		{MaxEntries: 1, MaxExpandedBytes: 0},
	} {
		if err := ExtractZipFileWithLimits(archive, t.TempDir(), limits); err == nil || err.Error() != "invalid ZIP extraction limits" {
			t.Fatalf("limits=%+v error=%v", limits, err)
		}
	}
}

func writeTestZip(t *testing.T, entryCount int, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for i := 0; i < entryCount; i++ {
		w, createErr := zw.Create("file-" + string(rune('0'+i)))
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := w.Write(content); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
