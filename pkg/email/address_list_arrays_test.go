package email

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestMailAddressLists_EmptyRecipientsSerializeAsArrays(t *testing.T) {
	// MailMessage and MailMessageSummary require To/Cc arrays, including when
	// their headers are missing. Bcc is independently nullable on MailMessage.
	t.Run("split", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			joined string
			want   []string
		}{
			{name: "empty", want: []string{}},
			{name: "whitespace", joined: " \t\r\n ", want: []string{}},
			{name: "populated", joined: " to@box.test , second@box.test ", want: []string{"to@box.test", "second@box.test"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := splitAddressList(tc.joined)
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("splitAddressList(%q) = %#v, want %#v", tc.joined, got, tc.want)
				}
				assertMailAddressJSON(t, gen.MailMessageSummary{To: got, Cc: got}, tc.want, tc.want, false)
			})
		}
	})

	for _, tc := range []struct {
		name    string
		headers string
		to      []string
		cc      []string
		bcc     []string
	}{
		{name: "missing_headers", to: []string{}, cc: []string{}},
		{name: "cc_only", headers: "Cc: cc@box.test\r\n", to: []string{}, cc: []string{"cc@box.test"}},
		{name: "to_only", headers: "To: to@box.test\r\n", to: []string{"to@box.test"}, cc: []string{}},
		{name: "bcc_only", headers: "Bcc: bcc@box.test\r\n", to: []string{}, cc: []string{}, bcc: []string{"bcc@box.test"}},
		{
			name: "populated", headers: "To: to@box.test, second@box.test\r\nCc: cc@box.test\r\n",
			to: []string{"to@box.test", "second@box.test"}, cc: []string{"cc@box.test"},
		},
	} {
		t.Run("imap/"+tc.name, func(t *testing.T) {
			raw := "From: sender@box.test\r\n" + tc.headers +
				"Subject: address arrays\r\n" +
				"Date: Thu, 01 Oct 2026 10:00:00 +0000\r\n" +
				"Message-ID: <address-arrays@box.test>\r\n\r\nbody\r\n"
			cl := startViewIMAPRaw(t, []viewMsg{{folder: FolderInbox, raw: []byte(raw)}}, nil)
			rows, uv, truncated, err := cl.ReadFolderPage(context.Background(), FolderInbox, 1, 0)
			if err != nil {
				t.Fatalf("ReadFolderPage: %v", err)
			}
			if len(rows) != 1 || truncated {
				t.Fatalf("ReadFolderPage: rows=%d truncated=%v, want 1 and false", len(rows), truncated)
			}
			// Project transport recipients directly into the generated models.
			// The gateway separately normalizes these arrays; this regression
			// guards the transport itself, not the gateway's existing wrapper.
			assertMailAddressJSON(t, gen.MailMessageSummary{To: rows[0].To, Cc: rows[0].Cc}, tc.to, tc.cc, false)

			ref := fmt.Sprintf("uid:%d:%d", uv, rows[0].UID)
			view, err := cl.ReadView(context.Background(), FolderInbox, ref)
			if err != nil {
				t.Fatalf("ReadView(%s): %v", ref, err)
			}
			if !reflect.DeepEqual(view.To, tc.to) || !reflect.DeepEqual(view.Cc, tc.cc) {
				t.Errorf("ReadView recipients: To=%#v Cc=%#v, want To=%#v Cc=%#v", view.To, view.Cc, tc.to, tc.cc)
			}
			if len(view.Bcc) != len(tc.bcc) || (len(tc.bcc) > 0 && !reflect.DeepEqual(view.Bcc, tc.bcc)) {
				t.Errorf("ReadView Bcc = %#v, want addresses %#v", view.Bcc, tc.bcc)
			}
			// Inbox's generated Bcc pointer stays nil, including for a message
			// whose raw Bcc header is populated. The actual gateway folder gate
			// is unchanged; this assertion checks nullable-model serialization.
			assertMailAddressJSON(t, gen.MailMessage{To: view.To, Cc: view.Cc}, tc.to, tc.cc, true)
		})
	}
}

func assertMailAddressJSON(t *testing.T, value any, wantTo, wantCc []string, nullableBcc bool) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode %T JSON: %v", value, err)
	}
	for _, field := range []struct {
		name string
		want []string
	}{{name: "to", want: wantTo}, {name: "cc", want: wantCc}} {
		want, err := json.Marshal(field.want)
		if err != nil {
			t.Fatalf("marshal expected %s: %v", field.name, err)
		}
		if string(fields[field.name]) != string(want) {
			t.Errorf("%T %s JSON = %s, want %s", value, field.name, fields[field.name], want)
		}
	}
	if nullableBcc && string(fields["bcc"]) != "null" {
		t.Errorf("%T bcc JSON = %s, want null", value, fields["bcc"])
	}
}
