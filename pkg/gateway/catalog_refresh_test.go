// catalog_refresh_test.go — the ADR-067 FR-008 gateway wiring (T067-07).
//
// Three things are pinned here, all of them things that have been silently
// wrong in this file's history:
//
//   - T42 the startup pull happens AFTER the listener is bound, and it flips
//     served_from from embedded to pulled; and it is skipped outright when the
//     persisted last-known-good is younger than an hour (F-34).
//   - T43 the ticker is the ONLY thing that pulls after startup: a thousand
//     read-path lookups and ten turn-shaped window resolutions add zero pulls,
//     and no source file outside the refresh loop calls Refresh at all.
//   - T44 a corrupt embedded snapshot degrades instead of aborting: boot
//     continues, exactly one ERROR is logged, the catalog serves nothing, and
//     the media gate stays optimistic (E7).
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// errStubPull is the fixed error failingPuller.Pull returns — no network, no
// valid document needed. Refresh treats any Pull error as "log and retain",
// which is a fast, deterministic, network-free signal that Refresh ran.
var errStubPull = errors.New("stubPuller: pull failure (test double)")

// recordingPuller records the wall-clock of every Pull and serves the bytes
// it was given. A nil body means "fail this pull".
type recordingPuller struct {
	mu    sync.Mutex
	hits  []time.Time
	body  []byte
	ready chan struct{} // closed after the first hit
	once  sync.Once
}

func newRecordingPuller(body []byte) *recordingPuller {
	return &recordingPuller{body: body, ready: make(chan struct{})}
}

func (p *recordingPuller) Pull(context.Context) ([]byte, error) {
	p.mu.Lock()
	p.hits = append(p.hits, time.Now())
	body := p.body
	p.mu.Unlock()
	p.once.Do(func() { close(p.ready) })
	if body == nil {
		return nil, errStubPull
	}
	return body, nil
}

func (p *recordingPuller) LastPullDegraded() (bool, error) { return false, nil }

func (p *recordingPuller) hitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.hits)
}

func (p *recordingPuller) firstHit() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.hits) == 0 {
		return time.Time{}, false
	}
	return p.hits[0], true
}

// testDocument builds a valid 2.0.0 document at the given version carrying one
// provider and one model, so a pulled document is distinguishable from the
// embedded snapshot by version alone.
func testDocument(t *testing.T, version string) []byte {
	t.Helper()
	doc := map[string]any{
		"schema_version":        "2.0.0",
		"version":               version,
		"updated_at":            time.Now().UTC().Format(time.RFC3339),
		"source":                "test",
		"default_resize_limits": map[string]any{"long_edge_px": 7680, "max_bytes": 10485760},
		"providers": []map[string]any{{
			"id":            "acme",
			"name":          "Acme",
			"api":           "https://api.example.test/v1",
			"protocol":      "openai-compatible",
			"tier":          "standard",
			"auth_methods":  []string{"api_key"},
			"resize_limits": map[string]any{"long_edge_px": 4096, "max_bytes": 5242880},
			"models": []map[string]any{{
				"id": "acme-1", "name": "Acme 1", "context_window": 200000,
				"input_modalities": []string{"text", "image"},
				"tool_call":        true, "status": "active",
			}},
		}},
	}
	data, err := json.Marshal(doc)
	require.NoError(t, err)
	return data
}

// TestGatewayBoot_OfflineSnapshot_Then_StartupPull (T42, US-3.AC1/AC2,
// FR-008/FR-010) drives the exact composition setupAndStartServices performs:
// catalog.Boot with no network, then — only once a listener is accepting —
// runCatalogRefreshLoop.
//
// It asserts the ordering structurally rather than by timing luck: the
// listener's bind time is captured before the loop is ever started, and the
// recording stub's FIRST hit must be later than it.
func TestGatewayBoot_OfflineSnapshot_Then_StartupPull(t *testing.T) {
	home := t.TempDir()

	// Boot with a puller that would fail if it were called — boot must not
	// call it at all, which is what "offline boot serves the snapshot" means.
	bootPuller := newRecordingPuller(nil)
	cat := catalog.Boot(
		context.Background(),
		catalog.EmbeddedSnapshot,
		bootPuller,
		catalog.NewFileStore(home),
		nil,
	)
	served, ok := cat.Served()
	require.True(t, ok, "offline boot must serve the embedded snapshot")
	assert.Equal(t, catalog.ServedEmbedded, served.From,
		"a boot that reached no network serves served_from=embedded")
	embeddedVersion := cat.Version()
	assert.Zero(t, bootPuller.hitCount(), "catalog.Boot must perform no network I/O")

	// Bind a listener, exactly as StartAll does, and only then start the loop.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	listenerBound := time.Now()

	// A version strictly above the embedded snapshot, so the apply is not
	// refused by the anti-downgrade rule.
	newer := fmt.Sprintf("v%d.1.1", time.Now().UTC().Year()+1)
	puller := newRecordingPuller(testDocument(t, newer))
	pulled := catalog.Boot(
		context.Background(),
		catalog.EmbeddedSnapshot,
		puller,
		catalog.NewFileStore(home),
		nil,
	)

	go runCatalogRefreshLoop(pulled, catalog.NewFileStore(home), time.Hour, 5*time.Second, time.Hour)

	select {
	case <-puller.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("startup pull never happened: the loop with a 1 h ticker must pull once immediately")
	}

	first, hadHit := puller.firstHit()
	require.True(t, hadHit)
	assert.True(t, first.After(listenerBound),
		"the first catalog pull must land AFTER the listener was bound (boot never waits on the network)")

	require.Eventually(t, func() bool {
		s, sok := pulled.Served()
		return sok && s.From == catalog.ServedPulled
	}, 5*time.Second, 10*time.Millisecond,
		"a successful startup pull must flip served_from from embedded to pulled")

	assert.NotEqual(t, embeddedVersion.String(), pulled.Version().String(),
		"the pulled document must replace the embedded snapshot's version")

	// FR-010: the pulled bytes are persisted as last-known-good.
	persisted := filepath.Join(home, catalog.PersistedFileName)
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(persisted)
		return statErr == nil
	}, 5*time.Second, 10*time.Millisecond, "a successful refresh must persist last-known-good")

	// F-34: with that fresh persisted file on disk, a gateway restarting now
	// must skip the startup pull entirely rather than spend GitHub's
	// unauthenticated rate limit re-fetching a document it already has.
	skipPuller := newRecordingPuller(testDocument(t, newer))
	skipCat := catalog.Boot(
		context.Background(),
		catalog.EmbeddedSnapshot,
		skipPuller,
		catalog.NewFileStore(home),
		nil,
	)
	go runCatalogRefreshLoop(skipCat, catalog.NewFileStore(home), time.Hour, 5*time.Second, time.Hour)
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, skipPuller.hitCount(),
		"startup pull must be skipped while the persisted document is younger than the skip window")
}

// TestSkipStartupPull_Window is the FR-008 skip predicate on its own: only a
// persisted document younger than the window skips; a missing or unreadable
// file never does, because there is nothing on disk to serve from.
func TestSkipStartupPull_Window(t *testing.T) {
	home := t.TempDir()
	store := catalog.NewFileStore(home)

	assert.False(t, skipStartupPull(store, time.Hour),
		"no persisted file at all → never skip; the pull is exactly what is wanted")

	require.NoError(t, store.Write(context.Background(), testDocument(t, "v2026.8.24")))
	assert.True(t, skipStartupPull(store, time.Hour),
		"a document just written is younger than the window → skip")
	assert.False(t, skipStartupPull(store, time.Nanosecond),
		"a window shorter than the file's age → pull")
	assert.False(t, skipStartupPull(store, 0),
		"a zero window disables the skip entirely")
	assert.False(t, skipStartupPull(nil, time.Hour),
		"no store → nothing to age → never skip")
}

// TestRefreshLoop_24h_NoRequestPathPulls (T43, FR-008, US-3.AC3) pins the
// invariant that matters operationally: after the one startup pull, nothing
// but the ticker pulls. A thousand read-path lookups and ten turn-shaped
// resolutions run against the live catalog and the pull count does not move.
//
// The interval used here (1 h) is far longer than the test, so any additional
// pull would have to have come from a request or turn path — the failure this
// asserts against.
func TestRefreshLoop_24h_NoRequestPathPulls(t *testing.T) {
	home := t.TempDir()
	puller := newRecordingPuller(testDocument(t, fmt.Sprintf("v%d.1.1", time.Now().UTC().Year()+1)))
	cat := catalog.Boot(
		context.Background(),
		catalog.EmbeddedSnapshot,
		puller,
		catalog.NewFileStore(home),
		nil,
	)

	go runCatalogRefreshLoop(cat, catalog.NewFileStore(home), time.Hour, 5*time.Second, 0)

	select {
	case <-puller.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("startup pull never happened")
	}
	require.Eventually(t, func() bool {
		s, ok := cat.Served()
		return ok && s.From == catalog.ServedPulled
	}, 5*time.Second, 10*time.Millisecond)

	after := puller.hitCount()
	require.Equal(t, 1, after, "exactly one startup pull")

	// 1,000 REST-shaped reads: everything a request handler does with the
	// catalog is Served/Provider/Resolve.
	for i := 0; i < 1000; i++ {
		_, ok := cat.Served()
		require.True(t, ok)
		_, _ = cat.Provider("acme")
		_ = cat.Resolve("acme", "acme-1").Window()
	}
	// 10 turn-shaped resolutions: the media gate and the resize budget.
	for i := 0; i < 10; i++ {
		require.True(t, cat.Resolve("acme", "acme-1").Supports(catalog.ModalityImage))
		require.NotZero(t, cat.Resolve("acme", "acme-1").Budget().LongEdgePx)
	}

	assert.Equal(t, 1, puller.hitCount(),
		"1,000 REST reads and 10 turns must add zero pulls — the ticker is the only pull trigger")
}

// TestRefreshLoop_TickerFires proves the other half of T43: the loop really
// does pull again on each tick, so the "no request-path pulls" assertion above
// is not passing merely because the loop is inert.
func TestRefreshLoop_TickerFires(t *testing.T) {
	home := t.TempDir()
	puller := newRecordingPuller(nil) // every pull fails; only the count matters
	cat := catalog.Boot(context.Background(), catalog.EmbeddedSnapshot, puller,
		catalog.NewFileStore(home), nil)

	go runCatalogRefreshLoop(cat, catalog.NewFileStore(home), 20*time.Millisecond, time.Second, 0)

	require.Eventually(t, func() bool { return puller.hitCount() >= 3 },
		5*time.Second, 10*time.Millisecond,
		"the ticker must keep pulling after the startup pull")
}

// TestRefreshLoop_NoCatalog_NoPanic guards the nil-catalog short circuit: the
// loop is started unconditionally from setupAndStartServices, and a test or a
// degraded boot can hand it nothing.
func TestRefreshLoop_NoCatalog_NoPanic(t *testing.T) {
	done := make(chan struct{})
	go func() {
		runCatalogRefreshLoop(nil, nil, time.Millisecond, time.Second, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runCatalogRefreshLoop(nil, ...) must return immediately, not spin")
	}
}

// TestEmbeddedSnapshot_Corrupt_BootDegrades (T44, E7) is the degraded-boot
// seam. Boot is handed an unparseable snapshot and no persisted fallback: it
// must return a catalog rather than fail, log exactly one ERROR naming the
// snapshot, serve nothing (so the REST handler answers 503 rather than an
// empty 200), and leave the media gate optimistic.
func TestEmbeddedSnapshot_Corrupt_BootDegrades(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "catalog-degraded.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.DEBUG)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	cat := catalog.Boot(
		context.Background(),
		[]byte("{not a catalog"),
		nil,
		catalog.NewFileStore(t.TempDir()),
		catalogLogAdapter{},
	)
	require.NotNil(t, cat, "a corrupt snapshot degrades; it never aborts boot")

	_, ok := cat.Served()
	assert.False(t, ok, "no document loaded → nothing to serve → the handler answers 503")
	assert.Nil(t, cat.Document())

	degraded, reason := cat.Degraded()
	assert.True(t, degraded)
	require.Error(t, reason)

	// The media path stays optimistic on a catalog with no document: a miss is
	// text+image, so images still reach the provider (FR-004).
	assert.True(t, cat.Resolve("openai", "gpt-5.4").Supports(catalog.ModalityImage),
		"a catalog with no document must not close the image gate")
	assert.Equal(t, catalog.DefaultResizeLimits, cat.Resolve("openai", "gpt-5.4").Budget(),
		"a catalog with no document serves the package default resize limits")

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	logged := string(data)
	assert.Contains(t, logged, "catalog: embedded snapshot is invalid")
	assert.Equal(t, 1, strings.Count(logged, "catalog: embedded snapshot is invalid"),
		"exactly one ERROR for the corrupt snapshot, not one per lookup")
}

// TestCatalogLogAdapter_RoutesToLoggerFileSink pins the log routing: the
// catalog package's diagnostics must land in $OMNIPUS_HOME/logs/gateway.log,
// not on log/slog's zero-value stderr handler, which is invisible on a
// backgrounded gateway.
func TestCatalogLogAdapter_RoutesToLoggerFileSink(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "catalog-log-adapter.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.DEBUG)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	adapter := catalogLogAdapter{}
	adapter.Warn("catalog refresh: document rejected", "reason", "checksum")
	adapter.Info("catalog refreshed", "version", "v2026.8.23.1")
	adapter.Error("catalog: embedded snapshot is invalid", "error", errors.New("boom-err-9f21"))

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	logged := string(data)
	assert.Contains(t, logged, "catalog refresh: document rejected")
	assert.Contains(t, logged, "checksum")
	assert.Contains(t, logged, "catalog refreshed")
	assert.Contains(t, logged, "v2026.8.23.1")
	assert.Contains(t, logged, "boom-err-9f21")
}

// TestSlogArgsToFields covers the key/value-pair conversion helper directly,
// including the malformed odd-length call site slog itself documents a
// "!BADKEY" convention for.
func TestSlogArgsToFields(t *testing.T) {
	fields := slogArgsToFields([]any{"a", 1, "b", "two"})
	assert.Equal(t, map[string]any{"a": 1, "b": "two"}, fields)

	fields = slogArgsToFields(nil)
	assert.Empty(t, fields)

	fields = slogArgsToFields([]any{"a", 1, "orphan"})
	assert.Equal(t, 1, fields["a"])
	assert.Equal(t, "orphan", fields["!BADKEY"])
}

// TestNoCatalogRefreshOutsideTheLoop is the structural half of T43: no
// gateway or agent source file may call Catalog.Refresh except the refresh
// loop itself. A pull wired into a request handler or a turn path would not
// show up as a failing assertion anywhere else — it would just quietly spend
// the rate limit on every request — so it is asserted on the source.
func TestNoCatalogRefreshOutsideTheLoop(t *testing.T) {
	roots := []string{".", filepath.Join("..", "agent")}
	var offenders []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		require.NoError(t, err)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(root, name))
			require.NoError(t, readErr)
			for i, line := range strings.Split(string(data), "\n") {
				if !strings.Contains(line, ".Refresh(ctx)") {
					continue
				}
				// The one sanctioned call site is inside runCatalogRefreshLoop.
				if root == "." && name == "gateway.go" && strings.Contains(line, "cat.Refresh(ctx)") {
					continue
				}
				offenders = append(offenders, fmt.Sprintf("%s/%s:%d: %s", root, name, i+1, strings.TrimSpace(line)))
			}
		}
	}
	assert.Empty(t, offenders,
		"catalog.Refresh may only be called from runCatalogRefreshLoop — no request path, no turn path (FR-008)")
}
