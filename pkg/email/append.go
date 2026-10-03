package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// AppendMessage APPENDs a fully composed RFC 5322 message to folder with the
// given flags (e.g. \Draft, or none for the Sent copy) and returns the new
// message's UID and the folder's UIDVALIDITY. It is the optional capability
// the email tools and gateway mail endpoints use to save sent copies (D7/D18)
// and create drafts (D8).
func (c *Client) AppendMessage(ctx context.Context, folder string, flags []string, raw []byte) (uid uint32, uidvalidity uint32, err error) {
	if strings.TrimSpace(folder) == "" {
		return 0, 0, fmt.Errorf("email transport: folder is required")
	}
	if len(raw) == 0 {
		return 0, 0, fmt.Errorf("email transport: message is empty")
	}
	// The pooled path (Wave C rewiring): the APPEND rides a session-pool
	// lease on the operation's own folder, flagged as a mutation so the
	// pool never coalesces it and never replays it automatically (W1
	// §4.5.4). Without an injected session source, withMailSession keeps
	// the legacy per-call dial with the identical command sequence.
	err = c.withMailSession(ctx, folder, true, func(ctx context.Context, client *imapclient.Client, _ uint32) error {
		var imapFlags []imap.Flag
		for _, f := range flags {
			if strings.TrimSpace(f) == "" {
				continue
			}
			imapFlags = append(imapFlags, imap.Flag(f))
		}
		var opts *imap.AppendOptions
		if len(imapFlags) > 0 {
			opts = &imap.AppendOptions{Flags: imapFlags, Time: time.Now()}
		}
		// Bounded like every other IMAP command in this package (runIMAP's
		// goroutine + ctx-bounded select): a stalling server that accepts the
		// APPEND literal but never sends the tagged response must not hang this
		// call forever. Write/Close/Wait all run inside the same fn so the
		// timeout covers the whole sequence, not just the final Wait.
		var data *imap.AppendData
		data, err = runIMAP(ctx, "append to "+folder, func() (*imap.AppendData, error) {
			cmd := client.Append(folder, int64(len(raw)), opts)
			if _, werr := cmd.Write(raw); werr != nil {
				return nil, werr
			}
			if cerr := cmd.Close(); cerr != nil {
				return nil, cerr
			}
			return cmd.Wait()
		})
		if err != nil {
			return fmt.Errorf("email transport: append to %s: %w", folder, err)
		}
		if data != nil {
			uid = uint32(data.UID)
			uidvalidity = data.UIDValidity
		}
		// Servers without UIDPLUS report no APPENDUID: select the folder to learn
		// its UIDVALIDITY (the draft tool's result needs both values).
		if uidvalidity == 0 {
			sel, serr := runIMAP(ctx, "select "+folder, func() (*imap.SelectData, error) {
				return client.Select(folder, nil).Wait()
			})
			if serr != nil {
				return fmt.Errorf("email transport: append to %s: no UIDVALIDITY: %w", folder, serr)
			}
			uidvalidity = sel.UIDValidity
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return uid, uidvalidity, nil
}
