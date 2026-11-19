package omnpkg

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/klauspost/compress/zstd"
)

// errDecodedLimit is the sentinel the counting reader returns once the decoded
// stream passes MaxDecodedBytes.
var errDecodedLimit = errors.New("omnpkg: decoded stream over the limit")

// boundedStream is the decoded side of a package: zstd with the FR-008 decoder
// limits, wrapped in a counter over every decoded byte (tar headers and
// padding included). It remembers the first underlying error so a failure is
// classified by its cause, whatever archive/tar turns it into.
type boundedStream struct {
	dec      *zstd.Decoder
	n        int64
	exceeded bool
	cause    error
}

func openBounded(r io.Reader) (*boundedStream, error) {
	// Hide any Bytes()/Len() method of r: the decoder would otherwise decode
	// a small in-memory buffer whole (up to MaxDecoderMemory) before the
	// counter could stop it.
	dec, err := zstd.NewReader(struct{ io.Reader }{r},
		zstd.WithDecoderMaxWindow(MaxDecoderWindow),
		zstd.WithDecoderMaxMemory(MaxDecoderMemory),
		zstd.WithDecoderConcurrency(1),
	)
	if err != nil {
		return nil, fmt.Errorf("omnpkg: open zstd decoder: %w", err)
	}
	return &boundedStream{dec: dec}, nil
}

func (s *boundedStream) Read(p []byte) (int, error) {
	if s.exceeded {
		return 0, errDecodedLimit
	}
	n, err := s.dec.Read(p)
	s.n += int64(n)
	if s.n > MaxDecodedBytes {
		s.exceeded = true
		return 0, errDecodedLimit
	}
	if err != nil && !errors.Is(err, io.EOF) && s.cause == nil {
		s.cause = err
	}
	return n, err
}

func (s *boundedStream) Close() { s.dec.Close() }

// classify maps a read failure to its wire refusal: a decoder limit is
// package_too_large (naming the limit), anything else not_a_package.
func (s *boundedStream) classify(err error) error {
	if s.exceeded || errors.Is(err, errDecodedLimit) {
		return tooLarge(LimitDecoded)
	}
	for _, e := range []error{s.cause, err} {
		switch {
		case e == nil:
		case errors.Is(e, zstd.ErrWindowSizeExceeded):
			return tooLarge(LimitWindow)
		case errors.Is(e, zstd.ErrDecoderSizeExceeded):
			return tooLarge(LimitDecoded)
		}
	}
	return newErr(CodeNotAPackage, "reason", "not a zstd-compressed tar package")
}

// readManifestEntry reads the first tar entry, which must be a regular
// manifest.json of at most MaxManifestBytes, and parses and validates it.
func readManifestEntry(s *boundedStream, tr *tar.Reader) (*Manifest, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, s.classify(err)
	}
	if hdr.Name != ManifestEntry || (hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA) { //nolint:staticcheck // TypeRegA: old regular-file flag
		return nil, newErr(CodeNotAPackage, "reason", "the first entry is not manifest.json")
	}
	if hdr.Size > MaxManifestBytes {
		return nil, tooLarge(LimitManifest)
	}
	raw, err := io.ReadAll(io.LimitReader(tr, MaxManifestBytes+1))
	if err != nil {
		return nil, s.classify(err)
	}
	if len(raw) > MaxManifestBytes {
		return nil, tooLarge(LimitManifest)
	}
	return parseManifest(raw)
}

// parseManifest applies the manifest-only rules: JSON object, format,
// format_version (FR-004) and agent element schema (FR-005). Unknown fields
// are ignored.
func parseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, newErr(CodeNotAPackage, "reason", "manifest.json is not a valid manifest")
	}
	if m.Format != FormatName {
		return nil, newErr(CodeNotAPackage, "reason", "manifest format is not omnpkg")
	}
	if m.FormatVersion < 1 {
		return nil, newErr(CodeNotAPackage, "reason", "manifest format_version is missing or below 1")
	}
	if m.FormatVersion > FormatVersion {
		return nil, newErr(CodePackageTooNew, "format_version", fmt.Sprint(m.FormatVersion))
	}
	for _, el := range m.Elements {
		if el.Type != ElementTypeAgent {
			continue
		}
		if el.Schema > AgentSchemaVersion {
			return nil, newErr(CodeElementSchemaTooNew, "element", el.SourceID, "schema", fmt.Sprint(el.Schema))
		}
		if el.Schema < 1 {
			return nil, newErr(CodeInvalidElement, "element", el.SourceID, "reason", "schema below 1")
		}
	}
	return &m, nil
}

// ReadManifest reads and validates ONLY the manifest: first entry, bounded
// manifest size, format, format_version and element schema rules (FR-001,
// FR-004, FR-005); unknown fields ignored. The payload is never read.
func ReadManifest(r io.Reader) (*Manifest, error) {
	s, err := openBounded(r)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return readManifestEntry(s, tar.NewReader(s))
}

// Inspect reads only the bounded manifest and makes no integrity claim
// (FR-052); it writes nothing and needs no gateway.
func Inspect(r io.Reader) (*Manifest, error) { return ReadManifest(r) }

// VerifyReport lists the payload files whose checksums verified.
type VerifyReport struct {
	FilesVerified []string
}

// Verify performs every format check (FR-001–FR-009, FR-052) and names the
// failing file.
func Verify(r io.Reader) (*VerifyReport, error) {
	m, payload, err := ReadPackage(r)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(payload))
	for name := range m.Files {
		files = append(files, name)
	}
	sort.Strings(files)
	return &VerifyReport{FilesVerified: files}, nil
}

// ReadPackage performs every format check and returns the manifest and the
// decoded payload files keyed by entry path (the upload-side reader, FR-020).
// Element content rules (record JSON, types) are ValidateElements'.
func ReadPackage(r io.Reader) (*Manifest, map[string][]byte, error) {
	s, err := openBounded(r)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	tr := tar.NewReader(s)
	m, err := readManifestEntry(s, tr)
	if err != nil {
		return nil, nil, err
	}
	if err = checkManifestStructure(m); err != nil {
		return nil, nil, err
	}
	payload, err := readPayload(s, tr, m)
	if err != nil {
		return nil, nil, err
	}
	if err = checkAgentLayouts(m); err != nil {
		return nil, nil, err
	}
	if err = checkRecordIdentity(m, payload); err != nil {
		return nil, nil, err
	}
	return m, payload, nil
}

// readPayload reads every entry after the manifest with the entry-count,
// entry-type, path, size and checksum checks, then reconciles the listing
// (FR-003, FR-007, FR-008). Unlisted entries are counted but never kept.
func readPayload(s *boundedStream, tr *tar.Reader, m *Manifest) (map[string][]byte, error) {
	payload := make(map[string][]byte, len(m.Files))
	seen := map[string]bool{ManifestEntry: true}
	entries := 1
	firstUnlisted := ""
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, s.classify(err)
		}
		entries++
		if entries > MaxEntries {
			return nil, tooLarge(LimitEntries)
		}
		if err = checkEntryHeader(hdr, seen); err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		fi, listed := m.Files[hdr.Name]
		if !listed {
			if firstUnlisted == "" {
				firstUnlisted = hdr.Name
			}
			if _, err = io.Copy(io.Discard, tr); err != nil {
				return nil, s.classify(err)
			}
			continue
		}
		body, err := readListedEntry(s, tr, hdr, fi)
		if err != nil {
			return nil, err
		}
		payload[hdr.Name] = body
	}
	if firstUnlisted != "" {
		return nil, newErr(CodeManifestMismatch, "file", firstUnlisted, "reason", "entry not listed in the manifest")
	}
	for _, name := range sortedFileNames(m.Files) {
		if _, ok := payload[name]; !ok {
			return nil, newErr(CodeManifestMismatch, "file", name, "reason", "listed entry missing from the package")
		}
	}
	return payload, nil
}

// checkEntryHeader applies the universal checks to one entry header: safe
// type, safe path, no duplicate path, and the per-file cap for agent-owned
// paths.
func checkEntryHeader(hdr *tar.Header, seen map[string]bool) error {
	if !SafeEntryType(hdr.Typeflag) {
		return newErr(CodeUnsafeEntryType, "file", hdr.Name)
	}
	if UnsafeEntryPath(hdr.Name) {
		return newErr(CodeUnsafePath, "file", hdr.Name)
	}
	if seen[hdr.Name] {
		return newErr(CodeUnsafeEntryType, "file", hdr.Name, "reason", "duplicate path")
	}
	seen[hdr.Name] = true
	if len(hdr.Name) > len(AgentsPrefix) && hdr.Name[:len(AgentsPrefix)] == AgentsPrefix && hdr.Size > MaxAgentFileBytes {
		return tooLarge(LimitAgentFile)
	}
	return nil
}

// readListedEntry reads a listed entry and checks its size and SHA-256
// against the manifest, naming the file on a mismatch.
func readListedEntry(s *boundedStream, tr *tar.Reader, hdr *tar.Header, fi FileInfo) ([]byte, error) {
	if hdr.Size != fi.Size {
		return nil, newErr(CodeChecksumMismatch, "file", hdr.Name, "reason", "size differs from the manifest")
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, tr); err != nil {
		return nil, s.classify(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	if hex.EncodeToString(sum[:]) != fi.SHA256 {
		return nil, newErr(CodeChecksumMismatch, "file", hdr.Name, "reason", "sha256 differs from the manifest")
	}
	return buf.Bytes(), nil
}

func sortedFileNames(files map[string]FileInfo) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
