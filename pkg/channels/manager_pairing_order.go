// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package channels

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// publishConfigSnapshot validates and publishes as one guarded effect. The
// manager lock is already held; all orders release before dispatch/native waits.
func (mr *managerReload) publishConfigSnapshot() bool {
	store := mr.m.PairingAttempts()
	ids := store.ConfigOrderInstances()
	for id := range mr.cfg.Channels {
		ids = append(ids, id)
	}
	orders := store.LockChannels(ids)
	defer func() {
		for i := len(orders) - 1; i >= 0; i-- {
			orders[i].Unlock()
		}
	}()
	if err := store.CheckConfigSnapshot(mr.cfg); err != nil {
		return false
	}
	mr.oldConfig, mr.oldSecrets, mr.oldCredSigs = mr.m.config, mr.m.secrets, mr.m.credSigs
	mr.m.config, mr.m.secrets = mr.cfg, mr.secrets
	mr.m.liveCfg.Store(mr.cfg)
	return true
}

// addGenerationRestarts detects identical-public-config ABA using the exact
// snapshot carried by the resource, not a second ordering counter/authority.
func (mr *managerReload) addGenerationRestarts() {
	for id := range mr.list {
		generation, _ := mr.cfg.ChannelOrderGeneration(id)
		if _, registered := mr.m.channels[id]; !registered || generation == mr.m.resourceConfigOrders[id] {
			continue
		}
		if !slices.Contains(mr.added, id) {
			mr.added = append(mr.added, id)
			mr.removed = append(mr.removed, id)
		}
	}
}

func (m *Manager) recordResourceConfigOrder(instance string, cfg *config.Config) {
	if m.resourceConfigOrders == nil {
		m.resourceConfigOrders = make(map[string]uint64)
	}
	m.resourceConfigOrders[instance], _ = cfg.ChannelOrderGeneration(instance)
}

// channelHTTPRoute captures adapter callbacks OUTSIDE an order. Applying these
// captured paths later never calls an adapter while holding the order.
type channelHTTPRoute struct {
	path    string
	handler http.Handler
}

func snapshotChannelHTTPRoutes(channel Channel) []channelHTTPRoute {
	var routes []channelHTTPRoute
	if wh, ok := channel.(WebhookHandler); ok {
		routes = append(routes, channelHTTPRoute{wh.WebhookPath(), wh})
	}
	if hc, ok := channel.(HealthChecker); ok {
		routes = append(routes, channelHTTPRoute{hc.HealthPath(), http.HandlerFunc(hc.HealthHandler)})
	}
	return routes
}

func (mr *managerReload) publishStartedWorker(name string, channel Channel) {
	w := newChannelWorker(mr.m.channelTypeForRateLimit(name), channel)
	routes := snapshotChannelHTTPRoutes(channel)
	order := mr.m.PairingAttempts().LockChannel(name)
	if !order.MatchesConfig(mr.cfg) || mr.m.channels[name] != channel || mr.dispatchCtx.Err() != nil {
		order.Unlock()
		mr.disposeUnpublishedStart(name, channel)
		return
	}
	mr.m.noteStartLocked(name, nil)
	w.gen = mr.m.dispatchGen
	mr.m.workers[name] = w
	mr.m.recordResourceConfigOrder(name, mr.cfg)
	go mr.m.runWorker(mr.dispatchCtx, name, w, mr.cfg)
	go mr.m.runMediaWorker(mr.dispatchCtx, name, w)
	order.Unlock()
	mr.deferFuncs = append(mr.deferFuncs, func() {
		order := mr.m.PairingAttempts().LockChannel(name)
		defer order.Unlock()
		if !order.MatchesConfig(mr.cfg) || mr.m.channels[name] != channel || mr.m.mux == nil {
			return
		}
		for _, route := range routes {
			mr.m.mux.Handle(route.path, route.handler)
		}
	})
}

// disposeUnpublishedStart owns the successfully started object, even after its
// publication permission expires. No ordering guard spans Stop or an adapter
// callback; the manager lock still protects the pointer-checked bookkeeping.
func (mr *managerReload) disposeUnpublishedStart(name string, channel Channel) {
	if err := channel.Stop(mr.ctx); err != nil {
		mr.retainFailedDisposal(name, channel, err)
		if mr.m.channels[name] == channel {
			// This is the first failed disposal of the newly constructed
			// resource, so replace any predecessor hash with its own snapshot.
			mr.m.channelHashes[name] = mr.list[name]
		}
		delete(mr.list, name)
		return
	}
	if mr.m.channels[name] == channel {
		// initChannel registered only this object, not its HTTP routes. Those
		// are installed by winning publication's deferred callback, so
		// unhandling these paths here could remove somebody else's handler.
		delete(mr.m.channels, name)
		delete(mr.m.resourceConfigOrders, name)
	}
	delete(mr.list, name)
}

// retainFailedDisposal carries every failed Stop into this reload's outcome.
// The manager lock is held; only the captured object's bookkeeping is retained.
// A later reload may retry it, but cannot overwrite it with a new construction.
func (mr *managerReload) retainFailedDisposal(name string, channel Channel, err error) {
	mr.startCleanupErr = errors.Join(mr.startCleanupErr,
		fmt.Errorf("channel %q cleanup unfinished: %w; reload configuration to retry cleanup", name, err))
	if mr.m.channels[name] != channel {
		return // never change a successor's registration or applied hash
	}
	if mr.failedDisposals == nil {
		mr.failedDisposals = make(map[string]bool)
	}
	mr.failedDisposals[name] = true
	if mr.m.channelHashes == nil {
		mr.m.channelHashes = make(map[string]string)
	}
	if _, retained := mr.m.channelHashes[name]; !retained {
		// A successful unpublished start has no applied hash yet. Retain its
		// original snapshot's hash so even a disabled reload can find it.
		mr.m.channelHashes[name] = mr.list[name]
	}
	// Keep the resource's existing originating order; this failed retry has
	// not applied the newer settings to that resource.
}

func (mr *managerReload) recordStartFailure(name string, channel Channel, err error) {
	order := mr.m.PairingAttempts().LockChannel(name)
	defer order.Unlock()
	if order.MatchesConfig(mr.cfg) && mr.m.channels[name] == channel {
		mr.m.noteStartLocked(name, err)
		delete(mr.list, name)
	}
}

func (mr *managerReload) restartDrainedWorker(name string, w *channelWorker) {
	// Join the cancelled generation BEFORE entering the ordering boundary.
	<-w.done
	<-w.mediaDone
	order := mr.m.PairingAttempts().LockChannel(name)
	defer order.Unlock()
	if !order.MatchesConfig(mr.cfg) || mr.m.workers[name] != w || mr.dispatchCtx.Err() != nil {
		return
	}
	w.done, w.mediaDone = make(chan struct{}), make(chan struct{})
	w.gen = mr.m.dispatchGen
	go mr.m.runWorker(mr.dispatchCtx, name, w, mr.cfg)
	go mr.m.runMediaWorker(mr.dispatchCtx, name, w)
}

func (mr *managerReload) commitAppliedHashes() {
	ids := make(map[string]bool)
	for id := range mr.list {
		ids[id] = true
	}
	for id := range mr.m.channelHashes {
		ids[id] = true
	}
	if mr.m.channelHashes == nil {
		mr.m.channelHashes = make(map[string]string)
	}
	for id := range ids {
		if mr.failedDisposals[id] {
			continue // retain the exact failed resource's removal/retry bookkeeping
		}
		order := mr.m.PairingAttempts().LockChannel(id)
		if order.MatchesConfig(mr.cfg) {
			if hash, exists := mr.list[id]; exists {
				mr.m.channelHashes[id] = hash
			} else {
				delete(mr.m.channelHashes, id)
			}
		}
		order.Unlock()
	}
}
