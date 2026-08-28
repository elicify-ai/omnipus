package voice

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestVoiceTranscriber_ConstructsViaProtocolDispatch — ADR-067 §7 regression
// requirement #4. NewAudioModelTranscriber is one of the two
// CreateProviderFromConfig callers the impact table lists with no existing
// coverage: nothing asserted that it still builds a provider after the
// factory collapsed to protocol dispatch, and a nil return here disables
// voice transcription SILENTLY (the constructor logs and returns nil).
func TestVoiceTranscriber_ConstructsViaProtocolDispatch(t *testing.T) {
	t.Setenv("VOICE_DISPATCH_TEST_KEY", "sk-test")

	t.Run("catalog row builds from the catalog URL", func(t *testing.T) {
		tr := NewAudioModelTranscriber(&config.ModelConfig{
			Provider:  "openai",
			Model:     "gpt-4o-audio-preview",
			APIKeyRef: "VOICE_DISPATCH_TEST_KEY",
		})
		if tr == nil {
			t.Fatal("NewAudioModelTranscriber returned nil — voice would be silently unavailable")
		}
		if tr.Name() != "audio-model" {
			t.Errorf("Name() = %q, want audio-model", tr.Name())
		}
		if tr.modelID != "gpt-4o-audio-preview" {
			t.Errorf("modelID = %q, want the bare catalog id verbatim", tr.modelID)
		}
		if _, ok := tr.provider.(*providers.HTTPProvider); !ok {
			t.Errorf("provider = %T, want *providers.HTTPProvider", tr.provider)
		}
	})

	t.Run("an unknown provider yields no transcriber", func(t *testing.T) {
		if tr := NewAudioModelTranscriber(&config.ModelConfig{
			Provider:  "z-ai",
			Model:     "glm-5.2",
			APIKeyRef: "VOICE_DISPATCH_TEST_KEY",
		}); tr != nil {
			t.Error("a retired provider spelling must not construct a transcriber")
		}
	})
}

// TestProviderSupportsAudioTranscription_FromCatalog — the vendor-name list
// this replaced was a second copy of the factory's switch, and it drifted:
// a provider added to the factory but forgotten here silently lost voice.
func TestProviderSupportsAudioTranscription_FromCatalog(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		protocol string
		want     bool
	}{
		{"catalog openai-compatible row", "openai", "", true},
		{"catalog anthropic row", "anthropic", "", false},
		{"catalog google row", "google", "", false},
		{"custom row declaring openai-compatible", "my-proxy", "openai-compatible", true},
		{"custom row declaring anthropic", "my-proxy", "anthropic", false},
		{"unknown provider", "z-ai", "", false},
		{"empty provider", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProviderSupportsAudioTranscription(tt.provider, tt.protocol); got != tt.want {
				t.Errorf("ProviderSupportsAudioTranscription(%q, %q) = %v, want %v",
					tt.provider, tt.protocol, got, tt.want)
			}
		})
	}
}
