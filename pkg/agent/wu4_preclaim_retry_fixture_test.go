package agent

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A real dependency file is replaced by a directory, then restored byte-for-byte.
// No permissions assumption (root and non-root behave alike), mock registry,
// private latch edit, or fake successful claim is involved.
type wu4PreclaimFileCut struct {
	path   string
	backup string
	armed  bool
}

func (c *wu4PreclaimFileCut) block() error {
	c.backup = c.path + ".sf02-saved"
	if err := os.Rename(c.path, c.backup); err != nil {
		return err
	}
	c.armed = true
	return os.Mkdir(c.path, 0o700)
}
func (c *wu4PreclaimFileCut) restore() error {
	if !c.armed {
		return nil
	}
	if err := os.Remove(c.path); err != nil {
		return err
	}
	if err := os.Rename(c.backup, c.path); err != nil {
		return err
	}
	c.armed = false
	return nil
}

func wu4PreclaimBlockFile(t *testing.T, path string) *wu4PreclaimFileCut {
	t.Helper()
	cut := &wu4PreclaimFileCut{path: path}
	t.Cleanup(func() { require.NoError(t, cut.restore()) })
	require.NoError(t, cut.block(), "instrument: arm actual filesystem rejection")
	return cut
}

type wu4PreclaimFixture struct {
	f                  *secretWakeAcceptanceFixture
	record             secretrequest.Record
	original           bus.InboundMessage
	credentialBytes    []byte
	credentialNames    []string
	metadata           map[string]credentials.CardSecret
	receipt            credentials.CardReceipt
	diagnostics        []string
	revivalCut         *wu4PreclaimFileCut
	failRevivalRead    bool
	revivalReadReached bool
}

// The original ordinary turn really invokes registered request_secret and parks.
// Submit really commits the encrypted version and successfully publishes the wake.
func newWU4PreclaimFixture(t *testing.T, restart bool) *wu4PreclaimFixture {
	t.Helper()
	h := &wu4PreclaimFixture{f: newSecretWakeAcceptanceFixture(t, false, true)}
	// The shared raw-config fixture leaves this operator setting off. Exercise
	// the real masking path instead of changing its exact masking assertion.
	h.f.al.GetConfig().Tools.FilterSensitiveData = true
	h.captureSafety(t)
	if restart {
		h.installRevivalReadCut(t)
	}
	var cards []secretrequest.Record
	h.f.al.SecretRequests().SetSink(func(rec secretrequest.Record) {
		cards = append(cards, rec)
	})
	ctx, episode := ordinaryExecutionContext(context.Background())
	finished := false
	t.Cleanup(func() {
		if !finished {
			require.NoError(t, h.f.al.finishExecutionDisposition(episode.disposition))
		}
	})
	_, _, err := h.f.al.processMessage(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: "sf02-ordinary-chat", SessionID: h.f.root,
		GatewayUserID: "qa-secret-wake-owner", Content: "Set up my API key with the secure card",
	})
	require.NoError(t, err)
	require.Len(t, cards, 1, "instrument: actual ordinary request_secret must publish one pending card")
	require.Equal(t, "pending", cards[0].Card.State)
	require.Equal(t, h.f.root, cards[0].Card.SessionId)
	require.Len(t, h.f.provider.calls(h.f.root), 1, "instrument: original turn parks, without consuming a secret resume")
	values := map[string]string{"api_key": secretWakeAcceptanceValue}
	h.record, err = h.f.al.SecretRequests().Submit(cards[0].Card.RequestId, cards[0].Owner, gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err, "instrument: original dispatch must ACCEPT the ordinary wake before any fault")
	require.Equal(t, "submitted", h.record.Card.State)
	h.original = h.f.takeWake(t, h.record.Card.RequestId)
	require.Equal(t, "webchat", h.original.Channel)
	require.Equal(t, h.f.root, h.original.SessionID)
	require.Empty(t, h.original.AsyncTranscriptSessionID, "instrument: this is ordinary, not the helper system-wake path")
	require.Equal(t, h.record.Card.RequestId, h.original.Metadata[secretResumeMetadataKey])
	h.credentialBytes, err = os.ReadFile(h.f.store.Path())
	require.NoError(t, err)
	h.credentialNames, err = h.f.store.List()
	require.NoError(t, err)
	h.metadata, err = h.f.store.CardSecretMetadata()
	require.NoError(t, err)
	var exists bool
	h.receipt, exists, err = h.f.store.CardReceipt(h.record.Card.RequestId)
	require.NoError(t, err)
	require.True(t, exists)
	require.Len(t, h.record.Card.ReferenceNames, 1)
	for _, ref := range h.record.Card.ReferenceNames {
		require.Equal(t, 1, h.metadata[ref].Version, "fresh card stores exactly its first version")
	}
	if restart {
		h.f.boot(t)
		landed, readErr := h.f.al.GetSessionLifecycleStore().Load(h.f.root)
		require.NoError(t, readErr)
		require.Equal(t, session.LifecycleStopped, landed.State)
		require.NotNil(t, landed.StopNote)
		require.Equal(t, session.StopCauseRestart, landed.StopNote.Cause, "instrument: real boot, not a fabricated restart flag")
	}
	// Retire the actual original outer producer before trying the accepted wake.
	require.NoError(t, h.f.al.finishExecutionDisposition(episode.disposition))
	finished = true
	require.Zero(t, secretWakeClaimCount(t, h.f.al, h.record))
	h.emitMaskControls(t)
	return h
}

// Fail an EXISTING dependency of reviveSecretRestart, not the revival itself.
// The real revival mutation commits first; its real state writer then reads a
// journal made temporarily unreadable at this construction-time writer seam.
func (h *wu4PreclaimFixture) installRevivalReadCut(t *testing.T) {
	h.revivalCut = &wu4PreclaimFileCut{path: filepath.Join(h.f.al.GetSessionLifecycleStore().Dir(), h.f.root+".jsonl")}
	t.Cleanup(func() { require.NoError(t, h.revivalCut.restore()) })
	canceller := NewSteerCanceller(h.f.al.GetSessionLifecycleStore(), h.f.al.SteerGenerationCancel)
	canceller.SetRevivalStateWriter(func(ctx context.Context, id string, generation int) error {
		if id == h.f.root && h.failRevivalRead {
			h.failRevivalRead = false
			if err := h.revivalCut.block(); err != nil {
				return err
			}
			h.revivalReadReached = true
		}
		return h.f.al.WriteSteerRevivalState(ctx, id, generation)
	})
	h.f.al.SetSteerCanceller(canceller)
}

func (h *wu4PreclaimFixture) process(msg bus.InboundMessage) error {
	msg.Metadata = maps.Clone(msg.Metadata)
	_, _, err := h.f.al.processMessage(context.Background(), msg)
	if err != nil {
		h.diagnostics = append(h.diagnostics, err.Error())
	}
	return err
}

func (h *wu4PreclaimFixture) takeRetryWakes() []bus.InboundMessage {
	var wakes []bus.InboundMessage
	for {
		select {
		case wake := <-h.f.bus.InboundChan():
			wakes = append(wakes, wake)
		default:
			return wakes
		}
	}
}
