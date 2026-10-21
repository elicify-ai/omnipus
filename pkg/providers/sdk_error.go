package providers

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/openai/openai-go/v3"
)

// normalizeSDKError preserves the SDK's HTTP facts at the same bounded-body
// boundary as raw-HTTP providers. The SDK decodes only the inner error object;
// restore its envelope so the shared structured-code readers see the code.
// Keep the original cause for errors.Is/As and never consume Response.Body.
func normalizeSDKError(err error) error {
	var pe *common.ProviderError
	if errors.As(err, &pe) {
		return err
	}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	body := apiErr.RawJSON()
	if body != "" {
		body = `{"error":` + body + `}`
	}
	var headers http.Header
	if apiErr.Response != nil {
		headers = apiErr.Response.Header
	}
	resp := &http.Response{
		StatusCode: apiErr.StatusCode,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	defer resp.Body.Close()
	boundaryErr := common.HandleErrorResponse(resp, "")
	if errors.As(boundaryErr, &pe) {
		pe.Err = err
	}
	return boundaryErr
}
