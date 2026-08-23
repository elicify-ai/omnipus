package catalog

// T067-04 — the refresh transaction (spec §7 TDD Plan):
//
//	T9   TestRefresh_ChecksumMismatch_Retains        — one WARN reason=checksum, document retained (US-3.AC4, DS-4.2)
//	T10  TestRefresh_WrongSchemaVersion_Ignored      — one WARN reason=schema_version (US-3.AC5, DS-4.6)
//	T11  TestRefresh_Downgrade_Refused               — one WARN reason=regressed (US-3.AC6, DS-4.7)
//	T12  TestRefresh_TooLarge_WarnsTooLargeNeverChecksum — refresh half: WARN reason=too_large (E1/E14; puller half in puller_test.go)
//	T14  TestRefresh_Concurrent_Serialized           — refresh mutex serializes concurrent pulls (E5, FR-028, -race)
//	     TestRefresh_Success_AppliesPersistsNotifies — DS-4 rows 1 and 8: apply, persist, one INFO, hook fired
//	     TestRefresh_RawFallbackApplied_DegradedReported — DS-4 row 4 (US-3.AC8)
//	     TestRefresh_TransportFailure_Retains        — DS-4 rows 5 and 9
//	     TestRefresh_InvalidDocument_Retains         — FR-009 reason=invalid

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── shared T067-04 test doubles ──────────────────────────────────────────────

// capturedLog is one captured log record: level, message, and the
// key-value args flattened into a map.
type capturedLog struct {
	level string
	msg   string
	attrs map[string]any
}

// captureLogger satisfies Logger and records every call so the DS-4
// "exactly one WARN with reason=<x>" rows are assertable.
type captureLogger struct {
	mu      sync.Mutex
	records []capturedLog
}

func (l *captureLogger) log(level, msg string, args ...any) {
	attrs := map[string]any{}
	for i := 0; i+1 < len(args); i += 2 {
		if k, ok := args[i].(string); ok {
			attrs[k] = args[i+1]
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, capturedLog{level: level, msg: msg, attrs: attrs})
}

func (l *captureLogger) Info(msg string, args ...any)  { l.log("INFO", msg, args...) }
func (l *captureLogger) Warn(msg string, args ...any)  { l.log("WARN", msg, args...) }
func (l *captureLogger) Error(msg string, args ...any) { l.log("ERROR", msg, args...) }

func (l *captureLogger) byLevel(level string) []capturedLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []capturedLog
	for _, r := range l.records {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

// mentions counts records whose message or any attr value contains substr —
// the T15 "zero log lines mention capabilities_catalog.json" probe.
func (l *captureLogger) mentions(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.records {
		hit := strings.Contains(r.msg, substr)
		for k, v := range r.attrs {
			if strings.Contains(k, substr) || strings.Contains(fmt.Sprint(v), substr) {
				hit = true
			}
		}
		if hit {
			n++
		}
	}
	return n
}

// requireOneWarnWithReason asserts exactly one WARN was logged and that it
// carries the given reason key (FR-009).
func requireOneWarnWithReason(t *testing.T, log *captureLogger, reason string) {
	t.Helper()
	warns := log.byLevel("WARN")
	require.Len(t, warns, 1, "FR-009: exactly one WARN, got %+v", warns)
	assert.Equal(t, reason, warns[0].attrs["reason"], "WARN must carry reason=%s, got %+v", reason, warns[0])
}

// fakePuller is the Pull seam for the refresh-transaction tests (the real
// transport behaviour is covered by the GHReleasePuller tests).
type fakePuller struct {
	mu         sync.Mutex
	data       []byte
	err        error
	degraded   bool
	releaseErr error
	pulls      int

	gate        chan struct{} // when non-nil, Pull blocks until closed
	inflight    atomic.Int32
	maxInflight atomic.Int32
}

func (p *fakePuller) Pull(ctx context.Context) ([]byte, error) {
	in := p.inflight.Add(1)
	defer p.inflight.Add(-1)
	for {
		cur := p.maxInflight.Load()
		if in <= cur || p.maxInflight.CompareAndSwap(cur, in) {
			break
		}
	}
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pulls++
	if p.err != nil {
		return nil, p.err
	}
	return append([]byte(nil), p.data...), nil
}

func (p *fakePuller) LastPullDegraded() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.degraded, p.releaseErr
}

func (p *fakePuller) pullCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pulls
}

// fixtureWithVersion re-encodes the FR-027 fixture at a different version.
func fixtureWithVersion(t *testing.T, version string) []byte {
	t.Helper()
	m := fixtureMap(t)
	m["version"] = version
	return encode(t, m)
}

// fixtureWithSchemaVersion re-encodes the fixture at a different schema_version.
func fixtureWithSchemaVersion(t *testing.T, schemaVersion string) []byte {
	t.Helper()
	m := fixtureMap(t)
	m["schema_version"] = schemaVersion
	return encode(t, m)
}

// bootEmbedded boots a catalog from the fixture as the embedded snapshot.
func bootEmbedded(t *testing.T, p Puller, s Store, log Logger) *Catalog {
	t.Helper()
	c := Boot(context.Background(), loadFixture(t), p, s, log)
	require.NotNil(t, c.Document(), "fixture must boot")
	return c
}

// ── T9 — DS-4 row 2, US-3.AC4 ────────────────────────────────────────────────

func TestRefresh_ChecksumMismatch_Retains(t *testing.T) {
	log := &captureLogger{}
	p := &fakePuller{err: fmt.Errorf("catalog: pull release: %w", ErrChecksumMismatch)}
	c := bootEmbedded(t, p, nil, log)
	before, ok := c.Served()
	require.True(t, ok)

	err := c.Refresh(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrChecksumMismatch))

	requireOneWarnWithReason(t, log, "checksum")
	after, ok := c.Served()
	require.True(t, ok)
	assert.Equal(t, before.ETag, after.ETag, "current document must continue to serve")
	assert.Equal(t, ServedEmbedded, after.From)
	assert.Equal(t, "v2026.8.22", c.Version().String())

	degraded, derr := c.Degraded()
	assert.True(t, degraded, "FR-037: a failed refresh degrades /health")
	require.Error(t, derr)
}

// ── T10 — DS-4 row 6, US-3.AC5 ───────────────────────────────────────────────

func TestRefresh_WrongSchemaVersion_Ignored(t *testing.T) {
	log := &captureLogger{}
	p := &fakePuller{data: fixtureWithSchemaVersion(t, "1.0.0")}
	c := bootEmbedded(t, p, nil, log)

	err := c.Refresh(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSchemaVersion))

	requireOneWarnWithReason(t, log, "schema_version")
	assert.Equal(t, "v2026.8.22", c.Version().String())
	served, ok := c.Served()
	require.True(t, ok)
	assert.Equal(t, ServedEmbedded, served.From)
}

// ── T11 — DS-4 row 7, US-3.AC6 ───────────────────────────────────────────────

func TestRefresh_Downgrade_Refused(t *testing.T) {
	log := &captureLogger{}
	p := &fakePuller{data: fixtureWithVersion(t, "v2026.8.21")}
	c := bootEmbedded(t, p, nil, log)

	err := c.Refresh(context.Background())
	require.Error(t, err)

	requireOneWarnWithReason(t, log, "regressed")
	assert.Equal(t, "v2026.8.22", c.Version().String(), "no downgrade")
}

// ── T12 (refresh half) — DS-1.13, E1/E14 ─────────────────────────────────────

func TestRefresh_TooLarge_WarnsTooLargeNeverChecksum(t *testing.T) {
	log := &captureLogger{}
	p := &fakePuller{err: fmt.Errorf("catalog: pull release: %w", ErrTooLarge)}
	c := bootEmbedded(t, p, nil, log)

	err := c.Refresh(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrTooLarge))

	requireOneWarnWithReason(t, log, "too_large")
	for _, r := range log.byLevel("WARN") {
		assert.NotEqual(t, "checksum", r.attrs["reason"], "an oversize body is never misreported as a checksum mismatch (E14)")
	}
	assert.Equal(t, "v2026.8.22", c.Version().String())
}

// ── FR-009 reason=invalid ────────────────────────────────────────────────────

func TestRefresh_InvalidDocument_Retains(t *testing.T) {
	log := &captureLogger{}
	m := fixtureMap(t)
	delete(m, "providers")
	p := &fakePuller{data: encode(t, m)}
	c := bootEmbedded(t, p, nil, log)

	err := c.Refresh(context.Background())
	require.Error(t, err)

	requireOneWarnWithReason(t, log, "invalid")
	assert.Equal(t, "v2026.8.22", c.Version().String())
}

// ── DS-4 rows 1 and 8 — apply, persist, one INFO, entitlement hook ──────────

func TestRefresh_Success_AppliesPersistsNotifies(t *testing.T) {
	dir := t.TempDir()
	log := &captureLogger{}
	newDoc := fixtureWithVersion(t, "v2026.8.23")
	p := &fakePuller{data: newDoc}
	c := bootEmbedded(t, p, NewFileStore(dir), log)

	var hookFired atomic.Int32
	c.OnRefreshApplied(func() { hookFired.Add(1) })

	before, ok := c.Served()
	require.True(t, ok)

	require.NoError(t, c.Refresh(context.Background()))

	// DS-4 row 1: applied and persisted.
	assert.Equal(t, "v2026.8.23", c.Version().String())
	after, ok := c.Served()
	require.True(t, ok)
	assert.Equal(t, ServedPulled, after.From)
	assert.NotEqual(t, before.ETag, after.ETag, "E10: the ETag changes when a refresh lands")

	persisted, err := os.ReadFile(filepath.Join(dir, PersistedFileName))
	require.NoError(t, err)
	assert.Equal(t, newDoc, persisted, "FR-010: the pulled bytes are the persisted last-known-good")

	assert.Len(t, log.byLevel("INFO"), 1, "one INFO per successful refresh")
	assert.Empty(t, log.byLevel("WARN"))
	assert.Equal(t, int32(1), hookFired.Load(), "FR-037: refresh invalidates the entitlement cache via the hook")

	degraded, _ := c.Degraded()
	assert.False(t, degraded)

	// DS-4 row 8: an equal-version pull is applied (no-op allowed), not refused.
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, "v2026.8.23", c.Version().String())
	assert.Len(t, log.byLevel("INFO"), 2)
	assert.Empty(t, log.byLevel("WARN"))
	assert.Equal(t, int32(2), hookFired.Load())
}

// ── DS-4 row 4 — US-3.AC8: raw fallback applied, Degraded reports it ────────

func TestRefresh_RawFallbackApplied_DegradedReported(t *testing.T) {
	log := &captureLogger{}
	p := &fakePuller{
		data:       fixtureWithVersion(t, "v2026.8.23"),
		degraded:   true,
		releaseErr: errors.New("release status: 403"),
	}
	c := bootEmbedded(t, p, nil, log)

	require.NoError(t, c.Refresh(context.Background()))

	assert.Equal(t, "v2026.8.23", c.Version().String())
	served, ok := c.Served()
	require.True(t, ok)
	assert.Equal(t, ServedPulled, served.From)

	degraded, derr := c.Degraded()
	assert.True(t, degraded, "US-3.AC8: the degraded transport is recorded")
	require.Error(t, derr)
	assert.Contains(t, derr.Error(), "release status: 403")
}

// ── DS-4 rows 5 and 9 — transport failure and timeout retain ────────────────

func TestRefresh_TransportFailure_Retains(t *testing.T) {
	cases := map[string]error{
		"release 403, raw 404": errors.New("catalog: pull failed (release=status: 403, raw=status: 404)"),
		"timeout":              fmt.Errorf("catalog: pull: %w", context.DeadlineExceeded),
	}
	for name, pullErr := range cases {
		t.Run(name, func(t *testing.T) {
			log := &captureLogger{}
			p := &fakePuller{err: pullErr}
			c := bootEmbedded(t, p, nil, log)

			err := c.Refresh(context.Background())
			require.Error(t, err)

			warns := log.byLevel("WARN")
			require.Len(t, warns, 1, "exactly one WARN per failed attempt")
			assert.Equal(t, "v2026.8.22", c.Version().String())

			degraded, derr := c.Degraded()
			assert.True(t, degraded)
			require.Error(t, derr)
		})
	}
}

// ── T14 — E5, FR-028: concurrent refreshes are serialized (-race) ───────────

func TestRefresh_Concurrent_Serialized(t *testing.T) {
	gate := make(chan struct{})
	p := &fakePuller{data: fixtureWithVersion(t, "v2026.8.23"), gate: gate}
	log := &captureLogger{}
	c := bootEmbedded(t, p, nil, log)

	const refreshers = 4
	var wg sync.WaitGroup
	for range refreshers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Refresh(context.Background())
		}()
	}

	// Readers hammer the served pair while the refreshes queue behind the
	// mutex; the -race gate proves there is no torn state.
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if s, ok := c.Served(); ok && len(s.Body) == 0 {
					t.Error("served body must never be empty")
					return
				}
				_ = c.Resolve("zai", "glm-5.2")
			}
		}()
	}

	time.Sleep(50 * time.Millisecond) // let the first Refresh enter Pull and the rest queue
	close(gate)
	wg.Wait()
	close(stop)
	readers.Wait()

	assert.Equal(t, int32(1), p.maxInflight.Load(), "E5: the refresh mutex serializes pulls — never two in flight")
	assert.Equal(t, refreshers, p.pullCount(), "every queued refresh runs, one at a time")
	assert.Equal(t, "v2026.8.23", c.Version().String())
}

// A catalog constructed without a puller treats Refresh as a no-op instead
// of failing (the boot path for tests and CLI tools that must never dial out).
func TestRefresh_NoPuller_NoOp(t *testing.T) {
	log := &captureLogger{}
	c := bootEmbedded(t, nil, nil, log)
	require.NoError(t, c.Refresh(context.Background()))
	assert.Empty(t, log.byLevel("WARN"))
	assert.Empty(t, log.byLevel("INFO"))
}
