//go:build !cgo

package gateway

import (
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// rest_voice.go — GET /api/v1/voice/provider.
//
// Returns a descriptor of the active voice (TTS) provider so the SPA can decide
// which voice widget variant (dropdown / free-text / disabled) to render in the
// agent edit slide-over (per agent-form spec §4.10.1).
//
// Source of truth: pkg/config.VoiceConfig (engine-level). The handler reads the
// voice provider identifier from the engine config and returns:
//
//	{
//	  "provider":   string | null,        // e.g. "openai-tts", "elevenlabs", "piper", null
//	  "voices":     string[] | undefined,  // populated for providers with an enum
//	  "voices_endpoint": string | null     // optional paginated endpoint URL
//	}
//
// When no voice provider is configured, `provider` is null and the SPA renders
// the field as disabled (the "Voice provider unavailable" tooltip).

// openAITTSVoices is the fixed voices enum exposed by OpenAI's TTS API. The SPA
// renders a <select> with this list when provider === "openai-tts". Keep in
// sync with the OpenAI TTS docs; this is intentionally not inlined into the
// schema because it's a static list of allowed values, not a per-call response
// shape.
var openAITTSVoices = []string{
	"alloy", "echo", "fable", "onyx", "nova", "shimmer",
}

// elevenLabsVoicesEndpoint is the paginated endpoint URL for ElevenLabs. We
// don't ship the voice list (paginated, dynamic) — the SPA hits the endpoint
// directly when provider === "elevenlabs".
const elevenLabsVoicesEndpoint = "https://api.elevenlabs.io/v1/voices"

// HandleVoiceProvider handles GET /api/v1/voice/provider. Returns the active
// voice provider's identity and (optionally) its voices enum.
func (a *restAPI) HandleVoiceProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	resp := a.describeVoiceProvider()
	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, http.StatusOK, resp); err != nil {
		jsonErr(w, http.StatusInternalServerError, "encode voice provider response: "+err.Error())
		return
	}
}

// describeVoiceProvider returns the wire-shape response for /voice/provider.
// Pulled out of the HTTP handler so it's trivially testable.
func (a *restAPI) describeVoiceProvider() gen.VoiceProvider {
	if a == nil || a.config == nil {
		// No config (test scaffolding) — return a "no provider" descriptor.
		return gen.VoiceProvider{Provider: nil}
	}

	provider := a.config.Voice.ModelName
	if provider == "" {
		// No voice provider configured globally.
		return gen.VoiceProvider{Provider: nil}
	}

	resp := gen.VoiceProvider{
		Provider: &provider,
	}

	switch provider {
	case "openai-tts":
		v := openAITTSVoices
		resp.Voices = &v
	case "elevenlabs":
		ep := elevenLabsVoicesEndpoint
		resp.VoicesEndpoint = &ep
	case "google-tts", "azure-speech", "piper":
		// Future: per-provider voice lists. For v0.1.0 the SPA falls back to
		// free-text when these are configured (no enum surfaced yet).
	}

	return resp
}
