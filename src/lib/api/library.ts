// library.ts: Library, knowledge base and vault records, with versioned writes

import { ApiError, isApiError as isApiErrorFn } from '../api-error'
import { maybeDevToast } from '../dev-toast'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  // library-spec.md — Library file explorer over workspace work/ trees
  // (contract-first #8), supersedes the media library above. Only response
  // schemas are imported (request bodies are validated server-side, matching
  // the existing convention — see the comment above ExecutorCommandPreviewResponse).
  LibraryWorkspaceNode as LibraryWorkspaceNodeSchema,
  LibraryEntry as LibraryEntrySchema,
  HostFolderListing as HostFolderListingSchema,
  WorkspaceMountCreateResponse as WorkspaceMountCreateResponseSchema,
  LibraryContentResponse as LibraryContentResponseSchema,
  LibraryUploadResponse as LibraryUploadResponseSchema,
  // ADR-067 stage 2 — the knowledge-base READ surface (contract-first #8).
  // Every one of the three surviving endpoints validates through its
  // generated schema; none of them hand-writes a wire type. (The relevance
  // search endpoint retired US-5/ADR-081 — the human vault-search endpoint
  // below is the ONE surviving human search.)
  KnowledgeBaseInfo as KnowledgeBaseInfoSchema,
  KnowledgeOutline as KnowledgeOutlineSchema,
  // B+C — human vault search, create-vault, PDF binary save (contract-first #8):
  VaultSearchRequest as VaultSearchRequestSchema,
  VaultSearchResponse as VaultSearchResponseSchema,
  CreateVaultRequest as CreateVaultRequestSchema,
  // unified-search-and-grep-spec.md workstream B — index-free file search
  // over a plain folder/mount's confined Library root (contract-first #8):
  FileSearchRequest as FileSearchRequestSchema,
  FileSearchResponse as FileSearchResponseSchema,
  LibraryBinaryContentRequest as LibraryBinaryContentRequestSchema,
  // ADR-083 EMB-007/EMB-007b Step 0 — the typed 409 body for a refused
  // Library whole-file save (contract-first #8):
  LibraryConflictError as LibraryConflictErrorSchema,
  KnowledgeGraphResponse as KnowledgeGraphResponseSchema,
  // view-kinds-design-2026-09-03 §7 — the evaluated saved-view result the
  // Library's .base surface draws, and the list of views one .base owns
  // (contract-first #8).
  ViewResult as ViewResultSchema,
  KnowledgeBaseViews as KnowledgeBaseViewsSchema,
  // UAT D-13 — the collection-addressed saved-views list (a `.base`-less
  // collection still owns its authored views).
  KnowledgeCollectionViews as KnowledgeCollectionViewsSchema,
  // HP-1 fix (defect-list-html-preview-2026-09-08.md) — the mint client for
  // the sandboxed HTML/SVG preview frame (ADR-067 §10.3, spec FR-003f):
  LibraryPreviewTokenResponse as LibraryPreviewTokenResponseSchema,
  // ADR-083 CW-4/CW-7 (EMB-085/EMB-086/EMB-087/EMB-094) — step 5's inline
  // record-field editor (contract-first #8):
  VaultRecord as VaultRecordSchema,
  RecordWriteRequest as RecordWriteRequestSchema,
  RelationWriteRequest as RelationWriteRequestSchema,
  RelationWriteResponse as RelationWriteResponseSchema,
  KnowledgeConflictError as KnowledgeConflictErrorSchema,
} from '@/lib/api/generated/schemas'
import type {
  // library-spec.md — Library file explorer over workspace work/ trees (contract-first #8):
  LibraryWorkspaceNode,
  LibraryEntry,
  HostFolderListing,
  WorkspaceMountCreateRequest,
  WorkspaceMountCreateResponse,
  LibraryContentResponse,
  LibraryContentRequest,
  LibraryMkdirRequest,
  LibraryRenameRequest,
  LibraryUploadResponse,
  LibraryTransferRequest,
  // HP-1 fix (defect-list-html-preview-2026-09-08.md) — mint client for the
  // sandboxed HTML/SVG preview frame (ADR-067 §10.3, spec FR-003f):
  LibraryPreviewTokenRequest,
  LibraryPreviewTokenResponse,
  // ADR-067 stage 2 — the knowledge-base read surface (contract-first #8):
  KnowledgeBaseInfo,
  KnowledgeOutline,
  // B+C wire types:
  VaultSearchRequest,
  VaultSearchResponse,
  CreateVaultRequest,
  LibraryBinaryContentRequest,
  // ADR-083 EMB-007/EMB-007b Step 0 — typed 409 body for a refused Library
  // whole-file save:
  LibraryConflictError,
  KnowledgeGraphResponse,
  // unified-search-and-grep-spec.md workstream B (contract-first #8):
  FileSearchRequest,
  FileSearchResponse,
  // view-kinds-design-2026-09-03 §7 — evaluated saved-view results:
  ViewResult,
  KnowledgeBaseViews,
  // UAT D-13 — the collection-addressed saved-views list:
  KnowledgeCollectionViews,
  // ADR-083 CW-4/CW-7 (EMB-085/EMB-086/EMB-087/EMB-094) — the typed record
  // read/write layer, wired to the gateway/SPA boundary for the inline
  // record-field editor (step 5):
  VaultRecord,
  RecordWriteRequest,
  RelationWriteRequest,
  RelationWriteResponse,
  KnowledgeConflictError,
} from '@/lib/api/generated/openapi-types'
import { ApiSchemaError, BASE_URL, CSRF_HEADER_NAME, _recordApiSchemaError, buildHeaders, readCSRFCookie, request, withCsrfRetry } from './http'

// ── Library (library-spec.md) ────────────────────────────────────────────────
//
// A file explorer over workspace work/ trees — supersedes the Media Library
// above (D-2: entries are workspace-relative PATHS, not UUIDs; a workspace's
// Library root IS its work/ directory). Two entry points share one component
// (D-3): the sidebar's virtual root (every workspace, GET /library/workspaces)
// and a workspace-scoped view (GET /library/{workspace_id}/entries). Wire
// types are the generated Library* schemas (contract-first #8) — never
// hand-written. See contracts/components/schemas/Library*.yaml.

export const libraryQueryKeys = {
  workspaces: () => ['library', 'workspaces'] as const,
  entries: (workspaceId: string, path: string, includeHidden: boolean) =>
    ['library', workspaceId, 'entries', path, includeHidden] as const,
  content: (workspaceId: string, path: string) => ['library', workspaceId, 'content', path] as const,
}

/** Every workspace as a Library virtual-root node (D-3 sidebar entry point). */
export function fetchLibraryWorkspaces(): Promise<LibraryWorkspaceNode[]> {
  return request<LibraryWorkspaceNode[]>(
    '/library/workspaces',
    undefined,
    z.array(LibraryWorkspaceNodeSchema) as ZodType<LibraryWorkspaceNode[]>,
  )
}

/**
 * List the entries directly inside `path` in a workspace's work/ tree. Omit
 * or pass '' to list the work-tree root. `includeHidden` surfaces
 * dot-prefixed entries (e.g. work/.library/ — D-8's "Show Hidden" toggle;
 * LibraryEntry.is_hidden is the sole definition of "hidden").
 */
export function fetchLibraryEntries(
  workspaceId: string,
  path = '',
  includeHidden = false,
): Promise<LibraryEntry[]> {
  const params = new URLSearchParams()
  if (path) params.set('path', path)
  if (includeHidden) params.set('include_hidden', 'true')
  const qs = params.toString()
  return request<LibraryEntry[]>(
    `/library/${encodeURIComponent(workspaceId)}/entries${qs ? `?${qs}` : ''}`,
    undefined,
    z.array(LibraryEntrySchema) as ZodType<LibraryEntry[]>,
  )
}

/**
 * Ask whether a folder in a workspace's work tree is a knowledge base
 * (ADR-067 FR-020/FR-021, GET /library/{workspace_id}/knowledge).
 *
 * Detection is marker-based — `.omnipus-vault/` or `.obsidian/` at the root —
 * and never reads file content. `is_knowledge_base: false` is an ANSWER, not
 * an error; a marker that exists but cannot be read comes back as
 * `detection_error` instead, and the caller must surface that rather than
 * treating the folder as ordinary (E-9).
 *
 * Carries no index counts, deliberately. Index progress is the
 * `knowledge_index_progress` WebSocket frame (FR-080) — polling this endpoint
 * for it is the mistake that contract exists to prevent, so callers must not
 * put this on an interval.
 *
 * `path` is workspace-relative; '' means the work-tree root. Unlike the
 * entries listing, the parameter is REQUIRED by the contract, so it is always
 * sent — including as an empty string.
 */
export function fetchKnowledgeBaseInfo(workspaceId: string, path = ''): Promise<KnowledgeBaseInfo> {
  const qs = new URLSearchParams({ path }).toString()
  return request<KnowledgeBaseInfo>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge?${qs}`,
    undefined,
    KnowledgeBaseInfoSchema as ZodType<KnowledgeBaseInfo>,
  )
}

/**
 * The heading outline of one markdown file
 * (ADR-067 FR-062, GET /library/{workspace_id}/knowledge/outline).
 *
 * Served for ANY markdown file, knowledge base or not — an outline is parsed
 * from the one file in hand and needs no index. The response's
 * `is_knowledge_base` / `collection_id` are what tell a caller whether it may
 * ALSO offer search and linked mentions, which is why this one call is enough
 * to decide the whole shape of the reading pane.
 *
 * `path` is workspace-relative and required.
 */
export function fetchKnowledgeOutline(
  workspaceId: string,
  path: string,
  signal?: AbortSignal,
): Promise<KnowledgeOutline> {
  const qs = new URLSearchParams({ path }).toString()
  return request<KnowledgeOutline>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/outline?${qs}`,
    signal ? { signal } : undefined,
    KnowledgeOutlineSchema as ZodType<KnowledgeOutline>,
  )
}

/** Query arguments for {@link fetchKnowledgeGraph}. Not a wire type — the wire
 *  shape is the query string the contract declares; this is the SPA-side
 *  argument bag for it. */
export interface KnowledgeGraphQuery { // not-wire-format: query-parameter argument bag, never serialized as a JSON body
  collectionId: string
  kind: KnowledgeGraphResponse['kind']
  /** COLLECTION-relative path of the note the query is about. Required by the
   *  contract for links/backlinks/neighbourhood. */
  path?: string
  /** UAT D-135 — kind=links only, mutually exclusive with `path`: several
   *  notes whose outbound links are wanted in ONE answer (the union of their
   *  edges; `source_path` is then absent on the wire). Bounded by the contract
   *  to 64 paths. */
  paths?: string[]
  hops?: number
  limit?: number
}

/**
 * One of the five link-graph queries over a knowledge base
 * (ADR-067 FR-051/FR-054, GET /library/{workspace_id}/knowledge/graph).
 *
 * Every query is bounded and reports its own truncation, so a caller can
 * always tell a small graph from a clipped one — see KnowledgeGraphResponse's
 * `truncated`, `node_limit_applied` and `skipped`.
 */
export function fetchKnowledgeGraph(
  workspaceId: string,
  query: KnowledgeGraphQuery,
  signal?: AbortSignal,
): Promise<KnowledgeGraphResponse> {
  const params = new URLSearchParams({ collection_id: query.collectionId, kind: query.kind })
  if (query.path !== undefined && query.path !== '') params.set('path', query.path)
  // Repeated keys (paths=a&paths=b), matching the contract's explode: true —
  // one member per row whose links are wanted, never a comma-joined string.
  for (const p of query.paths ?? []) params.append('paths', p)
  if (query.hops !== undefined) params.set('hops', String(query.hops))
  if (query.limit !== undefined) params.set('limit', String(query.limit))
  return request<KnowledgeGraphResponse>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/graph?${params.toString()}`,
    signal ? { signal } : undefined,
    KnowledgeGraphResponseSchema as ZodType<KnowledgeGraphResponse>,
  )
}

/**
 * Evaluate one saved view and return everything needed to draw it
 * (view-kinds-design-2026-09-03 §7, GET /library/{workspace_id}/knowledge/view).
 *
 * The server evaluates the view's filter/grouping/aggregation through the one
 * query engine and precomputes every aggregate under the gate rules — per-unit
 * totals only (G2), unit-less rows shown/excluded/counted (G3) — so the SPA
 * only draws. A view that cannot answer is a 200 with `refusal` set, never a
 * transport error; an out-of-scope collection answers exactly like an unknown
 * view (FR-052/FR-053), so callers must treat that refusal as an answer too.
 */
export function fetchKnowledgeViewResult(
  workspaceId: string,
  collectionId: string,
  view: string,
  signal?: AbortSignal,
): Promise<ViewResult> {
  const qs = new URLSearchParams({ collection_id: collectionId, view }).toString()
  return request<ViewResult>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/view?${qs}`,
    signal ? { signal } : undefined,
    ViewResultSchema as ZodType<ViewResult>,
  )
}

/**
 * The saved views one `.base` file owns
 * (GET /library/{workspace_id}/knowledge/base-views).
 *
 * THE SERVER'S SLUGS ARE THE ONLY ADDRESSES. Import is one-shot: a `.base`'s
 * views were translated into saved view files and the source is never read
 * again. The SPA used to re-derive each slug by parsing the `.base` itself,
 * which could not reproduce the importer's collision counter — two view names
 * that kebab alike collapsed onto one slug and the second tab rendered the
 * first view's rows. Every `name` returned here comes from the saved view
 * file and must be passed VERBATIM to fetchKnowledgeViewResult.
 *
 * The answer also carries the enclosing collection, so a caller needs no
 * ancestor walk of its own to learn where the views run.
 */
export function fetchKnowledgeBaseViews(
  workspaceId: string,
  path: string,
  signal?: AbortSignal,
): Promise<KnowledgeBaseViews> {
  const qs = new URLSearchParams({ path }).toString()
  return request<KnowledgeBaseViews>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/base-views?${qs}`,
    signal ? { signal } : undefined,
    KnowledgeBaseViewsSchema as ZodType<KnowledgeBaseViews>,
  )
}

/**
 * EVERY saved view a collection owns, file or no file (UAT D-13,
 * GET /library/{workspace_id}/knowledge/views).
 *
 * A view authored with knowledge_configure's create_view/write_view writes a
 * saved view file and NO `.base`, so the file-addressed base-views listing
 * cannot see it; this collection-addressed list is the one surface where such
 * a view exists. Every `name` is the server's own slug and must be passed
 * VERBATIM to fetchKnowledgeViewResult; `source` (when present) names the
 * `.base` an imported view came from. An out-of-scope collection_id answers
 * the same empty list as an unknown one.
 */
export function fetchKnowledgeCollectionViews(
  workspaceId: string,
  collectionId: string,
  signal?: AbortSignal,
): Promise<KnowledgeCollectionViews> {
  const qs = new URLSearchParams({ collection_id: collectionId }).toString()
  return request<KnowledgeCollectionViews>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/views?${qs}`,
    signal ? { signal } : undefined,
    KnowledgeCollectionViewsSchema as ZodType<KnowledgeCollectionViews>,
  )
}

// ── ADR-083 Step 5 (CW-4, CW-7) — inline record-field editor ────────────────
//
// The typed record read/write layer ADR-068 already built (RecordSchema,
// VaultRecord, RecordWriteRequest) was, until now, reachable only from
// agent-facing tools. `fetchVaultRecord` and `writeVaultRecord` are its
// FIRST wiring to the gateway/SPA boundary — the one write door an inline
// editor uses (EMB-085): the same lock, version compare-and-swap, atomic
// write and audit path an agent's write already goes through. Deliberately
// NOT the whole-file Library save endpoint and NOT a raw frontmatter
// property-setter — see RecordWriteRequest's own description for the two
// prohibitions (relations/person properties, derived values) this layer
// enforces server-side regardless of what the client offers an editor for.

/**
 * KnowledgeRecordConflictError is the typed 409 EMB-086 requires
 * `writeVaultRecord` to surface as an actionable CONFLICT, distinct from a
 * generic ApiError(409) — mirrors LibraryVersionConflictError's shape
 * exactly (same reasoning: `actualVersion` is the fresh token a retry MUST
 * send, never the stale one the refused attempt sent). Extends ApiError so
 * every existing `isApiError`/`getErrorMessage` call site still works
 * unchanged.
 */
export class KnowledgeRecordConflictError extends ApiError {
  readonly path: string
  readonly expectedVersion: string | undefined
  readonly actualVersion: string | undefined

  constructor(conflict: KnowledgeConflictError, bodyText: string) {
    super(409, conflict.error, { code: conflict.code, body: bodyText })
    this.name = 'KnowledgeRecordConflictError'
    this.path = conflict.path
    this.expectedVersion = conflict.expected_version
    this.actualVersion = conflict.actual_version
    Object.setPrototypeOf(this, KnowledgeRecordConflictError.prototype)
  }
}

export function isKnowledgeRecordConflict(err: unknown): err is KnowledgeRecordConflictError {
  return err instanceof KnowledgeRecordConflictError
}

/**
 * Re-parses a 409's raw body (preserved on ApiError.body by
 * ApiError.fromResponse) as the typed KnowledgeConflictError envelope. A 409
 * that doesn't match the envelope (unexpected shape, a proxy error page,
 * …) is returned unchanged — still a real 409 ApiError, just not one
 * isKnowledgeRecordConflict() recognises, rather than being misreported.
 */
function knowledgeRecordConflictFromApiError(err: unknown): unknown {
  if (!isApiErrorFn(err) || err.status !== 409 || !err.body) return err
  let raw: unknown
  try {
    raw = JSON.parse(err.body) as unknown
  } catch {
    return err
  }
  const parsed = (KnowledgeConflictErrorSchema as ZodType<KnowledgeConflictError>).safeParse(raw)
  if (!parsed.success) return err
  return new KnowledgeRecordConflictError(parsed.data, err.body)
}

/**
 * One record, by its stable identifier (GET .../knowledge/records/{id}).
 * Used both to open a record a relation cell named non-editable (EMB-092)
 * and, here, to refresh a record's current field values right after
 * `writeVaultRecord` refuses a stale write — so a 409 can show the reader
 * what the field ACTUALLY holds now, with a token this call itself just
 * read, never one carried over from the refused attempt.
 */
export function fetchVaultRecord(
  workspaceId: string,
  id: string,
  signal?: AbortSignal,
): Promise<VaultRecord> {
  return request<VaultRecord>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/records/${encodeURIComponent(id)}`,
    signal ? { signal } : undefined,
    VaultRecordSchema as ZodType<VaultRecord>,
  )
}

/**
 * Create or update one record's properties, by splice
 * (POST .../knowledge/records).
 *
 * `body.mode` STATES which operation this is — `'create'` (which requires
 * `path`) or `'update'` (which requires `id` and `version_token`). It is a
 * discriminated union, so TypeScript refuses a body that mixes the two at
 * compile time and `RecordWriteRequestSchema.parse` refuses one at runtime
 * before the request leaves the browser.
 *
 * That replaced a flat shape where the operation was INFERRED from whether
 * `id` happened to be set, under which an update that lost its `id` was not
 * an error but a valid create: it wrote a duplicate note, discarded the
 * `version_token` the caller had supplied to guard against a concurrent
 * write, and resolved successfully.
 *
 * On an update a stale token is refused with 409 and surfaces as
 * KnowledgeRecordConflictError, never a generic ApiError, so a caller can
 * branch on it specifically (see isKnowledgeRecordConflict).
 */
export async function writeVaultRecord(
  workspaceId: string,
  body: RecordWriteRequest,
): Promise<VaultRecord> {
  try {
    return await request<VaultRecord>(
      `/library/${encodeURIComponent(workspaceId)}/knowledge/records`,
      { method: 'POST', body: JSON.stringify(RecordWriteRequestSchema.parse(body)) },
      VaultRecordSchema as ZodType<VaultRecord>,
    )
  } catch (err) {
    throw knowledgeRecordConflictFromApiError(err)
  }
}

/**
 * Add, remove or replace one record's relation (or person) targets
 * (POST .../knowledge/records/{id}/relation — GAP-02 / #700).
 *
 * FR-045's verbs, verbatim: `add` and `remove` touch only the named targets
 * and leave every other edge alone (never a read-then-write splice through
 * RecordWriteRequest, which the server refuses for relation properties
 * anyway); `replace` is the named destructive verb and the only one that
 * accepts an empty `targets` (clearing the property). A no-op add or remove
 * resolves with `changed: false` — a defined outcome, never an error.
 *
 * Same conflict contract as writeVaultRecord: a stale `version_token`
 * surfaces as KnowledgeRecordConflictError (409) so the caller can re-read
 * and offer a Retry rather than reporting a generic failure. The response
 * carries the record after the write (with its fresh token) and the exact
 * stored spelling of every target, so a picker can reconcile its chips from
 * the response alone.
 */
export async function writeVaultRecordRelation(
  workspaceId: string,
  body: RelationWriteRequest,
): Promise<RelationWriteResponse> {
  try {
    return await request<RelationWriteResponse>(
      `/library/${encodeURIComponent(workspaceId)}/knowledge/records/${encodeURIComponent(body.id)}/relation`,
      { method: 'POST', body: JSON.stringify(RelationWriteRequestSchema.parse(body)) },
      RelationWriteResponseSchema as ZodType<RelationWriteResponse>,
    )
  } catch (err) {
    throw knowledgeRecordConflictFromApiError(err)
  }
}

/**
 * List the directories inside `path` on the operator's own machine, for the
 * mount folder picker.
 *
 * A web page cannot open the native folder picker and learn a real filesystem
 * path, so the gateway lists folders instead. Omit `path` to start in the
 * operator's home directory. Each entry carries its own `mountable`/`broad`
 * verdict so the picker can disable a choice at the point of selection rather
 * than accepting it and refusing afterwards.
 */
export function fetchHostFolders(path?: string): Promise<HostFolderListing> {
  const qs = path ? `?path=${encodeURIComponent(path)}` : ''
  return request<HostFolderListing>(
    `/system/folders${qs}`,
    undefined,
    HostFolderListingSchema as ZodType<HostFolderListing>,
  )
}

/**
 * ADR-072 D1.2/FR-074/FR-074a: what mount-creation time discloses about a
 * mount's recognised skills directory. SPA-shaped (camelCase) transform of
 * the real wire fields `WorkspaceMountCreateResponse.skills_count` /
 * `.skills_grants_message` / `.skills_threshold_warning` — see
 * contracts/components/schemas/WorkspaceMountCreateResponse.yaml, populated
 * by `pkg/gateway/rest_workspace_mounts.go::mountToCreateResponse` from
 * `pkg/skills/mount_threshold.go::EvaluateMountSkillsDisclosure`. Mirrors
 * the transformation-type convention used elsewhere in this file (e.g.
 * `Session`, `ToolCall`): the wire shape is the generated
 * `WorkspaceMountCreateResponse` type; this is the SPA's normalised view.
 */
export interface MountSkillsDisclosure { // not-wire-format: SPA transformation type (camelCase view over the real wire fields WorkspaceMountCreateResponse.skills_count/skills_grants_message/skills_threshold_warning) — see doc comment above
  /** How many project skills the mount's recognised skills directory carries. */
  count: number
  /**
   * FR-074a: states, every time count > 0 (even for a handful of skills),
   * that the mount's skills directory grants agents new auto-loadable
   * instructions — not merely files. Independent of the threshold.
   */
  grantsMessage: string
  /**
   * FR-074: non-empty only when count exceeds the mount-add-time threshold
   * (spec default 500) — states the count and its per-turn consequence. The
   * mount is still created either way (FR-075); this is information, not a
   * refusal.
   */
  thresholdWarning: string | null
}

/**
 * Read the ADR-072 D1.2 skills disclosure off a real
 * `WorkspaceMountCreateResponse` (already validated against the generated
 * schema by `createWorkspaceMount`), or `null` when the mount carries no
 * recognised skills directory (`skills_count` absent — see
 * `EvaluateMountSkillsDisclosure`'s zero-count contract).
 */
export function mountSkillsDisclosure(
  resp: WorkspaceMountCreateResponse,
): MountSkillsDisclosure | null {
  if (
    typeof resp.skills_count !== 'number' ||
    resp.skills_count <= 0 ||
    typeof resp.skills_grants_message !== 'string'
  ) {
    return null
  }
  return {
    count: resp.skills_count,
    grantsMessage: resp.skills_grants_message,
    thresholdWarning: resp.skills_threshold_warning ?? null,
  }
}

/**
 * Mount a real local folder into a workspace, making it writable there.
 *
 * Resolves with a `warning` when the target was broad but allowed (the home
 * directory, the filesystem root, a top-level system directory) — the caller
 * MUST surface it. Rejects (400) when the target is or lies inside the Omnipus
 * data directory, the one hard boundary.
 *
 * Also carries an ADR-072 D1.2 skills disclosure when the mounted folder has
 * a recognised skills directory — read it via `mountSkillsDisclosure(resp)`.
 * Goes through the standard `request<T>()` schema-validated path like every
 * other endpoint (drop + counter + dev-toast on a schema mismatch, per
 * Constraint #8) — no manual raw-body reimplementation needed now that
 * `skills_count`/`skills_grants_message`/`skills_threshold_warning` are real
 * `WorkspaceMountCreateResponse` fields.
 */
export function createWorkspaceMount(
  workspaceId: string,
  body: WorkspaceMountCreateRequest,
): Promise<WorkspaceMountCreateResponse> {
  const path = `/workspaces/${encodeURIComponent(workspaceId)}/mounts`
  return request<WorkspaceMountCreateResponse>(
    path,
    { method: 'POST', body: JSON.stringify(body) },
    WorkspaceMountCreateResponseSchema as ZodType<WorkspaceMountCreateResponse>,
  )
}

/**
 * Revoke a mount. This removes the workspace's ACCESS to the folder and
 * deletes nothing on the operator's disk — the distinction the UI must state
 * outright, since the control sits where "delete" normally lives.
 */
export function deleteWorkspaceMount(workspaceId: string, name: string): Promise<void> {
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/mounts/${encodeURIComponent(name)}`,
    { method: 'DELETE' },
  )
}

/**
 * Create a directory inside a workspace's work tree (mkdir -p semantics —
 * intermediate directories along `path` are created too). Idempotent:
 * resolves normally (200) if a directory already exists at `path`; the
 * caller cannot distinguish "created" from "already there" from the
 * response alone, which the New Folder UI doesn't need to (library-spec.md
 * — UAT fix: without this endpoint being reachable from the UI at all,
 * `POST /library/{workspace_id}/mkdir` working at the API layer was a
 * capability nobody could use).
 */
export function mkdirLibraryEntry(workspaceId: string, body: LibraryMkdirRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    `/library/${encodeURIComponent(workspaceId)}/mkdir`,
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/** Delete a file or directory (and everything under it) from a workspace's work tree. Returns 204. */
export function deleteLibraryEntry(workspaceId: string, path: string): Promise<void> {
  const qs = new URLSearchParams({ path }).toString()
  return request<void>(`/library/${encodeURIComponent(workspaceId)}/entries?${qs}`, { method: 'DELETE' })
}

/** Read a file's text content for the Library viewer (D-5 preview/edit — read side; the editor/preview pane itself is a separate, later task). */
export function fetchLibraryContent(workspaceId: string, path: string): Promise<LibraryContentResponse> {
  const qs = new URLSearchParams({ path }).toString()
  return request<LibraryContentResponse>(
    `/library/${encodeURIComponent(workspaceId)}/content?${qs}`,
    undefined,
    LibraryContentResponseSchema as ZodType<LibraryContentResponse>,
  )
}

// ── ADR-083 Step 0 (EMB-001/EMB-004/EMB-006/EMB-007/EMB-007a/EMB-007b/EMB-007c)
// — closing the Library's unguarded save door ──────────────────────────────
//
// EMB-007c names the problem precisely: `request<T>` above is the SPA's
// single API entry point, and it DISCARDS `res.headers` entirely — so no
// caller that goes through it can ever see the `ETag` response header
// EMB-007/EMB-007a/EMB-007b declare on GET .../content, GET .../download,
// PUT .../content and PUT .../content-binary. Widening `request<T>`'s return
// contract (option a) would touch every SPA call site's type surface for the
// benefit of four callers. Per the spec's own recommendation, this is option
// (b) instead: bespoke fetches — modelled on the providers-catalogue one
// above (`fetchProvidersCatalogOnce`) — for exactly these four operations,
// leaving `request<T>` and every other caller untouched.
//
// The bare (unquoted) token is what a caller sends back as `expect_version`
// (EMB-007b): the wire header is the RFC-quoted strong form (`ETag:
// "v1:…"`), matching what Go's `http.ServeContent` can parse; the request
// body carries the bare value. Centralising the strip here means every one
// of the four operations agrees on the same shape without re-deriving it.
function bareVersionToken(res: Response): string | null {
  const raw = res.headers.get('ETag')
  if (!raw) return null
  const trimmed = raw.trim()
  // Defensive: EMB-007b says a weak (`W/`) form MUST NOT be emitted, but a
  // caller reading an unexpected value should still recover the token rather
  // than silently treating it as absent.
  const unweak = trimmed.startsWith('W/') ? trimmed.slice(2) : trimmed
  if (unweak.length >= 2 && unweak.startsWith('"') && unweak.endsWith('"')) {
    return unweak.slice(1, -1)
  }
  return unweak
}

export interface LibraryVersionedResult<T> { // not-wire-format: SPA-internal pairing of a parsed response body with its ETag-derived version token — never itself sent or received on the wire.
  data: T
  /** Bare (unquoted) version token from the response's `ETag` header, or
   * `null` when the server did not send one (e.g. an older gateway build
   * that hasn't picked up EMB-007 yet). Send this back, unquoted, as
   * `expect_version` on a subsequent PUT .../content or .../content-binary. */
  version: string | null
}

/**
 * LibraryVersionConflictError is the typed 409 EMB-004 requires the Library
 * editors to surface as an actionable CONFLICT — distinct from a generic
 * ApiError(409): `isLibraryVersionConflict(err)` lets a caller branch on it
 * specifically, and `actualVersion` is the fresh token a retry MUST send
 * (never the stale one the refused attempt sent). Extends ApiError so every
 * existing `isApiError`/`getErrorMessage` call site still works unchanged.
 */
export class LibraryVersionConflictError extends ApiError {
  readonly path: string
  readonly expectedVersion: string | undefined
  readonly actualVersion: string | undefined

  constructor(conflict: LibraryConflictError, bodyText: string) {
    super(409, conflict.error, { code: conflict.code, body: bodyText })
    this.name = 'LibraryVersionConflictError'
    this.path = conflict.path
    this.expectedVersion = conflict.expected_version
    this.actualVersion = conflict.actual_version
    Object.setPrototypeOf(this, LibraryVersionConflictError.prototype)
  }
}

export function isLibraryVersionConflict(err: unknown): err is LibraryVersionConflictError {
  return err instanceof LibraryVersionConflictError
}

// Reads the 409 body ONCE (a Response body stream can only be consumed
// once), then attempts to parse it as the typed LibraryConflictError
// envelope. A 409 that doesn't match the envelope (unexpected shape, a proxy
// error page, …) still surfaces as a real 409 ApiError — just not one
// isLibraryVersionConflict() recognises — rather than being misreported as
// some other status.
async function libraryConflictErrorFromResponse(res: Response): Promise<ApiError> {
  const generic = () =>
    new ApiError(409, 'This conflicts with the current state. Please refresh and try again.')
  let bodyText: string
  try {
    bodyText = await res.text()
  } catch {
    return generic()
  }
  let raw: unknown
  try {
    raw = JSON.parse(bodyText) as unknown
  } catch {
    return new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', { body: bodyText })
  }
  const parsed = (LibraryConflictErrorSchema as ZodType<LibraryConflictError>).safeParse(raw)
  if (!parsed.success) {
    return new ApiError(409, 'This conflicts with the current state. Please refresh and try again.', { body: bodyText })
  }
  return new LibraryVersionConflictError(parsed.data, bodyText)
}

// Shared PUT implementation for the two version-carrying Library write doors.
// Mirrors request()'s CSRF fast-fail + withCsrfRetry recovery (never routes
// through request() itself, since that helper discards response headers —
// see the module note above), and distinguishes a 409 conflict from every
// other non-2xx before falling back to the generic ApiError path.
async function putLibraryVersionedWrite<TRes>(
  apiPath: string,
  body: unknown,
  resSchema: ZodType<TRes>,
  signal?: AbortSignal,
): Promise<LibraryVersionedResult<TRes>> {
  if (readCSRFCookie() === null) {
    throw new ApiError(
      403,
      `CSRF cookie missing — cannot PUT ${apiPath}. Log in or complete onboarding first so the server can issue the CSRF cookie.`,
      { code: 'csrf_missing' },
    )
  }
  return withCsrfRetry(() => attemptPutLibraryVersionedWrite(apiPath, body, resSchema, signal))
}

async function attemptPutLibraryVersionedWrite<TRes>(
  apiPath: string,
  body: unknown,
  resSchema: ZodType<TRes>,
  signal?: AbortSignal,
): Promise<LibraryVersionedResult<TRes>> {
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${apiPath}`, {
      method: 'PUT',
      credentials: 'include',
      headers: buildHeaders(),
      body: JSON.stringify(body),
      // UAT D-98 (2026-09-13): a save with no deadline hung in "Saving…"
      // for as long as the network was down — Save disabled, no error, no
      // retry. The editor hook passes a timeout signal so a stalled PUT
      // fails loudly and the button comes back.
      ...(signal ? { signal } : {}),
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (res.status === 409) {
    throw await libraryConflictErrorFromResponse(res)
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }
  let raw: unknown
  try {
    raw = (await res.json()) as unknown
  } catch {
    _recordApiSchemaError(`PUT /api/v1${apiPath}`, 1)
    const schemaErr = new ApiSchemaError(
      `PUT /api/v1${apiPath}`,
      [{ path: [], message: 'Response is not valid JSON' }],
      undefined,
    )
    void maybeDevToast(`[api] Non-JSON response: ${apiPath}`, `PUT:${apiPath}:non-json`)
    throw schemaErr
  }
  const parsed = resSchema.safeParse(raw)
  if (!parsed.success) {
    _recordApiSchemaError(`PUT /api/v1${apiPath}`, parsed.error.issues.length)
    const issues = parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message }))
    void maybeDevToast(`[api] Schema mismatch: ${apiPath} — ${issues[0]?.message ?? 'unknown'}`, `PUT:${apiPath}:schema`)
    throw new ApiSchemaError(`PUT /api/v1${apiPath}`, issues, raw)
  }
  return { data: parsed.data, version: bareVersionToken(res) }
}

/**
 * fetchLibraryContentVersioned is fetchLibraryContent's version-carrying
 * sibling (EMB-007c) — the read side of the Library text editor's save
 * guard. `useLibraryFileEditor` is the only production caller: it needs the
 * `ETag` header fetchLibraryContent's request()-based call can never expose,
 * to send back as `expect_version` on putLibraryContent.
 */
export async function fetchLibraryContentVersioned(
  workspaceId: string,
  path: string,
): Promise<LibraryVersionedResult<LibraryContentResponse>> {
  const qs = new URLSearchParams({ path }).toString()
  const apiPath = `/library/${encodeURIComponent(workspaceId)}/content?${qs}`
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${apiPath}`, { credentials: 'include', headers: buildHeaders() })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) throw await ApiError.fromResponse(res)
  let raw: unknown
  try {
    raw = (await res.json()) as unknown
  } catch {
    _recordApiSchemaError(`GET /api/v1${apiPath}`, 1)
    const schemaErr = new ApiSchemaError(
      `GET /api/v1${apiPath}`,
      [{ path: [], message: 'Response is not valid JSON' }],
      undefined,
    )
    void maybeDevToast(`[api] Non-JSON response: ${apiPath}`, `GET:${apiPath}:non-json`)
    throw schemaErr
  }
  const parsed = (LibraryContentResponseSchema as ZodType<LibraryContentResponse>).safeParse(raw)
  if (!parsed.success) {
    _recordApiSchemaError(`GET /api/v1${apiPath}`, parsed.error.issues.length)
    const issues = parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message }))
    void maybeDevToast(`[api] Schema mismatch: ${apiPath} — ${issues[0]?.message ?? 'unknown'}`, `GET:${apiPath}:schema`)
    throw new ApiSchemaError(`GET /api/v1${apiPath}`, issues, raw)
  }
  return { data: parsed.data, version: bareVersionToken(res) }
}

/**
 * downloadLibraryFileVersioned is the byte-stream sibling of
 * fetchLibraryContentVersioned (EMB-007/EMB-007c) — the ONLY read on the
 * annotated-PDF editor's save path (library-b-c-design-2026-09-07.md), since
 * that editor's loader never calls GET .../content. LibraryPdfPreview's
 * `fetchPdfBytes` is the sole production caller: it captures the `ETag` this
 * returns so `handleSave` has a token to send as `expect_version` on
 * putLibraryContentBinary. `libraryDownloadUrl` (below) stays a plain
 * URL-builder for every non-version-carrying caller (audio/video `src`,
 * download links) — this is deliberately a SEPARATE function, not a
 * behaviour change to that one.
 */
export async function downloadLibraryFileVersioned(
  workspaceId: string,
  path: string,
  signal?: AbortSignal,
): Promise<LibraryVersionedResult<ArrayBuffer>> {
  const qs = new URLSearchParams({ path }).toString()
  const apiPath = `/library/${encodeURIComponent(workspaceId)}/download?${qs}`
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${apiPath}`, {
      credentials: 'include',
      ...(signal ? { signal } : {}),
    })
  } catch (cause) {
    // An abort is the CALLER's own cancellation — it unmounted, or moved to
    // another file — and it is not a network condition. Relabelling it as
    // "Network unavailable" did two harmful things: it defeated the
    // `name === 'AbortError'` guard the one caller (LibraryPdfPreview's load
    // effect) writes to tell "we cancelled this" apart from "this failed",
    // and it reported a routine cancellation to the user, and to CI logs, as
    // a connectivity failure. Rethrow it exactly as the platform threw it.
    if (typeof cause === 'object' && cause !== null && (cause as { name?: string }).name === 'AbortError') {
      throw cause
    }
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) throw await ApiError.fromResponse(res)
  const data = await res.arrayBuffer()
  return { data, version: bareVersionToken(res) }
}

/**
 * Write a file's text content from the Library editor (D-5 — write side).
 * ADR-083 EMB-001/EMB-004/EMB-007c, founder ruling N2: `expect_version` is
 * REQUIRED with no exemption — the caller must have read a token first (see
 * fetchLibraryContentVersioned). Rejects locally with a 400 ApiError before
 * any network call if the token is missing or empty, so an empty token can
 * never reach the wire. A stale token surfaces as LibraryVersionConflictError
 * (409); the response's fresh ETag is returned so a second save in the same
 * session can send THAT token, not the one the original read returned.
 */
export async function putLibraryContent(
  workspaceId: string,
  body: LibraryContentRequest & { expect_version: string },
  opts?: { signal?: AbortSignal },
): Promise<LibraryVersionedResult<LibraryEntry>> {
  // `async` (not a bare `throw` in a non-async function returning a Promise
  // type) is deliberate: every OTHER rejection path in this module surfaces
  // as a genuine Promise rejection, and a caller that does
  // `putLibraryContent(...).catch(...)` rather than `await`/try-catch —
  // fully legitimate given the declared Promise<T> return type — would
  // never see a synchronous throw.
  if (!body.expect_version) {
    throw new ApiError(
      400,
      'A version token is required to save this file — reload it and try again.',
      { code: 'expect_version_missing' },
    )
  }
  return putLibraryVersionedWrite(
    `/library/${encodeURIComponent(workspaceId)}/content`,
    body,
    LibraryEntrySchema as ZodType<LibraryEntry>,
    opts?.signal,
  )
}

/**
 * The version token that says "I believe this file does not exist yet" —
 * `pkg/knowledge/version.go`'s `TokenAbsent`, spelled the same way here so a
 * CREATE goes through the same compare-and-swap door an update does: the
 * server refuses with 409 when a file already occupies the path, so two
 * people creating the same note at once can never silently overwrite each
 * other (UAT #699 / D-115, 2026-09-13).
 */
export const LIBRARY_VERSION_ABSENT = 'v1:absent'

/**
 * Create a NEW text file (UAT #699 / D-115: the Library's "New note").
 *
 * This is `putLibraryContent` with the absent-token sentinel, not a separate
 * endpoint: PUT .../content already treats `expect_version: "v1:absent"` as
 * an exclusive create (`checkLibraryVersion` in pkg/gateway/rest_library.go
 * accepts it only when nothing is at the path). A path that is already
 * taken therefore surfaces as LibraryVersionConflictError (409) — the same
 * error class the editor already knows how to explain — never as an
 * overwrite. Inside a knowledge base the file watcher indexes the new note
 * like any other write, so the record-create door (which needs a declared
 * record type and at least one property) is not required for a plain note.
 */
export function createLibraryTextFile(
  workspaceId: string,
  path: string,
  content: string,
): Promise<LibraryVersionedResult<LibraryEntry>> {
  return putLibraryContent(workspaceId, { path, content, expect_version: LIBRARY_VERSION_ABSENT })
}

/**
 * Write binary file content (a filled PDF, image, any non-UTF-8 bytes) from the
 * Library editor (feature B). Sibling of putLibraryContent — the text route
 * cannot carry bytes. `content_base64` is standard base64; the server decodes,
 * enforces a 25 MB decoded cap, and overwrites the file (PUT .../content-binary).
 * Same `expect_version` contract as putLibraryContent above (ADR-083
 * EMB-001/EMB-004/EMB-007/EMB-007c, founder ruling N2) — REQUIRED, no
 * exemption, including for the annotated-PDF editor: see
 * downloadLibraryFileVersioned for where that editor's token comes from.
 */
export async function putLibraryContentBinary(
  workspaceId: string,
  body: LibraryBinaryContentRequest & { expect_version: string },
): Promise<LibraryVersionedResult<LibraryEntry>> {
  // async for the same reason as putLibraryContent above — see its comment.
  if (!body.expect_version) {
    throw new ApiError(
      400,
      'A version token is required to save this file — reload it and try again.',
      { code: 'expect_version_missing' },
    )
  }
  const validated = LibraryBinaryContentRequestSchema.parse(body)
  return putLibraryVersionedWrite(
    `/library/${encodeURIComponent(workspaceId)}/content-binary`,
    validated,
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/**
 * Create a new Omnipus knowledge base ("vault") inside a workspace (feature C2).
 * Scaffolds the `.omnipus-vault/` marker and returns the created directory entry.
 * Rejects (409) if the target already exists (POST .../vaults).
 */
export function createVault(
  workspaceId: string,
  body: CreateVaultRequest,
): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    `/library/${encodeURIComponent(workspaceId)}/vaults`,
    { method: 'POST', body: JSON.stringify(CreateVaultRequestSchema.parse(body)) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/**
 * Human vault search (feature C1) — text hits, records by typed property, and
 * saved views, grouped as notes/records/views. Runs over the SAME index the
 * agent's knowledge_find uses (POST .../knowledge/find). Never errors on an
 * empty or not-yet-ready index — the response carries `complete`/`complete_reason`.
 */
export async function searchVault(
  workspaceId: string,
  body: VaultSearchRequest,
  signal?: AbortSignal,
): Promise<VaultSearchResponse> {
  return request<VaultSearchResponse>(
    `/library/${encodeURIComponent(workspaceId)}/knowledge/find`,
    {
      method: 'POST',
      body: JSON.stringify(VaultSearchRequestSchema.parse(body)),
      ...(signal ? { signal } : {}),
    },
    VaultSearchResponseSchema as ZodType<VaultSearchResponse>,
  )
}

/**
 * Index-free file search over a plain folder/mount's confined Library root
 * (unified-search-and-grep-spec.md workstream B/C,
 * POST .../library/{workspace_id}/files/search). Names/paths always match;
 * text-file content matches under the engine's byte/match/depth/deadline
 * bounds. The human bar always sends `regex:false` (a literal, smart-case
 * query) — `regex:true` is the tool/API opt-in this client fn also carries so
 * callers who need it aren't blocked.
 *
 * HONESTY: any bound that stops the walk is reported via `truncated` +
 * `truncated_reason`; clamped limits are echoed in `limits_applied`; a lost
 * walk/mount root is `truncated_reason: "root_lost"`, never a silent empty
 * result (MV-3/MV-9 siblings for this endpoint).
 *
 * `signal` cancels the in-flight request AND the server-side walk (the
 * gateway holds a 2-slot walk semaphore shared with the agent `grep` tool —
 * MV-11) — callers hold at most one in-flight search and abort the previous
 * one on a new keystroke or navigation.
 */
export async function searchFiles(
  workspaceId: string,
  body: FileSearchRequest,
  signal?: AbortSignal,
): Promise<FileSearchResponse> {
  return request<FileSearchResponse>(
    `/library/${encodeURIComponent(workspaceId)}/files/search`,
    {
      method: 'POST',
      body: JSON.stringify(FileSearchRequestSchema.parse(body)),
      ...(signal ? { signal } : {}),
    },
    FileSearchResponseSchema as ZodType<FileSearchResponse>,
  )
}

/** Rename or move an entry within a single workspace's work tree — same-workspace sugar over /library/move. Rejects (409) if "to" already exists. */
export function renameLibraryEntry(workspaceId: string, body: LibraryRenameRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    `/library/${encodeURIComponent(workspaceId)}/rename`,
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/**
 * Move a file or directory, optionally across two workspaces (D-9). Rejects
 * (409) if the destination already exists — the server never silently
 * overwrites, so there is no "overwrite" outcome to confirm beyond the
 * dialog step itself; a 409 here is surfaced to the caller as a normal
 * ApiError (never swallowed).
 */
export function moveLibraryEntry(body: LibraryTransferRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    '/library/move',
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/** Copy a file or directory, optionally across two workspaces (D-9), leaving the source in place. Same 409-on-conflict behavior as moveLibraryEntry. */
export function copyLibraryEntry(body: LibraryTransferRequest): Promise<LibraryEntry> {
  return request<LibraryEntry>(
    '/library/copy',
    { method: 'POST', body: JSON.stringify(body) },
    LibraryEntrySchema as ZodType<LibraryEntry>,
  )
}

/**
 * Mint a short-lived preview token (POST /library/preview-token, spec
 * FR-003f) so the sandboxed HTML/SVG preview frame — an opaque-origin
 * document that can send neither the session cookie nor an Authorization
 * header — can load itself and its relative subresources (ADR-067 §10.3).
 * Minting is authenticated and never widens access: the caller must already
 * be able to read `body.path`, and the token is confined to one workspace and
 * one path (FR-003b/FR-003i). No workspace_id in the route, matching the
 * existing /library/move and /library/copy shape — see LibraryPreviewPane's
 * `PREVIEW_TOKEN_MINTER`, the sole production caller.
 */
export function mintLibraryPreviewToken(
  body: LibraryPreviewTokenRequest,
): Promise<LibraryPreviewTokenResponse> {
  return request<LibraryPreviewTokenResponse>(
    '/library/preview-token',
    { method: 'POST', body: JSON.stringify(body) },
    LibraryPreviewTokenResponseSchema as ZodType<LibraryPreviewTokenResponse>,
  )
}

/**
 * Revoke one preview token before it expires
 * (DELETE /library/preview-token/{token}, generated operation
 * revokeLibraryPreviewToken — UAT 2026-09-13 D-110, Codex review #11).
 *
 * The endpoint shipped a round before this caller did: closing a preview
 * cleared the pane's expiry timer and nothing else, so a copied frame URL
 * kept answering 200 for the rest of its 15-minute life. LibraryPreviewPane's
 * HTML frame now calls this from its unmount cleanup — including for a mint
 * that completes AFTER the pane is gone — so the credential stops working the
 * moment the reader is done with it.
 *
 * 204 whether the token was live, expired, already revoked or never existed
 * (FR-003n): revoking can only narrow access, so a caller never needs to know
 * which it was, and a 404 here would be an oracle for whether a token ever
 * existed.
 */
export function revokeLibraryPreviewToken(token: string): Promise<void> {
  return request<void>(`/library/preview-token/${encodeURIComponent(token)}`, { method: 'DELETE' })
}

/**
 * Upload one or more files into `path` inside a workspace's work tree (D-1 —
 * uploads land as real, named files, de-duplicated server-side on collision).
 * Multipart; mirrors uploadFiles's raw-fetch pattern above since request() is
 * JSON-only.
 */
export async function uploadLibraryFiles(workspaceId: string, files: File[], path = ''): Promise<LibraryUploadResponse> {
  const formData = new FormData()
  for (const file of files) {
    formData.append('files', file)
  }
  // Upload is a state-changing POST — fail fast if we have no CSRF cookie
  // (see request() for the same pattern).
  if (readCSRFCookie() === null) {
    throw new ApiError(
      403,
      'CSRF cookie missing — cannot upload files. Log in first so the server can issue the CSRF cookie.',
      { code: 'csrf_missing' },
    )
  }
  // FormData holds File references (not a consumed stream), so retrying with
  // the same formData on a CSRF-recovery pass (withCsrfRetry) re-sends the
  // same bytes safely.
  return withCsrfRetry(() => doUploadLibraryFiles(workspaceId, formData, path))
}

async function doUploadLibraryFiles(workspaceId: string, formData: FormData, path: string): Promise<LibraryUploadResponse> {
  // Read fresh — never cache (see readCSRFCookie).
  const csrf = readCSRFCookie()
  const headers: Record<string, string> = {}
  if (csrf) headers[CSRF_HEADER_NAME] = csrf
  const qs = path ? `?${new URLSearchParams({ path }).toString()}` : ''
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/library/${encodeURIComponent(workspaceId)}/upload${qs}`, {
      method: 'POST',
      credentials: 'include',
      // NOTE: do NOT set Content-Type — the browser sets the multipart boundary.
      headers,
      body: formData,
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }
  const raw: unknown = await res.json()
  const parsed = (LibraryUploadResponseSchema as ZodType<LibraryUploadResponse>).safeParse(raw)
  if (!parsed.success) {
    _recordApiSchemaError('/library/upload', parsed.error.issues.length)
    const issues = parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message }))
    void maybeDevToast(`[api] uploadLibraryFiles response schema mismatch: ${issues[0]?.message ?? 'unknown'}`, 'POST:/library/upload:schema')
    throw new ApiSchemaError('/library/upload', issues, raw)
  }
  return parsed.data
}

/**
 * URL for downloading a file's raw bytes (GET .../download). Deliberately
 * NOT routed through request() — the response is a binary stream, not JSON,
 * and GET is not state-changing so no CSRF token is required. Meant for an
 * <a href=… download> or window.open(): auth rides the same-origin
 * omnipus-session cookie automatically on a plain navigation/anchor click.
 */
export function libraryDownloadUrl(workspaceId: string, path: string): string {
  const qs = new URLSearchParams({ path }).toString()
  return `${BASE_URL}/api/v1/library/${encodeURIComponent(workspaceId)}/download?${qs}`
}
