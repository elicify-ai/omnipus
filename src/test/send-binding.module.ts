// #1428 step 2 RED. Test helper only: no production logic.
// Loads spec modules that do not exist yet.
// A variable relative import is an assertion failure (BLOCKED) when the file
// is absent, and resolves the real module once GREEN adds it. A string-literal
// import of a missing file fails the suite at transform time, which is the
// wrong reason (spec: fail because the behaviour is missing).
import { expect } from 'vitest'

export async function loadRelative(specifier: string): Promise<Record<string, unknown> | null> {
  try {
    return await import(/* @vite-ignore */ specifier) as Record<string, unknown>
  } catch {
    return null
  }
}

/** Paths are relative to this file (`src/test/send-binding.module.ts`). */
export async function requireModule(specifier: string, specRef: string): Promise<Record<string, unknown>> {
  let caught = ''
  let mod: Record<string, unknown> | null = null
  try {
    mod = await import(/* @vite-ignore */ specifier) as Record<string, unknown>
  } catch (err) {
    caught = err instanceof Error ? err.message : String(err)
  }
  expect(
    mod,
    `BLOCKED: ${specifier} (from src/test/) not implemented — required by ${specRef}. loader: ${caught}`,
  ).not.toBeNull()
  return mod as Record<string, unknown>
}

export async function requireFn(specifier: string, name: string, specRef: string): Promise<(...args: unknown[]) => unknown> {
  const mod = await requireModule(specifier, specRef)
  const fn = mod[name]
  expect(typeof fn, `BLOCKED: ${specifier}::${name} not implemented — required by ${specRef}`).toBe('function')
  return fn as (...args: unknown[]) => unknown
}
