// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package agentstore is the agent-specific wrapper over the generic
// per-entity store (pkg/entity), rooted at $OMNIPUS_HOME/entities/agents/
// (ADR-054 D2). It is Wave 2 of ADR-054
// (docs/internal/architecture/ADR-054-entity-config-separation.md): the
// generic store primitive (pkg/entity) is Wave 1's; this package supplies the
// config.AgentConfig-specific Accessors wiring, and the read-side cache
// invalidation contract that keeps pkg/agent.AgentRegistry consistent with
// what's on disk without ever making a per-message routing read touch disk
// (ADR-054 D3 "READ PATH" / §5).
//
// Location: $OMNIPUS_HOME/entities/agents/<id>.json — deliberately NOT
// agents/<id>/ (an agent's own writable workspace/home directory). D2
// requires this: a record co-located inside an agent-writable tree would let
// any agent with write_file rewrite its own tool policy, locked flag, etc.
// pkg/fspolicy's carve-out list (Wave 1's file, not touched here) must
// include "entities" for this separation to hold under FSScopeUnrestricted —
// see D2's MANDATORY note in the ADR.
package agentstore

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// agentsSubdir is the fixed directory segment under $OMNIPUS_HOME/entities/
// this store is rooted at (ADR-054 D2).
const agentsSubdir = "agents"

// accessors wires config.AgentConfig's identity/creation-timestamp fields
// into the shape entity.Store[config.AgentConfig] needs. CreatedAt is a
// *time.Time (nil means "never set"); entity.Accessors works in concrete
// time.Time, so a nil pointer maps to the zero Time (entity.Store.Create
// treats a zero CreatedAt as "not yet stamped" and stamps it itself — see
// entity.Store.Create's doc comment).
var accessors = entity.Accessors[config.AgentConfig]{
	GetID: func(a *config.AgentConfig) string { return a.ID },
	SetID: func(a *config.AgentConfig, id string) { a.ID = id },
	GetCreatedAt: func(a *config.AgentConfig) time.Time {
		if a.CreatedAt == nil {
			return time.Time{}
		}
		return *a.CreatedAt
	},
	SetCreatedAt: func(a *config.AgentConfig, ts time.Time) { a.CreatedAt = &ts },
}

// Notifier is implemented by the read-side cache (pkg/agent.AgentRegistry) so
// a successful entity write can update/invalidate the cached in-memory copy
// immediately, instead of every read having to touch disk (ADR-054 §5's
// design-shaping fix: "the entity store is read through an in-memory cache,
// not the disk... a write updates the record, then the registry").
//
// Store calls these hooks synchronously, AFTER the underlying entity write
// has been durably confirmed (entity.Store.Create/Update already read the
// record back before returning — see their doc comments), and BEFORE
// Create/Update/Delete return control to the caller. So by the time a caller
// observes a nil error, the registry (if wired) already reflects it.
//
// Defined here — not imported from pkg/agent — to avoid an import cycle:
// pkg/agent already depends on pkg/config, and this package depends on
// pkg/config too, so pkg/agentstore must never import pkg/agent.
// *pkg/agent.AgentRegistry satisfies this interface via its UpsertAgent /
// RemoveAgent methods once the caller adapts them (see SetNotifier's doc
// comment for the expected wiring shape).
type Notifier interface {
	// AgentUpserted is called with the just-verified record after Create or
	// Update succeeds.
	AgentUpserted(id string, cfg *config.AgentConfig)
	// AgentDeleted is called after Delete succeeds.
	AgentDeleted(id string)
}

// Store is the agent-specific wrapper over the generic entity store. See the
// package doc for the on-disk location and ADR-054 D2/D3/D6 rules it
// implements.
type Store struct {
	inner    *entity.Store[config.AgentConfig]
	notifier Notifier
}

// New creates a Store rooted at $OMNIPUS_HOME/entities/agents. omnipusHome is
// $OMNIPUS_HOME itself (e.g. config.OmnipusHomeDir()) — this constructor
// appends the fixed "entities/agents" path segment ADR-054 D2 mandates.
//
// Eagerly creates the directory (best-effort; a failure is logged, not
// returned/panicked — Create's own os.MkdirAll inside entity.Store.write
// still runs and is authoritative). This works around a real gap in
// entity.Store[T].Create (pkg/entity, Wave 1 of this ADR): Create takes its
// sidecar flock via fileutil.WithFlock(s.lockPath(id), ...) BEFORE the
// directory is guaranteed to exist — WithFlock is a bare os.OpenFile with no
// MkdirAll of its own, and entity.Store's own MkdirAll only happens later,
// inside write(), which runs INSIDE that same WithFlock callback. On a truly
// fresh install (entities/agents/ has never been created) the very first
// Create fails outright with "no such file or directory" opening the lock
// file. Pre-creating the directory here at Store construction — which every
// boot path calls once, well before any concurrent Create — sidesteps it for
// agents specifically without touching pkg/entity (out of this wave's
// scope); the underlying gap should still be fixed at the source for every
// future entity kind (channels, etc.) that reuses entity.Store[T].
func New(omnipusHome string) *Store {
	dir := filepath.Join(omnipusHome, "entities", agentsSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.WarnF("agentstore: could not pre-create entity directory (Create will retry via entity.Store's own MkdirAll)",
			map[string]any{"dir": dir, "error": err.Error()})
	}
	return &Store{inner: entity.New[config.AgentConfig](dir, accessors)}
}

// SetNotifier wires the read-side cache invalidation hook (see Notifier).
// Call once at boot, before any concurrent Create/Update/Delete — e.g.:
//
//	store := agentstore.New(cfg.Agents.Defaults.Home)
//	store.SetNotifier(registryNotifierAdapter{registry})
//
// Passing nil is valid and is the zero-value default: Create/Update/Delete
// still work (and still verify persistence), they simply have no cache to
// invalidate — useful for the CLI, tests, and any boot path that does not yet
// have a live registry to notify.
func (s *Store) SetNotifier(n Notifier) {
	s.notifier = n
}

// Dir returns the store's entity directory
// ($OMNIPUS_HOME/entities/agents).
func (s *Store) Dir() string { return s.inner.Dir() }

// Get returns the agent record for id. Returns an error wrapping
// entity.ErrNotFound when absent (errors.Is(err, entity.ErrNotFound) still
// works through this wrapper, per fmt.Errorf's %w).
func (s *Store) Get(id string) (*config.AgentConfig, error) {
	cfg, err := s.inner.Get(id)
	if err != nil {
		return nil, fmt.Errorf("agentstore: get %q: %w", id, err)
	}
	return cfg, nil
}

// List returns every parseable agent record, sorted by (created_at, id) —
// entity.Store.List's ordering contract, which this wrapper does not
// re-implement — plus the set of ids that exist on disk but failed to load
// (ADR-054 D7's skip-and-warn boot semantics).
//
// Callers MUST distinguish `skipped` from "never existed" per D6 rule 2
// (dangling is surfaced, never fatal): a binding or delegation target naming
// a skipped ID is a different, more serious case ("this agent's record is
// broken") than one naming an ID that was simply never created. See
// pkg/agent.AgentRegistry.EvaluateDefaultAgentHealth for R3's specific
// consumer of this distinction (the configured default agent's record failed
// to load → mark degraded, but keep routing via the fallback ladder).
func (s *Store) List() (agents []config.AgentConfig, skipped []string, err error) {
	agents, skipped, err = s.inner.List()
	if err != nil {
		return nil, nil, fmt.Errorf("agentstore: list: %w", err)
	}
	return agents, skipped, nil
}

// Create persists a new agent record under id. id must be non-empty; a
// caller-supplied cfg.ID that conflicts with id is rejected rather than
// silently overridden (a mismatch here almost always indicates a caller bug
// upstream, e.g. reusing a request struct across two different creates).
//
// entity.Store.Create ALREADY performs write-then-verify internally
// (ADR-054 D6 corollary: it reads the just-written file back and confirms it
// parses with the expected ID before returning success) — this is the
// durable fix for the observed failure class where 8 create_agent calls all
// reported success but only 5 were actually persisted. This wrapper does not
// duplicate that check; it adds the agent-specific id/notifier concerns and
// propagates entity.Store's guarantee unchanged. A nil error from Create means
// the record is confirmed durable on disk, full stop — callers may treat the
// agent as real (e.g. safely reference its ID from a roster) without any
// further read-back of their own.
func (s *Store) Create(id string, cfg *config.AgentConfig) error {
	if id == "" {
		return fmt.Errorf("agentstore: create: empty id")
	}
	if cfg == nil {
		return fmt.Errorf("agentstore: create %q: nil config", id)
	}
	if cfg.ID != "" && cfg.ID != id {
		return fmt.Errorf("agentstore: create %q: cfg.ID %q does not match requested id", id, cfg.ID)
	}
	cfg.ID = id
	if err := s.inner.Create(cfg); err != nil {
		return fmt.Errorf("agentstore: create %q: %w", id, err)
	}
	if s.notifier != nil {
		s.notifier.AgentUpserted(id, cfg)
	}
	return nil
}

// Update performs a read-modify-write of the agent identified by id under
// entity.Store's striped-mutex + sidecar-flock guard (ADR-054 D3), then
// notifies the registry cache of the result. mutate must not change the
// record's ID.
func (s *Store) Update(id string, mutate func(*config.AgentConfig) error) (*config.AgentConfig, error) {
	updated, err := s.inner.Update(id, mutate)
	if err != nil {
		return nil, fmt.Errorf("agentstore: update %q: %w", id, err)
	}
	if s.notifier != nil {
		s.notifier.AgentUpserted(id, updated)
	}
	return updated, nil
}

// Delete removes the agent's entity record (ADR-054 D6 rule 5: the entity
// record is removed FIRST). Best-effort cleanup of the agent's own directory
// (agents/<id>/ — SOUL.md, HEARTBEAT.md, workspace files) is a separate
// concern owned by the caller (the REST handler), which alone knows about
// those file layouts; this method touches only the entity record. Dangling
// referrers this delete creates (bindings, mailboxes, workspace core_team)
// are surfaced for repair per D6 rule 2 — never silently pruned here or
// anywhere else in this package.
func (s *Store) Delete(id string) error {
	if err := s.inner.Delete(id); err != nil {
		return fmt.Errorf("agentstore: delete %q: %w", id, err)
	}
	if s.notifier != nil {
		s.notifier.AgentDeleted(id)
	}
	return nil
}
