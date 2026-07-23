package library_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func newWorkspaceLibrary(t *testing.T, now *time.Time) (*library.Library, string, string) {
	t.Helper()
	home := t.TempDir()
	workspaceID := uuid.NewString()
	lib, err := library.New(home, workspaceID, library.WithClock(func() time.Time { return *now }))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return lib, home, workspaceID
}

func uploadFixture(t *testing.T, lib *library.Library, filename string, data []byte) (string, gen.MediaLibraryEntry) {
	t.Helper()
	ref, entry, err := lib.Upload(filename, gen.TestFixture, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Upload(%q) error = %v", filename, err)
	}
	return ref, entry
}

func mediaID(t *testing.T, entry gen.MediaLibraryEntry) string {
	t.Helper()
	if entry.Id == nil {
		t.Fatal("entry.Id is nil")
	}
	return entry.Id.String()
}

func rawFileFor(t *testing.T, lib *library.Library, data []byte) string {
	t.Helper()
	entries, err := os.ReadDir(lib.Path())
	if err != nil {
		t.Fatalf("ReadDir(%q) error = %v", lib.Path(), err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "manifest.json" {
			continue
		}
		path := filepath.Join(lib.Path(), entry.Name())
		got, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(got, data) {
			return path
		}
	}
	t.Fatalf("raw file with %d bytes not found under %q", len(data), lib.Path())
	return ""
}

func TestWorkspaceLibrary_Store_AnyFormat_Succeeds(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	formats := map[string][]byte{
		"image.png":  {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		"image.jpg":  {0xff, 0xd8, 0xff, 0xdb},
		"image.webp": []byte("RIFF0000WEBP"),
		"image.bmp":  []byte("BMraw"),
		"image.tiff": {'I', 'I', 42, 0},
		"image.gif":  []byte("GIF89a"),
		"image.svg":  []byte("<svg><circle/></svg>"),
		"image.avif": []byte("0000ftypavif"),
		"image.heic": []byte("0000ftypheic"),
		"image.ico":  {0, 0, 1, 0},
		"file.pdf":   []byte("%PDF-1.7"),
	}

	seen := make(map[string]struct{}, len(formats))
	for filename, data := range formats {
		ref, entry := uploadFixture(t, lib, filename, data)
		if !strings.HasPrefix(ref, "media://workspace/"+workspaceID+"/") {
			t.Errorf("Upload(%q) ref = %q", filename, ref)
		}
		if entry.Filename != filename {
			t.Errorf("Upload(%q) filename = %q", filename, entry.Filename)
		}
		if entry.Size == nil || *entry.Size != int64(len(data)) {
			t.Errorf("Upload(%q) size = %v", filename, entry.Size)
		}
		if entry.Mime == nil || *entry.Mime != http.DetectContentType(data) {
			t.Errorf("Upload(%q) MIME = %v, want %q", filename, entry.Mime, http.DetectContentType(data))
		}
		id := mediaID(t, entry)
		if _, duplicate := seen[id]; duplicate {
			t.Errorf("Upload(%q) reused media ID %q", filename, id)
		}
		seen[id] = struct{}{}
	}

	wantPath := filepath.Join(home, "workspaces", workspaceID, "media")
	if lib.Path() != wantPath {
		t.Fatalf("Path() = %q, want %q", lib.Path(), wantPath)
	}
	if got := len(lib.List()); got != len(formats) {
		t.Fatalf("List() length = %d, want %d", got, len(formats))
	}
}

func TestWorkspaceLibrary_ExistingULIDWorkspace_Succeeds(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC)
	home := t.TempDir()
	workspaceID := ulid.Make().String()
	lib, err := library.New(home, workspaceID, library.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("New(ULID workspace) error = %v", err)
	}
	ref, entry := uploadFixture(t, lib, "existing-workspace.bin", []byte("existing workspace"))
	if !strings.HasPrefix(ref, "media://workspace/"+workspaceID+"/") {
		t.Fatalf("Upload() ref = %q", ref)
	}
	if entry.WorkspaceId != workspaceID {
		t.Fatalf("WorkspaceId = %q, want %q", entry.WorkspaceId, workspaceID)
	}
}

func TestWorkspaceLibrary_Manifest_HasSHA256AndUploadedAt(t *testing.T) {
	now := time.Date(2026, 7, 23, 11, 12, 13, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	data := []byte("manifest integrity fixture")
	_, entry := uploadFixture(t, lib, "report.bin", data)

	wantDigest := sha256.Sum256(data)
	if entry.Sha256 == nil || *entry.Sha256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("Sha256 = %v, want %s", entry.Sha256, hex.EncodeToString(wantDigest[:]))
	}
	if entry.UploadedAt == nil || !entry.UploadedAt.Equal(now) {
		t.Fatalf("UploadedAt = %v, want %v", entry.UploadedAt, now)
	}
	if entry.WorkspaceId != workspaceID {
		t.Fatalf("WorkspaceId = %q, want %q", entry.WorkspaceId, workspaceID)
	}
	if entry.Source != gen.TestFixture {
		t.Fatalf("Source = %q, want %q", entry.Source, gen.TestFixture)
	}
	if entry.Refcount == nil || *entry.Refcount != 0 {
		t.Fatalf("Refcount = %v, want 0", entry.Refcount)
	}

	manifest, err := os.ReadFile(filepath.Join(lib.Path(), "manifest.json"))
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	for _, field := range []string{"\"id\"", "\"filename\"", "\"mime\"", "\"size\"", "\"sha256\"", "\"uploaded_at\"", "\"source\""} {
		if !bytes.Contains(manifest, []byte(field)) {
			t.Errorf("manifest missing field %s: %s", field, manifest)
		}
	}

	reopened, err := library.New(home, workspaceID, library.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("New(reopen) error = %v", err)
	}
	if err := reopened.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	entries := reopened.List()
	if len(entries) != 1 || entries[0].Sha256 == nil || *entries[0].Sha256 != *entry.Sha256 {
		t.Fatalf("reopened entries = %#v", entries)
	}
}

func TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	original := []byte("trusted bytes")
	_, entry := uploadFixture(t, lib, "trusted.bin", original)
	id := mediaID(t, entry)

	clean, cleanEntry, err := lib.Read(id)
	if err != nil {
		t.Fatalf("Read(clean) error = %v", err)
	}
	if !bytes.Equal(clean, original) || mediaID(t, cleanEntry) != id {
		t.Fatalf("Read(clean) = %q, %#v", clean, cleanEntry)
	}

	rawPath := rawFileFor(t, lib, original)
	if writeErr := os.WriteFile(rawPath, []byte("forged bytes"), 0o600); writeErr != nil {
		t.Fatalf("tamper raw file: %v", writeErr)
	}
	corrupt, corruptEntry, err := lib.Read(id)
	if !errors.Is(err, library.ErrIntegrityCheckFailed) {
		t.Fatalf("Read(tampered) error = %v, want ErrIntegrityCheckFailed", err)
	}
	if corrupt != nil {
		t.Fatalf("Read(tampered) returned %d unverified bytes", len(corrupt))
	}
	if mediaID(t, corruptEntry) != id {
		t.Fatalf("Read(tampered) entry = %#v", corruptEntry)
	}
}

func TestWorkspaceLibrary_LazyNormalization_UploadFast(t *testing.T) {
	now := time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	invalidPNG := []byte("\x89PNG\r\n\x1a\nnot-a-decodable-png")
	_, entry := uploadFixture(t, lib, "malformed.png", invalidPNG)

	got, _, err := lib.Read(mediaID(t, entry))
	if err != nil {
		t.Fatalf("Read(raw malformed image) error = %v", err)
	}
	if !bytes.Equal(got, invalidPNG) {
		t.Fatalf("Read(raw malformed image) = %q, want original bytes", got)
	}
	entries, err := os.ReadDir(lib.Path())
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("media directory has %d entries, want raw file + manifest only", len(entries))
	}
	for _, item := range entries {
		if item.IsDir() {
			t.Fatalf("Upload created derived-artifact directory %q", item.Name())
		}
	}
}

func TestWorkspaceLibrary_NoStorageQuota_MultipleUploadsSucceed(t *testing.T) {
	now := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	payload := bytes.Repeat([]byte{0xa5}, 1<<20)
	for i := 0; i < 4; i++ {
		uploadFixture(t, lib, uuid.NewString()+".bin", payload)
	}
	if got := len(lib.List()); got != 4 {
		t.Fatalf("List() length = %d, want 4", got)
	}
}

func TestOrphanGC_DeletesUnreferencedAfterAge(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	data := []byte("orphan")
	_, entry := uploadFixture(t, lib, "orphan.bin", data)
	rawPath := rawFileFor(t, lib, data)

	now = now.Add(31 * 24 * time.Hour)
	deleted, err := lib.OrphanGC(library.OrphanGCConfig{Enabled: true})
	if err != nil {
		t.Fatalf("OrphanGC() error = %v", err)
	}
	if len(deleted) != 1 || mediaID(t, deleted[0]) != mediaID(t, entry) {
		t.Fatalf("OrphanGC() deleted = %#v", deleted)
	}
	if _, err := os.Stat(rawPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("raw file still exists after GC: %v", err)
	}
	if _, _, err := lib.Read(mediaID(t, entry)); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("Read(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestOrphanGC_OperatorDisabled_DoesNotDelete(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	data := []byte("preserved")
	_, entry := uploadFixture(t, lib, "preserved.bin", data)
	rawPath := rawFileFor(t, lib, data)

	now = now.Add(365 * 24 * time.Hour)
	deleted, err := lib.OrphanGC(library.OrphanGCConfig{Enabled: false, MaxAge: time.Hour})
	if err != nil {
		t.Fatalf("OrphanGC(disabled) error = %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("OrphanGC(disabled) deleted %d entries", len(deleted))
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("raw file removed while GC disabled: %v", err)
	}
	if _, _, err := lib.Read(mediaID(t, entry)); err != nil {
		t.Fatalf("Read(preserved) error = %v", err)
	}
}

func TestWorkspaceLibrary_Refcount_DrivesGC(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	data := []byte("referenced")
	_, entry := uploadFixture(t, lib, "referenced.bin", data)
	id := mediaID(t, entry)
	rawPath := rawFileFor(t, lib, data)

	count, incrementErr := lib.IncrementRefcount(id)
	if incrementErr != nil || count != 1 {
		t.Fatalf("IncrementRefcount() = %d, %v", count, incrementErr)
	}
	now = now.Add(31 * 24 * time.Hour)
	deleted, gcErr := lib.OrphanGC(library.OrphanGCConfig{Enabled: true})
	if gcErr != nil {
		t.Fatalf("OrphanGC(referenced) error = %v", gcErr)
	}
	if len(deleted) != 0 {
		t.Fatalf("OrphanGC(referenced) deleted %#v", deleted)
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("referenced raw file removed: %v", err)
	}

	if err := lib.Store(); err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	reopened, newErr := library.New(home, workspaceID, library.WithClock(func() time.Time { return now }))
	if newErr != nil {
		t.Fatalf("New(reopen) error = %v", newErr)
	}
	persisted, refcountErr := reopened.Refcount(id)
	if refcountErr != nil || persisted != 1 {
		t.Fatalf("reopened Refcount() = %d, %v", persisted, refcountErr)
	}

	count, decrementErr := reopened.DecrementRefcount(id)
	if decrementErr != nil || count != 0 {
		t.Fatalf("DecrementRefcount() = %d, %v", count, decrementErr)
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("DecrementRefcount immediately removed raw file: %v", err)
	}
	if _, err := reopened.DecrementRefcount(id); !errors.Is(err, library.ErrRefcountUnderflow) {
		t.Fatalf("second DecrementRefcount() error = %v, want ErrRefcountUnderflow", err)
	}
}

func TestWorkspaceLibrary_ManifestRefcount_DrivesDeferredGC(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	data := []byte("deferred")
	_, entry := uploadFixture(t, lib, "deferred.bin", data)
	id := mediaID(t, entry)
	rawPath := rawFileFor(t, lib, data)

	if _, err := lib.IncrementRefcount(id); err != nil {
		t.Fatalf("IncrementRefcount() error = %v", err)
	}
	now = now.Add(90 * 24 * time.Hour)
	if _, err := lib.DecrementRefcount(id); err != nil {
		t.Fatalf("DecrementRefcount() error = %v", err)
	}
	now = now.Add(29 * 24 * time.Hour)
	deleted, gcErr := lib.OrphanGC(library.OrphanGCConfig{Enabled: true})
	if gcErr != nil {
		t.Fatalf("OrphanGC(29d) error = %v", gcErr)
	}
	if len(deleted) != 0 {
		t.Fatalf("OrphanGC(29d) deleted %#v", deleted)
	}
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("deferred raw file removed before 30d: %v", err)
	}

	now = now.Add(2 * 24 * time.Hour)
	deleted, gcErr = lib.OrphanGC(library.OrphanGCConfig{Enabled: true})
	if gcErr != nil {
		t.Fatalf("OrphanGC(31d) error = %v", gcErr)
	}
	if len(deleted) != 1 || mediaID(t, deleted[0]) != id {
		t.Fatalf("OrphanGC(31d) deleted %#v", deleted)
	}
}

func TestWorkspaceLibrary_RejectsToolOutputPersistence(t *testing.T) {
	now := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)
	lib, _, _ := newWorkspaceLibrary(t, &now)
	_, _, err := lib.Upload("screenshot.png", gen.ToolOutput, bytes.NewReader([]byte("agent output")))
	if !errors.Is(err, library.ErrSourceNotAllowed) {
		t.Fatalf("Upload(tool_output) error = %v, want ErrSourceNotAllowed", err)
	}
	if got := len(lib.List()); got != 0 {
		t.Fatalf("tool output persisted %d entries", got)
	}
}

func TestWorkspaceLibrary_ResolveWithWorkspaceSignature(t *testing.T) {
	now := time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC)
	lib, _, workspaceID := newWorkspaceLibrary(t, &now)
	data := []byte("resolved")
	ref, entry := uploadFixture(t, lib, "resolved.bin", data)

	got, resolvedEntry, resolveErr := lib.ResolveWithWorkspace(ref, &workspaceID)
	if resolveErr != nil {
		t.Fatalf("ResolveWithWorkspace() error = %v", resolveErr)
	}
	if !bytes.Equal(got, data) || mediaID(t, resolvedEntry) != mediaID(t, entry) {
		t.Fatalf("ResolveWithWorkspace() = %q, %#v", got, resolvedEntry)
	}
	otherWorkspaceID := uuid.NewString()
	crossData, _, crossErr := lib.ResolveWithWorkspace(ref, &otherWorkspaceID)
	if !errors.Is(crossErr, library.ErrWorkspaceMismatch) || crossData != nil {
		t.Fatalf("ResolveWithWorkspace(cross-workspace) = %q, %v", crossData, crossErr)
	}
	nilContextData, _, nilContextErr := lib.ResolveWithWorkspace(ref, nil)
	if !errors.Is(nilContextErr, library.ErrWorkspaceContextRequired) || nilContextData != nil {
		t.Fatalf("ResolveWithWorkspace(nil) = %q, %v", nilContextData, nilContextErr)
	}
}

func TestWorkspaceLibrary_WorkspaceDeleteHookSignature(t *testing.T) {
	now := time.Date(2026, 7, 23, 17, 0, 0, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	_, entry := uploadFixture(t, lib, "delete-me.bin", []byte("delete me"))

	if err := workspace.WorkspaceDeleteHook(home, workspaceID, "test-user", nil); err != nil {
		t.Fatalf("WorkspaceDeleteHook() error = %v", err)
	}
	reopened, newErr := library.New(home, workspaceID, library.WithClock(func() time.Time { return now }))
	if newErr != nil {
		t.Fatalf("New(reopen) error = %v", newErr)
	}
	if got := len(reopened.List()); got != 0 {
		t.Fatalf("reopened manifest has %d entries after WorkspaceDeleteHook: %#v", got, reopened.List())
	}
	if _, _, readErr := reopened.Read(mediaID(t, entry)); !errors.Is(readErr, library.ErrNotFound) {
		t.Fatalf("reopened Read() error = %v, want ErrNotFound", readErr)
	}
	if err := workspace.WorkspaceDeleteHook(home, workspaceID, "test-user", nil); err != nil {
		t.Fatalf("WorkspaceDeleteHook() second call error = %v", err)
	}
	err := workspace.WorkspaceDeleteHook(home, "../escape", "test-user", nil)
	if !errors.Is(err, workspace.ErrInvalidWorkspaceID) {
		t.Fatalf("WorkspaceDeleteHook(traversal) error = %v", err)
	}
}

// newAuditLogger spins up an audit Logger writing to a t.TempDir()-rooted
// audit.jsonl file and returns the logger + the path to read records back
// from. Mirrors the helper in pkg/audit/events_test.go; duplicated here so
// the library test package does not have to import the internal audit
// test helpers.
func newAuditLogger(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
		RedactEnabled: false,
	})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return logger, filepath.Join(dir, "audit.jsonl")
}

// readAuditRecords drains the JSONL audit log into typed entries.
func readAuditRecords(t *testing.T, path string) []audit.Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	out := []audit.Entry{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e audit.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal audit entry %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

// TestWorkspaceLibrary_AuditSingleFileDelete — FR-008.
//
// Explicitly deleting one workspace-library file emits a media.delete
// audit entry whose Details carry the actor, workspace_id, media_id,
// filename, bytes_freed, and mime of the deleted file. The library's
// Delete API is responsible for the on-disk work; the audit emission
// lives in the caller (the library is kept audit-package-agnostic so the
// dep graph stays acyclic).
func TestWorkspaceLibrary_AuditSingleFileDelete(t *testing.T) {
	now := time.Date(2026, 7, 23, 18, 0, 0, 0, time.UTC)
	lib, _, workspaceID := newWorkspaceLibrary(t, &now)
	auditor, path := newAuditLogger(t)

	_, keep := uploadFixture(t, lib, "keep.bin", []byte("stay"))
	_, dropEntry := uploadFixture(t, lib, "drop.bin", []byte("bye"))
	dropID := mediaID(t, dropEntry)
	dropSize := int64(len("bye"))

	if _, _, err := lib.Read(dropID); err != nil {
		t.Fatalf("Read(drop) before delete: %v", err)
	}

	deleted, err := lib.Delete(dropID)
	if err != nil {
		t.Fatalf("Delete(%q) error = %v", dropID, err)
	}
	if mediaID(t, deleted) != dropID {
		t.Fatalf("Delete() returned id=%q, want %q", mediaID(t, deleted), dropID)
	}
	if _, _, readErr := lib.Read(dropID); !errors.Is(readErr, library.ErrNotFound) {
		t.Fatalf("Read(deleted) error = %v, want ErrNotFound", readErr)
	}
	if _, _, err := lib.Read(mediaID(t, keep)); err != nil {
		t.Fatalf("Read(keep) after delete: %v", err)
	}
	if got := len(lib.List()); got != 1 {
		t.Fatalf("List() length after single delete = %d, want 1", got)
	}

	if err := auditor.Log(&audit.Entry{
		Event:    audit.EventMediaDelete,
		Decision: audit.DecisionAllow,
		Details: map[string]any{
			"actor":        "test-user",
			"workspace_id": workspaceID,
			"media_id":     dropID,
			"filename":     deleted.Filename,
			"bytes_freed":  dropSize,
			"mime":         derefString(deleted.Mime),
		},
	}); err != nil {
		t.Fatalf("audit.Log(media.delete): %v", err)
	}
	if err := auditor.Close(); err != nil {
		t.Fatalf("auditor.Close: %v", err)
	}

	records := readAuditRecords(t, path)
	var match *audit.Entry
	for i := range records {
		if records[i].Event == string(audit.EventMediaDelete) {
			match = &records[i]
			break
		}
	}
	if match == nil {
		t.Fatalf("no media.delete audit record found in %d entries", len(records))
	}
	if match.Decision != string(audit.DecisionAllow) {
		t.Errorf("Decision = %q, want allow", match.Decision)
	}
	if match.Details["actor"] != "test-user" {
		t.Errorf("Details[actor] = %v, want test-user", match.Details["actor"])
	}
	if match.Details["workspace_id"] != workspaceID {
		t.Errorf("Details[workspace_id] = %v, want %q", match.Details["workspace_id"], workspaceID)
	}
	if match.Details["media_id"] != dropID {
		t.Errorf("Details[media_id] = %v, want %q", match.Details["media_id"], dropID)
	}
	if match.Details["filename"] != dropEntry.Filename {
		t.Errorf("Details[filename] = %v, want %q", match.Details["filename"], dropEntry.Filename)
	}
	if got, _ := match.Details["bytes_freed"].(float64); int64(got) != dropSize {
		t.Errorf("Details[bytes_freed] = %v, want %d", match.Details["bytes_freed"], dropSize)
	}
	if match.Timestamp.IsZero() {
		t.Errorf("Timestamp is zero; audit Log should populate it")
	}
}

// TestWorkspaceLibrary_AuditCascadeDelete — FR-009.
//
// Workspace deletion cascades the media library: WorkspaceDeleteHook
// calls library.CascadeDelete and emits ONE media.cascade_delete audit
// event whose Details carry the actor, workspace_id, media_ids list
// (one entry per deleted file, in stable id order), parallel filenames
// list, and the sum of bytes_freed. The test exercises the full path:
// upload 3 files, run the hook, assert the audit event and the
// post-cascade on-disk state.
func TestWorkspaceLibrary_AuditCascadeDelete(t *testing.T) {
	now := time.Date(2026, 7, 23, 18, 30, 0, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	auditor, path := newAuditLogger(t)

	files := []struct {
		name string
		data []byte
	}{
		{"alpha.bin", []byte("alpha-payload")},
		{"bravo.bin", []byte("bravo-payload")},
		{"charlie.bin", []byte("charlie-payload-longer")},
	}
	expectedIDs := make(map[string]string, len(files))
	expectedSize := int64(0)
	for _, f := range files {
		_, entry := uploadFixture(t, lib, f.name, f.data)
		expectedIDs[f.name] = mediaID(t, entry)
		expectedSize += int64(len(f.data))
	}

	if err := workspace.WorkspaceDeleteHook(home, workspaceID, "test-user", auditor); err != nil {
		t.Fatalf("WorkspaceDeleteHook() error = %v", err)
	}
	if err := auditor.Close(); err != nil {
		t.Fatalf("auditor.Close: %v", err)
	}

	records := readAuditRecords(t, path)
	var cascade *audit.Entry
	for i := range records {
		if records[i].Event == string(audit.EventMediaCascadeDelete) {
			cascade = &records[i]
			break
		}
	}
	if cascade == nil {
		t.Fatalf("no media.cascade_delete audit record found in %d entries", len(records))
	}

	if got := len(cascade.Details["media_ids"].([]any)); got != len(files) {
		t.Fatalf("Details[media_ids] length = %d, want %d", got, len(files))
	}
	mediaIDs := make([]string, 0, len(files))
	for _, raw := range cascade.Details["media_ids"].([]any) {
		mediaIDs = append(mediaIDs, raw.(string))
	}
	sortedMediaIDs := append([]string(nil), mediaIDs...)
	sortStrings(sortedMediaIDs)
	wantMediaIDs := make([]string, 0, len(files))
	for _, f := range files {
		wantMediaIDs = append(wantMediaIDs, expectedIDs[f.name])
	}
	sortStrings(wantMediaIDs)
	for i, got := range sortedMediaIDs {
		if got != wantMediaIDs[i] {
			t.Errorf("Details[media_ids][%d] = %q, want %q (sorted)", i, got, wantMediaIDs[i])
		}
	}

	filenames := make([]string, 0, len(files))
	for _, raw := range cascade.Details["filenames"].([]any) {
		filenames = append(filenames, raw.(string))
	}
	if got := len(filenames); got != len(files) {
		t.Fatalf("Details[filenames] length = %d, want %d", got, len(files))
	}
	for _, f := range files {
		found := false
		for _, got := range filenames {
			if got == f.name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("filenames missing %q: got %v", f.name, filenames)
		}
	}

	if got, _ := cascade.Details["bytes_freed"].(float64); int64(got) != expectedSize {
		t.Errorf("Details[bytes_freed] = %v, want %d", cascade.Details["bytes_freed"], expectedSize)
	}
	if cascade.Details["actor"] != "test-user" {
		t.Errorf("Details[actor] = %v, want test-user", cascade.Details["actor"])
	}
	if cascade.Details["workspace_id"] != workspaceID {
		t.Errorf("Details[workspace_id] = %v, want %q", cascade.Details["workspace_id"], workspaceID)
	}
	if got, _ := cascade.Details["count"].(float64); int(got) != len(files) {
		t.Errorf("Details[count] = %v, want %d", cascade.Details["count"], len(files))
	}

	reopened, newErr := library.New(home, workspaceID, library.WithClock(func() time.Time { return now }))
	if newErr != nil {
		t.Fatalf("New(reopen) error = %v", newErr)
	}
	if got := len(reopened.List()); got != 0 {
		t.Fatalf("reopened manifest has %d entries after cascade-delete: %#v", got, reopened.List())
	}
	for _, id := range expectedIDs {
		if _, _, err := reopened.Read(id); !errors.Is(err, library.ErrNotFound) {
			t.Errorf("reopened Read(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}

// TestWorkspaceLibrary_AuditEventShape — FR-033.
//
// Verifies the audit event shape contract for BOTH media.delete and
// media.cascade_delete against the spec's FR-033 field list:
//
//	action (event top-level: "media.delete" | "media.cascade_delete"),
//	actor, workspace_id, media_id (single) or media_ids (list, for
//	cascade), filenames, bytes_freed, timestamp.
//
// The audit wire format puts `event` at the top level (the "action"
// discriminator) and the per-event fields under Details; this test
// asserts BOTH that contract mapping AND that every required FR-033
// field is present and correctly typed.
func TestWorkspaceLibrary_AuditEventShape(t *testing.T) {
	now := time.Date(2026, 7, 23, 19, 0, 0, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	auditor, path := newAuditLogger(t)

	_, single := uploadFixture(t, lib, "shape-single.bin", []byte("single-payload"))
	singleID := mediaID(t, single)

	if _, err := lib.Delete(singleID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := auditor.Log(&audit.Entry{
		Event:     audit.EventMediaDelete,
		Decision:  audit.DecisionAllow,
		Timestamp: now,
		Details: map[string]any{
			"actor":        "shape-test",
			"workspace_id": workspaceID,
			"media_id":     singleID,
			"filename":     single.Filename,
			"bytes_freed":  int64(len("single-payload")),
		},
	}); err != nil {
		t.Fatalf("audit.Log(media.delete): %v", err)
	}

	_, a := uploadFixture(t, lib, "shape-a.bin", []byte("aaaaaaaaaa"))
	_, b := uploadFixture(t, lib, "shape-b.bin", []byte("bbbbbbbbbb"))
	aID, bID := mediaID(t, a), mediaID(t, b)

	if err := workspace.WorkspaceDeleteHook(home, workspaceID, "shape-test", auditor); err != nil {
		t.Fatalf("WorkspaceDeleteHook() error = %v", err)
	}
	if err := auditor.Close(); err != nil {
		t.Fatalf("auditor.Close: %v", err)
	}

	records := readAuditRecords(t, path)

	var singleRecord, cascadeRecord *audit.Entry
	for i := range records {
		switch records[i].Event {
		case string(audit.EventMediaDelete):
			singleRecord = &records[i]
		case string(audit.EventMediaCascadeDelete):
			cascadeRecord = &records[i]
		}
	}
	if singleRecord == nil {
		t.Fatalf("no media.delete record found in %d entries", len(records))
	}
	if cascadeRecord == nil {
		t.Fatalf("no media.cascade_delete record found in %d entries", len(records))
	}

	if singleRecord.Timestamp.IsZero() {
		t.Errorf("media.delete Timestamp is zero")
	}
	if !singleRecord.Timestamp.Equal(now) {
		t.Errorf("media.delete Timestamp = %v, want %v", singleRecord.Timestamp, now)
	}
	if singleRecord.Details["actor"] != "shape-test" {
		t.Errorf("media.delete actor = %v, want shape-test", singleRecord.Details["actor"])
	}
	if singleRecord.Details["workspace_id"] != workspaceID {
		t.Errorf("media.delete workspace_id = %v, want %q", singleRecord.Details["workspace_id"], workspaceID)
	}
	if singleRecord.Details["media_id"] != singleID {
		t.Errorf("media.delete media_id = %v, want %q", singleRecord.Details["media_id"], singleID)
	}
	if _, ok := singleRecord.Details["media_ids"]; ok {
		t.Errorf("media.delete should NOT carry media_ids (single-file shape)")
	}
	if singleRecord.Details["filename"] != single.Filename {
		t.Errorf("media.delete filename = %v, want %q", singleRecord.Details["filename"], single.Filename)
	}
	if got, _ := singleRecord.Details["bytes_freed"].(float64); int64(got) != int64(len("single-payload")) {
		t.Errorf("media.delete bytes_freed = %v, want %d", singleRecord.Details["bytes_freed"], len("single-payload"))
	}

	if cascadeRecord.Timestamp.IsZero() {
		t.Errorf("media.cascade_delete Timestamp is zero")
	}
	if cascadeRecord.Details["actor"] != "shape-test" {
		t.Errorf("media.cascade_delete actor = %v, want shape-test", cascadeRecord.Details["actor"])
	}
	if cascadeRecord.Details["workspace_id"] != workspaceID {
		t.Errorf("media.cascade_delete workspace_id = %v, want %q", cascadeRecord.Details["workspace_id"], workspaceID)
	}
	if _, ok := cascadeRecord.Details["media_id"]; ok {
		t.Errorf("media.cascade_delete should NOT carry media_id (cascade shape)")
	}
	mediaIDs := cascadeRecord.Details["media_ids"].([]any)
	if len(mediaIDs) != 2 {
		t.Fatalf("media.cascade_delete media_ids length = %d, want 2", len(mediaIDs))
	}
	gotIDs := []string{mediaIDs[0].(string), mediaIDs[1].(string)}
	sortStrings(gotIDs)
	wantIDs := []string{aID, bID}
	sortStrings(wantIDs)
	for i, got := range gotIDs {
		if got != wantIDs[i] {
			t.Errorf("media.cascade_delete media_ids[%d] = %q, want %q", i, got, wantIDs[i])
		}
	}
	filenames := cascadeRecord.Details["filenames"].([]any)
	if len(filenames) != 2 {
		t.Fatalf("media.cascade_delete filenames length = %d, want 2", len(filenames))
	}
	wantBytes := int64(len("aaaaaaaaaa") + len("bbbbbbbbbb"))
	if got, _ := cascadeRecord.Details["bytes_freed"].(float64); int64(got) != wantBytes {
		t.Errorf("media.cascade_delete bytes_freed = %v, want %d", cascadeRecord.Details["bytes_freed"], wantBytes)
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// sortStrings is a tiny local sort.Strings shim that avoids pulling the
// sort package into the imports (the test file already imports many
// stdlib packages, and `sort.Strings` is one line inlined here to keep
// the diff focused on the audit-event assertions).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
