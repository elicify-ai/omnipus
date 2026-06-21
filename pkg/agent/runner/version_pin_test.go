package runner

import (
	"context"
	"strings"
	"testing"
)

// TestDetectAndPinVersion_KnownVersion creates a fake CLI that prints a version
// matching one of the known prefixes, then verifies detectAndPinVersion returns
// the version with known=true (FR-5.6 / N3).
func TestDetectAndPinVersion_KnownVersion(t *testing.T) {
	dir := t.TempDir()
	isolatePATH(t, dir)
	writeFakeBin(t, dir, "fake-cli", "fake-cli version 1.2.3")

	ver, known := detectAndPinVersion(context.Background(), "fake-cli", "test/known", []string{"1.2"})
	if !known {
		t.Fatalf("expected known=true for matching prefix; got version=%q known=false", ver)
	}
	if !strings.Contains(ver, "1.2.3") {
		t.Fatalf("expected version to contain 1.2.3; got %q", ver)
	}
}

// TestDetectAndPinVersion_UnknownVersion creates a fake CLI whose version does
// not match any known prefix, verifying detectAndPinVersion returns the version
// with known=false (graceful degradation — the run proceeds but the driver
// cannot assert version-specific behavior).
func TestDetectAndPinVersion_UnknownVersion(t *testing.T) {
	dir := t.TempDir()
	isolatePATH(t, dir)
	writeFakeBin(t, dir, "fake-cli", "fake-cli version 9.9.9-beta")

	ver, known := detectAndPinVersion(context.Background(), "fake-cli", "test/unknown", []string{"1."})
	if known {
		t.Fatalf("expected known=false for non-matching prefix; got known=true version=%q", ver)
	}
	if !strings.Contains(ver, "9.9.9") {
		t.Fatalf("expected version to contain 9.9.9; got %q", ver)
	}
}

// TestDetectAndPinVersion_MissingBinary verifies that a missing binary returns
// ("", false) — the caller proceeds with graceful degradation rather than
// aborting.
func TestDetectAndPinVersion_MissingBinary(t *testing.T) {
	dir := t.TempDir()
	isolatePATH(t, dir)

	ver, known := detectAndPinVersion(context.Background(), "nonexistent-cli", "test/missing", []string{"1."})
	if known {
		t.Fatalf("expected known=false for missing binary; got known=true version=%q", ver)
	}
	if ver != "" {
		t.Fatalf("expected empty version for missing binary; got %q", ver)
	}
}
