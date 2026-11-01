package discord

// Discord target look-up and started-message delivery (channels-spec-r8
// T-9, FR-013, FR-019, FR-046, NB-23).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// wrappedCallTimeout bounds one wrapper-managed Discord REST call, including
// the rate-limit bucket wait and the wait for an in-flight slot (T-9).
const wrappedCallTimeout = 5 * time.Second

// wrappedCallSlots is the process-wide bound on wrapper-managed Discord
// calls in flight (look-ups and send-time DM opening share it, T-9). An
// abandoned call keeps its slot until discordgo returns, so abandoned
// goroutines stay bounded.
var wrappedCallSlots = make(chan struct{}, 2)

// errWrappedCallUnavailable is the wrapper's answer when the deadline or the
// caller's context ended first (slot wait, bucket wait or HTTP work).
var errWrappedCallUnavailable = errors.New("discord call did not complete in time")

// wrappedCall runs one discordgo REST call through the T-9 wrapper: its own
// goroutine, one shared in-flight slot, a 5-second deadline that covers the
// limiter's admission wait, and request options that turn off discordgo's
// rate-limit and 502 retries. A late result is dropped: nothing continues
// from it.
func wrappedCall[T any](ctx context.Context, call func(options ...discordgo.RequestOption) (T, error)) (T, error) {
	var zero T
	callCtx, cancel := context.WithTimeout(ctx, wrappedCallTimeout)
	defer cancel()
	select {
	case wrappedCallSlots <- struct{}{}:
	case <-callCtx.Done():
		return zero, fmt.Errorf("%w: no free call slot: %w", errWrappedCallUnavailable, callCtx.Err())
	}
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() { <-wrappedCallSlots }()
		value, err := call(
			discordgo.WithContext(callCtx),
			discordgo.WithRetryOnRatelimit(false),
			discordgo.WithRestRetries(0),
		)
		done <- outcome{value, err}
	}()
	select {
	case res := <-done:
		return res.value, res.err
	case <-callCtx.Done():
		return zero, fmt.Errorf("%w: %w", errWrappedCallUnavailable, callCtx.Err())
	}
}

// supportedStartChannel reports whether a message can be posted to a
// channel of this type: server text, announcement and the three thread
// kinds (T-9).
func supportedStartChannel(kind discordgo.ChannelType) bool {
	switch kind {
	case discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews,
		discordgo.ChannelTypeGuildNewsThread, discordgo.ChannelTypeGuildPublicThread,
		discordgo.ChannelTypeGuildPrivateThread:
		return true
	default:
		return false
	}
}

// restErrorIs reports whether err is a Discord REST error with the given
// HTTP status or Discord error code.
func restErrorIs(err error, status, code int) bool {
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) {
		return false
	}
	if restErr.Response != nil && restErr.Response.StatusCode == status {
		return true
	}
	return restErr.Message != nil && restErr.Message.Code == code
}

// CachedPostableChannel implements channels.LocalChannelCache: T-9 step 1
// only (the local session cache, no network call), for the Channels block's
// kind label (T-5, FR-006).
func (c *DiscordChannel) CachedPostableChannel(id string) bool {
	if c.session == nil || c.session.State == nil {
		return false
	}
	cached, err := c.session.State.Channel(id)
	return err == nil && cached != nil && supportedStartChannel(cached.Type)
}

// discordTarget is the outcome of the T-9 steps for one id.
type discordTarget struct {
	person bool                     // true: a user (start opens the DM); false: a postable channel
	code   channels.TargetErrorCode // non-empty: refused
	cause  error                    // the call error behind a refusal, for the log
}

// resolveTarget runs T-9 steps 1–3 for a bare Discord id: the local cache,
// then the REST channel read, then the REST user read. A timeout, network
// error, 429 or 5xx is lookup_unavailable, never not_found.
func (c *DiscordChannel) resolveTarget(ctx context.Context, id string) discordTarget {
	if c.session.State != nil {
		if cached, err := c.session.State.Channel(id); err == nil && cached != nil {
			if supportedStartChannel(cached.Type) {
				return discordTarget{}
			}
			return discordTarget{code: channels.TargetErrUnsupportedChannelKind}
		}
	}
	channel, err := wrappedCall(ctx, func(options ...discordgo.RequestOption) (*discordgo.Channel, error) {
		return c.session.Channel(id, options...)
	})
	switch {
	case err == nil && channel != nil:
		if supportedStartChannel(channel.Type) {
			return discordTarget{}
		}
		return discordTarget{code: channels.TargetErrUnsupportedChannelKind}
	case err == nil:
		return discordTarget{code: channels.TargetErrLookupUnavailable, cause: errors.New("empty channel response")}
	case restErrorIs(err, http.StatusNotFound, discordgo.ErrCodeUnknownChannel):
		// Not a channel: try a person (step 3).
	case restErrorIs(err, http.StatusForbidden, discordgo.ErrCodeMissingAccess):
		return discordTarget{code: channels.TargetErrNotFound, cause: err}
	default:
		return discordTarget{code: channels.TargetErrLookupUnavailable, cause: err}
	}
	user, err := wrappedCall(ctx, func(options ...discordgo.RequestOption) (*discordgo.User, error) {
		return c.session.User(id, options...)
	})
	switch {
	case err == nil && user != nil:
		return discordTarget{person: true}
	case err == nil:
		return discordTarget{code: channels.TargetErrLookupUnavailable, cause: errors.New("empty user response")}
	case restErrorIs(err, http.StatusNotFound, discordgo.ErrCodeUnknownUser):
		return discordTarget{code: channels.TargetErrNotFound, cause: err}
	default:
		return discordTarget{code: channels.TargetErrLookupUnavailable, cause: err}
	}
}

// ResolveOutboundTarget implements channels.OutboundTargetResolver: the T-9
// save-time check of one added canonical target ("discord:<id>").
func (c *DiscordChannel) ResolveOutboundTarget(ctx context.Context, canonical string) channels.OutboundTargetResult {
	target := c.resolveTarget(ctx, channels.BareTargetID(canonical))
	if target.cause != nil {
		logger.WarnCF("discord", "Outbound target look-up refused", map[string]any{
			"target": canonical, "code": string(target.code), "error": target.cause.Error(),
		})
	}
	return channels.OutboundTargetResult{Code: target.code}
}

// sendStart delivers an Operation=start message (FR-046): a postable channel
// gets the message directly; a person gets it in the DM channel opened
// through the wrapper. Any refusal or failure is an adapter error and
// nothing is sent. The error is permanent (ErrSendFailed), so the manager
// does not retry a start into a later delivery.
func (c *DiscordChannel) sendStart(ctx context.Context, msg bus.OutboundMessage) error {
	id := channels.BareTargetID(msg.ChatID)
	target := c.resolveTarget(ctx, id)
	if target.code != "" {
		logger.WarnCF("discord", "Started message not sent: target refused", map[string]any{
			"chat_id": id, "code": string(target.code), "error": fmt.Sprint(target.cause),
		})
		return fmt.Errorf("discord start to %s refused (%s): %w", id, target.code, channels.ErrSendFailed)
	}
	channelID := id
	if target.person {
		dm, err := wrappedCall(ctx, func(options ...discordgo.RequestOption) (*discordgo.Channel, error) {
			return c.session.UserChannelCreate(id, options...)
		})
		if err != nil || dm == nil || dm.ID == "" {
			if err == nil {
				err = errors.New("empty DM channel response")
			}
			logger.WarnCF("discord", "Started message not sent: could not open the direct message", map[string]any{
				"chat_id": id, "error": err.Error(),
			})
			return fmt.Errorf("discord start to %s: open direct message: %w: %w", id, channels.ErrSendFailed, err)
		}
		channelID = dm.ID
	}
	return c.sendChunk(ctx, channelID, msg.Content, "")
}
