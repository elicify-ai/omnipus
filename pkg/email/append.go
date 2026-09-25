package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
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
	client, _, err := c.dialIMAP(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer client.Close()

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
	cmd := client.Append(folder, int64(len(raw)), opts)
	if _, err := cmd.Write(raw); err != nil {
		return 0, 0, fmt.Errorf("email transport: append to %s: %w", folder, err)
	}
	if err := cmd.Close(); err != nil {
		return 0, 0, fmt.Errorf("email transport: append to %s: %w", folder, err)
	}
	data, err := cmd.Wait()
	if err != nil {
		return 0, 0, fmt.Errorf("email transport: append to %s: %w", folder, err)
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
			return uid, 0, fmt.Errorf("email transport: append to %s: no UIDVALIDITY: %w", folder, serr)
		}
		uidvalidity = sel.UIDValidity
	}
	return uid, uidvalidity, nil
}
