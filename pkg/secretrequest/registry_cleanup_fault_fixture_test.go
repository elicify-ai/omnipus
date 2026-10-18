package secretrequest_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const registryRetainedValue = "REGISTRY-MAINTENANCE-STORED-WITNESS"

type registryReceiptCut struct {
	c        *registryFaultCapture
	old      secretrequest.Record
	pending  secretrequest.Record
	backup   string
	before   []byte
	armed    bool
	active   bool
	cutCount int
}

func newRegistryReceiptCut(t *testing.T) *registryReceiptCut {
	t.Helper()
	c := newRegistryFaultCapture(t)
	f := c.f
	cut := &registryReceiptCut{c: c, backup: f.store.Path() + ".maintenance-cut-preserved"}
	opts := c.options(f.sessions, f.store)
	opts.Credentials = func() (*credentials.Store, error) {
		// The public late-bound store-open seam is a process boundary. Arm the
		// ACTUAL I/O fault only when the cancelled session bytes are published.
		// Earlier opens are allowed: no planned private queue/order is pinned.
		if cut.armed {
			for _, rec := range c.diskRecords(t, cut.pending.Card.SessionId) {
				if rec.Card.RequestId == cut.pending.Card.RequestId && rec.Card.State == "cancelled" {
					require.NoError(t, os.Rename(f.store.Path(), cut.backup))
					require.NoError(t, os.Mkdir(f.store.Path(), 0o700))
					cut.active, cut.armed = true, false
					cut.cutCount++
					break
				}
			}
		}
		return f.store, nil
	}
	f.reg = secretrequest.NewRegistry(opts)
	f.reg.SetSink(c.notify)
	t.Cleanup(func() { cut.repair(t) })
	sid := f.newSession("omnipus", "")
	first := f.create(sid, "omnipus", providerInput())
	var err error
	cut.old, err = f.reg.Submit(first.Card.RequestId, matrixOwner, valuesInput(map[string]string{"api_key": registryRetainedValue}))
	require.NoError(t, err)
	require.Equal(t, "submitted", cut.old.Card.State)
	require.Len(t, c.auditTransitions(t, first.Card.RequestId, "submitted"), 1, "normal Submit must reach the audit sink")
	// One submitted receipt + 31 cancelled cards = the specified 32 terminal
	// states. The next cancellation, not fixture fabrication, crosses the cap.
	for range secretrequest.MaxTerminalPerSession - 1 {
		pending := f.create(sid, "omnipus", providerInput())
		cancelled, cancelErr := f.reg.Submit(pending.Card.RequestId, matrixOwner, cancelInput())
		require.NoError(t, cancelErr)
		require.Equal(t, "cancelled", cancelled.Card.State)
	}
	require.Len(t, f.reg.Snapshot(matrixOwner, sid), 32)
	cut.pending = f.create(sid, "omnipus", providerInput())
	require.Len(t, c.diskRecords(t, sid), 33, "32 terminal plus one real pending card")
	cut.before, err = os.ReadFile(f.store.Path())
	require.NoError(t, err)
	return cut
}

func (cut *registryReceiptCut) cancelAcrossPublicationCut(t *testing.T) (secretrequest.Record, error) {
	t.Helper()
	cut.armed = true
	out, err := cut.c.f.reg.Submit(cut.pending.Card.RequestId, matrixOwner, cancelInput())
	require.Equal(t, 1, cut.cutCount, "instrument must cut real credential I/O AFTER cancelled state publication")
	require.True(t, cut.active)
	preserved, readErr := os.ReadFile(cut.backup)
	require.NoError(t, readErr)
	require.Equal(t, cut.before, preserved, "receipt fault cannot alter the preserved ciphertext/bindings/receipt")
	_, _, receiptErr := cut.c.f.store.CardReceipt(cut.old.Card.RequestId)
	require.Error(t, receiptErr, "directory obstruction must cause an actual read failure, not a fake callback error")
	var pathErr *os.PathError
	require.ErrorAs(t, receiptErr, &pathErr)
	return out, err
}

func (cut *registryReceiptCut) repair(t *testing.T) {
	t.Helper()
	if !cut.active {
		return
	}
	// Remove ONLY the empty test obstruction, never the encrypted store.
	require.NoError(t, os.Remove(cut.c.f.store.Path()))
	require.NoError(t, os.Rename(cut.backup, cut.c.f.store.Path()))
	cut.active = false
}

func registryRecordByID(records []secretrequest.Record, id string) (secretrequest.Record, bool) {
	for _, rec := range records {
		if rec.Card.RequestId == id {
			return rec, true
		}
	}
	return secretrequest.Record{}, false
}

func assertRegistryCancelledConflict(t *testing.T, rec secretrequest.Record, err error) {
	t.Helper()
	assert.Equal(t, "cancelled", rec.Card.State, "terminal cancellation cannot be reopened for plaintext resubmit")
	var refusal *secretrequest.RequestError
	if !assert.True(t, errors.As(err, &refusal), "resubmit must be an explicit 409/cancelled, got %v", err) {
		return
	}
	assert.Equal(t, http.StatusConflict, refusal.Status)
	if assert.NotNil(t, refusal.State) {
		assert.Equal(t, gen.SecretRequestStateState("cancelled"), refusal.State.State)
	}
}

func (cut *registryReceiptCut) assertEncryptedValuesAndBindingsUnchanged(t *testing.T) {
	t.Helper()
	f := cut.c.f
	after, err := os.ReadFile(f.store.Path())
	require.NoError(t, err)
	var beforeEnvelope, afterEnvelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(cut.before, &beforeEnvelope))
	require.NoError(t, json.Unmarshal(after, &afterEnvelope))
	for _, key := range []string{"credentials", "card_secrets"} {
		require.NotEmpty(t, beforeEnvelope[key], "instrument must compare actual encrypted data and recipient bindings")
		assert.JSONEq(t, string(beforeEnvelope[key]), string(afterEnvelope[key]), "cleanup may change receipt metadata ONLY: %s changed", key)
	}
	value, err := f.store.Get(cut.old.Card.ReferenceNames[0])
	require.NoError(t, err)
	assert.Equal(t, registryRetainedValue, value)
	metadata, err := f.store.CardSecretMetadata()
	require.NoError(t, err)
	assert.Equal(t, map[string]credentials.CardSecret{
		cut.old.Card.ReferenceNames[0]: {Recipient: cut.old.Recipients[0], Version: 1},
	}, metadata)
}

func blockRegistryStatePublication(t *testing.T, c *registryFaultCapture, sid string) func() {
	t.Helper()
	path := filepath.Join(c.f.sessions.BaseDir(), sid, "secret_requests.json")
	preserved := path + ".publication-cut-preserved"
	require.NoError(t, os.Rename(path, preserved))
	require.NoError(t, os.Mkdir(path, 0o700))
	active := true
	repair := func() {
		if active {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Rename(preserved, path))
			active = false
		}
	}
	t.Cleanup(repair)
	return repair
}
