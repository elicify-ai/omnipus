package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// WU-24 CHECK F4: DS-4 #14b is a retained HTTP control, not an
// exemption for a removed subprocess model provider. The independent
// fixture defines HTTP ChatGPT's catalog context window as 272000.
func TestResolveContextWindow_OpenAIChatGPTUsesCatalogWindow(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil)
	cfg := windowTestConfig()
	got := ResolveWindow(cfg, "openai-chatgpt", "gpt-5.4", "mia")
	assert.Equal(t, 272_000, got.Window, "HTTP turns must retain their catalog context budget")
	assert.Equal(t, WindowSourceCatalog, got.Source)
	assert.False(t, got.Exempt, "an HTTP model provider does not manage native-turn context for Omnipus")
	assert.False(t, got.Unknown)
}
