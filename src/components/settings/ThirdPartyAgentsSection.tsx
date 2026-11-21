// ThirdPartyAgentsSection — Settings → Third-party agents (T11 slice B, spec
// §4; FR-6, FR-7). One row per external CLI (Claude Code, Codex, opencode):
// the program path a person saves for it, pre-filled from detection, and the
// standard parameters added to every run of that CLI. One Save replaces the
// whole section (PUT /settings/third-party-agents); the server validates
// every row before writing and refuses the first bad one, so nothing is
// saved on a refusal. Check CLI is optional, runs the typed path through
// POST /system/cli-validate and saves nothing.
//
// Settings redesign #1519 (FR-022): the section is on the shared Save bar. A
// Save still sends all three rows in one PUT; after a 200 the rows show the
// answer, after saved_not_refreshed they show what was sent (never a re-read).
//
// Wire types come from the generated T8 contract only
// (ThirdPartyAgentSettings / ThirdPartyAgentSettingsView). Detection arrives
// inside the view (`detected`, already keyed by CLI), so this screen never
// calls /system/cli-detect and never maps detection keys itself.

import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
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
  updateThirdPartyAgentSettings,
} from '@/lib/api'
import type {
  CliPreset,
  CliValidateResponse,
  ThirdPartyAgentSettings,
  ThirdPartyAgentSettingsView,
} from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { SaveBarPanel, SaveOutcomeLine } from './SaveBar'
import { toWriteResponse, checkFormPart, type PartCheck } from './saveOutcome'
import { useFormSaveBar, type FormSavePart } from './useFormSaveBar'

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
  // Rows a Save stored but the gateway did not take into use (FR-022).
  const [knownSaved, setKnownSaved] = useState<ThirdPartyAgentSettings | null>(null)
  const sentRef = useRef<ThirdPartyAgentSettings | null>(null)
  // The saved rows when the last Save was sent (the C-6 "before" values).
  const sentBeforeRef = useRef<ThirdPartyAgentSettings | null>(null)
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
    return shownOf(view, cli, field)
  }

  function edit(cli: CliKey, field: PresetField, value: string) {
    setDrafts((prev) => ({ ...prev, [cli]: { ...prev[cli], [field]: value } }))
    if (field === 'path') setTouchedPaths((prev) => (prev.has(cli) ? prev : new Set(prev).add(cli)))
    if (refusal?.cli === cli && refusal.field === field) setRefusal(null)
  }

  // The rows as shown without this tab's drafts: saved values, or a detected
  // program path offered as a pre-fill.
  function shownOf(view: ThirdPartyAgentSettingsView, cli: CliKey, field: PresetField): string {
    const saved = knownSaved?.clis[cli] ?? view.clis[cli]
    if (field === 'path') {
      if (saved.path !== '') return saved.path
      return touchedPaths.has(cli) ? '' : detectedPrefill(view.clis[cli])
    }
    return saved.preset_args
  }

  const changedCount = data
    ? CLI_ROWS.reduce(
        (n, { cli }) =>
          n +
          (['path', 'preset_args'] as const).filter((f) => drafts[cli]?.[f] !== undefined && drafts[cli]![f] !== shownOf(data, cli, f)).length,
        0,
      )
    : 0

  // FR-022: one PUT carrying all three rows, built from the values loaded
  // when the section opened plus this tab's drafts.
  const fullBody = (): ThirdPartyAgentSettings | null =>
    data
      ? {
          clis: {
            'claude-code': { path: valueOf(data, 'claude-code', 'path'), preset_args: valueOf(data, 'claude-code', 'preset_args') },
            codex: { path: valueOf(data, 'codex', 'path'), preset_args: valueOf(data, 'codex', 'preset_args') },
            opencode: { path: valueOf(data, 'opencode', 'path'), preset_args: valueOf(data, 'opencode', 'preset_args') },
          },
        }
      : null

  const part: FormSavePart = {
    id: 'third-party-agents',
    count: changedCount,
    invalidCount: 0,
    write: async () => {
      const body = fullBody()
      sentRef.current = body
      sentBeforeRef.current = data
        ? {
            clis: Object.fromEntries(
              CLI_ROWS.map(({ cli }) => {
                const saved = knownSaved?.clis[cli] ?? data.clis[cli]
                return [cli, { path: saved.path, preset_args: saved.preset_args }]
              }),
            ) as ThirdPartyAgentSettings['clis'],
          }
        : null
      setRefusal(null)
      if (!body) return { ok: false, status: 400, json: async () => ({ error: 'The third-party agent settings are not loaded.' }) }
      return toWriteResponse(() => updateThirdPartyAgentSettings(body))
    },
    onOutcome: (outcome) => {
      const sent = sentRef.current
      if (!sent) return
      if (outcome.kind === 'saved') {
        // After a 200 the section shows the response body (FR-022).
        const resp = outcome.body as ThirdPartyAgentSettingsView | undefined
        if (resp && resp.clis) {
          queryClient.setQueryData(THIRD_PARTY_AGENTS_QUERY_KEY, resp)
          setKnownSaved(null)
        } else {
          // Saved per the saved-state check, with no body: the sent rows are
          // the saved rows known to the screen (FR-012).
          setKnownSaved(sent)
          void queryClient.invalidateQueries({ queryKey: THIRD_PARTY_AGENTS_QUERY_KEY })
        }
        setDrafts((prev) => draftsNotIn(prev, sent))
        void queryClient.invalidateQueries({ queryKey: ['agent'] })
        void queryClient.invalidateQueries({ queryKey: ['agents'] })
      } else if (outcome.kind === 'saved-not-in-use') {
        // Stored but not in use: show the rows it sent as saved, never a re-read.
        setKnownSaved(sent)
        setDrafts((prev) => draftsNotIn(prev, sent))
      } else if (outcome.kind === 'not-saved') {
        const match = outcome.field ? REFUSED_FIELD.exec(outcome.field) : null
        if (match) setRefusal({ cli: match[1] as CliKey, field: match[2] as PresetField, message: outcome.message })
      }
    },
    // C-6 saved-state check (FR-020 outcome 4, FR-022): GET
    // /config/pending-restart names each saved-but-not-applied value as
    // sandbox.third_party_agents.clis.<cli>.<field>; any other value is read
    // from GET /settings/third-party-agents.
    check: async (): Promise<PartCheck> => {
      const sent = sentRef.current
      const before = sentBeforeRef.current
      if (!sent || !before) return { kind: 'unknown' }
      const configKey = (cli: CliKey, field: PresetField) => `sandbox.third_party_agents.clis.${cli}.${field}`
      const items = CLI_ROWS.flatMap(({ cli, label }) =>
        (['path', 'preset_args'] as const).map((field) => ({
          key: configKey(cli, field),
          label: `${label} ${field === 'path' ? 'program path' : 'standard parameters'}`,
          draft: sent.clis[cli]?.[field] ?? '',
          before: before.clis[cli]?.[field] ?? '',
          cli,
          field,
        })),
      )
      let fresh: ThirdPartyAgentSettingsView | null = null
      const result = await checkFormPart(items, async (key) => {
        fresh ??= await getThirdPartyAgentSettings()
        const item = items.find((i) => i.key === key)!
        return fresh.clis[item.cli]?.[item.field] ?? ''
      })
      if (result.kind === 'changed-elsewhere') {
        // The read values become the saved rows; this tab's drafts stay.
        const next: ThirdPartyAgentSettings = {
          clis: Object.fromEntries(CLI_ROWS.map(({ cli }) => [cli, { ...before.clis[cli] }])) as ThirdPartyAgentSettings['clis'],
        }
        for (const item of items) {
          const row = next.clis[item.cli]
          if (row && item.key in result.saved) row[item.field] = String(result.saved[item.key] ?? '')
        }
        setKnownSaved(next)
      }
      return result
    },
    discard: () => {
      setDrafts({})
      setTouchedPaths(new Set())
      setRefusal(null)
    },
  }
  const { bar, message } = useFormSaveBar([part])

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

      <SaveOutcomeLine message={message('third-party-agents')} />
      <SaveBarPanel {...bar} />
    </section>
  )
}
