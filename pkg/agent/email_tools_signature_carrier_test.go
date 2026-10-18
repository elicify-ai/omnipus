package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// Oracle: email-mail-view-spec US-1 requires the configured mailbox signature
// on agent sends. Registration constructs this exact account -> real Client
// chain; the tools discover SignatureHTML through a structural capability.
// Neither the account mapper nor the client/capability is replaced by a fake.
func TestEmailToolAccount_ClientCarriesMailboxSignature(t *testing.T) {
	const want = "<p>Kind regards, SIG-REGISTRATION-CARRIER</p>"
	client, err := email.NewClient(emailToolAccount(config.MailboxConfig{
		IMAPHost: "imap.example", SMTPHost: "smtp.example",
		Username: "mia@box.test", SignatureHTML: want,
	}, "pw"))
	if err != nil {
		t.Fatalf("registration account -> NewClient: %v", err)
	}

	// Use the same optional capability shape as the tools. Removing the
	// production method must fail this assertion, not silently send unsigned.
	carrier, ok := any(client).(interface{ SignatureHTML() string })
	if !ok {
		t.Fatal("registration client does not expose the SignatureHTML capability")
	}
	if got := carrier.SignatureHTML(); got != want {
		t.Fatalf("registration account -> Client.SignatureHTML() = %q, want %q", got, want)
	}
}
