// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/stretchr/testify/require"
)

// parkedPuller holds the pull open until the context is cancelled, then
// returns a valid document anyway — modelling a GitHub release download that
// completes just as the gateway is stopping.
type parkedPuller struct {
	body  []byte
	ready chan struct{}
}

func (p *parkedPuller) Pull(ctx context.Context) ([]byte, error) {
	close(p.ready)
	<-ctx.Done()
	return p.body, nil
}
func (p *parkedPuller) LastPullDegraded() (bool, error) { return false, nil }

// TestStartCatalogRefreshLoop_ShutdownCancelsWaitsAndNeverPersists pins the
// cancel-and-wait contract shutdown relies on: after cancel(), done closes
// once the goroutine has EXITED, and a pull that lands after the cancel does
// not write providers_catalog.json. On 2026-09-12 the fire-and-forget form
// wrote the 2.4 MB file into integration-test home dirs after their gateway
// had stopped and t.TempDir had removed them.
//
// DIES ON: startCatalogRefreshLoop not closing done when the loop exits, or
// the persist step ignoring a cancelled context.
func TestStartCatalogRefreshLoop_ShutdownCancelsWaitsAndNeverPersists(t *testing.T) {
	home := t.TempDir()
	puller := &parkedPuller{body: testDocument(t, "v9999.1.1"), ready: make(chan struct{})}
	cat := catalog.Boot(context.Background(), catalog.EmbeddedSnapshot, puller, catalog.NewFileStore(home), nil)

	cancel, done := startCatalogRefreshLoop(context.Background(), cat, catalog.NewFileStore(home), time.Hour, 5*time.Second, 0)

	select {
	case <-puller.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("startup pull never started")
	}

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh loop did not exit within 3s of cancel — shutdown would return with a writer still running")
	}
	require.Less(t, time.Since(start), 3*time.Second)

	// The pull returned a valid, newer document AFTER cancellation. It must
	// not have been persisted.
	_, err := os.Stat(filepath.Join(home, catalog.PersistedFileName))
	require.True(t, os.IsNotExist(err), "providers_catalog.json must not be written after shutdown cancel; stat: %v", err)
}
