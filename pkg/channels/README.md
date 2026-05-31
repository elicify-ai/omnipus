# Omnipus Channel System

> **Scope**: `pkg/channels/`, `pkg/bus/`, `pkg/media/`, `pkg/identity/`

This document describes the channel system as it exists in the current codebase. Every concrete claim cites the file and line range it was verified against.

---

## Table of Contents

- [1. Architecture Overview](#1-architecture-overview)
- [2. Core Interfaces](#2-core-interfaces)
- [3. BaseChannel Ergonomics](#3-basechannel-ergonomics)
- [4. Factory Registration and Manager Lifecycle](#4-factory-registration-and-manager-lifecycle)
- [5. Message Bus](#5-message-bus)
- [6. Outbound Orchestration](#6-outbound-orchestration)
- [7. Inbound Auto-orchestration](#7-inbound-auto-orchestration)
- [8. Error Classification and Retry](#8-error-classification-and-retry)
- [9. Message Splitting](#9-message-splitting)
- [10. Registered Channels](#10-registered-channels)
- [11. Adding a New Channel](#11-adding-a-new-channel)
- [12. Supporting Types](#12-supporting-types)
- [13. Testing Conventions](#13-testing-conventions)
- [Appendix: Interface Quick Reference](#appendix-interface-quick-reference)

---

## 1. Architecture Overview

```
┌────────────┐   InboundMessage         ┌───────────┐   LLM + Tools   ┌────────────┐
│  Telegram   │──┐                       │           │                  │            │
│  Discord    │──┤  PublishInbound()     │           │ PublishOutbound()│            │
│  Slack      │──┼─────────────────────▶│ MessageBus│◀────────────────│ AgentLoop  │
│  LINE       │──┤  (buffered, 64)       │           │ (buffered, 64)  │            │
│  ...        │──┘                       │           │                  │            │
└────────────┘                           └─────┬─────┘                  └────────────┘
                                               │
                           SubscribeOutbound() │ SubscribeOutboundMedia()
                                               ▼
                                   ┌────────────────────┐
                                   │       Manager       │
                                   │  dispatchOutbound   │  route to worker queues
                                   │  runWorker          │  split + sendWithRetry
                                   │  runMediaWorker     │  sendMediaWithRetry
                                   │  preSend            │  stop typing, undo reaction,
                                   │                     │  edit/delete placeholder
                                   │  runTTLJanitor      │  evict stale state (10s)
                                   └──────────┬──────────┘
                                              │
                                    channel.Send() / SendMedia()
                                              │
                                              ▼
                                   ┌────────────────────┐
                                   │   Platform APIs    │
                                   └────────────────────┘
```

### Key Principles

| Principle | Details |
|-----------|---------|
| Sub-package isolation | Each channel is a standalone Go subpackage depending only on types from the parent `channels` package |
| Factory self-registration | Sub-packages call `channels.RegisterFactory` in their `func init()` (`pkg/channels/registry.go:22-27`) |
| Capability discovery | Optional behaviors (typing, reactions, placeholders, streaming, media, webhooks, command menus) are declared as interfaces; Manager discovers them via type assertion |
| Centralized orchestration | Rate limiting, message splitting, retries, and typing/reaction/placeholder lifecycle are all handled by Manager and BaseChannel; individual channels only implement `Send` |
| Credential injection | Factory functions receive a `credentials.SecretBundle` directly; channel secrets are never read from the process environment (`pkg/channels/registry.go:15`) |

> **Note on extensibility.** The current factory map is open at compile time but the `initChannels` if-ladder is closed: adding a channel requires editing both `pkg/channels/manager.go` and `pkg/gateway/gateway.go`. A migration to a fully map-driven loader is scoped in `docs/internal/architecture/plugin-extensibility-assessment.md` (§1) and tracked under the broader plugin work in issue #151.

---

## 2. Core Interfaces

### 2.1 Required: `Channel`

Defined at `pkg/channels/base.go:47-56`.

```go
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg bus.OutboundMessage) error
    IsRunning() bool
    IsAllowed(senderID string) bool
    IsAllowedSender(sender bus.SenderInfo) bool
    ReasoningChannelID() string
}
```

All methods except `Start`, `Stop`, and `Send` are provided for free by embedding `*BaseChannel`.

### 2.2 Optional Capability Interfaces

Defined at `pkg/channels/interfaces.go:13-70`, `pkg/channels/media.go:13-15`, `pkg/channels/webhook.go:8-20`.

| Interface | Method(s) | Purpose |
|-----------|-----------|---------|
| `TypingCapable` | `StartTyping(ctx, chatID) (stop func(), err error)` | Show "typing..." indicator |
| `ReactionCapable` | `ReactToMessage(ctx, chatID, messageID) (undo func(), err error)` | React to inbound message |
| `PlaceholderCapable` | `SendPlaceholder(ctx, chatID) (messageID string, err error)` | Send "Thinking..." message |
| `MessageEditor` | `EditMessage(ctx, chatID, messageID, content) error` | Edit a sent message |
| `MessageDeleter` | `DeleteMessage(ctx, chatID, messageID) error` | Delete a sent message |
| `StreamingCapable` | `BeginStream(ctx, chatID) (Streamer, error)` | Incremental LLM output |
| `MediaSender` | `SendMedia(ctx, msg bus.OutboundMediaMessage) error` | Send files/images/audio/video |
| `WebhookHandler` | `WebhookPath() string` + `http.Handler` | Receive messages via HTTP |
| `HealthChecker` | `HealthPath() string`, `HealthHandler(w, r)` | Health check endpoint |
| `CommandRegistrarCapable` | `RegisterCommands(ctx, defs []commands.Definition) error` | Register command menus (e.g. Telegram `/cancel`) |
| `MessageLengthProvider` | `MaxMessageLength() int` | Tell Manager the platform's character limit |

`PlaceholderCapable` requires `MessageEditor` to be useful: Manager's `preSend` edits the placeholder with the final reply only when the channel implements both (`pkg/channels/manager.go:226-246`).

`StreamingCapable` coexists with the typing/placeholder pipeline. If `BeginStream` succeeds for a turn, `preSend` detects the stream-active marker and cleans up the placeholder instead of editing it with the same content (`pkg/channels/manager.go:211-223`).

### 2.3 `CancelInterceptor`

Defined at `pkg/channels/cancelparse.go:14-25`. Channels that parse text commands (Tier B) call `DispatchCancelIfRecognized` to fire the full cancel state machine without importing `pkg/agent`. The interceptor is injected by Manager via `SetCancelInterceptor` after construction.

---

## 3. BaseChannel Ergonomics

Defined at `pkg/channels/base.go:85-362`.

`BaseChannel` is embedded by every channel implementation and provides all `Channel` interface methods except `Start`, `Stop`, and `Send`.

### Constructor

```go
base := channels.NewBaseChannel(
    "telegram",                                   // Name (must match RegisterFactory key)
    cfg.Channels.Telegram,                        // Raw config (any type)
    msgBus,                                       // Message bus
    cfg.Channels.Telegram.AllowFrom,              // Allow list (nil = allow all)
    channels.WithMaxMessageLength(4096),           // Platform character limit (runes)
    channels.WithGroupTrigger(cfg.Channels.Telegram.GroupTrigger),
    channels.WithReasoningChannelID(cfg.Channels.Telegram.ReasoningChannelID),
)
```

Functional options (`pkg/channels/base.go:62-76`): `WithMaxMessageLength`, `WithGroupTrigger`, `WithReasoningChannelID`.

### Key Methods

| Method | Description |
|--------|-------------|
| `IsRunning() bool` | Atomic read of running state (`atomic.Bool`) |
| `SetRunning(bool)` | Atomic write — call `SetRunning(true)` in `Start`, `SetRunning(false)` at the top of `Stop` |
| `MaxMessageLength() int` | Returns the rune limit (0 = unlimited); implements `MessageLengthProvider` |
| `IsAllowed(senderID string) bool` | Legacy string allow-list check; handles `"id\|username"` and `"@username"` formats |
| `IsAllowedSender(sender bus.SenderInfo) bool` | Structured allow-list check via `identity.MatchAllowed` |
| `ShouldRespondInGroup(isMentioned bool, content string) (bool, string)` | Unified group-chat trigger logic (mention-only, prefix list, permissive default) |
| `HandleMessage(ctx, peer, messageID, senderID, chatID, content, media, metadata, senderOpts...)` | Permission check + auto-trigger typing/reaction/placeholder + publish to bus |
| `SetMediaStore(s)` / `GetMediaStore()` | Injected by Manager at init time |
| `SetPlaceholderRecorder(r)` / `GetPlaceholderRecorder()` | Injected by Manager at init time |
| `SetOwner(ch Channel)` | Injected by Manager — enables `HandleMessage` to type-assert the concrete channel for capability auto-triggering |
| `SetCancelInterceptor(ci)` / `GetCancelInterceptor()` | Injected by Manager for Tier B text-parsing cancel support |
| `BuildMediaScope(channel, chatID, messageID) string` | Builds a `channel:chatID:messageID` scope key for media lifecycle tracking |

### Allow-list Lifecycle

`HandleMessage` (`pkg/channels/base.go:233-316`) performs the allow-list check first. When `SenderInfo` is populated it uses `IsAllowedSender`; otherwise it falls back to `IsAllowed(senderID)`. Channels do not need to duplicate this check.

---

## 4. Factory Registration and Manager Lifecycle

### 4.1 Factory Registration

`pkg/channels/registry.go:15-35` defines:

```go
// The factory signature — note the SecretBundle parameter.
type ChannelFactory func(cfg *config.Config, secrets credentials.SecretBundle, bus *bus.MessageBus) (Channel, error)

func RegisterFactory(name string, f ChannelFactory)  // called from subpackage init()
func getFactory(name string) (ChannelFactory, bool)  // called internally by Manager
```

Each subpackage calls `channels.RegisterFactory` from a `func init()` in its `init.go`. The gateway triggers these registrations with blank imports in `pkg/gateway/gateway.go:33-46`.

Example (`pkg/channels/telegram/init.go:10-17`):

```go
func init() {
    channels.RegisterFactory(
        "telegram",
        func(cfg *config.Config, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
            return NewTelegramChannel(cfg, secrets, b)
        },
    )
}
```

### 4.2 Manager Construction

```go
// pkg/channels/manager.go:285-312
manager, err := channels.NewManager(cfg, secrets, messageBus, mediaStore)
```

`NewManager` calls `initChannels` immediately. `initChannels` (in `pkg/channels/manager.go`) is a fixed if-ladder over typed `config.ChannelsConfig` fields — one branch per channel. Each branch calls `m.initChannel(name, displayName)` which looks up the factory, calls it, then injects `MediaStore`, `PlaceholderRecorder`, `Owner`, and `CancelInterceptor` into the constructed channel.

**Boot-fail policy:** If any enabled channel fails to construct, `initChannels` returns a joined error and `NewManager` fails. The gateway aborts boot. This is the deny-by-default policy in action.

Failed channels after a successful construction are tracked in `Manager.failedChannels` (`pkg/channels/manager.go:117`), accessible via `FailedChannels()`.

### 4.3 StartAll / StopAll

`StartAll` (`pkg/channels/manager.go:718-833`):
1. Calls `channel.Start(ctx)` for each channel in `m.channels`
2. Creates a `channelWorker` per successfully started channel
3. Launches `runWorker` + `runMediaWorker` goroutines
4. Launches `dispatchOutbound` + `dispatchOutboundMedia` goroutines
5. Launches `runTTLJanitor` goroutine (10 second interval)
6. Starts the shared HTTP server and preview server (if configured)
7. Calls `RegisterCommands` on every `CommandRegistrarCapable` channel (30s timeout, WARN on failure)

`StopAll` (`pkg/channels/manager.go:835-909`):
1. Shuts down the shared HTTP server (5s graceful)
2. Shuts down the preview server (5s graceful)
3. Cancels the dispatch context
4. Closes text worker queues; waits for drain
5. Closes media worker queues; waits for drain
6. Calls `channel.Stop(ctx)` for each channel

### 4.4 Hot Reload

`Reload` (`pkg/channels/manager.go:1327-1458`) compares MD5 hashes of each channel's config blob. Added channels are initialized and started; removed channels are stopped; unchanged channel workers are restarted on the new context to prevent routing failures.

### 4.5 Dynamic Registration

`RegisterChannel` / `UnregisterChannel` (`pkg/channels/manager.go:1460-1503`) allow channels to be added or removed at runtime (used by the webchat WebSocket handler for transient sessions).

---

## 5. Message Bus

Defined at `pkg/bus/bus.go:35-153`.

```go
type MessageBus struct {
    inbound       chan InboundMessage       // buffer = 64
    outbound      chan OutboundMessage      // buffer = 64
    outboundMedia chan OutboundMediaMessage // buffer = 64
    closeOnce     sync.Once
    done          chan struct{}
    closed        atomic.Bool
    publishMu     sync.Mutex              // guards closed+wg.Add (TOCTOU prevention)
    wg            sync.WaitGroup
    streamDelegate atomic.Pointer[StreamDelegate]
}
```

**Key behaviors:**

| Method | Behavior |
|--------|----------|
| `PublishInbound(ctx, msg)` | Acquires `publishMu`, increments `wg`, sends to `inbound` channel; blocks if channel is full |
| `PublishOutbound(ctx, msg)` | Same pattern for `outbound` channel |
| `PublishOutboundMedia(ctx, msg)` | Same pattern for `outboundMedia` channel |
| `InboundChan()` | Returns the read-only `inbound` channel (consumed by agent loop) |
| `OutboundChan()` | Returns the read-only `outbound` channel (consumed by Manager dispatcher) |
| `OutboundMediaChan()` | Returns the read-only `outboundMedia` channel |
| `Close()` | `sync.Once`-guarded: closes `done`, sets `closed`, waits for `wg`, closes all three channels, drains buffered messages |
| `SetStreamDelegate(d)` | Registers the channel Manager as the stream provider |
| `GetStreamer(ctx, channel, chatID, sessionID)` | Delegates to Manager's `GetStreamer` |

**Design notes:** `publishMu` prevents the TOCTOU race between `closed` check and `wg.Add(1)` when `Close()` runs concurrently. The channels themselves are closed inside `closeOnce`, after `wg.Wait()` guarantees no in-flight publishers remain.

---

## 6. Outbound Orchestration

### 6.1 Per-channel Worker Architecture

Each channel gets a `channelWorker` (`pkg/channels/manager.go:74-81`):

```go
type channelWorker struct {
    ch         Channel
    queue      chan bus.OutboundMessage      // buffered 16
    mediaQueue chan bus.OutboundMediaMessage // buffered 16
    done       chan struct{}
    mediaDone  chan struct{}
    limiter    *rate.Limiter
}
```

### 6.2 Per-channel Rate Limits

From `pkg/channels/manager.go:63-72`:

| Channel | Rate (msg/s) | Burst |
|---------|-------------|-------|
| telegram | 20 | 10 |
| discord | 1 | 1 |
| slack | 1 | 1 |
| matrix | 2 | 1 |
| line | 10 | 5 |
| qq | 5 | 3 |
| irc | 2 | 1 |
| _all others_ | 10 (default) | 5 |

Burst is computed as `max(1, ceil(rate/2))` (`pkg/channels/manager.go:916-918`).

### 6.3 Message Splitting (Two Stages)

`runWorker` (`pkg/channels/manager.go:930-974`) splits outbound content before calling `sendWithRetry`:

1. **Marker split** (if `config.Agents.Defaults.SplitOnMarker` is true): splits on `<|[SPLIT]|>` (`pkg/channels/marker.go:16`). Each chunk is then length-split.
2. **Length split**: if the content exceeds `MaxMessageLength()`, `SplitMessage` is called (`pkg/channels/split.go`). Splitting is rune-aware, prefers newline boundaries, and preserves fenced code block integrity.

### 6.4 Retry Strategy

`sendWithRetry` (`pkg/channels/manager.go:989-1044`):

```
Max retries: 3
ErrNotRunning  → fail immediately, no retry
ErrSendFailed  → fail immediately, no retry
ErrRateLimit   → wait 1s, retry
ErrTemporary   → exponential backoff: 500ms × 2^attempt, max 8s
unknown error  → same as ErrTemporary
```

### 6.5 Pre-send Cleanup (`preSend`)

Before each `channel.Send` call, `preSend` (in `pkg/channels/manager.go`) runs:

1. Stops the typing indicator (calls the stored `stop func()`, deletes from `typingStops`)
2. Undoes the message reaction (calls the stored `undo func()`, deletes from `reactionUndos`)
3. If the stream already finalized (key present in `streamActive`): deletes placeholder via `MessageDeleter` (or edits via `MessageEditor` as fallback), then **skips** `Send` — the streamer already delivered the content
4. Otherwise if a placeholder exists: attempts `MessageEditor.EditMessage` with the final content; on success, **skips** `Send`; on failure, logs a warning and falls through to `Send`

`preSendMedia` (`pkg/channels/manager.go:255-283`) runs the same typing stop + reaction undo but always deletes (not edits) any placeholder, because there is no text payload to replace it with.

### 6.6 TTL Janitor

`runTTLJanitor` (`pkg/channels/manager.go:1250-1289`) runs every 10 seconds:

- Typing stop functions: 5-minute TTL — calls `stop()` on eviction
- Reaction undo functions: 5-minute TTL — calls `undo()` on eviction
- Placeholder IDs: 10-minute TTL — silently deleted (no action)

---

## 7. Inbound Auto-orchestration

`BaseChannel.HandleMessage` (`pkg/channels/base.go:233-316`) automatically fires optional inbound indicators before publishing to the bus. The channel does not call these manually.

```go
// Pseudocode of the auto-trigger block (pkg/channels/base.go:283-307):
if c.owner != nil && c.placeholderRecorder != nil {
    // Typing — independent pipeline
    if tc, ok := c.owner.(TypingCapable); ok {
        if stop, err := tc.StartTyping(ctx, chatID); err == nil {
            c.placeholderRecorder.RecordTypingStop(c.name, chatID, stop)
        }
    }
    // Reaction — independent pipeline
    if rc, ok := c.owner.(ReactionCapable); ok && messageID != "" {
        if undo, err := rc.ReactToMessage(ctx, chatID, messageID); err == nil {
            c.placeholderRecorder.RecordReactionUndo(c.name, chatID, undo)
        }
    }
    // Placeholder — skipped for audio messages (audioAnnotationRe at
    // pkg/channels/base.go:37 matches `[voice]` / `[audio:…]` annotations
    // emitted by transcription channels; the agent sends the placeholder
    // itself once transcription completes).
    if !audioAnnotationRe.MatchString(content) {
        if pc, ok := c.owner.(PlaceholderCapable); ok {
            if phID, err := pc.SendPlaceholder(ctx, chatID); err == nil && phID != "" {
                c.placeholderRecorder.RecordPlaceholder(c.name, chatID, phID)
            }
        }
    }
}
```

All three pipelines are independent and do not interfere. Manager implements `PlaceholderRecorder` (`pkg/channels/manager.go:126-189`) and is injected into every channel via `SetPlaceholderRecorder`. Manager's `InvokeTypingStop` (`pkg/channels/manager.go:175-182`) is also called by the agent loop when processing ends abnormally (error or panic) so typing never gets stuck.

---

## 8. Error Classification and Retry

Sentinel errors (`pkg/channels/errors.go:6-21`):

```go
var (
    ErrNotRunning = errors.New("channel not running")  // permanent: Manager does not retry
    ErrRateLimit  = errors.New("rate limited")          // fixed 1s delay, then retry
    ErrTemporary  = errors.New("temporary failure")     // exponential backoff, then retry
    ErrSendFailed = errors.New("send failed")           // permanent: Manager does not retry
)
```

Helper functions (`pkg/channels/errutil.go:11-30`):

```go
// Wrap based on HTTP status code.
func ClassifySendError(statusCode int, rawErr error) error
// 429 → ErrRateLimit, 5xx → ErrTemporary, 4xx → ErrSendFailed

// Wrap network/timeout errors as temporary.
func ClassifyNetError(err error) error
```

**Contract:** A channel's `Send` method must return one of the sentinel errors (or wrap them) so Manager can apply the correct retry strategy. Returning an unclassified error is treated as `ErrTemporary` (exponential backoff).

---

## 9. Message Splitting

`SplitMessage(content string, maxLen int) []string` (`pkg/channels/split.go`).

Splitting strategy:
1. Buffer = max(maxLen/10, 50), capped at maxLen/2, reserved for code block fencing
2. Effective split point = `maxLen - buffer`
3. Prefers splitting at the last newline within the effective limit (up to 200 runes back)
4. Falls back to the last space/tab (up to 100 runes back)
5. Detects unclosed fenced code blocks (` ``` `). If unclosed:
   - Tries to extend the chunk to include the closing fence
   - If the block is too long: injects close + reopen fences between chunks
   - Last resort: splits before the unclosed block begins

`SplitByMarker(content string) []string` (`pkg/channels/marker.go:21-37`) splits on `<|[SPLIT]|>` and filters empty parts.

---

## 10. Registered Channels

The following factory names are registered. The gateway blank-imports each subpackage in `pkg/gateway/gateway.go` to trigger the `init()` registration; check that file for the canonical list.

| Factory name | Subpackage | Optional interfaces |
|---|---|---|
| `telegram` | `pkg/channels/telegram/` | TypingCapable, PlaceholderCapable, MessageEditor, MessageDeleter, MediaSender, StreamingCapable, CommandRegistrarCapable |
| `discord` | `pkg/channels/discord/` | TypingCapable, PlaceholderCapable, MessageEditor, MediaSender, CommandRegistrarCapable |
| `slack` | `pkg/channels/slack/` | TypingCapable (no-op), ReactionCapable, MediaSender, CommandRegistrarCapable |
| `line` | `pkg/channels/line/` | TypingCapable, MediaSender, WebhookHandler |
| `matrix` | `pkg/channels/matrix/` | TypingCapable, PlaceholderCapable, MessageEditor, MediaSender — conditionally imported (build tag in `pkg/gateway/channel_matrix.go`, CGo required) |
| `feishu` | `pkg/channels/feishu/` | PlaceholderCapable, MessageEditor, ReactionCapable, MediaSender, CommandRegistrarCapable (64-bit only; 32-bit stubs at `feishu_32.go`) |
| `dingtalk` | `pkg/channels/dingtalk/` | CommandRegistrarCapable (uses DingTalk Stream/WebSocket mode, not webhook) |
| `onebot` | `pkg/channels/onebot/` | ReactionCapable, MediaSender |
| `qq` | `pkg/channels/qq/` | TypingCapable, MediaSender |
| `irc` | `pkg/channels/irc/` | TypingCapable |
| `wecom` | `pkg/channels/wecom/` | StreamingCapable, MediaSender (WebSocket AI Bot; no WebhookHandler) |
| `weixin` | `pkg/channels/weixin/` | TypingCapable, MediaSender |
| `whatsapp` | `pkg/channels/whatsapp/` | — (Bridge mode: connects to external bridge via `BridgeURL`) |
| `whatsapp_native` | `pkg/channels/whatsapp_native/` | TypingCapable |
| `google-chat` | `pkg/channels/googlechat/` | TypingCapable, WebhookHandler, CommandRegistrarCapable |

**Notes on specific channels:**

- **matrix**: Conditionally imported with build tag `!mipsle && !netbsd && !(freebsd && arm) && cgo` (`pkg/gateway/channel_matrix.go:1-28`) because its transitive dependencies (`mautrix`, `modernc.org/sqlite`) fail on those targets.
- **whatsapp vs whatsapp_native**: `initChannels` checks `WhatsAppConfig.UseNative` to select which factory to use (`pkg/channels/manager.go:467-477`). Only one of the two is initialized per run.
- **weixin**: `RegisterFactory` call lives in `weixin.go` (no separate `init.go`) at `pkg/channels/weixin/weixin.go:40`.
- **google-chat**: Factory name is `"google-chat"` (with hyphen), matching the `ChannelsConfig` JSON key (`pkg/config/config.go:789`).

---

## 11. Adding a New Channel

### Step 1: Create the subpackage

```
pkg/channels/mychann/
├── init.go      ← factory registration
└── mychann.go   ← implementation
```

### Step 2: `init.go` — register the factory

```go
package mychann

import (
    "github.com/dapicom-ai/omnipus/pkg/bus"
    "github.com/dapicom-ai/omnipus/pkg/channels"
    "github.com/dapicom-ai/omnipus/pkg/config"
    "github.com/dapicom-ai/omnipus/pkg/credentials"
)

func init() {
    channels.RegisterFactory(
        "mychann",
        func(cfg *config.Config, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
            return NewMyChannel(cfg, secrets, b)
        },
    )
}
```

### Step 3: `mychann.go` — implement the channel

```go
package mychann

import (
    "context"
    "fmt"

    "github.com/dapicom-ai/omnipus/pkg/bus"
    "github.com/dapicom-ai/omnipus/pkg/channels"
    "github.com/dapicom-ai/omnipus/pkg/config"
    "github.com/dapicom-ai/omnipus/pkg/credentials"
    "github.com/dapicom-ai/omnipus/pkg/identity"
)

type MyChannel struct {
    *channels.BaseChannel
    config *config.Config
    // ... platform-specific client
}

func NewMyChannel(cfg *config.Config, secrets credentials.SecretBundle, msgBus *bus.MessageBus) (*MyChannel, error) {
    myCfg := cfg.Channels.MyChann

    base := channels.NewBaseChannel(
        "mychann",
        myCfg,
        msgBus,
        myCfg.AllowFrom,
        channels.WithMaxMessageLength(4096),
        channels.WithGroupTrigger(myCfg.GroupTrigger),
        channels.WithReasoningChannelID(myCfg.ReasoningChannelID),
    )

    return &MyChannel{
        BaseChannel: base,
        config:      cfg,
    }, nil
}

func (c *MyChannel) Start(ctx context.Context) error {
    // Initialize client, connect, set up listeners...
    c.SetRunning(true)
    return nil
}

func (c *MyChannel) Stop(ctx context.Context) error {
    c.SetRunning(false)
    // Clean up...
    return nil
}

func (c *MyChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
    if !c.IsRunning() {
        return channels.ErrNotRunning
    }
    err := c.sendToPlatform(ctx, msg.ChatID, msg.Content)
    if err != nil {
        // Use ClassifySendError if you have an HTTP status code:
        // return channels.ClassifySendError(statusCode, err)
        return fmt.Errorf("%w: %v", channels.ErrTemporary, err)
    }
    return nil
}

// Inbound message handling:
func (c *MyChannel) handleIncoming(chatID, senderPlatformID, displayName, content, messageID string) {
    sender := bus.SenderInfo{
        Platform:    "mychann",
        PlatformID:  senderPlatformID,
        CanonicalID: identity.BuildCanonicalID("mychann", senderPlatformID),
        DisplayName: displayName,
    }
    peer := bus.Peer{Kind: "direct", ID: chatID}

    // HandleMessage handles allow-list checks, auto-typing, auto-reaction,
    // auto-placeholder, and bus publish. Do not duplicate these checks.
    c.HandleMessage(c.ctx, peer, messageID, senderPlatformID, chatID, content, nil, nil, sender)
}
```

### Step 4: Add config struct in `pkg/config/config.go`

Add a `MyChannConfig` struct and a field to `ChannelsConfig`.

### Step 5: Add entry in `Manager.initChannels`

In `pkg/channels/manager.go`, add an `if` branch inside `initChannels`:

```go
if channels.MyChann.Enabled && channels.MyChann.Token != "" {
    if err := m.initChannel("mychann", "MyChannel"); err != nil {
        m.recordChannelFailure("mychann", "MyChannel", err)
    }
}
```

### Step 6: Add blank import in the gateway

In `pkg/gateway/gateway.go`:

```go
_ "github.com/dapicom-ai/omnipus/pkg/channels/mychann"
```

### Step 7: Implement optional interfaces as needed

See [Appendix: Interface Quick Reference](#appendix-interface-quick-reference) for method signatures. Return `ErrNotRunning` first in `SendMedia`; use `channels.ClassifySendError` or `channels.ClassifyNetError` for errors with HTTP context.

---

## 12. Supporting Types

### 12.1 Structured Bus Types (`pkg/bus/types.go`)

```go
type PeerKind string  // "direct" | "group" | "channel" | ""

type Peer struct {
    Kind PeerKind `json:"kind"`
    ID   string   `json:"id"`
}

type SenderInfo struct {
    Platform    string `json:"platform,omitempty"`
    PlatformID  string `json:"platform_id,omitempty"`
    CanonicalID string `json:"canonical_id,omitempty"`  // "platform:id" format
    Username    string `json:"username,omitempty"`
    DisplayName string `json:"display_name,omitempty"`
}

type InboundMessage struct {
    Channel    string            // source channel name
    SenderID   string            // deprecated: use Sender.CanonicalID
    Sender     SenderInfo
    ChatID     string
    Content    string
    Media      []string          // media store refs ("media://...")
    Peer       Peer
    MessageID  string
    MediaScope string            // "channel:chatID:messageID" scope key
    SessionKey string
    SessionID  string
    Metadata   map[string]string // channel-specific extensions only
}

type OutboundMessage struct {
    Channel          string
    ChatID           string
    SessionID        string
    Content          string
    ReplyToMessageID string
}

type MediaPart struct {
    Type        string // "image" | "audio" | "video" | "file"
    Ref         string // "media://uuid"
    Caption     string
    Filename    string
    ContentType string
}

type OutboundMediaMessage struct {
    Channel   string
    ChatID    string
    SessionID string
    Parts     []MediaPart
}
```

**Do not put routing information in `Metadata`.** Use `Peer`, `MessageID`, and `Sender` for all routing fields. `Metadata` is for channel-specific extensions (e.g. Telegram's `reply_to_message_id`).

### 12.2 Identity (`pkg/identity/identity.go`)

```go
func BuildCanonicalID(platform, platformID string) string  // → "telegram:123456"
func ParseCanonicalID(canonical string) (platform, id string, ok bool)
func MatchAllowed(sender bus.SenderInfo, allowed string) bool
```

Allow-list formats supported by `MatchAllowed`:

| Format | Matches |
|--------|---------|
| `"123456"` | `sender.PlatformID` |
| `"@alice"` | `sender.Username` |
| `"123456\|alice"` | PlatformID or Username (legacy compound) |
| `"telegram:123456"` | `sender.CanonicalID` (canonical format) |

### 12.3 MediaStore (`pkg/media/store.go`)

```go
type MediaStore interface {
    Store(localPath string, meta MediaMeta, scope string) (ref string, err error)
    Resolve(ref string) (localPath string, err error)
    ResolveWithMeta(ref string) (localPath string, meta MediaMeta, err error)
    RefByPath(localPath string) (ref string, ok bool)
    ReleaseAll(scope string) error
}
```

Reference format: `media://<uuid>`. Scope format: produced by `channels.BuildMediaScope`. The `FileMediaStore` implementation is in-memory (no file copy) with background TTL cleanup.

### 12.4 Shared HTTP Server

Manager creates a `dynamicServeMux` (`pkg/channels/dynamic_mux.go`) that supports runtime `Handle` / `Unhandle`. Channels implementing `WebhookHandler` are registered automatically during `initChannel` and `StartAll`. A separate preview server (`SetupPreviewServer`) hosts `/serve/` and `/dev/` routes on a different port for agent-generated HTML previews.

HTTP server timeout: `ReadTimeout = 30s`, `WriteTimeout = 30s` (`pkg/channels/manager.go`).

**Webhook security is the channel's responsibility.** The shared mux does not enforce HMAC signature checks, IP allow-lists, or replay protection — each `WebhookHandler` implementation must validate inbound requests itself (e.g. Slack/Telegram signing secrets, Google Chat JWT, Line signature header). When adding a new webhook channel, do this validation in the `http.Handler` before publishing to the bus.

#### 12.1 Webhook signature verification — project-wide invariant (#162)

**Every `WebhookHandler` MUST verify a platform-issued signature on the request body BEFORE parsing the payload or publishing to the `MessageBus`.** Without this check, anyone who knows the webhook path can inject fake messages, drive agent turns, and spend LLM/credential budget.

Current implementations and the canonical primitives they use:

| Channel | Header | Algorithm | Source |
|---------|--------|-----------|--------|
| `line` | `X-Line-Signature` | HMAC-SHA256, channel secret, base64 | `pkg/channels/line/line.go::verifySignature` |
| `google-chat` | `Google-Signature` | HMAC-SHA256, project token, hex | `pkg/channels/googlechat/googlechat.go::verifySignature` |

The verification is enforced by a hygiene test, `TestWebhookHandlers_HaveSignatureVerification` in `pkg/channels/webhook_signature_test.go`. It scans every package under `pkg/channels/*/` and fails if a package declares a `WebhookPath()` method but contains neither a `verifySignature` function nor an `hmac.Equal(...)` call. Adding a new webhook channel without a signature check breaks CI loudly.

When implementing a new webhook channel:

1. Read the body via `io.LimitReader` (cap it — match the existing `maxWebhookBodySize` patterns in line/googlechat).
2. Pull the platform signature header (the platform's docs name it; treat the empty case as a hard fail, never as "skip verification").
3. Compute the expected HMAC over the body using `crypto/hmac` and the secret your channel was configured with.
4. Compare with `hmac.Equal` — never `==` (timing-side-channel).
5. Only then unmarshal the body and dispatch events.

---

## 13. Testing Conventions

Framework-level test files in `pkg/channels/`:

| File | Tests |
|------|-------|
| `base_test.go` | BaseChannel unit tests |
| `manager_test.go` | Manager unit tests (worker queues, preSend, dispatch) |
| `manager_channel_test.go` | Channel hash comparison / Reload logic |
| `manager_register_commands_test.go` | CommandRegistrarCapable integration |
| `split_test.go` | SplitMessage edge cases |
| `marker_test.go` | SplitByMarker |
| `errors_test.go` | Sentinel error identity |
| `errutil_test.go` | ClassifySendError / ClassifyNetError |
| `cancelparse_test.go` | IsCancelCommand / DispatchCancelIfRecognized |
| `dynamic_mux_test.go` | dynamicServeMux routing |
| `wave4_typing_registry_test.go` | Typing stop / placeholder registry TTL |
| `interfaces_command_test.go` | CommandRegistrarCapable interface compliance |

Running tests:

```bash
# Framework tests
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./pkg/channels/ -v

# Single channel subpackage
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./pkg/channels/telegram/ -v

# Full suite
PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./... -count=1
```

---

## Appendix: Interface Quick Reference

```go
// ===== Required (provided by BaseChannel) =====
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg bus.OutboundMessage) error
    IsRunning() bool
    IsAllowed(senderID string) bool
    IsAllowedSender(sender bus.SenderInfo) bool
    ReasoningChannelID() string
}

// ===== Optional — discover at runtime via type assertion =====

// pkg/channels/media.go
type MediaSender interface {
    SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) error
}

// pkg/channels/interfaces.go
type TypingCapable interface {
    StartTyping(ctx context.Context, chatID string) (stop func(), err error)
}

type ReactionCapable interface {
    ReactToMessage(ctx context.Context, chatID, messageID string) (undo func(), err error)
}

type PlaceholderCapable interface {
    SendPlaceholder(ctx context.Context, chatID string) (messageID string, err error)
}

type MessageEditor interface {
    EditMessage(ctx context.Context, chatID string, messageID string, content string) error
}

type MessageDeleter interface {
    DeleteMessage(ctx context.Context, chatID string, messageID string) error
}

type StreamingCapable interface {
    BeginStream(ctx context.Context, chatID string) (Streamer, error)
}

type CommandRegistrarCapable interface {
    RegisterCommands(ctx context.Context, defs []commands.Definition) error
}

// pkg/channels/webhook.go
type WebhookHandler interface {
    WebhookPath() string
    http.Handler  // ServeHTTP(w http.ResponseWriter, r *http.Request)
}

type HealthChecker interface {
    HealthPath() string
    HealthHandler(w http.ResponseWriter, r *http.Request)
}

// ===== Provided by BaseChannel (opt-in via WithMaxMessageLength) =====
type MessageLengthProvider interface {
    MaxMessageLength() int
}

// ===== Injected by Manager into BaseChannel =====
type PlaceholderRecorder interface {
    RecordPlaceholder(channel, chatID, placeholderID string)
    RecordTypingStop(channel, chatID string, stop func())
    RecordReactionUndo(channel, chatID string, undo func())
}
```
