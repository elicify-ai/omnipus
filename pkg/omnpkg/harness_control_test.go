package omnpkg_test

// RED T-2 — explicit control test: the ONLY test expected to pass on the RED
// stub, because it exercises the test-side fixture machinery (stdlib tar +
// zstd round-trip), not the engine under test. It proves the instrument can
// see what the other tests assert on: that a fixture package is a well-formed
// zstd tar with manifest.json first. If this fails, every damaged-package
// expectation in this suite is unsound.

import (
	"bytes"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

func TestHarness_PackageBuilderControl(t *testing.T) {
	m, files := validManifest(testSourceID, true)
	pkg := buildPackage(t, m, files)

	dec := decodePackagePlain(t, pkg)
	require.Equal(t, omnpkg.ManifestEntry, dec.Order[0], "fixture: manifest.json first (FR-001)")
	require.Len(t, dec.Order, 4, "fixture: manifest + 3 payload entries")
	for name, body := range files {
		require.Equal(t, body, dec.Payload[name], "fixture round-trips %s", name)
	}
	// The fixture's manifest hashes match its own payload bytes.
	for name, body := range files {
		require.Equal(t, sha256hex(body), m.Files[name].SHA256)
	}
	// The crafted oversized-window frame decodes with a big window (it is a
	// valid frame; only a bounded reader must refuse it).
	frame := oversizedWindowFrame(t, bytes.Repeat([]byte("A"), 2048))
	require.Equal(t, 0x98, int(frame[5]), "fixture: window descriptor exponent 19 → 512 MiB")
}
