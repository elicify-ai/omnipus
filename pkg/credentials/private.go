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

// CardInventoryEntry is one stored card secret version, value-free.
type CardInventoryEntry struct {
	Reference string
	Secret    CardSecret
	// Selected is meaningful for the private kind only: whether this version
	// is the one injected into its owner pair's children.
	Selected bool
	// Options is set for the private kind only.
	Options PrivateOptions
}

// CardInventory lists every stored card secret version with its private
// selection state, sorted by reference. It never decrypts a value.
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
	sort.Slice(out, func(i, j int) bool { return out[i].Reference < out[j].Reference })
	return out, nil
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
	if _, stored := sf.Credentials[ref]; !stored {
		return CardInventoryEntry{}, ErrCardSecretNotFound
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

// PrivateEnvironment returns "NAME=value" entries for the selected private
// variables of (workspaceID, agentID), sorted by name. A channel-triggered
// turn receives only variables marked available in channel turns (FR-241).
// The values must only ever be placed into that pair's own child processes.
func (s *Store) PrivateEnvironment(workspaceID, agentID string, channelTurn bool) ([]string, error) {
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
	var env []string
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
		env = append(env, secret.Recipient.Field+"="+value)
	}
	sort.Strings(env)
	if len(failures.Variables) > 0 {
		sort.Strings(failures.Variables)
		return env, failures
	}
	return env, nil
}
