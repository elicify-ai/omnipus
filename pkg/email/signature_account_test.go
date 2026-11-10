package email

import "testing"

// The mail tools read the signature through Client.SignatureHTML. An account
// field that NewClient dropped, or a method that returned "", would leave
// agent sends unsigned even after composeMail learned to pass the value.
func TestClient_SignatureHTML_RoundTrip(t *testing.T) {
	const want = "<p>SIG-ONCE-TOKEN</p>"
	with, err := NewClient(Account{
		IMAPHost: "imap.example", SMTPHost: "smtp.example",
		Username: "mia@box.test", Password: "pw",
		SignatureHTML: want,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got := with.SignatureHTML(); got != want {
		t.Fatalf("SignatureHTML() = %q, want %q", got, want)
	}
	without, err := NewClient(Account{
		IMAPHost: "imap.example", SMTPHost: "smtp.example",
		Username: "mia@box.test", Password: "pw",
	})
	if err != nil {
		t.Fatalf("NewClient without signature: %v", err)
	}
	if got := without.SignatureHTML(); got != "" {
		t.Fatalf("SignatureHTML() = %q, want empty when the mailbox has none", got)
	}
}
