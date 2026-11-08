// ThirdPartyAgentsSection — Settings → Third-party agents (T11 slice B, spec
// §4; FR-6, FR-7). One row per external CLI (Claude Code, Codex, opencode):
// the program path a person saves for it, pre-filled from detection, and the
// standard parameters added to every run of that CLI. One Save replaces the
// whole section (PUT /settings/third-party-agents); the server validates
// every row before writing and refuses the first bad one, so nothing is
// saved on a refusal. Check CLI is optional, runs the typed path through
// POST /system/cli-validate and saves nothing.
//
// Wire types come from the generated T8 contract only
// (ThirdPartyAgentSettings / ThirdPartyAgentSettingsView). Detection arrives
// inside the view (`detected`, already keyed by CLI), so this screen never
// calls /system/cli-detect and never maps detection keys itself.

import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Card } from '@/components/ui/card'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { QueryErrorState } from '@/components/ui/query-error-state'
import {
  fetchCliValidate,
  getErrorMessage,
  getThirdPartyAgentSettings,
  isApiError,
  updateThirdPartyAgentSettings,
} from '@/lib/api'
import { parseServerErrorField } from '@/lib/api-error'
import type {
  CliPreset,
  CliValidateResponse,
  ThirdPartyAgentSettings,
  ThirdPartyAgentSettingsView,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'

type CliKey = keyof ThirdPartyAgentSettingsView['clis']
type PresetField = keyof CliPreset

const CLI_ROWS: ReadonlyArray<{ cli: CliKey; label: string }> = [
  { cli: 'claude-code', label: 'Claude Code' },
  { cli: 'codex', label: 'Codex' },
  { cli: 'opencode', label: 'opencode' },
]

export const THIRD_PARTY_AGENTS_QUERY_KEY = ['settings', 'third-party-agents'] as const

// Only the fields a person has edited since the last load or save. An
// untouched field shows the saved value (or the detection pre-fill when
// nothing is saved), so a background refetch can update it; a touched field
// keeps exactly what was typed, including an emptied one. Pre-fill
// eligibility is tracked separately (touchedPaths below): a save empties this
// buffer, but a path field a person has edited stays touched, so a cleared
// path is not pre-filled again on the next whole-section save (spec §4).
type Drafts = Partial<Record<CliKey, Partial<CliPreset>>> // not-wire-format: SPA-local edit buffer keyed by the generated CLI keys

// A save refusal the server attributed to one row and field
// (ErrorResponse.field = "clis.<cli>.path" | "clis.<cli>.preset_args").
type RowRefusal = { cli: CliKey; field: PresetField; message: string } // not-wire-format: SPA-local view state parsed from the generated ErrorResponse

const REFUSED_FIELD = /^clis\.(claude-code|codex|opencode)\.(path|preset_args)$/

function rowRefusalFrom(err: unknown): RowRefusal | null {
  if (!isApiError(err) || err.status !== 400 || !err.field) return null
  const match = REFUSED_FIELD.exec(err.field)
  if (!match) return null
  return { cli: match[1] as CliKey, field: match[2] as PresetField, message: err.userMessage }
}

function saveFailureMessage(err: unknown): string {
  if (isApiError(err) && err.status === 401) {
    return 'Your session has ended. Sign in again, then save; your changes are still here.'
  }
  // The write reached disk, so "Could not save" would be false; the server's
  // text says what happened and what to do. The client replaces every 5xx
  // text with a generic one in userMessage, so read the sentence from the body.
  if (isApiError(err) && err.code === 'saved_not_refreshed') {
    return parseServerErrorField(err.body) ?? err.userMessage
  }
  return `Could not save: ${getErrorMessage(err, 'unknown error')}`
}

function draftsNotIn(drafts: Drafts, sent: ThirdPartyAgentSettings): Drafts {
  const remaining: Drafts = {}
  for (const { cli } of CLI_ROWS) {
    const draft = drafts[cli]
    if (!draft) continue
    const kept: Partial<CliPreset> = {}
    if (draft.path !== undefined && draft.path !== sent.clis[cli]?.path) kept.path = draft.path
    if (draft.preset_args !== undefined && draft.preset_args !== sent.clis[cli]?.preset_args) kept.preset_args = draft.preset_args
    if (kept.path !== undefined || kept.preset_args !== undefined) remaining[cli] = kept
  }
  return remaining
}

function detectedPrefill(row: ThirdPartyAgentSettingsView['clis'][CliKey]): string {
  const found = row.detected
  if (!found.installed) return ''
  // The contract says to pre-fill from link_path (the path as found, before
  // symlink resolution) and fall back to the resolved path.
  return found.link_path ?? found.path ?? ''
}

function checkResultToast(label: string, result: CliValidateResponse): { message: string; variant: 'success' | 'warning' } {
  const detail = result.detail ?? result.reason
  switch (result.reason) {
    case 'ok':
      return { message: `${label} CLI works: version ${result.version ?? 'unknown'}.`, variant: 'success' }
    case 'unauthenticated':
      return {
        message: `${label} CLI runs (version ${result.version ?? 'unknown'}) but is not signed in: ${detail}.`,
        variant: 'warning',
      }
    default:
      return { message: `${label} CLI check: ${detail}. Nothing was saved.`, variant: 'warning' }
  }
}

function LoadingRows() {
  return (
    <div data-testid="third-party-agents-loading" aria-busy="true" className="space-y-[var(--space-2-5)]">
      {CLI_ROWS.map(({ cli }) => (
        <Card key={cli} className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
          <Skeleton className="h-4 w-32" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </Card>
      ))}
    </div>
  )
}

export function ThirdPartyAgentsSection() {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [drafts, setDrafts] = useState<Drafts>({})
  // Path fields a person has edited while this section is open. Detection
  // pre-fills only an untouched field (spec §4); unlike `drafts`, a successful
  // save does not reset this, so an intentionally cleared path stays empty.
  const [touchedPaths, setTouchedPaths] = useState<ReadonlySet<CliKey>>(() => new Set())
  const [refusal, setRefusal] = useState<RowRefusal | null>(null)
  const [saveFailure, setSaveFailure] = useState<string | null>(null)
  const [checkingCli, setCheckingCli] = useState<CliKey | null>(null)
  const inputRefs = useRef(new Map<string, HTMLInputElement>())

  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: THIRD_PARTY_AGENTS_QUERY_KEY,
    queryFn: getThirdPartyAgentSettings,
  })

  // When a save is refused, move focus to the refused field (spec §4).
  useEffect(() => {
    if (!refusal) return
    inputRefs.current.get(`${refusal.cli}.${refusal.field}`)?.focus()
  }, [refusal])

  function valueOf(view: ThirdPartyAgentSettingsView, cli: CliKey, field: PresetField): string {
    const draft = drafts[cli]?.[field]
    if (draft !== undefined) return draft
    const saved = view.clis[cli]
    if (field === 'path') {
      if (saved.path !== '') return saved.path
      return touchedPaths.has(cli) ? '' : detectedPrefill(saved)
    }
    return saved.preset_args
  }

  function edit(cli: CliKey, field: PresetField, value: string) {
    setDrafts((prev) => ({ ...prev, [cli]: { ...prev[cli], [field]: value } }))
    if (field === 'path') setTouchedPaths((prev) => (prev.has(cli) ? prev : new Set(prev).add(cli)))
    if (refusal?.cli === cli && refusal.field === field) setRefusal(null)
  }

  const save = useMutation({
    mutationFn: (body: ThirdPartyAgentSettings) => updateThirdPartyAgentSettings(body),
    onMutate: () => {
      setRefusal(null)
      setSaveFailure(null)
    },
    onSuccess: (saved, sent) => {
      queryClient.setQueryData(THIRD_PARTY_AGENTS_QUERY_KEY, saved)
      // Every external-CLI agent's read view reports the effective program,
      // standard parameters and program_warning from these settings, and a
      // settings change does not touch the agent's updated_at. Refetch the
      // agent detail and list queries so Runtime shows the new values (FR-8).
      void queryClient.invalidateQueries({ queryKey: ['agent'] })
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      // Drop only the drafts this save carried; anything typed while the
      // request was in flight stays as an unsaved draft.
      setDrafts((prev) => draftsNotIn(prev, sent))
      addToast({ message: 'Saved third-party agent settings.', variant: 'success' })
    },
    onError: (err) => {
      // The draft stays in place on every failure: drafts are only cleared
      // on success, and untouched fields still show the same derived value.
      const rowRefused = rowRefusalFrom(err)
      if (rowRefused) setRefusal(rowRefused)
      else setSaveFailure(saveFailureMessage(err))
    },
  })

  async function checkCli(cli: CliKey, label: string, path: string) {
    setCheckingCli(cli)
    try {
      const result = await fetchCliValidate(cli, path)
      addToast(checkResultToast(label, result))
    } catch (err) {
      addToast({ message: `Could not check the ${label} CLI: ${getErrorMessage(err, 'unknown error')}`, variant: 'error' })
    } finally {
      setCheckingCli((current) => (current === cli ? null : current))
    }
  }

  if (isPending) return <LoadingRows />

  if (isError) {
    return (
      <QueryErrorState
        layout="fill"
        testId="third-party-agents-error"
        message={`Could not load third-party agent settings: ${getErrorMessage(error, 'unknown error')}`}
        onRetry={() => void refetch()}
      />
    )
  }

  const view = data
  const body: ThirdPartyAgentSettings = {
    clis: {
      'claude-code': { path: valueOf(view, 'claude-code', 'path'), preset_args: valueOf(view, 'claude-code', 'preset_args') },
      codex: { path: valueOf(view, 'codex', 'path'), preset_args: valueOf(view, 'codex', 'preset_args') },
      opencode: { path: valueOf(view, 'opencode', 'path'), preset_args: valueOf(view, 'opencode', 'preset_args') },
    },
  }

  return (
    <section className="space-y-[var(--space-2-5)]" data-testid="third-party-agents-section">
      <div className="space-y-[var(--space-1)]">
        <h3 className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">
          Third-party agents
        </h3>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
          The program each third-party CLI runs, and the standard parameters Omnipus adds to every run of that CLI,
          after the agent&apos;s own flags. Only a person can set these here; agents cannot change them. Each program
          path is checked when you save.
        </p>
      </div>

      {CLI_ROWS.map(({ cli, label }) => {
        const pathValue = body.clis[cli]?.path ?? ''
        const found = detectedPrefill(view.clis[cli])
        const pathError = refusal?.cli === cli && refusal.field === 'path' ? refusal.message : undefined
        const paramsError = refusal?.cli === cli && refusal.field === 'preset_args' ? refusal.message : undefined
        return (
          <Card key={cli} data-testid={`third-party-agent-row-${cli}`} className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
            <p className="font-medium text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">{label}</p>
            <div className="space-y-[var(--space-1)]">
              <Field label={`${label} program path`} error={pathError}>
                <Input
                  ref={(node) => {
                    if (node) inputRefs.current.set(`${cli}.path`, node)
                    else inputRefs.current.delete(`${cli}.path`)
                  }}
                  value={pathValue}
                  onChange={(e) => edit(cli, 'path', e.target.value)}
                  placeholder="Not set"
                  spellCheck={false}
                  autoComplete="off"
                  className="font-mono text-[length:var(--type-utility-xs-size)]"
                />
              </Field>
              {found !== '' && (
                <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] break-all">
                  Found at {found}
                </p>
              )}
            </div>
            <Field label={`${label} parameters`} error={paramsError}>
              <Input
                ref={(node) => {
                  if (node) inputRefs.current.set(`${cli}.preset_args`, node)
                  else inputRefs.current.delete(`${cli}.preset_args`)
                }}
                value={body.clis[cli]?.preset_args ?? ''}
                onChange={(e) => edit(cli, 'preset_args', e.target.value)}
                placeholder="None"
                spellCheck={false}
                autoComplete="off"
                className="font-mono text-[length:var(--type-utility-xs-size)]"
              />
            </Field>
            <Button
              variant="outline"
              size="sm"
              aria-label={`Check ${label} CLI`}
              actionState={checkingCli === cli ? 'pending' : 'idle'}
              onClick={() => void checkCli(cli, label, pathValue)}
            >
              Check CLI
            </Button>
          </Card>
        )
      })}

      <div className="flex flex-col items-end gap-[var(--space-1)]">
        <Button actionState={save.isPending ? 'pending' : 'idle'} onClick={() => save.mutate(body)}>
          Save
        </Button>
        {/* FormError is not a public catalogue export; this mirrors its
            role="alert" error-line contract with the same tokens. */}
        {saveFailure && (
          <p
            role="alert"
            data-testid="third-party-agents-save-error"
            className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]"
          >
            {saveFailure}
          </p>
        )}
      </div>
    </section>
  )
}
