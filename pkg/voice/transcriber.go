package voice

import (
	"context"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

func supportsAudioTranscription(model string) bool {
	return ModelSupportsAudioTranscription(model)
}

// ModelSupportsAudioTranscription reports whether a model identifier (in
// protocol-prefixed form, e.g. "openai/gpt-4o-audio-preview") is routed through
// an OpenAI-compatible provider path capable of supplying the audio media payload
// shape expected by NewAudioModelTranscriber. Exported so the Integrations
// catalog (pkg/gateway) can detect the active voice transcriber without
// duplicating the protocol list.
func ModelSupportsAudioTranscription(model string) bool {
	protocol, _ := providers.ExtractProtocol(model)

	switch protocol {
	case "openai", "azure", "azure-openai",
		"litellm", "openrouter", "groq", "zhipu", "gemini", "nvidia",
		"ollama", "moonshot", "shengsuanyun", "deepseek", "cerebras",
		"vivgrid", "volcengine", "vllm", "qwen", "qwen-intl", "qwen-international", "dashscope-intl",
		"qwen-us", "dashscope-us", "mistral", "avian", "minimax", "longcat", "modelscope", "novita",
		"coding-plan", "alibaba-coding", "qwen-coding":
		// These protocols all go through the OpenAI-compatible or Azure provider path in
		// providers.CreateProviderFromConfig, so they are the only ones that can supply
		// the audio media payload shape expected by NewAudioModelTranscriber.

		// TODO(#497): Further restrict this by modelID, since not every model
		// under these protocols supports audio transcription.
		return true
	default:
		return false
	}
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
		if supportsAudioTranscription(modelCfg.Model) {
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
	// Fall back to any model-list entry that uses the groq/ protocol.
	for _, mc := range cfg.Providers {
		if strings.HasPrefix(mc.Model, "groq/") && mc.APIKey() != "" {
			return NewGroqTranscriber(mc.APIKey())
		}
	}
	return nil
}
