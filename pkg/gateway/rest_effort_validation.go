// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_effort_validation.go — the one effort-value check every REST writer
// runs on its raw body before any decode or write (effort-levels spec FR-060,
// S-56, S-94): every member named reasoning_effort or recap_reasoning_effort,
// at any depth, must be a string of at most 32 characters. null is refused
// with the spec sentence rather than read as "keep" or "clear", so a refusal
// saves nothing. REST writers never check the value against the catalog.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
)

const (
	restEffortNullMessage   = `reasoning_effort must be a string; send "" to clear it`
	restEffortLengthMessage = "reasoning_effort must be at most 32 characters"
	restEffortMaxChars      = 32
)

func isRESTEffortMember(name string) bool {
	return name == "reasoning_effort" || name == "recap_reasoning_effort"
}

// effortBodyRefusal returns the refusal message and the offending member path
// for the first invalid effort member in raw, or "" when every effort member
// is valid. A body that is not JSON is left to the writer's own decode.
func effortBodyRefusal(raw []byte) (msg, field string) {
	if !bytes.Contains(raw, []byte("reasoning_effort")) {
		return "", ""
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return "", ""
	}
	return walkEffortMembers(doc, "")
}

func walkEffortMembers(node any, path string) (string, string) {
	switch typed := node.(type) {
	case map[string]any:
		for name, sub := range typed {
			child := name
			if path != "" {
				child = path + "." + name
			}
			if isRESTEffortMember(name) {
				if msg := effortValueRefusal(name, sub); msg != "" {
					return msg, child
				}
				continue
			}
			if msg, field := walkEffortMembers(sub, child); msg != "" {
				return msg, field
			}
		}
	case []any:
		for i, sub := range typed {
			if msg, field := walkEffortMembers(sub, fmt.Sprintf("%s[%d]", path, i)); msg != "" {
				return msg, field
			}
		}
	}
	return "", ""
}

func effortValueRefusal(name string, v any) string {
	if v == nil {
		return restEffortNullMessage
	}
	s, ok := v.(string)
	if !ok {
		// The strict string decode's own wording, naming the member.
		return fmt.Sprintf("%s: json: cannot unmarshal %s into Go value of type string", name, jsonKindName(v))
	}
	if utf8.RuneCountInString(s) > restEffortMaxChars {
		return restEffortLengthMessage
	}
	return ""
}

// effortRecordRefusal is the effort rule for a package agent record
// (#1952). The record is decoded into config.AgentConfig, which accepts any
// letter case of a member name (bytes.EqualFold) and keeps the last of
// several spellings, so every member whose name folds to an effort name is
// checked, at any depth and in every position: no value the typed decode
// could store escapes the check, and an invalid member is refused even when a
// later valid spelling would override it. Member names are read unescaped
// from the token stream. A record that is not valid JSON is left to the
// typed decode, which refuses it. The REST body rule above is unchanged.
func effortRecordRefusal(raw []byte) (msg, field string) {
	// json.Valid enforces the decoder's nesting limit before the recursive walk.
	if !json.Valid(raw) {
		return "", ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	msg, field, err := walkEffortTokens(dec, "")
	if err != nil {
		// Unreachable for valid JSON; refuse rather than pass unchecked.
		return "agent.json could not be read: " + err.Error(), "reasoning_effort"
	}
	return msg, field
}

func isRecordEffortMember(name string) bool {
	return strings.EqualFold(name, "reasoning_effort") || strings.EqualFold(name, "recap_reasoning_effort")
}

// walkEffortTokens reads one JSON value from dec and returns the first
// effort refusal inside it.
func walkEffortTokens(dec *json.Decoder, path string) (string, string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", "", err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return "", "", nil
	}
	for i := 0; dec.More(); i++ {
		child := fmt.Sprintf("%s[%d]", path, i)
		if delim == '{' {
			if tok, err = dec.Token(); err != nil {
				return "", "", err
			}
			name, _ := tok.(string)
			if child = name; path != "" {
				child = path + "." + name
			}
			if isRecordEffortMember(name) {
				var v any
				if err = dec.Decode(&v); err != nil {
					return "", "", err
				}
				if msg := effortValueRefusal(name, v); msg != "" {
					return msg, child, nil
				}
				continue
			}
		}
		msg, field, walkErr := walkEffortTokens(dec, child)
		if walkErr != nil || msg != "" {
			return msg, field, walkErr
		}
	}
	_, err = dec.Token()
	return "", "", err
}

func jsonKindName(v any) string {
	switch v.(type) {
	case json.Number:
		return "number"
	case bool:
		return "bool"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	}
	return fmt.Sprintf("%T", v)
}

// refuseInvalidEffortBody writes the HTTP 400 for an invalid effort member
// and reports whether it did. The response names the member in field.
func refuseInvalidEffortBody(w http.ResponseWriter, raw []byte) bool {
	msg, field := effortBodyRefusal(raw)
	if msg == "" {
		return false
	}
	jsonErrField(w, http.StatusBadRequest, msg, field)
	return true
}

// effortCatalog is the catalog the REST writers map against: the API's own
// when wired, else the process catalog.
func (a *restAPI) effortCatalog() *catalog.Catalog {
	if a != nil && a.providerCatalog != nil {
		return a.providerCatalog
	}
	return providers.ProviderCatalog()
}

// mapEffortOnModelChange maps a stored effort onto a new (provider, model)
// pair when a save changes the model without sending an effort (S-58). It
// returns the value to store, or a refusal sentence when the target cannot
// turn reasoning off. An unknown pair keeps the stored value as written
// (REST never catalog-checks a stored effort, S-55).
func mapEffortOnModelChange(cat *catalog.Catalog, stored, provider, model string) (string, string) {
	if stored == "" || cat == nil {
		return stored, ""
	}
	h := cat.Resolve(provider, model)
	if !h.Found() {
		return stored, ""
	}
	levels, sw, reasons := h.ServedReasoning()
	target := effortdialect.Target{Levels: levels, Switch: sw, Reasons: reasons}
	m := effortdialect.MapEffort(stored, target)
	if m.Blocked {
		opts := append([]string{effortdialect.ValueProviderDefault}, levels...)
		return "", fmt.Sprintf("%s can't turn reasoning off. Send reasoning_effort with one of: %s.",
			model, strings.Join(opts, ", "))
	}
	if effortdialect.IsOff(stored) && effortdialect.IsOff(m.Value) {
		return stored, ""
	}
	return m.Value, ""
}
