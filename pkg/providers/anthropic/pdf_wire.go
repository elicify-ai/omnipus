package anthropicprovider

// pdf_wire.go — the two optional transport capabilities issue #1968 adds
// (spec FR-11, FR-12): the wire-message grouping the agent consults for
// max_documents_per_message, and the exact serialized request size it
// consults for max_request_bytes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go/option"
)

// errBodyMeasured stops the SDK request after its body was captured; the
// measurement never reaches the network.
var errBodyMeasured = errors.New("anthropic: request body measured, not sent")

// SerializedRequestBytes returns the exact byte length of the Messages API
// body Chat (stream false) or ChatStream (stream true) would send for this
// request. The SDK marshals the body as it would for a real call; a
// middleware captures it and stops before any network I/O.
func (p *Provider) SerializedRequestBytes(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	stream bool,
) (int, error) {
	params, err := buildParams(messages, tools, model, options)
	if err != nil {
		return 0, err
	}
	p.markCache(&params, messages, model)
	size := -1
	var readErr error
	capture := option.WithMiddleware(func(req *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		if req.Body == nil {
			size = 0
			return nil, errBodyMeasured
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			readErr = err
			return nil, errBodyMeasured
		}
		size = len(body)
		return nil, errBodyMeasured
	})
	opts := []option.RequestOption{capture, option.WithMaxRetries(0)}
	ctx := context.Background()
	var callErr error
	// OAuth/setup tokens always stream (see Chat).
	if stream || p.tokenSource != nil {
		s := p.client.Messages.NewStreaming(ctx, params, opts...)
		callErr = s.Err()
		if closeErr := s.Close(); closeErr != nil && callErr == nil {
			callErr = closeErr
		}
	} else {
		_, callErr = p.client.Messages.New(ctx, params, opts...)
	}
	if readErr != nil {
		return 0, fmt.Errorf("anthropic: read request body: %w", readErr)
	}
	if size < 0 {
		return 0, fmt.Errorf("anthropic: request body was not built: %w", callErr)
	}
	return size, nil
}

// WireMessageGroups reports which internal messages share one Messages API
// message: consecutive tool results travel in one user message
// (appendToolResult); every other message is its own wire message.
func (p *Provider) WireMessageGroups(messages []Message) [][]int {
	return groupConsecutive(messages, func(m Message) bool {
		return m.Role == "tool" || (m.Role == "user" && m.ToolCallID != "")
	})
}

// groupConsecutive puts each run of consecutive messages that satisfy
// merged into one group and every other message into its own group.
func groupConsecutive(messages []Message, merged func(Message) bool) [][]int {
	var groups [][]int
	for i := 0; i < len(messages); i++ {
		if merged(messages[i]) && len(groups) > 0 {
			prev := groups[len(groups)-1]
			if merged(messages[prev[len(prev)-1]]) && prev[len(prev)-1] == i-1 {
				groups[len(groups)-1] = append(prev, i)
				continue
			}
		}
		groups = append(groups, []int{i})
	}
	return groups
}
