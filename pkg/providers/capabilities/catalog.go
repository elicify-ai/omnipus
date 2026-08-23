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
//     last-known-good is retained (FR-025, SC-009). A successful pull that
//     fell back to the unverified raw transport is recorded on the catalog
//     (Catalog.Degraded) and persisted through the Store (see the Store bullet
//     below) so the degraded provenance survives a restart and is directly
//     inspectable, not just an in-memory value nothing reads.
//   - Resolver: Resolve(modelID) returns the resolved model, or an optimistic
//     default (image-capable) for unknown models (FR-026). The optimistic
//     default bounds blast radius: a wrong guess costs one outcome-based
//     step-4 retry (pkg/agent/media_downgrade.go), never a dead turn.
//   - Override scope: GLOBAL SEED ONLY (FR-027). There are no per-agent or
//     per-workspace override paths; operators edit the seed file directly and
//     publish a new catalog release, which the next pull picks up.
//
// # Two-stage parse (Wave 1 TD-M5)
//
//   - seedFile is the permissive JSON DTO. It maps the wire shape 1:1
//     and allows zero values everywhere; the operator can edit the
//     seed without learning the validation rules.
//   - Seed is the validated, invariant-bearing domain. The validate
//     transform (seedFile.validate) projects the DTO into the domain
//     and enforces: catalog metadata non-empty; default + per-model
//     ResizeBudget positive; each model.ID non-empty and unique; each
//     model.Provider non-empty; each model.InputModalities non-empty,
//     non-duplicate, and including ModalityText; the catalog default
//     applied to any model that omitted a ResizeBudget.
//
// # Concurrency invariants (Wave 1 TD-M3 + TD-M4)
//
//   - model is private. Resolve returns a *resolvedModel handle that
//     exposes only accessor methods (Supports / Budget / ID / Provider).
//     The handle is a deep-owned copy — the slice, the budget, and
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
	"errors"
	"fmt"
	"io/fs"
	"strings"
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

// model is the private, invariant-bearing in-memory catalog entry.
// The exposed API surface is the *resolvedModel handle returned by
// Resolve and the accessor methods on it; the underlying catalog state
// lives here and is never returned to callers directly.
//
// resizeBudget is a value type (not a pointer) — the invariant
// "every post-validate model carries a non-zero budget" is enforced
// by seedFile.validate, which finalizes the validated Model before
// projection.
type model struct {
	id              string
	provider        string
	inputModalities []Modality
	resizeBudget    ResizeBudget
	notes           string
}

// resolvedModel is the read-only accessor handle returned by Resolve
// and from Models(). All slices and the budget are deep copies; the
// handle cannot mutate catalog state.
type resolvedModel struct {
	id              string
	provider        string
	inputModalities []Modality
	resizeBudget    ResizeBudget // value, not pointer — guaranteed non-zero
	notes           string
}

// Supports reports whether the resolved model accepts modality.
// Unknown modalities (not in the catalog's known set) are reported as
// supported only when the model's slice explicitly carries them — the
// forward-compat rule the seed validator applies (an unknown modality
// is recorded as-is) preserves its truth value here.
func (r *resolvedModel) Supports(modality Modality) bool {
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
func (r *resolvedModel) InputModalities() []Modality {
	out := make([]Modality, len(r.inputModalities))
	copy(out, r.inputModalities)
	return out
}

// resolve returns a deep-owned resolvedModel view of m. The input's
// ResizeBudget is always populated (Wave 1 TD-M6: seedFile.validate
// guarantees every post-validate Model carries a non-zero budget, and
// the internal model stores ResizeBudget by value). Caller MUST hold
// the read lock.
func (c *Catalog) resolve(m model) *resolvedModel {
	modalities := append([]Modality(nil), m.inputModalities...)
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

// degradedTransportReporter is an optional interface a Puller may implement
// to report whether its most recently completed successful Pull fell back to
// an unverified transport (GHReleasePuller implements this for its
// raw.githubusercontent.com fallback path). refreshLocked type-asserts for
// it after every successful Pull; a Puller that doesn't implement it is
// treated as never-degraded (the pre-fix behavior), so this is purely
// additive and never required.
type degradedTransportReporter interface {
	LastPullDegraded() (degraded bool, releaseErr error)
}

// Store persists the last-known-good catalog state across restarts. Read is
// called on Catalog construction (before the first pull) so the catalog boots
// with prior data even when the network is unreachable. Write is called after
// a successful Refresh so the next boot has the latest data.
//
// The bytes exchanged with Store are an opaque envelope owned entirely by
// this package (see persistedCatalogState, encodePersistedState,
// decodePersistedState) — a Store implementation MUST treat them as opaque
// and round-trip them unmodified (capFileStore in pkg/gateway/gateway.go
// does exactly this: os.ReadFile / fileutil.WriteFileAtomic with no
// parsing). Since FIX 1 (review), the envelope carries the degraded-
// transport flag alongside the raw catalog JSON so an operator can inspect
// it directly from the persisted file — see Degraded's doc comment.
type Store interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
}

// persistedCatalogState is the on-disk envelope written by encodePersistedState
// and read back by decodePersistedState. It wraps the raw catalog JSON
// (unchanged shape — Catalog is independently parseable via ParseSeed) with
// the FIX-1 degraded-transport flag, so the flag survives a restart and is
// directly inspectable in the persisted file instead of existing only as an
// in-memory field nothing reads (the review finding this closes: Degraded()
// was write-only, called only from a test).
//
// Back-compat: a Store file written before this change is bare catalog JSON
// with no "catalog"/"degraded" keys at the top level. decodePersistedState
// detects that shape (Catalog is empty after unmarshal — the catalog schema
// has no field named "catalog") and falls back to treating the whole payload
// as the raw catalog, with degraded defaulting to false: a legacy file
// predates degraded-transport tracking entirely, so "not recorded" is
// represented honestly as "not degraded" rather than guessed.
type persistedCatalogState struct {
	Degraded             bool            `json:"degraded"`
	DegradedReleaseError string          `json:"degraded_release_error,omitempty"`
	Catalog              json.RawMessage `json:"catalog"`
}

// encodePersistedState wraps catalogJSON with the degraded-transport
// metadata for Store.Write. releaseErr is flattened to its message string
// (the envelope is JSON; error identity is not needed on the read side,
// only the diagnostic text).
func encodePersistedState(catalogJSON []byte, degraded bool, releaseErr error) ([]byte, error) {
	state := persistedCatalogState{
		Degraded: degraded,
		Catalog:  catalogJSON,
	}
	if releaseErr != nil {
		state.DegradedReleaseError = releaseErr.Error()
	}
	out, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("capabilities: encode persisted state: %w", err)
	}
	return out, nil
}

// decodePersistedState unwraps data written by encodePersistedState. When
// data does not carry the envelope's "catalog" key (the legacy pre-FIX-1
// shape, or any payload decodePersistedState cannot confidently unwrap), it
// falls back to treating data as the bare catalog JSON directly, reporting
// not-degraded — see persistedCatalogState's doc comment for the rationale.
func decodePersistedState(data []byte) (catalogJSON []byte, degraded bool, releaseErr error) {
	var state persistedCatalogState
	if err := json.Unmarshal(data, &state); err == nil && len(state.Catalog) > 0 {
		if state.DegradedReleaseError != "" {
			releaseErr = errors.New(state.DegradedReleaseError)
		}
		return state.Catalog, state.Degraded, releaseErr
	}
	return data, false, nil
}

// Seed is the validated, invariant-bearing domain catalog. Constructed
// by seedFile.validate from the permissive JSON DTO; never built
// directly from raw JSON by external callers.
type Seed struct {
	// Version is the legacy seed's date-string version ("2026-07-28"),
	// compared lexically. The semver-aware Version type moved to
	// pkg/providers/catalog with the 2.0.0 document (T067-03); this
	// package is deleted whole by T067-07.
	Version             string
	SchemaVersion       string
	UpdatedAt           time.Time
	Source              string
	Models              []Model
	DefaultResizeBudget ResizeBudget
}

// Model is the validated, invariant-bearing domain catalog entry.
// Every field is guaranteed non-zero/non-empty after validate except
// ResizeBudget which is guaranteed non-zero (the catalog default is
// applied to any model that omitted one).
type Model struct {
	ID              string
	Provider        string
	InputModalities []Modality
	ResizeBudget    ResizeBudget
	Notes           string
}

// seedFile is the permissive JSON DTO of the catalog. It maps the wire
// shape 1:1 and allows zero values everywhere; the validate transform
// produces the validated Seed. Kept unexported because external
// callers should never see the unvalidated shape.
type seedFile struct {
	Version             string        `json:"version"`
	SchemaVersion       string        `json:"schema_version"`
	UpdatedAt           time.Time     `json:"updated_at"`
	Source              string        `json:"source"`
	Models              []modelDTO    `json:"models"`
	DefaultResizeBudget *ResizeBudget `json:"default_resize_budget"`
}

// modelDTO is the wire shape of a single catalog entry in the seed JSON.
// Validated by seedFile.validate (Wave 1 TD-M5) before being projected
// onto the validated Model type.
type modelDTO struct {
	ID              string        `json:"id"`
	Provider        string        `json:"provider"`
	InputModalities []string      `json:"input_modalities"`
	ResizeBudget    *ResizeBudget `json:"resize_budget,omitempty"`
	Notes           string        `json:"notes,omitempty"`
}

// validate parses the seedFile's wire data into the validated Seed.
// All catalog invariants are enforced here (Wave 1 TD-M5):
//
//   - Catalog metadata: schema_version, updated_at, source and version
//     are non-empty. The version string is kept verbatim and compared
//     lexically by Catalog.Refresh's regression check (correct for the
//     ISO-date strings this legacy seed uses).
//   - DefaultResizeBudget has positive LongEdgePx and positive MaxBytes.
//   - Each model.ID is non-empty and unique.
//   - Each model.Provider is non-empty.
//   - Each model.InputModalities is non-empty, contains no empty or
//     duplicate values, and includes ModalityText.
//   - Each model.ResizeBudget, when present, has positive LongEdgePx
//     and positive MaxBytes. Models without one inherit the catalog
//     default (Wave 1 TD-M6 invariant: every post-validate Model
//     carries a non-zero budget).
//
// The two-stage parse (DTO then validate) is the explicit split
// required by Wave 1 TD-M5: the wire shape is permissive, the domain
// is invariant-bearing.
func (f seedFile) validate() (Seed, error) {
	if f.SchemaVersion == "" {
		return Seed{}, fmt.Errorf("schema_version must be non-empty")
	}
	if f.UpdatedAt.IsZero() {
		return Seed{}, fmt.Errorf("updated_at must be non-zero")
	}
	if f.Source == "" {
		return Seed{}, fmt.Errorf("source must be non-empty")
	}
	// FIX 5 (review): an empty/missing version must be rejected outright.
	// refreshLocked's anti-downgrade regression check is deliberately
	// gated on `s.Version != ""` (a version-less pulled/stored catalog
	// can't be compared, so the check is skipped for it rather than
	// false-rejecting). Without this validation, that skip becomes a hole:
	// a version-less catalog is applied UNCONDITIONALLY — even when it is
	// semantically older than what's running — with zero log. Rejecting it
	// here, before it can ever reach refreshLocked's apply step, is what
	// makes that guard's "" -> "skip" behavior actually safe.
	if f.Version == "" {
		return Seed{}, fmt.Errorf("version must be non-empty")
	}

	if f.DefaultResizeBudget == nil {
		return Seed{}, fmt.Errorf("default_resize_budget is required")
	}
	if f.DefaultResizeBudget.LongEdgePx <= 0 {
		return Seed{}, fmt.Errorf(
			"default_resize_budget.long_edge_px must be > 0, got %d",
			f.DefaultResizeBudget.LongEdgePx,
		)
	}
	if f.DefaultResizeBudget.MaxBytes <= 0 {
		return Seed{}, fmt.Errorf(
			"default_resize_budget.max_bytes must be > 0, got %d",
			f.DefaultResizeBudget.MaxBytes,
		)
	}

	out := Seed{
		Version:             f.Version,
		SchemaVersion:       f.SchemaVersion,
		UpdatedAt:           f.UpdatedAt,
		Source:              f.Source,
		DefaultResizeBudget: *f.DefaultResizeBudget,
		Models:              make([]Model, 0, len(f.Models)),
	}

	seen := make(map[string]bool, len(f.Models))
	for i, m := range f.Models {
		if m.ID == "" {
			return Seed{}, fmt.Errorf("models[%d].id must be non-empty", i)
		}
		if seen[m.ID] {
			return Seed{}, fmt.Errorf("models[%d].id %q is duplicated", i, m.ID)
		}
		seen[m.ID] = true
		if m.Provider == "" {
			return Seed{}, fmt.Errorf("models[%d].id %q: provider must be non-empty", i, m.ID)
		}
		if len(m.InputModalities) == 0 {
			return Seed{}, fmt.Errorf("models[%d].id %q has empty input_modalities", i, m.ID)
		}
		hasText := false
		modalSeen := make(map[string]bool, len(m.InputModalities))
		modalities := make([]Modality, 0, len(m.InputModalities))
		for j, ms := range m.InputModalities {
			if ms == "" {
				return Seed{}, fmt.Errorf("models[%d].id %q: input_modalities[%d] is empty", i, m.ID, j)
			}
			if modalSeen[ms] {
				return Seed{}, fmt.Errorf("models[%d].id %q: input_modalities has duplicate %q", i, m.ID, ms)
			}
			modalSeen[ms] = true
			modalities = append(modalities, Modality(ms))
			if Modality(ms) == ModalityText {
				hasText = true
			}
		}
		if !hasText {
			return Seed{}, fmt.Errorf(
				"models[%d].id %q: input_modalities must include %q (every model supports text)",
				i, m.ID, ModalityText,
			)
		}
		// Wave 1 TD-M6: apply the catalog default in place to any model
		// that omitted a ResizeBudget. After this branch, every post-validate
		// Model carries a non-zero budget by value.
		budget := out.DefaultResizeBudget
		if m.ResizeBudget != nil {
			if m.ResizeBudget.LongEdgePx <= 0 {
				return Seed{}, fmt.Errorf(
					"models[%d].id %q: resize_budget.long_edge_px must be > 0, got %d",
					i, m.ID, m.ResizeBudget.LongEdgePx,
				)
			}
			if m.ResizeBudget.MaxBytes <= 0 {
				return Seed{}, fmt.Errorf(
					"models[%d].id %q: resize_budget.max_bytes must be > 0, got %d",
					i, m.ID, m.ResizeBudget.MaxBytes,
				)
			}
			budget = *m.ResizeBudget
		}
		out.Models = append(out.Models, Model{
			ID:              m.ID,
			Provider:        m.Provider,
			InputModalities: modalities,
			ResizeBudget:    budget,
			Notes:           m.Notes,
		})
	}
	return out, nil
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
	// stateMu guards models, defaultBudget, version, updatedAt, source,
	// degraded, degradedReleaseErr.
	stateMu       sync.RWMutex
	models        map[string]model
	defaultBudget ResizeBudget
	version       string
	updatedAt     time.Time
	source        string
	// degraded and degradedReleaseErr record whether the most recently
	// applied Refresh's catalog came from a Puller reporting a fallback to
	// an unverified transport (e.g. GHReleasePuller's raw.githubusercontent.com
	// path after the GitHub Release path failed) — see refreshLocked and
	// Degraded(). Zero value (false, nil) means either the seed/last-known-
	// good hydration path (no live pull has run yet) or the most recent
	// live pull used the fully-verified transport.
	degraded           bool
	degradedReleaseErr error

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
//
// Two-stage parse: the wire bytes are first unmarshaled into the
// permissive seedFile DTO, then validated by seedFile.validate which
// produces the invariant-bearing Seed. A validation failure returns
// a descriptive error and the zero Seed.
func ParseSeed(data []byte) (Seed, error) {
	var f seedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return Seed{}, fmt.Errorf("capabilities: parse seed: %w", err)
	}
	s, err := f.validate()
	if err != nil {
		return Seed{}, fmt.Errorf("capabilities: validate seed: %w", err)
	}
	return s, nil
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
			// A fresh install has no persisted catalog yet — fs.ErrNotExist
			// (however the Store implementation surfaces it, e.g. an
			// unwrapped *fs.PathError from os.ReadFile) is the expected,
			// routine first-boot case, not a fault. Warning on it every
			// single fresh install is a false alarm that trains operators to
			// ignore this log line, masking the case that actually matters
			// (a real read failure — permissions, corruption, disk error).
			if !errors.Is(err, fs.ErrNotExist) {
				log.Warn("capabilities: last-known-good read failed; falling back to embedded seed", "error", err)
			}
		} else if len(prior) > 0 {
			catalogJSON, degraded, releaseErr := decodePersistedState(prior)
			if err := c.applySeedJSON(catalogJSON); err != nil {
				log.Warn("capabilities: last-known-good parse failed; falling back to embedded seed", "error", err)
			} else {
				hydrated = true
				// FIX 1 (review): restore the degraded-transport flag from
				// the persisted envelope so Degraded() reflects reality
				// immediately after a restart, not just after the next live
				// Refresh — an install that has been running on the
				// unverified raw.githubusercontent.com fallback for days
				// must not report "not degraded" for the whole boot window
				// before the 7-day ticker fires again.
				c.stateMu.Lock()
				c.degraded = degraded
				c.degradedReleaseErr = releaseErr
				c.stateMu.Unlock()
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
		c.models[m.ID] = model{
			id:              m.ID,
			provider:        m.Provider,
			inputModalities: append([]Modality(nil), m.InputModalities...),
			resizeBudget:    m.ResizeBudget,
			notes:           m.Notes,
		}
	}
	c.defaultBudget = s.DefaultResizeBudget
	c.version = s.Version
	c.updatedAt = s.UpdatedAt
	c.source = s.Source
}

// Resolve returns a read-only resolved model handle for modelID, or the
// optimistic default (image-capable) when the model is not in the
// catalog (FR-026). The handle is a deep-owned copy of the catalog
// state; callers may mutate it freely without affecting catalog state.
//
// Every catalog entry is keyed by its BARE model id (e.g. "glm-5.2"); the
// vendor is recorded separately in Model.Provider (e.g. "z-ai"). Callers,
// however, often pass a provider-prefixed id — the OpenRouter-style
// "<vendor>/<model>" routing form ("z-ai/glm-5.2"), sometimes with an extra
// namespace segment onboarding writes ("openrouter/z-ai/glm-5.2", see
// openai_compat.normalizeModel). An exact-only lookup on a prefixed id
// always misses, silently falling through to the FR-026 optimistic
// default (assume image-capable) even when the catalog carries an
// authoritative — and possibly negative — entry for that exact model.
//
// Confirmed live 2026-07-28 (UAT against z-ai/glm-5.2): the seed already
// lists "glm-5.2" (provider "z-ai") as text-only, but the prefixed lookup
// missed it, the optimistic default assumed image support, and the
// backend sent a native image block that OpenRouter's real endpoint for
// this model rejected with 404 "No endpoints found that support image
// input" — an avoidable failed round trip the outcome-based retry then
// had to paper over (pkg/agent/media_downgrade.go).
//
// The fallback below strips leading "<segment>/" prefixes one at a time
// and retries the exact lookup against each shorter suffix. This can
// never produce a WRONG match: every model.ID is required unique across
// the whole catalog (seedFile.validate), so a stripped suffix that hits
// is, by construction, the intended model. It also cannot regress a
// prefix that is itself a genuine bare catalog id with no vendor
// namespace (e.g. "gpt-4o") — those already match on the first, exact
// lookup and never reach the fallback.
func (c *Catalog) Resolve(modelID string) *resolvedModel {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if m, ok := c.models[modelID]; ok {
		return c.resolve(m)
	}
	if m, ok := c.resolveStrippedPrefix(modelID); ok {
		return c.resolve(m)
	}
	return c.optimistic(modelID)
}

// resolveStrippedPrefix retries the exact catalog lookup after stripping
// leading "<segment>/" prefixes one at a time (shortest-suffix-first is
// NOT required for correctness — see Resolve's doc comment — so this walks
// from the longest remaining suffix down to the bare trailing segment,
// stopping at the first exact hit). Caller MUST hold the read lock.
func (c *Catalog) resolveStrippedPrefix(modelID string) (model, bool) {
	rest := modelID
	for {
		idx := strings.IndexByte(rest, '/')
		if idx < 0 || idx == len(rest)-1 {
			return model{}, false
		}
		rest = rest[idx+1:]
		if m, ok := c.models[rest]; ok {
			return m, true
		}
	}
}

// optimistic returns the FR-026 optimistic default for an unknown model.
// Caller MUST hold the read lock (so the defaultBudget is consistent).
func (c *Catalog) optimistic(modelID string) *resolvedModel {
	return &resolvedModel{
		id:              modelID,
		provider:        "",
		inputModalities: []Modality{ModalityText, ModalityImage},
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
// modality (FR-026 optimistic default for unknown models). The modality
// parameter is a string for caller convenience (the SPA passes the
// literal from the wire); it is parsed into a Modality for the lookup.
func (c *Catalog) HasModal(modelID, modality string) bool {
	return c.Resolve(modelID).Supports(Modality(modality))
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

// Version returns the raw catalog version string from the seed/refresh
// source.
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

// Degraded reports whether the catalog currently applied came from a Puller
// that fell back to an unverified transport (e.g. GHReleasePuller's
// raw.githubusercontent.com path after its GitHub Release path failed —
// FR-025's "semver-tagged, checksum-verified release" guarantee does not
// apply to that transport). releaseErr is the release-path failure that
// triggered the fallback, or nil when degraded is false.
//
// This is the state-level half of the FIX-1 review fix: refreshLocked also
// logs a WARN on the transition, but a log line can be missed or rotated
// away — this accessor gives an operator (or a future status/health
// endpoint) a durable way to see which transport actually supplied the
// running catalog.
//
// Observability (review re-fix, closing the "write-only field" finding):
// the value is not merely in-memory — encodePersistedState/decodePersistedState
// round-trip it through the Store's persisted file (e.g.
// $OMNIPUS_HOME/capabilities_catalog.json via the gateway's capFileStore),
// so (a) it survives a restart — NewCatalog restores it from a prior
// Refresh's persisted state before any live pull runs on the new process,
// and (b) it is directly inspectable by an operator reading that file
// (`"degraded":true` at the top level) without needing any code path to
// call this accessor at all. A catalog with no Store, or whose Store has
// never been written to (fresh install, no Refresh has run yet), reports
// (false, nil) — the honest "unknown state defaults to not-degraded"
// reading documented on persistedCatalogState.
func (c *Catalog) Degraded() (degraded bool, releaseErr error) {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.degraded, c.degradedReleaseErr
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
//
// Validate runs inside the serialized transaction (on both initial load
// and on every refresh) — a malformed seed is rejected and the
// last-known-good is retained (Wave 1 TD-M5).
func (c *Catalog) refreshLocked(ctx context.Context) error {
	// 1. Pull.
	data, err := c.puller.Pull(ctx)
	if err != nil {
		c.logger.Warn("capabilities: pull failed; retaining last-known-good", "error", err)
		return err
	}
	// 1a. Degraded-transport check (review FIX 1): a successful Pull can
	// still mean the Puller silently fell back to an unverified transport
	// (GHReleasePuller: GitHub Release fetch/checksum failed, raw
	// fallback succeeded). That degradation used to be indistinguishable
	// from a clean success — fetchErr was discarded once the fallback
	// succeeded, and refreshLocked only ever logged on a non-nil Pull
	// error. degradedTransportReporter is an optional interface so this
	// works for GHReleasePuller without widening the Puller contract
	// itself (other implementations, e.g. tests, are unaffected).
	degraded := false
	var releaseErr error
	if reporter, ok := c.puller.(degradedTransportReporter); ok {
		degraded, releaseErr = reporter.LastPullDegraded()
		if degraded {
			c.logger.Warn(
				"capabilities: catalog pulled via degraded fallback transport; "+
					"the GitHub Release path failed and could not be checksum-verified "+
					"against a signed release asset, so this data is unpinned and unverified",
				"release_error", releaseErr,
			)
		}
	}
	// 2. Parse + validate (the two-stage parse; invalid JSON or invalid
	// invariants are both rejected here, last-known-good retained).
	s, err := ParseSeed(data)
	if err != nil {
		c.logger.Warn("capabilities: pulled catalog invalid; retaining last-known-good", "error", err)
		return err
	}
	// 3. Version check (under stateMu; the apply is the only state-mutating step).
	//    Lexical comparison preserves chronological ordering for the
	//    ISO-date version strings this legacy seed uses ("2026-07-28").
	c.stateMu.RLock()
	currentVersion := c.version
	c.stateMu.RUnlock()
	if currentVersion != "" && s.Version != "" && strings.Compare(s.Version, currentVersion) < 0 {
		c.logger.Warn("capabilities: pulled version regressed; retaining last-known-good",
			"pulled", s.Version, "current", currentVersion)
		return fmt.Errorf("pulled catalog version %q regressed below current %q", s.Version, currentVersion)
	}
	// 4. Apply.
	c.applySeed(s)
	c.stateMu.Lock()
	c.degraded = degraded
	c.degradedReleaseErr = releaseErr
	c.stateMu.Unlock()
	// 5. Store.
	if c.store != nil {
		persisted, encErr := encodePersistedState(data, degraded, releaseErr)
		if encErr != nil {
			// Encoding the envelope only fails on a json.Marshal error,
			// which cannot happen for this struct shape (a []byte, a bool,
			// and two strings) — guarded defensively so a future field
			// addition can't turn into a silent skip.
			c.logger.Warn(
				"capabilities: failed to encode persisted state; last-known-good write skipped",
				"error", encErr,
			)
		} else if err := c.store.Write(ctx, persisted); err != nil {
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
