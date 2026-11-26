// The one request the permissions fixture does not know: PUT /api/v1/skills/<id>/category
// (spec C-21, row "skill category"). Fix round 4, Part G, for the tests of
// SkillRowActions.tsx#overlay:1.
//
// This file is a helper, not a test file: it holds no test and no expectation about the
// dialog. Only the fetch edge is doubled, and it plays the server's part as the spec
// states it: a category is stored for the skill and the list that follows shows it; a
// refusal carries the server's sentence in the body as {"error": "<text>"}.
//
// Call categoryEdge() inside a test, after the permissions fixture has put its own fetch in
// place (its hook runs before the test body), so every other request still reaches it. The
// fixture's own afterEach removes this stub with the others.
import { vi } from 'vitest'
import type { Skill } from '@/lib/api'
import { world } from './skills-tools-harness'

export interface CategoryWrite {
  path: string
  body: unknown
}

export function categoryEdge() {
  const writes: CategoryWrite[] = []
  let refusal: { status: number; error: string; serverNow?: Partial<Skill> } | undefined
  const underneath = globalThis.fetch
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(input instanceof Request ? input.url : String(input), 'http://localhost').pathname
    const named = /^\/api\/v1\/skills\/([^/]+)\/category$/.exec(path)
    const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
    if (!named || method !== 'PUT') return underneath(input, init)

    writes.push({ path, body: JSON.parse(String(init?.body)) })
    const id = decodeURIComponent(named[1])
    if (refusal) {
      const { status, error, serverNow } = refusal
      refusal = undefined
      // The server had already stored something else for this skill: the list that follows shows it.
      if (serverNow) world.skills = world.skills.map((entry) => (entry.id === id ? { ...entry, ...serverNow } : entry))
      return Response.json({ error }, { status })
    }
    const { category } = writes[writes.length - 1].body as { category: Skill['category'] }
    world.skills = world.skills.map((entry) => (entry.id === id ? { ...entry, category } : entry))
    return Response.json(world.skills.find((entry) => entry.id === id))
  })
  return {
    writes,
    // The next category write is refused with this status and sentence.
    refuseNext(status: number, error: string, serverNow?: Partial<Skill>) {
      refusal = { status, error, serverNow }
    },
  }
}
