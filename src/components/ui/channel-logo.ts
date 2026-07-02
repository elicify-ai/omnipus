// channel-logo.ts — channel base-type -> <BrandIcon> logoSlug mapping
// (ADR-031 Track 2, US-9). Sibling module to brand-icon.tsx so ConnectorsScreen
// (and any future channel-listing surface) doesn't need to redefine this map.
//
// Only the following channel marks are vendored under
// src/assets/brand-logos/ as `c_<slug>.svg`: telegram, discord, matrix,
// whatsapp, googlechat, line, wechat, tencentqq (see VENDORED_CHANNEL_SLUGS).
// A handful of other base types are known to have no vendored asset and are
// documented here so the drift-guard test (channel-logo.test.ts) can assert
// every mappable channelSlug() output lands on either a real vendored mark
// or one of these explicitly-accepted lettermark-only types. Any other,
// undocumented base type still falls back to <BrandIcon>'s lettermark chip
// automatically — channelSlug() never throws — but won't satisfy the guard.

/** Base channel types with a vendored c_<slug>.svg mark on disk. */
export const VENDORED_CHANNEL_SLUGS = [
  'telegram',
  'discord',
  'matrix',
  'whatsapp',
  'googlechat',
  'line',
  'wechat',
  'tencentqq',
] as const

/**
 * Base channel types documented as having NO vendored mark — <BrandIcon>
 * renders a lettermark chip for these. Keep in sync if a channel gains a
 * vendored SVG (move it to VENDORED_CHANNEL_SLUGS above and drop it here).
 */
export const LETTERMARK_ONLY_CHANNEL_TYPES = [
  'slack',
  'feishu',
  'dingtalk',
  'wecom',
  'irc',
] as const

/**
 * Derives the <BrandIcon> logoSlug for a channel base type. Remaps the three
 * base types whose backend id differs from the vendored SVG slug; every other
 * base type passes through unchanged (resolving to a vendored mark if one
 * exists, or to <BrandIcon>'s lettermark fallback otherwise — never throws).
 */
export function channelSlug(baseType: string): string {
  switch (baseType) {
    case 'google-chat':
      return 'googlechat'
    case 'weixin':
      return 'wechat'
    case 'qq':
      return 'tencentqq'
    default:
      return baseType
  }
}
