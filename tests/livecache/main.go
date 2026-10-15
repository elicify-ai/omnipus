// Command livecache is the opt-in D22 two-call landing proof. It never discovers
// credentials and never runs a live request without LIVE_CACHE_PROVIDER.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/google/uuid"
)

type callReceipt struct { // not-wire-format: local, operator-requested landing evidence; not a gateway/SPA payload
	Call           int                  `json:"call"`
	Usage          *providers.UsageInfo `json:"usage"`
	BillingClass   string               `json:"billing_class"`
	UnpricedTokens int                  `json:"unpriced_tokens"`
	CostUSD        *float64             `json:"cost_usd,omitempty"`
}

type receipt struct { // not-wire-format: local, operator-requested landing evidence only
	Account  string        `json:"account"`
	Provider string        `json:"provider"`
	Model    string        `json:"model"`
	Commit   string        `json:"commit"`
	Calls    []callReceipt `json:"calls"`
	Pass     bool          `json:"pass"`
}

func main() {
	if os.Getenv("LIVE_CACHE_PROVIDER") == "" {
		fmt.Println("SKIP: LIVE_CACHE_PROVIDER is absent; no network request made")
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, logger.ScrubSensitiveValues(err.Error()))
		os.Exit(1)
	}
}

func run() error {
	providerID, model := os.Getenv("LIVE_CACHE_PROVIDER"), os.Getenv("LIVE_CACHE_MODEL")
	account, path := os.Getenv("LIVE_CACHE_ACCOUNT"), os.Getenv("LIVE_CACHE_RECEIPT")
	if model == "" || account == "" || path == "" {
		return errors.New("LIVE_CACHE_MODEL, LIVE_CACHE_ACCOUNT and LIVE_CACHE_RECEIPT are required")
	}
	switch providerID {
	case "openrouter", "zai-coding-plan", "minimax-coding-plan", "kimi-code-plan-global", "openai-chatgpt":
	default:
		return errors.New("provider is not one of the five D22 account routes")
	}
	secret := os.Getenv("LIVE_CACHE_API_KEY")
	if providerID == "openai-chatgpt" {
		secret = os.Getenv("LIVE_CACHE_ACCESS_TOKEN")
	}
	if secret == "" {
		return errors.New("explicit LIVE_CACHE_API_KEY or ChatGPT LIVE_CACHE_ACCESS_TOKEN is required")
	}
	restore := logger.SetSensitiveValueReplacer(strings.NewReplacer(secret, "[FILTERED]"))
	defer restore()
	commit, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return errors.New("cannot identify the checkout commit")
	}
	dirty, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil || len(dirty) > 0 {
		return errors.New("live proof requires a clean committed checkout")
	}
	cfg := &config.ModelConfig{Provider: providerID, Model: model, APIKeyRef: "LIVE_CACHE_API_KEY", APIBase: os.Getenv("LIVE_CACHE_BASE_URL")}
	if providerID == "openai-chatgpt" {
		closeStore, err := installSubscriptionCredential(secret)
		if err != nil {
			return err
		}
		defer closeStore()
		cfg.AuthMethod = config.AuthMethodSignIn
	}
	p, actualModel, err := providers.CreateProviderFromConfig(cfg)
	if err != nil {
		return errors.New("provider factory refused the supplied configuration")
	}
	result := receipt{Account: account, Provider: providerID, Model: actualModel, Commit: strings.TrimSpace(string(commit)), Calls: make([]callReceipt, 0, 2)}
	messages := []providers.Message{
		{Role: "system", Content: "Return only OK. The following conversation is cache verification data."},
		{Role: "user", Content: strings.Repeat("Stable cache verification data: a provider reuses an identical conversation prefix.\n", 600)},
	}
	opts := map[string]any{"prompt_cache_key": uuid.NewString(), "max_tokens": 512}
	for call := 1; call <= 2; call++ {
		response, callErr := p.Chat(context.Background(), messages, nil, actualModel, opts)
		if callErr != nil || response == nil {
			return fmt.Errorf("provider call %d failed; no provider body or credential is included in this diagnostic", call)
		}
		var usage providers.UsageInfo
		if response.Usage != nil {
			usage = *response.Usage
		}
		price := promptcache.Price(promptcache.PriceInput{ProviderID: providerID, Cost: providers.ProviderCatalog().Resolve(providerID, actualModel).Cost(), Usage: usage})
		item := callReceipt{Call: call, Usage: response.Usage, BillingClass: price.BillingClass, UnpricedTokens: price.UnpricedTokens}
		if price.BillingClass == promptcache.BillingClassPriced {
			item.CostUSD = &price.Cost
		}
		result.Calls = append(result.Calls, item)
	}
	result.Pass = result.Calls[1].Usage != nil && result.Calls[1].Usage.CacheReadTokens > 0
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if !result.Pass {
		return errors.New("cache proof failed: second call has no cached tokens (HTTP success is not a pass)")
	}
	fmt.Printf("PASS: receipt saved to %s\n", path)
	return nil
}

// A fresh temporary encrypted store is the same sanctioned OAuth store seam
// the real factory reads. No operator home, auth.json or existing key is opened.
func installSubscriptionCredential(accessToken string) (func(), error) {
	accountID := os.Getenv("LIVE_CACHE_ACCOUNT_ID")
	if accountID == "" {
		return nil, errors.New("ChatGPT LIVE_CACHE_ACCOUNT_ID is required")
	}
	home := os.Getenv("OMNIPUS_HOME")
	if home == "" {
		return nil, errors.New("ChatGPT proof requires an explicit temporary OMNIPUS_HOME")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(home, "omnipus-livecache-")
	if err != nil {
		return nil, err
	}
	store := credentials.NewStore(filepath.Join(dir, "credentials.json"))
	cleanup := func() { providers.SetDefaultCredentialStore(nil); store.Close(); _ = os.RemoveAll(dir) }
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		cleanup()
		return nil, err
	}
	if err := store.UnlockWithKey(key); err != nil {
		cleanup()
		return nil, err
	}
	for i := range key {
		key[i] = 0
	}
	if err := providers.WriteStoreOAuthCredential("openai-chatgpt", store, providers.OAuthCredential{AccessToken: accessToken, AccountID: accountID, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		cleanup()
		return nil, err
	}
	providers.SetDefaultCredentialStore(store)
	return cleanup, nil
}
