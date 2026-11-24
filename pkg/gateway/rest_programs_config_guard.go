// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// The Programs policy guard at the REST common commit (connected-programs
// FR-009, FR-028): the "programs" section and every channel's
// programs_allowed switch decide which turns may use program tokens, and only
// a signed-in person changes them, through the dedicated Programs routes.
// Every raw-document config save runs this guard on the decoded before/after
// pair, so a generic writer (PUT /api/v1/config with a bearer, or any other
// caller of safeUpdateConfigJSON) cannot change them, whatever key spelling,
// ancestor replacement or whole-channel replacement carries the change.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// programsProtectedError is a generic save's refused Programs change.
type programsProtectedError struct{ field string }

func (e *programsProtectedError) Error() string {
	return e.field + " can only be changed by a signed-in person in Settings → Programs or Connectors"
}

// guardedProgramsMutate wraps mutate so the Programs policy guard runs on its
// result. dedicated is true only while a dedicated, person-authorized Programs
// writer saves (safeUpdateProgramsConfigJSON); such a save may change both
// controls. Any other save:
//   - must leave the programs section as it was (refused otherwise);
//   - may switch a channel's programs_allowed off (or delete the channel), but
//     never on, and never keep it on for a newly created channel (refused);
//   - that rebinds a channel to another workspace or main agent switches its
//     programs_allowed off in the same write, as the dedicated rebind does,
//     so a switch never carries over to an agent the person did not choose.
func guardedProgramsMutate(mutate func(map[string]any) error, dedicated bool) func(map[string]any) error {
	return func(doc map[string]any) error {
		if dedicated {
			return mutate(doc)
		}
		before, err := decodeProgramsPolicyDoc(doc)
		if err != nil {
			return err
		}
		if err = mutate(doc); err != nil {
			return err
		}
		after, err := decodeProgramsPolicyDoc(doc)
		if err != nil {
			return err
		}
		beforeSection, err := json.Marshal(before.Programs)
		if err != nil {
			return fmt.Errorf("programs guard: serialize programs: %w", err)
		}
		afterSection, err := json.Marshal(after.Programs)
		if err != nil {
			return fmt.Errorf("programs guard: serialize programs: %w", err)
		}
		if !bytes.Equal(beforeSection, afterSection) {
			return &programsProtectedError{field: "programs"}
		}
		for id, inst := range after.Channels {
			if !inst.ProgramsAllowed {
				continue
			}
			prev, existed := before.Channels[id]
			if !existed || !prev.ProgramsAllowed {
				return &programsProtectedError{field: "channels." + id + ".programs_allowed"}
			}
			if channelBinding(prev) != channelBinding(inst) {
				if err := clearChannelProgramsAllowed(doc, id); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

// decodeProgramsPolicyDoc decodes the programs and channels sections of a raw
// config document the way config.json is loaded (encoding/json binds every
// key spelling).
func decodeProgramsPolicyDoc(doc map[string]any) (*config.Config, error) {
	section := map[string]any{}
	for _, name := range []string{"programs", "channels"} {
		for _, key := range foldedKeys(doc, name) {
			section[key] = doc[key]
		}
	}
	raw, err := json.Marshal(section)
	if err != nil {
		return nil, fmt.Errorf("programs guard: serialize sections: %w", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("programs guard: parse sections: %w", err)
	}
	return &cfg, nil
}

// channelBinding is the workspace and main agent a channel is bound to.
func channelBinding(inst config.ChannelInstanceConfig) string {
	kind, id := "", ""
	if inst.Identity != nil {
		kind, id = inst.Identity.Kind, inst.Identity.ID
	}
	return inst.WorkspaceID + "\x00" + kind + "\x00" + id
}

// clearChannelProgramsAllowed removes every key spelling of programs_allowed
// from the channel object id in the raw document.
func clearChannelProgramsAllowed(doc map[string]any, id string) error {
	cleared := false
	for _, key := range foldedKeys(doc, "channels") {
		channels, ok := doc[key].(map[string]any)
		if !ok {
			continue
		}
		inst, ok := channels[id].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range foldedKeys(inst, "programs_allowed") {
			delete(inst, field)
		}
		cleared = true
	}
	if !cleared {
		return fmt.Errorf("programs guard: channels.%s is not an object", id)
	}
	return nil
}

// safeUpdateProgramsConfigJSON is safeUpdateConfigJSON for the dedicated,
// person-authorized Programs writers (the Programs routes and PUT
// /channels/{id}/programs): only their saves may change the Programs controls.
func (a *restAPI) safeUpdateProgramsConfigJSON(mutate func(m map[string]any) error) error {
	store := a.pairingOrderStore()
	a.configMu.Lock()
	defer a.configMu.Unlock()
	a.programsDedicatedWrite = true
	defer func() { a.programsDedicatedWrite = false }()
	return a.orderedConfigJSONLocked(store, nil, nil, mutate)
}

// writeProgramsProtected answers a generic save the Programs guard refused
// with 403. It reports whether err was that refusal.
func writeProgramsProtected(w http.ResponseWriter, err error) bool {
	var refused *programsProtectedError
	if !errors.As(err, &refused) {
		return false
	}
	jsonErr(w, http.StatusForbidden, refused.Error())
	return true
}
