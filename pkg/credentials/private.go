// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"errors"
	"sort"
	"strings"
)

// KindAgentPrivate is the recipient kind of an agent-private variable: one
// variable owned by one agent in one workspace (FR-076, CRIT-001). The
// recipient's WorkspaceID, AgentID and Field (the variable name) are the scope.
const KindAgentPrivate = "agent_private"

// PrivateOptions are the per-variable options of an agent-private secret.
type PrivateOptions struct {
	// AvailableInChannelTurns injects the variable into turns triggered by a
	// channel person (FR-241). Default false.
	AvailableInChannelTurns bool `json:"available_in_channel_turns"`
}

// ErrCardSecretNotFound means the reference is not a stored card secret.
var ErrCardSecretNotFound = errors.New("secret reference not found")

// ErrNotPrivateSecret means the reference is not an agent-private secret.
var ErrNotPrivateSecret = errors.New("secret reference is not an agent-private secret")

// privateKey identifies one private variable by its (workspace, agent,
// variable) scope. The unit separator cannot occur in any of the three parts.
func privateKey(r CardRecipient) string {
	return r.WorkspaceID + "\x1f" + r.AgentID + "\x1f" + r.Field
}

// CardInventoryEntry is one stored secret version, value-free.
type CardInventoryEntry struct {
	Reference string
	Secret    CardSecret
	// Plain marks an entry stored outside a secret card (Settings, boot
	// migration): it has no recorded recipient and Secret.Version is 1.
	// Selected is meaningful for the private kind only: whether this version
	// is the one injected into its owner pair's children.
	Selected bool
	// Options is set for the private kind only.
	Options PrivateOptions
	Plain   bool
}

// CardInventory lists every stored secret version — card-stored versions
// with their recipient and private selection state, and plain entries — sorted
// by reference. It never decrypts a value.
func (s *Store) CardInventory() ([]CardInventoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return nil, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return nil, err
	}
	out := make([]CardInventoryEntry, 0, len(sf.CardSecrets))
	for ref, secret := range sf.CardSecrets {
		entry := CardInventoryEntry{Reference: ref, Secret: secret}
		if secret.Recipient.Kind == KindAgentPrivate {
			key := privateKey(secret.Recipient)
			entry.Selected = sf.PrivateSelected[key] == ref
			entry.Options = sf.PrivateOptions[key]
		}
		out = append(out, entry)
	}
	for ref := range sf.Credentials {
		if _, card := sf.CardSecrets[ref]; card {
			continue
		}
		// A plain row's version is 1: only a metadata row records versions,
		// never the name's shape (architect WU-6 K6 ruling).
		out = append(out, CardInventoryEntry{Reference: ref, Plain: true, Secret: CardSecret{Version: 1}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reference < out[j].Reference })
	return out, nil
}

// RemoveReference durably deletes one stored version — its encrypted value,
// its card recipient binding and, for a private variable, its selection when
// it was the selected version — in one publication (FR-077). Other versions
// of the same name and every receipt are untouched.
func (s *Store) RemoveReference(ref string) (CardInventoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return CardInventoryEntry{}, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return CardInventoryEntry{}, err
	}
	secret, card := sf.CardSecrets[ref]
	if _, stored := sf.Credentials[ref]; !stored && !card {
		return CardInventoryEntry{}, &NotFoundError{Name: ref}
	}
	entry := CardInventoryEntry{Reference: ref, Secret: secret, Plain: !card}
	if !card {
		entry.Secret.Version = 1
	}
	delete(sf.Credentials, ref)
	delete(sf.CardSecrets, ref)
	if card && secret.Recipient.Kind == KindAgentPrivate {
		key := privateKey(secret.Recipient)
		if sf.PrivateSelected[key] == ref {
			entry.Selected = true
			delete(sf.PrivateSelected, key)
		}
	}
	if err := s.saveFileNoLock(sf); err != nil {
		return CardInventoryEntry{}, err
	}
	return entry, nil
}

// SetPrivateChannelAvailability sets FR-241's option for the variable that
// ref is a version of. The option belongs to the variable, so every version
// of it reports the same value.
func (s *Store) SetPrivateChannelAvailability(ref string, available bool) (CardInventoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return CardInventoryEntry{}, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return CardInventoryEntry{}, err
	}
	secret, ok := sf.CardSecrets[ref]
	if !ok {
		return CardInventoryEntry{}, ErrCardSecretNotFound
	}
	if secret.Recipient.Kind != KindAgentPrivate {
		return CardInventoryEntry{}, ErrNotPrivateSecret
	}
	key := privateKey(secret.Recipient)
	if sf.PrivateOptions == nil {
		sf.PrivateOptions = make(map[string]PrivateOptions)
	}
	options := sf.PrivateOptions[key]
	options.AvailableInChannelTurns = available
	sf.PrivateOptions[key] = options
	if err := s.saveFileNoLock(sf); err != nil {
		return CardInventoryEntry{}, err
	}
	return CardInventoryEntry{Reference: ref, Secret: secret, Selected: sf.PrivateSelected[key] == ref, Options: options}, nil
}

// SelectPrivateVersion makes ref the selected version of its private
// variable (Omnipus's rollback, FR-086). Values and other versions are kept.
func (s *Store) SelectPrivateVersion(ref string) (CardInventoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		return CardInventoryEntry{}, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return CardInventoryEntry{}, err
	}
	secret, ok := sf.CardSecrets[ref]
	if !ok {
		return CardInventoryEntry{}, ErrCardSecretNotFound
	}
	if secret.Recipient.Kind != KindAgentPrivate {
		return CardInventoryEntry{}, ErrNotPrivateSecret
	}
	entry, stored := sf.Credentials[ref]
	if !stored {
		return CardInventoryEntry{}, ErrCardSecretNotFound
	}
	// Metadata presence does not prove the value is usable: a version that
	// cannot be decrypted is refused here, keeping the current selection,
	// rather than displacing a readable one (gate r1 SFH T2-SF-05).
	if _, err := decrypt(s.key, ref, entry); err != nil {
		return CardInventoryEntry{}, err
	}
	key := privateKey(secret.Recipient)
	if sf.PrivateSelected == nil {
		sf.PrivateSelected = make(map[string]string)
	}
	sf.PrivateSelected[key] = ref
	if err := s.saveFileNoLock(sf); err != nil {
		return CardInventoryEntry{}, err
	}
	return CardInventoryEntry{Reference: ref, Secret: secret, Selected: true, Options: sf.PrivateOptions[key]}, nil
}

// PrivateVariableError reports private variables of a pair whose selected
// value could not be read. Other variables are still returned (per-entry
// degradation); the names are safe to show, the causes are not.
type PrivateVariableError struct {
	Variables []string
	Errs      []error
}

func (e *PrivateVariableError) Error() string {
	return "private variables could not be read: " + strings.Join(e.Variables, ", ")
}

func (e *PrivateVariableError) Unwrap() []error { return e.Errs }

// PrivateValue is one selected private variable: its name and value. The
// value must only ever be placed into its owner pair's own child processes.
type PrivateValue struct {
	Name  string
	Value string
}

// PrivateEnvironment returns the selected private variables of
// (workspaceID, agentID), sorted by name. A channel-triggered turn receives
// only variables marked available in channel turns (FR-241).
func (s *Store) PrivateEnvironment(workspaceID, agentID string, channelTurn bool) ([]PrivateValue, error) {
	if workspaceID == "" || agentID == "" {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.key == nil {
		return nil, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return nil, err
	}
	var env []PrivateValue
	failures := &PrivateVariableError{}
	for key, ref := range sf.PrivateSelected {
		secret, ok := sf.CardSecrets[ref]
		if !ok || secret.Recipient.Kind != KindAgentPrivate || privateKey(secret.Recipient) != key {
			continue
		}
		if secret.Recipient.WorkspaceID != workspaceID || secret.Recipient.AgentID != agentID {
			continue
		}
		if channelTurn && !sf.PrivateOptions[key].AvailableInChannelTurns {
			continue
		}
		entry, stored := sf.Credentials[ref]
		if !stored {
			failures.Variables = append(failures.Variables, secret.Recipient.Field)
			failures.Errs = append(failures.Errs, &NotFoundError{Name: ref})
			continue
		}
		value, decryptErr := decrypt(s.key, ref, entry)
		if decryptErr != nil {
			failures.Variables = append(failures.Variables, secret.Recipient.Field)
			failures.Errs = append(failures.Errs, decryptErr)
			continue
		}
		env = append(env, PrivateValue{Name: secret.Recipient.Field, Value: value})
	}
	sort.Slice(env, func(i, j int) bool { return env[i].Name < env[j].Name })
	if len(failures.Variables) > 0 {
		sort.Strings(failures.Variables)
		return env, failures
	}
	return env, nil
}
