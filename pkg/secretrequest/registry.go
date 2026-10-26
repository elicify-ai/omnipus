// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package secretrequest owns the value-free secret-card lifecycle. Values
// exist only during Submit and in the encrypted credential store thereafter.
package secretrequest

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/childenv"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

const MaxPending = 64
const MaxTerminalPerSession = 32

// Record is internal session metadata. It NEVER holds a submitted value.
type Record struct {
	Card          gen.SessionStateSecretRequest `json:"card"`
	Input         Input                         `json:"input"`
	Recipients    []credentials.CardRecipient   `json:"recipients"`
	Owner         string                        `json:"owner"`
	ChatID        string                        `json:"chat_id"`
	ReasonCode    string                        `json:"reason_code,omitempty"`
	ResumeStopped bool                          `json:"resume_stopped,omitempty"`
}

// Options supplies live dependencies; reload never leaves a stale config or
// credential store captured on an executable tool.
type Options struct {
	Sessions    *session.UnifiedStore
	Config      func() *config.Config
	Credentials func() (*credentials.Store, error)
	// RecoveryCredentials reports ErrRecoveryStoreAbsent for a fresh install;
	// boot reconciliation must not create credentials or a key just to sweep.
	RecoveryCredentials func() (*credentials.Store, error)
	Stopped             func(string) (bool, error)
	Resume              func(Record) error
	Audit               func(Record, string)
}

// Registry serializes admission, Stop, submit and recovery. Session metadata
// is durable; the encrypted store receipt is the submission commit fact.
type Registry struct {
	mu        sync.Mutex
	opts      Options
	entries   map[string]Record
	sink      func(Record)
	inFlight  map[string]bool
	initErr   error
	recovered bool
	// receiptRetry holds receipts of terminal requests that retention already
	// removed from published session state but whose encrypted-store receipt
	// cleanup failed (request id -> session id). It is retried on later state
	// operations; a restart retries it through the full-inventory orphan sweep.
	receiptRetry map[string]string
	// vetoPending holds submitted requests whose explicit-Stop veto is set in
	// memory but whose publication failed (request id -> true). The next Stop
	// retries the publication instead of treating the cached flag as durable
	// (gate r1 SFH T2-SF-08).
	vetoPending map[string]bool
}

func NewRegistry(opts Options) *Registry {
	return &Registry{opts: opts, entries: make(map[string]Record), inFlight: make(map[string]bool),
		receiptRetry: make(map[string]string), vetoPending: make(map[string]bool)}
}

func (r *Registry) SetSink(sink func(Record)) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
}

// RequestError is a value-free HTTP refusal. State is supplied for a 409 so a
// client never guesses whether a request was submitted.
type RequestError struct {
	Status  int
	Message string
	State   *gen.SecretRequestState
}

func (e *RequestError) Error() string { return e.Message }

func failure(status int, text string) error { return &RequestError{Status: status, Message: text} }

func (r *Registry) conflict(rec Record, text string) error {
	state := State(rec)
	return &RequestError{Status: http.StatusConflict, Message: text, State: &state}
}

// Create is called only from the authenticated turn context, not from REST.
// The server derives agent, account, workspace and root session itself.
func (r *Registry) Create(ctx context.Context, sessionID, agentID, chatID, originLabel string, in Input) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.initErr != nil {
		return Record{}, errors.New("secret requests are unavailable: saved state could not be read")
	}
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if in.Variable != "" {
		// A malformed or protected name is refused with its rule before any
		// session or pending-card state is consulted (US-7 AS-5).
		if err := childenv.CheckPrivateVariableName(in.Variable); err != nil {
			return Record{}, err
		}
	}
	if r.opts.Sessions == nil || r.opts.Config == nil {
		return Record{}, errors.New("secret request service is unavailable")
	}
	if cleanupErr := r.reconcilePendingLocked(); cleanupErr != nil {
		return Record{}, errors.New("secret request state could not be reconciled")
	}
	meta, err := r.opts.Sessions.GetMeta(sessionID)
	if err != nil || meta.AgentID != agentID {
		return Record{}, errors.New("secret request session is unavailable")
	}
	ancestry, err := r.opts.Sessions.ResolveInteractionContext(sessionID)
	if err != nil {
		return Record{}, errors.New("secret request root session could not be resolved")
	}
	if ancestry.RootSessionID != sessionID && ancestry.RootAgentID != "omnipus" {
		return Record{}, errors.New("ask your parent to request a secret")
	}
	if ancestry.RootType != session.SessionTypeChat && ancestry.RootType != session.SessionTypeMain {
		return Record{}, errors.New("open the web app and ask the agent there")
	}
	cfg := r.opts.Config()
	name := agentName(cfg, agentID)
	if name == "" || len([]rune(name)) > 128 {
		return Record{}, errors.New("acting agent is unavailable or has an invalid display name")
	}
	if len([]rune(in.Purpose)) == 0 || len([]rune(in.Purpose)) > 500 || !utf8.ValidString(in.Purpose) {
		return Record{}, errors.New("purpose must contain 1–500 characters")
	}
	pending := 0
	for _, existing := range r.entries {
		if existing.Card.State != "pending" {
			continue
		}
		pending++
		if existing.Card.SessionId == sessionID {
			return Record{}, errors.New("a secret request is already pending in this session")
		}
	}
	if pending >= MaxPending {
		return Record{}, errors.New("too many pending secret requests")
	}
	if r.opts.Stopped != nil {
		stopped, stopErr := r.opts.Stopped(sessionID)
		if stopErr != nil || stopped {
			return Record{}, errors.New("secret request session is stopped or unavailable")
		}
	}
	if in, err = r.privateOwnerPairLocked(cfg, agentID, ancestry.WorkspaceID, in); err != nil {
		return Record{}, err
	}
	recipients, fields, display, err := ResolveRecipient(cfg, in)
	if err != nil {
		return Record{}, err
	}
	if in.Variable != "" {
		display.Lines = r.privateDisplayLines(cfg, in)
	}
	if len(display.Lines) < 1 || len(display.Lines) > 8 {
		return Record{}, errors.New("recipient cannot be shown on a secure card")
	}
	for _, line := range display.Lines {
		if !utf8.ValidString(line) || len([]rune(line)) < 1 || len([]rune(line)) > 500 {
			return Record{}, errors.New("recipient is too long to show safely on a secure card")
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rec := Record{Input: in, Recipients: recipients, Owner: ancestry.Owner, ChatID: chatID, Card: gen.SessionStateSecretRequest{
		RequestId: uuid.NewString(), SessionId: sessionID, ActingAgentId: agentID, ActingAgentName: name,
		Purpose: in.Purpose, Fields: fields, Recipient: display, State: "pending", UpdatedAt: now,
	}}
	if ancestry.RootSessionID != sessionID {
		rec.Card.RootSessionId = &ancestry.RootSessionID
	}
	if ancestry.WorkspaceID != "" {
		rec.Card.WorkspaceId = &ancestry.WorkspaceID
	}
	if in.Variable != "" {
		rec.Card.Owner = &struct {
			AgentId     string `json:"agent_id"`
			WorkspaceId string `json:"workspace_id"`
		}{AgentId: in.AgentID, WorkspaceId: in.WorkspaceID}
	}
	if originLabel != "" {
		if runes := []rune(originLabel); len(runes) > 200 {
			originLabel = string(runes[:200])
		}
		rec.Card.OriginLabel = &originLabel
	}
	r.entries[rec.Card.RequestId] = rec
	if err := r.persistLocked(sessionID); err != nil {
		delete(r.entries, rec.Card.RequestId)
		logStorageFault("state_publication", rec.Card.RequestId, sessionID, err, repairSessionStorage)
		return Record{}, &storageFaultError{message: "secret request could not be saved; " + repairSessionStorage, cause: err}
	}
	r.notifyLocked(rec, "requested")
	return cloneRecord(rec), nil
}

func agentName(cfg *config.Config, id string) string {
	if cfg == nil {
		return ""
	}
	for _, agent := range cfg.Agents.List {
		if agent.ID == id {
			return agent.Name
		}
	}
	return ""
}

// Get checks ownership and current session existence before revealing state.
func (r *Registry) Get(id, owner string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getLocked(id, owner)
}

func (r *Registry) getLocked(id, owner string) (Record, error) {
	if r.initErr != nil {
		return Record{}, failure(http.StatusServiceUnavailable, msgStateUnavailable)
	}
	r.retryReceiptMaintenanceLocked()
	if err := r.reconcilePendingLocked(); err != nil {
		return Record{}, failure(http.StatusServiceUnavailable, msgStateUnreconciled)
	}
	rec, ok := r.entries[id]
	if !ok || (rec.Owner != "" && rec.Owner != owner) {
		return Record{}, failure(http.StatusNotFound, "secret request not found")
	}
	if _, err := r.opts.Sessions.GetMeta(rec.Card.SessionId); err != nil {
		return Record{}, failure(http.StatusNotFound, "secret request not found")
	}
	return cloneRecord(rec), nil
}

// Submit performs every trust check before touching the encrypted store.
func (r *Registry) Submit(id, owner string, input gen.SecretSubmitRequest) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, err := r.getLocked(id, owner)
	if err != nil {
		return Record{}, err
	}
	if rec.Card.State != "pending" {
		return rec, r.conflict(rec, "secret request is not pending")
	}
	if checkErr := r.checkRecipientLocked(rec); checkErr != nil {
		return r.refuseLocked(rec, checkErr.Error())
	}
	if input.Cancel != nil && bool(*input.Cancel) && input.Values == nil {
		return r.finishLocked(rec, "cancelled")
	}
	if input.Cancel != nil || input.Values == nil || len(*input.Values) != len(rec.Card.Fields) {
		r.auditRefusalLocked(rec, "invalid_payload")
		return rec, failure(http.StatusBadRequest, "submit exactly the requested fields, or cancel")
	}
	values := make([]credentials.CardValue, 0, len(rec.Card.Fields))
	for i, field := range rec.Card.Fields {
		value, ok := (*input.Values)[field.Key]
		if !ok || len(value) < 4 || len(value) > 16384 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			r.auditRefusalLocked(rec, "invalid_value_shape")
			return rec, failure(http.StatusBadRequest, "each requested value must be 4–16384 UTF-8 bytes without NUL")
		}
		values = append(values, credentials.CardValue{BaseName: ReferenceBase(rec.Recipients[i]), Recipient: rec.Recipients[i], Value: value})
	}
	if r.opts.Credentials == nil {
		return rec, failure(http.StatusInternalServerError, msgValuesNotSaved)
	}
	store, err := r.opts.Credentials()
	if err == nil && store == nil {
		err = errors.New("secret store is unavailable")
	}
	if err != nil {
		logStorageFault("store_open", id, rec.Card.SessionId, err, repairCredentialStore)
		return rec, failure(http.StatusInternalServerError, msgValuesNotSaved)
	}
	receipt, err := store.CommitCard(id, rec.Card.SessionId, values, time.Now())
	if err != nil && !errors.Is(err, credentials.ErrCardAlreadySubmitted) {
		logStorageFault("credential_commit", id, rec.Card.SessionId, err, repairCredentialStore)
		return rec, failure(http.StatusInternalServerError, msgValuesNotSaved)
	}
	// Nothing beyond this point may claim the request is pending. A failed
	// metadata write is repairable from the receipt, not a failed submit.
	rec.Card.State, rec.Card.ReferenceNames = "submitted", receipt.References
	rec.Card.UpdatedAt = receipt.SubmittedAt.Format(time.RFC3339Nano)
	r.entries[id] = rec
	persistErr := r.persistLocked(rec.Card.SessionId)
	if persistErr != nil {
		logStorageFault("state_publication", id, rec.Card.SessionId, persistErr, repairSessionStorage)
	}
	r.notifyLocked(rec, "submitted")
	resumeErr := r.ensureDeliveryLocked(rec)
	rec = r.entries[id]
	if resumeErr != nil {
		logStorageFault("continuation_dispatch", id, rec.Card.SessionId, resumeErr, "reopen this chat to retry the saved continuation")
	}
	if persistErr != nil || resumeErr != nil {
		return cloneRecord(rec), &RequestError{Status: http.StatusServiceUnavailable, Message: msgSavedUnconfirmed, State: statePointer(rec)}
	}
	return cloneRecord(rec), nil
}

func (r *Registry) checkRecipientLocked(rec Record) error {
	ancestry, ancestryErr := r.opts.Sessions.ResolveInteractionContext(rec.Card.SessionId)
	root := rec.Card.SessionId
	if rec.Card.RootSessionId != nil {
		root = *rec.Card.RootSessionId
	}
	workspace := ""
	if rec.Card.WorkspaceId != nil {
		workspace = *rec.Card.WorkspaceId
	}
	if ancestryErr != nil || ancestry.RootSessionID != root || ancestry.WorkspaceID != workspace || ancestry.Owner != rec.Owner {
		return errors.New("secret request session or account binding changed; request a new card")
	}
	if agentName(r.opts.Config(), rec.Card.ActingAgentId) == "" {
		return errors.New("acting agent is unavailable")
	}
	if r.opts.Stopped != nil {
		stopped, err := r.opts.Stopped(rec.Card.SessionId)
		if err != nil || stopped {
			return errors.New("session is stopped or unavailable")
		}
		if rec.Card.RootSessionId != nil {
			stopped, err = r.opts.Stopped(*rec.Card.RootSessionId)
			if err != nil || stopped {
				return errors.New("root session is stopped or unavailable")
			}
		}
	}
	current, _, _, err := ResolveRecipient(r.opts.Config(), rec.Input)
	if err != nil || !reflect.DeepEqual(current, rec.Recipients) {
		return errors.New("secret recipient changed; request a new card")
	}
	return nil
}

func (r *Registry) refuseLocked(rec Record, reason string) (Record, error) {
	state := "refused"
	rec.ReasonCode = "recipient_changed"
	if strings.Contains(reason, "acting agent") {
		rec.ReasonCode = "acting_agent_unavailable"
	}
	if strings.Contains(reason, "binding changed") {
		rec.ReasonCode = "session_binding_changed"
	}
	if strings.Contains(reason, "stopped") {
		state = "stopped"
		rec.ReasonCode = "session_stopped"
	}
	updated, err := r.finishLocked(rec, state)
	if err != nil {
		return rec, err
	}
	return updated, r.conflict(updated, reason)
}

func (r *Registry) finishLocked(rec Record, state string) (Record, error) {
	previous := r.entries[rec.Card.RequestId]
	rec.Card.State, rec.Card.UpdatedAt = state, time.Now().UTC().Format(time.RFC3339Nano)
	r.entries[rec.Card.RequestId] = rec
	if err := r.persistLocked(rec.Card.SessionId); err != nil {
		r.entries[rec.Card.RequestId] = previous
		logStorageFault("state_publication", rec.Card.RequestId, rec.Card.SessionId, err, repairSessionStorage)
		return previous, failure(http.StatusInternalServerError, "secret request state could not be saved; "+repairSessionStorage)
	}
	r.notifyLocked(rec, state)
	return cloneRecord(rec), nil
}

func (r *Registry) notifyLocked(rec Record, event string) {
	if r.opts.Audit != nil {
		r.opts.Audit(cloneRecord(rec), event)
	}
	if r.sink != nil {
		r.sink(cloneRecord(rec))
	}
}

func State(rec Record) gen.SecretRequestState {
	at, _ := time.Parse(time.RFC3339Nano, rec.Card.UpdatedAt)
	state := gen.SecretRequestState{RequestId: rec.Card.RequestId, State: gen.SecretRequestStateState(rec.Card.State), UpdatedAt: at}
	if rec.Card.State == "submitted" {
		refs := append([]string{}, rec.Card.ReferenceNames...)
		state.ReferenceNames = &refs
	}
	return state
}
func statePointer(rec Record) *gen.SecretRequestState { state := State(rec); return &state }

func cloneRecord(rec Record) Record {
	cloned := rec
	cloned.Card.Fields = append([]gen.SecretRequestField(nil), rec.Card.Fields...)
	cloned.Card.ReferenceNames = append([]string(nil), rec.Card.ReferenceNames...)
	cloned.Card.Recipient.Lines = append([]string(nil), rec.Card.Recipient.Lines...)
	cloned.Recipients = append([]credentials.CardRecipient(nil), rec.Recipients...)
	for i := range cloned.Recipients {
		cloned.Recipients[i].Args = append([]string(nil), rec.Recipients[i].Args...)
	}
	if rec.Card.Owner != nil {
		owner := *rec.Card.Owner
		cloned.Card.Owner = &owner
	}
	if rec.Card.RootSessionId != nil {
		id := *rec.Card.RootSessionId
		cloned.Card.RootSessionId = &id
	}
	if rec.Card.WorkspaceId != nil {
		id := *rec.Card.WorkspaceId
		cloned.Card.WorkspaceId = &id
	}
	if rec.Card.OriginLabel != nil {
		label := *rec.Card.OriginLabel
		cloned.Card.OriginLabel = &label
	}
	return cloned
}
