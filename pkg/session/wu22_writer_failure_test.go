package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: L1-AMENDMENTS-20261013.md §4.2: both implementations return the
// original append/validation/meta error; Save is synchronous, not sticky.
// No compile-red: invoke the real method even when its old signature is void.
func wu22WriterCall(t *testing.T, writer SessionStore, method string, args ...any) (error, bool) {
	t.Helper()
	values := make([]reflect.Value, len(args))
	for i, arg := range args {
		values[i] = reflect.ValueOf(arg)
	}
	out := reflect.ValueOf(writer).MethodByName(method).Call(values)
	if len(out) == 0 {
		return nil, false
	}
	require.Len(t, out, 1, "amendment §4.2: exactly one error result")
	require.Equal(t, reflect.TypeFor[error](), out[0].Type())
	if out[0].IsNil() {
		return nil, true
	}
	err, ok := out[0].Interface().(error)
	require.True(t, ok)
	return err, true
}

func wu22Stores(t *testing.T, fn func(*testing.T, SessionStore, string, string)) {
	t.Helper()
	for _, implementation := range []string{"archiveBackend", "UnifiedStore"} {
		t.Run(implementation, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("OMNIPUS_HOME", home)
			base := filepath.Join(home, "sessions")
			var writer SessionStore
			key := "wu22-session"
			if implementation == "archiveBackend" {
				writer = newArchiveBackend(base)
			} else {
				store, err := NewUnifiedStoreWithHome(base, home)
				require.NoError(t, err)
				meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
				require.NoError(t, err)
				key = meta.ID
				writer = store
			}
			t.Cleanup(func() { require.NoError(t, writer.Close()) })
			fn(t, writer, base, key)
		})
	}
}

func TestWU22WriterArchiveFailureReturnsCause(t *testing.T) {
	wu22Stores(t, func(t *testing.T, writer SessionStore, base, key string) {
		for _, method := range []string{"AddMessage", "AddFullMessage"} {
			t.Run(method, func(t *testing.T) {
				path := filepath.Join(base, key, "transcript.jsonl")
				require.NoError(t, os.MkdirAll(path, 0o700)) // directory, never chmod/root-dependent
				var err error
				var returned bool
				if method == "AddMessage" {
					err, returned = wu22WriterCall(t, writer, method, key, "assistant", "must not be reported saved")
				} else {
					err, returned = wu22WriterCall(t, writer, method, key, providers.Message{Role: "assistant", Content: "must not be reported saved"})
				}
				// Prove the injected filesystem fault independently of the return
				// signature, using the same append-open operation on the target.
				file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
				if file != nil {
					require.NoError(t, file.Close())
				}
				require.Error(t, openErr, "instrument: archive file is a directory")
				assert.NoError(t, writer.Save(key), "Save has no pending state or sticky failure (§4.2)")
				assert.True(t, returned, "%s must return its failed write, not only log it (§4.2)", method)
				assert.Error(t, err, "%s falsely reports success on an unappendable archive", method)
				if err != nil {
					var pathErr *os.PathError
					require.ErrorAs(t, err, &pathErr, "original filesystem cause must survive")
					assert.Equal(t, path, pathErr.Path)
					assert.Equal(t, "open", pathErr.Op)
				}
				require.NoError(t, os.Remove(path))
				err, _ = wu22WriterCall(t, writer, method, wu22Args(method, key, "recovered answer")...)
				require.NoError(t, err)
				assert.Equal(t, []providers.Message{{Role: "assistant", Content: "recovered answer"}}, writer.GetHistory(key), "same session can write after repair: no prevention or sticky error")
				// Each method gets a new session so the assertions stay exact.
				key += "-next"
			})
		}
	})
}

func wu22Args(method, key, content string) []any {
	if method == "AddMessage" {
		return []any{key, "assistant", content}
	}
	return []any{key, providers.Message{Role: "assistant", Content: content}}
}

func TestWU22WriterHealthyAppendsAndSaveAreSynchronous(t *testing.T) {
	wu22Stores(t, func(t *testing.T, writer SessionStore, _ string, key string) {
		for _, method := range []string{"AddMessage", "AddFullMessage"} {
			err, _ := wu22WriterCall(t, writer, method, wu22Args(method, key, method+" exact answer")...)
			require.NoError(t, err)
		}
		want := []providers.Message{
			{Role: "assistant", Content: "AddMessage exact answer"},
			{Role: "assistant", Content: "AddFullMessage exact answer"},
		}
		assert.Equal(t, want, writer.GetHistory(key), "both appends are visible before Save")
		require.NoError(t, writer.Save(key))
		assert.Equal(t, want, writer.GetHistory(key), "Save must not alter/compact history")
	})
}

func TestWU22ProjectionValidationErrorsAreReturned(t *testing.T) {
	wu22Stores(t, func(t *testing.T, writer SessionStore, _ string, key string) {
		// §4.2 explicitly requires returning validateArchiveProjection's errors.
		cases := []struct {
			name  string
			key   memory.ProjectionKey
			state memory.ProjectionState
			want  string
		}{
			{"empty_call_id", memory.ProjectionKey{ArchiveLine: 0}, memory.ProjectionCapped, "archive_backend: projection: empty tool_call_id"},
			{"negative_line", memory.ProjectionKey{ToolCallID: "call", ArchiveLine: -1}, memory.ProjectionCapped, "archive_backend: projection: negative archive_line -1"},
			{"unknown_state", memory.ProjectionKey{ToolCallID: "call", ArchiveLine: 0}, memory.ProjectionState("invalid"), "archive_backend: projection: unknown state \"invalid\""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err, returned := wu22WriterCall(t, writer, "SetProjectionState", key, tc.key, tc.state)
				assert.True(t, returned, "validation failure must not be log-only (§4.2)")
				if assert.Error(t, err) {
					assert.EqualError(t, err, tc.want)
				}
				assert.Empty(t, writer.Projection(key).Entries, "invalid projection must not be published")
			})
		}
	})
}

func TestWU22ProjectionMetadataReadErrorsAreReturned(t *testing.T) {
	wu22Stores(t, func(t *testing.T, writer SessionStore, base, key string) {
		pk := memory.ProjectionKey{ToolCallID: "call", ArchiveLine: 0}
		path := filepath.Join(base, key, "backend_meta.json")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
		err, returned := wu22WriterCall(t, writer, "SetProjectionState", key, pk, memory.ProjectionCapped)
		assert.True(t, returned, "loadMetaLocked error must be returned (§4.2)")
		if assert.Error(t, err) {
			var syntax *json.SyntaxError
			assert.True(t, errors.As(err, &syntax), "original metadata decode error must survive: %v", err)
		}
	})
}

func TestWU22ProjectionHealthyStatePersists(t *testing.T) {
	wu22Stores(t, func(t *testing.T, writer SessionStore, _ string, key string) {
		pk := memory.ProjectionKey{ToolCallID: "call", ArchiveLine: 0} // valid minimum line
		for _, state := range []memory.ProjectionState{memory.ProjectionCapped, memory.ProjectionCappedFailure, memory.ProjectionEmptied} {
			err, _ := wu22WriterCall(t, writer, "SetProjectionState", key, pk, state)
			require.NoError(t, err)
			assert.Equal(t, memory.ProjectionSet{pk: state}, writer.Projection(key).Entries)
			require.NoError(t, writer.Save(key))
		}
	})
}
