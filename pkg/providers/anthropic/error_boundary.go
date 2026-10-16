package anthropicprovider

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// sdkErrorBoundary belongs to one request, including clients supplied by callers.
// Capture errors before the SDK reads them: the common boundary retains at most
// 8 KiB for classification and renders only its 512-byte preview. Give the SDK a
// valid, empty envelope instead of the original body (which may be truncated JSON)
// so its typed HTTP cause survives without retaining or rendering the full body.
// Successful/streamed responses pass through untouched.
type sdkErrorBoundary struct {
	baseURL string
	failure *common.ProviderError
}

func (b *sdkErrorBoundary) middleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	resp, err := next(req)
	if err != nil || resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return resp, err
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	failure := common.HandleErrorResponse(resp, b.baseURL)
	_ = resp.Body.Close()
	errors.As(failure, &b.failure)
	resp.Body = io.NopCloser(strings.NewReader("{}"))
	return resp, nil
}

func (b *sdkErrorBoundary) wrap(err error) error {
	if b.failure == nil {
		return err
	}
	b.failure.Err = errors.Join(err, b.failure.Err)
	return b.failure
}
