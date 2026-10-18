// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const secretResumeMetadataKey = "secret_request_resume"

type secretResumeValidatedContextKey struct{}

func secretResumeContextValidated(ctx context.Context) bool {
	validated, _ := ctx.Value(secretResumeValidatedContextKey{}).(bool)
	return validated
}

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
		Credentials:         al.secretCredentialStore,
		RecoveryCredentials: al.secretRecoveryCredentialStore,
		Stopped:             al.secretSessionStopped,
		Resume:              al.dispatchSecretResume,
		Audit: func(rec secretrequest.Record, event string) {
			audit.Emit(context.Background(), al.AuditLogger(), "secret.request."+event, audit.SeverityInfo, map[string]any{
				"request_id": rec.Card.RequestId, "session_id": rec.Card.SessionId, "agent_id": rec.Card.ActingAgentId,
				"state": rec.Card.State, "reason_code": rec.ReasonCode,
			})
		},
	})
	// Publish before recovery: its credential callback uses the same state
	// mutex, so recovery runs at the gateway boundary after this method exits.
	al.secretRequests.registry = reg
	return reg
}

func (al *AgentLoop) secretRecoveryCredentialStore() (*credentials.Store, error) {
	path := filepath.Join(filepath.Dir(al.GetSessionStore().BaseDir()), "credentials.json")
	al.mu.RLock()
	if al.sysagentDeps != nil && al.sysagentDeps.CredStore != nil {
		path = al.sysagentDeps.CredStore.Path()
	}
	al.mu.RUnlock()
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, secretrequest.ErrRecoveryStoreAbsent
	} else if err != nil {
		return nil, err
	}
	return al.secretCredentialStore()
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
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		if current, err := lifecycle.Load(sessionID); err == nil && secretRestartStopped(current) {
			return nil
		}
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
			return rec.Stopped() && !secretRestartStopped(rec), nil
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
	values, scanErr := store.SensitiveValues()
	var damaged *credentials.DamagedEntriesError
	if scanErr != nil && !errors.As(scanErr, &damaged) {
		return scanErr
	}
	// A readable sibling cannot establish that THIS submitted version is usable.
	// Recheck every submitted ref, and mask the values read at that boundary too.
	for _, ref := range rec.Card.ReferenceNames {
		value, readErr := store.Get(ref)
		if readErr != nil {
			return readErr
		}
		values = append(values, value)
	}
	al.GetConfig().RegisterSensitiveValues(values)
	if damaged != nil {
		slog.Error("secret resume: some vault entries are damaged; readable values remain masked — repair the named credentials",
			"request_id", rec.Card.RequestId, "session_id", rec.Card.SessionId,
			"stage", "sensitive_values", "credential_refs", damaged.Names)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := bus.InboundMessage{
		Channel: "webchat", ChatID: rec.ChatID, SessionID: rec.Card.SessionId,
		Content: secretrequest.ResumeMessage(rec), GatewayUserID: rec.Owner,
		Metadata: map[string]string{"agent_id": rec.Card.ActingAgentId, secretResumeMetadataKey: rec.Card.RequestId, ownerWakeMetadataKey: "1"},
	}
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		current, readErr := lifecycle.Load(rec.Card.SessionId)
		if readErr != nil && !errors.Is(readErr, session.ErrLifecycleNotFound) {
			return readErr
		}
		if readErr == nil && current.SteeredBy != nil {
			msg.Channel, msg.ChatID = "system", "webchat:"+rec.ChatID
			msg.AsyncTranscriptSessionID, msg.AsyncOriginAgentID = rec.Card.SessionId, rec.Card.ActingAgentId
			msg.Metadata["steer_message_id"] = secretrequest.ResumeEntryID(rec.Card.RequestId)
			msg.Metadata["steer_generation"] = strconv.Itoa(current.Generation)
		}
	}
	return al.bus.PublishInbound(ctx, msg)
}

// deferSecretResume makes a downstream queue rejection retriable and visible.
// It never asks the person to resend a value already committed to the vault.
func (al *AgentLoop) deferSecretResume(msg bus.InboundMessage, reasonCode string) bool {
	id := inboundMetadata(msg, secretResumeMetadataKey)
	if id == "" {
		return false
	}
	reg := al.SecretRequests()
	if reg == nil || !reg.DeferResume(id, msg.SessionID, reasonCode) {
		return false
	}
	al.emitEvent(EventKindError, EventMeta{SessionKey: msg.SessionID, TracePath: "secret.resume"}, ErrorPayload{
		Stage: "secret_resume", Code: "secret_resume_pending", SessionID: msg.SessionID,
		Message: "Secret saved. Its continuation is waiting; reopen this chat to retry. Do not submit the secret again.",
	})
	return true
}

// processSecretResume is reached INSIDE the session worker, never steered into
// the request's still-finishing park turn. ClaimResume writes the transcript
// identity once; duplicate bus messages do not enter the model loop again.
func (al *AgentLoop) processSecretResume(ctx context.Context, msg bus.InboundMessage) (string, *AgentInstance, error) {
	if msg.Channel == "system" && msg.AsyncTranscriptSessionID != "" {
		response, err := al.processSteeredSystemWake(ctx, msg)
		if err != nil {
			al.deferSecretResume(msg, "helper_admission_failed")
		}
		return response, nil, err
	}
	reg := al.SecretRequests()
	if reg == nil {
		return "", nil, errors.New("secret continuation service is unavailable")
	}
	// Every rejection below happens BEFORE the durable request claim, so the
	// accepted transport is released (DeferResume) and attach/boot can retry
	// once the fault is repaired. A Stop veto or non-submitted binding is not
	// retryable and is never released here.
	rec, err := reg.Get(msg.Metadata[secretResumeMetadataKey], msg.GatewayUserID)
	if err != nil {
		al.deferSecretResume(msg, "request_read_failed")
		return "", nil, err
	}
	if rec.ResumeStopped || rec.Card.State != "submitted" || rec.Card.SessionId != msg.SessionID {
		return "", nil, errors.New("secret continuation binding is unavailable")
	}
	if lifecycle := al.GetSessionLifecycleStore(); lifecycle != nil {
		current, readErr := lifecycle.Load(msg.SessionID)
		if readErr != nil && !errors.Is(readErr, session.ErrLifecycleNotFound) {
			al.deferSecretResume(msg, "lifecycle_read_failed")
			return "", nil, readErr
		}
		if readErr == nil && secretRestartStopped(current) {
			if _, reviveErr := al.reviveSecretRestart(ctx, current); reviveErr != nil {
				al.deferSecretResume(msg, "restart_revival_failed")
				return "", nil, reviveErr
			}
		}
	}
	msg.Metadata["agent_id"] = rec.Card.ActingAgentId
	msg.Content = secretrequest.ResumeMessage(rec)
	response, usedAgent, processErr := al.processMessage(context.WithValue(ctx, secretResumeValidatedContextKey{}, true), msg)
	if processErr != nil {
		al.deferSecretResume(msg, "ordinary_admission_failed")
	}
	return response, usedAgent, processErr
}
