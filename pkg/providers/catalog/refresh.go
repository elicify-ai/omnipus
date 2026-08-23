package catalog

// refresh.go — the refresh transaction (T067-04, US-3, FR-009/FR-028).
//
// Refresh runs the whole pull → degraded check → parse (2.0.0 schema gate)
// → anti-downgrade → apply → persist sequence under one mutex, so two
// concurrent refreshes (a 24 h tick during a slow startup pull, E5) are
// serialized and can never apply out of order. Every rejection logs
// exactly ONE reason-keyed WARN — reason ∈ {checksum, schema_version,
// invalid, regressed, too_large} (FR-009) — and retains the currently
// served document; a transport failure (release and raw both unreachable,
// or a timeout) logs one WARN without a reason key, since no document was
// ever obtained to classify. Every successful refresh logs exactly one
// INFO and fires the OnRefreshApplied hooks (FR-037 — T067-11 registers
// the entitlement-cache invalidation there).
//
// The 24 h ticker and the startup pull that call Refresh live in the
// gateway wiring (T067-07); this package owns only the transaction.

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Puller fetches the current catalog release. The returned bytes MUST have
// been integrity-checked (sidecar verified) by the implementation;
// GHReleasePuller is the production Puller.
type Puller interface {
	// Pull returns the release asset bytes, or an error wrapping
	// ErrChecksumMismatch / ErrTooLarge when the asset was obtained but
	// failed acceptance, or a transport error when it was not obtained.
	Pull(ctx context.Context) ([]byte, error)
	// LastPullDegraded reports whether the most recent successful Pull
	// fell back to the raw transport, with the release-path error that
	// forced the fallback (US-3.AC8).
	LastPullDegraded() (degraded bool, releaseErr error)
}

// Logger is the minimal logging surface this package needs; *slog.Logger
// satisfies it. A nil Logger silences the package (tests, CLI tools).
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// The FR-009 WARN reason vocabulary.
const (
	reasonChecksum      = "checksum"
	reasonSchemaVersion = "schema_version"
	reasonInvalid       = "invalid"
	reasonRegressed     = "regressed"
	reasonTooLarge      = "too_large"
)

// OnRefreshApplied registers fn to run after every successfully applied
// refresh (FR-037). The gateway registers the entitlement-cache
// invalidation here (T067-11). Hooks run outside the catalog's internal
// locks, on the refreshing goroutine, after the new document is visible.
func (c *Catalog) OnRefreshApplied(fn func()) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.onApplied = append(c.onApplied, fn)
}

// Refresh executes one refresh transaction. Concurrent calls are
// serialized (FR-028): the second caller waits, then runs its own full
// transaction (an equal-version re-apply is a permitted no-op, DS-4 row 8).
// Every failure retains the currently served document and is returned to
// the caller after being logged; Refresh is always non-fatal to the
// gateway.
//
// A Catalog constructed without a Puller treats Refresh as a no-op — the
// embedded snapshot (or persisted last-known-good) is the only source.
func (c *Catalog) Refresh(ctx context.Context) error {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.refreshLocked(ctx)
}

// refreshLocked is the transaction body. Caller holds refreshMu.
func (c *Catalog) refreshLocked(ctx context.Context) error {
	if c.puller == nil {
		return nil
	}

	data, err := c.puller.Pull(ctx)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooLarge):
			c.rejectRefresh(err, reasonTooLarge)
		case errors.Is(err, ErrChecksumMismatch):
			c.rejectRefresh(err, reasonChecksum)
		default:
			// No document was obtained; there is nothing to classify
			// under FR-009 — one WARN, current document retained.
			c.logWarn("catalog refresh: pull failed; retaining current document", "error", err)
			c.setRefreshErr(err)
		}
		return err
	}
	degraded, releaseErr := c.puller.LastPullDegraded()

	doc, err := ParseDocument(data)
	if err != nil {
		if errors.Is(err, ErrSchemaVersion) {
			c.rejectRefresh(err, reasonSchemaVersion)
		} else {
			c.rejectRefresh(err, reasonInvalid)
		}
		return err
	}

	// Anti-downgrade (US-3.AC6): a pulled version below the served one is
	// refused; an equal version is a permitted no-op re-apply.
	if cur := c.cur.Load(); cur != nil && doc.Version.Compare(cur.doc.Version) < 0 {
		err := fmt.Errorf("catalog: pulled version %s is below served version %s", doc.Version, cur.doc.Version)
		c.rejectRefresh(err, reasonRegressed)
		return err
	}

	if err := c.applyDoc(doc, ServedPulled); err != nil {
		c.rejectRefresh(err, reasonInvalid)
		return err
	}

	// Persist the pulled bytes verbatim as last-known-good (FR-010). A
	// persist failure does not undo the apply — the document is already
	// serving; the next boot just falls back one release.
	if c.store != nil {
		if werr := c.store.Write(ctx, data); werr != nil {
			c.logWarn("catalog refresh: persisting last-known-good failed", "error", werr)
		}
	}

	c.stateMu.Lock()
	c.lastRefreshErr = nil
	c.degradedTransport = degraded
	c.degradedReleaseErr = releaseErr
	hooks := slices.Clone(c.onApplied)
	c.stateMu.Unlock()

	if degraded {
		c.logWarn("catalog refresh: release path failed; document pulled via raw fallback", "error", releaseErr)
	}
	c.logInfo("catalog refreshed", "version", doc.Version.String(), "served_from", string(ServedPulled))

	for _, fn := range hooks {
		fn()
	}
	return nil
}

// rejectRefresh logs the single reason-keyed WARN for a rejected refresh
// (FR-009) and records the failure for Degraded().
func (c *Catalog) rejectRefresh(err error, reason string) {
	c.logWarn("catalog refresh: document rejected; retaining current document", "reason", reason, "error", err)
	c.setRefreshErr(err)
}

func (c *Catalog) setRefreshErr(err error) {
	c.stateMu.Lock()
	c.lastRefreshErr = err
	c.stateMu.Unlock()
}

func (c *Catalog) logInfo(msg string, args ...any) {
	if c.log != nil {
		c.log.Info(msg, args...)
	}
}

func (c *Catalog) logWarn(msg string, args ...any) {
	if c.log != nil {
		c.log.Warn(msg, args...)
	}
}

func (c *Catalog) logError(msg string, args ...any) {
	if c.log != nil {
		c.log.Error(msg, args...)
	}
}
