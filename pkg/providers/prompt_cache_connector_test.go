package providers

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	officialanthropic "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
)

// TestConnector_OneAnthropicPackage is TDD row 20. The factory builds the
// official connector. The hand-written package is not part of the build.
func TestConnector_OneAnthropicPackage(t *testing.T) {
	if _, err := os.Stat("anthropic_messages"); err == nil {
		t.Error("hand-written Anthropic package pkg/providers/anthropic_messages is still present")
	}
	withFixtureCatalog(t)
	p, _, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider:  "minimax",
		Model:     "MiniMax-M2.7",
		APIKeyRef: keyRef(t, "FACTORY_OFFICIAL_ANTHROPIC_KEY"),
	})
	if err != nil {
		t.Fatalf("CreateProviderFromConfig: %v", err)
	}
	if _, ok := p.(*officialanthropic.Provider); !ok {
		t.Fatalf("provider = %T, want the official *anthropic.Provider", p)
	}
}
