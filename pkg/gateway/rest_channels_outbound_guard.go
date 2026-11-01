package gateway

// The outbound guard at the REST common commit (channels-spec-r8 T-8,
// FR-014, FR-018): every raw-document config save runs config.GuardOutbound
// on the decoded before/candidate pair before the file is written, and the
// guard's result (cleared, restored or refused) is applied to the raw
// document, so the persisted bytes and the published live configuration both
// reflect it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// guardedConfigMutate wraps mutate so the outbound guard runs on its result.
// dedicated is true only for the dedicated outbound writer
// (applyOutboundChange, T-8 row 2).
func guardedConfigMutate(mutate func(map[string]any) error, dedicated bool) func(map[string]any) error {
	return func(doc map[string]any) error {
		before, err := decodeChannelsDoc(doc)
		if err != nil {
			return err
		}
		if err = mutate(doc); err != nil {
			return err
		}
		candidate, err := decodeChannelsDoc(doc)
		if err != nil {
			return err
		}
		changed, err := config.GuardOutbound(before, candidate, dedicated)
		if err != nil {
			return err
		}
		return applyGuardedOutbound(doc, candidate, changed)
	}
}

// decodeChannelsDoc decodes the channels section of a raw config document the
// way config.json is loaded (encoding/json binds every key spelling).
func decodeChannelsDoc(doc map[string]any) (*config.Config, error) {
	section := map[string]any{}
	for _, key := range foldedKeys(doc, "channels") {
		section[key] = doc[key]
	}
	raw, err := json.Marshal(section)
	if err != nil {
		return nil, fmt.Errorf("serialize channels: %w", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse channels: %w", err)
	}
	return &cfg, nil
}

// applyGuardedOutbound writes the guard's cleared/restored outbound values of
// the changed instances into the raw document, replacing every key spelling
// that would bind to the field.
func applyGuardedOutbound(doc map[string]any, candidate *config.Config, changed []string) error {
	if len(changed) == 0 {
		return nil
	}
	var channelsMap map[string]any
	for _, key := range foldedKeys(doc, "channels") {
		if m, ok := doc[key].(map[string]any); ok {
			channelsMap = m
		}
	}
	if channelsMap == nil {
		return errors.New("outbound guard: channels section is not an object")
	}
	for _, id := range changed {
		inst, ok := channelsMap[id].(map[string]any)
		if !ok {
			return fmt.Errorf("outbound guard: channels.%s is not an object", id)
		}
		for _, key := range foldedKeys(inst, "outbound") {
			delete(inst, key)
		}
		if outbound := candidate.Channels[id].Outbound; outbound != nil {
			raw, err := json.Marshal(outbound)
			if err != nil {
				return fmt.Errorf("outbound guard: serialize channels.%s.outbound: %w", id, err)
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("outbound guard: channels.%s.outbound: %w", id, err)
			}
			inst["outbound"] = value
		}
	}
	return nil
}

// foldedKeys returns every key of m that encoding/json would bind to name.
func foldedKeys(m map[string]any, name string) []string {
	want := jsonFoldKey(name)
	var keys []string
	for key := range m {
		if jsonFoldKey(key) == want {
			keys = append(keys, key)
		}
	}
	return keys
}

// writeOutboundProtected answers a generic save the guard refused (T-8 row 5)
// with 403 and the T-8 text. It reports whether err was that refusal.
func writeOutboundProtected(w http.ResponseWriter, err error) bool {
	var refused *config.OutboundProtectedError
	if !errors.As(err, &refused) {
		return false
	}
	jsonErr(w, http.StatusForbidden,
		"channels."+refused.InstanceID+".outbound can only be changed with PUT /api/v1/channels/{id}/outbound")
	return true
}
