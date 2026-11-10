package effortdialect

import (
	"strings"
)

// Reserved stored effort words (effort-levels spec FR-044).
const (
	ValueProviderDefault = "provider_default"
	ValueOn              = "on"
	ValueOff             = "off"
	ValueNone            = "none"
)

// Record reasons an adapter can report (EffortRecord.reason).
const (
	ReasonNotSupported = "not_supported"
	ReasonContinuation = "continuation"
	ReasonExtraBody    = "extra_body"
)

// ReportOptionKey is the request option carrying a *Sent the adapter fills
// with what its final serialised body actually carried (FR-058: EffortSent is
// computed in each adapter after final shaping — extra_body and guards).
const ReportOptionKey = "reasoning_effort_report"

// Sent is what one request's final body carried for effort. Value is the
// effort value in the body ("" = nothing sent); Reason explains a difference
// the adapter itself caused ("continuation", "extra_body", "not_supported").
type Sent struct {
	Value  string
	Reason string
	// Provider is the catalog provider id of the route that built the body,
	// when the adapter knows it and the caller's candidate did not name one
	// (a single native client): the record then still names the answering
	// pair (FR-058). "" = not reported.
	Provider string
}

// Requested returns the resolved effort value the loop asked the adapter to
// send: "" when no effort option is present or it is Provider default
// (Provider default sends no effort field on every route, S-19).
func Requested(options map[string]any) string {
	v, _ := options[EffortOptionKey].(string)
	v = strings.TrimSpace(v)
	if v == ValueProviderDefault {
		return ""
	}
	return v
}

// Report stores what the final body carried into the request's report slot,
// when the caller supplied one.
func Report(options map[string]any, s Sent) {
	if r, ok := options[ReportOptionKey].(*Sent); ok && r != nil {
		*r = s
	}
}

// ReportProvider stores the adapter's own provider id in the request's
// report slot, when the caller supplied one. Call it after Report.
func ReportProvider(options map[string]any, provider string) {
	if r, ok := options[ReportOptionKey].(*Sent); ok && r != nil {
		r.Provider = provider
	}
}

// IsSwitchValue reports whether v is the switch's on/off word.
func IsSwitchValue(v string) bool { return v == ValueOn || v == ValueOff }

// Enables reports whether a resolved value turns reasoning on (a level other
// than none, or On). Off and none are explicit disables.
func Enables(v string) bool {
	return v != "" && v != ValueOff && v != ValueNone
}

// sendable reports whether the route's encoding can carry value v.
func sendable(enc Encoding, v string) bool {
	if IsSwitchValue(v) {
		return enc.Switch
	}
	return enc.Levels
}

// EncodeOpenAIChat returns the request-body fields that carry effort value v
// on an OpenAI-compatible chat route (FR-037 "Provider request forms"), and
// what was sent. A value the route cannot encode sends nothing, reason
// not_supported.
func EncodeOpenAIChat(providerID, model, v string) (map[string]any, Sent) {
	if v == "" {
		return nil, Sent{}
	}
	r := Route{Protocol: ProtocolOpenAIChat, ProviderID: providerID, ModelFamily: FamilyOf(ProtocolOpenAIChat, model)}
	enc, ok := Default().EncodingFor(r)
	if !ok || !sendable(enc, v) {
		return nil, Sent{Reason: ReasonNotSupported}
	}
	switch strings.ToLower(providerID) {
	case "openrouter":
		if IsSwitchValue(v) {
			return map[string]any{"reasoning": map[string]any{"enabled": v == ValueOn}}, Sent{Value: v}
		}
		return map[string]any{"reasoning": map[string]any{"effort": v}}, Sent{Value: v}
	case "deepseek", "z-ai":
		if IsSwitchValue(v) {
			t := "disabled"
			if v == ValueOn {
				t = "enabled"
			}
			return map[string]any{"thinking": map[string]any{"type": t}}, Sent{Value: v}
		}
	}
	return map[string]any{"reasoning_effort": v}, Sent{Value: v}
}

// effortBodyKeys are the request-body keys that carry effort on any route.
var effortBodyKeys = []string{"reasoning_effort", "reasoning", "thinking", "output_config"}

// ApplyExtraBody merges an operator's extra_body last (it wins on any key it
// sets) and reports when it overrides effort: if extra_body sets an effort
// key, the record says extra_body and sent is the value it carries
// (S-20, X7).
func ApplyExtraBody(body, extra map[string]any, sent Sent) Sent {
	overridden := false
	for k, v := range extra {
		body[k] = v
		for _, ek := range effortBodyKeys {
			if k == ek {
				overridden = true
			}
		}
	}
	if !overridden {
		return sent
	}
	return Sent{Value: effortValueInBody(body), Reason: ReasonExtraBody}
}

// effortValueInBody reads the effort value an OpenAI-compatible body carries.
func effortValueInBody(body map[string]any) string {
	if s, ok := body["reasoning_effort"].(string); ok {
		return s
	}
	if r, ok := body["reasoning"].(map[string]any); ok {
		if s, ok := r["effort"].(string); ok {
			return s
		}
		if b, ok := r["enabled"].(bool); ok {
			if b {
				return ValueOn
			}
			return ValueOff
		}
	}
	if th, ok := body["thinking"].(map[string]any); ok {
		switch th["type"] {
		case "disabled":
			return ValueOff
		case "enabled", "adaptive":
			if oc, ok := body["output_config"].(map[string]any); ok {
				if s, ok := oc["effort"].(string); ok {
					return s
				}
			}
			return ValueOn
		}
	}
	return ""
}

// EncodeBedrock returns the additionalModelRequestFields members that carry
// effort value v on a Bedrock Converse route for the model's catalog base
// family (S-17): Anthropic models get thinking {type: adaptive} +
// output_config {effort}; Nova gets reasoningConfig {maxReasoningEffort}.
// Other families have no encoder (not_supported). Bedrock has no switch
// encoding, so On and Off send nothing.
//
// toolLoop reports whether the current tool loop holds an assistant tool call
// (Bedrock carries no reasoning blocks, so any tool call counts): an enabling
// value is then not sent (FR-014, reason continuation).
func EncodeBedrock(baseModel, v string, toolLoop bool) (map[string]any, Sent) {
	if v == "" {
		return nil, Sent{}
	}
	family := FamilyOf(ProtocolBedrockConverse, baseModel)
	enc, ok := Default().EncodingFor(Route{Protocol: ProtocolBedrockConverse, ModelFamily: family})
	if !ok || !sendable(enc, v) || v == ValueNone {
		return nil, Sent{Reason: ReasonNotSupported}
	}
	if toolLoop {
		return nil, Sent{Reason: ReasonContinuation}
	}
	switch family {
	case "claude":
		return map[string]any{
			"thinking":      map[string]any{"type": "adaptive"},
			"output_config": map[string]any{"effort": v},
		}, Sent{Value: v}
	case "nova":
		return map[string]any{"reasoningConfig": map[string]any{"maxReasoningEffort": v}}, Sent{Value: v}
	}
	return nil, Sent{Reason: ReasonNotSupported}
}
