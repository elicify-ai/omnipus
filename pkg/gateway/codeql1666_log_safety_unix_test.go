//go:build linux || darwin

package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	"github.com/stretchr/testify/require"
)

// #1666 F2 fixtures that plant control-bearing filesystem paths (session ids
// and workspace ids in real directory names), which Windows forbids — the
// same Linux/macOS scope as the #1618 stored-ref fixtures. Every fault is a
// real filesystem state that works under any uid: a directory swapped for the
// blob (the #1618 dir trick) and a closed unix-socket inode (open(2) on a
// socket fails with ENXIO even as root, where permission tricks are void).

// codeQL1666ShortTempDir is a shorter t.TempDir: the unix-socket fixture
// plants its inode at the resolved file's own path, and a unix bind fails
// with EINVAL once the address exceeds sockaddr_un's 108-byte limit — a
// limit t.TempDir()'s test-name-derived prefix can cross.
func codeQL1666ShortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "c1666-*")
	require.NoError(t, err, "fixture: short temp dir")
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// codeQL1666PlantUnopenableInode replaces the regular file at path with a
// unix-socket inode. EvalSymlinks/Lstat succeed on it, but os.Open fails with
// ENXIO for any uid — the same privilege-independent class the #1618 dir
// trick provides for reads, here for the open() failure the serve-failed log
// sites need. The probe keeps the fixture loud if an environment ever
// returns a different class.
func codeQL1666PlantUnopenableInode(t *testing.T, path string) {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(context.Background(), "unix", path)
	require.NoError(t, err, "fixture: plant a real unix-socket inode")
	if unix, ok := listener.(*net.UnixListener); ok {
		// The socket file must outlive the listener: Go unlinks it on Close
		// by default, and the planted inode is the fixture's fault.
		unix.SetUnlinkOnClose(false)
	}
	listener.Close()

	file, openErr := os.Open(path)
	if file != nil {
		file.Close()
	}
	require.Error(t, openErr, "fixture: the planted inode must be unopenable")
	require.False(t, errors.Is(openErr, os.ErrNotExist),
		"fixture: the open fault must be the non-not-exist class the serve-failed sites log")
}

func codeQL1666MediaByRefRequest(t *testing.T, workspaceID, mediaID string) *http.Request {
	t.Helper()
	path := "/api/v1/media/workspace/" + url.PathEscape(workspaceID) + "/" + url.PathEscape(mediaID)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	require.Equal(t, "/api/v1/media/workspace/"+workspaceID+"/"+mediaID, request.URL.Path,
		"fixture: the real request parser must decode the control-bearing path segments")
	return request
}

func TestCodeQL1666_ServeUploadServeFailedLogIsControlFree(t *testing.T) {
	// #1666 F2, HandleServeUpload's slog.Error ("rest: uploads: serve
	// failed"): raw session_id and raw error. The id rides the real request
	// path (validateEntityID permits every case below), through real path
	// resolution, into an open() that fails on the planted socket inode.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			api := &restAPI{homePath: codeQL1666ShortTempDir(t)}
			sessionID := "session-1666" + sample.value + "-item"
			require.NoError(t, validateEntityID(sessionID),
				"fixture: validation must let the id reach the log site")
			dir := filepath.Join(api.homePath, "uploads", sessionID)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			codeQL1666PlantUnopenableInode(t, filepath.Join(dir, "item.txt"))

			request := httptest.NewRequest(http.MethodGet,
				"/api/v1/uploads/"+url.PathEscape(sessionID)+"/item.txt", nil)
			require.Equal(t, "/api/v1/uploads/"+sessionID+"/item.txt", request.URL.Path,
				"fixture: the real request parser must decode the control-bearing path segment")
			response := httptest.NewRecorder()
			api.HandleServeUpload(response, request)

			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"could not read file"}`, response.Body.String())
			record := codeQL1618SingleRecord(t, logs, "rest: uploads: serve failed")
			codeQL1666AssertRecordSafe(t, record, "session_id", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_MediaByRefNotFoundLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's 404 branch through the real split route: a real
	// workspace library (created with the control-bearing workspace id on
	// disk) behind the production-shaped provider closure, an absent media id.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			workspaceID := "ws-1666" + sample.value + "-ws"
			mediaID := "media-1666" + sample.value + "-item"
			require.NoError(t, validateEntityID(workspaceID), "fixture: the workspace id must reach the log site")
			require.NoError(t, validateEntityID(mediaID), "fixture: the media id must reach the log site")
			lib, err := library.New(t.TempDir(), workspaceID)
			require.NoError(t, err, "fixture: the library must accept the workspace id (safeWorkspaceDir rejects only /, \\ and ..)")
			store := media.NewFileMediaStore()
			store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
				return lib, nil // gateway_boot.go's production shape, minus the cache
			})
			api := &restAPI{homePath: t.TempDir(), mediaStore: store}

			response := httptest.NewRecorder()
			api.HandleMediaByRef(response, codeQL1666MediaByRefRequest(t, workspaceID, mediaID))

			require.Equal(t, http.StatusNotFound, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"media not found"}`, response.Body.String())
			record := codeQL1618SingleRecord(t, logs, "rest: media ref not found")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_MediaResolveFailedLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's catch-all branch ("rest: media: resolve
	// failed"): the wired provider itself errors — the exact failure shape
	// production boots with when no workspace library is cached
	// (gateway_boot.go's "workspace library unavailable"). Real store, real
	// guard, real provider seam.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			workspaceID := "ws-1666" + sample.value + "-ws"
			require.NoError(t, validateEntityID(workspaceID), "fixture: the workspace id must reach the log site")
			store := media.NewFileMediaStore()
			store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
				return nil, fmt.Errorf("workspace library unavailable for %q", id)
			})
			api := &restAPI{homePath: t.TempDir(), mediaStore: store}

			response := httptest.NewRecorder()
			api.HandleMediaByRef(response, codeQL1666MediaByRefRequest(t, workspaceID, "media-1666-id"))

			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"internal server error"}`, response.Body.String())
			record := codeQL1618SingleRecord(t, logs, "rest: media: resolve failed")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_MediaStrandedLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's ErrEntryStranded branch: a real compound
	// rollback-of-rollback built with the library's own test seams
	// (WithManifestWriter + WithFileRenamer), the same construction
	// pkg/media/library's stranded regression tests use. The workspace id
	// carries the control into logRef; the media id stays a real uploaded
	// uuid. The library's internal markStrandedLocked WarnCF is pkg/logger
	// output outside the #1666 site list; the capture sweep covers the
	// gateway-side slog records only.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			home := t.TempDir()
			workspaceID := "ws-1666" + sample.value + "-ws"
			require.NoError(t, validateEntityID(workspaceID), "fixture: the workspace id must reach the log site")
			failPersist, failRollback := new(bool), new(bool)
			lib, err := library.New(home, workspaceID,
				library.WithManifestWriter(func(path string, data []byte, perm os.FileMode) error {
					if *failPersist {
						return errors.New("codeql1666: manifest persist failed")
					}
					return os.WriteFile(path, data, perm)
				}),
				library.WithFileRenamer(func(oldPath, newPath string) error {
					if *failRollback && strings.HasPrefix(filepath.Base(oldPath), ".delete-") {
						return errors.New("codeql1666: rollback rename failed")
					}
					return os.Rename(oldPath, newPath)
				}))
			require.NoError(t, err)
			uploadRef, _, err := lib.Upload("doc.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader("codeql1666 stranded fixture bytes"))
			require.NoError(t, err)
			_, mediaID, ok := media.ParseWorkspaceRef(uploadRef)
			require.True(t, ok, "fixture: the uploaded ref must parse to its id")
			*failPersist, *failRollback = true, true
			_, deleteErr := lib.Delete(mediaID)
			require.True(t, errors.Is(deleteErr, library.ErrEntryStranded),
				"fixture: the compound rollback must strand the id, as in the library's own regression tests")

			store := media.NewFileMediaStore()
			store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
				return lib, nil
			})
			api := &restAPI{homePath: home, mediaStore: store}

			response := httptest.NewRecorder()
			api.HandleMediaByRef(response, codeQL1666MediaByRefRequest(t, workspaceID, mediaID))

			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"media entry is in an inconsistent state"}`, response.Body.String())
			record := codeQL1618SingleRecord(t, logs, "rest: media: entry stranded (manifest/disk diverged)")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_MediaNotAFileLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's not-a-file branch: the real blob swapped for a
	// directory after a real upload — resolve is path-only, serveLibraryPath
	// stats it. Privilege-independent, like the #1618 dir trick.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			home := t.TempDir()
			workspaceID := "ws-1666" + sample.value + "-ws"
			require.NoError(t, validateEntityID(workspaceID), "fixture: the workspace id must reach the log site")
			lib, err := library.New(home, workspaceID)
			require.NoError(t, err)
			uploadRef, _, err := lib.Upload("doc.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader("codeql1666 not-a-file fixture bytes"))
			require.NoError(t, err)
			_, mediaID, ok := media.ParseWorkspaceRef(uploadRef)
			require.True(t, ok, "fixture: the uploaded ref must parse to its id")
			blob := filepath.Join(lib.Path(), mediaID)
			require.NoError(t, os.Remove(blob))
			require.NoError(t, os.Mkdir(blob, 0o700))

			store := media.NewFileMediaStore()
			store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
				return lib, nil
			})
			api := &restAPI{homePath: home, mediaStore: store}

			response := httptest.NewRecorder()
			api.HandleMediaByRef(response, codeQL1666MediaByRefRequest(t, workspaceID, mediaID))

			require.Equal(t, http.StatusNotFound, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"media not found"}`, response.Body.String())
			record := codeQL1618SingleRecord(t, logs, "rest: media: resolved path is not a readable file")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_MediaServeFailedLogIsControlFree(t *testing.T) {
	// #1666 F2, serveMedia's catch-all serve branch ("rest: media: serve
	// failed"): the resolved blob is a socket inode whose open() fails with
	// ENXIO for any uid.
	for _, sample := range codeQL1618ControlCases(false) {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			home := codeQL1666ShortTempDir(t)
			workspaceID := "ws-1666" + sample.value + "-ws"
			require.NoError(t, validateEntityID(workspaceID), "fixture: the workspace id must reach the log site")
			lib, err := library.New(home, workspaceID)
			require.NoError(t, err)
			uploadRef, _, err := lib.Upload("doc.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader("codeql1666 serve-failed fixture bytes"))
			require.NoError(t, err)
			_, mediaID, ok := media.ParseWorkspaceRef(uploadRef)
			require.True(t, ok, "fixture: the uploaded ref must parse to its id")
			require.NoError(t, os.Remove(filepath.Join(lib.Path(), mediaID)))
			codeQL1666PlantUnopenableInode(t, filepath.Join(lib.Path(), mediaID))

			store := media.NewFileMediaStore()
			store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
				return lib, nil
			})
			api := &restAPI{homePath: home, mediaStore: store}

			response := httptest.NewRecorder()
			api.HandleMediaByRef(response, codeQL1666MediaByRefRequest(t, workspaceID, mediaID))

			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"internal server error"}`, response.Body.String())
			record := codeQL1618SingleRecord(t, logs, "rest: media: serve failed")
			codeQL1666AssertRecordSafe(t, record, "ref", "error")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_UploadStageErrorCFLogHasSafeMediaID(t *testing.T) {
	// #1666 F2, the staging logger.ErrorCF line's media_id and error fields,
	// through the landed #1618 stored-ref fixture: a manifest-persisted
	// control-bearing id the real loader accepts, then a real corrupted blob
	// so staging fails at the integrity check. The "rest: server-side
	// failure" slog record jsonServerFailure emits is part of the same call,
	// so the capture sweep asserts it too. Excludes invalid UTF-8: a JSON
	// manifest key cannot carry it (stated gap; covered at the raw sites).
	for _, sample := range codeQL1666ManifestSafeCases() {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			readFile := codeQL1666CaptureFileLog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1666"
			lib, ref, projection, _ := codeQL1618StoredRefLibrary(t, api.homePath, workspaceID, sample.value)
			_, loadedID, ok := media.ParseWorkspaceRef(ref)
			require.True(t, ok)
			require.NoError(t, os.WriteFile(filepath.Join(lib.Path(), loadedID), []byte("corrupted fixture bytes"), 0o600),
				"fixture: corrupt the real blob so staging fails at the integrity check")

			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}
			flow := upload.finishWorkspaceUpload("item.txt", ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"could not stage uploaded file for agent access: server storage or configuration failed. Check disk space and permissions, then retry. Details are in the server log."}`,
				response.Body.String())

			record := codeQL1666SingleFileRecord(t, readFile(),
				"upload: could not stage workspace library copy for agent access")
			codeQL1666AssertFileRecordSafe(t, record, "media_id", "filename", "error", "workspace_id")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}

func TestCodeQL1666_UploadAssociationErrorCFLogHasSafeMediaID(t *testing.T) {
	// #1666 F2, the association logger.ErrorCF line: the manifest-writer seam
	// fails the real SetLibraryPath persist, whose error text
	// (associateLibraryFile) embeds the raw media id — so both the media_id
	// field and the error field carry the control today. Same invalid-UTF-8
	// exclusion as the stage fixture; same double capture (file sink + slog).
	for _, sample := range codeQL1666ManifestSafeCases() {
		t.Run(sample.name, func(t *testing.T) {
			logs := captureRedactionSlog(t)
			readFile := codeQL1666CaptureFileLog(t)
			api := &restAPI{homePath: t.TempDir()}
			const workspaceID = "ws-codeql1666"
			lib, ref, projection, _ := codeQL1618StoredRefLibrary(t, api.homePath, workspaceID, sample.value)

			response := httptest.NewRecorder()
			upload := &restAPIHandleUpload{a: api, w: response, workspaceID: workspaceID, workspaceLib: lib}
			flow := upload.finishWorkspaceUpload("item.txt", ref, projection, nil)
			require.Equal(t, restAPIHandleUploadReturn, flow)
			require.Equal(t, http.StatusInternalServerError, response.Code)
			// characterization test: the issue pins this response as unchanged.
			require.JSONEq(t, `{"error":"could not record the uploaded file: server storage or configuration failed. Check disk space and permissions, then retry. Details are in the server log."}`,
				response.Body.String())

			record := codeQL1666SingleFileRecord(t, readFile(),
				"upload: could not record the library file of the upload")
			codeQL1666AssertFileRecordSafe(t, record, "media_id", "error", "workspace_id")
			codeQL1666AssertCaptureControlFree(t, logs)
		})
	}
}
