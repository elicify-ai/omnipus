package voice

import (
	"context"
	"net/http"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

type GroqTranscriber struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
}

func NewGroqTranscriber(apiKey string) *GroqTranscriber {
	logger.DebugCF("voice", "Creating Groq transcriber", map[string]any{"has_api_key": apiKey != ""})

	apiBase := "https://api.groq.com/openai/v1"
	return &GroqTranscriber{
		apiKey:  apiKey,
		apiBase: apiBase,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (t *GroqTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error) {
	logger.InfoCF("voice", "Starting transcription", map[string]any{"audio_file": audioFilePath})

	result, err := doMultipartTranscribe(ctx, t.httpClient, audioFilePath, multipartTranscribeSpec{
		APIName:         "Groq",
		ErrorPrefix:     "",
		URL:             t.apiBase + "/audio/transcriptions",
		AuthHeaderName:  "Authorization",
		AuthHeaderValue: "Bearer " + t.apiKey,
		FormFields: []multipartFormField{
			{Name: "model", Value: "whisper-large-v3"},
			{Name: "response_format", Value: "json"},
		},
	})
	if err != nil {
		return nil, err
	}

	logger.InfoCF("voice", "Transcription completed successfully", map[string]any{
		"text_length":           len(result.Text),
		"language":              result.Language,
		"duration_seconds":      result.Duration,
		"transcription_preview": utils.Truncate(result.Text, 50),
	})

	return result, nil
}

func (t *GroqTranscriber) Name() string {
	return "groq"
}
