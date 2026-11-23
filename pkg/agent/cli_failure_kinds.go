// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// cli_failure_kinds.go — #1857 (notifications spec §13.8): the CLI-only
// failure classifier (R-2), the message-slot renderer for catalogue
// sentences that carry observed facts ({cli} {model} {n} {minutes}), and the
// typed errors that carry a known cause's code from its producer to every
// surface (§13.8.4 rules 1–2) instead of being re-classified from text.
//
// The CLI classifier is deliberately separate from classifyByMessage: the
// provider-side matchers must not grow CLI wording (or provider text could
// start matching CLI kinds and the reverse), and the CLI path never yields a
// provider or network code — its only pass-through typed exit is the
// driver's own run timeout (row C10/B17a).
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// cliDisplayNames is the CLOSED map from a driver key (the configured
// executor.cli value, or the driver's own short name in its error text) to
// the fixed name a person reads in the {cli} slot (§13.8.2 F-14). A value
// outside this map never reaches a user surface.
var cliDisplayNames = map[string]string{
	"claude-code": "Claude Code",
	"claude":      "Claude Code",
	"codex":       "Codex",
	"opencode":    "OpenCode",
}

// CLIDisplayName returns the fixed display name for a CLI driver key, or ""
// when the key is not one of the supported drivers.
func CLIDisplayName(cli string) string {
	return cliDisplayNames[strings.ToLower(strings.TrimSpace(cli))]
}

// messageSlotPattern matches one catalogue message slot ({cli}, {n}, …).
var messageSlotPattern = regexp.MustCompile(`\{[a-z_]+\}`)

// messageHasSlots reports whether a catalogue sentence still carries a slot.
func messageHasSlots(text string) bool {
	return messageSlotPattern.MatchString(text)
}

// messageSlotFacts are the observed facts a catalogue sentence may name.
// An empty or zero field is an absent fact.
type messageSlotFacts struct {
	cli     string // display name (CLIDisplayName), never the raw config value
	model   string // the model string Omnipus passed (user-chosen)
	n       int    // a count, e.g. the CLI's resolved turn cap
	minutes int    // a wait in whole minutes
}

// renderMessageSlots fills a catalogue sentence's slots from facts. It fails
// closed: a sentence whose slot has no fact never renders an empty or literal
// slot (§13.2) — the generic catalogue sentence is returned instead, and one
// warning names the code and the missing slot names, never their values (F7).
func renderMessageSlots(code LLMErrorCode, text string, facts messageSlotFacts) string {
	rendered := fillMessageSlots(text, facts)
	if missing := unfilledSlotNames(rendered); len(missing) > 0 {
		warnMissingSlotFacts(code, missing)
		return defaultUserMessage(CodeUnknown)
	}
	return rendered
}

// renderMessageVoices renders a code's chat sentence and its neutral twin
// once, from the same facts (#1857 design note D-B B1). A missing fact sends
// BOTH voices to the generic sentence together, with one warning (F7).
func renderMessageVoices(code LLMErrorCode, chat, neutral string, facts messageSlotFacts) (string, string) {
	renderedChat := fillMessageSlots(chat, facts)
	renderedNeutral := fillMessageSlots(neutral, facts)
	missing := unfilledSlotNames(renderedChat + " " + renderedNeutral)
	if len(missing) > 0 {
		warnMissingSlotFacts(code, missing)
		generic := defaultUserMessage(CodeUnknown)
		return generic, generic
	}
	return renderedChat, renderedNeutral
}

// unfilledSlotNames returns the distinct slot names (without braces) still
// literal in text, in order of first appearance.
func unfilledSlotNames(text string) []string {
	var names []string
	seen := map[string]bool{}
	for _, slot := range messageSlotPattern.FindAllString(text, -1) {
		name := slot[1 : len(slot)-1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// warnMissingSlotFacts is the one diagnostic for a catalogue sentence that
// could not be shown because a slot had no fact (#1857 F7): the code and the
// slot NAMES only — never a slot value or the sentence.
func warnMissingSlotFacts(code LLMErrorCode, missing []string) {
	logger.WarnCF("agent", "catalogue sentence has a slot without a fact; the generic sentence is used",
		map[string]any{"code": string(code), "missing_slots": missing})
}

// fillMessageSlots substitutes every slot that has a fact and leaves the
// others literal for the caller's fail-closed check.
func fillMessageSlots(text string, facts messageSlotFacts) string {
	replacements := make([]string, 0, 8)
	if facts.cli != "" {
		replacements = append(replacements, "{cli}", facts.cli)
	}
	if facts.model != "" {
		replacements = append(replacements, "{model}", scrubDetailText(facts.model))
	}
	if facts.n > 0 {
		replacements = append(replacements, "{n}", fmt.Sprint(facts.n))
	}
	if facts.minutes > 0 {
		replacements = append(replacements, "{minutes}", fmt.Sprint(facts.minutes))
	}
	if len(replacements) > 0 {
		text = strings.NewReplacer(replacements...).Replace(text)
	}
	return text
}

// RenderCLIMessage fills the {cli} slot (and {model} when known) of a
// sanitized CLI sentence for the named driver. Used by surfaces outside the
// run path that learn the CLI only after sanitizing (the Settings test).
// A sentence without slots is returned unchanged; one whose slot has no fact
// becomes the generic catalogue sentence, never a literal slot.
func RenderCLIMessage(text, cli, model string) string {
	if !messageHasSlots(text) {
		return text
	}
	return renderMessageSlots(catalogueCodeOfSentence(text), text,
		messageSlotFacts{cli: CLIDisplayName(cli), model: strings.TrimSpace(model)})
}

// catalogueCodeOfSentence names the code whose catalogue sentence (chat or
// neutral, message or variant) is text, for the missing-fact diagnostic of a
// caller that holds only the sentence; unknown when none matches.
func catalogueCodeOfSentence(text string) LLMErrorCode {
	for _, code := range generated.LLMErrorCodes {
		if generated.LLMErrorUserMessages[code] == text || generated.LLMErrorNeutralMessages[code] == text {
			return LLMErrorCode(code)
		}
		for key, sentence := range generated.LLMErrorMessageVariants[code] {
			if sentence == text || generated.LLMErrorNeutralVariants[code][key] == text {
				return LLMErrorCode(code)
			}
		}
	}
	return CodeUnknown
}

// catalogueSentence returns a code's catalogue sentence, or its named
// variant when one is set and catalogued.
func catalogueSentence(code LLMErrorCode, variant string) string {
	if variant != "" {
		if sentence := generated.LLMErrorMessageVariants[string(code)][variant]; sentence != "" {
			return sentence
		}
		logger.WarnCF("agent", "catalogue variant missing — using the code's message",
			map[string]any{"code": string(code), "variant": variant})
	}
	return defaultUserMessage(code)
}

// neutralCatalogueSentence is catalogueSentence in the neutral voice (#1857
// design note D-B B1): the contract's neutral twin of the code's sentence or
// of its named variant. A sentence without a twin has no first person and
// serves both voices, so the chat sentence is returned.
func neutralCatalogueSentence(code LLMErrorCode, variant string) string {
	if variant != "" {
		if _, ok := generated.LLMErrorMessageVariants[string(code)][variant]; ok {
			if twin := generated.LLMErrorNeutralVariants[string(code)][variant]; twin != "" {
				return twin
			}
			return catalogueSentence(code, variant)
		}
	}
	if twin := generated.LLMErrorNeutralMessages[string(code)]; twin != "" {
		return twin
	}
	return defaultUserMessage(code)
}

// cliFailure is the CLI-only classifier's verdict on one runner error: the
// code, its catalogue variant, and the safe facts the sentence and the
// technical details may name (E1 B / E3 A — never the CLI's own text).
type cliFailure struct {
	code       LLMErrorCode
	variant    string
	maxTurns   int    // {n} for external_cli_turn_limit; 0 when not observed
	exitStatus string // technical details only (VS-2); "" when not observed
}

// Variant keys of the catalogue (contracts/components/schemas/LLMError.yaml).
const (
	cliVariantStreamRead  = "stream_read"
	cliVariantStart       = "start"
	cliVariantOutOfCredit = "out_of_credit"
	// cliVariantNoProgram is external_cli_invalid_config's sentence for the
	// B16 pre-checks that refuse before any program is known (W19).
	cliVariantNoProgram = "no_program"
	// toolDedupVariantMCP is tool_assembly_duplicate's sentence for a
	// collision with an MCP tool among the pair (W20); the code's own
	// sentence (W21) is the no-MCP, fail-closed class.
	toolDedupVariantMCP = "mcp"
	// delegationVariantNoCLI is delegation_not_resumable's no-fact sentence
	// (W14's form without the CLI clause), used when no display name is known.
	delegationVariantNoCLI = "no_cli"
)

// cliModelUnavailableTypes are the structured error types a CLI reports when
// it cannot use or route the requested model (§13.8.2 C2). Classified from the
// structured field, never from message wording.
var cliModelUnavailableTypes = map[string]bool{
	"provider.no-route": true,
}

// cliSignInPhrases recognise a CLI that is not signed in (§13.8.2 C3): Claude
// Code's stream result "Not logged in · Please run /login", and the login
// commands the CLIs name in their own refusal.
var cliSignInPhrases = []string{
	"not logged in",
	"please run /login",
	"not signed in",
	"run `codex login`",
	"run `opencode auth login`",
}

// cliUsagePhrases recognise a command line the CLI rejected before any work
// ran (§13.8.2 C1) — only inside the process-exit stderr tail.
var cliUsagePhrases = []string{
	"unknown option",
	"unrecognized option",
	"unrecognised option",
	"unknown flag",
	"unknown argument",
	"unexpected argument",
	"cannot be used multiple times",
	"invalid value for",
	"\nusage:",
}

// cliExitStatusPattern reads the exit status the drivers print in their
// process-exit message ("<cli> exited: exit status N").
var cliExitStatusPattern = regexp.MustCompile(`exited: exit status (\d+)`)

// cliTurnCapPattern reads the drivers' own turn-cap message (§13.8.2 C5).
var cliTurnCapPattern = regexp.MustCompile(`^turn cap exceeded: \d+ turns \(max (\d+)\)`)

// classifyCLIFailure is the CLI-only classifier (R-2). structuredType is the
// error kind the driver read from a structured field (runner.ErrorEvent.Type,
// e.g. OpenCode's error.type); when empty, a raw JSON error envelope in the
// message is decoded for the same field. Every cause it does not recognise is
// external_cli_failed — never unknown, never a provider or network code.
func classifyCLIFailure(message, structuredType string) cliFailure {
	if structuredType == "" {
		structuredType = cliEnvelopeErrorType(message)
	}
	out := cliFailure{code: CodeExternalCLIFailed}
	if m := cliExitStatusPattern.FindStringSubmatch(message); m != nil {
		out.exitStatus = m[1]
	}
	lower := strings.ToLower(message)
	isExit := strings.Contains(message, " exited: ") && strings.Contains(message, "\nstderr:\n")
	switch {
	case strings.HasPrefix(message, "run exceeded timeout"):
		// The driver's own run timeout: R-2's typed-exit pass-through (C10/B17a).
		out.code = CodeTurnTimedOut
	case strings.Contains(message, runner.ErrMaxTurnsRequired.Error()):
		// B17b: Omnipus's own refusal of an unusable resolved turn cap
		// (runner/parse.go::invalidMaxTurnsEvent, validateMaxTurns) — a
		// config gap, never the CLI's own wording.
		out.code = CodeExternalCLIInvalidConfig
	case cliModelUnavailableTypes[structuredType]:
		out.code = CodeExternalCLIModelUnavailable
	case cliTurnCapPattern.MatchString(message):
		out.code = CodeExternalCLITurnLimit
		if m := cliTurnCapPattern.FindStringSubmatch(message); m != nil {
			fmt.Sscan(m[1], &out.maxTurns)
		}
	case containsAnyPhrase(lower, cliSignInPhrases):
		out.code = CodeExternalCLINeedsSignin
	case isBillingEvidence(message, 0):
		out.code = CodeExternalCLIUpstreamRefused
		out.variant = cliVariantOutOfCredit
	case isRateLimitMessage(message):
		out.code = CodeExternalCLIUpstreamRefused
	case isExit && containsAnyPhrase(lower, cliUsagePhrases):
		out.code = CodeExternalCLIInvalidArgs
	case strings.HasPrefix(message, "stream read error:"):
		out.variant = cliVariantStreamRead
	case strings.Contains(message, "dispatch: driver start ("):
		out.variant = cliVariantStart
	}
	return out
}

// containsAnyPhrase reports whether lower contains any of the phrases.
func containsAnyPhrase(lower string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// cliEnvelopeErrorType decodes a raw CLI JSON error envelope
// ({"type":"error","error":{"type":"…",…}}) and returns its structured
// error.type, or "" when message is not such an envelope.
func cliEnvelopeErrorType(message string) string {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil || envelope.Type != "error" {
		return ""
	}
	return envelope.Error.Type
}

// cliTechnicalDetail is the Verbose/Debug technical-details text for a CLI
// failure (FR-093, E3 A): only the program's display name, the exit status
// and the kind of failure — never the CLI's own output.
func cliTechnicalDetail(cliName string, f cliFailure) string {
	parts := make([]string, 0, 3)
	if cliName != "" {
		parts = append(parts, cliName)
	}
	if f.exitStatus != "" {
		parts = append(parts, "exit status "+f.exitStatus)
	}
	kind := string(f.code)
	if f.variant != "" {
		kind += " (" + f.variant + ")"
	}
	parts = append(parts, kind)
	return strings.Join(parts, " · ")
}

// externalCLIRunError is a fatal external-CLI run failure whose cause the
// CLI-only classifier already named. It carries the code and the rendered
// catalogue sentence in both voices to every surface (§13.8.4 rule 2): text
// is the chat form, neutral the form for surfaces outside a chat. Error() is
// the internal, logged form; it keeps the "external-cli run failed: " prefix,
// which reaches gateway.log only — every user and agent surface reads the
// code and sentence instead (rule 3).
type externalCLIRunError struct {
	code    LLMErrorCode
	text    string
	neutral string
}

func (e *externalCLIRunError) Error() string { return "external-cli run failed: " + e.text }

// agentText is the agent-readable failure for the parent's tool result and
// completion message: the typed code and its NEUTRAL sentence (§13.8.4,
// design note D-B B1 — these surfaces are outside the chat).
func (e *externalCLIRunError) agentText() string {
	return "(" + string(e.code) + ") " + e.neutral
}

// RefusalText is what the delegate tool may show the calling agent as written
// (pkg/tools.refusalTexter): the typed code and sentence, no prefix.
func (e *externalCLIRunError) RefusalText() string { return e.agentText() }

// agentRequestLimitError is Omnipus's own per-agent hourly model-request
// limit refusing an iteration (SEC-26, §13.8.5 B3). Its text is unchanged
// from the untyped error it replaces; its code is agent_request_limit, never
// the provider-overloaded rate_limited.
type agentRequestLimitError struct {
	policyRule        string
	retryAfterSeconds float64
}

func (e *agentRequestLimitError) Error() string {
	return fmt.Sprintf("rate limit: %s (retry after %.0fs)", e.policyRule, e.retryAfterSeconds)
}

// waitMinutes is the {minutes} fact: the limiter's wait rounded up to whole
// minutes, or 0 (absent) when the limiter reported no wait.
func (e *agentRequestLimitError) waitMinutes() int {
	return waitMinutesFor(e.retryAfterSeconds)
}

// waitMinutesFor rounds a limiter wait in seconds up to whole minutes; a wait
// of zero or less is 0 (absent fact).
func waitMinutesFor(retryAfterSeconds float64) int {
	if retryAfterSeconds <= 0 {
		return 0
	}
	return int(math.Ceil(retryAfterSeconds / 60))
}

// toolDedupError is the FR-066 detector's verdict on an assembled tool list
// with a duplicate name (§13.8.5 B10). Its text is unchanged; mcpInvolved is
// the one structured source fact the detector records (an MCP tool among the
// colliding pair), carried to the emit site to select the catalogue variant.
type toolDedupError struct {
	name        string
	sources     []string
	mcpInvolved bool
}

func (e *toolDedupError) Error() string {
	return fmt.Sprintf("tools[] dedup invariant violated: tool %q appears from sources %v", e.name, e.sources)
}

// externalCLIRefusalError is an external-CLI dispatch refused before the run
// started, whose cause Omnipus itself knows (§13.8.5): C8 the program-path
// refusal (external_cli_not_found), B16-config a missing executor config or a
// driver that could not be built, B17b an unusable turn cap
// (external_cli_invalid_config), C7 a start failure (external_cli_failed,
// variant start). Error() and Unwrap() are the cause's, unchanged — the
// refusal texts and errors.Is checks callers already rely on keep working —
// while every user surface reads code and sentence (§13.8.4 rule 2).
type externalCLIRefusalError struct {
	code    LLMErrorCode
	text    string // chat form
	neutral string // neutral form (design note D-B B1)
	cause   error
}

func (e *externalCLIRefusalError) Error() string { return e.cause.Error() }
func (e *externalCLIRefusalError) Unwrap() error { return e.cause }

// newExternalCLIRefusal builds the typed pre-run refusal: the catalogue
// sentence (or its variant) with {cli} filled from the closed display-name
// map. An unknown or empty CLI leaves the slot without a fact, so the
// generic sentence is used (renderMessageSlots) — never a literal slot.
func newExternalCLIRefusal(code LLMErrorCode, variant, cli string, cause error) *externalCLIRefusalError {
	text, neutral := renderMessageVoices(code, catalogueSentence(code, variant),
		neutralCatalogueSentence(code, variant), messageSlotFacts{cli: CLIDisplayName(cli)})
	return &externalCLIRefusalError{code: code, text: text, neutral: neutral, cause: cause}
}

// newResumeUnavailableRefusal types errExternalResumeUnavailable (§13.8.5
// B16a) as delegation_not_resumable, W14 form 1, with {cli} filled from the
// closed display-name map. A CLI without a display name selects the no-fact
// variant explicitly: the slotted sentence with no fact would fall back to the
// generic "can't tell why" sentence, which is untrue here. cause keeps its
// identity (errors.Is) and its text (Error()).
func newResumeUnavailableRefusal(cli string, cause error) *externalCLIRefusalError {
	variant := ""
	if CLIDisplayName(cli) == "" {
		variant = delegationVariantNoCLI
	}
	return newExternalCLIRefusal(CodeDelegationNotResumable, variant, cli, cause)
}

// reviveRefusalError is a revival refusal whose sentence Omnipus authored for
// the calling agent (pkg/tools.refusalTexter shows it as written) and whose
// cause keeps its identity for errors.Is — which curatedTurnError, having no
// Unwrap, cannot do.
type reviveRefusalError struct {
	text  string
	cause error
}

func (e *reviveRefusalError) Error() string       { return e.text }
func (e *reviveRefusalError) RefusalText() string { return e.text }
func (e *reviveRefusalError) Unwrap() error       { return e.cause }

// executorCLIOf is agentID's configured external-CLI key, or "" when the
// agent is unknown or has no external executor. It is a fact for the {cli}
// slot only: CLIDisplayName maps it, and an unmapped key never reaches a
// person.
func (al *AgentLoop) executorCLIOf(agentID string) string {
	if al == nil || al.GetRegistry() == nil {
		return ""
	}
	a, ok := al.GetRegistry().GetAgent(strings.TrimSpace(agentID))
	if !ok {
		return ""
	}
	if executor := executorConfigOf(a); executor != nil {
		return executor.CLI
	}
	return ""
}

// classifyDriverStartError types a driver Run (start) failure: an unusable
// turn cap is the B17b config gap, anything else the C7 start failure.
func classifyDriverStartError(cli string, err error) error {
	if errors.Is(err, runner.ErrMaxTurnsRequired) {
		return newExternalCLIRefusal(CodeExternalCLIInvalidConfig, "", cli, err)
	}
	return newExternalCLIRefusal(CodeExternalCLIFailed, cliVariantStart, cli, err)
}

// rateLimitDenialCode is the code a SEC-26 denial is recorded under
// (loop.go::recordRateLimitDenial). #1857 §13.8.5 B3: the agent's own hourly
// model-request limit (resource llm_call) is agent_request_limit
// (attribution config), never the provider's rate_limited. Other SEC-26
// denials (tool calls) have no §13.8 row yet and keep rate_limited.
func rateLimitDenialCode(payload RateLimitPayload) LLMErrorCode {
	if payload.Resource == "llm_call" {
		return CodeAgentRequestLimit
	}
	return CodeRateLimited
}

// translateKnownCauseError maps a typed known-cause error to its LLMError, or
// reports false when err carries none.
func translateKnownCauseError(err error) (LLMError, bool) {
	var refusalErr *externalCLIRefusalError
	if errors.As(err, &refusalErr) {
		return LLMError{
			Code:           refusalErr.code,
			Message:        refusalErr.text,
			NeutralMessage: refusalErr.neutral,
			Retryable:      isRetryable(refusalErr.code),
		}, true
	}
	var cliErr *externalCLIRunError
	if errors.As(err, &cliErr) {
		return LLMError{
			Code:           cliErr.code,
			Message:        cliErr.text,
			NeutralMessage: cliErr.neutral,
			Retryable:      isRetryable(cliErr.code),
		}, true
	}
	var limitErr *agentRequestLimitError
	if errors.As(err, &limitErr) {
		llm := agentRequestLimitLLMError(limitErr.waitMinutes())
		llm.Detail = buildDetail(nil, err.Error())
		return llm, true
	}
	var dupErr *toolDedupError
	if errors.As(err, &dupErr) {
		return toolDedupLLMErrorFor(dupErr.mcpInvolved), true
	}
	return LLMError{}, false
}

// agentRequestLimitLLMError is agent_request_limit's LLMError in both voices
// (W11 and its neutral twin) with {minutes} filled from the limiter's wait in
// whole minutes. The live reply (translateKnownCauseError) and the saved
// record (loop.go::recordRateLimitDenial) both build it here, so live and
// saved say the same. A wait of 0 is an absent fact: both voices fall back to
// the generic sentence together (renderMessageVoices).
func agentRequestLimitLLMError(minutes int) LLMError {
	chat, neutral := renderMessageVoices(CodeAgentRequestLimit,
		defaultUserMessage(CodeAgentRequestLimit), neutralCatalogueSentence(CodeAgentRequestLimit, ""),
		messageSlotFacts{minutes: minutes})
	return LLMError{
		Code:           CodeAgentRequestLimit,
		Message:        chat,
		NeutralMessage: neutral,
		Retryable:      isRetryable(CodeAgentRequestLimit),
	}
}

// toolDedupLLMErrorFor is tool_assembly_duplicate's LLMError in both voices:
// the MCP variant (W20) when an MCP tool is among the colliding pair,
// otherwise the code's own no-MCP sentence (W21).
func toolDedupLLMErrorFor(mcpInvolved bool) LLMError {
	variant := ""
	if mcpInvolved {
		variant = toolDedupVariantMCP
	}
	return LLMError{
		Code:           CodeToolAssemblyDuplicate,
		Message:        catalogueSentence(CodeToolAssemblyDuplicate, variant),
		NeutralMessage: neutralCatalogueSentence(CodeToolAssemblyDuplicate, variant),
		Retryable:      isRetryable(CodeToolAssemblyDuplicate),
	}
}

// toolDedupLLMError is the B10 emit's LLMError for the FR-066 detector's
// verdict: always tool_assembly_duplicate. The MCP-involved fact selects the
// catalogue variant (W20 with an MCP tool, W21 without); it is also logged so
// the operator sees which class fired, and an untyped error fails closed to
// the no-MCP class.
func toolDedupLLMError(err error) LLMError {
	mcpInvolved := false
	var dupErr *toolDedupError
	if errors.As(err, &dupErr) {
		mcpInvolved = dupErr.mcpInvolved
	}
	logger.WarnCF("agent", "tool assembly duplicate classified",
		map[string]any{"code": string(CodeToolAssemblyDuplicate), "mcp_involved": mcpInvolved})
	return toolDedupLLMErrorFor(mcpInvolved)
}

// persistedCatalogueMessage is the sentence an untrusted-stage error record
// stores for an already-classified code. A code whose catalogue sentences
// have no slots stores the caller's message when it is exactly one of the
// code's variants (#1857: the selected variant survives), otherwise the
// code's sentence, as before. A slotted code (#1857) keeps the caller's
// message only when it IS one of its sentences with every slot filled,
// proven by matching the template, so no other text can ride the record;
// otherwise the generic sentence is stored, never a literal slot.
func persistedCatalogueMessage(code LLMErrorCode, message string) string {
	if !catalogueCodeHasSlots(code) {
		for _, variant := range generated.LLMErrorMessageVariants[string(code)] {
			if message == variant {
				return message
			}
		}
		return defaultUserMessage(code)
	}
	if matchesCatalogueSentence(code, message) {
		return message
	}
	return defaultUserMessage(CodeUnknown)
}

// catalogueSentences lists a code's chat sentences: its message, then its
// variants.
func catalogueSentences(code LLMErrorCode) []string {
	variants := generated.LLMErrorMessageVariants[string(code)]
	candidates := make([]string, 0, 1+len(variants))
	candidates = append(candidates, defaultUserMessage(code))
	for _, variant := range variants {
		candidates = append(candidates, variant)
	}
	return candidates
}

// catalogueCodeHasSlots reports whether any of a code's chat sentences
// (message or variant) carries a message slot.
func catalogueCodeHasSlots(code LLMErrorCode) bool {
	for _, sentence := range catalogueSentences(code) {
		if messageHasSlots(sentence) {
			return true
		}
	}
	return false
}

// matchesCatalogueSentence reports whether message is one of the code's chat
// sentences with every slot filled.
func matchesCatalogueSentence(code LLMErrorCode, message string) bool {
	for _, candidate := range catalogueSentences(code) {
		if renderedFromTemplate(message, candidate) {
			return true
		}
	}
	return false
}

// assembledCatalogueSentence is the server layer of design note D-A
// (option 1): the trust decision for a message carried by a code, applied
// at the live frame (WireLLMError) and at the persisted record
// (persistErrorTranscript).
//
//   - A code whose sentences have no slots: message unchanged, not assembled
//     (today's behaviour; provider-message trust is decided elsewhere).
//   - A slotted code whose message is one of its sentences with every slot
//     filled: that message, assembled.
//   - Any other text on a slotted code (a literal slot, a missing fact, a
//     foreign text): the generic sentence, assembled — a full sentence is
//     safe to show as sent — and one warning naming the code and the slot
//     names that had no fact, never their values (F7). Never a raw slot.
func assembledCatalogueSentence(code LLMErrorCode, message string) (string, bool) {
	if !catalogueCodeHasSlots(code) {
		return message, false
	}
	if matchesCatalogueSentence(code, message) {
		return message, true
	}
	missing := unfilledSlotNames(message)
	if len(missing) == 0 {
		// No literal slot in the text (a foreign or already-generic text):
		// name the slots the code's own sentence needed.
		missing = unfilledSlotNames(defaultUserMessage(code))
	}
	warnMissingSlotFacts(code, missing)
	return defaultUserMessage(CodeUnknown), true
}

// renderedFromTemplate reports whether text is template with each slot
// replaced by a non-empty, brace-free value.
func renderedFromTemplate(text, template string) bool {
	if messageHasSlots(text) {
		return false
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	last := 0
	for _, loc := range messageSlotPattern.FindAllStringIndex(template, -1) {
		pattern.WriteString(regexp.QuoteMeta(template[last:loc[0]]))
		pattern.WriteString(`[^{}]+`)
		last = loc[1]
	}
	pattern.WriteString(regexp.QuoteMeta(template[last:]))
	pattern.WriteString("$")
	matched, err := regexp.MatchString(pattern.String(), text)
	return err == nil && matched
}
