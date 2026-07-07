package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// multipartFormField is one ordered form field written to a multipart
// transcription request body, after the "file" part. Order is preserved
// exactly as each provider's pre-refactor implementation wrote it.
type multipartFormField struct {
	Name  string
	Value string
}

// multipartTranscribeSpec captures the provider-specific pieces of a
// multipart-upload speech-to-text request: endpoint URL, auth header, extra
// form fields, and the labels used in log/error text. Everything else (file
// open/stat, multipart encoding, request construction, response read and
// decode) is identical across providers and lives in doMultipartTranscribe.
type multipartTranscribeSpec struct {
	// APIName labels the provider in the "Sending transcription request to
	// <APIName> API" / "Received response from <APIName> API" debug logs
	// (e.g. "Groq", "ElevenLabs").
	APIName string
	// ErrorPrefix is prepended to the "API error" log message and the
	// returned error text. Preserved verbatim from each provider's
	// pre-refactor error text: "" for Groq ("API error (status %d): %s"),
	// "ElevenLabs " for ElevenLabs ("ElevenLabs API error (status %d): %s").
	ErrorPrefix string
	// URL is the full transcription endpoint (apiBase + path).
	URL string
	// AuthHeaderName/AuthHeaderValue set the provider's auth header, e.g.
	// ("Authorization", "Bearer sk-...") for Groq or ("Xi-Api-Key", "sk_...")
	// for ElevenLabs.
	AuthHeaderName  string
	AuthHeaderValue string
	// FormFields are additional multipart fields written, in order, after
	// the "file" part (e.g. model/response_format for Groq, model_id for
	// ElevenLabs).
	FormFields []multipartFormField
}

// doMultipartTranscribe performs the shared file-open -> multipart-encode ->
// HTTP POST -> JSON-decode flow common to every multipart-upload
// transcription provider (Groq, ElevenLabs, ...). Only the values in spec
// vary by provider; the request/response mechanics are functionally
// equivalent to what each provider's hand-written implementation did before
// this was extracted, with two intentional ElevenLabs-side observability
// improvements: it now also logs the copy-progress and WriteField-failure
// lines that Groq's implementation already had but ElevenLabs's didn't.
func doMultipartTranscribe(
	ctx context.Context,
	httpClient *http.Client,
	audioFilePath string,
	spec multipartTranscribeSpec,
) (*TranscriptionResponse, error) {
	audioFile, err := os.Open(audioFilePath)
	if err != nil {
		logger.ErrorCF("voice", "Failed to open audio file", map[string]any{"path": audioFilePath, "error": err})
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer audioFile.Close()

	fileInfo, err := audioFile.Stat()
	if err != nil {
		logger.ErrorCF("voice", "Failed to get file info", map[string]any{"path": audioFilePath, "error": err})
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	logger.DebugCF("voice", "Audio file details", map[string]any{
		"size_bytes": fileInfo.Size(),
		"file_name":  filepath.Base(audioFilePath),
	})

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	part, err := writer.CreateFormFile("file", filepath.Base(audioFilePath))
	if err != nil {
		logger.ErrorCF("voice", "Failed to create form file", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	copied, err := io.Copy(part, audioFile)
	if err != nil {
		logger.ErrorCF("voice", "Failed to copy file content", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	logger.DebugCF("voice", "File copied to request", map[string]any{"bytes_copied": copied})

	for _, field := range spec.FormFields {
		if fieldErr := writer.WriteField(field.Name, field.Value); fieldErr != nil {
			logger.ErrorCF(
				"voice",
				fmt.Sprintf("Failed to write %s field", field.Name),
				map[string]any{"error": fieldErr},
			)
			return nil, fmt.Errorf("failed to write %s field: %w", field.Name, fieldErr)
		}
	}

	if err = writer.Close(); err != nil {
		logger.ErrorCF("voice", "Failed to close multipart writer", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.URL, &requestBody)
	if err != nil {
		logger.ErrorCF("voice", "Failed to create request", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(spec.AuthHeaderName, spec.AuthHeaderValue)

	logger.DebugCF("voice", fmt.Sprintf("Sending transcription request to %s API", spec.APIName), map[string]any{
		"url":                spec.URL,
		"request_size_bytes": requestBody.Len(),
		"file_size_bytes":    fileInfo.Size(),
	})

	resp, err := httpClient.Do(req)
	if err != nil {
		logger.ErrorCF("voice", "Failed to send request", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.ErrorCF("voice", "Failed to read response", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.ErrorCF("voice", spec.ErrorPrefix+"API error", map[string]any{
			"status_code": resp.StatusCode,
			"response":    string(body),
		})
		return nil, fmt.Errorf("%sAPI error (status %d): %s", spec.ErrorPrefix, resp.StatusCode, string(body))
	}

	logger.DebugCF("voice", fmt.Sprintf("Received response from %s API", spec.APIName), map[string]any{
		"status_code":         resp.StatusCode,
		"response_size_bytes": len(body),
	})

	var result TranscriptionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		logger.ErrorCF("voice", "Failed to unmarshal response", map[string]any{"error": err})
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}
