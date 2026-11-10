package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// registerEmailToolsForAgent builds every tool transport from emailToolAccount.
// The signature has to be copied here: the tools only see the account, not
// config.MailboxConfig.
func TestEmailToolAccount_CarriesMailboxSignature(t *testing.T) {
	const want = "<p>SIG-ONCE-TOKEN</p>"
	acct := emailToolAccount(config.MailboxConfig{
		IMAPHost:         "imap.example",
		IMAPPort:         993,
		SMTPHost:         "smtp.example",
		SMTPPort:         587,
		Username:         "mia@box.test",
		SentFolderName:   "Sent Items",
		DraftsFolderName: "Drafts Box",
		SignatureHTML:    want,
	}, "pw")
	if acct.SignatureHTML != want {
		t.Fatalf("SignatureHTML = %q, want %q", acct.SignatureHTML, want)
	}
	if acct.Password != "pw" || acct.Username != "mia@box.test" {
		t.Fatalf("account identity = user %q password %q, want mia@box.test / pw", acct.Username, acct.Password)
	}
	if acct.SentFolder != "Sent Items" || acct.DraftsFolder != "Drafts Box" {
		t.Fatalf("folders = sent %q drafts %q, want the mailbox overrides", acct.SentFolder, acct.DraftsFolder)
	}
	if acct.IMAPHost != "imap.example" || acct.IMAPPort != 993 || acct.SMTPHost != "smtp.example" || acct.SMTPPort != 587 {
		t.Fatalf("endpoints changed: %+v", acct)
	}
	empty := emailToolAccount(config.MailboxConfig{
		IMAPHost: "imap.example", SMTPHost: "smtp.example", Username: "mia@box.test",
	}, "pw")
	if empty.SignatureHTML != "" {
		t.Fatalf("SignatureHTML = %q, want empty when the mailbox has none", empty.SignatureHTML)
	}
}
