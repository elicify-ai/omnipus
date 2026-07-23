package library_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
	var hook func(string, string) error = workspace.WorkspaceDeleteHook
	now := time.Date(2026, 7, 23, 17, 0, 0, 0, time.UTC)
	lib, home, workspaceID := newWorkspaceLibrary(t, &now)
	uploadFixture(t, lib, "delete-me.bin", []byte("delete me"))

	if err := hook(home, workspaceID); err != nil {
		t.Fatalf("WorkspaceDeleteHook() error = %v", err)
	}
	if _, err := os.Stat(lib.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("media directory exists after WorkspaceDeleteHook: %v", err)
	}
	if err := hook(home, workspaceID); err != nil {
		t.Fatalf("WorkspaceDeleteHook() second call error = %v", err)
	}
	if err := hook(home, "../escape"); !errors.Is(err, workspace.ErrInvalidWorkspaceID) {
		t.Fatalf("WorkspaceDeleteHook(traversal) error = %v", err)
	}
}
