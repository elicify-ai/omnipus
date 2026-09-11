// RecordFieldEditor — the inline record-field editor ADR-083 Step 5 (D8,
// §4) adds to the base view surfaces (view-kinds-design-2026-09-03 §7):
// TablePart and ListPart both mount ONE of these per cell, and it is the
// ONLY place in `viewparts/` that calls writeVaultRecord.
//
// WHAT GATES AN EDITOR AT ALL (§4.6, EMB-088) — see `viewResultData.ts`'s
// `isEditableCell`, the single source of that decision:
//   - `derived` true, or `relation` true → NO editor, ever. A derived value
//     is never written into frontmatter (ADR-068 D9/FR-046) and a relation
//     is modified through RelationWriteRequest's explicit verbs, never a
//     read-then-write splice (FR-045) — RecordWriteRequest refuses both
//     server-side regardless, but offering a control that will always fail
//     is worse than offering none.
//   - No declared `type` at all → NO editor. Absence is how "this cell
//     doesn't correspond to a declared record property" is represented; an
//     editor must never guess a type from the rendered `value`'s shape.
//   - `type` declared but not one of enum/date/text (integer, decimal,
//     checkbox) → NO editor either, today — a scope decision (no control
//     built for them yet), not a second data-integrity gate.
// On top of the cell's own metadata, a row missing `id` or `version_token`
// also gets no editor — there is nothing to address a write at, or nothing
// to compare-and-swap against (VaultFindRow's own doc comment: "an inline
// editor MUST treat an absent token the same as an ungoverned field").
//
// A record's title/path are never reachable here at all: `file.name` has no
// entry in `row.cells` (it's a synthetic field `cellValue()` special-cases),
// so `row.cells.find(...)` finds nothing for it and the caller never even
// constructs a VaultFindCell to hand this component (Founder ruling Q5).
//
// THE CONFLICT CONTRACT (§4.3, §4.4) this mirrors from
// useLibraryFileEditor.ts's already-proven shape: a stale `version_token`
// comes back as KnowledgeRecordConflictError (409), never a generic
// failure; the field then shows the SERVER's current value (re-read via
// fetchVaultRecord, never inferred) with a state stating "this changed
// while you were editing"; nothing here ever resends the refused write
// automatically; and a manual Retry reopens the editor already holding the
// FRESH token that re-read returned — never the stale one the refused
// attempt sent. `versionToken` (component state) is the ONLY source commit()
// reads a token from, and it is updated in exactly two places: a
// successful write's own response, or a conflict's follow-up read — never
// copied from a prop after the initial mount, so a token is never sent
// paired with a value this component did not itself read.
//
// WHEN THAT RE-READ ITSELF FAILS, the conflict banner is NOT shown. The
// banner's whole claim is "here is what the server holds now"; with no
// successful re-read there is no such value to show, and displaying the
// reader's own pre-edit copy underneath that sentence asserts something
// false. The failure gets its own message and clears `versionToken`, so the
// next attempt is refused with a real reason rather than looping forever on
// a token already known to be stale. Same rule for a 200 that carries no
// token: an anomaly that is stated, never a silent no-op.

import { useEffect, useState, type MouseEvent, type ReactNode } from 'react'
import { PencilSimple, SpinnerGap, WarningCircle } from '@phosphor-icons/react'
import type {
  RecordPropertyValue,
  RecordValue,
  VaultFindCell,
  VaultFindRow,
} from '@/lib/api/generated/openapi-types'
import {
  fetchVaultRecord,
  getErrorMessage,
  isKnowledgeRecordConflict,
  writeVaultRecord,
} from '@/lib/api'
import { isEditableCell, recordPropertyText, type EditableCellType } from './viewResultData'

/** One successful inline field write, reported upward. The ONLY intended
 *  wiring point is the screen that owns this view's TanStack Query cache
 *  (BasePreview) — it invalidates the per-note caches ADR-083 §4.5 names
 *  (content, outline, links) for the WRITTEN NOTE, and the view-result
 *  queries for the written COLLECTION, so every mounted embed of every view
 *  over that collection reflects the write at once (§4.5's "two embeds of
 *  the same view" requirement, and its "a different view of the same
 *  collection" sibling). This component holds no query client of its own and
 *  invalidates nothing outside its own local editing state. */
export interface RecordFieldWriteResult {
  /** WORKSPACE-relative path of the written note — what the per-note cache
   *  keys (`libraryQueryKeys.content`, outline, links) are addressed by. */
  path: string
  recordId: string
  property: string
  /** The field's new rendered value, read back from the write's own
   *  response — never the raw text the user typed, so a server-side
   *  normalisation (e.g. a declared decimal scale) is reflected honestly. */
  value: string
  /** The record's version_token AFTER this write, or `undefined` when the
   *  200 carried none. Optional BECAUSE that case is real (the server omits
   *  the field whenever its own version read came back empty) and must not
   *  be papered over: this callback fires either way, since the write landed
   *  either way and the caches downstream are stale either way. The editor
   *  itself refuses the next edit rather than reusing a stale token. */
  versionToken: string | undefined
}

/** What an inline editor needs to write through RecordWriteRequest at all —
 *  bundled the same way ViewCellLinkResolver bundles the KB-8b injection
 *  point (ViewCellLink.tsx), so ViewPartsRenderer/TablePart/ListPart thread
 *  ONE optional prop rather than several. Absent (the default) renders
 *  every cell exactly as before — plain text, never an editor. */
export interface RecordEditContext {
  workspaceId: string
  /** The record type this view queries (ViewResult.type) — REQUIRED by
   *  RecordWriteRequest.type on every write. A view with no record type
   *  (ViewResult.type absent) cannot write through this door — its cells
   *  also carry no VaultFindCell.type metadata (that metadata is derived
   *  FROM the record schema), so isEditableCell already answers false for
   *  every cell in it; this being undefined is inert, not a second gate. */
  recordType?: string | undefined
  onFieldWritten?: (result: RecordFieldWriteResult) => void
}

/** Resolved, definitely-writable target for one row/cell pair — computed
 *  once per render from props that are stable for the row's lifetime
 *  (unlike `version_token`, which the component's own state tracks
 *  separately because a write or a conflict-refresh replaces it). Returning
 *  a small object rather than a boolean lets every call site below read
 *  `target.recordId` / `target.workspaceId` / `target.recordType` without a
 *  non-null assertion — the object simply does not exist when any input is
 *  missing. */
interface EditTarget {
  workspaceId: string
  recordType: string
  recordId: string
  onFieldWritten?: ((result: RecordFieldWriteResult) => void) | undefined
}

function resolveEditTarget(
  context: RecordEditContext | undefined,
  row: VaultFindRow,
  cell: VaultFindCell,
): EditTarget | undefined {
  if (context === undefined) return undefined
  if (context.recordType === undefined) return undefined
  if (row.id === undefined) return undefined
  if (row.version_token === undefined) return undefined
  if (!isEditableCell(cell)) return undefined
  return {
    workspaceId: context.workspaceId,
    recordType: context.recordType,
    recordId: row.id,
    onFieldWritten: context.onFieldWritten,
  }
}

/** One editable cell type's value, in the shape RecordWriteRequest accepts.
 *
 *  The `default` arm is a COMPILE-TIME guard, not defensive runtime code:
 *  widening `EditableCellType` (adding `checkbox`, say — which this file's own
 *  header calls "a scope decision, not a second data-integrity gate") without
 *  adding a case here fails `npm run typecheck` on the `never` assignment.
 *  Left open, that same edit returned `undefined`, the request body became
 *  `values: [undefined]`, `RecordWriteRequestSchema.parse` threw an uncaught
 *  Zod error on the way out, and the reader saw the generic "Could not save
 *  this field" — a message about the server for a fault entirely on this
 *  side of the wire. */
function buildRecordValue(type: EditableCellType, raw: string): RecordValue {
  switch (type) {
    case 'enum':
      return { type: 'enum', enum: raw }
    case 'date':
      return { type: 'date', date: raw }
    case 'text':
      return { type: 'text', text: raw }
    default: {
      const unhandled: never = type
      throw new Error(`RecordFieldEditor: no write shape for cell type ${String(unhandled)}`)
    }
  }
}

function stop(event: MouseEvent): void {
  event.stopPropagation()
}

/**
 * One cell's inline editor, or its plain rendered value when this cell
 * cannot be edited at all — the SAME `renderValue` output either way, so a
 * relation's rendered wikilink (KB-8b) or a plain-text truncation never
 * differs between the editable and inert paths.
 */
export function EditableCell({
  context,
  row,
  cell,
  renderValue,
}: {
  context?: RecordEditContext | undefined
  row: VaultFindRow
  cell: VaultFindCell
  /** Renders one value exactly as the surface would with no editor at all. */
  renderValue: (value: string) => ReactNode
}) {
  const target = resolveEditTarget(context, row, cell)

  const [value, setValue] = useState(cell.value)
  const [versionToken, setVersionToken] = useState(row.version_token)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(cell.value)
  const [saving, setSaving] = useState(false)
  const [conflictMessage, setConflictMessage] = useState<string>()
  const [error, setError] = useState<string>()

  // The row/cell this instance was given can change identity under it (a
  // fresh view-result fetch, or a different row scrolled into view) —
  // refresh the local mirror THEN, but never while a conflict banner or an
  // open editor is on screen, which would silently discard what the user is
  // looking at mid-interaction.
  //
  // DELIBERATELY keyed on [cell.value, row.version_token] ONLY — `editing`
  // and `conflictMessage` are read inside, but must NOT be dependencies.
  // This bit the first draft: with them listed, the effect also re-ran
  // whenever EITHER flipped on its own (e.g. `editing` going true→false the
  // instant a write commits), and at that exact moment it copied the STILL-
  // STALE `cell.value`/`row.version_token` PROPS — which the parent has not
  // refetched yet — straight back over the value this component had just
  // written locally, silently reverting a successful edit on screen. Fixed
  // by relying on ordinary closure freshness instead: this effect only
  // fires on a genuine prop change, and reads whatever `editing`/
  // `conflictMessage` happen to be at THAT render — always current, never
  // stale, with no ref needed.
  useEffect(() => {
    if (editing || conflictMessage !== undefined) return
    setValue(cell.value)
    setVersionToken(row.version_token)
  }, [cell.value, row.version_token])

  if (target === undefined) return <>{renderValue(value)}</>

  async function commit(raw: string): Promise<void> {
    if (target === undefined) return
    if (versionToken === undefined) {
      setError('Could not confirm this record’s current version — reopen it and try again.')
      return
    }
    // `type` is deliberately OMITTED — RecordPropertyValue.type is optional
    // on a write ("the schema is the authority", per its own doc comment),
    // and `cell.type` here is VaultFindCell's full 8-value type (it also
    // spans `relation`/`person`/`checkbox`, none of which RecordPropertyValue
    // accepts on write), so echoing it back would be a type mismatch for
    // exactly the values isEditableCell already ruled out.
    const properties: RecordPropertyValue[] = [
      {
        property: cell.property,
        values: raw === '' ? [] : [buildRecordValue(cell.type as EditableCellType, raw)],
      },
    ]
    setSaving(true)
    setError(undefined)
    try {
      // `mode: 'update'` is STATED, not implied by carrying an `id`. The
      // contract used to infer the operation from which optionals were set,
      // so an update that lost its `id` — a bug here, a changed response
      // shape upstream — silently became a CREATE: a duplicate note, the
      // version token discarded, and a success toast. The inline editor only
      // ever edits a record that already exists, so it only ever has one
      // mode to name, and naming it is what makes the other outcome
      // impossible rather than merely unlikely.
      const written = await writeVaultRecord(target.workspaceId, {
        mode: 'update',
        type: target.recordType,
        id: target.recordId,
        version_token: versionToken,
        properties,
      })
      const newValue = recordPropertyText(written, cell.property)
      setValue(newValue)
      setDraft(newValue)
      setEditing(false)
      setConflictMessage(undefined)
      // The write LANDED — so the caches downstream are stale NOW, whether or
      // not the response carried a fresh token. `onFieldWritten` is the only
      // thing that drives that invalidation, so it fires unconditionally.
      // (It was inside the token check, whose false branch did nothing and
      // said nothing: the cell painted the new value, a second embed of the
      // same view kept the old one indefinitely, and the next edit re-sent the
      // PRE-WRITE token — producing a 409 the UI then explained as "this
      // changed while you were editing", blaming a phantom concurrent editor
      // for the server's own omission.)
      target.onFieldWritten?.({
        path: row.path,
        recordId: target.recordId,
        property: cell.property,
        value: newValue,
        versionToken: written.version_token,
      })
      if (written.version_token !== undefined) {
        setVersionToken(written.version_token)
      } else {
        // A 200 with no token is an ANOMALY, not a no-op. Dropping the token
        // makes the next edit fail commit()'s own guard above with the real
        // reason ("could not confirm this record's current version") instead
        // of silently sending a token known to be stale.
        setVersionToken(undefined)
        setError('Saved, but this record’s version could not be confirmed — reopen it before editing again.')
      }
    } catch (err) {
      if (isKnowledgeRecordConflict(err)) {
        setEditing(false)
        try {
          const fresh = await fetchVaultRecord(target.workspaceId, target.recordId)
          const freshValue = recordPropertyText(fresh, cell.property)
          setValue(freshValue)
          setDraft(freshValue)
          setConflictMessage('This changed while you were editing.')
          if (fresh.version_token !== undefined) setVersionToken(fresh.version_token)
        } catch (refreshErr) {
          // The re-read that is supposed to SHOW the reader the server's
          // current value failed. This used to be an empty catch, and the
          // result was the worst available render: the conflict banner stayed
          // up beside the reader's OWN pre-edit value, asserting it was the
          // server's, while Retry reopened the editor still holding the stale
          // token — so the next write 409'd, the refresh failed again, and the
          // loop was unbounded with no diagnostic anywhere. The banner is only
          // honest when the re-read actually succeeded, so it is set INSIDE
          // the try above and this branch states what really happened.
          setConflictMessage(undefined)
          setVersionToken(undefined)
          // Both halves, deliberately: the SITUATION the reader needs to act
          // on, then the real underlying cause. `getErrorMessage` alone
          // would render a bare "network down" — technically true, and it
          // tells the reader nothing about what just happened to their edit
          // or what to do next.
          setError(
            `This changed on the server, and the current value could not be read — reopen the record. (${getErrorMessage(refreshErr, 'the read failed')})`,
          )
        }
      } else {
        setError(getErrorMessage(err, 'Could not save this field'))
      }
    } finally {
      setSaving(false)
    }
  }

  if (conflictMessage !== undefined) {
    return (
      <span className="inline-flex flex-wrap items-center gap-1.5" data-testid="viewpart-cell-conflict">
        <span className="min-w-0 truncate">{renderValue(value)}</span>
        <span className="inline-flex items-center gap-1 text-[11px] text-[var(--color-warning)]">
          <WarningCircle size={12} weight="fill" />
          {conflictMessage}
        </span>
        <button
          tabIndex={0}
          type="button"
          onMouseDown={stop}
          onClick={(event) => {
            stop(event)
            setConflictMessage(undefined)
            setDraft(value)
            setEditing(true)
          }}
          data-testid="viewpart-cell-conflict-retry"
          className="text-[11px] font-medium text-[var(--color-accent)] underline underline-offset-2"
        >
          Retry
        </button>
      </span>
    )
  }

  if (cell.type === 'enum') {
    return (
      <span className="inline-flex items-center gap-1.5">
        <select
          tabIndex={0}
          value={value}
          disabled={saving}
          onMouseDown={stop}
          onClick={stop}
          onChange={(event) => {
            const next = event.target.value
            setDraft(next)
            void commit(next)
          }}
          data-testid="viewpart-cell-editor-enum"
          aria-label={`Edit ${cell.property}`}
          className="rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[12px] text-[var(--color-secondary)] disabled:opacity-60"
        >
          {(cell.values ?? []).map((v) => (
            <option key={v.value} value={v.value}>
              {v.label ?? v.value}
            </option>
          ))}
          {/* A stored value outside the declared set (a stale schema, a
              hand-edited note) is still shown as its own option rather than
              silently swapped for the first declared one — rendering it
              must never itself change what is stored. */}
          {value !== '' && !(cell.values ?? []).some((v) => v.value === value) && (
            <option value={value}>{value}</option>
          )}
        </select>
        {saving && <SpinnerGap size={12} className="animate-spin text-[var(--color-muted)]" />}
        {error !== undefined && (
          <span role="alert" data-testid="viewpart-cell-error" className="text-[11px] text-[var(--color-warning)]">
            {error}
          </span>
        )}
      </span>
    )
  }

  if (!editing) {
    // The error is rendered HERE as well as inside the open editor below.
    // Both cases that close the editor and then set an error — a conflict
    // whose re-read failed, and a 200 that carried no version_token — land
    // on this branch, so an error shown only in the editing branch would be
    // set and never drawn: the state exists, the reader sees an ordinary
    // cell. That is the same invisible-failure shape the error is there to
    // prevent, one level down.
    return (
      <span className="inline-flex max-w-full min-w-0 flex-wrap items-center gap-1.5">
        <button
          tabIndex={0}
          type="button"
          onMouseDown={stop}
          onClick={(event) => {
            stop(event)
            setError(undefined)
            setDraft(value)
            setEditing(true)
          }}
          data-testid="viewpart-cell-editor-trigger"
          aria-label={`Edit ${cell.property}`}
          className="group inline-flex max-w-full min-w-0 items-center gap-1 text-left"
        >
          <span className="min-w-0 truncate">{renderValue(value)}</span>
          <PencilSimple
            size={11}
            className="shrink-0 text-[var(--color-muted)] opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100"
          />
        </button>
        {error !== undefined && (
          <span role="alert" data-testid="viewpart-cell-error" className="text-[11px] text-[var(--color-warning)]">
            {error}
          </span>
        )}
      </span>
    )
  }

  return (
    <span className="inline-flex min-w-0 items-center gap-1.5" onMouseDown={stop} onClick={stop}>
      <input
        tabIndex={0}
        type={cell.type === 'date' ? 'date' : 'text'}
        value={draft}
        autoFocus
        disabled={saving}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={() => void commit(draft)}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault()
            void commit(draft)
          } else if (event.key === 'Escape') {
            event.preventDefault()
            setEditing(false)
            setDraft(value)
          }
        }}
        data-testid={`viewpart-cell-editor-${cell.type}`}
        aria-label={`Edit ${cell.property}`}
        className="min-w-0 max-w-full rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-1.5 py-0.5 text-[12px] text-[var(--color-secondary)] disabled:opacity-60"
      />
      {saving && <SpinnerGap size={12} className="animate-spin text-[var(--color-muted)]" />}
      {error !== undefined && (
        <span role="alert" data-testid="viewpart-cell-error" className="text-[11px] text-[var(--color-warning)]">
          {error}
        </span>
      )}
    </span>
  )
}
