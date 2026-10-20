package anthropicprovider

import (
	"encoding/json"
	"errors"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
)

// Opening input/cache counters describe the request once. Later deltas can
// carry incremental input counts, but output is the latest cumulative value.
func restoreOpeningUsage(msg *anthropic.Message, opening *anthropic.Usage) {
	if opening == nil {
		return
	}
	if opening.JSON.InputTokens.Valid() {
		msg.Usage.InputTokens = opening.InputTokens
	}
	if opening.JSON.CacheReadInputTokens.Valid() {
		msg.Usage.CacheReadInputTokens = opening.CacheReadInputTokens
	}
	if opening.JSON.CacheCreationInputTokens.Valid() {
		msg.Usage.CacheCreationInputTokens = opening.CacheCreationInputTokens
	}
	msg.Usage.CacheCreation = opening.CacheCreation
}

func hasMessageUsage(msg *anthropic.Message) bool {
	return msg.JSON.Usage.Valid() || msg.Usage.InputTokens > 0 || msg.Usage.OutputTokens > 0 ||
		msg.Usage.CacheReadInputTokens > 0 || msg.Usage.CacheCreationInputTokens > 0
}

func messageUsage(msg *anthropic.Message) (*UsageInfo, error) {
	// Accumulated fields contain the latest output count; RawJSON still
	// describes message_start and must not be used for final accounting.
	raw, err := json.Marshal(msg.Usage)
	if err != nil {
		return nil, err
	}
	return promptcache.ParseUsage("anthropic", raw)
}

// Every failed exit keeps known usage, without replacing its original error
// identity (including cancellation/stall). Tool-argument errors already carry
// their own usage evidence and retain that existing recovery contract.
func failedStreamUsage(err error, msg *anthropic.Message, opening *anthropic.Usage) error {
	if err == nil {
		return nil
	}
	var argumentsErr *common.ToolArgumentsError
	if errors.As(err, &argumentsErr) {
		return err
	}
	restoreOpeningUsage(msg, opening)
	if !hasMessageUsage(msg) {
		return err
	}
	usage, usageErr := messageUsage(msg)
	if usageErr != nil {
		return errors.Join(err, usageErr)
	}
	var streamErr *common.StreamProviderError
	if errors.As(err, &streamErr) {
		streamErr.Usage = usage
		return err
	}
	code := "error"
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		code = string(apiErr.Type())
	}
	return errors.Join(err, &common.StreamProviderError{
		Code: code, Message: err.Error(), Usage: usage,
	})
}
