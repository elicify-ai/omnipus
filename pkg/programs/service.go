// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Connection states (ProgramConnection.state).
const (
	StateNotConnected    = "not_connected"
	StateChecking        = "checking"
	StateConnected       = "connected"
	StateSavedNotChecked = "saved_not_checked"
	StateCheckFailed     = "check_failed"
)

// Messages of the spec's message table.
const (
	MsgSaveFailed      = "The token could not be saved. The previous token is still in use. Try again."
	MsgStaleGeneration = "The token changed since this page was loaded. Check the current state before saving again."
	MsgStoreBusy       = "The credential store is busy. Try again."
	MsgNoCheck         = "Saved, not checked: this program has no account check."
	MsgNotConnected    = "Connect a token first."
)

// CommitCardToken stores a program card's token with its receipt when no
// Programs service is bound (FR-048: program-scoped store writes stay in this
// package). It starts no account check and returns the new generation.
func CommitCardToken(store *credentials.Store, id, value string, receipt *credentials.CardReceipt) (int64, error) {
	return store.CommitProgramCredential(TokenReference(id), value, receipt)
}

// Service errors. The REST layer maps them (errors.Is) to the spec's statuses
// and the Msg texts above.
var (
	ErrUnknownProgram  = errors.New("programs: unknown program")
	ErrStaleGeneration = errors.New("programs: token generation changed")
	ErrStoreBusy       = errors.New("programs: credential store busy")
	ErrSaveFailed      = errors.New("programs: token not saved")
	ErrNotConnected    = errors.New("programs: no token connected")
)

// Connection is the persisted connection state of one entry. It never holds
// the token.
type Connection struct {
	State         string    `json:"state"`
	Generation    int64     `json:"generation"`
	AccountLabel  string    `json:"account_label,omitempty"`
	CheckedAt     time.Time `json:"checked_at,omitempty"`
	Message       string    `json:"message,omitempty"`
	CheckRevision int64     `json:"check_revision,omitempty"`
}

// reservation is an entry's current check reservation (FR-047).
type reservation struct {
	id         uint64
	generation int64
	revision   int64
	cancel     context.CancelFunc
}

// Service owns program connections: publication and disconnect (the only
// callers of the program-scoped store methods, invariant I-3), the account
// check and the persisted connection state.
type Service struct {
	cfg       func() *config.Config
	store     func() (*credentials.Store, error)
	statePath string

	mu      sync.Mutex
	conns   map[string]Connection
	checks  map[string]*reservation
	nextID  uint64
	running sync.WaitGroup
}

// NewService builds the service. statePath is the connection-state file
// (value-free); a missing file starts every entry not connected.
func NewService(cfg func() *config.Config, store func() (*credentials.Store, error), statePath string) *Service {
	s := &Service{cfg: cfg, store: store, statePath: statePath,
		conns: map[string]Connection{}, checks: map[string]*reservation{}}
	if data, err := os.ReadFile(statePath); err == nil {
		var stored map[string]Connection
		if jerr := json.Unmarshal(data, &stored); jerr != nil {
			slog.Error("programs: connection state unreadable; every entry starts from the store", "path", statePath, "error", jerr)
		} else {
			s.conns = stored
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		slog.Error("programs: read connection state", "path", statePath, "error", err)
	}
	return s
}

// Wait blocks until every running check has ended (tests and shutdown).
func (s *Service) Wait() { s.running.Wait() }

// Connection returns the entry's current connection, reconciled with the
// stored generation (the store is the authority for generation and token
// presence).
func (s *Service) Connection(id string) Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connectionLocked(id)
}

func (s *Service) connectionLocked(id string) Connection {
	c, ok := s.conns[id]
	if !ok {
		c = Connection{State: StateNotConnected}
	}
	if store, err := s.store(); err == nil {
		if gen, found := store.ProgramEntryGeneration(TokenReference(id)); found {
			c.Generation = gen
		}
	}
	return c
}

func (s *Service) persistLocked() {
	data, err := json.MarshalIndent(s.conns, "", "  ")
	if err != nil {
		slog.Error("programs: marshal connection state", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o700); err != nil {
		slog.Error("programs: connection state not saved; it is rebuilt from the store at the next check", "error", err)
		return
	}
	if err := fileutil.WriteFileAtomic(s.statePath, data, 0o600); err != nil {
		slog.Error("programs: connection state not saved; it is rebuilt from the store at the next check", "error", err)
	}
}

// Publish stores a new token for the entry as the next generation (FR-004,
// FR-043, FR-044). value must already be checked by ValidateTokenValue.
// expectedGeneration, when set, must equal the current generation. A card
// submission passes its receipt, which is written in the same store write.
// The entry reports checking and its check runs after the store lock is
// released; an entry without an account check reports saved_not_checked.
func (s *Service) Publish(id, value string, expectedGeneration *int64, receipt *credentials.CardReceipt) (Connection, error) {
	entry, found, err := Lookup(s.cfg(), id)
	if err != nil {
		return Connection{}, err
	}
	if !found {
		return Connection{}, ErrUnknownProgram
	}
	store, err := s.store()
	if err != nil {
		return Connection{}, fmt.Errorf("%w: %w", ErrSaveFailed, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.connectionLocked(id)
	if expectedGeneration != nil && *expectedGeneration != cur.Generation {
		return Connection{}, ErrStaleGeneration
	}
	gen, err := store.CommitProgramCredential(TokenReference(id), value, receipt)
	if err != nil {
		if errors.Is(err, credentials.ErrStoreBusy) {
			return Connection{}, ErrStoreBusy
		}
		return Connection{}, fmt.Errorf("%w: %w", ErrSaveFailed, err)
	}
	next := Connection{State: StateChecking, Generation: gen, CheckRevision: cur.CheckRevision}
	s.supersedeLocked(id)
	if entry.IdentityCheck == nil {
		next.State = StateSavedNotChecked
		next.Message = MsgNoCheck
		next.CheckedAt = time.Now().UTC()
		s.conns[id] = next
		s.persistLocked()
		return next, nil
	}
	s.conns[id] = next
	s.persistLocked()
	s.launchLocked(entry, next)
	return next, nil
}

// Disconnect removes the entry's token and advances the generation (FR-043).
// Switches on agents are kept. An entry with no stored token is unchanged.
func (s *Service) Disconnect(id string) (Connection, error) {
	if _, found, err := Lookup(s.cfg(), id); err != nil {
		return Connection{}, err
	} else if !found {
		return Connection{}, ErrUnknownProgram
	}
	store, err := s.store()
	if err != nil {
		return Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.supersedeLocked(id)
	err = store.RemoveProgramCredential(TokenReference(id))
	var notFound *credentials.NotFoundError
	switch {
	case errors.As(err, &notFound):
		return s.connectionLocked(id), nil
	case errors.Is(err, credentials.ErrStoreBusy):
		return Connection{}, ErrStoreBusy
	case err != nil:
		return Connection{}, err
	}
	gen, _ := store.ProgramEntryGeneration(TokenReference(id))
	next := Connection{State: StateNotConnected, Generation: gen, CheckRevision: s.conns[id].CheckRevision}
	s.conns[id] = next
	s.persistLocked()
	return next, nil
}

// Recheck runs the account check again for the current generation
// (POST …/check). The generation is unchanged.
func (s *Service) Recheck(id string) (Connection, error) {
	entry, found, err := Lookup(s.cfg(), id)
	if err != nil {
		return Connection{}, err
	}
	if !found {
		return Connection{}, ErrUnknownProgram
	}
	store, err := s.store()
	if err != nil {
		return Connection{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := store.ProgramEntryGeneration(TokenReference(id)); !ok {
		return Connection{}, ErrNotConnected
	}
	if _, gerr := store.Get(TokenReference(id)); gerr != nil {
		return Connection{}, ErrNotConnected
	}
	cur := s.connectionLocked(id)
	s.supersedeLocked(id)
	if entry.IdentityCheck == nil {
		cur.State, cur.Message, cur.AccountLabel = StateSavedNotChecked, MsgNoCheck, ""
		cur.CheckedAt = time.Now().UTC()
		s.conns[id] = cur
		s.persistLocked()
		return cur, nil
	}
	cur.State, cur.Message, cur.AccountLabel = StateChecking, "", ""
	s.conns[id] = cur
	s.persistLocked()
	s.launchLocked(entry, cur)
	return cur, nil
}

// ResumeChecks starts a fresh check for every entry left in checking by a
// previous run (S-115: a check interrupted by a restart runs again).
func (s *Service) ResumeChecks() {
	entries, err := Entries(s.cfg())
	if err != nil {
		slog.Error("programs: catalog unreadable; interrupted checks not resumed", "error", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		c, ok := s.conns[e.ID]
		if !ok || c.State != StateChecking || e.IdentityCheck == nil {
			continue
		}
		s.launchLocked(e, s.connectionLocked(e.ID))
	}
}

// ForgetEntry drops the connection state of a deleted entry.
func (s *Service) ForgetEntry(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.supersedeLocked(id)
	delete(s.conns, id)
	s.persistLocked()
}

// supersedeLocked stops the entry's running check; its result is discarded.
func (s *Service) supersedeLocked(id string) {
	if r := s.checks[id]; r != nil {
		r.cancel()
		delete(s.checks, id)
	}
}

// launchLocked reserves a check for conn's generation and starts it. Only
// the current reservation, with an unchanged generation and check revision,
// may publish its result (FR-047).
func (s *Service) launchLocked(entry ProgramEntry, conn Connection) {
	s.nextID++
	timeout := time.Duration(checkTimeoutSeconds(entry)) * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	r := &reservation{id: s.nextID, generation: conn.Generation, revision: conn.CheckRevision, cancel: cancel}
	s.checks[entry.ID] = r
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer cancel()
		result := s.runCheck(ctx, entry, timeout)
		s.finish(entry.ID, r, result)
	}()
}

// finish publishes a check result if its reservation is still current.
func (s *Service) finish(id string, r *reservation, res checkResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.checks[id]; cur != r {
		return // superseded: result discarded
	}
	delete(s.checks, id)
	c := s.connectionLocked(id)
	if c.Generation != r.generation || c.CheckRevision != r.revision || c.State != StateChecking {
		return
	}
	c.State, c.Message, c.AccountLabel = res.state, res.message, res.label
	c.CheckedAt = time.Now().UTC()
	s.conns[id] = c
	s.persistLocked()
}

func checkTimeoutSeconds(e ProgramEntry) int {
	if e.IdentityCheck == nil || e.IdentityCheck.TimeoutSeconds <= 0 {
		return DefaultCheckTimeoutSeconds
	}
	if e.IdentityCheck.TimeoutSeconds > MaxCheckTimeoutSeconds {
		return MaxCheckTimeoutSeconds
	}
	return e.IdentityCheck.TimeoutSeconds
}
