// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const secretResumeMetadataKey = "secret_request_resume"

type secretRequestState struct {
	mu       sync.Mutex
	registry *secretrequest.Registry
	store    *credentials.Store
}

// SecretRequests returns the one live card service, shared by REST and tools.
// Its late-bound config/store seams remain valid across normal service rewires.
func (al *AgentLoop) SecretRequests() *secretrequest.Registry {
	al.secretRequests.mu.Lock()
	defer al.secretRequests.mu.Unlock()
	if al.secretRequests.registry != nil {
		return al.secretRequests.registry
	}
	if al.GetSessionStore() == nil {
		return nil
	}
	reg := secretrequest.NewRegistry(secretrequest.Options{
		Sessions: al.GetSessionStore(), Config: al.GetConfig,
		Credentials: al.secretCredentialStore,
		Stopped:     al.secretSessionStopped,
		Resume:      al.dispatchSecretResume,
		Audit: func(rec secretrequest.Record, event string) {
			audit.Emit(context.Background(), al.AuditLogger(), "secret.request."+event, audit.SeverityInfo, map[string]any{
				"request_id": rec.Card.RequestId, "session_id": rec.Card.SessionId, "agent_id": rec.Card.ActingAgentId,
				"state": rec.Card.State,
			})
		},
	})
	// Publish before recovery: its credential callback uses the same state
	// mutex, so recovery runs at the gateway boundary after this method exits.
	al.secretRequests.registry = reg
	return reg
}

func (al *AgentLoop) secretCredentialStore() (*credentials.Store, error) {
	al.mu.RLock()
	var shared *credentials.Store
	if al.sysagentDeps != nil {
		shared = al.sysagentDeps.CredStore
	}
	al.mu.RUnlock()
	if shared != nil {
		al.GetConfig().SetStoredSensitiveSource(shared.SensitiveValues)
		return shared, nil
	}
	al.secretRequests.mu.Lock()
	defer al.secretRequests.mu.Unlock()
	if al.secretRequests.store == nil {
		home := filepath.Dir(al.GetSessionStore().BaseDir())
		store := credentials.NewStore(filepath.Join(home, "credentials.json"))
		if err := credentials.Unlock(store); err != nil {
			return nil, err
		}
		al.secretRequests.store = store
	}
	store := al.secretRequests.store
	al.GetConfig().SetStoredSensitiveSource(store.SensitiveValues)
	return store, nil
}

func (al *AgentLoop) stopSecretRequests(sessionID string) error {
	al.secretRequests.mu.Lock()
	reg := al.secretRequests.registry
	al.secretRequests.mu.Unlock()
	if reg == nil {
		return nil
	}
	return reg.Stop(sessionID)
}

func (al *AgentLoop) closeSecretRequests() {
	al.secretRequests.mu.Lock()
	defer al.secretRequests.mu.Unlock()
	if al.secretRequests.store != nil {
		al.secretRequests.store.Close()
	}
}

func (al *AgentLoop) secretSessionStopped(id string) (bool, error) {
	if _, err := al.GetSessionStore().GetMeta(id); err != nil {
		return false, err
	}
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		rec, err := lifecycle.Load(id)
		if err != nil && !errors.Is(err, session.ErrLifecycleNotFound) {
			return false, err
		}
		if err == nil {
			return rec.Stopped(), nil
		}
	}
	return false, nil
}

func (al *AgentLoop) dispatchSecretResume(rec secretrequest.Record) error {
	// Complete masking before a submitted reference is exposed to ANY probe
	// or resumed tool activity. RegisterSensitiveValues also rebuilds on reload.
	store, err := al.secretCredentialStore()
	if err != nil {
		return err
	}
	values, err := store.SensitiveValues()
	if err != nil {
		return err
	}
	al.GetConfig().RegisterSensitiveValues(values)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return al.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: rec.ChatID, SessionID: rec.Card.SessionId,
		Content: secretrequest.ResumeMessage(rec), GatewayUserID: rec.Owner,
		Metadata: map[string]string{"agent_id": rec.Card.ActingAgentId, secretResumeMetadataKey: rec.Card.RequestId},
	})
}

// processSecretResume is reached INSIDE the session worker, never steered into
// the request's still-finishing park turn. ClaimResume writes the transcript
// identity once; duplicate bus messages do not enter the model loop again.
func (al *AgentLoop) processSecretResume(ctx context.Context, msg bus.InboundMessage) (string, *AgentInstance, error) {
	reg := al.SecretRequests()
	if reg == nil {
		return "", nil, errors.New("secret continuation service is unavailable")
	}
	rec, claimed, err := reg.ClaimResume(msg.Metadata[secretResumeMetadataKey], msg.SessionID)
	if err != nil || !claimed {
		return "", nil, err
	}
	delete(msg.Metadata, secretResumeMetadataKey)
	msg.Content = secretrequest.ResumeMessage(rec)
	msg.TranscriptEntryID = secretrequest.ResumeEntryID(rec.Card.RequestId)
	return al.processMessage(ctx, msg)
}
