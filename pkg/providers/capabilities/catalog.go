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
//   - Resolver: Resolve(modelID) returns the resolved model, or an optimistic
//     default (image-capable) for unknown models (FR-026). The optimistic
//     default bounds blast radius: a wrong guess costs one outcome-based
//     step-4 retry (pkg/agent/media_downgrade.go), never a dead turn.
//   - Override scope: GLOBAL SEED ONLY (FR-027). There are no per-agent or
//     per-workspace override paths; operators edit the seed file directly and
//     publish a new catalog release, which the next pull picks up.
//
// # Concurrency invariants (Wave 1 TD-M3 + TD-M4)
//
//   - model is private. Resolve returns a *resolvedModel handle that
//     exposes only accessor methods (Supports / Budget / ID / Provider).
//     The handle is a deep-owned copy — the slice, the budget pointer, and
//     the notes string are all independent of catalog state. External
//     mutation through the handle cannot corrupt catalog state.
//   - Refresh acquires a dedicated refreshMu (separate from the
//     catalog-state mu) and serializes the whole transaction
//     pull → parse → version-check → apply → store atomically. Two
//     concurrent Refresh calls cannot both pass the version check and
//     apply out of order.
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

// ResizeBudget is the canonical per-model resize budget for step-2
// normalize-and-resize (FR-014/FR-015). It is the single source of
// truth for the budget shape across the catalog and the resize
// pipeline (pkg/media/resize accepts this type directly — Wave 1
// TD-M6 unifies the formerly duplicated Budget types).
//
// The catalog default (LongEdgePx = 7680, MaxBytes = 10 MB) is always
// returned by Budget(): a resolved model never carries a nil/zero
// budget. MaxBytes is int64 to keep byte counts end-to-end without
// the int truncation hazard on 32-bit targets.
type ResizeBudget struct {
	// LongEdgePx is the max long-edge dimension after resize. Images longer
	// than this on the long edge are scaled down (preserving aspect ratio)
	// before send.
	LongEdgePx int `json:"long_edge_px"`
	// MaxBytes is the max file size in bytes after the PNG→JPEG quality
	// ladder (FR-015). Files still over budget route to step-5 offload.
	MaxBytes int64 `json:"max_bytes"`
}

// model is the private, invariant-bearing catalog entry. The exposed
// API surface is the *resolvedModel handle returned by Resolve and the
// accessor methods on it; the underlying catalog state lives here and
// is never returned to callers directly.
//
// resizeBudget is a value type (not a pointer) — the invariant
// "every post-validate model carries a non-zero budget" is enforced
// by Seed.validate, which mutates the DTO slice in place to apply
// the catalog default to any model that omitted one (Wave 1 TD-M6).
type model struct {
	id              string
	provider        string
	inputModalities []string
	resizeBudget    ResizeBudget
	notes           string
}

// resolvedModel is the read-only accessor handle returned by Resolve
// and from Models(). All slices and pointers are deep copies; the
// handle cannot mutate catalog state.
type resolvedModel struct {
	id              string
	provider        string
	inputModalities []string
	resizeBudget    ResizeBudget // value, not pointer — guaranteed non-zero
	notes           string
}

// Supports reports whether the resolved model accepts modality.
// Unknown modalities (not in the catalog's known set) are reported as
// supported only when the model's slice explicitly carries them — the
// forward-compat rule the seed validator applies (an unknown modality
// is recorded as-is) preserves its truth value here.
func (r *resolvedModel) Supports(modality string) bool {
	for _, m := range r.inputModalities {
		if m == modality {
			return true
		}
	}
	return false
}

// Budget returns the resolved model's resize budget. Always non-zero
// (the catalog default is applied when a seed entry omits one).
func (r *resolvedModel) Budget() ResizeBudget {
	return r.resizeBudget
}

// ID returns the canonical model identifier.
func (r *resolvedModel) ID() string {
	return r.id
}

// Provider returns the lowercased provider id from the seed.
func (r *resolvedModel) Provider() string {
	return r.provider
}

// Notes returns the diagnostic annotation (provider source, last
// validated date). Never sent on the wire.
func (r *resolvedModel) Notes() string {
	return r.notes
}

// InputModalities returns a copy of the model's modality slice.
func (r *resolvedModel) InputModalities() []string {
	out := make([]string, len(r.inputModalities))
	copy(out, r.inputModalities)
	return out
}

// resolve returns a deep-owned resolvedModel view of m. The input's
// ResizeBudget is always populated (Wave 1 TD-M6: Seed.validate
// guarantees every post-validate model DTO carries a non-nil budget,
// and the internal model stores ResizeBudget by value). Caller MUST
// hold the read lock.
func (c *Catalog) resolve(m model) *resolvedModel {
	modalities := append([]string(nil), m.inputModalities...)
	return &resolvedModel{
		id:              m.id,
		provider:        m.provider,
		inputModalities: modalities,
		resizeBudget:    m.resizeBudget,
		notes:           m.notes,
	}
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
type Store interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
}

// Seed is the parsed in-memory representation of the embedded
// data/providers_capabilities_seed.json. The Catalog holds a map of model
// keyed by id plus the catalog-wide default resize budget.
type Seed struct {
	Version             string       `json:"version"`
	SchemaVersion       string       `json:"schema_version"`
	UpdatedAt           time.Time    `json:"updated_at"`
	Source              string       `json:"source"`
	Models              []modelDTO   `json:"models"`
	DefaultResizeBudget ResizeBudget `json:"default_resize_budget"`
}

// modelDTO is the wire shape of a single catalog entry in the seed JSON.
// Validated by Seed.validate (Wave 1 TD-M5) before being projected onto
// the internal model type.
type modelDTO struct {
	ID              string        `json:"id"`
	Provider        string        `json:"provider"`
	InputModalities []string      `json:"input_modalities"`
	ResizeBudget    *ResizeBudget `json:"resize_budget,omitempty"`
	Notes           string        `json:"notes,omitempty"`
}

// toModel projects the validated DTO onto the private model type.
// All invariants must already have been checked by Seed.validate —
// in particular, d.ResizeBudget is guaranteed non-nil after validate
// (Wave 1 TD-M6: the catalog default is applied to any DTO that
// omitted one). The internal type uses ResizeBudget by value so the
// invariant "every post-validate model carries a non-zero budget"
// is unrepresentable in the in-memory model.
func (d modelDTO) toModel() model {
	out := model{
		id:       d.ID,
		provider: d.Provider,
		// ResizeBudget invariant: post-validate DTO always has a non-nil
		// ResizeBudget (validate applies the catalog default when missing).
		resizeBudget: *d.ResizeBudget,
		notes:        d.Notes,
	}
	if d.InputModalities != nil {
		out.inputModalities = append([]string(nil), d.InputModalities...)
	}
	return out
}

// Catalog is the live in-memory model capability registry.
//
// Concurrent use: Resolve / Models / HasModal take the stateMu read-lock;
// Refresh acquires a dedicated refreshMu (separate from stateMu) and
// holds stateMu only across the compare-and-apply window. Pull failure
// (any error from Puller.Pull) is non-fatal — the in-memory map retains
// its prior values and the error is returned to the caller for logging
// (FR-025, SC-009).
type Catalog struct {
	// stateMu guards models, defaultBudget, version, updatedAt, source.
	stateMu       sync.RWMutex
	models        map[string]model
	defaultBudget ResizeBudget
	version       string
	updatedAt     time.Time
	source        string

	// refreshMu serializes the WHOLE Refresh transaction (pull → parse
	// → version-check → apply → store). Two concurrent Refresh calls
	// cannot both pass the version check and apply out of order (the
	// Wave 1 TD-M4-major invariant).
	refreshMu sync.Mutex

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

// validate enforces the seed schema invariants (Wave 1 TD-M5) and
// finalizes the per-model ResizeBudget so every post-validate DTO is
// guaranteed non-nil (Wave 1 TD-M6).
//
//   - DefaultResizeBudget has positive LongEdgePx and positive MaxBytes.
//   - Each model.ID is non-empty and unique.
//   - Each model.Provider is non-empty.
//   - Each model.InputModalities is non-empty, contains "text" (the
//     text-invariant — every model supports text), and has no empty or
//     duplicate values.
//   - ResizeBudget, when present, has positive LongEdgePx and positive MaxBytes.
//   - After validation: every model.ResizeBudget is non-nil (the catalog
//     default is applied in place to any model that omitted one).
func (s *Seed) validate() error {
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
		if m.Provider == "" {
			return fmt.Errorf("models[%d].id %q: provider must be non-empty", i, m.ID)
		}
		if len(m.InputModalities) == 0 {
			return fmt.Errorf("models[%d].id %q has empty input_modalities", i, m.ID)
		}
		hasText := false
		modalSeen := make(map[string]bool, len(m.InputModalities))
		for j, modality := range m.InputModalities {
			if modality == "" {
				return fmt.Errorf("models[%d].id %q: input_modalities[%d] is empty", i, m.ID, j)
			}
			if modalSeen[modality] {
				return fmt.Errorf("models[%d].id %q: input_modalities has duplicate %q", i, m.ID, modality)
			}
			modalSeen[modality] = true
			if modality == "text" {
				hasText = true
			}
		}
		if !hasText {
			return fmt.Errorf("models[%d].id %q: input_modalities must include %q (every model supports text)", i, m.ID, "text")
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
	// Wave 1 TD-M6: apply the catalog default in place to any model that
	// omitted a ResizeBudget. After this loop, every model DTO carries a
	// non-nil budget so toModel can dereference without a nil check.
	for i := range s.Models {
		if s.Models[i].ResizeBudget == nil {
			budget := s.DefaultResizeBudget
			s.Models[i].ResizeBudget = &budget
		}
	}
	return nil
}

// NewCatalog constructs a Catalog from the embedded seed plus the puller and
// store. If puller is nil, Refresh becomes a no-op (the seed is the only
// data source — useful for tests and CLI tools that should never call out).
// If store is nil, last-known-good persistence is disabled (the in-memory
// map is the only state; reboots start from the embedded seed).
func NewCatalog(seed []byte, puller Puller, store Store, log logger) (*Catalog, error) {
	if log == nil {
		log = noopLogger{}
	}
	c := &Catalog{
		models:        map[string]model{},
		defaultBudget: ResizeBudget{LongEdgePx: 7680, MaxBytes: 10 * 1024 * 1024},
		puller:        puller,
		store:         store,
		logger:        log,
	}

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

	if !hydrated {
		if err := c.applySeedJSON(seed); err != nil {
			return nil, fmt.Errorf("capabilities: embedded seed invalid: %w", err)
		}
	}

	return c, nil
}

// applySeedJSON parses data and atomically replaces the catalog state.
func (c *Catalog) applySeedJSON(data []byte) error {
	s, err := ParseSeed(data)
	if err != nil {
		return err
	}
	c.applySeed(s)
	return nil
}

func (c *Catalog) applySeed(s Seed) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.models = make(map[string]model, len(s.Models))
	for _, m := range s.Models {
		c.models[m.ID] = m.toModel()
	}
	if s.DefaultResizeBudget.LongEdgePx > 0 && s.DefaultResizeBudget.MaxBytes > 0 {
		c.defaultBudget = s.DefaultResizeBudget
	}
	c.version = s.Version
	c.updatedAt = s.UpdatedAt
	c.source = s.Source
}

// Resolve returns a read-only resolved model handle for modelID, or the
// optimistic default (image-capable) when the model is not in the
// catalog (FR-026). The handle is a deep-owned copy of the catalog
// state; callers may mutate it freely without affecting catalog state.
func (c *Catalog) Resolve(modelID string) *resolvedModel {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if m, ok := c.models[modelID]; ok {
		return c.resolve(m)
	}
	return c.optimistic(modelID)
}

// optimistic returns the FR-026 optimistic default for an unknown model.
// Caller MUST hold the read lock (so the defaultBudget is consistent).
func (c *Catalog) optimistic(modelID string) *resolvedModel {
	return &resolvedModel{
		id:              modelID,
		provider:        "",
		inputModalities: []string{"text", "image"},
		resizeBudget:    c.defaultBudget,
		notes:           "optimistic default for unknown model (FR-026)",
	}
}

// OptimisticModel returns the optimistic default for an unknown model.
func (c *Catalog) OptimisticModel(modelID string) *resolvedModel {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.optimistic(modelID)
}

// HasModal reports whether the catalog entry for modelID accepts the given
// modality (FR-026 optimistic default for unknown models).
func (c *Catalog) HasModal(modelID, modality string) bool {
	return c.Resolve(modelID).Supports(modality)
}

// ModelSnapshot is a single (id, accessor handle) pair returned by
// Models(). The handle is a deep-owned copy.
type ModelSnapshot struct {
	ID     string
	Handle *resolvedModel
}

// Models returns a snapshot of the catalog: a slice of (id, handle)
// pairs sorted by id. The handles are deep-owned copies.
func (c *Catalog) Models() []ModelSnapshot {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	out := make([]ModelSnapshot, 0, len(c.models))
	for id, m := range c.models {
		out = append(out, ModelSnapshot{ID: id, Handle: c.resolve(m)})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// DefaultResizeBudget returns the catalog-wide default resize budget.
func (c *Catalog) DefaultResizeBudget() ResizeBudget {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.defaultBudget
}

// Version returns the catalog version string from the seed/refresh source.
func (c *Catalog) Version() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.version
}

// UpdatedAt returns the timestamp from the most recently applied seed/refresh.
func (c *Catalog) UpdatedAt() time.Time {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.updatedAt
}

// Source returns the source string from the most recently applied seed/refresh.
func (c *Catalog) Source() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.source
}

// Refresh fetches an updated catalog via the configured Puller. The whole
// transaction — pull → parse → version-check → apply → store — runs
// under a dedicated refreshMu, so two concurrent Refresh calls cannot
// interleave (Wave 1 TD-M4-major invariant). On success, the new
// catalog is applied atomically AND persisted to the Store (last-known-good
// survives restart). On any error, the in-memory state is untouched
// and the error is returned to the caller (non-fatal — FR-025, SC-009).
//
// If no Puller is configured (NewCatalog with puller=nil), Refresh returns
// nil without doing anything.
func (c *Catalog) Refresh(ctx context.Context) error {
	if c.puller == nil {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	return c.refreshLocked(ctx)
}

// refreshLocked runs the Refresh transaction. Caller MUST hold c.refreshMu.
func (c *Catalog) refreshLocked(ctx context.Context) error {
	// 1. Pull.
	data, err := c.puller.Pull(ctx)
	if err != nil {
		c.logger.Warn("capabilities: pull failed; retaining last-known-good", "error", err)
		return err
	}
	// 2. Parse.
	s, err := ParseSeed(data)
	if err != nil {
		c.logger.Warn("capabilities: pulled catalog invalid; retaining last-known-good", "error", err)
		return err
	}
	// 3. Version check (under stateMu; the apply is the only state-mutating step).
	c.stateMu.RLock()
	currentVersion := c.version
	c.stateMu.RUnlock()
	if currentVersion != "" && s.Version != "" && s.Version < currentVersion {
		c.logger.Warn("capabilities: pulled version regressed; retaining last-known-good",
			"pulled", s.Version, "current", currentVersion)
		return fmt.Errorf("pulled catalog version %q regressed below current %q", s.Version, currentVersion)
	}
	// 4. Apply.
	c.applySeed(s)
	// 5. Store.
	if c.store != nil {
		if err := c.store.Write(ctx, data); err != nil {
			c.logger.Warn(
				"capabilities: last-known-good write failed (in-memory state updated, persistence lagged)",
				"error", err,
			)
		}
	}
	return nil
}

// noopLogger is the default logger when the caller passes nil.
type noopLogger struct{}

func (noopLogger) Warn(string, ...any) {}
func (noopLogger) Info(string, ...any) {}
