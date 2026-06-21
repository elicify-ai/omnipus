//go:build !cgo

package gateway

import (
	"net/http"
	"strings"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// rest_voice.go — GET /api/v1/voice/provider.
//
// Returns a descriptor of the active voice (TTS) provider so the SPA can decide
// which voice widget variant (dropdown / free-text / disabled) to render in the
// agent edit slide-over (per agent-form spec §4.10.1).
//
// Provider identification (read from cfg.Voice via a.agentLoop.GetConfig()):
//
//   - ModelName starts with "openai/" (or matches openai/tts-*) → "openai-tts",
//     voices = the 6-voice OpenAI TTS fixed list.
//   - ElevenLabsAPIKeyRef is set (non-empty) → "elevenlabs",
//     voices_endpoint = ElevenLabs paginated list.
//   - Otherwise → provider = nil; SPA renders the field disabled with the
//     "Voice provider unavailable" tooltip (per voice-provider-detect.ts).
//
// We do NOT cache *config.Config on restAPI — the agentLoop's snapshot
// middleware is the source of truth for hot-reload consistency.

var openAITTSVoices = []string{
	"alloy", "echo", "fable", "onyx", "nova", "shimmer",
}

const elevenLabsVoicesEndpoint = "https://api.elevenlabs.io/v1/voices"

const (
	voiceProviderOpenAI     = "openai-tts"
	voiceProviderElevenLabs = "elevenlabs"
)

// HandleVoiceProvider handles GET /api/v1/voice/provider. Returns the active
// voice provider's identity and (optionally) its voices enum.
func (a *restAPI) HandleVoiceProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	resp := a.describeVoiceProvider()
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, resp)
}

// describeVoiceProvider returns the wire-shape response for /voice/provider.
func (a *restAPI) describeVoiceProvider() gen.VoiceProvider {
	if a == nil {
		return gen.VoiceProvider{Provider: nil}
	}

	// Resolve via agentLoop (NOT a cached config pointer) so hot-reload
	// changes to cfg.Voice propagate to subsequent requests.
	cfg := a.agentLoop.GetConfig()

	modelName := cfg.Voice.ModelName
	hasElevenLabs := cfg.Voice.ElevenLabsAPIKeyRef != ""

	switch {
	case strings.HasPrefix(modelName, "openai/") || strings.HasPrefix(modelName, "openai-tts"):
		p := voiceProviderOpenAI
		v := openAITTSVoices
		return gen.VoiceProvider{
			Provider: &p,
			Voices:   &v,
		}

	case hasElevenLabs:
		p := voiceProviderElevenLabs
		ep := elevenLabsVoicesEndpoint
		return gen.VoiceProvider{
			Provider:       &p,
			VoicesEndpoint: &ep,
		}

	default:
		return gen.VoiceProvider{Provider: nil}
	}
}
