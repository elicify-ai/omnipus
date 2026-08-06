package channels

import (
	"fmt"
	"net/http"
)

// ClassifySendError wraps a raw error with the appropriate sentinel based on
// an HTTP status code. Channels that perform HTTP API calls should use this
// in their Send path.
func ClassifySendError(statusCode int, rawErr error) error {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %v", ErrRateLimit, rawErr)
	case statusCode >= 500:
		return fmt.Errorf("%w: %v", ErrTemporary, rawErr)
	case statusCode >= 400:
		return fmt.Errorf("%w: %v", ErrSendFailed, rawErr)
	default:
		return rawErr
	}
}

// ClassifyNetError wraps a network/timeout error as ErrTemporary.
func ClassifyNetError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrTemporary, err)
}

// ClassifyMediaSendError classifies a mid-loop media send/upload API failure
// for MediaSender.SendMedia implementations that deliver a message's parts
// one at a time (Telegram, Feishu, Matrix, Slack — see the CRITICAL retry
// finding on branch sendfile-fix). sentCount is the number of parts already
// successfully delivered to the platform before this failure.
//
// Manager.sendMediaWithRetry has no per-part resume capability: a retry
// re-invokes SendMedia with the same, unmutated bus.OutboundMediaMessage, so
// the whole part list — including any parts already delivered — is
// resolved and sent again from the top. That is safe only when nothing has
// been sent yet:
//
//   - sentCount > 0: at least one part already reached the platform.
//     Retrying would re-deliver it, duplicating it for the user. The
//     failure is classified as permanent (ErrSendFailed) so the manager's
//     retry loop does not re-attempt the message at all. This mirrors the
//     existing convention for a partial LOCAL resolve/open failure (which
//     is caught by the post-loop sentCount<len(msg.Parts) guard in each
//     channel's SendMedia).
//   - sentCount == 0: nothing from this message has been sent yet, so a
//     bare retry of the whole message cannot duplicate anything. The
//     failure keeps the normal ErrTemporary classification so
//     sendMediaWithRetry backs off and retries.
//
// Either way, err is preserved in the chain (%w) alongside the sentinel so
// the platform's actual failure reason (e.g. a permanent 4xx such as "file
// too large" or an unsupported MIME type) remains visible in the returned
// error's message and to errors.Is/As callers, instead of being flattened
// to a bare "temporary failure" that burns retries and tells the user
// nothing useful.
func ClassifyMediaSendError(channel string, sentCount int, err error) error {
	if err == nil {
		return nil
	}
	if sentCount > 0 {
		return fmt.Errorf("%s send media: %w: %w", channel, ErrSendFailed, err)
	}
	return fmt.Errorf("%s send media: %w: %w", channel, ErrTemporary, err)
}
