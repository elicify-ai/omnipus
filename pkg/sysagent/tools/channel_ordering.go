// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// channelWrite lives for one local transaction. Manager requests, callbacks and
// waits use its captured results only after withChannelWrite releases both locks.
type channelWrite struct {
	deps         *Deps
	store        *channels.PairingAttempts
	orders       []*channels.PairingOrder
	reservations map[string]string
	progress     []channels.PairingProgress
}

func (d *Deps) pairingStore() *channels.PairingAttempts {
	if d.PairingAttempts != nil {
		return d.PairingAttempts()
	}
	return nil
}

func (d *Deps) withChannelWrite(id, expected string, write func(*channelWrite) error) error {
	store := d.pairingStore()
	unlock := func() {}
	if d.LockConfigWrites != nil {
		unlock = d.LockConfigWrites()
	}
	w := &channelWrite{deps: d, store: store, reservations: make(map[string]string)}
	if store != nil {
		w.orders = store.LockChannels([]string{id})
	}
	err := func() error {
		current := channelRevision(d.GetCfg(), id)
		if current != expected {
			return &revisionConflictError{Kind: sharedChannel, ID: id, Expected: expected, Current: current}
		}
		return write(w)
	}()
	w.release()
	unlock()
	w.publish()
	return err
}

func (w *channelWrite) release() {
	for i := len(w.orders) - 1; i >= 0; i-- {
		w.orders[i].Unlock()
	}
}

func (w *channelWrite) publish() {
	if w.deps.PublishPairingProgress != nil {
		w.deps.PublishPairingProgress(w.progress)
	}
}

func (w *channelWrite) admit(ctx context.Context, id string, kind channels.PairingAdmission, owner *channels.PairingOwner) (string, error) {
	var attemptID string
	var err error
	if w.store != nil {
		rec, admitErr := w.orders[0].AdmitContext(ctx, kind, owner)
		attemptID, err = rec.AttemptID, admitErr
	} else if kind == channels.PairingAdmissionConnect && w.deps.AdmitPairing != nil {
		attemptID, err = w.deps.AdmitPairing(id, owner.RequestID, owner.SessionID)
	} else if kind == channels.PairingAdmissionEnable && w.deps.AdmitEnablePairing != nil {
		attemptID, err = w.deps.AdmitEnablePairing(id)
	}
	if err == nil && attemptID != "" {
		w.reservations[id] = attemptID
	}
	return attemptID, err
}

func (w *channelWrite) withConfig(fn func(*config.Config) error) error {
	before := w.deps.GetCfg()
	var commit *channels.PairingConfigCommit
	err := w.deps.WithConfig(func(candidate *config.Config) error {
		if err := fn(candidate); err != nil {
			return err
		}
		if w.store != nil {
			var err error
			commit, err = channels.PreparePairingConfig(before, candidate, w.orders, w.reservations)
			return err
		}
		return nil
	})
	if err == nil && commit != nil {
		w.progress = append(w.progress, commit.Commit(true)...)
	} else if err != nil && w.store != nil {
		for _, order := range w.orders {
			for _, attemptID := range w.reservations {
				order.Release(attemptID, channels.PairingOutcomeInterrupted)
			}
		}
	}
	return err
}

// withGenericConfigWrite also covers set_config's whole-channel-section writes.
// The section's prospective keys are known from its input before the mutator;
// mutation runs once, with sorted guards already outside the agent-loop lock.
func (d *Deps) withGenericConfigWrite(key string, value any, fn func(*config.Config) error) error {
	store := d.pairingStore()
	unlock := func() {}
	if d.LockConfigWrites != nil {
		unlock = d.LockConfigWrites()
	}
	w := &channelWrite{deps: d, store: store}
	if store != nil {
		var ids []string
		for id := range d.GetCfg().Channels {
			ids = append(ids, id)
		}
		if strings.EqualFold(key, "channels") {
			raw, _ := json.Marshal(value)
			if text, ok := value.(string); ok {
				raw = []byte(text)
			}
			var entries map[string]json.RawMessage
			if json.Unmarshal(raw, &entries) == nil {
				for id := range entries {
					ids = append(ids, id)
				}
			}
		}
		w.orders = store.LockChannels(ids)
	}
	err := w.withConfig(fn)
	w.release()
	unlock()
	w.publish()
	return err
}
