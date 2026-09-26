package gateway

// T36 - TestNoLocalMailStore (spec section 7 row 36, B-15, MC-12, D6, FR-033).
// Oracle from the spec: after a full exercise, a data-dir sweep finds no
// message bodies on disk. The watcher state file is the one expected local
// write (metadata only). A distinctive body marker must not appear in any
// file under the data dir.

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/email"
)

const noStoreMarker = "X-NO-LOCAL-STORE-MARKER-4f9a2b"

// sweepDataDirForMarker walks every file under dir and reports the paths
// whose bytes contain the marker.
func sweepDataDirForMarker(t *testing.T, dir, marker string) []string {
	t.Helper()
	var hits []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(b, []byte(marker)) {
			hits = append(hits, path)
		}
		return nil
	})
	require.NoError(t, err)
	return hits
}

// TestNoLocalMailStore_SweepDetectsPlantedMarker proves the instrument: the
// sweep reports a file that carries the marker, so a clean sweep on the real
// data dir means something.
func TestNoLocalMailStore_SweepDetectsPlantedMarker(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(clean, []byte("{\"clean\":true}"), 0o600))
	dirty := filepath.Join(dir, "leak.bin")
	require.NoError(t, os.WriteFile(dirty, []byte("subject: x\r\n\r\n"+noStoreMarker+"\r\n"), 0o600))
	hits := sweepDataDirForMarker(t, dir, noStoreMarker)
	require.Equal(t, []string{dirty}, hits, "sweep must detect a planted marker file")
}

// inboxUIDValidity reads the INBOX UIDVALIDITY from the server.
func inboxUIDValidity(t *testing.T, cl *imapclient.Client) uint32 {
	t.Helper()
	data, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	require.NoError(t, err)
	return data.UIDValidity
}

func TestNoLocalMailStore(t *testing.T) {
	env := newMailRedEnv(t)
	imapPort, cl := startPlainIMAP(t)
	smtpPort, _ := listenCount(t)
	pointMailboxAt(t, env, imapPort, smtpPort)

	msg := func(id string) []byte {
		return []byte("From: a@b.test\r\nTo: mailbox@test.local\r\nSubject: sweep " + id + "\r\n" +
			"Message-ID: <" + id + "@b.test>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
			"body with " + noStoreMarker + " inside\r\n")
	}
	appendRaw(t, cl, "INBOX", msg("sweep1"), nil)
	appendRaw(t, cl, "INBOX", msg("sweep2"), nil)

	// Realistic read exercise through the gateway: folders, list, full read
	// of both messages, seen, summary.
	rec := mailDo(env.mux, "GET", mailFoldersPath(), nextMailIP(), true, "")
	require.Equal(t, 200, rec.Code, "folders: %s", rec.Body.String())
	rec = mailDo(env.mux, "GET", mailMessagesPath("inbox"), nextMailIP(), true, "")
	require.Equal(t, 200, rec.Code, "list: %s", rec.Body.String())
	uv := inboxUIDValidity(t, cl)
	for _, uid := range []string{"1", "2"} {
		rec = mailDo(env.mux, "GET", mailMessagesPath("inbox")+"/uid:"+strconv.Itoa(int(uv))+":"+uid, nextMailIP(), true, "")
		require.Equal(t, 200, rec.Code, "read uid:%d:%s: %s", uv, uid, rec.Body.String())
	}
	rec = mailDo(env.mux, "POST", mailMessagesPath("inbox")+"/uid:1:1/seen", nextMailIP(), true, "")
	if rec.Code != 204 && rec.Code != 200 {
		t.Logf("seen returned %d (non-fatal for the sweep): %s", rec.Code, rec.Body.String())
	}
	rec = mailDo(env.mux, "GET", summaryPath(), nextMailIP(), true, "")
	require.Equal(t, 200, rec.Code, "summary: %s", rec.Body.String())

	// The one expected local write: the watcher state file (metadata only).
	cycleClient, err := email.NewClient(email.Account{
		IMAPHost: "127.0.0.1",
		IMAPPort: imapPort,
		SMTPHost: "127.0.0.1",
		SMTPPort: smtpPort,
		Username: "mailbox@test.local",
		Password: "s3cret",
	})
	require.NoError(t, err)
	w, err := email.NewWatcher(email.WatcherConfig{
		AgentID:     mailRedAgent,
		WorkspaceID: mailRedWS,
		Transport:   cycleClient,
		StateDir:    env.api.homePath,
	})
	require.NoError(t, err)
	require.NoError(t, w.Cycle(context.Background()))

	hits := sweepDataDirForMarker(t, env.api.homePath, noStoreMarker)
	if len(hits) != 0 {
		t.Fatalf("MC-12/D6: message-body marker found on disk in %v - the feature must not persist mail content locally", hits)
	}
}
