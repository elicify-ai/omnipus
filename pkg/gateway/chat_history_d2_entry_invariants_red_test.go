package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func d2RequireEntryRoute(t *testing.T) {
	t.Helper()
	api, sid, position := d2EntryFixture(t)
	w := d2EntryRequest(api, sid, position, "Bearer d2-route-test-token", nil)
	var message generated.Message
	decode := json.Unmarshal(w.Body.Bytes(), &message)
	if w.Code != 200 || decode != nil || message.Id != "d2-full-source" || message.HistoryPosition == nil || *message.HistoryPosition != position {
		t.Fatalf("BLOCKED: getSessionHistoryEntry not implemented (HTTP%d did not carry the addressed public Message) — required by D2 A2.2/T65", w.Code)
	}
}
func TestHistoryEntryRead_RealCredentialClasses(t *testing.T) {
	for _, kind := range []string{"cookie", "configured_user_bearer", "CLI_bearer"} {
		t.Run(kind, func(t *testing.T) {
			api, sid, position := d2EntryFixture(t)
			t.Setenv("OMNIPUS_BEARER_TOKEN", "")
			cfg := api.agentLoop.GetConfig()
			token := "omnipus_" + strings.Repeat("a", 64)
			hash, err := bcrypt.GenerateFromPassword([]byte(config.TokenSecret(token)), bcrypt.MinCost)
			require.NoError(t, err)
			var cookie *http.Cookie
			switch kind {
			case "cookie":
				raw, h, e := middleware.MintSessionToken()
				require.NoError(t, e)
				cfg.Gateway.Users = []config.UserConfig{{Username: "d2-cookie", SessionTokenHash: config.BcryptHash(h)}}
				cookie = &http.Cookie{Name: middleware.SessionCookieName, Value: raw}
			case "CLI_bearer":
				cfg.Gateway.CLIToken = &config.TokenEntry{Hash: config.BcryptHash(hash)}
			default:
				cfg.Gateway.Users = []config.UserConfig{{Username: "d2-user", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash)}}}}
			}
			mux := http.NewServeMux()
			mux.Handle("/api/v1/sessions/", api.withAuth(api.HandleSessions))
			read := func(path string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if cookie != nil {
					r.AddCookie(cookie)
				} else {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			// A known page validation response proves the real auth wrapper accepted
			// the credential before this test judges the new route's implementation.
			require.Equal(t, 400, read("/api/v1/sessions/"+sid+"/messages?limit=19").Code)
			w := read("/api/v1/sessions/" + sid + "/messages/" + position)
			require.Equal(t, 200, w.Code, "A2.2: same real cookie/user/CLI authentication as paged reads")
			var m map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
			require.Equal(t, "FULL_PUBLIC_CANARY", m["content"])
			cfg.Gateway.Users = nil
			cfg.Gateway.CLIToken = nil
			t.Setenv("OMNIPUS_BEARER_TOKEN", "a-different-required-token")
			require.Equal(t, 401, read("/api/v1/sessions/"+sid+"/messages/"+position).Code, "revoked credential must not use a cached auth success")
		})
	}
}
func TestHistoryEntryRead_ProvenExpiryAndForeignExpired(t *testing.T) {
	d2RequireEntryRoute(t)
	driver := historyMeasurementDriver(t)
	f := r1CleanupBuildFixture(t, driver)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "d2-route-test-token")
	position := pagingPositionOracle(f.sid, session.ArchiveAddress{PartitionKey: "2026-01-01", ByteOffset: 0, EntryID: "r1-old"})
	prior := d2EntryRequest(f.api, f.sid, position, "Bearer d2-route-test-token", nil)
	require.Equal(t, 200, prior.Code, "positive control: occurrence truly existed, not an unknown token")
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(f.rolled, old, old))
	removed, err := f.store.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.NoFileExists(t, f.rolled)
	for i := 0; i < 2; i++ {
		d2EntryRefusal(t, d2EntryRequest(f.api, f.sid, position, "Bearer d2-route-test-token", nil), 410, "history entry expired", f.sid, f.rolled, "R1_BODY_CANARY")
	}
	other := createTestSession(t, f.api)
	d2EntryRefusal(t, d2EntryRequest(f.api, other, position, "Bearer d2-route-test-token", nil), 404, "history position not found", f.sid, "R1_BODY_CANARY")
}
func TestHistoryEntryRead_CleanupStorageFailureOutranksUnknown(t *testing.T) {
	d2RequireEntryRoute(t)
	f := r1CleanupBuildFixture(t, historyMeasurementDriver(t))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "d2-route-test-token")
	work := r1CleanupStart(t, func(ctx context.Context) error {
		r, e := newHistoryPageRequest(f.sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{})
		if e != nil {
			return e
		}
		_, e = r.Read(ctx, f.store)
		return e
	})
	r1CleanupRetirePinned(t, f)
	restore, native := r1CleanupHoldRemoveFault(t, f.retired)
	defer restore()
	err := r1CleanupJoin(t, work)
	require.ErrorIs(t, err, session.ErrChatHistoryUnavailable)
	require.ErrorIs(t, err, native, "native failed last-reader release established real pending cleanup")
	unknown := "hp1_" + strings.Repeat("A", 43)
	expired := pagingPositionOracle(f.sid, session.ArchiveAddress{PartitionKey: "2026-01-01", ByteOffset: 0, EntryID: "r1-old"})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	t.Run("unknown_position", func(t *testing.T) {
		w := d2EntryRequest(f.api, f.sid, unknown, "Bearer d2-route-test-token", context.Background())
		d2EntryRefusal(t, w, 500, historyStorageCopy, unknown, f.retired, "R1_BODY_CANARY")
	})
	// Oracle correction (B76 inherits B69/T53): cancellation changes delivery,
	// not cleanup precedence. The old HTTP500 requirement on a cancelled
	// transport was invalid. Do not replace it with an HTTP200 expectation.
	t.Run("cancellation_with_proven_expiry_typed_causes", func(t *testing.T) {
		position, parseErr := session.ParseHistoryPosition(expired)
		require.NoError(t, parseErr)
		_, readErr := f.store.ReadChatHistoryEntry(cancelled, f.sid, position)
		require.ErrorIs(t, readErr, session.ErrChatHistoryUnavailable, "B69: a pre-cancelled normal read must still surface real pending cleanup failure")
		var unavailable *session.ChatHistoryUnavailableError
		require.ErrorAs(t, readErr, &unavailable)
		require.Equal(t, "storage_error", unavailable.Reason)
		require.ErrorIs(t, readErr, native, "actual native removal cause is preserved")
		require.ErrorIs(t, readErr, context.Canceled, "the original read cancellation stays internally matchable")
		require.Equal(t, session.ErrChatHistoryUnavailable.Error(), readErr.Error(), "public error string stays sanitized")
		require.FileExists(t, f.retired, "failure remains pending for a later ordinary read")
	})
	t.Run("cancellation_with_proven_expiry_storage_log", func(t *testing.T) {
		readLogs := pagingLogCapture(t) // Includes its own real file-sink positive control.
		before := len(readLogs())
		w := d2EntryRequest(f.api, f.sid, expired, "Bearer d2-route-test-token", cancelled)
		logs := readLogs()[before:]
		storageLogs := 0
		for _, row := range logs {
			if row["message"] == "rest: session storage failure" {
				storageLogs++
				require.Equal(t, "read", row["op"])
				require.Equal(t, f.sid, row["session_id"])
			}
		}
		require.Equal(t, 1, storageLogs, "B69: actual single-entry route must record normal storage failure even if cancellation prevents a body")
		for _, canary := range []string{expired, f.retired, "R1_BODY_CANARY"} {
			require.NotContains(t, w.Body.String(), canary)
		}
		// No assertion requires a response status or body from a cancelled transport.
	})
}
func TestHistoryEntryRead_WorkspaceMetadataStorageFailure(t *testing.T) {
	d2RequireEntryRoute(t)
	api, _, _ := d2EntryFixture(t)
	// A real unreadable workspace record is not a missing workspace. The test
	// uses a directory in the expected file slot so the read fails portably.
	ws := "d2-unreadable-workspace"
	path := filepath.Join(api.homePath, "workspaces", ws+".json")
	require.NoError(t, os.MkdirAll(path, 0700))
	_, err := readWorkspaceFile(api.homePath, ws)
	require.Error(t, err, "positive native failure control: directory occupies the workspace file")
	require.NotErrorIs(t, err, errWorkspaceNotFound, "this is storage failure, not a missing workspace")
	mainID, err := session.MainSessionID(ws, "mia")
	require.NoError(t, err)
	d2EntryRefusal(t, d2EntryRequest(api, mainID, "malformed", "Bearer d2-route-test-token", nil), 500, "", path, mainID, "is a directory")
}
