//go:build (linux || darwin || windows) && !mipsle

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package whatsapp

import (
	"context"
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// nativeLifecycle is one captured resource, not an authority or lookup by name.
// Client/container/attemptID are written under c.mu, then frozen by the work join.
// err is read only after done closes, including by retained attempt handles.
type nativeLifecycle struct {
	ctx       context.Context
	cancel    context.CancelFunc
	client    *whatsmeow.Client
	container *sqlstore.Container
	attemptID string
	once      sync.Once
	done      chan struct{}
	err       error
}

func (c *WhatsAppNativeChannel) beginNativeStart(ctx context.Context) (*nativeLifecycle, error) {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous := c.lifecycle; previous != nil {
		select {
		case <-previous.done:
			if previous.err != nil {
				return nil, previous.err
			}
		default:
			return nil, fmt.Errorf("the previous WhatsApp client is still running or stopping")
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	resource := &nativeLifecycle{ctx: runCtx, cancel: cancel, done: make(chan struct{})}
	c.lifecycle, c.runCtx, c.runCancel = resource, runCtx, cancel
	c.stopping.Store(false)
	c.reconnecting = false
	c.wg.Add(1) // Start, including initialization and its first connect, joins before final disposal
	return resource, nil
}

// stopNative initiates cancellation once; the caller's deadline only bounds its
// wait. Disconnect can itself wait behind ConnectContext's library socket lock,
// so both disconnects and the join run in the retained finalizer, not the caller.
func (c *WhatsAppNativeChannel) stopNative(ctx context.Context, resource *nativeLifecycle, interrupt bool) error {
	resource.once.Do(func() {
		// This captured context is immutable and needs no native/order lock.
		// A reconnect may hold those locks while waiting for a config writer,
		// so even one-time bookkeeping belongs off the caller's deadline path.
		if resource.cancel != nil {
			resource.cancel()
		}
		go c.beginNativeStop(resource, interrupt)
	})
	select {
	case <-resource.done:
		return resource.err
	case <-ctx.Done():
		return fmt.Errorf("the WhatsApp client did not finish stopping: %w", ctx.Err())
	}
}

// beginNativeStop retains the existing Add/Wait synchronization: stopping is
// set under reconnectMu before disposeNative joins work. Only this once-owned
// goroutine may wait for native locks or the channel order during setup.
func (c *WhatsAppNativeChannel) beginNativeStop(resource *nativeLifecycle, interrupt bool) {
	c.reconnectMu.Lock()
	c.mu.Lock()
	attemptID, store, instance := resource.attemptID, c.attempts, c.instance
	if c.lifecycle == resource {
		c.stopping.Store(true)
		if attemptID == "" {
			attemptID = c.attemptID
			resource.attemptID = attemptID
		}
		if attemptID != "" {
			if c.unfinishedTeardowns == nil {
				c.unfinishedTeardowns = make(map[string]*nativeLifecycle)
			}
			c.unfinishedTeardowns[attemptID] = resource
		}
		if c.attemptID == attemptID {
			c.attemptID = ""
		}
	}
	c.mu.Unlock()
	c.reconnectMu.Unlock()
	c.disposeNative(resource, store, instance, attemptID, interrupt)
}

func (c *WhatsAppNativeChannel) disposeNative(resource *nativeLifecycle, store *channels.PairingAttempts, instance, attemptID string, interrupt bool) {
	var replacement channels.PairingAttempt
	if store != nil && attemptID != "" {
		rec, _ := store.Attempt(instance, attemptID)
		if interrupt || rec.Phase == channels.PairingPhaseStopping && rec.Outcome == channels.PairingOutcomeInterrupted {
			if captured, applied := store.BeginResourceTeardown(instance, attemptID); applied {
				replacement = captured
				c.emitAttempt(attemptID, channels.PairingStatusError, "", channels.PairingStopPendingMessage)
			}
		}
	}
	c.mu.Lock()
	client := resource.client
	c.mu.Unlock()
	if client != nil {
		client.Disconnect()
	}
	c.wg.Wait()
	// Initialization and every connect have now returned. Retain this exact
	// client through a FINAL disconnect: a late success can follow the first.
	c.mu.Lock()
	client, container := resource.client, resource.container
	c.mu.Unlock()
	if client != nil {
		client.Disconnect()
	}
	if container != nil {
		if err := container.Close(); err != nil {
			resource.err = fmt.Errorf("WhatsApp session-store disposal failed: %w", err)
			logger.ErrorCF("whatsapp", "Client cleanup unfinished; resolve the session-store error before restarting pairing",
				map[string]any{"error": err.Error(), "channel_id": instance})
		}
	}
	c.mu.Lock()
	if c.lifecycle == resource {
		if resource.err == nil && c.client == client {
			c.client, c.container = nil, nil
		}
		c.SetRunning(false)
	}
	c.mu.Unlock()
	var completed channels.PairingAttempt
	var finished bool
	if replacement.AttemptID != "" {
		completed, finished = store.FinishReplacement(instance, replacement.AttemptID, replacement.StopGeneration, resource.err)
	}
	close(resource.done) // cancellation + known work + final disposal, never just a timeout
	if finished {
		c.emitAttempt(completed.AttemptID, channels.PairingStatusError, "", channels.PairingInterruptedMessage)
	}
}

// connectNative is shared by initial pairing, linked starts, reconnect and
// repair. The library receives the lifecycle context, never BackgroundEventCtx.
func (c *WhatsAppNativeChannel) connectNative(client *whatsmeow.Client) error {
	c.reconnectMu.Lock()
	c.mu.Lock()
	if !c.nativeClientCurrentLocked(client) {
		c.mu.Unlock()
		c.reconnectMu.Unlock()
		return channels.ErrPairingConflict
	}
	ctx := c.runCtx
	c.wg.Add(1)
	c.mu.Unlock()
	c.reconnectMu.Unlock()
	defer c.wg.Done()
	err := client.ConnectContext(ctx)
	c.mu.Lock()
	current := c.nativeClientCurrentLocked(client)
	c.mu.Unlock()
	if !current {
		client.Disconnect() // the captured late success, never a channel-name lookup
		if err == nil {
			return channels.ErrPairingConflict
		}
	}
	return err
}

// Caller holds the native mutex. Native -> channel order is the permitted lock
// direction; no guard holder calls native code. A post-connect check is not a
// publication permit: Start validates again at its actual running-state effect.
func (c *WhatsAppNativeChannel) nativeClientCurrentLocked(client *whatsmeow.Client) bool {
	if c.client != client || c.stopping.Load() || c.runCtx == nil || c.runCtx.Err() != nil {
		return false
	}
	if c.attempts == nil {
		return true
	}
	order := c.attempts.LockChannel(c.instance)
	defer order.Unlock()
	if c.originConfig != nil && !order.MatchesConfig(c.originConfig) {
		return false
	}
	return order.AcceptsResource(c.resourceGeneration, c.attemptID)
}

func (c *WhatsAppNativeChannel) publishNativeRunning(client *whatsmeow.Client) bool {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != client || c.stopping.Load() || c.runCtx.Err() != nil {
		return false
	}
	if c.attempts != nil {
		order := c.attempts.LockChannel(c.instance)
		defer order.Unlock()
		if !order.AcceptsResource(c.resourceGeneration, c.attemptID) || c.originConfig != nil && !order.MatchesConfig(c.originConfig) {
			return false
		}
	}
	c.SetRunning(true)
	return true
}
