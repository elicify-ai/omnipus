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
		t.Fatalf("%s\n%v\n%s", classifyBuildFailure(combined), err, combined)
	}
	return out
}

// hostBuildFailureMarkers are toolchain messages that mean the MACHINE could not
// complete a link, not that anything about the code changed. They have all been
// observed on the CI worker, whose build cache and $TMPDIR share one small
// volume with every other gate's artefacts.
var hostBuildFailureMarkers = []string{
	"no space left on device",
	"cannot allocate memory",
	"signal: killed",
}

// classifyBuildFailure labels a failed `go build` as an environment fault or a
// genuine one.
//
// This NEVER converts a failure into a pass or a skip — the caller still
// t.Fatalf's either way, so the guard stays red and nobody reads "clean" for a
// binary that was never produced (docs/internal/false-green-patterns.md).
// It only names the cause, because the two causes want opposite responses and
// have already been confused once: a CI run exhausted the worker's disk
// mid-link, and this test's failure was read as "a removed provider id came
// back in the binary" when no binary had been built at all.
func classifyBuildFailure(combined []byte) string {
	lowered := strings.ToLower(string(combined))
	for _, marker := range hostBuildFailureMarkers {
		if strings.Contains(lowered, marker) {
			return "go build ./cmd/omnipus/ failed for an ENVIRONMENT reason (" + marker +
				"): no binary was produced, so the removed-provider scan did not run. " +
				"This is NOT evidence that a removed provider id reappeared, and NOT a " +
				"code regression — free disk/memory on the build host and re-run."
		}
	}
	return "go build ./cmd/omnipus/ failed: the binary under test could not be produced, " +
		"so the removed-provider scan did not run. Fix the build first; this failure says " +
		"nothing either way about whether a removed provider id is present."
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

// TestClassifyBuildFailure_SeparatesEnvironmentFromRegression exercises the
// classifier by INJECTING the toolchain's output, rather than by actually
// filling the disk — the same reason this delivery replaced its chmod-based
// fault injection: a real ENOSPC is not reproducible on demand, and a guard you
// cannot exercise is a guard nobody knows is wired up.
func TestClassifyBuildFailure_SeparatesEnvironmentFromRegression(t *testing.T) {
	for _, tc := range []struct {
		name        string
		combined    string
		wantEnvFail bool
	}{
		{
			name: "enospc during link",
			combined: "# github.com/elicify-ai/omnipus/cmd/omnipus\n" +
				"/usr/local/go/pkg/tool/linux_amd64/link: mapping output file failed: no space left on device\n",
			wantEnvFail: true,
		},
		{
			name:        "oom killed",
			combined:    "signal: killed\n",
			wantEnvFail: true,
		},
		{
			name:        "out of memory",
			combined:    "compile: cannot allocate memory\n",
			wantEnvFail: true,
		},
		{
			name:        "genuine compile error",
			combined:    "./main.go:12:2: undefined: notAThing\n",
			wantEnvFail: false,
		},
		{
			name:        "empty output",
			combined:    "",
			wantEnvFail: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyBuildFailure([]byte(tc.combined))

			// Whatever the cause, the message must state that the scan did not
			// run — otherwise a reader can mistake a failed build for a clean
			// or a dirty binary, and both readings are wrong.
			if !strings.Contains(got, "did not run") {
				t.Errorf("message never says the scan did not run: %q", got)
			}

			isEnvFail := strings.Contains(got, "ENVIRONMENT reason")
			if isEnvFail != tc.wantEnvFail {
				t.Errorf("classified as environment-failure=%v, want %v\nmessage: %q",
					isEnvFail, tc.wantEnvFail, got)
			}

			// An environment failure must actively deny being a regression, so
			// it cannot be triaged as "a removed provider id came back".
			if tc.wantEnvFail && !strings.Contains(got, "NOT a") {
				t.Errorf("environment failure does not disclaim being a regression: %q", got)
			}
		})
	}
}
