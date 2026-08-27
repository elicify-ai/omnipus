// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/auth"
	"github.com/elicify-ai/omnipus/pkg/config"
	anthropicmessages "github.com/elicify-ai/omnipus/pkg/providers/anthropic_messages"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// NewCliProviderForKind returns the subprocess driver named by a catalog row's
// `cli_kind` (ADR-068 FR-003, X-14): `codex` -> CodexCliProvider,
// `copilot` -> CopilotCliProvider. cliPath is the row's optional `cli_path`
// override; empty means the vendor's default binary name on PATH.
//
// It is the ONE place a kind maps to a constructor; `case ProtocolCLI` in
// CreateProviderFromConfig calls it with the row's own kind and never with a
// provider id.
func NewCliProviderForKind(kind, workspace, cliPath string) (LLMProvider, error) {
	if workspace == "" {
		workspace = "."
	}
	switch kind {
	case catalog.CLIKindCodex:
		return NewCodexCliProvider(workspace), nil
	case catalog.CLIKindCopilot:
		return NewCopilotCliProviderWithCommand(cliPath, workspace), nil
	default:
		return nil, fmt.Errorf("unknown cli_kind %q (want %s|%s)",
			kind, catalog.CLIKindCodex, catalog.CLIKindCopilot)
	}
}

// resolvedRow is everything the dispatch below needs, already decided: which
// wire protocol to speak, at which base URL, and (for a subprocess row) which
// CLI. It is produced by resolveRow — the only function that knows the
// difference between a catalog row and an operator-typed custom row, so the
// dispatch itself never asks "who is this provider?" again.
type resolvedRow struct {
	protocol catalog.Protocol
	api      string
	cliKind  string
	// locality is the catalog's FR-039 classification. It is the ONLY thing
	// that decides whether a row may be constructed without a credential: a
	// local runtime (Ollama, vLLM, LM Studio, a custom row on a private
	// host) has no key to give.
	locality catalog.Locality
}

// resolveRow turns a ModelConfig into the (protocol, base URL) pair to build.
//
// Two shapes exist and only two (FR-035):
//
//   - A CATALOG row — the id is in the served document. Protocol and base URL
//     come from the row; an explicit `protocol` selects one of the row's
//     secondary endpoints (FR-013); an explicit `api_base` overrides the URL
//     and nothing else (DS-3 row 11).
//   - A CUSTOM row — the id is NOT in the document. It is accepted only when
//     it carries BOTH `api_base` and `protocol ∈ {openai-compatible,
//     anthropic}`; it is then keyed by its own id, so several custom
//     endpoints coexist (DS-3 rows 13/14). Anything else is
//     ErrUnknownProvider — with the id and no canonical alternative.
func resolveRow(cfg *config.ModelConfig) (resolvedRow, error) {
	id := strings.TrimSpace(cfg.Provider)
	if id == "" {
		return resolvedRow{}, fmt.Errorf("provider is required")
	}
	want := catalog.Protocol(strings.TrimSpace(cfg.Protocol))
	base := strings.TrimSpace(cfg.APIBase)

	row, known := CatalogProvider(id)
	if !known {
		// A row the catalog does not know is a custom row — but only when it
		// carries BOTH halves of the operator's own endpoint definition. An
		// id flagged `custom: true` gets the specific complaint about the
		// half it is missing (the REST PUT turns that into the 400 of
		// US-8.AC4); an unflagged id that fails the rule is simply unknown,
		// with no canonical alternative offered (DS-3 row 15).
		if cfg.Custom {
			if base == "" {
				return resolvedRow{}, fmt.Errorf(
					"api_base is required for custom provider %q", id)
			}
			if !isCustomProtocol(want) {
				return resolvedRow{}, fmt.Errorf(
					"custom provider %q: protocol must be %s or %s",
					id, catalog.ProtocolOpenAICompatible, catalog.ProtocolAnthropic)
			}
			return resolvedRow{
				protocol: want,
				api:      base,
				locality: catalog.DeriveLocality(id, want, true, base),
			}, nil
		}
		if base != "" && isCustomProtocol(want) {
			return resolvedRow{
				protocol: want,
				api:      base,
				locality: catalog.DeriveLocality(id, want, true, base),
			}, nil
		}
		return resolvedRow{}, &UnknownProviderError{ProviderID: id}
	}
	if row.Tier == catalog.TierUnsupported {
		return resolvedRow{}, wrapUnsupported(row)
	}
	ep, err := endpointFor(row, want)
	if err != nil {
		return resolvedRow{}, err
	}
	if base != "" {
		ep.API = base
	}
	return resolvedRow{
		protocol: ep.Protocol,
		api:      ep.API,
		cliKind:  row.CLIKind,
		locality: row.Locality,
	}, nil
}

// isCustomProtocol reports whether an operator-typed row may speak this
// protocol: an OpenAI- or Anthropic-compatible URL and nothing else (FR-014,
// E12). Written as a comparison rather than a switch so the file's only
// protocol `switch` is the dispatch itself (SC-004).
func isCustomProtocol(p catalog.Protocol) bool {
	return p == catalog.ProtocolOpenAICompatible || p == catalog.ProtocolAnthropic
}

// requireKey enforces the one credential precondition the factory owns: a
// CLOUD endpoint at the CATALOG's own URL, reached over HTTP, needs a key.
//
// Three shapes are exempt. A `locality = local` row — Ollama, vLLM, LM
// Studio, a custom row on a private host — has no key to give, and demanding
// one would make every local setup unconfigurable. A row that authenticates
// by vendor SIGN-IN keeps its credential in the vendor's own file, which
// Omnipus reads and never writes (ADR-068 FR-007). And a row the operator
// has pointed at their OWN `api_base` may well sit behind a gateway that
// authenticates some other way — this is the pre-ADR-067 rule ("api_key or
// api_base"), kept deliberately.
func requireKey(cfg *config.ModelConfig, row resolvedRow) error {
	if cfg.APIKey() != "" {
		return nil
	}
	if row.locality == catalog.LocalityLocal ||
		cfg.AuthMethod == config.AuthMethodSignIn ||
		strings.TrimSpace(cfg.APIBase) != "" {
		return nil
	}
	return fmt.Errorf("api_key is required for provider %q (model: %s)", cfg.Provider, cfg.Model)
}

// agentOAuthRefreshTimeout bounds one agent-path OAuth refresh exchange
// (auth.OAuthProviderConfig.Timeout). Deliberately shorter than the auth
// package's own default, which serves the interactive sign-in flow where an
// operator is watching a spinner: here nobody is watching, the caller is a
// turn in progress, and the call is made while holding the per-vendor refresh
// lock every other turn on this provider needs.
//
// It is now an alias of MaxOAuthRefreshLockHold rather than its own literal.
// The bound only ever meant anything as a ceiling on how long ANY caller may
// hold that lock, and while it was a private 20s here the sign-in status poll
// ran on the auth package's 30s default and quietly became the real ceiling
// for agent turns queued behind it.
const agentOAuthRefreshTimeout = MaxOAuthRefreshLockHold

// CreateProviderFromConfig builds the LLM transport for one provider config.
//
// It dispatches on the WIRE PROTOCOL the catalog carries for the provider id —
// never on the id itself (ADR-067 D11, FR-012). Adding a provider is a catalog
// row, not a Go case; `TestFactory_NoVendorCases` fails the build if a vendor
// case ever comes back.
//
// Returns the provider, the model id to send upstream (the bare catalog model
// id, verbatim — FR-034 leaves any `/` in it alone), and any error.
func CreateProviderFromConfig(cfg *config.ModelConfig) (LLMProvider, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("config is nil")
	}
	modelID := strings.TrimSpace(cfg.Model)
	if modelID == "" {
		return nil, "", fmt.Errorf("model is required")
	}

	row, err := resolveRow(cfg)
	if err != nil {
		return nil, "", err
	}

	switch row.protocol {
	case catalog.ProtocolOpenAICompatible, catalog.ProtocolGoogle, catalog.ProtocolOllama:
		// ADR-068 §8b (T068-32/T068-14): openai-chatgpt is an
		// openai-compatible row whose credential is Omnipus's OWN stored
		// device-code OAuth token, not an API key — so it is handled here,
		// ahead of requireKey, rather than as a protocol of its own. The
		// vendor file ~/.codex/auth.json stays codex-cli's and read-only.
		// DefaultCredentialStore is wired once at gateway boot
		// (pkg/gateway/gateway.go); a nil store means a test or CLI path
		// never called SetDefaultCredentialStore, and we fail closed with a
		// clear error rather than nil-dereferencing inside the token source.
		if cfg.Provider == "openai-chatgpt" {
			if DefaultCredentialStore == nil {
				return nil, "", fmt.Errorf(
					"openai-chatgpt: credential store not available (SetDefaultCredentialStore was never called)",
				)
			}
			// The agent-path refresh runs inside a live turn and holds the
			// process-wide per-vendor refresh lock while it does, so it is
			// bounded tighter than the interactive sign-in flow's default:
			// a hung vendor must cost one turn, not every later turn.
			oauthCfg := auth.OpenAIOAuthConfig()
			oauthCfg.Timeout = agentOAuthRefreshTimeout
			tokenSource := NewStoreOAuthTokenSource("openai-chatgpt", DefaultCredentialStore, oauthCfg)
			return NewCodexProviderWithTokenSource("", "", tokenSource), modelID, nil
		}

		// One HTTP transport, three protocol values. `google` is the row's
		// Gemini OpenAI-compatible base with Bearer auth (F-13) and `ollama`
		// is the local OpenAI-compatible surface; the values stay distinct so
		// the URL rule and the locality rule (FR-039) can be stated per
		// protocol without a vendor case.
		if err := requireKey(cfg, row); err != nil {
			return nil, "", err
		}
		p, err := NewHTTPProviderWithMaxTokensFieldAndRequestTimeout(
			cfg.APIKey(),
			row.api,
			cfg.Proxy,
			cfg.MaxTokensField,
			cfg.RequestTimeout,
			cfg.ExtraBody,
		)
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	case catalog.ProtocolAnthropic:
		if err := requireKey(cfg, row); err != nil {
			return nil, "", err
		}
		return anthropicmessages.NewProviderWithTimeout(
			cfg.APIKey(),
			row.api,
			cfg.RequestTimeout,
		), modelID, nil

	case catalog.ProtocolCLI:
		p, err := NewCliProviderForKind(row.cliKind, cfg.Home, "")
		if err != nil {
			return nil, "", err
		}
		return p, modelID, nil

	default:
		return nil, "", fmt.Errorf("provider %q declares no usable protocol", cfg.Provider)
	}
}
