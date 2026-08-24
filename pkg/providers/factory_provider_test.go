// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	anthropicmessages "github.com/elicify-ai/omnipus/pkg/providers/anthropic_messages"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// withFixtureCatalog installs the DS-3 dispatch fixture as the process
// catalog for the duration of one test and restores the embedded-snapshot
// fallback afterwards. The fixture is deliberately small and committed
// (testdata/factory_dispatch_catalog.json) so a dispatch assertion pins the
// factory's behaviour, never the day's registry contents.
func withFixtureCatalog(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile("testdata/factory_dispatch_catalog.json")
	if err != nil {
		t.Fatalf("read dispatch fixture: %v", err)
	}
	c, err := catalog.NewCatalog(raw)
	if err != nil {
		t.Fatalf("parse dispatch fixture: %v", err)
	}
	SetCatalog(c)
	t.Cleanup(func() { SetCatalog(nil) })
}

// keyRef sets a credential env var for the row under test and returns its
// ref name.
func keyRef(t *testing.T, name string) string {
	t.Helper()
	t.Setenv(name, "test-key")
	return name
}

// T20 — DS-3 rows 1, 2, 3, 4, 10, 11, 12, 13, 14, 15 and US-8.AC2's row 9.
//
// One table, one assertion shape: given a ModelConfig, which TRANSPORT is
// built and at which URL. Every expectation is derived from the fixture
// document, never from the factory's own source.
func TestCreateProviderFromConfig_ProtocolDispatch(t *testing.T) {
	withFixtureCatalog(t)

	const (
		httpKind      = "http"
		anthropicKind = "anthropic"
		cliKind       = "cli"
	)

	tests := []struct {
		name     string
		cfg      config.ModelConfig
		wantKind string
		wantURL  string // "" = do not assert (cli rows have no HTTP base)
		wantErr  string // substring; "" = expect success
	}{
		{
			name:     "DS-3.1 catalog row uses its own openai-compatible base",
			cfg:      config.ModelConfig{Provider: "zai", Model: "glm-5.2"},
			wantKind: httpKind,
			wantURL:  "https://api.z.ai/api/paas/v4",
		},
		{
			name:     "DS-3.2 registry protocol anthropic builds the Messages transport",
			cfg:      config.ModelConfig{Provider: "minimax", Model: "MiniMax-M2.7"},
			wantKind: anthropicKind,
			wantURL:  "https://api.minimax.io/anthropic/v1",
		},
		{
			name:     "DS-3.3 explicit secondary protocol picks that row's endpoint",
			cfg:      config.ModelConfig{Provider: "zai", Model: "glm-5.2", Protocol: "anthropic"},
			wantKind: anthropicKind,
			wantURL:  "https://api.z.ai/api/anthropic/v1",
		},
		{
			name:    "DS-3.4 a protocol the row does not offer is an error",
			cfg:     config.ModelConfig{Provider: "zai", Model: "glm-5.2", Protocol: "google"},
			wantErr: "does not offer protocol",
		},
		{
			name:     "DS-3.10 google builds the HTTP transport at the Gemini OpenAI base",
			cfg:      config.ModelConfig{Provider: "google", Model: "gemini-2.5-flash"},
			wantKind: httpKind,
			wantURL:  "https://generativelanguage.googleapis.com/v1beta/openai",
		},
		{
			name:     "DS-3.11 an explicit api_base overrides the catalog URL",
			cfg:      config.ModelConfig{Provider: "zai", Model: "glm-5.2", APIBase: "https://proxy.example/v1"},
			wantKind: httpKind,
			wantURL:  "https://proxy.example/v1",
		},
		{
			name:    "DS-3.12 an empty provider is an error, not a default",
			cfg:     config.ModelConfig{Model: "glm-5.2"},
			wantErr: "provider is required",
		},
		{
			name: "DS-3.13 a custom row builds at its own base",
			cfg: config.ModelConfig{
				Provider: "my-proxy", Custom: true, Protocol: "openai-compatible",
				Model: "glm-5.2", APIBase: "https://llm.example/v1",
			},
			wantKind: httpKind,
			wantURL:  "https://llm.example/v1",
		},
		{
			name: "DS-3.14 a second custom row coexists on the anthropic protocol",
			cfg: config.ModelConfig{
				Provider: "my-proxy-2", Custom: true, Protocol: "anthropic",
				Model: "claude-x", APIBase: "https://llm2.example",
			},
			wantKind: anthropicKind,
			wantURL:  "https://llm2.example/v1",
		},
		{
			name:     "ollama builds the local OpenAI-compatible transport",
			cfg:      config.ModelConfig{Provider: "ollama", Model: "llama3"},
			wantKind: httpKind,
			wantURL:  "http://localhost:11434/v1",
		},
		{
			name:     "a cli row dispatches on its cli_kind",
			cfg:      config.ModelConfig{Provider: "codex-cli", Model: "gpt-5.4-codex"},
			wantKind: cliKind,
		},
		{
			name:    "DS-3.9 a tier-unsupported row names the catalog's reason",
			cfg:     config.ModelConfig{Provider: "amazon-bedrock", Model: "anthropic.claude-opus-4"},
			wantErr: "cloud-iam",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.APIKeyRef = keyRef(t, "FACTORY_DISPATCH_TEST_KEY")

			p, modelID, err := CreateProviderFromConfig(&cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("CreateProviderFromConfig() = %T, want error containing %q", p, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateProviderFromConfig() error = %v", err)
			}
			if modelID != cfg.Model {
				t.Errorf("modelID = %q, want the config's model %q verbatim", modelID, cfg.Model)
			}

			switch tt.wantKind {
			case httpKind:
				got, ok := p.(*HTTPProvider)
				if !ok {
					t.Fatalf("provider = %T, want *HTTPProvider", p)
				}
				if got.APIBase() != tt.wantURL {
					t.Errorf("base URL = %q, want %q", got.APIBase(), tt.wantURL)
				}
			case anthropicKind:
				got, ok := p.(*anthropicmessages.Provider)
				if !ok {
					t.Fatalf("provider = %T, want *anthropicmessages.Provider", p)
				}
				if got.APIBase() != tt.wantURL {
					t.Errorf("base URL = %q, want %q", got.APIBase(), tt.wantURL)
				}
			case cliKind:
				if _, ok := p.(*CodexCliProvider); !ok {
					t.Fatalf("provider = %T, want *CodexCliProvider", p)
				}
			}
		})
	}
}

// T20b / E8 — a model the catalog marks `status: retired` still CONSTRUCTS.
// Retirement is a selection-UI signal (ADR-068), never a turn-time refusal:
// an operator whose agent is pinned to a model the registry retired keeps
// working until they change it.
func TestCreateProviderFromConfig_RetiredModelConstructs(t *testing.T) {
	withFixtureCatalog(t)

	cfg := config.ModelConfig{
		Provider:  "retiredshop",
		Model:     "old-1",
		APIKeyRef: keyRef(t, "FACTORY_RETIRED_TEST_KEY"),
	}
	if h := ProviderCatalog().Resolve("retiredshop", "old-1"); h.Status() != catalog.StatusRetired {
		t.Fatalf("fixture precondition: status = %q, want retired", h.Status())
	}

	p, modelID, err := CreateProviderFromConfig(&cfg)
	if err != nil {
		t.Fatalf("a retired model must still construct; got error %v", err)
	}
	if _, ok := p.(*HTTPProvider); !ok {
		t.Fatalf("provider = %T, want *HTTPProvider", p)
	}
	if modelID != "old-1" {
		t.Errorf("modelID = %q, want %q", modelID, "old-1")
	}
}

// T21 — the dual-protocol choice, asserted on both halves of the same row so
// the test cannot pass by ignoring `protocol` altogether.
func TestCreateProviderFromConfig_ProtocolChoice(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "FACTORY_PROTOCOL_CHOICE_KEY")

	primary, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider: "zai", Model: "glm-5.2", APIKeyRef: ref,
	})
	if err != nil {
		t.Fatalf("primary protocol: %v", err)
	}
	if _, ok := primary.(*HTTPProvider); !ok {
		t.Fatalf("no protocol chosen: got %T, want the row's primary *HTTPProvider", primary)
	}

	secondary, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider: "zai", Model: "glm-5.2", Protocol: "anthropic", APIKeyRef: ref,
	})
	if err != nil {
		t.Fatalf("secondary protocol: %v", err)
	}
	got, ok := secondary.(*anthropicmessages.Provider)
	if !ok {
		t.Fatalf("protocol: anthropic gave %T, want *anthropicmessages.Provider", secondary)
	}
	if want := "https://api.z.ai/api/anthropic/v1"; got.APIBase() != want {
		t.Errorf("base URL = %q, want the row's anthropic endpoint %q", got.APIBase(), want)
	}
}

// T22 — the custom-row rule (FR-014, FR-035, E12, DS-3 rows 13–15).
func TestCreateProviderFromConfig_Custom(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "FACTORY_CUSTOM_TEST_KEY")

	t.Run("two custom rows with different ids coexist", func(t *testing.T) {
		a, _, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "my-proxy", Custom: true, Protocol: "openai-compatible",
			Model: "m", APIBase: "https://a.example/v1", APIKeyRef: ref,
		})
		if err != nil {
			t.Fatalf("first custom row: %v", err)
		}
		b, _, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "my-proxy-2", Custom: true, Protocol: "openai-compatible",
			Model: "m", APIBase: "https://b.example/v1", APIKeyRef: ref,
		})
		if err != nil {
			t.Fatalf("second custom row: %v", err)
		}
		ap, bp := a.(*HTTPProvider), b.(*HTTPProvider)
		if ap.APIBase() == bp.APIBase() {
			t.Fatalf("both custom rows resolved to %q; each must keep its own endpoint", ap.APIBase())
		}
	})

	t.Run("custom row without api_base is rejected", func(t *testing.T) {
		_, _, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "my-proxy", Custom: true, Protocol: "openai-compatible",
			Model: "m", APIKeyRef: ref,
		})
		if err == nil || !strings.Contains(err.Error(), "api_base is required") {
			t.Fatalf("error = %v, want it to name the missing api_base", err)
		}
	})

	t.Run("E12 custom row on a disallowed protocol is rejected", func(t *testing.T) {
		for _, proto := range []string{"ollama", "google", "cli", ""} {
			_, _, err := CreateProviderFromConfig(&config.ModelConfig{
				Provider: "my-proxy", Custom: true, Protocol: proto,
				Model: "m", APIBase: "https://x.example/v1", APIKeyRef: ref,
			})
			if err == nil {
				t.Fatalf("protocol %q was accepted on a custom row; only openai-compatible and anthropic are", proto)
			}
		}
	})

	t.Run("DS-3.15 an unflagged unknown id with only api_base is unknown", func(t *testing.T) {
		_, _, err := CreateProviderFromConfig(&config.ModelConfig{
			Provider: "nope", Model: "m", APIBase: "https://x.example/v1", APIKeyRef: ref,
		})
		if !errors.Is(err, ErrUnknownProvider) {
			t.Fatalf("error = %v, want ErrUnknownProvider", err)
		}
	})
}

// T22b — the catalog key is the pair, and the model id crosses the factory
// untouched even when it contains a slash (FR-034, DS-2 rows 9 and 10).
func TestCatalogKey_ProviderAndBareModel(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "FACTORY_CATALOG_KEY_TEST_KEY")

	cfg := &config.ModelConfig{Provider: "openrouter", Model: "z-ai/glm-5.2", APIKeyRef: ref}
	p, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if modelID != "z-ai/glm-5.2" {
		t.Fatalf("modelID = %q, want the full id %q — no prefix may be split off",
			modelID, "z-ai/glm-5.2")
	}
	if got := p.(*HTTPProvider).APIBase(); got != "https://openrouter.ai/api/v1" {
		t.Errorf("base URL = %q, want OpenRouter's — the `z-ai/` segment is part of the model id", got)
	}

	// DS-2.9 / DS-2.10: the pair resolves exactly, and a stale prefix typed
	// into Model under the direct provider is a miss.
	if w := ProviderCatalog().Resolve("openrouter", "z-ai/glm-5.2").Window(); w != 1048576 {
		t.Errorf("(openrouter, z-ai/glm-5.2) window = %d, want 1048576", w)
	}
	if h := ProviderCatalog().Resolve("zai", "zai/glm-5.2"); h.Found() {
		t.Errorf("(zai, zai/glm-5.2) resolved; a stale prefix must be a miss, not a strip")
	}
}

// T23 — every retired spelling fails as an unknown provider, and the error
// text never offers the canonical id (FR-015, SC-010, US-5.AC6).
func TestCreateProviderFromConfig_UnknownProvider_NoHint(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "FACTORY_UNKNOWN_TEST_KEY")

	// Every id below used to be a case in the factory switch or a key in the
	// alias table. The canonical id each one would have been renamed to is
	// listed beside it, and must NOT appear in the error.
	tests := []struct {
		id        string
		canonical string
	}{
		{"z-ai", "zai"},
		{"z.ai", "zai"},
		{"zhipu", "zai"},
		{"moonshot-cn-anthropic", "moonshotai"},
		{"qwen-intl", "alibaba"},
		{"qwen-international", "alibaba"},
		{"dashscope-intl", "alibaba"},
		{"gemini", "google"},
		{"minimax-anthropic", "minimax"},
		{"deepseek-anthropic", "deepseek"},
		{"azure-openai", "azure"},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			_, _, err := CreateProviderFromConfig(&config.ModelConfig{
				Provider: tt.id, Model: "any-model", APIKeyRef: ref,
			})
			if !errors.Is(err, ErrUnknownProvider) {
				t.Fatalf("error = %v, want ErrUnknownProvider", err)
			}
			if !strings.Contains(err.Error(), tt.id) {
				t.Errorf("error %q does not name the id the operator typed", err)
			}
			// SC-010: the assertion is on the absence of the CANONICAL id,
			// never on the echoed user-supplied one — several retired
			// spellings contain their replacement as a substring
			// ("minimax-anthropic" contains "minimax"), so the echoed id is
			// removed before looking.
			residue := strings.ReplaceAll(err.Error(), tt.id, "")
			if strings.Contains(residue, tt.canonical) {
				t.Errorf("error %q offers the canonical id %q — no rename hint is allowed anywhere",
					err, tt.canonical)
			}
		})
	}
}

// TestCreateProviderFromConfig_NilAndEmpty pins the two argument guards.
func TestCreateProviderFromConfig_NilAndEmpty(t *testing.T) {
	withFixtureCatalog(t)

	if _, _, err := CreateProviderFromConfig(nil); err == nil {
		t.Error("nil config: want an error")
	}
	_, _, err := CreateProviderFromConfig(&config.ModelConfig{Provider: "zai"})
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Errorf("empty model: error = %v, want it to name the missing model", err)
	}
}

// TestCreateProviderFromConfig_RequestTimeoutPropagation keeps the per-row
// request_timeout wired through the collapse.
func TestCreateProviderFromConfig_RequestTimeoutPropagation(t *testing.T) {
	withFixtureCatalog(t)

	p, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider:       "zai",
		Model:          "glm-5.2",
		APIKeyRef:      keyRef(t, "FACTORY_TIMEOUT_TEST_KEY"),
		RequestTimeout: 7,
	})
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if _, ok := p.(*HTTPProvider); !ok {
		t.Fatalf("provider = %T, want *HTTPProvider", p)
	}
}

// TestCreateProviderFromConfig_EmbeddedSnapshotIsTheDefaultSource proves the
// fallback: with NO catalog installed, a shipped provider id still resolves,
// because the committed snapshot is what the factory reads (A-21).
func TestCreateProviderFromConfig_EmbeddedSnapshotIsTheDefaultSource(t *testing.T) {
	SetCatalog(nil)

	p, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider:  "openai",
		Model:     "gpt-4.1",
		APIKeyRef: keyRef(t, "FACTORY_EMBEDDED_TEST_KEY"),
	})
	if err != nil {
		t.Fatalf("with no catalog installed the embedded snapshot must serve: %v", err)
	}
	got, ok := p.(*HTTPProvider)
	if !ok {
		t.Fatalf("provider = %T, want *HTTPProvider", p)
	}
	if !strings.HasPrefix(got.APIBase(), "https://api.openai.com") {
		t.Errorf("base URL = %q, want OpenAI's own from the snapshot", got.APIBase())
	}
}

// TestNewCliProviderForKind_UnknownKind keeps the kind switch honest.
func TestNewCliProviderForKind_UnknownKind(t *testing.T) {
	if _, err := NewCliProviderForKind("nope", "/tmp", ""); err == nil {
		t.Error("unknown cli_kind: want an error naming the accepted kinds")
	}
}
