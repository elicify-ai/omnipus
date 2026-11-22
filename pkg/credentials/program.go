// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrProgramOwned is returned by every ordinary mutating Store method when the
// target entry belongs to a connected program (connected-programs spec
// FR-048, invariant I-3). The caller-visible text, "<reference> belongs to the
// program <name>. Change it in Settings → Programs.", is built by the REST and
// tool layers, which know the program's name.
var ErrProgramOwned = errors.New("credential entry is owned by a connected program")

// ProgramOwnedError names the program-owned reference a guarded method
// refused. It unwraps to ErrProgramOwned.
type ProgramOwnedError struct{ Reference string }

func (e *ProgramOwnedError) Error() string {
	return fmt.Sprintf("credentials: %q is owned by a connected program", e.Reference)
}

func (e *ProgramOwnedError) Unwrap() error { return ErrProgramOwned }

// KindProgram is the card-secret recipient kind of a program-owned entry
// (invariant I-3: "A program-owned entry is a card secret with recipient kind
// program").
const KindProgram = "program"

// maxProgramValueBytes bounds a program token (spec token rules: at most
// 16,384 bytes). pkg/programs applies the full value rules first.
const maxProgramValueBytes = 16384

// programMeta is the stored metadata of one program-owned reference. The
// generation survives a disconnect, so the next publication continues from it
// (FR-043: every publication and disconnect changes the generation).
type programMeta struct {
	Generation int64 `json:"generation"`
}

// programOwnedLocked reports whether ref belongs to a connected program in sf:
// it has program metadata, or a card secret with recipient kind program.
func programOwnedLocked(sf *storeFile, ref string) bool {
	if _, ok := sf.ProgramEntries[ref]; ok {
		return true
	}
	secret, ok := sf.CardSecrets[ref]
	return ok && secret.Recipient.Kind == KindProgram
}

// CommitProgramCredential is the ONLY method that creates or replaces a
// program-owned credential entry (invariant I-3; only pkg/programs calls it).
// In one store transaction it writes the value, the card-secret binding with
// recipient kind program, the next generation and, when receipt is given,
// the CardReceipt carrying that generation (FR-051). It returns the new
// generation (1 on first publication).
func (s *Store) CommitProgramCredential(reference string, value string, receipt *CardReceipt) (int64, error) {
	if reference == "" {
		return 0, errors.New("credentials: program reference is empty")
	}
	if value == "" || len(value) > maxProgramValueBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return 0, errors.New("credentials: invalid program token value")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return 0, ErrStoreLocked
	}
	var gen int64
	err := s.transact(func(sf *storeFile) error {
		if secret, card := sf.CardSecrets[reference]; card && secret.Recipient.Kind != KindProgram {
			return fmt.Errorf("credentials: reference %q is already a %s secret", reference, secret.Recipient.Kind)
		}
		if receipt != nil {
			if receipt.RequestID == "" {
				return errors.New("credentials: program receipt has no request id")
			}
			if _, dup := sf.CardReceipts[receipt.RequestID]; dup {
				return ErrCardAlreadySubmitted
			}
		}
		entry, sealErr := encrypt(s.key, reference, []byte(value))
		if sealErr != nil {
			return fmt.Errorf("credentials: encrypt %q: %w", reference, sealErr)
		}
		if sf.ProgramEntries == nil {
			sf.ProgramEntries = make(map[string]programMeta)
		}
		if sf.CardSecrets == nil {
			sf.CardSecrets = make(map[string]CardSecret)
		}
		meta := sf.ProgramEntries[reference]
		meta.Generation++
		gen = meta.Generation
		sf.ProgramEntries[reference] = meta
		sf.Credentials[reference] = entry
		sf.CardSecrets[reference] = CardSecret{
			Recipient: CardRecipient{Kind: KindProgram, Field: reference},
			Version:   int(gen),
		}
		if receipt != nil {
			if sf.CardReceipts == nil {
				sf.CardReceipts = make(map[string]CardReceipt)
			}
			stored := *receipt
			stored.References = append([]string(nil), receipt.References...)
			stored.SubmittedAt = receipt.SubmittedAt.UTC()
			stored.ProgramGeneration = gen
			sf.CardReceipts[receipt.RequestID] = stored
		}
		return s.saveFileNoLock(sf)
	})
	if err != nil {
		return 0, err
	}
	return gen, nil
}

// RemoveProgramCredential is the ONLY method that removes a program-owned
// credential (invariant I-3; only pkg/programs calls it). It removes the value
// and its card-secret binding and advances the generation (FR-043), in one
// store transaction. Receipts stay. A reference with no program metadata is
// a *NotFoundError.
func (s *Store) RemoveProgramCredential(reference string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrStoreLocked
	}
	return s.transact(func(sf *storeFile) error {
		if !programOwnedLocked(sf, reference) {
			return &NotFoundError{Name: reference}
		}
		if sf.ProgramEntries == nil {
			sf.ProgramEntries = make(map[string]programMeta)
		}
		meta := sf.ProgramEntries[reference]
		meta.Generation++
		sf.ProgramEntries[reference] = meta
		delete(sf.Credentials, reference)
		delete(sf.CardSecrets, reference)
		return s.saveFileNoLock(sf)
	})
}

// ProgramEntryGeneration returns the stored generation of a program-owned
// reference and true, or 0 and false when the reference has no program
// metadata (or the store is locked or unreadable). Read-only.
func (s *Store) ProgramEntryGeneration(reference string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return 0, false
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return 0, false
	}
	meta, ok := sf.ProgramEntries[reference]
	if !ok {
		return 0, false
	}
	return meta.Generation, true
}
