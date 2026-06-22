package email

import (
	"strings"
	"testing"
)

func TestNewClient_Validation(t *testing.T) {
	cases := []struct {
		name string
		acct Account
		ok   bool
	}{
		{"missing imap", Account{SMTPHost: "s", Username: "u", Password: "p"}, false},
		{"missing smtp", Account{IMAPHost: "i", Username: "u", Password: "p"}, false},
		{"missing user", Account{IMAPHost: "i", SMTPHost: "s", Password: "p"}, false},
		{"missing pass", Account{IMAPHost: "i", SMTPHost: "s", Username: "u"}, false},
		{"complete", Account{IMAPHost: "i", SMTPHost: "s", Username: "u", Password: "p"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl, err := NewClient(c.acct)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error, got client %+v", cl)
			}
		})
	}
}

func TestNewClient_AppliesDefaultPorts(t *testing.T) {
	cl, err := NewClient(Account{IMAPHost: "i", SMTPHost: "s", Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if cl.acct.IMAPPort != 993 {
		t.Errorf("default IMAP port want 993, got %d", cl.acct.IMAPPort)
	}
	if cl.acct.SMTPPort != 587 {
		t.Errorf("default SMTP port want 587, got %d", cl.acct.SMTPPort)
	}
}

func TestClient_AddressReturnsUsername(t *testing.T) {
	cl, _ := NewClient(Account{IMAPHost: "i", SMTPHost: "s", Username: "me@x.com", Password: "p"})
	if cl.Address() != "me@x.com" {
		t.Fatalf("Address() want me@x.com, got %q", cl.Address())
	}
}

func TestBuildEmailBody_Headers(t *testing.T) {
	body := buildEmailBody("from@x.com", "to@x.com", "Hello", "the body", "")
	for _, want := range []string{
		"From: from@x.com\r\n",
		"To: to@x.com\r\n",
		"Subject: Hello\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "In-Reply-To") {
		t.Error("non-reply body must not have In-Reply-To header")
	}
	// Headers must precede the blank-line + body.
	hdrEnd := strings.Index(body, "\r\n\r\n")
	if hdrEnd < 0 || !strings.HasSuffix(body, "the body") {
		t.Error("body must follow the header block")
	}
}

func TestBuildEmailBody_ReplyThreadingHeaders(t *testing.T) {
	body := buildEmailBody("from@x.com", "to@x.com", "Re: Hi", "reply text", "<orig@x.com>")
	if !strings.Contains(body, "In-Reply-To: <orig@x.com>\r\n") {
		t.Error("reply must set In-Reply-To")
	}
	if !strings.Contains(body, "References: <orig@x.com>\r\n") {
		t.Error("reply must set References")
	}
}

func TestSanitizeHeader_StripsCRLF(t *testing.T) {
	got := sanitizeHeader("Subject\r\nBcc: evil@x.com")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header injection not stripped: %q", got)
	}
	if got != "SubjectBcc: evil@x.com" {
		t.Fatalf("unexpected sanitized value: %q", got)
	}
}
