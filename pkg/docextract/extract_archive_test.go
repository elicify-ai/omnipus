package docextract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type archiveTestEntry struct {
	name string
	body string
}

func makeTestZIP(t *testing.T, entries ...archiveTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func makeTestTAR(t *testing.T, entries ...archiveTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		body := []byte(entry.body)
		if err := tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func gzipTestBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatalf("write gzip: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func makeMagicDocx(t *testing.T, text string) []byte {
	t.Helper()
	contentTypes := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`
	document := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`
	return makeTestZIP(t,
		archiveTestEntry{name: "[Content_Types].xml", body: contentTypes},
		archiveTestEntry{name: "word/document.xml", body: document},
	)
}

func TestExtractBytes_ZipManifest_TwoEntries(t *testing.T) {
	data := makeTestZIP(t,
		archiveTestEntry{name: "alpha.txt", body: "alpha"},
		archiveTestEntry{name: "dir/beta.txt", body: "beta"},
	)
	text, ok, reason := ExtractBytes(data, "", "files.zip")
	if !ok {
		t.Fatalf("expected manifest, reason=%q", reason)
	}
	for _, want := range []string{"Archive manifest (zip):", "alpha.txt (5 bytes)", "dir/beta.txt (4 bytes)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("manifest %q does not contain %q", text, want)
		}
	}
}

func TestExtractBytes_ZipManifest_PasswordProtected(t *testing.T) {
	data := makeTestZIP(t, archiveTestEntry{name: "secret.txt", body: "secret"})
	for pos := 0; pos+10 <= len(data); pos++ {
		switch binary.LittleEndian.Uint32(data[pos : pos+4]) {
		case 0x04034b50:
			flags := binary.LittleEndian.Uint16(data[pos+6:pos+8]) | 1
			binary.LittleEndian.PutUint16(data[pos+6:pos+8], flags)
		case 0x02014b50:
			flags := binary.LittleEndian.Uint16(data[pos+8:pos+10]) | 1
			binary.LittleEndian.PutUint16(data[pos+8:pos+10], flags)
		}
	}
	text, ok, reason := ExtractBytes(data, "application/zip", "secret.zip")
	if !ok {
		t.Fatalf("expected protected manifest, reason=%q", reason)
	}
	if !strings.Contains(text, "secret.txt (6 bytes) [protected]") {
		t.Fatalf("expected protected note, got %q", text)
	}
}

func TestExtractBytes_ZipManifest_TruncatesAboveCap(t *testing.T) {
	entries := make([]archiveTestEntry, maxArchiveEntries+1)
	for i := range entries {
		entries[i] = archiveTestEntry{name: fmt.Sprintf("entry-%04d.txt", i), body: "x"}
	}
	text, ok, reason := ExtractBytes(makeTestZIP(t, entries...), "", "many.zip")
	if !ok {
		t.Fatalf("expected capped manifest, reason=%q", reason)
	}
	want := fmt.Sprintf("[truncated: showing first %d of %d entries]", maxArchiveEntries, maxArchiveEntries+1)
	if !strings.Contains(text, want) {
		t.Fatalf("expected %q, got suffix %q", want, text[max(0, len(text)-100):])
	}
}

func TestExtractBytes_ZipManifest_CorruptFailsClosed(t *testing.T) {
	data := makeTestZIP(t, archiveTestEntry{name: "a.txt", body: "a"})
	data = data[:len(data)-8]
	_, ok, reason := ExtractBytes(data, "application/zip", "broken.zip")
	if ok {
		t.Fatal("expected corrupt ZIP to fail closed")
	}
	if !strings.Contains(reason, "zip manifest failed") {
		t.Fatalf("expected explicit ZIP failure, got %q", reason)
	}
}

func TestExtractBytes_TarManifest_TwoEntries(t *testing.T) {
	data := makeTestTAR(t,
		archiveTestEntry{name: "alpha.txt", body: "alpha"},
		archiveTestEntry{name: "beta.txt", body: "beta"},
	)
	text, ok, reason := ExtractBytes(data, "", "files.tar")
	if !ok {
		t.Fatalf("expected tar manifest, reason=%q", reason)
	}
	if !strings.Contains(text, "alpha.txt (5 bytes)") || !strings.Contains(text, "beta.txt (4 bytes)") {
		t.Fatalf("unexpected tar manifest %q", text)
	}
}

func TestExtractBytes_TarGzManifest_TwoEntries(t *testing.T) {
	tarData := makeTestTAR(t,
		archiveTestEntry{name: "one.txt", body: "1"},
		archiveTestEntry{name: "two.txt", body: "22"},
	)
	text, ok, reason := ExtractBytes(gzipTestBytes(t, tarData), "", "files.tar.gz")
	if !ok {
		t.Fatalf("expected tar.gz manifest, reason=%q", reason)
	}
	if !strings.Contains(text, "one.txt (1 bytes)") || !strings.Contains(text, "two.txt (2 bytes)") {
		t.Fatalf("unexpected tar.gz manifest %q", text)
	}
}

func TestExtractBytes_GzPlain(t *testing.T) {
	text, ok, reason := ExtractBytes(gzipTestBytes(t, []byte("plain gzip text")), "", "note.txt.gz")
	if !ok {
		t.Fatalf("expected gzip text, reason=%q", reason)
	}
	if text != "plain gzip text" {
		t.Fatalf("got %q", text)
	}
}

func TestExtractBytes_OOXMLMagicBytes_WrongExtension(t *testing.T) {
	text, ok, reason := ExtractBytes(makeMagicDocx(t, "renamed document"), "", "renamed.zip")
	if !ok {
		t.Fatalf("expected OOXML extraction, reason=%q", reason)
	}
	if !strings.Contains(text, "renamed document") {
		t.Fatalf("unexpected extracted text %q", text)
	}
}

func TestExtractBytes_OOXMLMagicBytes_NoExtension(t *testing.T) {
	text, ok, reason := ExtractBytes(makeMagicDocx(t, "extensionless document"), "", "document")
	if !ok {
		t.Fatalf("expected OOXML extraction, reason=%q", reason)
	}
	if !strings.Contains(text, "extensionless document") {
		t.Fatalf("unexpected extracted text %q", text)
	}
}

func TestExtractBytes_MIMEFirst_UnknownExtension(t *testing.T) {
	data := makeTestZIP(t, archiveTestEntry{name: "from-mime.txt", body: "data"})
	text, ok, reason := ExtractBytes(data, "application/zip", "opaque.unknown")
	if !ok {
		t.Fatalf("expected MIME-first ZIP extraction, reason=%q", reason)
	}
	if !strings.Contains(text, "from-mime.txt (4 bytes)") {
		t.Fatalf("unexpected MIME-first manifest %q", text)
	}
}

func TestIsExtractableBytes_RenamedOOXML(t *testing.T) {
	data := makeMagicDocx(t, "detect me")
	if !IsExtractableBytes(data, "renamed.bin") {
		t.Fatal("expected renamed OOXML bytes to be extractable")
	}
}

func TestTruncateName_StripsControlChars(t *testing.T) {
	got := truncateName("dir/evil\x00\n\tname.txt")
	if strings.ContainsAny(got, "\x00\n\t") {
		t.Fatalf("control characters survived: %q", got)
	}
	if got != "dir/evilname.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateName_RuneAware(t *testing.T) {
	input := strings.Repeat("界", maxArchiveNameLen+10)
	got := truncateName(input)
	if utf8.RuneCountInString(got) != maxArchiveNameLen {
		t.Fatalf("expected %d runes, got %d", maxArchiveNameLen, utf8.RuneCountInString(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated name is not valid UTF-8")
	}
}

func TestExtract_RejectsOversizedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create oversized file: %v", err)
	}
	if err := f.Truncate(MaxDocBytes + 1); err != nil {
		f.Close()
		t.Fatalf("truncate oversized file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close oversized file: %v", err)
	}
	_, ok, reason := Extract(path, "text/plain", filepath.Base(path))
	if ok {
		t.Fatal("expected oversized path to be rejected")
	}
	if !strings.Contains(reason, "document too large") {
		t.Fatalf("unexpected reason %q", reason)
	}
}
