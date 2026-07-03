/**
 * channel-fields.test.ts — unit assertions for ChannelField metadata (US-C1 / §2.2 / M-4)
 *
 * TDD row 12: every descriptor helpLink.url must match ^https:// (security constraint).
 * helpLink.url values MUST be compile-time constant literals authored in channel-fields.ts —
 * never user/runtime input — and rendered with target="_blank" rel="noopener noreferrer".
 * This test is the compile-time equivalent of a linter: it fails the build if any url
 * slips in that isn't https://, foreclosing javascript:, tabnabbing, and stored XSS.
 */

import { describe, it, expect } from 'vitest'
import { CHANNEL_FIELDS, getChannelFields } from './channel-fields'

describe('helpLinkScheme.test — M-4: every helpLink.url must be https://', () => {
  it('all helpLink.url values across all channel descriptors start with https://', () => {
    const violations: Array<{ channel: string; field: string; url: string }> = []

    for (const [channelId, fields] of Object.entries(CHANNEL_FIELDS)) {
      if (!fields) continue
      for (const field of fields) {
        if (field.helpLink) {
          if (!field.helpLink.url.startsWith('https://')) {
            violations.push({
              channel: channelId,
              field: field.key,
              url: field.helpLink.url,
            })
          }
        }
      }
    }

    expect(violations).toEqual([])
  })

  it('helpLink.label is a non-empty string when helpLink is present', () => {
    const violations: Array<{ channel: string; field: string }> = []

    for (const [channelId, fields] of Object.entries(CHANNEL_FIELDS)) {
      if (!fields) continue
      for (const field of fields) {
        if (field.helpLink && !field.helpLink.label.trim()) {
          violations.push({ channel: channelId, field: field.key })
        }
      }
    }

    expect(violations).toEqual([])
  })

  it('CJK channels (feishu/line/dingtalk/qq/wecom/weixin) have helpText on every required field', () => {
    const cjkChannels = ['feishu', 'line', 'dingtalk', 'qq', 'wecom', 'weixin'] as const
    const missing: Array<{ channel: string; field: string }> = []

    for (const channelId of cjkChannels) {
      const fields = CHANNEL_FIELDS[channelId] ?? []
      for (const field of fields) {
        if (field.required && !field.helpText) {
          missing.push({ channel: channelId, field: field.key })
        }
      }
    }

    expect(missing).toEqual([])
  })

  it('all 13 channels are present in CHANNEL_FIELDS (signal is absent — expected)', () => {
    const expected = [
      'telegram', 'discord', 'slack', 'whatsapp',
      'feishu', 'matrix', 'line', 'dingtalk', 'qq',
      'wecom', 'irc', 'weixin', 'google-chat',
    ]
    for (const ch of expected) {
      expect(CHANNEL_FIELDS).toHaveProperty(ch)
    }
    // signal is confirmed absent from the descriptor (spec §5 US-C1)
    expect(CHANNEL_FIELDS).not.toHaveProperty('signal')
    // email is configured exclusively via the dedicated EmailMailboxPanel
    // (/agents/{id}/mailbox) — ConnectorsScreen filters email out before
    // ChannelConfigPanel could ever open for it, so a second field-catalog
    // entry here would be dead code that can only drift
    expect(CHANNEL_FIELDS).not.toHaveProperty('email')
  })

  it('google-chat authGroup fields belong to known groups', () => {
    const fields = CHANNEL_FIELDS['google-chat'] ?? []
    const knownGroups = new Set(['webhook', 'service_account'])
    for (const field of fields) {
      if (field.authGroup !== undefined) {
        expect(knownGroups.has(field.authGroup)).toBe(true)
      }
    }
  })
})

describe('dead-field regression pins — fields the backend silently drops or never reads', () => {
  it('google-chat has no service_account_file descriptor (backend strips filesystem-path fields on configure — pkg/gateway/rest.go channelFilesystemPathFields — the UI form would be a dead end)', () => {
    const fields = CHANNEL_FIELDS['google-chat'] ?? []
    expect(fields.find((f) => f.key === 'service_account_file')).toBeUndefined()
  })

  it('line has no webhook_host/webhook_port descriptors (the webhook handler is mounted on the shared gateway mux — pkg/channels/line/line.go only reads webhook_path — the backend never consults these)', () => {
    const fields = CHANNEL_FIELDS['line'] ?? []
    expect(fields.find((f) => f.key === 'webhook_host')).toBeUndefined()
    expect(fields.find((f) => f.key === 'webhook_port')).toBeUndefined()
  })

  it('irc includes a nickserv_password field of type password (distinct secret from the server-level password field — pkg/channels/irc/handler.go sends NickServ IDENTIFY when set and SASL is not in use)', () => {
    const fields = CHANNEL_FIELDS['irc'] ?? []
    const field = fields.find((f) => f.key === 'nickserv_password')
    expect(field).toBeDefined()
    expect(field?.type).toBe('password')
  })
})

describe('getChannelFields — whatsapp_native normalisation', () => {
  it('normalises whatsapp_native to whatsapp for field lookup', () => {
    expect(getChannelFields('whatsapp_native')).toEqual(getChannelFields('whatsapp'))
    expect(getChannelFields('whatsapp_native').length).toBeGreaterThan(0)
  })
})

describe('getChannelFields — ADR-029 namespaced instance keys (<type>.<slug>)', () => {
  // Live-UAT regression: the field catalog is per-TYPE, but a namespaced
  // instance id (telegram.sales) missed the lookup entirely, so
  // ChannelConfigPanel rendered NOTHING for any operator-created instance.
  it('resolves a namespaced instance id to its base type fields', () => {
    expect(getChannelFields('telegram.sales')).toEqual(getChannelFields('telegram'))
    expect(getChannelFields('telegram.sales').length).toBeGreaterThan(0)
    expect(getChannelFields('discord.eu-support')).toEqual(getChannelFields('discord'))
  })

  it('namespaced whatsapp instances still normalise through the native alias', () => {
    expect(getChannelFields('whatsapp.sales')).toEqual(getChannelFields('whatsapp'))
    expect(getChannelFields('whatsapp.sales').length).toBeGreaterThan(0)
  })

  it('unknown base types still fall through to the empty default', () => {
    expect(getChannelFields('nonexistent.sales')).toEqual([])
    expect(getChannelFields('')).toEqual([])
  })
})
