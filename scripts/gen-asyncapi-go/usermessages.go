// usermessages.go — LLMError user-facing copy catalogue emitter.
//
// The AsyncAPI contract carries the user-facing message AND the fault
// attribution for every LLMError code, as two specification extensions on
// components.schemas.LLMError:
//
//	x-user-message-attributions: [model, provider, product, config, ambiguous, unknown]
//	x-user-messages:
//	  rate_limited:
//	    message: "…"
//	    attribution: provider
//
// This file turns that block into a Go catalogue
// (pkg/api/generated/llm_error_messages.gen.go) so pkg/agent/translate_error.go
// no longer hand-maintains the copy. The TypeScript half is emitted from the
// same block by scripts/_gen-asyncapi-types.mjs — the two catalogues cannot
// drift because neither is written by hand.
//
// Every validation below is FATAL. A missing message, a stray catalogue entry
// for a code that is not in the enum, an empty message, an attribution that
// is not in the declared vocabulary, or a provider_message template that uses
// a slot outside the closed vocabulary aborts codegen with a precise error
// rather than emitting a catalogue with a hole in it. That is what makes
// "every code has a message and an attribution" true by construction.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"sort"
	"strings"
)

// userMessageEntry is one row of the LLMError copy catalogue: a wire code, the
// user-facing sentence shown for it, the tag naming who owns the fault, and
// (optionally) the templated provider variant assembled when the failing
// attempt's facts are present.
type userMessageEntry struct {
	code            string
	message         string
	attribution     string
	providerMessage string
}

// userMessageCatalogue is the validated catalogue extracted from the contract.
// Codes and Entries are in contract (enum) order so the generated artifacts are
// deterministic and diff cleanly against the YAML.
type userMessageCatalogue struct {
	attributions []string
	entries      []userMessageEntry
}

const (
	// userMessagesKey / attributionsKey are the specification-extension keys
	// carrying the catalogue on components.schemas.LLMError.
	userMessagesKey = "x-user-messages"
	attributionsKey = "x-user-message-attributions"

	// providerMessageKey is the optional per-entry key carrying the templated
	// provider variant of the sentence (provider-messages spec §6, OBS-002).
	providerMessageKey = "provider_message"
)

// providerMessageSlots is the CLOSED slot vocabulary for provider_message
// templates (provider-messages spec §6). Both generators hard-fail on any
// other slot, so a template can never silently render a raw "{oops}".
var providerMessageSlots = map[string]bool{
	"{provider}":         true,
	"{model}":            true,
	"{attempt}":          true,
	"{max}":              true,
	"{countdown}":        true,
	"{answered_model}":   true,
	"{unavailable_model}": true,
}

// extractUserMessageCatalogue reads the copy catalogue off
// components.schemas.<schemaName> in the parsed AsyncAPI document and validates
// it against that schema's own `code` enum.
//
// Validation (all fatal):
//   - the schema, its code enum, and both extension keys must be present;
//   - the attribution vocabulary must be non-empty with no duplicates;
//   - every enum value must have exactly one catalogue entry (no gaps);
//   - every catalogue entry must name an enum value (no orphans);
//   - every message must be non-empty after trimming;
//   - every attribution must be drawn from the declared vocabulary.
func extractUserMessageCatalogue(doc map[string]any, schemaName string) (*userMessageCatalogue, error) {
	components, ok := doc["components"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing components section")
	}
	rawSchemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing components.schemas section")
	}
	rawSchema, ok := rawSchemas[schemaName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing components.schemas.%s", schemaName)
	}

	codes, err := codeEnumOf(rawSchema, schemaName)
	if err != nil {
		return nil, err
	}

	attributions, err := stringSlice(rawSchema[attributionsKey])
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", schemaName, attributionsKey, err)
	}
	if len(attributions) == 0 {
		return nil, fmt.Errorf("%s.%s: must declare at least one attribution", schemaName, attributionsKey)
	}
	allowed := make(map[string]bool, len(attributions))
	for _, a := range attributions {
		if allowed[a] {
			return nil, fmt.Errorf("%s.%s: duplicate attribution %q", schemaName, attributionsKey, a)
		}
		allowed[a] = true
	}

	rawMessages, ok := rawSchema[userMessagesKey].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: missing or malformed %s (want a mapping of code → {message, attribution})",
			schemaName, userMessagesKey)
	}

	entries := make([]userMessageEntry, 0, len(codes))
	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		rawEntry, ok := rawMessages[code].(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"%s.%s: no entry for code %q — every value of the code enum needs a message and an attribution",
				schemaName, userMessagesKey, code)
		}
		message, _ := rawEntry["message"].(string)
		if strings.TrimSpace(message) == "" {
			return nil, fmt.Errorf("%s.%s.%s: message must be a non-empty string",
				schemaName, userMessagesKey, code)
		}
		attribution, _ := rawEntry["attribution"].(string)
		if !allowed[attribution] {
			return nil, fmt.Errorf(
				"%s.%s.%s: attribution %q is not in %s (%s)",
				schemaName, userMessagesKey, code, attribution, attributionsKey,
				strings.Join(attributions, ", "))
		}
		providerMessage, _ := rawEntry[providerMessageKey].(string)
		if rawEntry[providerMessageKey] != nil && strings.TrimSpace(providerMessage) == "" {
			return nil, fmt.Errorf("%s.%s.%s: provider_message must be a non-empty string when present",
				schemaName, userMessagesKey, code)
		}
		if err := validateTemplateSlots(providerMessage); err != nil {
			return nil, fmt.Errorf("%s.%s.%s: %w", schemaName, userMessagesKey, code, err)
		}
		entries = append(entries, userMessageEntry{code: code, message: message, attribution: attribution, providerMessage: providerMessage})
		seen[code] = true
	}

	// Orphan check — a catalogue entry for a code that is not in the enum is
	// dead copy that no runtime path can ever reach.
	orphans := make([]string, 0)
	for code := range rawMessages {
		if !seen[code] {
			orphans = append(orphans, code)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		return nil, fmt.Errorf("%s.%s: entries for codes not in the enum: %s",
			schemaName, userMessagesKey, strings.Join(orphans, ", "))
	}

	return &userMessageCatalogue{attributions: attributions, entries: entries}, nil
}

// validateTemplateSlots enforces the closed slot vocabulary on a
// provider_message template: balanced braces and every {slot} drawn from
// providerMessageSlots. An empty template passes (the entry simply has no
// templated variant).
func validateTemplateSlots(t string) error {
	if t == "" {
		return nil
	}
	if strings.Count(t, "{") != strings.Count(t, "}") {
		return fmt.Errorf("provider_message has unbalanced braces (%d open, %d close)",
			strings.Count(t, "{"), strings.Count(t, "}"))
	}
	for _, slot := range templateSlots(t) {
		if !providerMessageSlots[slot] {
			return fmt.Errorf(
				"provider_message uses slot %q — not in the closed slot vocabulary (%s)",
				slot, slotVocabularyList())
		}
	}
	return nil
}

// templateSlots returns every {slot} token in a template, in order.
func templateSlots(t string) []string {
	var slots []string
	for i := 0; i < len(t); {
		start := strings.IndexByte(t[i:], '{')
		if start < 0 {
			break
		}
		rel := strings.IndexByte(t[i+start:], '}')
		if rel < 0 {
			break
		}
		slots = append(slots, t[i+start:i+start+rel+1])
		i += start + rel + 1
	}
	return slots
}

// slotVocabularyList renders the closed vocabulary for messages, sorted.
func slotVocabularyList() string {
	keys := make([]string, 0, len(providerMessageSlots))
	for k := range providerMessageSlots {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// codeEnumOf returns the `properties.code.enum` values of a raw schema map.
func codeEnumOf(rawSchema map[string]any, schemaName string) ([]string, error) {
	properties, ok := rawSchema["properties"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: missing properties", schemaName)
	}
	codeProp, ok := properties["code"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: missing properties.code", schemaName)
	}
	codes, err := stringSlice(codeProp["enum"])
	if err != nil {
		return nil, fmt.Errorf("%s.properties.code.enum: %w", schemaName, err)
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("%s.properties.code.enum: must be a non-empty list", schemaName)
	}
	return codes, nil
}

// stringSlice coerces a parsed YAML sequence into []string, rejecting any
// non-string element rather than silently dropping it.
func stringSlice(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a list, got %T", raw)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("element %d: expected a string, got %T", i, item)
		}
		out = append(out, s)
	}
	return out, nil
}

// generateUserMessages renders the Go catalogue source for cat.
func generateUserMessages(cat *userMessageCatalogue) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString("//go:build go1.22\n\n")
	buf.WriteString("// Code generated by scripts/gen-asyncapi-go. DO NOT EDIT.\n")
	buf.WriteString("// Source: contracts/asyncapi.yaml (components.schemas.LLMError)\n")
	buf.WriteString("//\n")
	buf.WriteString("// The user-facing copy and the fault attribution for every LLMError code are\n")
	buf.WriteString("// contract data, not code. Edit the x-user-messages block in\n")
	buf.WriteString("// contracts/components/schemas/LLMError.yaml (and its three sibling copies)\n")
	buf.WriteString("// and re-run `make gen-contracts`. The TypeScript half of this catalogue,\n")
	buf.WriteString("// src/lib/api/generated/llm-error-messages.ts, is emitted from the same block.\n")
	buf.WriteString("//\n")
	buf.WriteString("// Regenerate with:\n")
	buf.WriteString("//   cd scripts/gen-asyncapi-go &&\n")
	buf.WriteString("//   go run . ../../contracts/asyncapi.yaml \\\n")
	buf.WriteString("//     ../../pkg/api/generated/asyncapi_types.gen.go \\\n")
	buf.WriteString("//     ../../pkg/api/generated/llm_error_messages.gen.go\n")
	buf.WriteString("\n")
	buf.WriteString("package generated\n\n")

	buf.WriteString("// LLMErrorAttribution names who owns the fault behind an LLMError code, so a\n")
	buf.WriteString("// reader (and a test) can tell an upstream failure from one Omnipus caused.\n")
	buf.WriteString("type LLMErrorAttribution string\n\n")

	buf.WriteString("// Defines values for LLMErrorAttribution.\n")
	buf.WriteString("const (\n")
	for _, a := range cat.attributions {
		fmt.Fprintf(&buf, "\tLLMErrorAttribution%s LLMErrorAttribution = %q\n", toPascalCase(a), a)
	}
	buf.WriteString(")\n\n")

	buf.WriteString("// LLMErrorAttributionValues is the closed attribution vocabulary, in contract order.\n")
	buf.WriteString("var LLMErrorAttributionValues = []LLMErrorAttribution{\n")
	for _, a := range cat.attributions {
		fmt.Fprintf(&buf, "\tLLMErrorAttribution%s,\n", toPascalCase(a))
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// LLMErrorCodes lists every LLMError code, in contract (enum) order.\n")
	buf.WriteString("var LLMErrorCodes = []string{\n")
	for _, e := range cat.entries {
		fmt.Fprintf(&buf, "\t%q,\n", e.code)
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// LLMErrorUserMessages maps every LLMError code to the sentence a user sees.\n")
	buf.WriteString("// Exhaustive by construction: codegen aborts if any code lacks an entry.\n")
	buf.WriteString("var LLMErrorUserMessages = map[string]string{\n")
	for _, e := range cat.entries {
		fmt.Fprintf(&buf, "\t%q: %q,\n", e.code, e.message)
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// LLMErrorUserAttributions maps every LLMError code to its fault attribution.\n")
	buf.WriteString("// Exhaustive by construction, same as LLMErrorUserMessages.\n")
	buf.WriteString("var LLMErrorUserAttributions = map[string]LLMErrorAttribution{\n")
	for _, e := range cat.entries {
		fmt.Fprintf(&buf, "\t%q: LLMErrorAttribution%s,\n", e.code, toPascalCase(e.attribution))
	}
	buf.WriteString("}\n\n")

	buf.WriteString("// LLMErrorProviderMessages maps the codes that carry a templated variant to\n")
	buf.WriteString("// that variant (provider-messages spec §6, OBS-002). A code absent from this\n")
	buf.WriteString("// map has no template — the catalogue message is the only copy. Slots come\n")
	buf.WriteString("// from the closed vocabulary enforced above; the TypeScript half is\n")
	buf.WriteString("// llmErrorProviderMessages in src/lib/api/generated/llm-error-messages.ts.\n")
	buf.WriteString("var LLMErrorProviderMessages = map[string]string{\n")
	for _, e := range cat.entries {
		if e.providerMessage != "" {
			fmt.Fprintf(&buf, "\t%q: %q,\n", e.code, e.providerMessage)
		}
	}
	buf.WriteString("}\n")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt the generated catalogue: %w", err)
	}
	return formatted, nil
}
