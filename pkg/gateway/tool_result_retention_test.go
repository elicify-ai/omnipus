package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestToolResultStore_RetentionSweep_DeletesAgedFiles verifies that
// retentionSweep deletes only files older than retentionDays*24h.
func TestToolResultStore_RetentionSweep_DeletesAgedFiles(t *testing.T) {
	home := t.TempDir()
	s := newToolResultStore(home)

	sid := "sessionA"
	sessDir := filepath.Join(home, "tool_results", sid)
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	freshPath := filepath.Join(sessDir, "fresh.json")
	stalePath := filepath.Join(sessDir, "stale.json")
	if err := os.WriteFile(freshPath, []byte(`{"k":"fresh"}`), 0o600); err != nil {
		t.Fatalf("write fresh: %v", err)
	}
	if err := os.WriteFile(stalePath, []byte(`{"k":"stale"}`), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	// Backdate stale to 10 days ago.
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	removed, err := s.retentionSweep(7)
	if err != nil {
		t.Fatalf("retentionSweep: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d; want 1", removed)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Errorf("stale.json should have been deleted, stat err = %v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("fresh.json should remain, stat err = %v", err)
	}
}

// TestToolResultStore_RetentionSweep_ZeroDays is a no-op.
func TestToolResultStore_RetentionSweep_ZeroDays(t *testing.T) {
	home := t.TempDir()
	s := newToolResultStore(home)
	sessDir := filepath.Join(home, "tool_results", "x")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "any.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	removed, err := s.retentionSweep(0)
	if err != nil {
		t.Fatalf("retentionSweep: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d; want 0 when retentionDays<=0", removed)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file should remain when sweep disabled, stat err = %v", err)
	}
}

// TestToolResultStore_RetentionSweep_RemovesEmptySessionDir verifies that a
// session subdirectory with all its files deleted is removed entirely.
func TestToolResultStore_RetentionSweep_RemovesEmptySessionDir(t *testing.T) {
	home := t.TempDir()
	s := newToolResultStore(home)
	sessDir := filepath.Join(home, "tool_results", "all_stale")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "stale.json")
	if err := os.WriteFile(p, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if _, err := s.retentionSweep(7); err != nil {
		t.Fatalf("retentionSweep: %v", err)
	}
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Errorf("session dir should have been removed after last file deleted, stat err = %v", err)
	}
}

// TestToolResultStore_RetentionSweep_NilStoreIsNoOp covers the disabled-store path.
func TestToolResultStore_RetentionSweep_NilStoreIsNoOp(t *testing.T) {
	var s *toolResultStore
	removed, err := s.retentionSweep(7)
	if err != nil {
		t.Fatalf("nil retentionSweep: %v", err)
	}
	if removed != 0 {
		t.Errorf("nil store removed = %d; want 0", removed)
	}
}

// TestToolResultStore_RetentionSweep_DisabledStoreIsNoOp covers the empty-homePath case.
func TestToolResultStore_RetentionSweep_DisabledStoreIsNoOp(t *testing.T) {
	s := newToolResultStore("")
	removed, err := s.retentionSweep(7)
	if err != nil {
		t.Fatalf("disabled retentionSweep: %v", err)
	}
	if removed != 0 {
		t.Errorf("disabled store removed = %d; want 0", removed)
	}
}

// TestToolResultStore_RetentionSweep_MissingBaseDir is a no-op even when
// $OMNIPUS_HOME/tool_results does not exist (no offload has ever happened).
func TestToolResultStore_RetentionSweep_MissingBaseDir(t *testing.T) {
	s := newToolResultStore(t.TempDir())
	removed, err := s.retentionSweep(7)
	if err != nil {
		t.Fatalf("retentionSweep: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d; want 0 when base dir absent", removed)
	}
}
