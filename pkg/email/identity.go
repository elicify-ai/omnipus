package email

import "net/mail"

// Identity, folder-name, and recipient-parse capabilities the mail tools use
// at composition time. All are optional interfaces at the pkg/tools side
// (structural type assertions), implemented by *Client; the transport
// interface itself stays dial-shaped (ReadInbox/Search/ReadMessage/Send/
// MarkSeen) so the test fake remains trivial.

// AccountAddress returns the mailbox's sending identity (the username —
// an email address). The draft tool uses it as the From header and the
// Message-ID domain seed; SMTP sends take the identity from the transport
// itself, so this capability is only needed by the compose-and-APPEND path.
func (c *Client) AccountAddress() string { return c.acct.Username }

// SentFolderName returns the account's Sent folder name (default "Sent").
func (c *Client) SentFolderName() string { return c.acct.withDefaults().sentFolder() }

// DraftsFolderName returns the account's Drafts folder name (default "Drafts").
func (c *Client) DraftsFolderName() string { return c.acct.withDefaults().draftsFolder() }

// ParseRecipientList parses recipient entries with the SAME MC-4 rules the
// transport and Compose use: entries split on CR/LF and commas BEFORE parsing
// (an injection attempt degrades into an unparseable, reported piece),
// display names kept, case-insensitive de-duplication in first-seen order.
// Exported for the tool layer, which must apply the identical parsing and the
// MC-27 50-recipient cap BEFORE any dial or APPEND.
func ParseRecipientList(entries []string) (addrs []mail.Address, bad []string) {
	return parseRecipientList(entries)
}
