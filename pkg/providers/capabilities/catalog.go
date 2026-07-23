// Package capabilities is the in-repo capability catalog transport for
// provider/model input modalities (FR-024/025/026/027, ADR-051 Rev 4 §Capability
// source). It is intentionally separate from pkg/providers/catalog — that
// package is the *provider metadata* (company × plan × region × wire), not the
// model-capability data this package carries. The two packages address
// orthogonal concerns; this one is the source of truth for "which models
// accept which input modalities" used by the Layer-1 presentation
// orchestrator (pkg/agent/media_present.go, Wave 3).
//
// # Design
//
//   - Seed: a compiled-in JSON file embedded via go:embed (data/providers_capabilities_seed.json).
//     The seed is the FR-024 freeze-gate artifact: each provider's modalities
//     are re-validated against current official docs before the seed is
//     committed; see docs/internal/research/provider-media-format-support-2026-07.md.
//   - Puller: a Puller interface fetches updated catalog JSON from the Omnipus
//     repo on gateway startup and every 7 days. The default implementation is
//     GHReleasePuller (GitHub Release asset, semver-tagged, checksum-verified,
//     with raw.githubusercontent.com fallback). Pull failure is non-fatal —
//     last-known-good is retained (FR-025, SC-009).
//   - Resolver: Resolve(modelID) returns the Model entry, or an optimistic
//     default (image-capable) for unknown models (FR-026). The optimistic
//     default bounds blast radius: a wrong guess costs one outcome-based
//     step-4 retry (pkg/agent/media_downgrade.go), never a dead turn.
//   - Override scope: GLOBAL SEED ONLY (FR-027). There are no per-agent or
//     per-workspace override paths; operators edit the seed file directly and
//     publish a new catalog release, which the next pull picks up.
//
// # Wire types
//
// The seed JSON is internal — not a wire format. It does NOT cross the
// gateway/SPA boundary and is not in contracts/components/schemas/. Per
// hard-constraint #8, the wire types referenced by the orchestrator are the
// gen.* types in pkg/api/generated/. The Catalog returns internal Model values
// to callers; the orchestrator translates them to wire types at the API edge
// if/when SPA exposure is added (out of scope for v0.1.1 per the spec).
//
// # Package size budget
//
// Hard-constraint #3 limits security-feature RAM overhead to <10 MB beyond
// baseline. The seed is ~70 models ≈ 8 KB; the in-memory map ≈ 30 KB; puller
// uses an http.Client from the gateway's existing pool (no new sockets).
// Net overhead: <50 KB. The pull runs once per 7 days (FR-025) and on gateway
// startup; the http response body is read with io.LimitReader at 2 MB.
package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ResizeBudget is the per-model resize budget for step-2 normalize-and-resize
// (FR-014/FR-015). When absent on a Model entry, callers fall back to the
// catalog's DefaultResizeBudget (long_edge_px=7680, max_bytes=10 MB — the
// documented default covering every provider in the freeze-gate matrix).
type ResizeBudget struct {
	// LongEdgePx is the max long-edge dimension after resize. Images longer
	// than this on the long edge are scaled down (preserving aspect ratio)
	// before send.
	LongEdgePx int `json:"long_edge_px"`
	// MaxBytes is the max file size in bytes after the PNG→JPEG quality
	// ladder (FR-015). Files still over budget route to step-5 offload.
	MaxBytes int64 `json:"max_bytes"`
}

// Model is one catalog entry — the input_modalities a model accepts plus its
// resize budget. ID is the canonical model identifier used by the omnipus
// providers/* layer; provider is the lowercased catalog provider id
// (openai/anthropic/google/xai/mistral/deepseek/z-ai/moonshot/minimax).
//
// Unknown models (Resolve returns the optimistic default) carry the marker
// input_modalities=["text","image"] and the catalog's default resize budget.
// Callers MUST NOT distinguish "explicitly optimistic" from "catalog optimistic"
// — that distinction belongs to logs/diagnostics, not to runtime behavior
// (FR-026).
type Model struct {
	// ID is the canonical model identifier. Stable across provider renames.
	ID string `json:"id"`
	// Provider is the lowercased provider id from pkg/providers/catalog.
	Provider string `json:"provider"`
	// InputModalities is the set of input modalities the model accepts.
	// Permitted values: "text", "image", "pdf", "audio", "video". Unknown
	// values are accepted at parse time (forward-compatibility) but flagged
	// via the diagnostics channel; they do not change Resolve semantics.
	InputModalities []string `json:"input_modalities"`
	// ResizeBudget overrides the catalog default for this model. Nil →
	// use catalog default (or the package-level DefaultResizeBudget).
	ResizeBudget *ResizeBudget `json:"resize_budget,omitempty"`
	// Notes is a free-form human-readable annotation (provider source, last
	// validated date). Never sent on the wire; for diagnostic logs only.
	Notes string `json:"notes,omitempty"`
}

// Puller fetches an updated catalog JSON from a remote source. Implementations
// MUST be safe to call from multiple goroutines — Catalog.Refresh serializes
// calls but stores the returned []byte in the live model map; concurrent
// callers see whichever result lands last. Puller.Pull returns the raw JSON
// bytes (a single JSON object — the catalog schema — never an array or stream).
//
// Pull failures (network, transport, malformed JSON, checksum mismatch) MUST
// be returned as errors. The Catalog caller decides the policy (always
// non-fatal — last-known-good retained).
type Puller interface {
	Pull(ctx context.Context) ([]byte, error)
}

// Store persists the last-known-good catalog JSON across restarts. Read is
// called on Catalog construction (before the first pull) so the catalog boots
// with prior data even when the network is unreachable. Write is called after
// a successful Refresh so the next boot has the latest data.
//
// Implementations need not be concurrent-safe across the full read/write
// boundary — Catalog serializes both calls — but reads/writes on the same
// Store instance from Catalog's own goroutine ordering are safe.
type Store interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
}

// Seed is the parsed in-memory representation of the embedded
// data/providers_capabilities_seed.json. The Catalog holds a map of Model
// keyed by Model.ID plus the catalog-wide default resize budget.
type Seed struct {
	Version             string       `json:"version"`
	SchemaVersion       string       `json:"schema_version"`
	UpdatedAt           time.Time    `json:"updated_at"`
	Source              string       `json:"source"`
	Models              []Model      `json:"models"`
	DefaultResizeBudget ResizeBudget `json:"default_resize_budget"`
}

// Catalog is the live in-memory model capability registry.
//
// Concurrent use: Resolve is read-mostly and takes a read-lock; Refresh takes
// a write-lock. Pull failure (any error from Puller.Pull) is non-fatal — the
// in-memory map retains its prior values and the error is returned to the
// caller for logging/telemetry (FR-025, SC-009).
type Catalog struct {
	mu            sync.RWMutex
	models        map[string]Model
	defaultBudget ResizeBudget
	version       string
	updatedAt     time.Time
	source        string

	puller Puller
	store  Store
	logger logger
}

// logger is the minimal logger surface Catalog uses; it is satisfied by
// *slog.Logger and by no-op test loggers. Defined as an interface so tests
// can capture log output without dragging in slog.
type logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// ParseSeed parses the embedded seed JSON. Used by tests and by
// NewCatalog; exported so callers can inspect the seed without constructing
// a Catalog (e.g. for documentation generators, smoke tests).
//
// Returns a Seed with Models as a slice (preserves order for diagnostics).
// NewCatalog converts this slice to the internal map. Returns an error on
// any JSON parse failure or schema validation failure (unknown modality,
// invalid resize budget, etc.).
//
// Forward-compatibility: unknown top-level fields and unknown modality
// strings are accepted (logged at warn via the catalog logger at apply time,
// never persisted). This lets the seed schema evolve ahead of the
// orchestrator's awareness — see Seed.validate.
func ParseSeed(data []byte) (Seed, error) {
	var s Seed
	if err := json.Unmarshal(data, &s); err != nil {
		return Seed{}, fmt.Errorf("capabilities: parse seed: %w", err)
	}
	if err := s.validate(); err != nil {
		return Seed{}, fmt.Errorf("capabilities: validate seed: %w", err)
	}
	return s, nil
}

// validate enforces the seed schema invariants:
//   - DefaultResizeBudget has positive LongEdgePx and positive MaxBytes.
//   - Each Model.ID is non-empty and unique.
//   - Each Model.InputModalities is non-empty (every model accepts at least "text").
//   - Each modality value is a known string OR a non-empty unknown value
//     accepted for forward compatibility (recorded as-is).
//   - Each ResizeBudget has positive LongEdgePx and positive MaxBytes.
//
// Forward-compatibility note: a NEW modality (e.g. "3d") added by a future
// catalog release is accepted at parse time. Catalog.Resolve returns the
// model as-is; callers that don't recognize the modality fall back to
// capability-gate conservative (treat as unsupported). This is intentional
// — the seed schema is allowed to evolve ahead of the orchestrator's
// awareness.
func (s Seed) validate() error {
	if s.DefaultResizeBudget.LongEdgePx <= 0 {
		return fmt.Errorf("default_resize_budget.long_edge_px must be > 0, got %d", s.DefaultResizeBudget.LongEdgePx)
	}
	if s.DefaultResizeBudget.MaxBytes <= 0 {
		return fmt.Errorf("default_resize_budget.max_bytes must be > 0, got %d", s.DefaultResizeBudget.MaxBytes)
	}
	seen := make(map[string]bool, len(s.Models))
	for i, m := range s.Models {
		if m.ID == "" {
			return fmt.Errorf("models[%d].id must be non-empty", i)
		}
		if seen[m.ID] {
			return fmt.Errorf("models[%d].id %q is duplicated", i, m.ID)
		}
		seen[m.ID] = true
		if len(m.InputModalities) == 0 {
			return fmt.Errorf("models[%d].id %q has empty input_modalities", i, m.ID)
		}
		if m.ResizeBudget != nil {
			if m.ResizeBudget.LongEdgePx <= 0 {
				return fmt.Errorf(
					"models[%d].id %q: resize_budget.long_edge_px must be > 0, got %d",
					i, m.ID, m.ResizeBudget.LongEdgePx,
				)
			}
			if m.ResizeBudget.MaxBytes <= 0 {
				return fmt.Errorf(
					"models[%d].id %q: resize_budget.max_bytes must be > 0, got %d",
					i, m.ID, m.ResizeBudget.MaxBytes,
				)
			}
		}
	}
	return nil
}

// NewCatalog constructs a Catalog from the embedded seed plus the puller and
// store. If puller is nil, Refresh becomes a no-op (the seed is the only
// data source — useful for tests and CLI tools that should never call out).
// If store is nil, last-known-good persistence is disabled (the in-memory
// map is the only state; reboots start from the embedded seed).
//
// On construction, Catalog attempts to hydrate from Store first (last-known-good
// from the prior boot); only on Store read failure does it fall back to the
// embedded seed. This bounds the blast radius of a corrupted embedded seed
// (boot still proceeds from prior good data) and matches the spec's
// "last-known-good retained" guarantee.
func NewCatalog(seed []byte, puller Puller, store Store, log logger) (*Catalog, error) {
	if log == nil {
		log = noopLogger{}
	}
	c := &Catalog{
		models:        map[string]Model{},
		defaultBudget: ResizeBudget{LongEdgePx: 7680, MaxBytes: 10 * 1024 * 1024},
		puller:        puller,
		store:         store,
		logger:        log,
	}

	// Step 1: try to hydrate from Store (last-known-good).
	hydrated := false
	if store != nil {
		prior, err := store.Read(context.Background())
		if err != nil {
			log.Warn("capabilities: last-known-good read failed; falling back to embedded seed", "error", err)
		} else if len(prior) > 0 {
			if err := c.applySeedJSON(prior); err != nil {
				log.Warn("capabilities: last-known-good parse failed; falling back to embedded seed", "error", err)
			} else {
				hydrated = true
			}
		}
	}

	// Step 2: apply embedded seed if Store didn't hydrate.
	if !hydrated {
		if err := c.applySeedJSON(seed); err != nil {
			return nil, fmt.Errorf("capabilities: embedded seed invalid: %w", err)
		}
	}

	return c, nil
}

// applySeedJSON parses data and atomically replaces the catalog state. The
// caller is responsible for calling applySeedJSON from a serialized context
// (Catalog construction or Catalog.Refresh — both single-goroutine paths).
func (c *Catalog) applySeedJSON(data []byte) error {
	s, err := ParseSeed(data)
	if err != nil {
		return err
	}
	c.applySeed(s)
	return nil
}

func (c *Catalog) applySeed(s Seed) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = make(map[string]Model, len(s.Models))
	for _, m := range s.Models {
		c.models[m.ID] = m
	}
	if s.DefaultResizeBudget.LongEdgePx > 0 && s.DefaultResizeBudget.MaxBytes > 0 {
		c.defaultBudget = s.DefaultResizeBudget
	}
	c.version = s.Version
	c.updatedAt = s.UpdatedAt
	c.source = s.Source
}

// Resolve returns the Model entry for modelID, or an optimistic default
// (image-capable) when the model is not in the catalog (FR-026). The optimistic
// default is a fresh Model value on each call; callers MUST NOT compare
// pointer-equality with catalog-returned values.
//
// For known models, the returned Model is a copy (safe to mutate); the
// catalog retains the original.
func (c *Catalog) Resolve(modelID string) Model {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if m, ok := c.models[modelID]; ok {
		return c.cloneWithDefaultBudget(m)
	}
	return OptimisticModel(modelID)
}

// OptimisticModel returns the optimistic default for an unknown model
// (FR-026): text + image modalities, the catalog default resize budget.
// Exposed so callers (the orchestrator in Wave 3) can construct the same
// value when they want to log or audit an unknown-model resolution without
// re-querying the catalog.
func OptimisticModel(modelID string) Model {
	return Model{
		ID:              modelID,
		Provider:        "",
		InputModalities: []string{"text", "image"},
		ResizeBudget:    nil,
		Notes:           "optimistic default for unknown model (FR-026)",
	}
}

// HasModal reports whether the catalog entry for modelID accepts the given
// modality. Unknown models return true for "image" and "text" (the optimistic
// default — FR-026); false for every other modality on unknown models.
//
// This is the convenience method the orchestrator calls during capability
// gating (step 1 of the Layer-1 chain).
func (c *Catalog) HasModal(modelID, modality string) bool {
	m := c.Resolve(modelID)
	for _, x := range m.InputModalities {
		if x == modality {
			return true
		}
	}
	return false
}

// Models returns a snapshot copy of the catalog model map. The returned map
// is safe to iterate and read; callers MUST NOT mutate the Model values
// (the ResizeBudget pointer is shared — mutating it mutates the catalog).
func (c *Catalog) Models() map[string]Model {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]Model, len(c.models))
	for k, v := range c.models {
		out[k] = v
	}
	return out
}

// DefaultResizeBudget returns the catalog-wide default resize budget. Callers
// use this for the "model-specific budget absent → use default" path (FR-014,
// package ambiguity #8 in the spec).
func (c *Catalog) DefaultResizeBudget() ResizeBudget {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.defaultBudget
}

// Version returns the catalog version string from the seed/refresh source.
// Empty if the catalog has never been hydrated.
func (c *Catalog) Version() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// UpdatedAt returns the timestamp from the most recently applied seed/refresh.
// Zero time before the first apply.
func (c *Catalog) UpdatedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.updatedAt
}

// Source returns the source string from the most recently applied seed/refresh.
// "embedded" when only the compiled-in seed has been applied; the freeze-gate
// artifact path when a refresh loaded a pulled catalog.
func (c *Catalog) Source() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.source
}

// Refresh fetches an updated catalog via the configured Puller. On success,
// the new catalog is applied atomically AND persisted to the Store (last-known-good
// survives restart). On any error, the in-memory state is untouched and the
// error is returned to the caller for logging (non-fatal — FR-025, SC-009).
//
// If no Puller is configured (NewCatalog with puller=nil), Refresh returns
// nil without doing anything.
func (c *Catalog) Refresh(ctx context.Context) error {
	if c.puller == nil {
		return nil
	}
	data, err := c.puller.Pull(ctx)
	if err != nil {
		c.logger.Warn("capabilities: pull failed; retaining last-known-good", "error", err)
		return err
	}
	// Parse first; only mutate state on success.
	s, err := ParseSeed(data)
	if err != nil {
		c.logger.Warn("capabilities: pulled catalog invalid; retaining last-known-good", "error", err)
		return err
	}
	// Validate version is non-decreasing. A regression to an older version is
	// a strong signal of a bad pull (e.g. raw fallback returning a stale
	// tag). We retain last-known-good.
	if c.Version() != "" && s.Version != "" && s.Version < c.Version() {
		c.logger.Warn("capabilities: pulled version regressed; retaining last-known-good",
			"pulled", s.Version, "current", c.Version())
		return fmt.Errorf("pulled catalog version %q regressed below current %q", s.Version, c.Version())
	}
	c.applySeed(s)
	if c.store != nil {
		if err := c.store.Write(ctx, data); err != nil {
			c.logger.Warn(
				"capabilities: last-known-good write failed (in-memory state updated, persistence lagged)",
				"error", err,
			)
			// Non-fatal: the in-memory state is updated; the next boot will
			// re-pull. Don't return the error here.
		}
	}
	return nil
}

// cloneWithDefaultBudget returns a copy of m with a non-nil ResizeBudget
// (the catalog default if m.ResizeBudget is nil). Caller MUST hold the
// read lock.
func (c *Catalog) cloneWithDefaultBudget(m Model) Model {
	out := m
	if out.ResizeBudget == nil {
		b := c.defaultBudget
		out.ResizeBudget = &b
	}
	return out
}

// noopLogger is the default logger when the caller passes nil — keeps
// NewCatalog's contract "log is optional" without scattering nil-checks
// throughout the package.
type noopLogger struct{}

func (noopLogger) Warn(string, ...any) {}
func (noopLogger) Info(string, ...any) {}
