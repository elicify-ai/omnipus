// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package preview

import (
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
)

// shared is the gateway's one Service (admission and the page cache are
// per gateway, AD-6/AD-9). Agent instances are built from configuration
// alone, so the tool reads it at call time.
var shared atomic.Pointer[Service]

// SetShared installs the gateway's Service (nil uninstalls).
func SetShared(s *Service) { shared.Store(s) }

// Shared returns the gateway's Service, or nil before the gateway set one.
func Shared() *Service { return shared.Load() }

// StartGateway builds the gateway's Service for home, runs AD-8 startup
// recovery and installs it. cfg feeds the writer exclusion (may be nil).
func StartGateway(home string, cfg func() *config.Config) (*Service, RecoveryReport, error) {
	resolver, err := component.NewResolver(home)
	if err != nil {
		return nil, RecoveryReport{}, err
	}
	svc, err := New(Config{
		Home:     home,
		Self:     resolver.Executable,
		Resolver: resolver,
		WriteRoots: func() []component.WriteRoot {
			var c *config.Config
			if cfg != nil {
				c = cfg()
			}
			return ConfiguredWriteRoots(home, c)
		},
	})
	if err != nil {
		return nil, RecoveryReport{}, err
	}
	rep := svc.Recover()
	SetShared(svc)
	return svc, rep, nil
}
