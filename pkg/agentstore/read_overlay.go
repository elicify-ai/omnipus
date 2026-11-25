// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agentstore

// Skills & Tools C-19 "When the new rules apply": from the moment the
// one-time upgrade's snapshot is durable, an agent still pending or in_flight
// is resolved from its planned values, not from its record on disk. The plan
// lives in pkg/skillperm, which imports packages that must not depend on this
// one, so it registers its overlay here, the way config.RegisterSkillMapHeal
// works.

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ReadOverlay adjusts an agent record read from the store under home in
// place, before any caller sees it. An error means the record's values
// cannot be decided; the record is then not served.
type ReadOverlay func(home string, agent *config.AgentConfig) error

var readOverlay ReadOverlay

// RegisterReadOverlay installs the overlay applied by Get and List. It is
// called from a package init, before any read.
func RegisterReadOverlay(fn ReadOverlay) {
	readOverlay = fn
}

func (s *Store) overlay(agent *config.AgentConfig) error {
	if readOverlay == nil || agent == nil {
		return nil
	}
	return readOverlay(s.home, agent)
}

// notifyOverlaid hands the registry a record just written by a skills path
// with the overlay applied, so an agent the upgrade has not reached runs on
// its planned values (C-19). rec must be the caller's own copy: the overlay
// changes it in place. When the values cannot be decided the registry is not
// told, and the error says the record itself was saved.
func (s *Store) notifyOverlaid(id string, rec *config.AgentConfig) error {
	if s.notifier == nil {
		return nil
	}
	if err := s.overlay(rec); err != nil {
		return fmt.Errorf("agentstore: %q was saved, but its upgraded skill values cannot be decided, so the running agent was not updated: %w", id, err)
	}
	s.notifier.AgentUpserted(id, rec)
	return nil
}
