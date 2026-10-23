// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

func (a *restAPI) pairingOrderStore() *channels.PairingAttempts {
	if a.agentLoop == nil {
		return nil
	}
	if mgr := a.agentLoop.GetChannelManager(); mgr != nil {
		return mgr.PairingAttempts()
	}
	return nil
}

// prepareConfigJSONLocked reads the ACTUAL saved state, including after a
// durable save whose live refresh failed. It performs no pairing or disk effect.
func (a *restAPI) prepareConfigJSONLocked(mutate func(map[string]any) error) ([]byte, *config.Config, *config.Config, error) {
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read config: %w", err)
	}
	var doc map[string]any
	var before config.Config
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("parse config: %w", err)
	}
	if err = json.Unmarshal(raw, &before); err != nil {
		return nil, nil, nil, fmt.Errorf("parse config: %w", err)
	}
	if err = mutate(doc); err != nil {
		return nil, nil, nil, err
	}
	if v, ok := doc["version"].(float64); !ok || int(v) < config.CurrentVersion {
		doc["version"] = config.CurrentVersion
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("serialize config: %w", err)
	}
	var candidate config.Config
	if err := json.Unmarshal(out, &candidate); err != nil {
		return nil, nil, nil, fmt.Errorf("parse candidate config: %w", err)
	}
	candidate.CopyChannelOrderFrom(a.agentLoop.GetConfig())
	if store := a.pairingOrderStore(); store != nil {
		store.CarrySavedConfigOrders(&candidate)
	}
	return out, &before, &candidate, nil
}

func (a *restAPI) publishWrittenConfig(candidate, source *config.Config, exactWrite bool) error {
	if exactWrite {
		candidate.CopyChannelWriteFrom(source)
		a.agentLoop.SwapWrittenConfig(candidate)
		return nil
	}
	if store := a.pairingOrderStore(); store != nil {
		// Split-phase non-channel writers also reload actual saved bytes. Bind
		// there, not from a later GetConfig, then validate at publication.
		progress, err := store.BindSavedConfigRead(candidate)
		if err != nil {
			return err
		}
		a.publishPairingAfterConfigUnlock(progress)
		return a.agentLoop.SwapReadConfig(candidate)
	}
	a.agentLoop.SwapWrittenConfig(candidate)
	return nil
}

// orderedConfigJSONLocked is the common local commit for channel settings,
// routing, bulk/cascade writes and single-channel enable/disable. The config
// write lock is already held. Fixed orders belong to the caller's admission.
func (a *restAPI) orderedConfigJSONLocked(store *channels.PairingAttempts, fixed []*channels.PairingOrder, reservations map[string]string, mutate func(map[string]any) error, exclusiveDeletes ...string) error {
	if store == nil {
		out, err := a.writeConfigJSONLocked(mutate)
		if err != nil {
			return err
		}
		return a.applyWrittenConfigLocked(out)
	}
	out, before, candidate, err := a.prepareConfigJSONLocked(mutate)
	if err != nil {
		return err
	}
	orders := fixed
	if orders == nil {
		orders = store.LockChannels(channels.ChannelConfigChanges(before, candidate))
		defer func() {
			for i := len(orders) - 1; i >= 0; i-- {
				orders[i].Unlock()
			}
		}()
	}
	commit, err := channels.PreparePairingConfig(before, candidate, orders, reservations)
	if err != nil {
		return err
	}
	commit.ProtectDeletes(exclusiveDeletes...)
	if err = fileutil.WriteFileAtomic(a.configPath(), out, 0o600); err != nil {
		return err // no ready/revoke transition before persistence
	}
	err = a.applyWrittenConfigLocked(out, candidate)
	progress := commit.Commit(err == nil)
	a.publishPairingAfterConfigUnlock(progress)
	return err
}

// Some callers compose this helper inside a larger config-locked operation.
// The short handoff waits for that lock to release without holding an order;
// its envelopes have already been accepted/stamped and cannot target successors.
func (a *restAPI) publishPairingAfterConfigUnlock(progress []channels.PairingProgress) {
	if len(progress) == 0 || a.agentLoop == nil {
		return
	}
	go func() {
		a.configMu.Lock()
		a.configMu.Unlock()
		a.agentLoop.PublishPairingProgress(progress)
	}()
}
