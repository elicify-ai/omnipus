// chatLinks.ts — the SPA half of chat links (chat-links spec, "Machine-
// Verifiable Constraints": link grammar, Bare-link rule, URL transform).
//
// A chat link names one Library location: a workspace id plus a
// workspace-relative path. Two written forms are recognised:
//
//   omnipus://file/<ws>/<seg>[/<seg>...]   — carries its own workspace
//   notes/plan.md                          — "bare": only when the chat's
//                                            workspace is known
//
// The accepted grammar is deliberately a STRICT SUBSET of the server's
// `pkg/library/root.go::CleanRelPath`: everything accepted here is a path the
// Library can address, and a few things CleanRelPath would tolerate (empty and
// `.` segments, invisible or direction-changing characters, over-long
// segments) are refused here so a link never names something other than what
// it shows. Anything that is not an exact match is "no link": the caller
// renders it exactly as today (the struck-through unsafe-link span).
//
// The rendered href is always REBUILT from the parsed fields (SEC-10) — never
// the agent's raw text — by `libraryPageHref`.

import { defaultUrlTransform } from 'react-markdown'

/** A parsed chat link: which workspace, which Library path (decoded, `/`-joined). */
export interface ChatLinkTarget { // not-wire-format: SPA-internal parse result, rebuilt from a markdown href
  workspaceId: string
  path: string
}

const OMNIPUS_FILE_PREFIX = 'omnipus://file/'
const WORKSPACE_ID_RE = /^[A-Za-z0-9_.-]+$/
const MAX_SEGMENT_CHARS = 255
const FILE_EXTENSION_RE = /^[A-Za-z0-9]{1,16}$/

/**
 * Characters that make a name lie about itself or that the Library cannot
 * address: C0 controls, DEL, C1 controls, the bidirectional controls
 * (U+202A-202E, U+2066-2069, U+200E/F) and the zero-width characters
 * (U+200B-200D, U+2060, U+FEFF). Spec: "SPA-only rejections".
 */
// eslint-disable-next-line no-control-regex -- the control ranges ARE the rule
const FORBIDDEN_CHARS_RE = /[\u0000-\u001F\u007F-\u009F\u200B-\u200F\u202A-\u202E\u2060\u2066-\u2069\uFEFF]/

/** Workspace-id rule shared with the Go encoder (`safeID` plus the character class). */
function isValidWorkspaceId(ws: string): boolean {
  if (!WORKSPACE_ID_RE.test(ws)) return false
  if (/^\.+$/.test(ws)) return false
  if (ws.includes('..')) return false
  return [...ws].length <= MAX_SEGMENT_CHARS
}

/** One decoded path segment, or null when it fails the strict check. */
function decodeSegment(raw: string): string | null {
  let seg: string
  try {
    seg = decodeURIComponent(raw)
  } catch {
    return null // malformed percent-encoding
  }
  if (seg === '' || seg === '.' || seg.startsWith('..')) return null
  if (seg.includes('/') || seg.includes('\\')) return null
  if (FORBIDDEN_CHARS_RE.test(seg)) return null
  if ([...seg].length > MAX_SEGMENT_CHARS) return null
  return seg
}

/** The decoded `/`-joined path, or null when any segment fails. Never empty. */
function decodePath(raw: string): string | null {
  if (raw === '' || raw.includes('?') || raw.includes('#')) return null
  const out: string[] = []
  for (const part of raw.split('/')) {
    const seg = decodeSegment(part)
    if (seg === null) return null
    out.push(seg)
  }
  return out.join('/')
}

/** `omnipus://file/<ws>/<path>` only. Case-sensitive; no host but `file`. */
function parseOmnipusFileLink(href: string): ChatLinkTarget | null {
  if (!href.startsWith(OMNIPUS_FILE_PREFIX)) return null
  const rest = href.slice(OMNIPUS_FILE_PREFIX.length)
  const slash = rest.indexOf('/')
  if (slash <= 0) return null // empty workspace, or no path at all
  const workspaceId = rest.slice(0, slash)
  if (!isValidWorkspaceId(workspaceId)) return null
  const path = decodePath(rest.slice(slash + 1))
  return path === null ? null : { workspaceId, path }
}

/** True when the final segment looks like a file: a non-leading `.` then 1-16 alphanumerics. */
function looksLikeFile(path: string): boolean {
  const last = path.slice(path.lastIndexOf('/') + 1)
  const dot = last.lastIndexOf('.')
  return dot > 0 && FILE_EXTENSION_RE.test(last.slice(dot + 1))
}

/** A scheme-less href under the Bare-link rule, resolved against `chatWorkspaceId`. */
function parseBareLink(href: string, chatWorkspaceId: string): ChatLinkTarget | null {
  if (href === '') return null
  for (const banned of ['/', './', '../', '#', '?']) {
    if (href.startsWith(banned)) return null // `//` is covered by `/`
  }
  const firstSegment = href.split('/', 1)[0]
  if (firstSegment.includes(':')) return null
  const path = decodePath(href)
  if (path === null || !looksLikeFile(path)) return null
  return { workspaceId: chatWorkspaceId, path }
}

/**
 * Parse a markdown href as a chat link. `chatWorkspaceId` is the workspace the
 * chat session recorded (null = none known, which makes every bare link plain
 * text). Returns null for anything outside the strict grammar.
 */
export function parseChatLink(
  href: string | null | undefined,
  chatWorkspaceId: string | null,
): ChatLinkTarget | null {
  if (typeof href !== 'string' || href === '') return null
  if (href.startsWith(OMNIPUS_FILE_PREFIX)) return parseOmnipusFileLink(href)
  if (chatWorkspaceId === null || chatWorkspaceId === '') return null
  // Any other scheme (https:, mailto:, omnipus: in another shape, …) is not a
  // bare link; the first-segment `:` check in parseBareLink refuses it.
  return parseBareLink(href, chatWorkspaceId)
}

/** True for a well-formed `omnipus://file/...` link (the form the URL transform must keep). */
export function isChatLink(href: string): boolean {
  return parseOmnipusFileLink(href) !== null
}

/**
 * react-markdown `urlTransform` for both chat renderers (FR-002): a valid
 * `omnipus://file/` link on an `href` survives; everything else — including
 * any `omnipus://` image `src` — goes through react-markdown's default
 * transform, which still blanks `javascript:` and every other unsafe scheme.
 */
export function chatLinkUrlTransform(url: string, key: string): string {
  if (key === 'href' && isChatLink(url)) return url
  return defaultUrlTransform(url)
}

/**
 * The standalone full-page Library address for a target — what a modified
 * click (Ctrl/Cmd/middle/Shift) opens in a new tab. Hash history: `main.tsx`
 * mounts the router on `createHashHistory()`, so the route lives after `#`
 * (same convention as `KnowledgeBacklinks.tsx::libraryNoteHref`). Built from
 * the parsed fields only (SEC-10).
 */
export function libraryPageHref(target: ChatLinkTarget): string {
  const params = new URLSearchParams({ workspace: target.workspaceId, path: target.path })
  return `/#/library?${params.toString()}`
}

/** "W / notes/plan.md" — the visible target hint shown when link text differs from the target. */
export function chatLinkTargetLabel(target: ChatLinkTarget): string {
  return `${target.workspaceId} / ${target.path}`
}

/** True when the visible link text already names the target (its full path or its base name). */
export function linkTextNamesTarget(text: string, target: ChatLinkTarget): boolean {
  const shown = text.trim()
  const base = target.path.slice(target.path.lastIndexOf('/') + 1)
  return shown === target.path || shown === base
}
