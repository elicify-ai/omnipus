// Package e2e hosts the end-to-end checks that need the BUILT artefact rather
// than a package under test. Everything else in this directory is Playwright.
//
// ADR-068 SC-001, last clause: "`strings` on the built binary contains neither
// id". scripts/check-no-removed-providers.sh proves the SOURCE TREE is clean;
// this test proves the SHIPPED BINARY is — a different property, and the one an
// operator can actually observe. A literal can reach the binary without
// appearing in the scanned roots (an embedded asset, a generated snapshot, a
// vendored dependency), and the source guard would stay green through all of
// them.
//
// # WHY THIS FILE MAY SPELL THE FORBIDDEN IDS
//
// check-no-removed-providers.sh scans `pkg cmd src contracts config docs`.
// `tests/` is outside those roots by construction, exactly so the guard for a
// property can live somewhere the property does not have to hold. A Go test
// under pkg/ could not do this — it would have to spell the names it forbids
// inside a scanned root, which is itself a trace (ADR-068 §2.4).
//
// This test is intentionally NOT skippable. A skip here would report "clean"
// for a binary nobody looked at (docs/internal/false-green-patterns.md).
package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The ids ADR-068 §2.4 deleted. Lower-case; the scan is case-insensitive.
var removedProviderLiterals = []string{"antigravity", "claude-cli"}

// A literal that MUST be present. Without it a scan that silently read zero
// bytes — a truncated file, a wrong path, a mis-sized chunk loop — would report
// exactly the same "no hits" as a genuinely clean binary.
const sentinelLiteral = "openrouter"

// buildTags mirrors GO_BUILD_TAGS in the Makefile. Building without them fails
// on pkg/channels/matrix (CLAUDE.md, "Testing & building").
const buildTags = "goolm,stdjson"

// omnipusBinary returns a path to a built omnipus binary, building one if the
// environment did not hand us a prebuilt path via OMNIPUS_TEST_BINARY (which
// the embed-build / e2e gates already produce, so CI need not build twice).
func omnipusBinary(t *testing.T) string {
	t.Helper()

	if prebuilt := os.Getenv("OMNIPUS_TEST_BINARY"); prebuilt != "" {
		info, err := os.Stat(prebuilt)
		if err != nil {
			t.Fatalf("OMNIPUS_TEST_BINARY=%q is not readable: %v", prebuilt, err)
		}
		if info.Size() == 0 {
			t.Fatalf("OMNIPUS_TEST_BINARY=%q is empty", prebuilt)
		}
		return prebuilt
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	ensureSPAStub(t, repoRoot)
	out := filepath.Join(t.TempDir(), "omnipus-under-test")
	cmd := exec.Command("go", "build", "-tags", buildTags, "-o", out, "./cmd/omnipus/")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/omnipus/ failed: %v\n%s", err, combined)
	}
	return out
}

// ensureSPAStub mirrors deploy/ci-worker/runci.sh's `ensure_spa_stub`.
//
// pkg/gateway/spa/ is a gitignored build output (CLAUDE.md, "SPA Embed
// Pipeline"), and pkg/gateway/embed.go's `go:embed all:spa` refuses to compile
// without it — so a fresh clone or worktree cannot build cmd/omnipus at all
// until something puts a file there. Every gate that builds the binary already
// does exactly this; doing it here is what lets this test run everywhere
// instead of skipping (a skip would report "clean" for a binary nobody built).
//
// The stub is deliberately NOT removed afterwards: `go test ./...` compiles
// packages in parallel and several of them embed this directory, so deleting it
// mid-run would break an unrelated package's build. Leaving a gitignored
// placeholder behind is precisely what the CI worker does.
//
// The stub's CONTENT never matters here — this test reads the Go binary's own
// strings, not the SPA's. A real embedded SPA (embed-build / e2e gates, or
// OMNIPUS_TEST_BINARY pointing at a shipped binary) is scanned too and must be
// equally clean.
func ensureSPAStub(t *testing.T, repoRoot string) {
	t.Helper()
	index := filepath.Join(repoRoot, "pkg", "gateway", "spa", "index.html")
	if _, err := os.Stat(index); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(index), 0o755); err != nil {
		t.Fatalf("create SPA embed dir: %v", err)
	}
	if err := os.WriteFile(index, []byte("<!doctype html><title>test-stub</title>"), 0o644); err != nil {
		t.Fatalf("write SPA embed stub: %v", err)
	}
}

// scanBinary reports which of `needles` appear anywhere in the file, matched
// case-insensitively. Read in overlapping chunks so a hit that straddles a
// chunk boundary is still found and the whole binary is never held in memory.
func scanBinary(t *testing.T, path string, needles []string) map[string]bool {
	t.Helper()

	longest := 0
	lowered := make([][]byte, len(needles))
	for i, n := range needles {
		lowered[i] = []byte(strings.ToLower(n))
		if len(n) > longest {
			longest = len(n)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	const chunk = 4 << 20 // 4 MiB
	overlap := longest - 1
	if overlap < 0 {
		overlap = 0
	}
	buf := make([]byte, chunk+overlap)
	found := make(map[string]bool, len(needles))
	carry := 0
	total := 0

	for {
		n, readErr := f.Read(buf[carry : carry+chunk])
		if n > 0 {
			total += n
			window := buf[:carry+n]
			lowerWindow := bytes.ToLower(window)
			for i, needle := range lowered {
				if bytes.Contains(lowerWindow, needle) {
					found[needles[i]] = true
				}
			}
			if overlap > 0 && len(window) >= overlap {
				copy(buf, window[len(window)-overlap:])
				carry = overlap
			} else {
				carry = len(window)
				copy(buf, window)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
	}

	if total == 0 {
		t.Fatalf("read 0 bytes from %s — the scan proved nothing", path)
	}
	return found
}

// TestBinaryHasNoRemovedProviderLiteral — ADR-068 SC-001 / FR-001 (TDD row 1a).
func TestBinaryHasNoRemovedProviderLiteral(t *testing.T) {
	bin := omnipusBinary(t)

	needles := append(append([]string{}, removedProviderLiterals...), sentinelLiteral)
	found := scanBinary(t, bin, needles)

	// The scan works: a literal that IS in the binary was found. Without this,
	// the assertions below could pass on a scan that reads nothing meaningful.
	if !found[sentinelLiteral] {
		t.Fatalf(
			"sentinel %q not found in %s — the scan is not reading the binary's strings, "+
				"so its 'no removed literals' result proves nothing",
			sentinelLiteral, bin,
		)
	}

	for _, literal := range removedProviderLiterals {
		if found[literal] {
			t.Errorf(
				"removed provider id %q is present in the built binary (%s). "+
					"ADR-068 §2.4 deletes it with no alias, shim or error string; "+
					"a merge from a branch cut before the deletion re-adds it as an "+
					"ordinary, conflict-free addition — resolve by KEEPING the deletion.",
				literal, bin,
			)
		}
	}
}
