package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturePath is the FR-027 conformance fixture shared (by copy) with the
// assembly repository's own tests.
const fixturePath = "testdata/providers_catalog_2.0.0_fixture.json"

func loadFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(fixturePath))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// fixtureMap decodes the fixture into a generic tree so each DS-1 row can
// mutate exactly one field before re-encoding.
func fixtureMap(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(loadFixture(t), &m); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return m
}

func encode(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func providers(m map[string]any) []any { return m["providers"].([]any) }

func provider(m map[string]any, i int) map[string]any { return providers(m)[i].(map[string]any) }

func models(p map[string]any) []any { return p["models"].([]any) }

func model(p map[string]any, i int) map[string]any { return models(p)[i].(map[string]any) }

// providerIndex returns the index of provider id in the fixture, failing
// the test when absent, so DS-1 rows never silently mutate the wrong row.
func providerIndex(t *testing.T, m map[string]any, id string) int {
	t.Helper()
	for i, p := range providers(m) {
		if p.(map[string]any)["id"] == id {
			return i
		}
	}
	t.Fatalf("fixture has no provider %q", id)
	return -1
}

func mustCatalog(t *testing.T, data []byte) *Catalog {
	t.Helper()
	c, err := NewCatalog(data)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return c
}

// T1 — DS-1.1, US-1.AC1: the conforming fixture loads and every pair
// resolves with its own facts.
func TestParseDocument_Conforming(t *testing.T) {
	doc, err := ParseDocument(loadFixture(t))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", doc.SchemaVersion, SchemaVersion)
	}
	if doc.Version != "v2026.8.22" {
		t.Fatalf("version = %q", doc.Version)
	}
	if doc.UpdatedAt.IsZero() || doc.Source == "" {
		t.Fatalf("updated_at/source not carried: %v %q", doc.UpdatedAt, doc.Source)
	}
	if doc.DefaultResizeLimits != (ResizeLimits{LongEdgePx: 7680, MaxBytes: 10485760}) {
		t.Fatalf("default_resize_limits = %+v", doc.DefaultResizeLimits)
	}
	if got := len(doc.Providers); got != 6 {
		t.Fatalf("providers = %d, want 6", got)
	}
	total := 0
	for _, p := range doc.Providers {
		total += len(p.Models)
	}
	if total != 6 {
		t.Fatalf("models = %d, want 6", total)
	}

	c := mustCatalog(t, loadFixture(t))
	h := c.Resolve("zai", "glm-5.2")
	if !h.Found() {
		t.Fatal("(zai, glm-5.2) must resolve")
	}
	if h.Window() != 1000000 || h.MaxOutput() != 131072 {
		t.Fatalf("window/max_output = %d/%d", h.Window(), h.MaxOutput())
	}
	if !h.Supports(ModalityText) || h.Supports(ModalityImage) {
		t.Fatalf("modalities wrong: %v", h.InputModalities())
	}
	if !h.ToolCall() || h.Status() != StatusActive {
		t.Fatalf("tool_call/status = %v/%q", h.ToolCall(), h.Status())
	}
	if h.Budget() != (ResizeLimits{LongEdgePx: 4096, MaxBytes: 5242880}) {
		t.Fatalf("provider resize limits not applied: %+v", h.Budget())
	}
	if h.ProviderID() != "zai" || h.ModelID() != "glm-5.2" {
		t.Fatalf("ids = %q/%q", h.ProviderID(), h.ModelID())
	}

	// FR-030 picker fields carried (A-9/A-14), incl. the second protocol.
	p, ok := c.Provider("zai")
	if !ok {
		t.Fatal("Provider(zai) must be present")
	}
	if p.Name != "Z.AI" || p.Company != "Zhipu AI" || p.Tier != TierPopular {
		t.Fatalf("zai picker fields: %+v", p)
	}
	if len(p.AuthMethods) != 1 || p.AuthMethods[0] != AuthAPIKey {
		t.Fatalf("auth_methods = %v", p.AuthMethods)
	}
	if len(p.Protocols) != 2 || p.Protocols[1].Protocol != ProtocolAnthropic {
		t.Fatalf("protocols = %+v", p.Protocols)
	}
	if p.Models[0].ReleaseDate != "2026-05-20" || p.Models[0].Name != "GLM-5.2" {
		t.Fatalf("model picker fields: %+v", p.Models[0])
	}
	// The disputed row (A-22) is carried through.
	if !c.Resolve("openrouter", "z-ai/glm-5.2").Disputed() {
		t.Fatal("disputed marker must be carried")
	}
	if c.Resolve("zai", "glm-5.2").Disputed() {
		t.Fatal("non-disputed row must not be marked")
	}
	// E2: a provider with zero models is accepted and listed.
	if p, ok := c.Provider("ollama"); !ok || len(p.Models) != 0 {
		t.Fatalf("ollama must be listed with zero models: %v %+v", ok, p)
	}
}

// T2 — DS-1 rows 2–8, 11, 15, 16, 23, 26 (+ 9, 10, 12, 24, 25 accept
// rows): each defect is rejected naming the offending path; an accepted
// row loads. "Previous retained" is asserted through Catalog.Apply.
func TestParseDocument_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, m map[string]any)
		wantErr string // substring of the error; "" = accept
		schema  bool   // expect errors.Is(err, ErrSchemaVersion)
	}{
		{name: "DS-1.2 schema 1.0.0", mutate: func(_ *testing.T, m map[string]any) { m["schema_version"] = "1.0.0" }, wantErr: "schema_version", schema: true},
		{name: "DS-1.3 schema 2.1.0", mutate: func(_ *testing.T, m map[string]any) { m["schema_version"] = "2.1.0" }, wantErr: "schema_version", schema: true},
		{name: "DS-1.4 empty version", mutate: func(_ *testing.T, m map[string]any) { m["version"] = "" }, wantErr: "version"},
		{name: "DS-1.26 version without v", mutate: func(_ *testing.T, m map[string]any) { m["version"] = "2026.8.22" }, wantErr: "version"},
		{name: "DS-1.5 duplicate provider", mutate: func(t *testing.T, m map[string]any) {
			provider(m, 1)["id"] = "zai"
		}, wantErr: "providers[1].id"},
		{name: "DS-1.6 duplicate model in zai", mutate: func(t *testing.T, m map[string]any) {
			zai := provider(m, providerIndex(t, m, "zai"))
			model(zai, 1)["id"] = model(zai, 0)["id"]
		}, wantErr: "providers[0].models[1].id"},
		{name: "empty provider id", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["id"] = "" }, wantErr: "providers[0].id"},
		{name: "empty model id", mutate: func(t *testing.T, m map[string]any) { model(provider(m, 0), 0)["id"] = "" }, wantErr: "providers[0].models[0].id"},
		{name: "DS-1.7 protocol grpc", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["protocol"] = "grpc" }, wantErr: "providers[0].protocol"},
		{name: "empty protocol on supported tier", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["protocol"] = "" }, wantErr: "providers[0].protocol"},
		{name: "DS-1.8 modalities lack text", mutate: func(t *testing.T, m map[string]any) {
			model(provider(m, 0), 0)["input_modalities"] = []any{"image"}
		}, wantErr: "providers[0].models[0].input_modalities"},
		{name: "DS-1.11 zero providers", mutate: func(_ *testing.T, m map[string]any) { m["providers"] = []any{} }, wantErr: "providers"},
		{name: "DS-1.15 default max_bytes 0", mutate: func(_ *testing.T, m map[string]any) {
			m["default_resize_limits"].(map[string]any)["max_bytes"] = 0
		}, wantErr: "default_resize_limits"},
		{name: "provider resize long_edge_px 0", mutate: func(t *testing.T, m map[string]any) {
			provider(m, 0)["resize_limits"].(map[string]any)["long_edge_px"] = 0
		}, wantErr: "providers[0].resize_limits"},
		{name: "DS-1.16 auth_methods empty", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["auth_methods"] = []any{} }, wantErr: "providers[0].auth_methods"},
		{name: "auth_methods unknown value", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["auth_methods"] = []any{"password"} }, wantErr: "providers[0].auth_methods"},
		{name: "tier unknown", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["tier"] = "gold" }, wantErr: "providers[0].tier"},
		{name: "status unknown", mutate: func(t *testing.T, m map[string]any) { model(provider(m, 0), 0)["status"] = "beta" }, wantErr: "providers[0].models[0].status"},
		{name: "release_date malformed", mutate: func(t *testing.T, m map[string]any) { model(provider(m, 0), 0)["release_date"] = "2026/05/20" }, wantErr: "providers[0].models[0].release_date"},
		{name: "empty updated_at", mutate: func(_ *testing.T, m map[string]any) { m["updated_at"] = "" }, wantErr: "updated_at"},
		{name: "empty source", mutate: func(_ *testing.T, m map[string]any) { m["source"] = "" }, wantErr: "source"},
		{name: "DS-1.23 protocols lacks primary", mutate: func(t *testing.T, m map[string]any) {
			provider(m, 0)["protocols"] = []any{map[string]any{"protocol": "anthropic", "api": "https://api.z.ai/api/anthropic"}}
		}, wantErr: "providers[0].protocols"},
		{name: "protocols duplicate entry", mutate: func(t *testing.T, m map[string]any) {
			p := provider(m, 0)
			p["protocols"] = []any{
				map[string]any{"protocol": p["protocol"], "api": p["api"]},
				map[string]any{"protocol": p["protocol"], "api": p["api"]},
			}
		}, wantErr: "providers[0].protocols[1]"},
		{name: "protocols primary with different api", mutate: func(t *testing.T, m map[string]any) {
			p := provider(m, 0)
			p["protocols"] = []any{map[string]any{"protocol": p["protocol"], "api": "https://elsewhere.example/v1"}}
		}, wantErr: "providers[0].protocols"},
		{name: "not JSON", mutate: func(_ *testing.T, m map[string]any) { m["providers"] = "not-a-list" }, wantErr: "providers"},

		// Accept rows.
		{name: "DS-1.9 context_window 0 accepted", mutate: func(t *testing.T, m map[string]any) { model(provider(m, 0), 0)["context_window"] = 0 }},
		{name: "DS-1.10 provider with models [] accepted", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["models"] = []any{} }},
		{name: "DS-1.12 unicode name preserved", mutate: func(t *testing.T, m map[string]any) { provider(m, 0)["name"] = "智谱 AI" }},
		{name: "DS-1.24 protocols omitted", mutate: func(t *testing.T, m map[string]any) { delete(provider(m, 0), "protocols") }},
		{name: "DS-1.25 bedrock empty protocol, tier unsupported", mutate: func(t *testing.T, m map[string]any) {
			b := provider(m, providerIndex(t, m, "amazon-bedrock"))
			b["protocol"] = ""
			b["tier"] = "unsupported"
			b["unsupported_reason"] = "cloud-iam"
		}},
		{name: "release_date omitted accepted", mutate: func(t *testing.T, m map[string]any) { delete(model(provider(m, 0), 0), "release_date") }},
		{name: "unknown modality carried", mutate: func(t *testing.T, m map[string]any) {
			model(provider(m, 0), 0)["input_modalities"] = []any{"text", "hologram"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureMap(t)
			tc.mutate(t, m)
			data := encode(t, m)

			c := mustCatalog(t, loadFixture(t))
			before := c.Document()

			_, perr := ParseDocument(data)
			aerr := c.Apply(data)

			if tc.wantErr == "" {
				if perr != nil || aerr != nil {
					t.Fatalf("expected accept, got parse=%v apply=%v", perr, aerr)
				}
				if c.Document() == before {
					t.Fatal("Apply must swap in the new document")
				}
				return
			}
			if perr == nil || aerr == nil {
				t.Fatalf("expected rejection containing %q, got parse=%v apply=%v", tc.wantErr, perr, aerr)
			}
			if !strings.Contains(perr.Error(), tc.wantErr) {
				t.Fatalf("error %q does not name %q", perr.Error(), tc.wantErr)
			}
			if errors.Is(perr, ErrSchemaVersion) != tc.schema {
				t.Fatalf("ErrSchemaVersion match = %v, want %v (err=%v)", !tc.schema, tc.schema, perr)
			}
			if !errors.Is(perr, ErrInvalid) {
				t.Fatalf("every rejection must wrap ErrInvalid: %v", perr)
			}
			if c.Document() != before {
				t.Fatal("a rejected Apply must retain the previous document")
			}
		})
	}
}

// DS-1.12 / E9: unicode survives the round trip byte-for-byte.
func TestParseDocument_UnicodePreserved(t *testing.T) {
	m := fixtureMap(t)
	provider(m, 0)["name"] = "智谱 AI"
	model(provider(m, 0), 0)["name"] = "GLM‑5.2 旗舰"
	doc, err := ParseDocument(encode(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Providers[0].Name != "智谱 AI" || doc.Providers[0].Models[0].Name != "GLM‑5.2 旗舰" {
		t.Fatalf("unicode altered: %q %q", doc.Providers[0].Name, doc.Providers[0].Models[0].Name)
	}
}

// DS-1.17 / FR-030: aliases are accepted and never participate in
// resolution.
func TestParseDocument_AliasesNeverResolve(t *testing.T) {
	m := fixtureMap(t)
	provider(m, providerIndex(t, m, "zai"))["aliases"] = []any{"z-ai", "zhipu"}
	c := mustCatalog(t, encode(t, m))
	p, ok := c.Provider("zai")
	if !ok || len(p.Aliases) != 2 {
		t.Fatalf("aliases not carried: %v %+v", ok, p.Aliases)
	}
	if _, ok := c.Provider("z-ai"); ok {
		t.Fatal("alias z-ai must not resolve a provider")
	}
	if c.Resolve("z-ai", "glm-5.2").Found() {
		t.Fatal("alias z-ai must not resolve a model")
	}
}

// T9c — DS-1.18–22, 25 and FR-033: URL validation outline.
func TestParseDocument_APIURLValidation(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		api      string
		wantErr  string // "" = accept
	}{
		{"DS-1.18 http on hosted", "zai", "http://api.z.ai/api/paas/v4", "providers[0].api"},
		{"DS-1.19 metadata host", "zai", "https://169.254.169.254/v1", "providers[0].api"},
		{"DS-1.20 rfc1918 host", "zai", "https://10.0.0.5/v1", "providers[0].api"},
		{"DS-1.21 userinfo", "zai", "https://u:p@api.z.ai", "providers[0].api"},
		{"query", "zai", "https://api.z.ai/v1?x=1", "providers[0].api"},
		{"fragment", "zai", "https://api.z.ai/v1#frag", "providers[0].api"},
		{"loopback v4", "zai", "https://127.0.0.1/v1", "providers[0].api"},
		{"loopback v6", "zai", "https://[::1]/v1", "providers[0].api"},
		{"ula v6", "zai", "https://[fd00::1]/v1", "providers[0].api"},
		{"link-local v4", "zai", "https://169.254.1.1/v1", "providers[0].api"},
		{"localhost name", "zai", "https://localhost/v1", "providers[0].api"},
		{"relative", "zai", "api.z.ai/v1", "providers[0].api"},
		{"empty host", "zai", "https:///v1", "providers[0].api"},
		{"DS-1.22 lmstudio loopback http", "lmstudio", "http://127.0.0.1:1234/v1", ""},
		{"ollama localhost http", "ollama", "http://localhost:11434/v1", ""},
		{"hosted https public", "zai", "https://api.z.ai/api/paas/v4", ""},
		{"hosted https public ip", "zai", "https://8.8.8.8/v1", ""},
		{"DS-1.25 unsupported empty api", "amazon-bedrock", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureMap(t)
			i := providerIndex(t, m, tc.provider)
			p := provider(m, i)
			p["api"] = tc.api
			if tc.provider == "zai" {
				// Keep the protocols[] primary consistent with the new api so the
				// failure is attributed to `api`, not `protocols`.
				p["protocols"] = []any{map[string]any{"protocol": p["protocol"], "api": tc.api}}
			}
			_, err := ParseDocument(encode(t, m))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected accept, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected rejection naming %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not name %q", err, tc.wantErr)
			}
		})
	}

	// FR-033 also applies to protocols[].api.
	t.Run("protocols[].api http on hosted", func(t *testing.T) {
		m := fixtureMap(t)
		p := provider(m, providerIndex(t, m, "zai"))
		p["protocols"] = []any{
			map[string]any{"protocol": p["protocol"], "api": p["api"]},
			map[string]any{"protocol": "anthropic", "api": "http://10.0.0.9/anthropic"},
		}
		_, err := ParseDocument(encode(t, m))
		if err == nil || !strings.Contains(err.Error(), "providers[0].protocols[1].api") {
			t.Fatalf("want providers[0].protocols[1].api rejection, got %v", err)
		}
	})
}

// T3 — DS-2.1/2, US-1.AC4, US-4.AC1: the key is the pair.
func TestResolve_SameModelIDTwoProviders(t *testing.T) {
	m := fixtureMap(t)
	// Give minimax a model whose id equals zai's "glm-5.2" to prove the pair
	// keys independently (US-1.AC4).
	mm := provider(m, providerIndex(t, m, "minimax"))
	mm["models"] = append(models(mm), map[string]any{
		"id": "glm-5.2", "name": "GLM via MiniMax", "tool_call": false,
		"context_window": 4096, "max_output_tokens": 1024,
		"input_modalities": []any{"text"}, "status": "active",
	})
	c := mustCatalog(t, encode(t, m))

	if got := c.Resolve("openrouter", "z-ai/glm-5.2").Window(); got != 1048576 {
		t.Fatalf("DS-2.1 (openrouter, z-ai/glm-5.2) = %d, want 1048576", got)
	}
	if got := c.Resolve("zai", "glm-5.2").Window(); got != 1000000 {
		t.Fatalf("DS-2.2 (zai, glm-5.2) = %d, want 1000000", got)
	}
	h := c.Resolve("minimax", "glm-5.2")
	if !h.Found() || h.Window() != 4096 || h.ToolCall() {
		t.Fatalf("(minimax, glm-5.2) must resolve independently: %+v", h)
	}
}

// T4 — DS-1.9, US-1.AC5: unknown limits are 0 and the handle is usable.
func TestResolve_UnknownLimitsAreZero(t *testing.T) {
	c := mustCatalog(t, loadFixture(t))
	h := c.Resolve("amazon-bedrock", "anthropic.claude-opus-4")
	if !h.Found() {
		t.Fatal("row must resolve")
	}
	if h.Window() != 0 || h.MaxOutput() != 0 {
		t.Fatalf("unknown limits must be 0, got %d/%d", h.Window(), h.MaxOutput())
	}
	if !h.Supports(ModalityText) || h.Status() != StatusActive {
		t.Fatalf("other facts must still be served: %v %q", h.InputModalities(), h.Status())
	}
}

// T5 — DS-2.3/4/9/10, US-4.AC2, FR-003, E3: no prefix stripping or adding.
func TestResolve_NoPrefixStripping(t *testing.T) {
	c := mustCatalog(t, loadFixture(t))
	misses := [][2]string{
		{"openrouter", "glm-5.2"},       // DS-2.3
		{"zai", "z-ai/glm-5.2"},         // DS-2.4
		{"zai", "zai/glm-5.2"},          // DS-2.10
		{"openrouter", "z-ai/glm-5.2/"}, // trailing data is data
	}
	for _, k := range misses {
		if c.Resolve(k[0], k[1]).Found() {
			t.Fatalf("(%s, %s) must miss", k[0], k[1])
		}
	}
	// DS-2.9: the verbatim ModelConfig.Model with a `/` is one exact key.
	if got := c.Resolve("openrouter", "z-ai/glm-5.2").Window(); got != 1048576 {
		t.Fatalf("DS-2.9 = %d", got)
	}
}

// T6 — DS-2.5/6/7, FR-004, US-4.AC3: miss per consumer.
func TestResolve_MissSemantics(t *testing.T) {
	c := mustCatalog(t, loadFixture(t))
	for _, k := range [][2]string{
		{"", "glm-5.2"},     // DS-2.5
		{"zai", ""},         // DS-2.6
		{"ZAI", "glm-5.2"},  // DS-2.7 / E4: no case folding
		{" zai", "glm-5.2"}, // E4: no trimming here (config boundary trims)
		{"nope", "glm-5.2"}, // unknown provider
		{"zai", "GLM-5.2"},  // model ids are exact too
	} {
		h := c.Resolve(k[0], k[1])
		if h.Found() {
			t.Fatalf("(%q, %q) must miss", k[0], k[1])
		}
		// Agent loop: window/output unknown.
		if h.Window() != 0 || h.MaxOutput() != 0 {
			t.Fatalf("miss window/output must be 0: %d/%d", h.Window(), h.MaxOutput())
		}
		// Media path: optimistic modality default (text+image) + catalog default
		// resize limits, exactly as today's capabilities catalog.
		if !h.Supports(ModalityText) || !h.Supports(ModalityImage) {
			t.Fatalf("miss must be optimistic text+image: %v", h.InputModalities())
		}
		if h.Supports(ModalityPDF) || h.Supports(ModalityAudio) || h.Supports(ModalityVideo) {
			t.Fatalf("miss must not claim pdf/audio/video: %v", h.InputModalities())
		}
		if h.Budget() != (ResizeLimits{LongEdgePx: 7680, MaxBytes: 10485760}) {
			t.Fatalf("miss budget must be the catalog default: %+v", h.Budget())
		}
		if h.Status() != StatusActive || h.Disputed() || h.ToolCall() {
			t.Fatalf("miss must carry neutral facts: %q %v %v", h.Status(), h.Disputed(), h.ToolCall())
		}
		if h.ProviderID() != k[0] || h.ModelID() != k[1] {
			t.Fatalf("miss handle must echo the asked key: %q/%q", h.ProviderID(), h.ModelID())
		}
	}

	// An empty catalog (E7 — no document at all) degrades the same way.
	empty := New()
	h := empty.Resolve("zai", "glm-5.2")
	if h.Found() || h.Window() != 0 || !h.Supports(ModalityImage) {
		t.Fatalf("empty catalog must miss optimistically: %+v", h)
	}
	if h.Budget() != DefaultResizeLimits {
		t.Fatalf("empty catalog budget must be the package default: %+v", h.Budget())
	}
	if empty.Document() != nil {
		t.Fatal("empty catalog has no document")
	}
	if _, ok := empty.Provider("zai"); ok {
		t.Fatal("empty catalog has no providers")
	}
}

// DS-2.8 / E8: a retired model still resolves with its facts and status.
func TestResolve_RetiredStillResolves(t *testing.T) {
	m := fixtureMap(t)
	zai := provider(m, providerIndex(t, m, "zai"))
	model(zai, 0)["status"] = "retired"
	c := mustCatalog(t, encode(t, m))
	h := c.Resolve("zai", "glm-5.2")
	if !h.Found() || h.Window() != 1000000 || h.Status() != StatusRetired {
		t.Fatalf("DS-2.8: found=%v window=%d status=%q", h.Found(), h.Window(), h.Status())
	}
}

// Handles must not expose catalog state to mutation.
func TestResolve_HandleIsReadOnlyCopy(t *testing.T) {
	c := mustCatalog(t, loadFixture(t))
	mods := c.Resolve("openrouter", "z-ai/glm-5.2").InputModalities()
	mods[0] = "corrupted"
	if !c.Resolve("openrouter", "z-ai/glm-5.2").Supports(ModalityText) {
		t.Fatal("mutating the returned slice must not affect the catalog")
	}
	p, _ := c.Provider("zai")
	p.Models[0].ID = "corrupted"
	p.Aliases = append(p.Aliases, "x")
	if !c.Resolve("zai", "glm-5.2").Found() {
		t.Fatal("mutating a Provider copy must not affect the catalog")
	}
}

// T24b — FR-039: the single locality predicate.
func TestCatalog_LocalityPredicate(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		protocol Protocol
		custom   bool
		api      string
		want     Locality
	}{
		{"ollama by protocol", "ollama", ProtocolOllama, false, "http://localhost:11434/v1", LocalityLocal},
		{"ollama protocol under another id", "my-ollama", ProtocolOllama, false, "https://ollama.example/v1", LocalityLocal},
		{"vllm by id", "vllm", ProtocolOpenAICompatible, false, "http://localhost:8000/v1", LocalityLocal},
		{"lmstudio by id", "lmstudio", ProtocolOpenAICompatible, false, "http://127.0.0.1:1234/v1", LocalityLocal},
		{"custom loopback", "my-proxy", ProtocolOpenAICompatible, true, "http://127.0.0.1:8080/v1", LocalityLocal},
		{"custom localhost name", "my-proxy", ProtocolOpenAICompatible, true, "http://localhost:8080/v1", LocalityLocal},
		{"custom rfc1918", "my-proxy", ProtocolAnthropic, true, "http://192.168.1.20:8080", LocalityLocal},
		{"custom ula v6", "my-proxy", ProtocolOpenAICompatible, true, "http://[fd12::1]:8080", LocalityLocal},
		{"custom public", "my-proxy", ProtocolOpenAICompatible, true, "https://proxy.example.com/v1", LocalityCloud},
		{"custom unparsable", "my-proxy", ProtocolOpenAICompatible, true, "::not a url::", LocalityCloud},
		{"zai", "zai", ProtocolOpenAICompatible, false, "https://api.z.ai/api/paas/v4", LocalityCloud},
		{"openai-chatgpt", "openai-chatgpt", ProtocolOpenAICompatible, false, "https://chatgpt.com/backend-api/codex", LocalityCloud},
		{"codex-cli", "codex-cli", ProtocolCLI, false, "", LocalityCloud},
		{"hosted row with private host is still cloud (rejected elsewhere)", "zai", ProtocolOpenAICompatible, false, "https://10.0.0.5/v1", LocalityCloud},
		{"unsupported", "amazon-bedrock", "", false, "", LocalityCloud},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveLocality(tc.id, tc.protocol, tc.custom, tc.api); got != tc.want {
				t.Fatalf("DeriveLocality(%q,%q,%v,%q) = %q, want %q", tc.id, tc.protocol, tc.custom, tc.api, got, tc.want)
			}
		})
	}

	// Derived on load and carried on every provider handle.
	c := mustCatalog(t, loadFixture(t))
	for id, want := range map[string]Locality{
		"ollama": LocalityLocal, "lmstudio": LocalityLocal,
		"zai": LocalityCloud, "openrouter": LocalityCloud, "minimax": LocalityCloud, "amazon-bedrock": LocalityCloud,
	} {
		p, ok := c.Provider(id)
		if !ok {
			t.Fatalf("provider %q missing", id)
		}
		if p.Locality != want {
			t.Fatalf("%s locality = %q, want %q", id, p.Locality, want)
		}
	}
	if got := c.Resolve("lmstudio", "qwen3-8b").Locality(); got != LocalityLocal {
		t.Fatalf("handle locality = %q", got)
	}
	if got := c.Resolve("nope", "x").Locality(); got != LocalityCloud {
		t.Fatalf("miss locality must default to cloud, got %q", got)
	}
	// A published `locality` field is ignored: it is derived, never read.
	m := fixtureMap(t)
	provider(m, providerIndex(t, m, "zai"))["locality"] = "local"
	c2 := mustCatalog(t, encode(t, m))
	if p, _ := c2.Provider("zai"); p.Locality != LocalityCloud {
		t.Fatal("published locality must be ignored (derived on load)")
	}
}

// SC-011: Resolve must not allocate (asserted in `go test`, not only in the
// benchmark).
func TestResolve_ZeroAllocs(t *testing.T) {
	c := mustCatalog(t, loadFixture(t))
	var sink int
	allocs := testing.AllocsPerRun(1000, func() {
		h := c.Resolve("openrouter", "z-ai/glm-5.2")
		sink += h.Window()
		if h.Supports(ModalityImage) {
			sink++
		}
		sink += h.Budget().LongEdgePx
		h = c.Resolve("openrouter", "glm-5.2")
		sink += h.Window()
	})
	if allocs != 0 {
		t.Fatalf("Resolve allocated %v times per run, want 0", allocs)
	}
	if sink == 0 {
		t.Fatal("sink unused")
	}
}

// The package must not depend on the packages it serves (spec §3 Cluster
// Placement), nor on the generated wire types (pure domain package).
func TestCatalog_ImportBoundary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			`"github.com/elicify-ai/omnipus/pkg/providers"`,
			`"github.com/elicify-ai/omnipus/pkg/gateway`,
			`"github.com/elicify-ai/omnipus/pkg/agent`,
			`"github.com/elicify-ai/omnipus/pkg/api/generated"`,
		} {
			if strings.Contains(string(src), forbidden) {
				t.Fatalf("%s imports %s", e.Name(), forbidden)
			}
		}
	}
	if _, err := os.Stat("gen"); !os.IsNotExist(err) {
		t.Fatalf("gen/ must be deleted (T067-02), stat err=%v", err)
	}
}
