// Channel configuration field definitions
// Each entry maps to the Go ChannelsConfig struct fields in pkg/config/config.go

import type { ChannelId } from '@/lib/api/generated/openapi-types'
import { WHATSAPP_NATIVE_CHANNEL_ID } from '@/components/skills/whatsappChannelId'

export interface ChannelField { // not-wire-format: UI form-field descriptor for channel config forms; never serialised over any HTTP/WS boundary
  key: string
  label: string
  type: 'text' | 'password' | 'url' | 'number' | 'toggle' | 'textarea'
  required: boolean
  placeholder?: string
  helpText?: string
  /** Where to get this value — rendered as <a target="_blank" rel="noopener noreferrer">. MUST be a compile-time https:// literal (M-4). */
  helpLink?: { label: string; url: string }
  /** Render this field under the "Advanced" collapsible (default-hidden). */
  advanced?: boolean
  /** Mutually-exclusive authentication group identifier (GChat pick-one). */
  authGroup?: string
}

// drift-guard: keying by the generated ChannelId enum (via Partial — not every
// channel has a config form, e.g. webchat) makes a removed/renamed channel id a
// tsc error here, forcing this list back in sync with the contract.
export const CHANNEL_FIELDS: Partial<Record<ChannelId, ChannelField[]>> = {
  telegram: [
    {
      key: 'token',
      label: 'Bot Token',
      type: 'password',
      required: true,
      placeholder: '123456:ABC-DEF...',
      helpText: 'Get from @BotFather on Telegram',
      helpLink: { label: 'Open BotFather', url: 'https://t.me/BotFather' },
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated user/chat IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when the bot is mentioned in group chats',
      advanced: true,
    },
    {
      key: 'base_url',
      label: 'Custom API URL',
      type: 'url',
      required: false,
      placeholder: 'https://api.telegram.org',
      helpText: 'Override the default Telegram Bot API URL (for self-hosted)',
      advanced: true,
    },
    {
      key: 'proxy',
      label: 'Proxy URL',
      type: 'url',
      required: false,
      placeholder: 'socks5://...',
      helpText: 'Optional proxy for connecting to Telegram',
      advanced: true,
    },
    {
      key: 'use_markdown_v2',
      label: 'Use MarkdownV2',
      type: 'toggle',
      required: false,
      helpText: 'Send messages using MarkdownV2 format instead of HTML',
    },
  ],

  discord: [
    {
      key: 'token',
      label: 'Bot Token',
      type: 'password',
      required: true,
      placeholder: 'MTAx...',
      helpText: 'Get from Discord Developer Portal → Bot → Token',
      helpLink: { label: 'Discord Developer Portal', url: 'https://discord.com/developers/applications' },
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated user/server IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'mention_only',
      label: 'Mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when the bot is mentioned',
      advanced: true,
    },
    {
      key: 'proxy',
      label: 'Proxy URL',
      type: 'url',
      required: false,
      placeholder: 'socks5://...',
      helpText: 'Optional proxy for connecting to Discord',
      advanced: true,
    },
  ],

  slack: [
    {
      key: 'bot_token',
      label: 'Bot Token',
      type: 'password',
      required: true,
      placeholder: 'xoxb-...',
      helpText: 'OAuth Bot Token from Slack App settings',
      helpLink: { label: 'Slack App settings', url: 'https://api.slack.com/apps' },
    },
    {
      key: 'app_token',
      label: 'App Token',
      type: 'password',
      required: true,
      placeholder: 'xapp-...',
      helpText: 'App-Level Token for Socket Mode',
      helpLink: { label: 'Slack App settings', url: 'https://api.slack.com/apps' },
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'U12345, C12345',
      helpText: 'Comma-separated user/channel IDs (empty = allow all)',
      advanced: true,
    },
  ],

  whatsapp: [
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: '+1234567890, group_jid@g.us',
      helpText: 'Comma-separated phone numbers or JIDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when mentioned in group chats',
      advanced: true,
    },
  ],

  feishu: [
    {
      key: 'app_id',
      label: 'App ID',
      type: 'text',
      required: true,
      placeholder: 'cli_...',
      helpText: 'Application ID from Feishu/Lark Developer Console',
      helpLink: { label: 'Feishu Developer Console', url: 'https://open.feishu.cn/app' },
    },
    {
      key: 'app_secret',
      label: 'App Secret',
      type: 'password',
      required: true,
      helpText: 'Application Secret from Feishu/Lark Developer Console',
      helpLink: { label: 'Feishu Developer Console', url: 'https://open.feishu.cn/app' },
    },
    {
      key: 'encrypt_key',
      label: 'Encrypt Key',
      type: 'password',
      required: false,
      helpText: 'Event encryption key (set in Event Subscriptions → Encrypt Key)',
      advanced: true,
    },
    {
      key: 'verification_token',
      label: 'Verification Token',
      type: 'password',
      required: false,
      helpText: 'Token for verifying webhook requests (set in Event Subscriptions)',
      advanced: true,
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated user/chat IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'is_lark',
      label: 'Lark Mode',
      type: 'toggle',
      required: false,
      helpText: 'Enable if using Lark (international version of Feishu)',
    },
  ],

  matrix: [
    {
      key: 'homeserver',
      label: 'Homeserver URL',
      type: 'url',
      required: true,
      placeholder: 'https://matrix.org',
      helpText: 'Your Matrix homeserver address',
    },
    {
      key: 'user_id',
      label: 'User ID',
      type: 'text',
      required: true,
      placeholder: '@botname:matrix.org',
      helpText: 'Full Matrix user ID for the bot',
    },
    {
      key: 'access_token',
      label: 'Access Token',
      type: 'password',
      required: true,
      helpText: 'Matrix access token for the bot account',
      helpLink: { label: 'How to get a Matrix access token', url: 'https://spec.matrix.org/latest/client-server-api/#client-authentication' },
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: '@user:matrix.org, !room:matrix.org',
      helpText: 'Comma-separated Matrix user IDs or room IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'join_on_invite',
      label: 'Auto-join on invite',
      type: 'toggle',
      required: false,
      helpText: 'Automatically join rooms when invited',
    },
    {
      key: 'crypto_passphrase',
      label: 'Crypto Passphrase',
      type: 'password',
      required: false,
      helpText: 'Passphrase for end-to-end encryption database',
      advanced: true,
    },
  ],

  line: [
    {
      key: 'channel_secret',
      label: 'Channel Secret',
      type: 'password',
      required: true,
      helpText: 'Channel Secret from LINE Developers Console → Basic settings',
      helpLink: { label: 'LINE Developers Console', url: 'https://developers.line.biz/console/' },
    },
    {
      key: 'channel_access_token',
      label: 'Channel Access Token',
      type: 'password',
      required: true,
      helpText: 'Long-lived Channel Access Token from LINE Developers Console → Messaging API',
      helpLink: { label: 'LINE Developers Console', url: 'https://developers.line.biz/console/' },
    },
    {
      key: 'webhook_host',
      label: 'Webhook Host',
      type: 'text',
      required: false,
      placeholder: '0.0.0.0',
      helpText: 'Host address to listen on for LINE webhook events',
      advanced: true,
    },
    {
      key: 'webhook_port',
      label: 'Webhook Port',
      type: 'number',
      required: false,
      placeholder: '8443',
      helpText: 'Port number to listen on for LINE webhook events',
      advanced: true,
    },
    {
      key: 'webhook_path',
      label: 'Webhook Path',
      type: 'text',
      required: false,
      placeholder: '/webhook',
      helpText: 'URL path for the LINE webhook endpoint',
      advanced: true,
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'U1234..., C1234...',
      helpText: 'Comma-separated LINE user/group IDs (empty = allow all)',
      advanced: true,
    },
  ],

  dingtalk: [
    {
      key: 'client_id',
      label: 'Client ID',
      type: 'text',
      required: true,
      helpText: 'Client ID from DingTalk Open Platform → Application credentials',
      helpLink: { label: 'DingTalk Open Platform', url: 'https://open.dingtalk.com/document/orgapp/configure-robot' },
    },
    {
      key: 'client_secret',
      label: 'Client Secret',
      type: 'password',
      required: true,
      helpText: 'Client Secret from DingTalk Open Platform → Application credentials',
      helpLink: { label: 'DingTalk Open Platform', url: 'https://open.dingtalk.com/document/orgapp/configure-robot' },
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated DingTalk user IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when the bot is mentioned in group chats',
      advanced: true,
    },
  ],

  qq: [
    {
      key: 'app_id',
      label: 'App ID',
      type: 'text',
      required: true,
      helpText: 'App ID from QQ Open Platform → Application credentials',
      helpLink: { label: 'QQ Open Platform', url: 'https://q.qq.com/wiki/develop/bot/base/intro.html' },
    },
    {
      key: 'app_secret',
      label: 'App Secret',
      type: 'password',
      required: true,
      helpText: 'App Secret from QQ Open Platform → Application credentials',
      helpLink: { label: 'QQ Open Platform', url: 'https://q.qq.com/wiki/develop/bot/base/intro.html' },
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, group_id1',
      helpText: 'Comma-separated QQ user or group IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when the bot is mentioned in group chats',
      advanced: true,
    },
    {
      key: 'send_markdown',
      label: 'Send Markdown',
      type: 'toggle',
      required: false,
      helpText: 'Send messages using Markdown format',
    },
  ],

  wecom: [
    {
      key: 'bot_id',
      label: 'Bot ID',
      type: 'text',
      required: true,
      helpText: 'Application ID from WeCom (Enterprise WeChat) admin console → My applications',
      helpLink: { label: 'WeCom admin console', url: 'https://work.weixin.qq.com/wework_admin/frame#apps' },
    },
    {
      key: 'secret',
      label: 'Secret',
      type: 'password',
      required: true,
      helpText: 'Application Secret from WeCom admin console → My applications',
      helpLink: { label: 'WeCom admin console', url: 'https://work.weixin.qq.com/wework_admin/frame#apps' },
    },
    {
      key: 'websocket_url',
      label: 'WebSocket URL',
      type: 'url',
      required: false,
      placeholder: 'wss://...',
      helpText: 'Custom WebSocket relay URL if needed (leave blank for default)',
      advanced: true,
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated WeCom user IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'send_thinking_message',
      label: 'Send thinking message',
      type: 'toggle',
      required: false,
      helpText: 'Show a placeholder message while the bot is thinking',
    },
  ],

  'google-chat': [
    {
      key: 'webhook_url',
      label: 'Webhook URL',
      type: 'password',
      required: false,
      placeholder: 'https://chat.googleapis.com/v1/spaces/...',
      helpText: 'Incoming webhook URL for a Google Chat space (simplest setup)',
      helpLink: { label: 'Create a Google Chat webhook', url: 'https://developers.google.com/workspace/chat/quickstart/webhooks' },
      authGroup: 'webhook',
    },
    {
      key: 'service_account_json',
      label: 'Service Account JSON',
      type: 'textarea',
      required: false,
      placeholder: '{ "type": "service_account", ... }',
      helpText: 'Paste the service-account key JSON (for bot/API mode). Stored encrypted.',
      helpLink: { label: 'Create a service account key', url: 'https://developers.google.com/workspace/chat/authenticate-authorize-chat-app' },
      authGroup: 'service_account',
    },
    {
      key: 'service_account_file',
      label: 'Service Account File Path',
      type: 'text',
      required: false,
      placeholder: '/path/to/service-account.json',
      helpText: 'Alternative to pasting JSON: path to the service-account key file on disk',
      authGroup: 'service_account',
    },
    {
      key: 'space',
      label: 'Space',
      type: 'text',
      required: false,
      placeholder: 'spaces/AAAA...',
      helpText: 'Default Google Chat space to post to (bot mode)',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user1@example.com, user2@example.com',
      helpText: 'Comma-separated user IDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when mentioned in a space',
      advanced: true,
    },
  ],

  irc: [
    {
      key: 'server',
      label: 'Server',
      type: 'text',
      required: true,
      placeholder: 'irc.libera.chat:6697',
      helpText: 'IRC server address with optional port',
    },
    {
      key: 'nick',
      label: 'Nick',
      type: 'text',
      required: true,
      placeholder: 'omnipus-bot',
      helpText: 'Bot nickname to use on IRC',
    },
    {
      key: 'channels',
      label: 'Channels',
      type: 'text',
      required: false,
      placeholder: '#general, #bots',
      helpText: 'Comma-separated list of channels to join',
    },
    {
      key: 'password',
      label: 'Server Password',
      type: 'password',
      required: false,
      helpText: 'Server password (NickServ or server-level, if required)',
    },
    {
      key: 'tls',
      label: 'TLS',
      type: 'toggle',
      required: false,
      helpText: 'Connect using TLS/SSL',
    },
    {
      key: 'sasl_user',
      label: 'SASL Username',
      type: 'text',
      required: false,
      helpText: 'SASL username for server authentication',
      advanced: true,
    },
    {
      key: 'sasl_password',
      label: 'SASL Password',
      type: 'password',
      required: false,
      helpText: 'SASL password for server authentication',
      advanced: true,
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'nick1, #channel1',
      helpText: 'Comma-separated IRC nicks or channels (empty = allow all)',
      advanced: true,
    },
  ],



  weixin: [
    {
      key: 'token',
      label: 'Token',
      type: 'password',
      required: true,
      helpText: 'WeChat Official Account token for webhook verification (set in Basic configuration)',
      helpLink: { label: 'WeChat Official Account platform', url: 'https://mp.weixin.qq.com/' },
    },
    {
      key: 'account_id',
      label: 'Account ID',
      type: 'text',
      required: false,
      helpText: 'WeChat Official Account ID (found in Basic configuration)',
    },
    {
      key: 'base_url',
      label: 'API Base URL',
      type: 'url',
      required: false,
      placeholder: 'https://api.weixin.qq.com',
      helpText: 'Override the default WeChat API URL (leave blank unless using a proxy)',
      advanced: true,
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'openid1, openid2',
      helpText: 'Comma-separated WeChat user OpenIDs (empty = allow all)',
      advanced: true,
    },
    {
      key: 'proxy',
      label: 'Proxy URL',
      type: 'url',
      required: false,
      placeholder: 'http://...',
      helpText: 'Optional proxy for connecting to WeChat API',
      advanced: true,
    },
  ],
}

export function getChannelFields(channelId: string): ChannelField[] {
  // CHANNEL_FIELDS is keyed by the generated ChannelId enum (drift-guard). The
  // caller passes an arbitrary string, so cast at the lookup site; an unknown id
  // simply falls through to the empty default.
  //
  // WHATSAPP_NATIVE_CHANNEL_ID ('whatsapp_native') is the internal registry name for
  // the whatsmeow channel; the REST API uses 'whatsapp' as the canonical ChannelId
  // (normalised in the backend). Both names must resolve to the same field descriptor
  // so the config panel works regardless of which ID the caller holds.
  const normalised = channelId.toLowerCase() === WHATSAPP_NATIVE_CHANNEL_ID ? 'whatsapp' : channelId.toLowerCase()
  return CHANNEL_FIELDS[normalised as ChannelId] ?? []
}
