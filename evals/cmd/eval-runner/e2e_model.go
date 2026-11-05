package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// centralE2EModelPath is the repo's ONE committed source of truth for the
// real-LLM e2e/evals models: a primary and a fallback (provider, model,
// api_key_ref) pair. Every workflow job, the Fly CI runner
// (deploy/ci-worker/runci.sh, through scripts/e2e-model.sh) and this binary
// resolve their models from it; scripts/check-no-hardcoded-e2e-model.sh fails
// any model-id or provider-id literal that appears in live config or code
// outside it.
const centralE2EModelPath = "tests/e2e/e2e-model.json"

// E2EModelSide is one half of the central file: a provider id, a model id
// and the product credential name the provider row points at (api_key_ref).
type E2EModelSide struct {
	Provider string
	Model    string
	KeyRef   string // json api_key_ref
}

// Slug is the provider-prefixed model id the runner carries around
// ("<provider>/<model>"). The first slash is the split point, so a model id
// that itself contains slashes (a vendor/model OpenRouter id) round-trips.
func (s E2EModelSide) Slug() string { return s.Provider + "/" + s.Model }

// E2EModelPair is the parsed central file.
type E2EModelPair struct {
	Primary  E2EModelSide
	Fallback E2EModelSide
}

type e2eModelSideDoc struct {
	Provider  *string `json:"provider"`
	Model     *string `json:"model"`
	APIKeyRef *string `json:"api_key_ref"`
}

type e2eModelDoc struct {
	Primary  *e2eModelSideDoc `json:"primary"`
	Fallback *e2eModelSideDoc `json:"fallback"`
	Model    *json.RawMessage `json:"model"` // legacy shape marker
}

// centralE2EModelPair reads centralE2EModelPath from the working directory
// and fails closed: both objects present, every field non-empty after trim,
// and the primary pair different from the fallback pair. The legacy
// {"model": ...} document is rejected; only scripts/e2e-model.sh --legacy-ok
// (the Fly runner) still reads it.
func centralE2EModelPair() (E2EModelPair, error) {
	data, err := os.ReadFile(centralE2EModelPath)
	if err != nil {
		return E2EModelPair{}, fmt.Errorf("read %s: %w — run from the repo root, or set AGENT_MODEL and JUDGE_MODEL explicitly",
			centralE2EModelPath, err)
	}
	return parseE2EModelPair(data)
}

func parseE2EModelPair(data []byte) (E2EModelPair, error) {
	var doc e2eModelDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return E2EModelPair{}, fmt.Errorf("parse %s: %w", centralE2EModelPath, err)
	}
	if doc.Primary == nil && doc.Model != nil {
		return E2EModelPair{}, fmt.Errorf(`%s is the old shape {"model": ...}; it must carry .primary and .fallback, each with provider, model and api_key_ref`,
			centralE2EModelPath)
	}
	if doc.Primary == nil {
		return E2EModelPair{}, fmt.Errorf("%s: .primary object is missing", centralE2EModelPath)
	}
	if doc.Fallback == nil {
		return E2EModelPair{}, fmt.Errorf("%s: .fallback object is missing", centralE2EModelPath)
	}
	primary, err := doc.Primary.side("primary")
	if err != nil {
		return E2EModelPair{}, err
	}
	fallback, err := doc.Fallback.side("fallback")
	if err != nil {
		return E2EModelPair{}, err
	}
	if primary.Provider == fallback.Provider && primary.Model == fallback.Model {
		return E2EModelPair{}, fmt.Errorf("%s: primary and fallback are the same provider/model (%s); the fallback must differ",
			centralE2EModelPath, primary.Slug())
	}
	return E2EModelPair{Primary: primary, Fallback: fallback}, nil
}

func (d *e2eModelSideDoc) side(name string) (E2EModelSide, error) {
	field := func(key string, v *string) (string, error) {
		if v == nil || strings.TrimSpace(*v) == "" {
			return "", fmt.Errorf("%s: .%s.%s is missing, empty or whitespace-only", centralE2EModelPath, name, key)
		}
		return strings.TrimSpace(*v), nil
	}
	provider, err := field("provider", d.Provider)
	if err != nil {
		return E2EModelSide{}, err
	}
	model, err := field("model", d.Model)
	if err != nil {
		return E2EModelSide{}, err
	}
	keyRef, err := field("api_key_ref", d.APIKeyRef)
	if err != nil {
		return E2EModelSide{}, err
	}
	return E2EModelSide{Provider: provider, Model: model, KeyRef: keyRef}, nil
}

// centralE2EModel is the primary slug of the central file (the default for
// both the agent and the judge model knobs).
func centralE2EModel() (string, error) {
	pair, err := centralE2EModelPair()
	if err != nil {
		return "", err
	}
	return pair.Primary.Slug(), nil
}

// centralE2EFallbackModel is the fallback slug of the central file.
func centralE2EFallbackModel() (string, error) {
	pair, err := centralE2EModelPair()
	if err != nil {
		return "", err
	}
	return pair.Fallback.Slug(), nil
}

// centralE2EModelFileExists reports whether the central file is present in
// the working directory. A present-but-broken file is NOT "absent": callers
// that tolerate absence still fail closed on a corrupt file.
func centralE2EModelFileExists() (bool, error) {
	_, err := os.Stat(centralE2EModelPath)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("stat %s: %w", centralE2EModelPath, err)
	}
}

// sideFromSlug splits "<provider>/<model>" on the first slash. The key ref is
// the provider's credential name from the embedded catalog.
func sideFromSlug(slug string) (E2EModelSide, error) {
	slug = strings.TrimSpace(slug)
	provider, model, ok := strings.Cut(slug, "/")
	if !ok || provider == "" || model == "" {
		return E2EModelSide{}, fmt.Errorf("model %q is not a <provider>/<model> slug", slug)
	}
	side := E2EModelSide{Provider: provider, Model: model}
	if p, found := catalogProvider(provider); found && len(p.Env) > 0 {
		side.KeyRef = p.Env[0]
	}
	return side, nil
}

// ── Provider facts from the embedded catalog ────────────────────────────────

var embeddedCatalog = sync.OnceValues(func() (*catalog.Catalog, error) {
	return catalog.NewCatalog(catalog.EmbeddedSnapshot)
})

// catalogProvider looks a provider row up in the catalog compiled into this
// binary. Provider ids are data, never Go literals here (the e2e model guard
// scans evals/).
func catalogProvider(id string) (catalog.Provider, bool) {
	c, err := embeddedCatalog()
	if err != nil || c == nil {
		return catalog.Provider{}, false
	}
	return c.Provider(id)
}

// openRouterProviderID is the one provider whose eval key differs from the
// CI naming rule: the nightly evals have their own OpenRouter key, while the
// _CI OpenRouter keys belong to the pull-request e2e shards.
const openRouterProviderID = "openrouter"

// evalKeyEnvs returns the environment variables the eval-runner reads a
// provider's API key from, in order (first non-empty wins):
//
//	openrouter          -> OPENROUTER_API_KEY_EVAL
//	any other provider  -> <ENV>_CI, then <ENV>_EVAL
//
// where <ENV> is the provider's credential name in the catalog (for the
// MiniMax plan: MINIMAX_API_KEY_CI, then MINIMAX_API_KEY_EVAL). The MiniMax
// plan has one CI key that the nightly evals reuse (design §4.3).
func evalKeyEnvs(provider string) ([]string, error) {
	p, ok := catalogProvider(provider)
	if !ok || len(p.Env) == 0 || strings.TrimSpace(p.Env[0]) == "" {
		return nil, fmt.Errorf("provider %q has no credential name in the embedded catalog; the eval-runner cannot choose its API key", provider)
	}
	ref := strings.TrimSpace(p.Env[0])
	if provider == openRouterProviderID {
		return []string{ref + "_EVAL"}, nil
	}
	return []string{ref + "_CI", ref + "_EVAL"}, nil
}

// lookupEvalKey returns the first non-empty key for provider. role names the
// side ("primary", "fallback", "judge") for the error message. The error
// names every env var tried and never contains a value.
func lookupEvalKey(provider, role string) (string, error) {
	names, err := evalKeyEnvs(provider)
	if err != nil {
		return "", err
	}
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("%s API key for provider %q is not set: set %s", role, provider, strings.Join(names, " or "))
}
