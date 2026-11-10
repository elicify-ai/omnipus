package browser

// #1730: archive extraction observes the boot drain's cancellation (see
// installer_cancel_test.go for the family background).

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeTestZip writes a zip archive with the given file names (each holding
// its own name as content) and returns its path.
func writeTestZip(t *testing.T, names ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
		if _, err := w.Write([]byte(name)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
	return path
}

// TestExtractZip_CancelledWritesNothing: an extraction whose context is
// already cancelled — the boot drain cancelled the preprovision just as its
// download finished — must not create a single entry.
func TestExtractZip_CancelledWritesNothing(t *testing.T) {
	zipPath := writeTestZip(t, "build/one.txt", "build/two.txt", "build/three.txt")
	dest := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := extractZip(ctx, zipPath, dest)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("extractZip with a cancelled context returned %v, want context.Canceled", err)
	}
	entries, readErr := os.ReadDir(dest)
	if readErr != nil {
		t.Fatalf("read extraction dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("extractZip kept writing after cancellation (#1730): %d entries created, want none", len(entries))
	}
}

// TestExtractZip_PositiveControl: the same archive with a live context is
// extracted completely, so the cancelled case above cannot pass because the
// fixture is unreadable.
func TestExtractZip_PositiveControl(t *testing.T) {
	names := []string{"build/one.txt", "build/two.txt", "build/three.txt"}
	zipPath := writeTestZip(t, names...)
	dest := t.TempDir()
	if err := extractZip(context.Background(), zipPath, dest); err != nil {
		t.Fatalf("extractZip: %v", err)
	}
	for _, name := range names {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Errorf("extracted %s missing: %v", name, err)
			continue
		}
		if string(got) != name {
			t.Errorf("extracted %s = %q, want %q", name, got, name)
		}
	}
}

// TestExtractZip_CancelledBetweenFilesLeavesLaterFilesUnwritten is the
// in-flight witness: the cancel arrives only after the first file is fully on
// disk, the moment extraction asks its context before the second file. The
// second and third files must never be written. An extraction that checks its
// context once, before the loop, sees a live context there and writes them.
func TestExtractZip_CancelledBetweenFilesLeavesLaterFilesUnwritten(t *testing.T) {
	names := []string{"build/one.txt", "build/two.txt", "build/three.txt"}
	zipPath := writeTestZip(t, names...)
	dest := t.TempDir()
	first := filepath.Join(dest, names[0])
	ctx := newProgressCancelCtx(t, func() bool {
		got, err := os.ReadFile(first)
		return err == nil && string(got) == names[0]
	})

	err := extractZip(ctx, zipPath, dest)

	if !ctx.fired.Load() {
		t.Error("extractZip never consulted its context after the first file was on disk (#1730): no cancellation point between files, so the cancel was never delivered")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("extractZip cancelled between files returned %v, want context.Canceled", err)
	}
	for _, name := range names[1:] {
		if _, statErr := os.Stat(filepath.Join(dest, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("extractZip kept writing after a cancel between files (#1730): %s exists (stat err %v)", name, statErr)
		}
	}
}
