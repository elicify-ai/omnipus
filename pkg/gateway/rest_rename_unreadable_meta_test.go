package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S2-17 (round 1, still open in round 2): renameSession must decide on a
// readable record, like deleteSession does. A session whose metadata cannot be
// read is not renamed and not guessed at: a session that does not exist is a
// plain 404, and any other read failure is a visible error naming the READ
// (not a rename that "failed in storage"). ORACLE: deleteSession's fail-closed
// rule (MEDIUM-1) and the 404 every other session read gives.
func TestRenameSession_UnknownSessionIs404(t *testing.T) {
	env := u1NewEnv(t, false)
	code, body := sf1Do(env, http.MethodPut, "/api/v1/sessions/session_01HZZZZZZZZZZZZZZZZZZZZZZZ", `{"title":"Renamed"}`)
	assert.Equal(t, http.StatusNotFound, code, "body=%s", body)
}

func TestRenameSession_ExistingChatStillRenames(t *testing.T) {
	env := u1NewEnv(t, false)
	chat := sf1NewChat(t, env)
	code, body := sf1Do(env, http.MethodPut, "/api/v1/sessions/"+chat, `{"title":"Renamed"}`)
	require.Equal(t, http.StatusOK, code, "instrument check: a readable chat renames; body=%s", body)
}

// An id that exists on disk with an unreadable record (never cached, so the
// store must parse the file) is refused before any write, and the answer names
// the READ - the same fail-closed shape deleteSession gives.
func TestRenameSession_UnreadableMetaFailsClosedAndSaysSo(t *testing.T) {
	env := u1NewEnv(t, false)
	const id = "session_01HBROKENMETA00000000000001"
	dir := filepath.Join(env.store(t).BaseDir(), id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{not json"), 0o600))

	code, body := sf1Do(env, http.MethodPut, "/api/v1/sessions/"+id, `{"title":"Renamed"}`)

	assert.GreaterOrEqual(t, code, 500, "body=%s", body)
	assert.Contains(t, body, "could not be read", "the refusal must name the failed READ, not a failed rename")
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(raw), "nothing is written")
}
