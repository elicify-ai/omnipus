package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"
)

const mailArrayFixture = "From: sender@example.test\r\n" +
	"Subject: array fixture\r\n" +
	"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
	"Message-ID: <arrays@example.test>\r\n\r\nbody\r\n"

func TestMailMessages_EmptyFolderSerializesMessagesAsArray(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, _ := startPlainIMAP(t)
	pointMailboxAt(t, env, imapPort, 1)

	rec := mailDo(env.mux, http.MethodGet, mailMessagesPath("inbox"), nextMailIP(), true, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Contains(t, rec.Body.String(), `"messages":[]`,
		"an empty folder must serialize messages as [], never null")
}

func TestMailResponses_NonNullableArraysNeverSerializeNull(t *testing.T) {
	tests := []struct {
		name  string
		serve func(*testing.T) *httptest.ResponseRecorder
		paths [][]any
	}{
		{
			name: "message page recipient lists",
			serve: func(t *testing.T) *httptest.ResponseRecorder {
				env := newMailRedEnv(t)
				imapPort, cl := startPlainIMAP(t)
				pointMailboxAt(t, env, imapPort, 1)
				appendRaw(t, cl, "INBOX", []byte(mailArrayFixture), nil)
				return mailDo(env.mux, http.MethodGet, mailMessagesPath("inbox"), nextMailIP(), true, "")
			},
			paths: [][]any{{"messages"}, {"messages", 0, "to"}, {"messages", 0, "cc"}},
		},
		{
			name: "full inbound message lists",
			serve: func(t *testing.T) *httptest.ResponseRecorder {
				env := newMailRedEnv(t)
				imapPort, cl := startPlainIMAP(t)
				pointMailboxAt(t, env, imapPort, 1)
				appendRaw(t, cl, "INBOX", []byte(mailArrayFixture), nil)
				uv := inboxUIDValidity(t, cl)
				path := fmt.Sprintf("%s/uid:%d:1", mailMessagesPath("inbox"), uv)
				return mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
			},
			paths: [][]any{{"to"}, {"cc"}, {"attachments"}},
		},
		{
			name: "owner sent copy bcc list",
			serve: func(t *testing.T) *httptest.ResponseRecorder {
				env := newMailRedEnv(t)
				imapPort, cl := startPlainIMAP(t)
				pointMailboxAt(t, env, imapPort, 1)
				appendRaw(t, cl, "Sent", []byte(mailArrayFixture), nil)
				uv := mdhFolderUIDValidity(t, cl, "Sent")
				path := fmt.Sprintf("%s/uid:%d:1", mailMessagesPath("sent"), uv)
				return mailDo(env.mux, http.MethodGet, path, nextMailIP(), true, "")
			},
			paths: [][]any{{"bcc"}},
		},
		{
			name: "draft update lists",
			serve: func(t *testing.T) *httptest.ResponseRecorder {
				env := newMailRedEnv(t)
				imapPort, cl := startPlainIMAP(t)
				pointMailboxAt(t, env, imapPort, 1)
				appendRaw(t, cl, "Drafts", []byte(draftRaw), []imap.Flag{imap.FlagDraft})
				uv := draftUIDValidity(t, cl)
				body := fmt.Sprintf(
					`{"uid":1,"uidvalidity":%d,"to":["a@example.test"],"subject":"updated","body_markdown":"body","keep_attachment_parts":[]}`,
					uv,
				)
				return mailDo(env.mux, http.MethodPut, fmt.Sprintf("%s/uid:%d:1", mailMessagesPath("drafts"), uv), nextMailIP(), true, body)
			},
			paths: [][]any{{"to"}, {"cc"}, {"bcc"}, {"attachments"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := tt.serve(t)
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			var body any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())
			for _, path := range tt.paths {
				value := mailJSONPath(t, body, path...)
				require.IsType(t, []any{}, value, "%v must be a JSON array, got %#v; body: %s", path, value, rec.Body.String())
			}
		})
	}
}

func mailJSONPath(t *testing.T, value any, path ...any) any {
	t.Helper()
	current := value
	for _, segment := range path {
		switch key := segment.(type) {
		case string:
			object, ok := current.(map[string]any)
			require.True(t, ok, "path %v: %#v is not an object", path, current)
			current, ok = object[key]
			require.True(t, ok, "path %v: key %q is absent", path, key)
		case int:
			array, ok := current.([]any)
			require.True(t, ok, "path %v: %#v is not an array", path, current)
			require.Greater(t, len(array), key, "path %v: index %d is absent", path, key)
			current = array[key]
		default:
			t.Fatalf("path %v: unsupported segment %#v", path, segment)
		}
	}
	return current
}
