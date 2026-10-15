//go:build !windows

package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// The redirect must not outlive the restore. A marker written while fd 2
// points at the panic log lands in that file; a marker written after the
// restore must not.
func TestRestoreProcessStderr_UndoesPanicFileRedirect(t *testing.T) {
	saved, err := saveProcessStderr()
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			_ = restoreProcessStderr(saved)
		}
	})

	panicLog := filepath.Join(t.TempDir(), "panic.log")
	cleanup, err := logger.InitPanic(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if _, err := os.Stderr.WriteString("HIDDEN_MARKER\n"); err != nil {
		t.Fatal(err)
	}
	if err := restoreProcessStderr(saved); err != nil {
		t.Fatal(err)
	}
	restored = true
	if _, err := os.Stderr.WriteString("VISIBLE_MARKER\n"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "HIDDEN_MARKER") {
		t.Fatalf("panic log %q does not contain the write made while stderr was redirected", text)
	}
	if strings.Contains(text, "VISIBLE_MARKER") {
		t.Fatalf("panic log still received a write after restore: %q", text)
	}
}
