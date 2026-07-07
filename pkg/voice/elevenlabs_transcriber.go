package voice

import (
	"context"
	"net/http"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// ElevenLabsTranscriber uses the ElevenLabs Scribe API for speech-to-text.
type ElevenLabsTranscriber struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
}

func NewElevenLabsTranscriber(apiKey string) *ElevenLabsTranscriber {
	logger.DebugCF("voice", "Creating ElevenLabs transcriber", map[string]any{"has_api_key": apiKey != ""})

	return &ElevenLabsTranscriber{
		apiKey:  apiKey,
		apiBase: "https://api.elevenlabs.io",
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (t *ElevenLabsTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error) {
	logger.InfoCF("voice", "Starting ElevenLabs transcription", map[string]any{"audio_file": audioFilePath})

	result, err := doMultipartTranscribe(ctx, t.httpClient, audioFilePath, multipartTranscribeSpec{
		APIName:         "ElevenLabs",
		ErrorPrefix:     "ElevenLabs ",
		URL:             t.apiBase + "/v1/speech-to-text",
		AuthHeaderName:  "Xi-Api-Key",
		AuthHeaderValue: t.apiKey,
		FormFields: []multipartFormField{
			{Name: "model_id", Value: "scribe_v1"},
		},
	})
	if err != nil {
		return nil, err
	}

	logger.InfoCF("voice", "ElevenLabs transcription completed successfully", map[string]any{
		"text_length":           len(result.Text),
		"language":              result.Language,
		"transcription_preview": utils.Truncate(result.Text, 50),
	})

	return result, nil
}

func (t *ElevenLabsTranscriber) Name() string {
	return "elevenlabs"
}
