import { describe, it, expect } from 'vitest'
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkRehype from 'remark-rehype'
import { toHtml } from 'hast-util-to-html'
import { rehypePhosphorEmoji, EMOJI_MAP } from './rehype-phosphor-emoji'
import { PHOSPHOR_EMOJI_ICONS } from './phosphor-emoji-icons'
import type { Root } from 'hast'

// Helper: run markdown through remark→rehype→rehypePhosphorEmoji, return HTML string.
function processMarkdown(md: string): string {
  const processor = unified().use(remarkParse).use(remarkRehype).use(rehypePhosphorEmoji)
  const tree = processor.runSync(processor.parse(md)) as Root
  return toHtml(tree)
}

// Return raw emoji codepoints that appear in HTML outside of Phosphor span elements.
function rawEmojiInHtml(html: string): string[] {
  // Strip all <span data-phosphor-icon="..."></span>
  const stripped = html.replace(/<span data-phosphor-icon="[^"]*"><\/span>/g, '')
  // Match emoji ranges used by our EMOJI_MAP entries
  // \u{2764} (heavy black heart) and \u{FE0F} (variation selector-16) are kept as
  // separate alternation branches — not adjacent inside one character class — so this
  // isn't misread as matching the combined ❤️ grapheme as a unit: a class only ever
  // matches ONE code point per position, so the two must stay independently matchable
  // (a bare ❤ or a stray FE0F) exactly as before, just without the misleading adjacency
  // that no-misleading-character-class flags.
  const emojiRe =
    /[\u{1F000}-\u{1FFFF}\u{2600}-\u{27BF}\u{2300}-\u{23FF}\u{231A}\u{231B}\u{23E9}-\u{23FA}\u{2764}]|\u{FE0F}/gu
  return [...stripped.matchAll(emojiRe)].map((m) => m[0])
}

describe('rehype-phosphor-emoji — newly mapped emoji render as icon spans', () => {
  it('converts face emoji (😀 😉 😢 😐 😡 😵) to Phosphor spans', () => {
    const html = processMarkdown('Hello 😀 wink 😉 sad 😢 meh 😐 angry 😡 dizzy 😵')
    expect(html).toContain('data-phosphor-icon="Smiley"')
    expect(html).toContain('data-phosphor-icon="SmileyWink"')
    expect(html).toContain('data-phosphor-icon="SmileySad"')
    expect(html).toContain('data-phosphor-icon="SmileyMeh"')
    expect(html).toContain('data-phosphor-icon="SmileyAngry"')
    expect(html).toContain('data-phosphor-icon="SmileyXEyes"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 👍 → ThumbsUp and 👎 → ThumbsDown', () => {
    const html = processMarkdown('Good 👍 bad 👎')
    expect(html).toContain('data-phosphor-icon="ThumbsUp"')
    expect(html).toContain('data-phosphor-icon="ThumbsDown"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts ❤️ 💕 💗 → Heart spans', () => {
    const html = processMarkdown('Love ❤️ hearts 💕 more 💗')
    expect(html).toContain('data-phosphor-icon="Heart"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts ⏰ 🕐 🕑 → Clock spans', () => {
    const html = processMarkdown('Alarm ⏰ one 🕐 two 🕑')
    expect(html).toContain('data-phosphor-icon="Clock"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 📝 → NotePencil, 📓 → Note, ✏️ → Pencil', () => {
    const html = processMarkdown('Task 📝 notebook 📓 edit ✏️')
    expect(html).toContain('data-phosphor-icon="NotePencil"')
    expect(html).toContain('data-phosphor-icon="Note"')
    expect(html).toContain('data-phosphor-icon="Pencil"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 🎯 → Target, 🏆 → Trophy', () => {
    const html = processMarkdown('Goal 🎯 win 🏆')
    expect(html).toContain('data-phosphor-icon="Target"')
    expect(html).toContain('data-phosphor-icon="Trophy"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts ✨ → Sparkle, 🔥 → Fire, 💡 → Lightbulb', () => {
    const html = processMarkdown('Magic ✨ hot 🔥 idea 💡')
    expect(html).toContain('data-phosphor-icon="Sparkle"')
    expect(html).toContain('data-phosphor-icon="Fire"')
    expect(html).toContain('data-phosphor-icon="Lightbulb"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 🔍 and 🔎 → MagnifyingGlass spans', () => {
    const html = processMarkdown('Find 🔍 zoom 🔎')
    expect(html).toContain('data-phosphor-icon="MagnifyingGlass"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 👤 → Person, 👥 → Users, 🤖 → Robot', () => {
    const html = processMarkdown('User 👤 team 👥 bot 🤖')
    expect(html).toContain('data-phosphor-icon="Person"')
    expect(html).toContain('data-phosphor-icon="Users"')
    expect(html).toContain('data-phosphor-icon="Robot"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 🔗 → Link, 📤 → Upload, 📥 → Download', () => {
    const html = processMarkdown('Ref 🔗 push 📤 pull 📥')
    expect(html).toContain('data-phosphor-icon="Link"')
    expect(html).toContain('data-phosphor-icon="Upload"')
    expect(html).toContain('data-phosphor-icon="Download"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 👁️ and 👁 → Eye spans', () => {
    const html = processMarkdown('Eye 👁️ watch 👁')
    expect(html).toContain('data-phosphor-icon="Eye"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 🔔 → Bell, 🏷️ → Tag, ⚡ → Lightning, 🛡️ → Shield, 👑 → Crown', () => {
    const html = processMarkdown('Notify 🔔 label 🏷️ bolt ⚡ guard 🛡️ king 👑')
    expect(html).toContain('data-phosphor-icon="Bell"')
    expect(html).toContain('data-phosphor-icon="Tag"')
    expect(html).toContain('data-phosphor-icon="Lightning"')
    expect(html).toContain('data-phosphor-icon="Shield"')
    expect(html).toContain('data-phosphor-icon="Crown"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })

  it('converts 📷 / 📸 → Camera, 🗑️ → Trash, ❓ → Question', () => {
    const html = processMarkdown('Photo 📷 snap 📸 delete 🗑️ help ❓')
    expect(html).toContain('data-phosphor-icon="Camera"')
    expect(html).toContain('data-phosphor-icon="Trash"')
    expect(html).toContain('data-phosphor-icon="Question"')
    expect(rawEmojiInHtml(html)).toEqual([])
  })
})

describe('rehype-phosphor-emoji — code blocks still skip emoji', () => {
  it('does NOT translate emoji inside a fenced code block', () => {
    const html = processMarkdown('```\nprint("hello 😀")\n```')
    expect(html).not.toContain('data-phosphor-icon=')
  })

  it('does NOT translate emoji inside inline code', () => {
    const html = processMarkdown('Use `👍` to approve')
    // The 👍 inside inline code must remain raw
    expect(html).toMatch(/<code>.*👍.*<\/code>/s)
    expect(html).not.toMatch(/<code>.*data-phosphor-icon.*<\/code>/s)
  })
})

describe('rehype-phosphor-emoji — meta: allow-list completeness', () => {
  it('every EMOJI_MAP value exists as a key in PHOSPHOR_EMOJI_ICONS', () => {
    const allowListKeys = new Set(Object.keys(PHOSPHOR_EMOJI_ICONS))
    for (const [emoji, iconName] of Object.entries(EMOJI_MAP)) {
      expect(
        allowListKeys.has(iconName),
        `emoji ${emoji} maps to "${iconName}" which is missing from PHOSPHOR_EMOJI_ICONS`,
      ).toBe(true)
    }
  })

  it('translates EVERY emoji in EMOJI_MAP with no raw leak (full-map sweep)', () => {
    for (const [emoji, icon] of Object.entries(EMOJI_MAP)) {
      const html = processMarkdown(`pre ${emoji} post`)
      expect(html, `${emoji} should render data-phosphor-icon="${icon}"`).toContain(
        `data-phosphor-icon="${icon}"`,
      )
      // After removing the icon spans, the raw emoji char must be gone.
      const stripped = html.replace(/<span data-phosphor-icon="[^"]*"><\/span>/g, '')
      expect(stripped.includes(emoji), `${emoji} leaked raw into output`).toBe(false)
    }
  })
})
