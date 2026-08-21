// mountNameFromPath derives the name a mounted folder takes inside work/ from
// the real folder's own name.
//
// It exists so the operator is not asked to invent a second name for a folder
// they just pointed at. The SERVER owns uniqueness and validity and rejects
// anything it will not accept — deriving a suggestion here does not move that
// rule to the client, which is why this only strips characters that could not
// be a single path segment rather than trying to reproduce the server's
// validation.
export function mountNameFromPath(hostPath: string): string {
  const segments = hostPath.split('/').filter(Boolean)
  const base = segments[segments.length - 1] ?? ''
  return base.replace(/[^A-Za-z0-9._-]/g, '-').replace(/^[.]+/, '').slice(0, 64)
}
