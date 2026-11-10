// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// CardRecipient is the exact, value-free destination consented to on a card.
// It is internal credential metadata, never a wire-format payload.
type CardRecipient struct {
	Kind        string   `json:"kind"`
	Provider    string   `json:"provider,omitempty"`
	Endpoint    string   `json:"endpoint,omitempty"`
	Protocol    string   `json:"protocol,omitempty"`
	Server      string   `json:"server,omitempty"`
	Transport   string   `json:"transport,omitempty"`
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	URL         string   `json:"url,omitempty"`
	Instance    string   `json:"instance,omitempty"`
	ChannelType string   `json:"channel_type,omitempty"`
	Account     string   `json:"account,omitempty"`
	WorkspaceID string   `json:"workspace_id,omitempty"`
	AgentID     string   `json:"agent_id,omitempty"`
	Field       string   `json:"field"`
}

// CardSecret records a reference's destination, never its value.
type CardSecret struct {
	Recipient CardRecipient `json:"recipient"`
	Version   int           `json:"version"`
}

// CardReceipt is published in the SAME credentials.json write as its values.
// It is the commit fact when session metadata could not be updated (FR-087).
type CardReceipt struct {
	RequestID   string    `json:"request_id"`
	SessionID   string    `json:"session_id"`
	References  []string  `json:"references"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// CardValue is a transient write input. It must never be logged or persisted
// outside the encrypted credential envelope.
type CardValue struct {
	BaseName  string
	Recipient CardRecipient
	Value     string
}

var ErrCardAlreadySubmitted = errors.New("secret request was already submitted")

// CommitCard stores all values, their destination bindings and the submit
// receipt in one atomic publication. A retry never overwrites an old version.
func (s *Store) CommitCard(requestID, sessionID string, values []CardValue, at time.Time) (CardReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return CardReceipt{}, ErrStoreLocked
	}
	if requestID == "" || sessionID == "" || len(values) < 1 || len(values) > 8 {
		return CardReceipt{}, errors.New("invalid secret submission")
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return CardReceipt{}, err
	}
	if receipt, ok := sf.CardReceipts[requestID]; ok {
		return receipt, ErrCardAlreadySubmitted
	}
	if sf.CardSecrets == nil {
		sf.CardSecrets = make(map[string]CardSecret)
	}
	if sf.CardReceipts == nil {
		sf.CardReceipts = make(map[string]CardReceipt)
	}
	receipt := CardReceipt{RequestID: requestID, SessionID: sessionID, SubmittedAt: at.UTC(), References: make([]string, 0, len(values))}
	for _, item := range values {
		if item.BaseName == "" || len(item.Value) < 4 || len(item.Value) > 16384 || !utf8.ValidString(item.Value) || strings.ContainsRune(item.Value, 0) {
			return CardReceipt{}, errors.New("invalid secret value")
		}
		if sf.CardHighWater == nil {
			sf.CardHighWater = make(map[string]int)
		}
		// Never reuse a name: start above the highest version ever issued
		// for this family, not only above the versions still stored.
		version := sf.CardHighWater[item.BaseName] + 1
		for name := range sf.Credentials {
			if strings.HasPrefix(name, item.BaseName+".v") {
				var existing int
				if _, scanErr := fmt.Sscanf(strings.TrimPrefix(name, item.BaseName+".v"), "%d", &existing); scanErr == nil && existing >= version {
					version = existing + 1
				}
			}
		}
		sf.CardHighWater[item.BaseName] = version
		ref := fmt.Sprintf("%s.v%d", item.BaseName, version)
		entry, sealErr := encrypt(s.key, ref, []byte(item.Value))
		if sealErr != nil {
			return CardReceipt{}, errors.New("could not encrypt secret values")
		}
		sf.Credentials[ref] = entry
		sf.CardSecrets[ref] = CardSecret{Recipient: item.Recipient, Version: version}
		if item.Recipient.Kind == KindAgentPrivate {
			// Q7 A / FR-086: every version submitted on the owner pair's card
			// is selected on submit, in this same publication; the store
			// mutex makes the last committed submit the selected one.
			if sf.PrivateSelected == nil {
				sf.PrivateSelected = make(map[string]string)
			}
			sf.PrivateSelected[privateKey(item.Recipient)] = ref
		}
		receipt.References = append(receipt.References, ref)
	}
	sf.CardReceipts[requestID] = receipt
	if err := s.saveFileNoLock(sf); err != nil {
		return CardReceipt{}, err
	}
	return receipt, nil
}

// CardReceipt returns the value-free commit fact without resolving a value.
func (s *Store) CardReceipt(requestID string) (CardReceipt, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return CardReceipt{}, false, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return CardReceipt{}, false, err
	}
	receipt, ok := sf.CardReceipts[requestID]
	return receipt, ok, nil
}

// PruneCardReceipts removes receipts only after their session terminal metadata
// has been pruned. The encrypted secret and its recipient binding do not expire.
func (s *Store) PruneCardReceipts(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	changed := false
	for _, id := range ids {
		if _, ok := sf.CardReceipts[id]; ok {
			delete(sf.CardReceipts, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveFileNoLock(sf)
}

// PruneOrphanCardReceipts reconciles receipt metadata against a complete,
// successfully read request-state inventory. Values, versions and recipient
// bindings are deliberately untouched; an unchanged inventory performs no write.
func (s *Store) PruneOrphanCardReceipts(keep map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return err
	}
	changed := false
	for id, receipt := range sf.CardReceipts {
		if sessionID, exists := keep[id]; exists {
			if sessionID != receipt.SessionID {
				return errors.New("secret receipt session binding does not match saved request")
			}
			continue
		}
		delete(sf.CardReceipts, id)
		changed = true
	}
	if !changed {
		return nil
	}
	return s.saveFileNoLock(sf)
}

// CardSecretMetadata returns internal, value-free reference bindings.
func (s *Store) CardSecretMetadata() (map[string]CardSecret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return nil, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return nil, err
	}
	return maps.Clone(sf.CardSecrets), nil
}

// SensitiveValues includes every readable stored value, not only configured
// references. Unselected and superseded card values must remain masked too.
// A decryptable witness makes failures in other entries a DamagedEntriesError;
// locked, unreadable and wholly undecryptable stores never acquire that scope.
func (s *Store) SensitiveValues() ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return nil, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sf.Credentials))
	for name := range sf.Credentials {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, 0, len(names))
	failures := &DamagedEntriesError{}
	for _, name := range names {
		value, decryptErr := decrypt(s.key, name, sf.Credentials[name])
		if decryptErr != nil {
			failures.Names = append(failures.Names, name)
			failures.Errs = append(failures.Errs, decryptErr)
			continue
		}
		values = append(values, value)
		if strings.HasSuffix(name, "_OAUTH") {
			var grant map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &grant) == nil {
				for _, field := range []string{"access_token", "refresh_token"} {
					var token string
					if json.Unmarshal(grant[field], &token) == nil && token != "" {
						values = append(values, token)
					}
				}
			}
		}
	}
	if len(failures.Errs) > 0 && len(values) > 0 {
		return values, failures
	}
	return values, errors.Join(failures.Errs...)
}
