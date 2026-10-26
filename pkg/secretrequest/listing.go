// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// ReferenceConsumers maps every reference name the configuration uses to
// value-free labels of where it is used: any string field named *_ref and
// every value of an env_refs map, labelled by its configuration path (array
// elements by their provider/name/id when they have one).
func ReferenceConsumers(cfg *config.Config) map[string][]string {
	out := map[string][]string{}
	if cfg == nil {
		return out
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return out
	}
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		return out
	}
	var walk func(node any, path string, inRefs bool)
	walk = func(node any, path string, inRefs bool) {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				child := typed[key]
				label := key
				if path != "" {
					label = path + "." + key
				}
				if value, ok := child.(string); ok && value != "" && (inRefs || strings.HasSuffix(key, "_ref")) {
					out[value] = append(out[value], label)
					continue
				}
				walk(child, label, key == "env_refs")
			}
		case []any:
			for i, child := range typed {
				walk(child, path+"."+elementLabel(child, i), false)
			}
		}
	}
	walk(tree, "", false)
	return out
}

// elementLabel names an array element by its provider, name or id field.
func elementLabel(node any, index int) string {
	if fields, ok := node.(map[string]any); ok {
		for _, key := range []string{"provider", "name", "id"} {
			if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return "[" + strconv.Itoa(index) + "]"
}

// UnboundRecipientLine is the one fixed display line of an unbound entry.
const UnboundRecipientLine = "Stored in Settings; no recipient recorded"

// NameEntries builds the value-free K6 entries for a store inventory. Private
// versions are selected or superseded by the store's selection (FR-086). An
// integration version is selected when configuration references it,
// superseded when a newer version of the same destination is referenced, and
// stored otherwise (FR-072). A plain (Settings-stored) entry has no recorded
// recipient and is listed as kind unbound (architect WU-6 K6 ruling).
func NameEntries(cfg *config.Config, home string, inventory []credentials.CardInventoryEntry) []gen.SecretNameEntry {
	consumers := ReferenceConsumers(cfg)
	entries := make([]gen.SecretNameEntry, 0, len(inventory))
	for _, item := range inventory {
		entries = append(entries, NameEntry(cfg, home, item, inventory, consumers))
	}
	return entries
}

// NameEntry builds one K6 entry.
func NameEntry(cfg *config.Config, home string, item credentials.CardInventoryEntry, all []credentials.CardInventoryEntry, consumers map[string][]string) gen.SecretNameEntry {
	used := append([]string{}, consumers[item.Reference]...)
	entry := gen.SecretNameEntry{Reference: item.Reference, Version: int64(item.Secret.Version), Consumers: used}
	if item.Plain {
		// Architect WU-6 K6 ruling: no recipient recorded → kind unbound,
		// version 1, one fixed line; where it is used is stated only by
		// consumers. Never infer a kind.
		entry.Kind = gen.SecretNameEntryKindUnbound
		entry.Recipient.Kind = gen.SecretNameEntryRecipientKindUnbound
		entry.Recipient.Lines = []string{UnboundRecipientLine}
		entry.Version = 1
		entry.State = gen.SecretNameEntryStateStored
		if len(used) > 0 {
			entry.State = gen.SecretNameEntryStateSelected
		}
		return entry
	}
	recipient := item.Secret.Recipient
	entry.Kind = gen.SecretNameEntryKind(recipient.Kind)
	entry.Recipient.Kind = gen.SecretNameEntryRecipientKind(recipient.Kind)
	entry.Recipient.Lines = DisplayLines(cfg, home, recipient)
	if recipient.Kind == credentials.KindAgentPrivate {
		entry.Owner = &struct {
			AgentId     string `json:"agent_id"`
			WorkspaceId string `json:"workspace_id"`
		}{AgentId: recipient.AgentID, WorkspaceId: recipient.WorkspaceID}
		available := item.Options.AvailableInChannelTurns
		entry.AvailableInChannelTurns = &available
		entry.State = gen.SecretNameEntryStateSuperseded
		if item.Selected {
			entry.State = gen.SecretNameEntryStateSelected
		}
		return entry
	}
	entry.State = gen.SecretNameEntryStateStored
	if len(used) > 0 {
		entry.State = gen.SecretNameEntryStateSelected
		return entry
	}
	for _, other := range all {
		if other.Plain || other.Reference == item.Reference || other.Secret.Version <= item.Secret.Version {
			continue
		}
		if sameDestination(other.Secret.Recipient, recipient) && len(consumers[other.Reference]) > 0 {
			entry.State = gen.SecretNameEntryStateSuperseded
			break
		}
	}
	return entry
}

func sameDestination(a, b credentials.CardRecipient) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return bytes.Equal(ra, rb)
}
