package voice

import (
	"context"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

type Transcriber interface {
	Name() string
	Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error)
}

type TranscriptionResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

func supportsAudioTranscription(mc *config.ModelConfig) bool {
	if mc == nil {
		return false
	}
	return ProviderSupportsAudioTranscription(mc.Provider, mc.Protocol)
}

// ProviderSupportsAudioTranscription reports whether a configured provider row
// reaches an OpenAI-compatible transport — the only shape that can carry the
// base64 audio media payload NewAudioModelTranscriber sends.
//
// It asks the CATALOG for the row's wire protocol (ADR-067 FR-012) instead of
// matching a hand-typed list of ~30 vendor names against a `<protocol>/model`
// prefix. That list was a second, silently-drifting copy of the factory's
// switch: a provider added to the factory but forgotten here simply stopped
// offering voice, with no error anywhere. Exported so the Integrations
// catalogue (pkg/gateway) asks the same question the same way.
//
// TODO(#497): narrow this further by model id — not every model behind an
// OpenAI-compatible endpoint accepts audio input.
func ProviderSupportsAudioTranscription(providerID, protocol string) bool {
	if p := strings.TrimSpace(protocol); p != "" {
		return catalog.Protocol(p) == catalog.ProtocolOpenAICompatible
	}
	row, ok := providers.CatalogProvider(providerID)
	if !ok {
		return false
	}
	return row.Protocol == catalog.ProtocolOpenAICompatible
}

// DetectTranscriber inspects cfg and returns the appropriate Transcriber, or
// nil if no supported transcription provider is configured.
// secrets provides the resolved ElevenLabs API key without using os.Getenv.
func DetectTranscriber(cfg *config.Config, secrets credentials.SecretBundle) Transcriber {
	if modelName := strings.TrimSpace(cfg.Voice.ModelName); modelName != "" {
		modelCfg, err := cfg.FindModelConfigBySlug(modelName)
		if err != nil {
			slog.Warn("voice: configured transcription model not found in providers — transcription unavailable",
				"voice.model_name", modelName, "error", err)
			return nil
		}
		if supportsAudioTranscription(modelCfg) {
			return NewAudioModelTranscriber(modelCfg)
		}
	}

	// ElevenLabs voice config (supports Scribe STT). Key is resolved from the
	// SecretBundle so child processes never see it via /proc/<pid>/environ.
	if key := strings.TrimSpace(secrets.GetString(cfg.Voice.ElevenLabsAPIKeyRef)); key != "" {
		return NewElevenLabsTranscriber(key)
	}
	// Groq transcriber via the dedicated voice.groq_api_key_ref (FR-12.1
	// integrations surface). Resolved from the SecretBundle, same rationale as
	// ElevenLabs above. Takes precedence over the legacy providers-list fallback
	// so the Integrations picker is the single source of truth when set.
	if key := strings.TrimSpace(secrets.GetString(cfg.Voice.GroqAPIKeyRef)); key != "" {
		return NewGroqTranscriber(key)
	}
	// Fall back to any configured row on the `groq` provider. The old check
	// was a `groq/` prefix on the model string; ids are now the pair's
	// provider half, compared exactly (ADR-067 FR-036).
	for _, mc := range cfg.Providers {
		if strings.TrimSpace(mc.Provider) == "groq" && mc.APIKey() != "" {
			return NewGroqTranscriber(mc.APIKey())
		}
	}
	return nil
}
