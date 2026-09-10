// VideoEmbed — the click-to-play facade for an allow-listed external video
// embedded in a knowledge-base note (ADR-083 D-C/D9, US-9, EMB-075..EMB-082).
//
// WHY THE FACADE IS MANDATORY, NOT COSMETIC. D-C forbids external images —
// `img-src` stays same-origin, so there is no YouTube thumbnail to show.
// That is not a missing nicety; it is what forces this component to exist at
// all. If nothing stood between the markdown and a real `<iframe>`, every
// note that mentions a video would contact the provider the instant it
// mounted — one request per embed, before anyone chose to watch anything.
// This component draws the play control from LOCAL assets only (a Phosphor
// icon, no remote image, no remote font, no preconnect, no prefetch) and
// creates the `<iframe>` for the first time on click, never before.
//
// WHY THE ALLOW-LIST CHECK LIVES HERE, REACTIVELY, RATHER THAN AT PARSE TIME.
// The allow-list is operator configuration (`video_embed_hosts` on
// `GET /state`), and an operator can empty it while the app is already open
// (A-14 in the spec's open-questions register). A parse-time decision baked
// into the rendered markdown would go stale the moment the config changes —
// exactly the "control that does nothing" failure this feature exists to
// prevent. So this component reads the SAME `['app-state']` query AppShell
// already keeps warm (same key, same `staleTime`), which means: (a) no new
// network request on a normal mount — TanStack Query serves the cached
// value — and (b) the decision re-evaluates on the query's own refresh
// schedule, not once at boot.
//
// THE THREE "NOT PLAYABLE" STATES ARE DELIBERATELY DIFFERENT MESSAGES.
// An empty allow-list ("no video hosts are permitted") is a legitimate,
// intentional configuration — not an error, and not the same sentence as
// "this specific host is not allowed" (a non-empty list that just doesn't
// name this host). Collapsing the two into one generic "can't embed this"
// string is exactly the undetectable-fallback shape the JPEG-screencast
// removal (see CLAUDE.md) also forbids: a reader who sees one message has no
// way to tell "my deployment disabled video" from "this particular link is
// wrong". A third state — the host IS allowed but the URL carries no
// recognisable video identifier — degrades to a plain link rather than a
// play control that would 404 inside its own frame.
//
// THE FRAME SRC IS CONSTRUCTED, NEVER PASSED THROUGH (D9). Only a
// validated 11-character identifier and a validated positive integer start
// offset ever reach `buildEmbedSrc`; the rest of the author's URL —
// tracking query parameters, a `/watch` path, anything — is discarded. A
// look-alike host (`www.youtube-nocookie.com.evil.example`) is refused by
// exact hostname equality, never a prefix/suffix match.

import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { PlayCircle, Prohibit, SpinnerGap, VideoCameraSlash, Warning } from '@phosphor-icons/react'
import { fetchAppState } from '@/lib/api'

/** YouTube (and youtube-nocookie.com) video identifiers are always exactly
 *  11 characters of this alphabet. This is the WHOLE filter — there is no
 *  separate sanitisation step, so an id that fails this pattern is treated
 *  as absent, never rendered into a URL. */
export const VIDEO_ID_PATTERN = /^[A-Za-z0-9_-]{11}$/

export interface ParsedVideoEmbed {
  /** Exact lowercased hostname parsed from the destination, or `null` when
   *  the destination could not be parsed as a URL at all. Compared against
   *  the allow-list by EXACT equality only — never `.includes`/`.endsWith`,
   *  which is how a look-alike domain would sneak past. */
  host: string | null
  /** The 11-character video id, or `null` when none could be extracted, or
   *  extracted but it failed `VIDEO_ID_PATTERN`. */
  id: string | null
  /** Positive integer seconds to start playback at, if present in the
   *  source URL. A negative, zero, or non-integer value is DROPPED (not
   *  clamped) — the embed still plays, just without a start offset. */
  start?: number
}

/**
 * Reads (never trusts) the author's destination URL for a host, a video id,
 * and an optional start offset. Accepts either an already-`/embed/<id>` URL
 * or a classic `/watch?v=<id>` shape — the accepted INPUT shape is
 * deliberately permissive because the OUTPUT (`buildEmbedSrc`) is rebuilt
 * from scratch regardless, so a permissive parser here creates no passthrough
 * risk.
 */
export function parseVideoEmbedUrl(destination: string): ParsedVideoEmbed {
  let parsed: URL
  try {
    parsed = new URL(destination)
  } catch {
    return { host: null, id: null }
  }

  const host = parsed.hostname.toLowerCase()

  const embedPathMatch = /\/embed\/([^/?#]+)/.exec(parsed.pathname)
  const rawId = embedPathMatch?.[1] ?? parsed.searchParams.get('v') ?? ''
  const id = VIDEO_ID_PATTERN.test(rawId) ? rawId : null

  const rawStart = parsed.searchParams.get('start') ?? parsed.searchParams.get('t')
  let start: number | undefined
  if (rawStart !== null) {
    const digitsOnly = /^(\d+)s?$/.exec(rawStart)
    if (digitsOnly) {
      const n = Number.parseInt(digitsOnly[1] as string, 10)
      if (Number.isFinite(n) && n > 0) start = n
    }
  }

  return { host, id, start }
}

/** Builds the frame's `src` from validated parts only — the author's own
 *  query string never reaches this URL (D9: "constructed, never passed
 *  through"). */
export function buildEmbedSrc(host: string, id: string, start?: number): string {
  const src = new URL(`https://${host}/embed/${id}`)
  if (start !== undefined) src.searchParams.set('start', String(start))
  return src.toString()
}

export interface VideoEmbedProps {
  /** The embed's destination exactly as the author wrote it (a markdown
   *  link's URL). Never forwarded to the frame verbatim — see
   *  `parseVideoEmbedUrl`/`buildEmbedSrc` above. */
  url: string
  /** The link text / alt text the author wrote, if any. The ONLY source for
   *  a caption — a video's title is never fetched from the provider (A-10:
   *  fetching one is itself a contact with the provider, exactly what
   *  click-to-play exists to prevent). */
  title?: string
}

const PANEL_BASE =
  'flex w-full flex-col items-center justify-center gap-2 rounded-md border p-6 text-center text-xs'

export function VideoEmbed({ url, title }: VideoEmbedProps) {
  const appStateQuery = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
    staleTime: 60_000,
  })
  const [playing, setPlaying] = useState(false)
  const parsed = useMemo(() => parseVideoEmbedUrl(url), [url])

  // Loading: the very first app-wide fetch of `['app-state']` hasn't
  // resolved yet. On a normal mount (AppShell renders before any note can)
  // this branch is not reachable — it exists for correctness, not the happy
  // path, and it is deliberately its own message: NOT the same text as
  // "no hosts configured", which is a resolved answer, not a pending one.
  if (appStateQuery.isPending) {
    return (
      <div
        data-testid="video-embed"
        data-state="loading"
        className={`${PANEL_BASE} border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-muted)]`}
      >
        <SpinnerGap size={16} className="animate-spin" />
        <span>Checking allowed video hosts…</span>
      </div>
    )
  }

  if (appStateQuery.isError) {
    return (
      <div
        data-testid="video-embed"
        data-state="error"
        className={`${PANEL_BASE} border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 text-[var(--color-warning)]`}
      >
        <Warning size={16} />
        <span>Could not check which video hosts are allowed.</span>
        <button
          tabIndex={0}
          type="button"
          onClick={() => void appStateQuery.refetch()}
          className="text-[11px] underline underline-offset-2"
        >
          Retry
        </button>
      </div>
    )
  }

  const hosts = appStateQuery.data?.video_embed_hosts ?? []
  const hostAllowed = parsed.host !== null && hosts.some((h) => h.toLowerCase() === parsed.host)

  if (!hostAllowed) {
    // An empty list is a legitimate, intentional configuration (REQUIREMENT
    // 4) — say so plainly, and say something DIFFERENT from the "this host
    // specifically is not allowed" refusal below, so a reader (and a test)
    // can tell the two apart.
    if (hosts.length === 0) {
      return (
        <div
          data-testid="video-embed"
          data-state="disabled"
          className={`${PANEL_BASE} border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-muted)]`}
        >
          <VideoCameraSlash size={20} />
          <span>No video hosts are permitted by this deployment.</span>
        </div>
      )
    }
    return (
      <div
        data-testid="video-embed"
        data-state="refused"
        className={`${PANEL_BASE} border-[var(--color-error)]/40 bg-[var(--color-error)]/5 text-[var(--color-error)]`}
      >
        <Prohibit size={20} />
        <span>
          {parsed.host
            ? `Video from "${parsed.host}" is not allowed here.`
            : 'This video link is not allowed here.'}
        </span>
      </div>
    )
  }

  // Host is allowed, but no valid 11-character identifier could be read
  // from the URL (wrong length, injected characters, a page that isn't a
  // video at all). Never draw a play control for a frame that would 404 —
  // fall back to an honest, still-functional link instead.
  if (parsed.id === null) {
    return (
      <div
        data-testid="video-embed"
        data-state="invalid"
        className={`${PANEL_BASE} border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-muted)]`}
      >
        <span>This video link could not be recognised.</span>
        <a
          tabIndex={0}
          href={url}
          target="_blank"
          rel="noreferrer"
          className="text-[var(--color-accent)] underline underline-offset-2"
        >
          Open the link instead
        </a>
      </div>
    )
  }

  if (!playing) {
    return (
      <div
        data-testid="video-embed"
        data-state="placeholder"
        className="relative flex aspect-video w-full items-center justify-center overflow-hidden rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)]"
      >
        <button
          tabIndex={0}
          type="button"
          data-testid="video-embed-play"
          aria-label={title ? `Play video: ${title}` : 'Play video'}
          onClick={() => setPlaying(true)}
          className="flex items-center justify-center text-[var(--color-accent)] transition-colors hover:text-[var(--color-accent-hover)]"
        >
          <PlayCircle size={56} weight="fill" />
        </button>
        {title !== undefined && title !== '' && (
          <span className="absolute bottom-2 left-1/2 max-w-[90%] -translate-x-1/2 truncate text-[11px] text-[var(--color-muted)]">
            {title}
          </span>
        )}
      </div>
    )
  }

  const src = buildEmbedSrc(parsed.host as string, parsed.id, parsed.start)
  return (
    <div
      data-testid="video-embed"
      data-state="playing"
      className="aspect-video w-full overflow-hidden rounded-md border border-[var(--color-border)]"
    >
      <iframe
        data-testid="video-embed-frame"
        src={src}
        title={title && title !== '' ? title : 'Embedded video player'}
        sandbox="allow-scripts allow-same-origin allow-presentation"
        referrerPolicy="no-referrer"
        allow="encrypted-media; picture-in-picture; fullscreen"
        className="h-full w-full border-0"
      />
    </div>
  )
}
