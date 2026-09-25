package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// readByAgentKeyword is the IMAP keyword stored (with \Seen) when an agent
// reads a message (MC-36).
const readByAgentKeyword = "$OmnipusAgentRead"

// ReadByAgentFromFlags reports whether the flag list marks the message as read
// by an agent — the $OmnipusAgentRead keyword, case-insensitive (MC-36).
func ReadByAgentFromFlags(flags []string) bool {
	for _, f := range flags {
		if strings.EqualFold(f, readByAgentKeyword) {
			return true
		}
	}
	return false
}

// markAgentRead is ReadMessage's read marker: exactly one STORE adds \Seen
// and $OmnipusAgentRead together (MC-36). A server that rejects the keyword
// gets exactly one \Seen-only follow-up STORE and the read still succeeds —
// graceful fallback, logged, never silent.
func (c *Client) markAgentRead(ctx context.Context, client *imapclient.Client, set imap.NumSet) error {
	both := &imap.StoreFlags{
		Op:     imap.StoreFlagsAdd,
		Flags:  []imap.Flag{imap.FlagSeen, imap.Flag(readByAgentKeyword)},
		Silent: true,
	}
	if _, err := runIMAP(ctx, "store agent-read", func() (struct{}, error) {
		return struct{}{}, client.Store(set, both, nil).Close()
	}); err != nil {
		seenOnly := &imap.StoreFlags{
			Op:     imap.StoreFlagsAdd,
			Flags:  []imap.Flag{imap.FlagSeen},
			Silent: true,
		}
		if _, ferr := runIMAP(ctx, "store seen", func() (struct{}, error) {
			return struct{}{}, client.Store(set, seenOnly, nil).Close()
		}); ferr != nil {
			return fmt.Errorf("email transport: mark agent-read (and \\Seen fallback): %w", ferr)
		}
		slog.Warn("email transport: server rejected the "+readByAgentKeyword+" keyword; stored \\Seen only",
			"keyword", readByAgentKeyword)
	}
	return nil
}

// MailboxStatus returns the INBOX counters the watcher needs with one STATUS
// command: unseen count, UIDNEXT and UIDVALIDITY. It mutates no flag (MC-18).
func (c *Client) MailboxStatus(ctx context.Context) (int, uint32, uint32, error) {
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer client.Close()
	data, err := runIMAP(ctx, "status", func() (*imap.StatusData, error) {
		return client.Status("INBOX", &imap.StatusOptions{NumUnseen: true, UIDNext: true, UIDValidity: true}).Wait()
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("email transport: status: %w", err)
	}
	var unseen int
	if data.NumUnseen != nil {
		unseen = int(*data.NumUnseen)
	}
	return unseen, uint32(data.UIDNext), data.UIDValidity, nil
}
