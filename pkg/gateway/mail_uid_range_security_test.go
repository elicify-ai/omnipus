package gateway

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/email"
)

func TestMailDraftUIDPreconditionsRejectOutOfUint32Range(t *testing.T) {
	cur := &email.MailView{UID: ^uint32(0), UIDValidity: ^uint32(0)}

	for _, tc := range []struct {
		name string
		uid  int64
		uv   int64
	}{
		{name: "negative uid", uid: -1, uv: 1},
		{name: "negative uidvalidity", uid: 1, uv: -1},
		{name: "overflowing uid", uid: 1 << 32, uv: 1},
		{name: "overflowing uidvalidity", uid: 1, uv: 1 << 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := mailDraftRefBodyAgreement("mid:<draft@example.test>", tc.uid, tc.uv); ok {
				t.Fatal("out-of-range draft precondition passed the pre-read request validation")
			}
			status, _, _ := mailDraftStaleness(cur, tc.uid, tc.uv)
			if status != http.StatusBadRequest {
				t.Fatalf("out-of-range draft precondition status = %d, want 400", status)
			}
		})
	}

	status, _, _ := mailDraftStaleness(cur, 1<<32-1, 1<<32-1)
	if status != 0 {
		t.Fatalf("maximum uint32 precondition status = %d, want accepted", status)
	}
}

func TestMailDraftUIDPreconditionsRejectAtHandlersBeforeDial(t *testing.T) {
	env := newMailRedEnv(t)
	// The handler must enforce the uint32 domain even when optional inbound
	// schema validation is disabled.
	env.api.agentLoop.GetConfig().Gateway.ValidateInbound = false
	port, dials := listenCount(t)
	pointMailboxAt(t, env, port, port)
	path := mailMessagesPath("drafts") + "/" + url.PathEscape("mid:<draft@example.test>")

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name: "update negative uid", method: http.MethodPut, path: path,
			body: `{"uid":-1,"uidvalidity":1,"to":["a@b.test"],"subject":"s","body_markdown":"b","keep_attachment_parts":[]}`,
		},
		{
			name: "update overflowing uidvalidity", method: http.MethodPut, path: path,
			body: `{"uid":1,"uidvalidity":4294967296,"to":["a@b.test"],"subject":"s","body_markdown":"b","keep_attachment_parts":[]}`,
		},
		{
			name: "send negative uidvalidity", method: http.MethodPost, path: path + "/send",
			body: `{"uid":1,"uidvalidity":-1,"to":["a@b.test"],"subject":"s","body_markdown":"b"}`,
		},
		{
			name: "send overflowing uid", method: http.MethodPost, path: path + "/send",
			body: `{"uid":4294967296,"uidvalidity":1,"to":["a@b.test"],"subject":"s","body_markdown":"b"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := mailDo(env.mux, tc.method, tc.path, nextMailIP(), true, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if got := dials.Load(); got != 0 {
				t.Fatalf("invalid draft precondition opened %d connection(s), want zero", got)
			}
		})
	}
}

func TestMailUIDToWirePreservesUint32Maximum(t *testing.T) {
	const want = int64(1<<32 - 1)
	if got := mailUIDToWire(^uint32(0)); got != want {
		t.Fatalf("mailUIDToWire(max uint32) = %d, want %d", got, want)
	}
	if got := fmt.Sprintf("%d", mailUIDToWire(^uint32(0))); got != "4294967295" {
		t.Fatalf("serialized max uint32 = %q, want 4294967295", got)
	}
}
