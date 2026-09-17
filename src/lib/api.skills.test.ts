import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

function stubCookie(value: string) {
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => value,
  })
}

function restoreCookie() {
  // Remove our override so subsequent tests start clean.
  // jsdom reinstates its own descriptor when we delete the override.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (document as any).cookie
}

function makeOkResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('Skill registry helpers (ClawHub search + install-by-slug)', () => {
  let fetchSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    stubCookie('__Host-csrf=test-csrf-token')
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    sessionStorage.clear()
    restoreCookie()
    vi.resetModules()
  })

  describe('fetchSkills (tolerant installed-skills list)', () => {
    it('keeps valid skills and drops a malformed one (one bad skill must not hide the whole list)', async () => {
      const payload = [
        { revision: '0'.repeat(64), id: 'good', name: 'Good', version: '2.1.0', verified: false, status: 'active', source: 'global' },
        // Structurally invalid: missing required version/verified/status.
        { id: 'bad', name: 'Bad' },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchSkills } = await import('./api')
      const result = await fetchSkills()

      // Must NOT throw; the valid skill survives, the bad one is dropped.
      expect(result.map((s) => s.id)).toEqual(['good'])
    })

    it('accepts a non-semver version like "1.0" (ClawHub versions are arbitrary)', async () => {
      const payload = [
        { revision: '0'.repeat(64), id: 'cw', name: 'ClawHub Skill', version: '1.0', verified: false, status: 'active', source: 'global' },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchSkills } = await import('./api')
      const result = await fetchSkills()

      expect(result).toHaveLength(1)
      expect(result[0].version).toBe('1.0')
    })

    // ADR-072 D3.1: `last_invoked` is not yet declared in
    // contracts/components/schemas/Skill.yaml (additionalProperties:
    // false), so SkillSchema.safeParse alone would silently strip it even
    // once the backend starts sending it. fetchSkills merges it back from
    // the RAW response body — this proves that merge actually happens
    // against a real (mocked) HTTP response, not just against a hand-built
    // object in a component test.
    it('merges last_invoked from the raw response even though the Skill schema does not declare it', async () => {
      const payload = [
        {
          revision: '0'.repeat(64),
          id: 'release-notes',
          name: 'Release Notes',
          version: '1.0.0',
          verified: true,
          status: 'active',
          source: 'global',
          last_invoked: '2026-08-15T10:00:00Z',
        },
        {
          revision: '0'.repeat(64),
          id: 'never-called',
          name: 'Never Called',
          version: '1.0.0',
          verified: true,
          status: 'active',
          source: 'global',
          // no last_invoked at all — the granted-but-never-invoked case.
        },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { fetchSkills, skillLastInvoked } = await import('./api')
      const result = await fetchSkills()

      expect(result).toHaveLength(2)
      expect(skillLastInvoked(result[0])).toBe('2026-08-15T10:00:00Z')
      expect(skillLastInvoked(result[1])).toBeNull()
    })
  })

  describe('searchSkills', () => {
    it('GET /api/v1/skills/search — URL-encodes q and sends limit', async () => {
      const payload = [
        {
          slug: 'web-search',
          display_name: 'Web Search',
          summary: 'Search the web.',
          version: '1.4.0',
          score: 0.9,
          registry_name: 'clawhub',
          owner_handle: 'acme',
        },
      ]
      fetchSpy.mockResolvedValueOnce(makeOkResponse(payload))

      const { searchSkills } = await import('./api')
      const result = await searchSkills('web search', 5)

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/skills/search')
      expect(url).toContain('q=web+search')
      expect(url).toContain('limit=5')
      expect((init.method ?? 'GET').toUpperCase()).toBe('GET')
      expect(result).toEqual(payload)
    })

    it('defaults limit to 20 when omitted', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse([]))
      const { searchSkills } = await import('./api')
      await searchSkills('files')
      const [url] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('limit=20')
    })

    it('propagates a 502 as a typed ApiError', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('registry down', { status: 502 }))
      const { searchSkills } = await import('./api')
      await expect(searchSkills('web')).rejects.toThrow('502')
    })
  })

  describe('installSkillBySlug', () => {
    const okSkill = {
      revision: 'b'.repeat(64),
      persistence_status: 'complete',
      activation_status: 'active',
      changed_fields: ['skill'],
      id: 'web-search',
      name: 'web-search',
      version: '1.4.0',
      status: 'active',
      verified: false,
    }

    it('POST /api/v1/skills/install — sends {slug} body + CSRF', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse(okSkill))
      const { installSkillBySlug } = await import('./api')
      const skill = await installSkillBySlug('web-search')

      const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(url).toContain('/api/v1/skills/install')
      expect((init.method ?? '').toUpperCase()).toBe('POST')
      const headers = new Headers(init.headers as HeadersInit)
      expect(headers.get('X-CSRF-Token')).toBe('test-csrf-token')
      expect(JSON.parse(init.body as string)).toEqual({ slug: 'web-search' })
      expect(skill.id).toBe('web-search')
    })

    it('includes version when provided', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse(okSkill))
      const { installSkillBySlug } = await import('./api')
      await installSkillBySlug('web-search', '1.4.0')
      const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({ slug: 'web-search', version: '1.4.0' })
    })

    it('includes the reviewed revision when replacing an installed skill', async () => {
      fetchSpy.mockResolvedValueOnce(makeOkResponse(okSkill))
      const { installSkillBySlug } = await import('./api')
      await installSkillBySlug('web-search', '1.4.0', 'a'.repeat(64))
      const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
      expect(JSON.parse(init.body as string)).toEqual({
        slug: 'web-search', version: '1.4.0', revision: 'a'.repeat(64),
      })
    })

    it('propagates a 409 (already installed) as a typed ApiError', async () => {
      fetchSpy.mockResolvedValueOnce(new Response('already installed', { status: 409 }))
      const { installSkillBySlug } = await import('./api')
      await expect(installSkillBySlug('web-search')).rejects.toThrow('409')
    })
  })
})

