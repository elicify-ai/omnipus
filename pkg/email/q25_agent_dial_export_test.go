//go:build goolm && stdjson

package email

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/emersion/go-imap/v2/imapclient"
)

// Q25UseIMAPDialForTest exposes ONLY the existing network-edge dial seam to
// this package's external integration tests. No production API or service is
// changed; the normal agent still constructs its real email.Client itself.
func Q25UseIMAPDialForTest(t *testing.T, dial func(context.Context, string, *tls.Config) (*imapclient.Client, error)) {
	t.Helper()
	pinLegacyDialWorld(t)
	previous := imapDial
	imapDial = dial
	t.Cleanup(func() { imapDial = previous })
}
