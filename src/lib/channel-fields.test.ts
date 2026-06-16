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

describe('getChannelFields — whatsapp_native normalisation', () => {
  it('normalises whatsapp_native to whatsapp for field lookup', () => {
    expect(getChannelFields('whatsapp_native')).toEqual(getChannelFields('whatsapp'))
    expect(getChannelFields('whatsapp_native').length).toBeGreaterThan(0)
  })
})
