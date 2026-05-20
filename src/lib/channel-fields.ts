// Channel configuration field definitions
// Each entry maps to the Go ChannelsConfig struct fields in pkg/config/config.go

export interface ChannelField { // not-wire-format: UI form-field descriptor for channel config forms; never serialised over any HTTP/WS boundary
  key: string
  label: string
  type: 'text' | 'password' | 'url' | 'number' | 'toggle' | 'textarea'
  required: boolean
  placeholder?: string
  helpText?: string
}

export const CHANNEL_FIELDS: Record<string, ChannelField[]> = {
  telegram: [
    {
      key: 'token',
      label: 'Bot Token',
      type: 'password',
      required: true,
      placeholder: '123456:ABC-DEF...',
      helpText: 'Get from @BotFather on Telegram',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated user/chat IDs (empty = allow all)',
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when the bot is mentioned in group chats',
    },
    {
      key: 'base_url',
      label: 'Custom API URL',
      type: 'url',
      required: false,
      placeholder: 'https://api.telegram.org',
      helpText: 'Override the default Telegram Bot API URL (for self-hosted)',
    },
    {
      key: 'proxy',
      label: 'Proxy URL',
      type: 'url',
      required: false,
      placeholder: 'socks5://...',
      helpText: 'Optional proxy for connecting to Telegram',
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
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
      helpText: 'Comma-separated user/server IDs (empty = allow all)',
    },
    {
      key: 'mention_only',
      label: 'Mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when the bot is mentioned',
    },
    {
      key: 'proxy',
      label: 'Proxy URL',
      type: 'url',
      required: false,
      placeholder: 'socks5://...',
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
    },
    {
      key: 'app_token',
      label: 'App Token',
      type: 'password',
      required: true,
      placeholder: 'xapp-...',
      helpText: 'App-Level Token for Socket Mode',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'U12345, C12345',
      helpText: 'Comma-separated user/channel IDs (empty = allow all)',
    },
  ],

  whatsapp: [
    {
      key: 'use_native',
      label: 'Native Mode (whatsmeow)',
      type: 'toggle',
      required: false,
      helpText: 'Uses built-in WhatsApp connection — requires QR code scan',
    },
    {
      key: 'bridge_url',
      label: 'Bridge URL',
      type: 'url',
      required: false,
      placeholder: 'ws://localhost:3001',
      helpText: 'Only needed if native mode is off',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: '+1234567890, group_jid@g.us',
      helpText: 'Comma-separated phone numbers or JIDs (empty = allow all)',
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
      helpText: 'Only respond when mentioned in group chats',
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
    },
    {
      key: 'app_secret',
      label: 'App Secret',
      type: 'password',
      required: true,
      helpText: 'Application Secret from Feishu/Lark Developer Console',
    },
    {
      key: 'encrypt_key',
      label: 'Encrypt Key',
      type: 'password',
      required: false,
      helpText: 'Event encryption key (set in Event Subscriptions)',
    },
    {
      key: 'verification_token',
      label: 'Verification Token',
      type: 'password',
      required: false,
      helpText: 'Token for verifying webhook requests',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
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
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: '@user:matrix.org, !room:matrix.org',
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
    },
  ],

  line: [
    {
      key: 'channel_secret',
      label: 'Channel Secret',
      type: 'password',
      required: true,
      helpText: 'Channel Secret from LINE Developers Console',
    },
    {
      key: 'channel_access_token',
      label: 'Channel Access Token',
      type: 'password',
      required: true,
      helpText: 'Long-lived Channel Access Token from LINE Developers Console',
    },
    {
      key: 'webhook_host',
      label: 'Webhook Host',
      type: 'text',
      required: false,
      placeholder: '0.0.0.0',
      helpText: 'Host to listen on for LINE webhook events',
    },
    {
      key: 'webhook_port',
      label: 'Webhook Port',
      type: 'number',
      required: false,
      placeholder: '8443',
    },
    {
      key: 'webhook_path',
      label: 'Webhook Path',
      type: 'text',
      required: false,
      placeholder: '/webhook',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'U1234..., C1234...',
    },
  ],

  dingtalk: [
    {
      key: 'client_id',
      label: 'Client ID',
      type: 'text',
      required: true,
      helpText: 'Client ID from DingTalk Open Platform',
    },
    {
      key: 'client_secret',
      label: 'Client Secret',
      type: 'password',
      required: true,
      helpText: 'Client Secret from DingTalk Open Platform',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
    },
  ],

  qq: [
    {
      key: 'app_id',
      label: 'App ID',
      type: 'text',
      required: true,
      helpText: 'App ID from QQ Open Platform',
    },
    {
      key: 'app_secret',
      label: 'App Secret',
      type: 'password',
      required: true,
      helpText: 'App Secret from QQ Open Platform',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, group_id1',
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
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
      helpText: 'Application ID from WeCom (Enterprise WeChat) admin console',
    },
    {
      key: 'secret',
      label: 'Secret',
      type: 'password',
      required: true,
      helpText: 'Application Secret from WeCom admin console',
    },
    {
      key: 'websocket_url',
      label: 'WebSocket URL',
      type: 'url',
      required: false,
      placeholder: 'wss://...',
      helpText: 'Custom WebSocket relay URL if needed',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, user_id2',
    },
    {
      key: 'send_thinking_message',
      label: 'Send thinking message',
      type: 'toggle',
      required: false,
      helpText: 'Show a placeholder message while the bot is thinking',
    },
  ],

  onebot: [
    {
      key: 'ws_url',
      label: 'WebSocket URL',
      type: 'url',
      required: true,
      placeholder: 'ws://localhost:3000',
      helpText: 'WebSocket address of your OneBot-compatible client (e.g. go-cqhttp)',
    },
    {
      key: 'access_token',
      label: 'Access Token',
      type: 'password',
      required: false,
      helpText: 'Optional access token for authenticating with the OneBot client',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'user_id1, group_id1',
    },
    {
      key: 'group_trigger.mention_only',
      label: 'Groups: mention only',
      type: 'toggle',
      required: false,
    },
    {
      key: 'reconnect_interval',
      label: 'Reconnect Interval (s)',
      type: 'number',
      required: false,
      placeholder: '5',
      helpText: 'Seconds to wait before reconnecting on disconnect',
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
    },
    {
      key: 'sasl_password',
      label: 'SASL Password',
      type: 'password',
      required: false,
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'nick1, #channel1',
    },
  ],



  weixin: [
    {
      key: 'token',
      label: 'Token',
      type: 'password',
      required: true,
      helpText: 'WeChat Official Account token for webhook verification',
    },
    {
      key: 'account_id',
      label: 'Account ID',
      type: 'text',
      required: false,
      helpText: 'WeChat account ID',
    },
    {
      key: 'base_url',
      label: 'API Base URL',
      type: 'url',
      required: false,
      placeholder: 'https://api.weixin.qq.com',
    },
    {
      key: 'allow_from',
      label: 'Allow From',
      type: 'text',
      required: false,
      placeholder: 'openid1, openid2',
    },
    {
      key: 'proxy',
      label: 'Proxy URL',
      type: 'url',
      required: false,
      placeholder: 'http://...',
    },
  ],
}

export function getChannelFields(channelId: string): ChannelField[] {
  return CHANNEL_FIELDS[channelId.toLowerCase()] ?? []
}
