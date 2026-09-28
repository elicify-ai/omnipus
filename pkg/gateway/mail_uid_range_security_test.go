package gateway

import (
	"net/http"
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
