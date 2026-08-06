# Final type-design-analyzer review (pr-review-toolkit)

**Reviewer:** `pr-review-toolkit:type-design-analyzer`
**Scope:** `sendfile-fix` HEAD `1ea79f9e` against parent `ae9271d0` (42 commits, ADR-051 Rev 4, Slices A + B + C + D + E + F + G + H + Wave-2/3 wiring)
**Mode:** read-only review; the only written artifact is this requested report
**Focused surfaces:** `pkg/media/library/`, `pkg/media/resize/`, `pkg/providers/capabilities/`, `pkg/agent/media_present.go`, `pkg/media/resolve.go`, the generated `MediaLibraryEntry` / `MediaAttachmentRequest` Go + TS + Zod, the SPA library-attachment store and tab/composer surfaces

## Verdict

**REVISE — DO NOT MERGE TO MAIN WITHOUT FIXING THE WIRE-ROUTE GAP.  0 CRITICAL, 4 MAJOR, 6 MINOR, 3 OBSERVATIONS.**

The four new private-domain types — `Library.manifestEntry`, `refcount`, `model` + `resolvedModel`, `ResolveOpts` / `WorkspaceLibraryResolver` — are well-designed (private fields, value-typed invariants, projected to wire types at the boundary). Most r1+carry-over TD findings from the wave-0/1/2 type-design reports are closed in this stack. The two `MAJOR`s left by this review are (a) a workspace-media REST routing gap that makes `MediaLibraryEntry` / `MediaAttachmentRequest` dead-on-arrival at the gateway, and (b) a `Library.upload` ⇆ `Library.UploadFixture` near-duplication that has remained unaddressed across two waves. The `MAJOR` on `sessionRefcounter.DecrementRefcount`'s signature is half-implemented. Plus a fresh `MAJOR` on `audit.EventMediaDelete` being declared but never produced in production.

The strongest corrections to build on:
- `manifestEntry` + private `refcount` + single source-of-truth: every invariant lives on the value type, the gen type is a projection at the API edge. The Load path validates through `newManifestEntry`. The persisted envelope at `library.go:1203-1205` is `{Version, Entries}` only, no parallel `Refcounts` map.
- `capabilities.model` + `resolvedModel` are private; `Resolve` returns a deep-owned handle whose `InputModalities()` returns a copy; the `refreshMu` serializes the WHOLE Refresh transaction.
- `Version.Compare` is semver-aware (numeric major/minor/patch) with a lexical fallback for date-tracking strings — the v10 > v2 bug is fixed.
- `MediaLibraryEntrySource` has only `ToolOutput` and `UserUpload` on the wire; the internal-only `fixtureSource` is a package-private constant rejected by `gen.UserUpload`-only `Upload`.
- `ResolveOpts` + `WorkspaceLibraryResolver` separate the bytes-returning (`Read` with sha256 verification) and path-returning (`ResolvePathWithCaller` for transport-only consumers) surface so the cross-workspace guard (FR-028a) is load-bearing on every entry point without a redundant decode for transport-only callers.

The must-fix before merge is the gateway routing for workspace media — see `TD-M9` below.

## Findings

| ID | Severity | Surface | Finding | Required correction |
|---|---|---|---|---|
| **TD-M9** | **MAJOR** | Slice H/Wave 2 — workspace-media REST endpoints | The OpenAPI contract (`contracts/openapi.yaml:5493-5505`, `5506-…`, etc.) and the SPA client (`src/lib/api.ts:3287`, `3300`, `3313`) define `fetchWorkspaceMedia` (`GET /api/v1/workspaces/{id}/media`), `attachWorkspaceMedia` (`POST /api/v1/workspaces/{id}/media/attachments`, body `MediaAttachmentRequest`), and `deleteWorkspaceMedia` (`DELETE /api/v1/workspaces/{id}/media/{mediaId}`). The Go gateway dispatcher at `pkg/gateway/rest_workspaces.go:494-545` (`HandleWorkspaces`) has explicit rules for `/milestones`, `/delegation`, `/instructions`, then reaches line 530's `if strings.Contains(id, "/") { http.NotFound(w, r); return }` — which returns 404 for any nested path under `/workspaces/{id}/…` not matched above. The Media tab (`src/components/workspaces/WorkspaceMediaTab.tsx:71`) and composer library picker (`src/components/chat/ComposerMediaLibrary.tsx:77`) will 404 every call. UAT-009 ("Library list + delete") cannot pass against a live gateway. This makes `MediaLibraryEntry` and `MediaAttachmentRequest` *typed but unreachable* at the boundary — a contract-first violation (Constraint #8 intent: wire types serve implemented endpoints). | Wire three handlers in a new `pkg/gateway/rest_workspace_media.go` (or extend `rest_workspaces.go`): `handleWorkspaceMediaList` on `GET /workspaces/{id}/media`, `handleWorkspaceMediaAttach` on `POST /workspaces/{id}/media/attachments` (consumes the `MediaAttachmentRequest` body and registers the ref — currently no go-routine does this registration; verify the existing `register`/`attach` semantics for a non-uploaded file exist or add them), and `handleWorkspaceMediaDelete` on `DELETE /workspaces/{id}/media/{mediaId}` that calls `library.Delete` AND emits `audit.EventMediaDelete`. Add the dispatch logic in `HandleWorkspaces` (matching the precedence pattern at line 506 for `/delegation`). Then run a live-gateway smoke that exercises each of the three paths and asserts the SPA renders the row. The TS client wrapper functions already exist — all that is missing is the Go half. |
| **TD-M10** | **MAJOR** | Slice B1 — `Library.Upload` ⇆ `Library.UploadFixture` near-duplication | `Upload` (`library.go:339-453`) and `UploadFixture` (`library.go:1034-1135`) are 114-line near-clones: every byte-for-byte detail of the streaming-write + sha256 + mime-sniff + manifest-construct + persist-on-success path is duplicated, differing only in (i) the `source` value passed to `newManifestEntry` (`gen.UserUpload` vs `fixtureSource`), and (ii) the early `ErrSourceNotAllowed` check in `Upload` versus its absence in `UploadFixture`. The two pass ~700 lines of bodies that must stay in sync; a future regression on one path (e.g. a tightening of `normalizeFilename`, a new field added to `manifestEntry`, a future quarantine step added to `persistLocked`) has to be applied to both. The fixture-source split (Wave 1 TD-m1) was needed; the implementation just didn't extract the shared core. | Extract a private `uploadInternal(filename string, source gen.MediaLibraryEntrySource, requireSourceIsProduction bool, reader io.Reader) (string, gen.MediaLibraryEntry, error)` that does the shared streaming + sha256 + persist logic. `Upload` calls it with `source = gen.UserUpload`, `requireSourceIsProduction = true` (the existing `ErrSourceNotAllowed` check goes here). `UploadFixture` calls it with `source = fixtureSource`, `requireSourceIsProduction = false`. Both share the same bytes-on-disk + sha256-on-write + manifest-construct path — drift impossible. Note: `fixtureSource` must still NOT leak to the wire; `UploadFixture`'s exposure on the returned projection is fine because the wire enum drops it (contracts/components/schemas/MediaLibraryEntry.yaml only enumerates `user_upload` and `tool_output`). |
| **TD-M11** | **MAJOR** | Slice B1 — `OrphanGC` rollback re-builds a `manifestEntry` from a projection, bypassing `newManifestEntry`'s invariant gate | `Library.OrphanGC` (`library.go:722-806`) snapshots the to-be-deleted entries as projections (`deleted = append(deleted, l.manifest[id].projection())` at line 765), then on `persistLocked` failure at line 768 rebuilds the in-memory entry from the projection by hand (`manifestEntry{...}` literal at line 779), bypassing `newManifestEntry`'s full invariant gate (id not nil, filename normalized, mime non-empty, size in range, sha256 well-formed, source valid, refcount ≥ 0, observedAt non-zero). Today this is safe because the projections came from already-validated entries; the r1 invariant might come back to bite a future schema migration that tightens `newManifestEntry` (e.g. a new required field). This is the same class of "type invariant owns construction but not rollback" bug the r1 manifestEntry refactor was supposed to close. | Replace the projection-based rollback with a parallel `[]manifestEntry` snapshot (kept under the mutex) and restore directly from the saved snapshot. The projection-derived path can be deleted; the snapshot already carries the full private shape, no round-trip rebuild required. |
| **TD-M12** | **MAJOR** | Slice B2 — `audit.EventMediaDelete` is declared but no production caller emits it | `pkg/audit/events.go:267` declares `EventMediaDelete = "media.delete"` (an INFO event for FR-008 single-file delete). It is registered in the audit allow-list at `pkg/audit/audit.go:211`. The `Library.Delete` method returns the deleted `gen.MediaLibraryEntry` (library.go:599, 645) so the caller has every field needed to construct the event (filename + size for `bytes_freed`). But a search across `pkg/gateway/` for `EventMediaDelete` finds zero production callers — only test stubs at `pkg/media/library/library_test.go:538, 558, 730, 760` reference it. FR-008's audit event is therefore unenforced at the runtime boundary. Cascade-delete (`EventMediaCascadeDelete`) IS wired correctly via `WorkspaceDeleteHook` at `pkg/workspace/media_delete.go:56-76`. | Once `TD-M9`'s DELETE handler exists, the new `handleWorkspaceMediaDelete` MUST emit `audit.EventMediaDelete` with `bytes_freed = *projection.Size`, `media_id = id.String()`, `filename = projection.Filename`, `workspace_id = workspaceID`, `actor = <authenticated principal>`. Mirror the shape used in `WorkspaceDeleteHook`'s `EventMediaCascadeDelete` (line 56-75). Then add an integration test through `pkg/gateway/` exercising the handler and asserting the audit row lands. |
| **TD-m5** | MINOR | Slice B1 — wire `MediaLibraryEntry.refcount` / `last_refcount_seen_at` description says "Required on every entry" but neither schema nor Zod enforces it | `contracts/components/schemas/MediaLibraryEntry.yaml:78-83` textually describes both fields as required (`Required on every entry (FR-007a / Wave 1 TD-M2)`), but they are NOT listed in the YAML `required:` array (line 7-15). The generated Go (`pkg/api/generated/openapi_types.gen.go:5565, 5559`) types both as `*int` / `*time.Time` (pointer, `omitempty`). The SPA Zod schema (`src/lib/api/generated/schemas.ts:2394-2395`) types them as `.optional()`. The TS wire type (`src/lib/api/generated/openapi-types.ts:3093, 3098`) types them as `readonly?: number` / `readonly?: string`. The contract says required; every layer below it says optional; the SPA never reads either field. | Either (preferred) make them required in YAML and let the codegen regenerate to non-optional — that requires the server to always emit them, which it does (`library.manifestEntry.refcount` + `lastRefcountSeenAt` are always populated post-load). Or (acceptable) drop the description's "Required on every entry" wording and commit to "server-maintained; clients should treat undefined as an older server version" — but that requires a new optional-on-the-wire shape (`optionalProperties`) which oapi-codegen doesn't natively support. The first path is the more honest fix. |
| **TD-m6** | MINOR | Slice F — `Result.Mime` is still a `string` after r1 | `pkg/media/resize/resize.go:58-62` returns `Result.Mime string` with `"image/png"` or `"image/jpeg"` literal callers (lines 114, 125). The doc comment notes "PNG or JPEG, see Mime" but does not represent that closure. PNG-only at `resize.go:111-115` (canonical output), JPEG at `resize.go:118-127`. This was a r1 carry-over (TD-m3 in the wave-1 round-1 report). | Introduce a typed `OutputFormat string` (or `MIME string`) with `OutputPNG OutputFormat = "image/png"` and `OutputJPEG OutputFormat = "image/jpeg"` constants. Make `Result.Mime` of this type. The `longEdge ↔ mime` coupling is enforced by `ResizeToFit` — only those two values emerge. |
| **TD-m7** | MINOR | Slice D — `detectFileClass` returns a closed `string` enum | `pkg/agent/loop_media.go:837-845` returns `"image" | "document" | "file"` (no type), consumed by `buildOffloadGuidance` (line 882) which switches on the literal string. The class is closed (only three values emerge from the function); the type system does not know that. An internal helper (`offloadNoun` at line 888) switches on the same literals — second occurrence. | Type the class as a closed internal type (e.g. `type fileClass int; const (classImage fileClass = iota; classDocument; classFile)`) or a typed string with constants, and route both `buildOffloadGuidance` and `offloadNoun` through it. The string output (`"Cannot read this image…"` line 884) becomes a `string(fileClass)` cast at one place. |
| **TD-m8** | MINOR | Slice C — `GHReleasePuller` carries exported mutable fields and a nil-error constructor | `pkg/providers/capabilities/puller.go:35-66` exposes `Owner`, `Repo`, `Asset`, `Ref`, `HTTPClient`, `BaseURL`, `RawBaseURL`, `UserAgent` as exported fields. `NewGHReleasePuller` (line 86-97) returns `*GHReleasePuller` with no error — invalid required fields surface only at `Pull` time. A nil `HTTPClient` is defensively handled (line 302-307) but a struct-literal caller can set `UserAgent = ""` and the call to GitHub rejects the request; the empty-string path is not documented as a defended invariant. This was an r1+carry-over (TD-m2). | Construct via `NewGHReleasePuller(owner, repo, asset string) (*GHReleasePuller, error)` returning an error when any of `owner|repo|asset` is empty. Keep production fields private + immutable; expose narrowly named test-only options (e.g. `withHTTPClient`, `withBaseURLs`) for the test override path. |
| **TD-m9** | MINOR | Slice B1 — `Refcount(id string) (int, error)` loses the wrapped type | `Library.Refcount` (library.go:576-587) returns the canonical `int` rather than the internal `refcount` type. This is fine for the sentinel-error contract but it lets a caller assign to `int` and lose the wrapped-type guard. Every other refcount-bearing path uses `*resolvedModel`'s accessor or the same wrapper-typed fields. | Either (a) keep `Refcount` as-is and acknowledge that the watcher inspection path doesn't need the wrapper, OR (b) add `RefcountWrapped(id string) (refcount, error)` and have the existing `Refcount` wrap it. Path (a) is the simpler call. |
| **TD-m10** | MINOR | Slice B1 — `Library.Read` integrity-check projection returns a partially-defaulted gen type | When `Read` (library.go:455-494) catches an integrity violation (line 491), it returns `nil, projection, fmt.Errorf(...)`. The `projection` at that point carries `Sha256` and `Size` (computed from `entry.projection()` at line 465). The defensive nil-check `if projection.Size == nil || projection.Sha256 == nil` (line 466) is dead — `manifestEntry.projection()` (library.go:220-241) always sets `Size` and `Sha256` to non-nil pointers to copied values. The defensive check is **never** exercised in production. | Drop the unreachable defensive check at lines 466-468. The constructor invariant guarantees the projection fields are populated; the check only adds noise and makes the integration harder to read. |

## Observations

| ID | Surface | Note |
|---|---|---|
| **TD-O1** | Slice B1 — `manifestFile` envelope is narrower than the legacy split | `manifestFile{Version, Entries}` (`library.go:1203-1205`) is a real invariant improvement over the r1 `{Entries, Refcounts}` shape. The optional `Generation` / `UpdatedAt` shape fields are deliberately omitted; the two-call-site Load (line 836-838) only branches on `Version`. This is the right call for v0.1.1. |
| **TD-O2** | Slice C — `capabilities.logger` interface | `pkg/providers/capabilities/catalog.go:410-413` defines a minimal local `logger` interface (`Warn/Info`) instead of importing the project's `*slog.Logger`. Tests can swap a `noopLogger{}` cleanly. This is the right level of abstraction — keeping logging non-imported is correct for a leaf package. |
| **TD-O3** | Slice B1 — `OffloadSink` is private (not exposed) and `Library.Refcount` is read-only | `offloadSink` (`loop_media.go:687-689`) is package-private and constructed by the orchestrator via dependency injection. `Library.Refcount` exposes only the read value — mutation is gated through `IncrementRefcount` / `DecrementRefcount` (which atomically write through `persistLocked`). The `Refcount(id) int` exposure is correct: it lets `OrphanGC` (library.go:736) compare against zero without exposing the internal `refcount` type, and the writer side never escapes the package. |

## Detailed evidence and causal chains

### TD-M9 — the workspace-media routes are SPA-only; the gateway 404s

1. SPA client wrapper `fetchWorkspaceMedia(workspaceId)` at `src/lib/api.ts:3287-3293` issues `GET /workspaces/{encodeURIComponent(workspaceId)}/media` and validates the response via `z.array(MediaLibraryEntrySchema) as ZodType<MediaLibraryEntry[]>`.
2. `attachWorkspaceMedia(workspaceId, mediaId)` at `src/lib/api.ts:3313-3319` issues `POST /workspaces/{encodeURIComponent(workspaceId)}/media/attachments` with JSON body `MediaAttachmentRequest = { media_id: mediaId }`.
3. `deleteWorkspaceMedia(workspaceId, mediaId)` at `src/lib/api.ts:3300-3305` issues `DELETE /workspaces/{encodeURIComponent(workspaceId)}/media/{encodeURIComponent(mediaId)}` (note: contract path `media/attachments` for POST vs `media/{media_id}` for DELETE matches the openapi.yaml).
4. The Media tab renders the result: `WorkspaceMediaTab.tsx:71-80` calls `useQuery({queryKey: workspacesQueryKeys.media(workspaceId), queryFn: () => fetchWorkspaceMedia(workspaceId), enabled: workspaceId !== ''})` and renders the rows on success.
5. The composer library picker: `ComposerMediaLibrary.tsx:75-80` calls the same `fetchWorkspaceMedia` plus `attachWorkspaceMedia` at line 88. The picker's empty-state copy is `"No files in this workspace yet. Upload one in chat first."` — implying list was previously expected to return entries.
6. The OpenAPI contract promises the endpoints. Spot-check the Slice H commit `724cf001`:
   ```
   $ git show --stat 724cf001 | head
   contracts/openapi.yaml                             |  31 +++
   src/components/chat/ComposerMediaLibrary.test.tsx  | 142 +++++
   src/components/chat/ComposerMediaLibrary.tsx       | 240 +++++
   src/components/workspaces/WorkspaceMediaTab.test.tsx | 115 +++
   src/components/workspaces/WorkspaceMediaTab.tsx    | 238 +++
   src/lib/api.ts                                     |  58 +
   src/lib/library-attachment.ts                      | 113 +++
   src/lib/library-attachment.test.ts                 | 133 +++
   src/routes/_app/workspaces.$workspaceId.media.tsx  |  13 +
   ```
   No Go file. The Slice H deliverable was contract + SPA only; the Go gateway wiring was scoped to a follow-up that did not happen.
7. The Go dispatcher at `pkg/gateway/rest_workspaces.go:494-545` (`HandleWorkspaces`) recognises:
   - line 500: `if strings.Contains(rest, "/milestones")` — delegates to `HandleMilestones`
   - line 506: `if strings.HasSuffix(rest, "/delegation")` — handles GET/PUT delegation
   - line 520: `if strings.HasSuffix(rest, "/instructions")` — handles instructions
   - line 527-545: `if len(rest) > 1` — handles the bare `/workspaces/{id}` GET/PUT/DELETE
   - line 530-532: any path containing a `/` after the id (i.e. `/workspaces/{id}/anything`) falls through to `http.NotFound`
8. The WorkspaceMediaTab test (`WorkspaceMediaTab.test.tsx:115+`) — bundled with Slice H — covers only the SPA happy/sad paths; it does not exercise the gateway dispatch.
9. UAT-009 in `docs/internal/uat/ADR-051-rev4-uat-test-plan.md:75-79` says "list shows all entries (filename, size, sha256, uploaded_at). Delete removes file + manifest." — but the UAT CI evidence (`ADR-051-rev4-ci-evidence.md`, run 30029156405, "22/22 passed") is the same documentation file, not a recorded live-gateway smoke (the dev-pod is a local workstation; "Observe" rather than "Assert" UAT steps predate the live-skill that converts UAT to assertions). The UAT is tool-verified against the SPA via Component tests only.
10. Net effect: `MediaLibraryEntry` and `MediaAttachmentRequest` are types that describe a wire contract whose endpoints are unreachable. A user clicking the workspace "Media" tab gets an error toast (the SPA's `ApiSchemaError` re-throw path at `src/lib/api.ts::ApiSchemaError` for non-2xx responses with a `ZodError` is the visible symptom), and `attachWorkspaceMedia` from the composer library picker will 404 on a real deployment.

### TD-M10 — `Library.Upload` ⇆ `Library.UploadFixture` are 114-line near-clones

Diff analysis: lines 339-453 (`Upload`, 115 lines) vs lines 1034-1135 (`UploadFixture`, 102 lines). Identical structural shape:

| Step | Upload line | UploadFixture line | Difference |
|---|---|---|---|
| filename normalize | 344-347 | 1038-1041 | identical |
| source check | 348-350 | (none — `UploadFixture` always passes `fixtureSource` directly to the constructor) | Upload errors with `ErrSourceNotAllowed` for non-`UserUpload`; `UploadFixture` doesn't bother (correctly — no need) |
| reader nil | 351-353 | 1042-1044 | identical |
| mkdir + createTemp + chmod | 354-374 | 1045-1065 | identical |
| prefix sniff + size-cap read | 375-389 | 1066-1080 | identical |
| sync + close + rename | 390-400 | 1081-1091 | identical |
| manifest construct via `newManifestEntry` | 416-434 | 1098-1116 | one field: `source` literal differs |
| `l.manifest[id.String()] = entry; persistLocked` | 436-449 | 1118-1131 | identical |
| Return ref + projection | 451-452 | 1133-1134 | identical |

If a future change updates the sha256-write path or the persist rollback, both copies have to change in lockstep. Today the spec says `manifestEntry.uploadedAt.UTC()` (line 200 + 1094) is set from `l.now()`; a future tightening (e.g. requiring the upload to fingerprint against a MEXC/Merkle proof) would need to land in both places. The two ways diverge by exactly one branch: the source-allowed check. That branch belongs in `uploadInternal`.

### TD-M11 — `OrphanGC` rollback bypasses the invariant gate

`library.go:769-791` snapshot+rollback sequence:
```go
deleted = append(deleted, l.manifest[id].projection())   // line 765
delete(l.manifest, id)                                   // line 766
if err := l.persistLocked(); err != nil {
    for _, projection := range deleted {                  // line 769 — rollback
        if projection.Id == nil { continue }
        id := projection.Id.String()
        rc, _ := newRefcount(derefInt(projection.Refcount))   // sole constructor gate
        l.manifest[id] = manifestEntry{                    // literal — bypasses full constructor
            id:                 *projection.Id,
            workspaceID:        derefString(projection.WorkspaceId),
            filename:           projection.Filename,
            mime:               derefString(projection.Mime),
            size:               derefInt64(projection.Size),
            sha256:             derefString(projection.Sha256),
            uploadedAt:         derefTime(projection.UploadedAt),
            source:             projection.Source,
            refcount:           rc,
            lastRefcountSeenAt: derefTime(projection.LastRefcountSeenAt),
        }
    }
    l.restoreQuarantined(quarantined)
    return nil, err
}
```

Two problems:
1. `newManifestEntry` (library.go:149-205) validates: id non-Nil; workspaceID non-empty; filename normalized (length ≤256, no control chars); mime non-empty; size 0 ≤ size ≤ MaxFileSize; sha256 64 lowercase hex; uploadedAt non-zero; source valid (or `fixtureSource`); refcount ≥ 0; observedAt non-zero.
   `OrphanGC`'s rollback passes `newRefcount(intVal)` for the refcount, but skips the rest. A persisted projection with `projection.Mime == nil` (e.g. a corrupted-but-loadable old manifest) would silently land a `manifestEntry{mime: ""}` after rollback — bypassing the mime-non-empty gate that `newManifestEntry` enforces on initial load.
2. The deref helpers (`derefString`/`derefInt`/`derefInt64`/`derefTime` at library.go:1169-1195) silently coerce nil to zero values. If a future refactor narrows the projection shape, those helpers fall through with wrong-but-typed defaults.

The simple fix is to keep a `previousManifest := make(map[string]manifestEntry, len(l.manifest))` snapshot under the mutex (CascadeDelete already does this at line 673) and restore from it instead of from projection. It's the same pattern, copy-paste-able from CascadeDelete's rollback at line 695-699.

### TD-M12 — `audit.EventMediaDelete` is dead in production

`pkg/audit/events.go:259-267` declares it; `pkg/audit/audit.go:211` registers it in the allow-list. Yet:
```bash
$ grep -rn 'EventMediaDelete' pkg/ --include="*.go" -l
pkg/audit/events.go
pkg/audit/audit.go
pkg/media/library/library_test.go
```

No `pkg/gateway/` file references it. `Library.Delete` (library.go:599-646) is wired for callers:
- `cleanupWorkspaceUploads` (rest.go:9063) — used only on intra-batch upload failure, not on user-driven delete
- the test stubs in `library_test.go:538, 558, 730, 760` are the only emitters

FR-008 says explicit delete MUST emit `media.delete`. With `TD-M9` fixing the gateway route, the new DELETE handler MUST chain the audit emit.

### TD-m5 — `refcount`/`last_refcount_seen_at` "Required on every entry" is unenforced

`contracts/components/schemas/MediaLibraryEntry.yaml:7-15` lists required:
```yaml
required:
  - id
  - workspace_id
  - filename
  - mime
  - size
  - sha256
  - uploaded_at
  - source
```
Notably absent: `refcount`, `last_refcount_seen_at`. But the description at line 78 explicitly says **"Required on every entry (FR-007a / Wave 1 TD-M2)"**. Same wording at line 82 for `last_refcount_seen_at`.

Codegen drift observed:
- Go: `Refcount *int`, `LastRefcountSeenAt *time.Time`, both `omitempty` (`openapi_types.gen.go:5559, 5565`)
- TS types: `readonly?: number`, `readonly?: string` (`openapi-types.ts:3093, 3098`)
- Zod: `.optional()` for both (`schemas.ts:2394-2395`)
- TS consumer (Media tab): `WorkspaceMediaTab.tsx` and `ComposerMediaLibrary.tsx` never read either field

If the contract intends them required, the YAML `required:` array needs to include them and the codegen needs to re-run. If the contract intends them optional, the description text needs to lose "Required on every entry (FR-007a / Wave 1 TD-M2)" wording. The current state — required-by-description, optional-by-Zod, optional-by-Go, never-read-by-SPA — is the worst of all worlds.

### TD-m6 — `Result.Mime` still a plain `string`

`pkg/media/resize/resize.go:58-62` returns `Mime string` with a doc-comment that names two values (`image/png` and `image/jpeg`). The only two producers (PNG-only at line 114, JPEG-fallback at line 125) write `Mime: "image/png"` and `Mime: "image/jpeg"`. The relationship between `Result.Mime` and `Result.Data` is data-derivable from the encoder, but the type system doesn't capture it. This was the r1 carry-over (TD-m3) and was NOT closed in the current stack.

Suggested fix: `type OutputFormat string` with constants `OutputPNG OutputFormat = "image/png"`, `OutputJPEG OutputFormat = "image/jpeg"`. `Result.Mime OutputFormat`. The two write sites become `Result{Mime: OutputPNG}` and `Result{Mime: OutputJPEG}`. Consumer code that compares `result.Mime == "image/png"` becomes `result.Mime == OutputPNG` — refactor reaches `resize.go:114, 125` and the test assertions.

### TD-m7 — `detectFileClass` returns a closed `string` enum, twice

`pkg/agent/loop_media.go:837-845` (`detectFileClass`) and `pkg/agent/loop_media.go:888-897` (`offloadNoun`) both switch on the literal `"image" | "document" | "file"`. The closure is implicit. A future addition of `"video"` requires changing both sites and rerunning UAT — refactor-protect it with a typed value. `pkg/agent/loop_media.go:882-885` (`buildOffloadGuidance`) consumes the string as a switch expression.

### TD-m8 — `GHReleasePuller` carries exported mutable fields and an infallible constructor

`pkg/providers/capabilities/puller.go:35-66` exposes 8 fields; `NewGHReleasePuller` at line 86-97 returns `*GHReleasePuller` (no error). Callers that construct via struct literal can:
- set `Owner = ""` (causes "release status: 404" via `apiURL` line 138 — but invalid input should reject at construction time)
- set `BaseURL = ""` (line 138 does `strings.TrimSuffix("", "/") + "/repos/..."` — produces malformed URL, fails on `client.Do`, but the error message is opaque)
- set `UserAgent = ""` (line 144, 179, 204, 245 — the request goes out; GitHub rejects with 403 per its docs, but the empty User-Agent is the request's fault not the response's)
- mutate mid-`Pull` — racing with another goroutine that reads the same field

Fix: build with a single argument (owner, repo, asset) — those are the immutable configuration. Make the remaining fields package-private with test-only setters. `NewGHReleasePuller` returns `(*GHReleasePuller, error)` for the empty-arg case. This was the r1+carry-over (TD-m2) and was NOT closed in the current stack.

### TD-m9 — `Refcount(id) (int, error)` loses the wrapped type

`Library.Refcount` exposes the count as `int` rather than the internal `refcount` type (`library.go:114`). This is fine for the read-only sentinel contract; the wrapped type isn't needed at the watcher. Document the choice or add a wrapped accessor.

### TD-m10 — `Library.Read`'s defensive nil check is unreachable

`library.go:466-468`:
```go
if projection.Size == nil || projection.Sha256 == nil || *projection.Size < 0 || *projection.Size > MaxFileSize {
    return nil, projection, fmt.Errorf("%w: invalid integrity fields for %s", ErrIntegrityCheckFailed, id)
}
```

The first part (`projection.Size == nil || projection.Sha256 == nil`) is unreachable because `entry.projection()` at line 220-241 unconditionally sets `Sha256` and `Size` to non-nil copies. The size-range check is already covered by `manifestEntryFromProjection` at line 888-890 on every load. Drop or replace with a single non-nil assertion.

## Re-verification of carry-over findings

| ID | r1 / r2 source | Status | Evidence |
|---|---|---|---|
| TD-C1 (r1 CRIT) | wave0 r1 type-design | ✅ CLOSED | `pkg/api/generated/llm_error_codes_test.go:200-237`; schemas updated at `pkg/gateway/inboundschemas/LLMError.yaml` and `contracts/components/schemas/LLMError.yaml`. |
| TD-M1 (r1+r2 MAJOR) | wave1 r1+r2 | ✅ CLOSED | `manifestEntry` is private, single refcount source-of-truth, `Load` validates via `newManifestEntry`. |
| TD-M2 (r1 MAJOR) | wave1 r1+r2 | ⚠️ PARTIAL | YAML `required:` array still omits `refcount` and `last_refcount_seen_at`; description text says required. **TD-m5** above. |
| TD-M3 (r1+r2 MAJOR) | wave1 r1+r2 | ⚠️ PARTIAL (type-safe closed; no mutation regression test) | `pkg/providers/capabilities/catalog_test.go` has no regression test asserting that mutating a returned `resolvedModel.InputModalities()` slice does NOT corrupt catalog state. The type-safe design forecloses the aliasing; the run-time test would catch a future "optimization" that returns the internal slice directly. |
| TD-M4 (r1+r2 MAJOR) | wave1 r1+r2 | ✅ CLOSED | `TestCatalog_Refresh_ConcurrentSerialization` (`catalog_test.go:1005-1042`) fires 32 concurrent `Refresh` calls, asserts `maxInFlight == 1` and `hits == 32`. Plus `TestCatalog_Resolve_ConcurrentRead` (line 913+). |
| TD-M5 (r1 MAJOR) | wave1 r1 | ✅ CLOSED | Two-stage parse: `seedFile` (permissive) → `Seed` (validated) via `ParseSeed` / `validate`. `Version.Compare` semver-aware. Modality typed string with `ModalityText`/`Image`/`PDF`/`Audio`/`Video` constants. `KnownModality` set is the runtime recognition boundary. |
| TD-M6 (r1+r2 MAJOR) | wave1 r1+r2 | ✅ CLOSED (type-safe closed; no test gap) | One canonical `capabilities.ResizeBudget`. Test `TestResolvedModel_AlwaysCarriesBudget` at `catalog_test.go:622-635`; `TestOptimisticModel_DefaultBudget` at 599-614. The test gap for `InputModalities` mutation (TD-M3) still stands. |
| TD-M7 (r1+r2 MAJOR) | wave1 r1+r2 | ✅ CLOSED | `classifyByHTTPStatus` returns `CodeUnknown` for residual 4xx; `outcomeFallbackEligible` accepts ONLY `CodeUnknown`. Locks at `media_outcome_retry_test.go:580-826`. |
| TD-M8 (r1+r2 MAJOR) | wave1 r1+r2 | ⚠️ PARTIAL (typed return closed; wire emission still missing) | `MediaDowngradeResult{Applied, Trigger, MediaClass}` at `media_downgrade.go:107-116`; `Trigger == TriggerOutcomeFallback` at `loop.go:6949-6951` decides relabel. **Wire-half carry-over:** `pkg/gateway/websocket.go:3353-3386` re-runs `TranslateLLMError` and ignores `ts.outcomeRelabel`. Same finding as wave-1 r2. |
| TD-m1 (r1+r2 MINOR) | wave1 r1+r2 | ✅ CLOSED | `gen.MediaLibraryEntrySource` wire enum has only `ToolOutput`/`UserUpload`; `fixtureSource` is package-private and rejected by `Upload`. |
| TD-m2 (r1+r2 MINOR) | wave1 r1+r2 | ❌ NOT CLOSED (carry-over) | Same finding as wave-1 r2. **TD-m8** above. |
| TD-m3 (r1+r2 MINOR) | wave1 r1+r2 | ❌ NOT CLOSED (carry-over) | Same finding as wave-1 r2. **TD-m6** above. |
| TD-m4 (r1+r2 MINOR) | wave1 r1+r2 | ✅ CLOSED | `Library.Load`/`Store` removed from public surface; Load is called only by `New`. |

## Read-only / required / discriminator assessment (updated)

### Fields that should be read-only or hidden
- `MediaLibraryEntry.workspace_id` (`contracts/components/schemas/MediaLibraryEntry.yaml:27`): `readOnly: true` ✓
- `MediaLibraryEntry.mime` (`…yaml:38`): `readOnly: true` ✓
- `MediaLibraryEntry.size` (`…yaml:45`): `readOnly: true` ✓
- `MediaLibraryEntry.sha256` (`…yaml:52`): `readOnly: true` ✓
- `MediaLibraryEntry.uploaded_at` (`…yaml:58`): `readOnly: true` ✓
- `MediaLibraryEntry.refcount` (`…yaml:77`): `readOnly: true` ✓ (but see TD-m5 — `required` is missing)
- `MediaLibraryEntry.last_refcount_seen_at` (`…yaml:82`): `readOnly: true` ✓ (same)
- Catalog `model` fields: private, immutable, accessor-only ✓
- Library `manifestEntry` fields: private, package-private constructor ✓
- `Library` lifecycle: `Load`/`Store` removed ✓
- `GHReleasePuller` fields: ALL exported mutable ❌ → see TD-m8
- `OutputFormat` / `Mime` for resize: still plain `string` ❌ → see TD-m6

### Optionals that should be required
- `MediaLibraryEntry.refcount`: `required` NOT set in YAML; should be set → TD-m5
- `MediaLibraryEntry.last_refcount_seen_at`: same → TD-m5
- `MediaAttachmentRequest.media_id`: required ✓ (`…yaml:7-8`)
- `ResizeBudget.LongEdgePx`/`MaxBytes`: required by `seedFile.validate` (positive) ✓
- `Seed.SchemaVersion`/`UpdatedAt`/`Source`/`Models`: required ✓
- `Model.ID`/`Provider`/`InputModalities`: required ✓

### Enum / `oneOf` decisions
- **LLM error codes:** canonical closed enum (live + replay + Zod matched) ✓
- **Modalities:** typed `Modality` with known constants + forward-compat unknown ✓
- **`MediaLibraryEntrySource`:** closed (ToolOutput / UserUpload on wire; `fixtureSource` is package-private) ✓
- **Resize output format:** still plain `string` ❌ → TD-m6 (carry-over from r1)
- **File class (image/document/file):** plain `string` ✗ → TD-m7

## Competing hypotheses and dispositions (final)

| Hypothesis | Evidence sought | Disposition |
|---|---|---|
| H1 — the workspace media REST routes are wired in the Go gateway | grep for `handleWorkspaceMediaList` / `createWorkspaceMediaAttachment` / `workspaceMedia*` in `pkg/gateway/` | **Rejected.** `HandleWorkspaces` at `pkg/gateway/rest_workspaces.go:494-545` does not dispatch these routes. Anything but `/milestones`, `/delegation`, `/instructions`, or the bare `/workspaces/{id}` produces a 404 at line 530. The contract promises the routes; the SPA calls them; the Go dispatcher rejects them. **TD-M9.** |
| H2 — `Library.Upload` and `Library.UploadFixture` share a single common body | diff the two functions | **Rejected.** They are 114-line and 102-line near-clones differing only in the source-value parameter and the prod-sources-only check. **TD-M10.** |
| H3 — the OrphanGC rollback rebuilds `manifestEntry` from a projection without re-validation | read `library.go:769-791` | **Confirmed.** A literal struct construction sidesteps `newManifestEntry`'s invariant gate; the `derefX` helpers silently coerce nil to zero. **TD-M11.** |
| H4 — `EventMediaDelete` is emitted in production code | grep for `EventMediaDelete` in `pkg/gateway/` | **Rejected.** No production call site. Only `library_test.go` references the audit constant. **TD-M12.** |
| H5 — `refcount` and `last_refcount_seen_at` are required on the wire | inspect `…/MediaLibraryEntry.yaml:7-15` for `required` | **Rejected.** Both fields are required-by-description but optional-by-YAML-required-array; codegen emits them as pointers/optionals. **TD-m5.** |
| H6 — `Resolve` returns a deep-owned `*resolvedModel` whose mutation cannot corrupt catalog state | inspect `pkg/providers/capabilities/catalog_test.go` for a mutation regression test | **Type-safe:** confirmed (`InputModalities()` returns a copy at line 156-161; `resolve()` re-allocates the slice at line 168-177). **Test gap: confirmed** (no mutation-corrupts-state test). |
| H7 — `Catalog.Refresh` serializes the whole transaction atomically | `TestCatalog_Refresh_ConcurrentSerialization` | **Confirmed.** 32 concurrent refreshes drive 32 hits, max-in-flight = 1, final state internally consistent. |
| H8 — the `result-relabel` reaches the SPA wire | grep for `outcomeRelabelCode()` callers in `pkg/gateway/` | **Rejected (wave-1 r2 carry-over).** The WS forwarder at `pkg/gateway/websocket.go:3353-3386` re-runs `TranslateLLMError` on the raw `pe` and ignores `ts.outcomeRelabel`. The wave-1 r2 finding stands. |

## Verification observed

### Reproduction

```text
$ git rev-parse HEAD
1ea79f9eb0d31b9a1501c9c21ca2069a6c391880

$ git rev-parse ae9271d0
ae9271d0ebd0017968a600a78aa349b5731c1b27

$ git rev-list --count ae9271d0..HEAD
42
```

42 commits observed; Slices A through H + Wave 2/3 wiring + UAT + Lint closures + ci-evidence.

### File evidence

| File | What was verified |
|---|---|
| `pkg/media/library/library.go` | (1) `manifestEntry` private value type with all 10 required facts as required values; (2) `refcount` private, constructed via `newRefcount`; (3) `Upload` and `UploadFixture` near-duplicate; (4) `OrphanGC` projection-rollback bypasses the invariant gate; (5) `Load` rejects `fixtureSource` set by external callers but tolerates it for backward-compat round-trip; (6) `gen.MediaLibraryEntrySource` wire enum is closed (ToolOutput + UserUpload only); (7) `Read` defensive nil-check is dead. |
| `pkg/media/library/library_test.go` | (1) `manifestEntry` invariant lock tests at line 169; (2) `Upload` and `UploadFixture` exercise the same shape; (3) `EventMediaDelete` test stubs at line 538, 558, 730, 760. |
| `pkg/media/resize/resize.go` | (1) `ResizeToFit` consumes `capabilities.ResizeBudget` (single-source-of-truth); (2) `Result.Mime` still `string` (TD-m6 carry-over); (3) PNG-first then JPEG ladder; (4) `ErrLadderFloor` is the right sentinel. |
| `pkg/providers/capabilities/catalog.go` | (1) `model` is private; `resolvedModel` accessor API; (2) `Models()` returns `ModelSnapshot{ID, Handle *resolvedModel}` deep-owned; (3) `Refresh` serializes under `refreshMu`; (4) `OptimisticModel` and `optimistic()` set `resizeBudget: c.defaultBudget` (never nil); (5) GHReleasePuller hooks at `embed.go` and `puller.go`. |
| `pkg/providers/capabilities/puller.go` | (1) `GHReleasePuller` exposes 8 mutable fields; (2) `NewGHReleasePuller` infallible; (3) verified checksum sidecar fails-soft when 404 (line 251-256). |
| `pkg/providers/capabilities/version.go` | (1) `Version.Compare` semver-aware (numeric major/minor/patch); (2) lexical fallback for ISO-date strings; (3) the v10 > v2 bug is fixed. |
| `pkg/providers/capabilities/modality.go` | (1) Typed `Modality` with known constants; (2) `KnownModality` map for runtime recognition boundary. |
| `pkg/agent/media_present.go` | (1) `workspaceRefcounter` interface for the manifest-refcount contract; (2) `sessionRefcountState` per-session dedup wrapper; (3) `sessionRefcounter.DecrementRefcount` returns `0, nil` unconditionally (interface-half-implementation smell, see TD-O3); (4) `getTurnRefcounter` caches per-session state on `AgentLoop.sessionRefcounts`; (5) `decrementSessionMediaRefcounts` runs `LoadAndDelete` then iterates; (6) `modelSupportsImage` is the optimistic step-1 capability gate. |
| `pkg/agent/loop_media.go:687-897` | (1) `offloadSink` is the private step-5 sink; (2) `detectFileClass` returns closed-enum string (TD-m7); (3) `offloadNoun` switches over the same literals; (4) `buildOffloadGuidance` composes the format-aware line. |
| `pkg/media/resolve.go` | (1) `ResolveOpts{CallerWorkspace *string}` with `WithCallerWorkspace` helper; (2) `WorkspaceLibraryResolver` interface (path-only) for transport consumers; (3) `WorkspaceLibraryProvider` is the gateway-injectable seam; (4) `IsWorkspaceRef` / `ParseWorkspaceRef` / `ValidateCallerWorkspace` are the FR-028a guards. |
| `pkg/workspace/media_delete.go` | (1) `WorkspaceDeleteHook` runs cascade-delete + emits `EventMediaCascadeDelete` audit; (2) opens fresh library instance (not from request scope). |
| `pkg/gateway/rest_workspaces.go:494-545` | (1) `HandleWorkspaces` does NOT dispatch `/workspaces/{id}/media/*` paths; (2) the 404 path at line 530 catches them; (3) `WorkspaceDeleteHook` wired at line 1242. |
| `pkg/gateway/rest.go:8726-8830` | (1) `HandleUpload` wires `workspace_id` form/query param to `library.Upload` with `gen.UserUpload`; (2) cascade-cleanup at line 9051-9068. |
| `contracts/components/schemas/MediaLibraryEntry.yaml` | (1) All server-set fields marked `readOnly: true`; (2) `required` list is missing `refcount` and `last_refcount_seen_at` (TD-m5). |
| `contracts/components/schemas/MediaAttachmentRequest.yaml` | (1) `media_id` is required and constrained to UUID; (2) `additionalProperties: false` (wire lint clean). |
| `pkg/api/generated/openapi_types.gen.go:5545-5581` | (1) Both types generated; (2) `MediaLibraryEntrySource` is a typed `string` with `Valid()` method. |
| `src/lib/api/generated/schemas.ts:2385-2399` | (1) Zod schema mirrors canonical YAML; (2) refcount/lastRefcountSeenAt optional (per TD-m5); (3) `MediaAttachmentRequest` strict. |
| `src/lib/api/generated/openapi-types.ts:3047-3106` | (1) TS types mirror canonical YAML; (2) readonly fields marked; (3) refcount/lastRefcountSeenAt optional (per TD-m5). |
| `src/lib/library-attachment.ts:1-50` | (1) `LibraryAttachment` marked `// not-wire-format:` (opt-out from wire lint); (2) `buildWorkspaceMediaRef(workspaceId, mediaId)` centralizes the canonical ref string; (3) module store + `useSyncExternalStore` hook. |
| `src/components/chat/ComposerMediaLibrary.tsx:75-100` | (1) `fetchWorkspaceMedia` + `attachWorkspaceMedia` calls hit the wire routes (which 404 — see TD-M9); (2) `useLibraryAttachments` reactive read; (3) `buildWorkspaceMediaRef` invoked on user pick. |
| `src/components/workspaces/WorkspaceMediaTab.tsx:71-78` | (1) `useQuery({queryFn: fetchWorkspaceMedia})` will resolve to error on a live gateway (TD-M9); (2) `entry.id` and `entry.mime` used directly off the wire type; (3) `entry.refcount` never accessed. |
| `src/routes/_app/workspaces.$workspaceId.media.tsx` | (1) Route is a thin wrapper over `WorkspaceMediaTab`. |
| `docs/internal/uat/ADR-051-rev4-uat-test-plan.md:75-85` | UAT-009 ("library list+delete") cannot pass against a live gateway (TD-M9); UAT-015 (sha256) is the only test that exercises the library byte path against the running binary. |
| `pkg/audit/events.go:259-277` | `EventMediaDelete` declared; not produced in production (TD-M12). |

## Bottom line

**Do not merge to main without addressing TD-M9.** The wire types `MediaLibraryEntry` and `MediaAttachmentRequest` are documented and consumed by the SPA, but the routes they ride are 404 at the gateway. This is the inverse of Constraint #8 ("contract first; generated types are the only legal cross-boundary types") — the contract describes endpoints that aren't built. TD-M9 closure requires three handlers in `pkg/gateway/`, an audit emit (`TD-M12`), and a live-gateway smoke that exercises the SPA media tab.

Once TD-M9 is closed, the remaining MAJOR + MINOR findings (TD-M10, TD-M11, TD-M12, TD-m5, TD-m6, TD-m7, TD-m8, TD-m9, TD-m10 + the wave-1 r2 carry-over on `outcomeRelabel`-on-the-wire) can ship in either this PR or a follow-up; none of them are data-loss or contract-first violations in isolation. The architectural achievements (private invariant-bearing types, single source-of-truth refcount, deep-owned `resolvedModel`, `Version.Compare` semver-aware, `WorkspaceLibraryResolver` split between bytes- and path-returning, `cleanupWorkspaceUploads` cascade-safety) should be preserved through the merge.
