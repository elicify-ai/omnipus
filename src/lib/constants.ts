// Shared constants and utilities used across multiple components

/** Generate an unguessable ID from a CSPRNG: crypto.randomUUID() where
 *  available, else crypto.getRandomValues() (128-bit — it is not gated to
 *  secure contexts, so it covers plain HTTP). These ids serve as unguessable
 *  storage/URL keys for uploads, so there is deliberately no predictable
 *  fallback: with no secure source, generateId() throws. */
export function generateId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = crypto.getRandomValues(new Uint8Array(16))
    return `${Date.now().toString(36)}-${Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')}`
  }
  throw new Error('generateId: no secure random source available (crypto.randomUUID / crypto.getRandomValues)')
}

/** Hint text for API key input fields, keyed by canonical CatalogProvider id
 *  (the `id` of GET /providers/catalog entries, ADR-067 schema 2.0.0). */
export const PROVIDER_HINTS: Record<string, string> = {
  anthropic: 'Starts with sk-ant-...',
  openai: 'Starts with sk-...',
  groq: 'Starts with gsk_...',
  openrouter: 'Starts with sk-or-v1-...',
}
