# Issue #780 — Channel Allow-List Review

## Executive verdict

The shipped default is fail-open for every channel: an empty `allow_from` accepts every sender. That behavior is documented and visible in the configuration panel, and this review does **not** change it.

The review found two headline defects separate from that deferred policy decision, plus the adjacent identity and ordering findings documented below:

1. **WeCom enforced too late; this branch contains a reproduced, narrowly scoped fix.** A configured allow-list stopped final publication to the agent, but only after media processing, reply-route persistence, opening a response turn, an optional outbound “Processing…” message, and `/cancel` interception. An unlisted WeCom sender could therefore cause side effects and reach the cancellation control even though its ordinary message was eventually dropped. The fix checks the same per-channel list immediately after constructing the sender identity. Empty lists remain fail-open. See `pkg/channels/wecom/wecom.go::WeComChannel.dispatchIncoming` and `pkg/channels/base.go::BaseChannel.HandleMessage`.
2. **The UI's multi-entry editor does not produce a multi-entry list.** All 13 panels advertise comma-separated values, but the text input submits a JSON string and the decoder turns that whole string into one entry. For example, `alice, bob` becomes `[]string{"alice, bob"}`, not `[]string{"alice", "bob"}`. A single allowed sender is reachable; the advertised multi-sender configuration is not. See `src/lib/channel-fields.ts::CHANNEL_FIELDS`, `src/components/skills/ChannelConfigPanel.tsx::ChannelConfigPanel.buildSubmitPayload`, and `pkg/config/config_unmarshal.go::FlexibleStringSlice.UnmarshalJSON`.

The literal grep finding that WeCom is the only package without a direct `IsAllowedSender` call is true, but “no enforcement at all” is not: its final `HandleMessage` call reaches the shared check. The defect is enforcement order and pre-publication side effects.

## Scope and method

- Baseline: `release/v0.1.1` commit `a2fde301d7c427735283a5fe74e24e87c458a99a`.
- Review only. The fail-open default is explicitly out of scope for behavior change.
- Method: reproduce, isolate, diagnose, and fix only a genuine independently reproduced bypass.
- Evidence citations use `file::symbol`; no secret values are recorded.

## Verified starting points

| Starting point | Verdict | Evidence |
|---|---|---|
| Empty list is fail-open | **Confirmed** | Both the legacy string check and structured sender check return `true` for an empty list in `pkg/channels/base.go::BaseChannel.IsAllowed` and `pkg/channels/base.go::BaseChannel.IsAllowedSender`. |
| Value is per-channel `AllowFrom` | **Confirmed, but the “not in pkg/config” inference is false** | All 13 instance config adapters declare `AllowFrom` and copy it into the concrete channel config in `pkg/config/config_channels_instance.go::InstanceToTelegram` through `pkg/config/config_channels_instance.go::InstanceToGoogleChat`. Every channel constructor passes it to `pkg/channels/base.go::NewBaseChannel`. |
| UI may not expose it | **Disproved** | All 13 channel descriptors include an advanced `allow_from` field in `src/lib/channel-fields.ts::CHANNEL_FIELDS`, rendered by `src/components/skills/ChannelConfigPanel.tsx::ChannelConfigPanel`. The field is only partially functional because comma-separated input is not split. |
| 12 of 13 packages call `IsAllowedSender`; WeCom does not | **Confirmed literally; misleading as an enforcement conclusion** | WeCom relies on the second check inside `pkg/channels/base.go::BaseChannel.HandleMessage`. Its earlier side effects and `/cancel` interception remain outside that check in `pkg/channels/wecom/wecom.go::WeComChannel.dispatchIncoming`. |

## Per-channel enforcement matrix

“Ignored” below means the adapter does not publish that event to the agent, so it is not an allow-list bypass. “Text path” means a typed `/command` travels through the same checked message handler. Every actual publish is checked again at the shared choke point `pkg/channels/base.go::BaseChannel.HandleMessage`.

| Channel | Registered inbound starts | Direct / group message coverage | Commands, edits, reactions, callbacks | Enforcement verdict | Denial visibility |
|---|---|---|---|---|---|
| DingTalk | Authenticated stream chatbot callback: `pkg/channels/dingtalk/dingtalk.go::DingTalkChannel.Start` → `DingTalkChannel.onChatBotMessageReceived` | One handler covers direct and group messages; structured sender checked before publish | Commands use the text path. No separate edit, reaction, or interactive callback is registered. | **Agent publication is covered, but checked after reply-state mutation.** The handler stores the session webhook and applies group-trigger processing before authorization. A denied sender cannot invoke the agent but can replace that chat's stored reply route. | No denial-specific log; a generic debug receive log with a truncated content preview occurs before the check. |
| Discord | Gateway `MessageCreate`: `pkg/channels/discord/discord.go::DiscordChannel.Start` → `DiscordChannel.handleMessage` | One handler covers direct messages and guild channels; checked before attachment download | Text commands use the message path. Native slash commands are registered by `pkg/channels/discord/command_registration.go::DiscordChannel.RegisterCommands`, but no `InteractionCreate` handler exists, so native slash commands are unreachable rather than open. Edits and reactions are not registered. | **Covered for the only registered agent-driving path.** Separate functionality gap: registered native slash commands have no receiver. | Debug log with user ID. |
| Feishu / Lark | WebSocket `P2MessageReceiveV1`: `pkg/channels/feishu/feishu_64.go::FeishuChannel.Start` → `FeishuChannel.handleMessageReceive` | One handler covers p2p and group messages and supported media types; checked before media download | Commands use the text path. No message-edit, reaction, or card-action handler is registered. | **Covered for every registered agent-driving path.** | Silent drop. |
| Google Chat | Signed HTTP webhook: `pkg/channels/googlechat/googlechat.go::GoogleChatChannel.webhookHandler` → `GoogleChatChannel.processEvent` | `MESSAGE` events cover direct spaces and rooms; checked before publish | Non-`MESSAGE` events, including card callbacks, are ignored. Commands carried as message text are checked. No edit or reaction path is registered. | **Covered for every registered agent-driving path.** Webhook authenticity is separately verified before parsing. | Silent drop. |
| IRC | IRC `PRIVMSG`: `pkg/channels/irc/irc.go::IRCChannel.Start` → `pkg/channels/irc/handler.go::IRCChannel.onPrivmsg` | One callback covers private messages and joined channels; checked before group trigger and `/cancel` | Commands use the text path. IRC has no native edit/reaction/slash callback in this adapter. | **Covered for every registered agent-driving path.** | Silent drop. |
| LINE | Signed HTTP webhook: `pkg/channels/line/line.go::LINEChannel.webhookHandler` → `LINEChannel.processEvent` | `message` events cover user, group, and room sources and all decoded message types | Non-message webhook events are ignored. Commands use the text path. No edit/reaction/slash callback drives the agent. | **Publication covered, but checked late:** media download and reply/quote-token caching occur before authorization. This permits resource use by a denied sender, but not agent invocation or `/cancel`. | Silent drop. |
| Matrix | Sync callbacks for `m.room.message`, encrypted messages, and membership: `pkg/channels/matrix/matrix.go::MatrixChannel.Start` | Message handler covers direct and group rooms, plaintext and decrypted media/text | Edits are explicitly ignored; reactions are not registered. Text `/cancel` is checked after authorization. Membership invites can auto-join without sender allow-list evaluation, but do not publish an agent message. | **Agent publication and `/cancel` covered, but checked after inbound content/media extraction.** Auto-join expands room exposure independently of sender authorization. | Debug log with sender ID. |
| QQ | WebSocket C2C and group-@ handlers: `pkg/channels/qq/qq.go::QQChannel.Start`, `QQChannel.handleC2CMessage`, `QQChannel.handleGroupATMessage` | Separate direct and group-mention handlers both check before attachment extraction | Commands use the text path. Other event types, edits, and reactions are not registered. | **Covered for every registered agent-driving path.** | Silent drop. |
| Slack | Socket Mode Events API, slash-command, and interactive envelopes: `pkg/channels/slack/slack.go::SlackChannel.eventLoop` | `SlackChannel.handleMessageEvent` covers DMs/channels; `SlackChannel.handleAppMention` covers mentions | `SlackChannel.handleSlashCommand` performs its own sender check. Interactive callbacks are acknowledged but do not drive the agent. Edited-message subtypes are ignored; reactions are not registered. | **Covered across message, mention, and native slash-command paths.** | Debug log for message, mention, and slash-command denial. |
| Telegram | Long polling `AnyMessage`: `pkg/channels/telegram/telegram.go::TelegramChannel.Start` → `TelegramChannel.handleMessage` | One handler covers private, group, supergroup, forum-topic, text, and media messages; checked before media download | Bot commands are ordinary `Message` objects and are checked. Edited messages, callbacks, and reactions have no registered handler. | **Covered for every registered agent-driving path.** | Debug log with user ID. |
| WeCom | Authenticated WebSocket message/event callbacks: `pkg/channels/wecom/wecom.go::WeComChannel.handleEnvelope` | `WeComChannel.dispatchIncoming` handles direct/group text, voice, image, file, video, and mixed messages | Event callbacks are parsed then ignored. On the baseline, text `/cancel` was intercepted before the shared check. | **Baseline defect reproduced and fixed on this branch.** The new entry check precedes message-type handling, media download, route/turn creation, outbound response, `/cancel`, and final publication. The shared check remains as defence in depth. | Fixed path writes a debug denial with sender ID. |
| Weixin | Long-poll messages: `pkg/channels/weixin/weixin.go::WeixinChannel.pollLoop` → `WeixinChannel.handleInboundMessage` | Adapter treats every inbound sender as a direct peer; handles text and media items | Commands use the text path and are checked before `/cancel`. No group, edit, reaction, or separate callback path is registered. | **Publication covered, but checked after media download.** Denied senders can consume inbound media resources. | Debug log with sender ID. |
| WhatsApp native | whatsmeow `events.Message`: `pkg/channels/whatsapp_native/whatsapp_native.go::WhatsAppNativeChannel.eventHandler` → `WhatsAppNativeChannel.handleIncoming` | One handler covers direct/group conversation or extended-text messages | Commands use the checked text path. Media-only, protocol edit, and reaction bodies produce no supported content and are ignored; no separate callback handler drives the agent. | **Covered for every currently supported agent-driving path.** | Silent drop. |

## Configuration reachability

`allow_from` is a per-instance setting for all 13 channel types. It is reachable through both JSON configuration and the UI. There is no missing global field: the intended source of truth is each channel instance in `pkg/config/config_channels_instance.go::InstanceToTelegram` through `pkg/config/config_channels_instance.go::InstanceToGoogleChat`.

The UI verdict is **partially reachable, materially misleading**:

- `src/lib/channel-fields.ts::CHANNEL_FIELDS` exposes an advanced text input for all 13 types and explicitly says “empty = allow all.”
- `src/components/skills/ChannelConfigPanel.tsx::ChannelConfigPanel.buildSubmitPayload` sends the text input unchanged.
- `pkg/gateway/rest_channels.go::restAPI.configureChannel` stores that channel-specific value unchanged.
- `pkg/config/config_unmarshal.go::FlexibleStringSlice.UnmarshalJSON` accepts a JSON string as exactly one list item; only `FlexibleStringSlice.UnmarshalText` splits comma-separated environment-variable input.
- Therefore a UI entry such as `alice, bob` becomes one unmatched identifier. Existing JSON arrays remain valid if the input is not edited, and a single UI identifier works.

A second reachability problem is incorrect field guidance. Runtime matching is sender-only through `pkg/identity/identity.go::MatchAllowed`; it never compares chat, room, group, server, or channel ID. Yet the UI says those IDs are accepted for Telegram, Discord, Slack, WhatsApp, Feishu, Matrix, LINE, QQ, and IRC. Google Chat's placeholder suggests email addresses, while the adapter supplies the platform's `sender.name` resource as the ID in `pkg/channels/googlechat/googlechat.go::GoogleChatChannel.processEvent`.

## Fail-open default by channel class

The founder has deferred a default-policy change. The recommendations below make the existing choice visible and give operators safer guidance; they do not propose silently changing existing installations.

| Channel | Exposure class | What empty `allow_from` means in practice | Recommendation while fail-open remains |
|---|---|---|---|
| Telegram | Public-address bot | Anyone who can find or receive the bot handle can send a direct or group message. | Strong setup warning; recommend stable numeric user IDs before enabling. |
| Discord | Mixed community boundary | Guild membership narrows some traffic, but direct messages and membership in broad/public guilds weaken that boundary. | Strong warning; recommend stable Discord user IDs, particularly when DMs are enabled. |
| Slack | Tenant-bounded | Workspace membership is an independent access boundary; the bot still accepts every member who can reach it. | Informational warning; empty can be a reasonable deliberate workspace-wide choice. |
| WhatsApp native | Public phone address | Anyone able to message the linked phone identity can reach the channel. | Strong warning; recommend exact sender JIDs, with UI-assisted normalization. |
| Feishu / Lark | Tenant-bounded | Tenant/app installation limits the audience, though group membership may still be broad. | Informational warning; recommend stable open/user IDs for restricted agents. |
| Matrix | Federated/open | Users and rooms may cross homeservers; invitations can auto-join independently of the sender list. | Strong warning; recommend canonical sender IDs and a separate review of invite policy. |
| LINE | Public bot address | Anyone who can add or message the bot can reach the webhook. | Strong warning; recommend LINE user IDs. |
| DingTalk | Tenant-bounded | Organization/app installation constrains reach, but the bot accepts every reachable member. | Informational warning; recommend user IDs for privileged agents. |
| QQ | Mixed/public bot | C2C users and members of configured groups can reach registered handlers. | Strong warning; recommend QQ user IDs. |
| WeCom | Tenant-bounded | Organization installation constrains reach; every reachable member is accepted when empty. | Informational warning; recommend stable WeCom user IDs for privileged agents. |
| Weixin | Public or contact-addressed account | Every sender the connected account can receive from is accepted. | Strong warning; recommend sender OpenIDs. |
| IRC | Open/federated network | Anyone able to join or message on the network can present a nick; a nick is not inherently a verified account identity. | Strong warning; require a server/account-authentication plan before relying on nick filtering. |
| Google Chat | Mixed workspace/space boundary | Workspace and space membership constrain reach, but external/shared spaces can widen it. | Informational-to-strong warning based on space exposure; recommend exact sender `name` resources. |

The product should preserve the explicit empty-list choice for now but distinguish “tenant-wide access” from “internet-addressable access” in the setup copy. That is a disclosure and guidance change, not a policy flip.

## Adjacent identity and enforcement findings

1. **Compound `id|username` is an OR, not a binding.** `pkg/identity/identity.go::MatchAllowed` authorizes when either the ID or username matches. If a username is mutable or reused, `123|alice` can authorize a different account named `alice` even when its ID is not `123`. Stable platform IDs should be the primary recommendation; if the compound form is intended to bind two attributes, its semantics need a separately specified change.
2. **Case handling is inconsistent.** Canonical `platform:id` matching uses `strings.EqualFold` over the entire value, including the ID, while plain platform-ID and username matching are case-sensitive. That can over-authorize case-distinct canonical IDs and under-authorize usernames whose platform treats case as insignificant. See `pkg/identity/identity.go::BuildCanonicalID` and `pkg/identity/identity.go::MatchAllowed`.
3. **Matrix IDs collide with canonical-ID parsing.** A documented Matrix ID such as `@user:example.org` contains a colon. `pkg/identity/identity.go::ParseCanonicalID` treats the prefix before the first colon as a platform, and `MatchAllowed` accepts every non-numeric prefix as canonical despite its “known platform” comment. It therefore returns before the plain platform-ID comparison. The currently reliable form is `matrix:@user:example.org`, not the UI/docs example. This is a source-proven configuration defect; no behavior change is included here.
4. **IRC identifies only by nick.** `pkg/channels/irc/handler.go::IRCChannel.onPrivmsg` supplies the current nick as both platform ID and username. Nick ownership depends on the IRC network and services configuration, so the allow-list is not independently identity-proof.
5. **Room/group entries cannot authorize a sender.** The common matcher sees only `bus.SenderInfo`; chat/room/group IDs are evaluated for routing but never by `MatchAllowed`. The UI claims support for group, room, server, or channel IDs on nine channel forms, so those entries either fail to match or give the operator a false model of the protection.
6. **Some adapters authorize after side effects or resource work.** DingTalk stores the session webhook before its check; LINE caches reply/quote state and can download media; Matrix and Weixin can extract or download media before authorization. Their final message and `/cancel` paths remain protected, but a denied sender can still alter reply state or consume local/network resources. The WeCom variant was more severe because it also reached route/turn state, outbound response, and cancellation; that reproduced bug is fixed here. See `pkg/channels/dingtalk/dingtalk.go::DingTalkChannel.onChatBotMessageReceived`, `pkg/channels/line/line.go::LINEChannel.processEvent`, `pkg/channels/matrix/matrix.go::MatrixChannel.handleMessage`, and `pkg/channels/weixin/weixin.go::WeixinChannel.handleInboundMessage`.
7. **Unknown-sender fallbacks deserve tightening.** WeCom and Feishu substitute the literal `unknown` when the platform sender ID is absent. An operator who explicitly allowed that literal would authorize unidentified traffic. Other adapters reject missing identities or naturally fail matching.
8. **WhatsApp and Google Chat require exact runtime identifiers.** WhatsApp supplies its event sender JID and performs no phone-number normalization; Google Chat supplies the event's sender `name` resource, not an email address. The present placeholders encourage values that may never match.
9. **Denied-sender visibility is inconsistent.** Discord, Slack, Telegram, Matrix, Weixin, and the fixed WeCom path emit denial-specific debug logs. Feishu, Google Chat, IRC, LINE, QQ, and WhatsApp silently drop; DingTalk emits only its generic pre-check receive log, including a truncated content preview. None provides an operator-level warning that an allow-list is misconfigured or under repeated probing.

## Discoverability and operator warning

The operator can discover the default, but only by expanding advanced settings or reading connector documentation:

- All 13 definitions in `src/lib/channel-fields.ts::CHANNEL_FIELDS` say that empty means allow all.
- All 13 connector guides document `allow_from` and the empty-list behavior in their configuration sections: `docs/connectors/telegram.md::Configuration`, `docs/connectors/discord.md::Configuration`, `docs/connectors/slack.md::Configuration`, `docs/connectors/whatsapp_native.md::Configuration`, `docs/connectors/feishu.md::Configuration`, `docs/connectors/matrix.md::Configuration`, `docs/connectors/line.md::Configuration`, `docs/connectors/dingtalk.md::Configuration`, `docs/connectors/qq.md::Configuration`, `docs/connectors/wecom.md::Configuration`, `docs/connectors/weixin.md::Configuration`, `docs/connectors/irc.md::Configuration`, and `docs/connectors/google-chat.md::Configuration`.
- The configuration screen reaches these fields through `src/components/screens/ConnectorsScreen.tsx::ConnectorsScreen` and `src/components/skills/ChannelConfigPanel.tsx::ChannelConfigPanel`, but the field sits in advanced settings rather than in the enable decision.
- No channel start path and no common initialization path in `pkg/channels/manager.go::Manager.initChannels` emits an operator-visible warning when an enabled channel has an empty list. There is also no security-status surface summarizing open channels.

Thus the policy is documented but weakly surfaced at the moment it matters. A user can enable an internet-addressable channel without affirmatively seeing that every sender is accepted.

## Recommended UI design

1. Replace the free-text field with a tokenized per-channel list. Each token should be submitted as an element of a JSON array, not as one comma-containing string.
2. Validate using channel-specific identity rules and examples: numeric Telegram user ID, Discord/Slack user ID, exact WhatsApp JID, Matrix canonical sender ID, Google Chat sender `name`, and so on. Do not offer room/group/channel identifiers unless runtime matching actually supports them.
3. Put a plain-language empty state next to the enable action: **“No sender restrictions — everyone who can reach this bot can use the assigned agent.”** Keep empty valid.
4. For public-address channels, show a non-blocking acknowledgement on first enable with an empty list. For tenant-bounded channels, show a lower-severity informational note. Existing configurations should not be auto-modified.
5. After save, show the parsed count and normalized values, for example “3 allowed senders,” so an operator can verify that `alice, bob` did not become one entry.
6. Add a startup warning and a Security/Connectors summary for enabled, empty-list channels. Rate-limit repeated denied-sender logging and never include message content or credentials.

The UI conversion defect should receive its own test-driven fix: split/trim into an array at the form boundary, preserve already stored arrays, validate empty tokens and duplicates, and cover round-trip save/reopen behavior. It was not bundled into this investigation-only change.

## Reproduction receipts and code changes

### RED — baseline defect reproduced

Test: `pkg/channels/wecom/wecom_test.go::TestDispatchIncoming_DeniedSenderHasNoSideEffects`

```text
=== RUN   TestDispatchIncoming_DeniedSenderHasNoSideEffects
    wecom_test.go:77: denied sender side effects: cancel calls=1, commands=2, turn=false, route=true; want all zero/false
--- FAIL: TestDispatchIncoming_DeniedSenderHasNoSideEffects (0.00s)
FAIL
FAIL    github.com/elicify-ai/omnipus/pkg/channels/wecom    2.549s
FAIL
exit=1
```

This proves the configured denied sender reached cancellation, sent two platform commands, and persisted a route before the shared publication check rejected the message.

### Root cause and fix

`pkg/channels/wecom/wecom.go::WeComChannel.dispatchIncoming` built a structured sender but did not call `IsAllowedSender` before media processing, state persistence, response creation, and `DispatchCancelIfRecognized`. It relied only on the later check in `pkg/channels/base.go::BaseChannel.HandleMessage`.

The fix adds the same structured-sender check immediately after identity construction and writes a debug denial containing only the sender ID. It does not alter `BaseChannel.IsAllowedSender`: an empty list still returns `true`.

### GREEN — fix restored

```text
=== RUN   TestDispatchIncoming_DeniedSenderHasNoSideEffects
--- PASS: TestDispatchIncoming_DeniedSenderHasNoSideEffects (0.00s)
=== RUN   TestDispatchIncoming_DeniedSenderDoesNotDownloadMedia
--- PASS: TestDispatchIncoming_DeniedSenderDoesNotDownloadMedia (0.00s)
=== RUN   TestDispatchIncoming_UsesActualChatIDAndStoresReqIDRoute
--- PASS: TestDispatchIncoming_UsesActualChatIDAndStoresReqIDRoute (0.01s)
PASS
ok      github.com/elicify-ai/omnipus/pkg/channels/wecom    5.444s
exit=0
```

The media test uses a counting HTTP transport and proves that a denied image message makes zero download requests. The existing positive-path test uses the unchanged empty allow-list and still publishes, queues the turn, persists the route, and sends the opening command. This directly verifies that the founder-deferred fail-open behavior remains intact.

### Revert proof

After the green run, the new entry check was removed without changing the test. The test returned to the same real failure:

```text
=== RUN   TestDispatchIncoming_DeniedSenderHasNoSideEffects
    wecom_test.go:78: denied sender side effects: cancel calls=1, commands=2, turn=false, route=true; want all zero/false
--- FAIL: TestDispatchIncoming_DeniedSenderHasNoSideEffects (0.01s)
=== RUN   TestDispatchIncoming_DeniedSenderDoesNotDownloadMedia
    wecom_test.go:133: denied sender media requests = 1, want 0
--- FAIL: TestDispatchIncoming_DeniedSenderDoesNotDownloadMedia (0.00s)
FAIL
FAIL    github.com/elicify-ai/omnipus/pkg/channels/wecom    11.879s
FAIL
exit=1
```

The fix was then restored and the three-test GREEN receipt above was captured. GitNexus classified the changed production symbol as LOW risk: two direct callers, five affected symbols total, one module, and no indexed affected processes.

### Verification gates

- Focused WeCom regression and positive-path tests: **PASS**, uncached, `exit=0`.
- `make lint-guards`: **PASS**, all 27 guards.
- `make lint-budgets`: **PASS**, `exit=0`; only existing warning-level/grandfathered entries were reported.
- `git diff --check`: **PASS**.
- Final GitNexus change detection: **LOW** risk, two changed code files and zero affected indexed processes.
- Independent code review and simplification review: **clean** after the denied-media test and DingTalk report correction.

## Honest gaps

- This is a source-level and focused unit-test review. No live vendor credentials were used, and no secret values were read or printed.
- Registered inbound event starts were exhaustively traced in this repository, but every vendor event variant was not replayed against a live service. “Ignored/unregistered” means the current adapter has no agent-driving handler for it; a future SDK registration must repeat this review.
- The UI multi-entry, Matrix canonical-ID, compound identity, late media-download, invite-policy, and warning/discoverability findings are reported, not changed. Each needs its own specification and RED test before implementation.
- Only the narrowly scoped WeCom tests were run locally, as required by the repository's memory limits. The full Go suite was not run locally; CI remains the authority for broader Go results.
- This review did not change any REST or WebSocket wire format, so contract regeneration was not applicable.
