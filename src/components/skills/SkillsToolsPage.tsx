// SkillsToolsPage — Skills & Tools: the one place for the global answer to
// "may this run, and what does the model see about it" (skills-tools-redesign
// spec). Three tabs — Skills (trust block first), Omnipus tools, External
// tools — each one list with search, two filters and collapsed groups; every
// change saves on its own through the app-level queue (C-16).

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { GearSix, PlugsConnected, Wrench, Books } from '@phosphor-icons/react'
import { fetchRegistryTools, fetchSkills, type Skill } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { usePermissionQueue } from '@/lib/permissionSaveQueue'
import { Button } from '@/components/ui/button'
import { Callout, CalloutActions, CalloutDescription } from '@/components/ui/callout'
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia } from '@/components/ui/empty'
import { PageHeader } from '@/components/ui/page-header'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { IntegrationsSection } from '@/components/settings/IntegrationsSection'
import { InstallReview } from './InstallReview'
import { NewSkillMenu } from './NewSkillMenu'
import { PermissionCatalog } from './PermissionCatalog'
import { SkillBrowser } from './SkillBrowser'
import { SkillRowActions } from './SkillRowActions'
import { TrustBlock } from './TrustBlock'
import { skillItems, toolItems } from './skillsToolsModel'
import { useSkillInstall } from './useSkillInstall'
import { useExternalServices } from './useExternalServices'
import { useSkillsTabNotices } from './useSkillsTabNotices'

const WEB_GROUP_ID = 'web & search'
const SHELL_GROUP_ID = 'shell'

/**
 * A plain hash link to a Settings section (the app uses hash history). S-65:
 * the tab links to Protection and to Sandbox & network, never to tool permissions.
 */
function SettingsLink({ tab, children }: { tab: 'protection' | 'sandbox'; children: ReactNode }) {
  return (
    <Button asChild variant="link" size="sm" className="h-auto p-0 pointer-coarse:min-h-[var(--target-touch-minimum)]">
      <a href={`#/settings?tab=${tab}`} tabIndex={0}>{children}</a>
    </Button>
  )
}

/** The spec's Partial text when the list loaded but the permissions did not (S-132). */
const PERMISSIONS_NOT_LOADED = 'Permissions could not be loaded.'

/** A tab's empty state in the kit Empty (UI Screens and States, Empty column). */
function TabEmpty({ icon, text, action }: { icon: ReactNode; text: string; action?: ReactNode }) {
  return (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon">{icon}</EmptyMedia>
        <EmptyDescription>{text}</EmptyDescription>
      </EmptyHeader>
      {action && <EmptyContent>{action}</EmptyContent>}
    </Empty>
  )
}

function loadErrorText(err: unknown): string {
  if (isApiError(err)) return err.userMessage
  return err instanceof Error ? err.message : String(err)
}

export function SkillsToolsPage() {
  const navigate = useNavigate()
  const openPage = useCallback(() => { void navigate({ to: '/skills' }) }, [navigate])
  const queue = usePermissionQueue(openPage)
  const skillsQuery = useQuery({ queryKey: ['skills'], queryFn: fetchSkills })
  const toolsQuery = useQuery({ queryKey: ['registry-tools'], queryFn: fetchRegistryTools })
  const [tab, setTab] = useState('skills')
  const [announcement, setAnnouncement] = useState('')
  const [browseOpen, setBrowseOpen] = useState(false)
  const [webSettingsOpen, setWebSettingsOpen] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const skills = useMemo(() => skillItems(skillsQuery.data ?? []), [skillsQuery.data])
  const builtinTools = useMemo(() => toolItems((toolsQuery.data ?? []).filter((tool) => tool.source !== 'mcp')), [toolsQuery.data])
  const externalTools = useMemo(() => toolItems((toolsQuery.data ?? []).filter((tool) => tool.source === 'mcp')), [toolsQuery.data])
  const currentRevision = useCallback(
    (name: string) => (skillsQuery.data ?? []).find((skill: Skill) => skill.id === name || skill.name === name)?.revision,
    [skillsQuery.data],
  )
  const install = useSkillInstall(currentRevision)

  const listFailures = [
    skillsQuery.isError ? skillsQuery.error : undefined,
    toolsQuery.isError ? toolsQuery.error : undefined,
  ].filter((err) => err !== undefined)
  const permissionsFailed = !queue.stored && queue.loadError !== undefined
  const failures = permissionsFailed ? [...listFailures, queue.loadError] : listFailures
  // A failed list names its cause; when only the permissions failed, the
  // rows still show read-only and the page says so (UI states, Partial).
  const failureText = listFailures.length > 0 ? loadErrorText(listFailures[0]) : PERMISSIONS_NOT_LOADED
  const retryLoads = () => {
    if (skillsQuery.isError) void skillsQuery.refetch()
    if (toolsQuery.isError) void toolsQuery.refetch()
    if (!queue.stored) void queue.load()
  }
  const disabled = failures.length > 0 || !queue.stored || skillsQuery.isPending || toolsQuery.isPending
  const trust = queue.valueOf('skill_trust')
  const services = useExternalServices(disabled)
  const notices = useSkillsTabNotices(queue)
  // A service added, removed or reconnected may store its own key (C-7):
  // read the stored permissions again once the service list changes.
  const serviceIds = (services.serversQuery.data ?? []).map((server) => server.id).sort().join('|')
  const seenServices = useRef<string | null>(null)
  useEffect(() => {
    if (!services.serversQuery.isSuccess) return
    if (seenServices.current !== null && seenServices.current !== serviceIds) void queue.load()
    seenServices.current = serviceIds
  }, [serviceIds, services.serversQuery.isSuccess, queue])
  const skillByKey = useMemo(
    () => new Map((skillsQuery.data ?? []).map((skill) => [skill.id.toLowerCase(), skill])),
    [skillsQuery.data],
  )

  return (
    <div className="flex h-full min-h-0 flex-col gap-[var(--space-3)] overflow-y-auto p-[var(--space-4)]">
      <PageHeader
        title="Skills & Tools"
        description="What agents may run, and what the model sees about each tool and skill."
        primaryAction={(
          <NewSkillMenu
            onUpload={() => {
              const input = fileInput.current
              if (!input) return
              // Cleared before the chooser opens, so the same file can be
              // picked again; never cleared inside its own change event.
              input.value = ''
              input.click()
            }}
            onBrowse={() => setBrowseOpen(true)}
          />
        )}
      />
      <input
        ref={fileInput}
        type="file"
        accept=".md,.zip"
        aria-label="Skill file to upload"
        className="hidden"
        onChange={(event) => {
          const file = event.target.files?.[0]
          if (file) void install.upload(file)
        }}
      />
      {install.cancelNotice && (
        <Callout variant="info"><CalloutDescription>{install.cancelNotice}</CalloutDescription></Callout>
      )}
      {install.stageError && (
        <Callout variant="error"><CalloutDescription>{install.stageError}</CalloutDescription></Callout>
      )}
      {install.staging && (
        <section aria-label="Checking the skill" className="flex flex-wrap items-center gap-[var(--space-2)] rounded-md border border-[var(--color-border)] p-[var(--space-3)]">
          <span className="text-[length:var(--type-caption-size)]">Checking the skill…</span>
          <Button size="sm" variant="ghost" onClick={install.cancelStaging}>Cancel</Button>
        </section>
      )}
      {install.review && (
        <InstallReview
          key={install.review.review_id}
          review={{
            reviewId: install.review.review_id,
            skillName: install.review.name,
            version: install.review.version,
            category: install.review.suggested_category ?? null,
            verified: install.review.verified,
            verification: install.review.verification,
            trust: trust === 'allow' || trust === 'deny' ? trust : 'ask',
          }}
          outcome={install.outcome}
          installing={install.busy}
          onInstall={(category) => void install.install(category)}
          onRetry={install.retry}
          onCancel={() => void install.cancel()}
          cancelling={install.cancelling}
          onClose={install.close}
        />
      )}
      {failures.length > 0 && (
        <Callout variant="error">
          <CalloutDescription>{failureText}</CalloutDescription>
          <CalloutActions>
            <Button size="sm" variant="outline" onClick={retryLoads}>Retry</Button>
          </CalloutActions>
        </Callout>
      )}
      {/* Radix switches tabs on pointer-down and focus; onClick also
          switches on a plain click event (assistive tech, scripted clicks). */}
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="skills" onClick={() => setTab('skills')}>
            Skills
            <span className="ml-[var(--space-1)] text-[var(--color-muted)]">{skillsQuery.isPending ? '…' : skills.length}</span>
          </TabsTrigger>
          <TabsTrigger value="tools" onClick={() => setTab('tools')}>
            Omnipus tools
            {toolsQuery.isSuccess && <span className="ml-[var(--space-1)] text-[var(--color-muted)]">{builtinTools.length}</span>}
          </TabsTrigger>
          <TabsTrigger value="external" onClick={() => setTab('external')}>
            External tools (MCP)
            {toolsQuery.isSuccess && <span className="ml-[var(--space-1)] text-[var(--color-muted)]">{externalTools.length}</span>}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="skills">
          <PermissionCatalog
            items={skills}
            queue={queue}
            noun="skills"
            testIdPrefix="skill"
            loading={skillsQuery.isPending}
            disabled={disabled}
            announce={setAnnouncement}
            empty={skillsQuery.isSuccess && (
              <TabEmpty
                icon={<Books size={20} />}
                text="No skills installed"
                action={<Button size="sm" variant="outline" onClick={() => setBrowseOpen(true)}>Browse the catalog</Button>}
              />
            )}
            rowActions={(item) => {
              const skill = skillByKey.get(item.key)
              return skill
                ? <SkillRowActions skill={skill} skills={skillsQuery.data ?? []} trust={typeof trust === 'string' ? trust : undefined} disabled={disabled} />
                : null
            }}
          >
            {notices}
            <TrustBlock queue={queue} disabled={disabled} />
          </PermissionCatalog>
        </TabsContent>
        <TabsContent value="tools">
          <p className="mb-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            <SettingsLink tab="protection">Open Settings</SettingsLink>
            {' to change Auto-approve and God Mode.'}
          </p>
          <PermissionCatalog
            items={builtinTools}
            queue={queue}
            noun="tools"
            testIdPrefix="tool"
            loading={toolsQuery.isPending}
            disabled={disabled}
            announce={setAnnouncement}
            empty={toolsQuery.isSuccess && (
              <TabEmpty
                icon={<Wrench size={20} />}
                text="No Omnipus tools reported"
                action={<Button size="sm" variant="outline" onClick={() => void toolsQuery.refetch()}>Retry</Button>}
              />
            )}
            groupActions={(groupId) => groupId === SHELL_GROUP_ID ? (
              <SettingsLink tab="sandbox">Shell safety rules (Settings)</SettingsLink>
            ) : groupId === WEB_GROUP_ID && (
              <Button size="sm" variant="ghost" className="h-7 gap-[var(--space-1)] pointer-coarse:min-h-[var(--target-touch-minimum)]" onClick={() => setWebSettingsOpen(true)}>
                <GearSix size={14} aria-hidden="true" />
                Settings
              </Button>
            )}
          />
        </TabsContent>
        <TabsContent value="external">
          <PermissionCatalog
            items={externalTools}
            queue={queue}
            noun="external tools"
            testIdPrefix="tool"
            loading={toolsQuery.isPending || services.serversQuery.isPending}
            loadingText="Loading services…"
            empty={services.serversQuery.isSuccess && (
              // Add service stays in the toolbar above, so the empty state draws no second one.
              <TabEmpty icon={<PlugsConnected size={20} />} text="No external services yet" />
            )}
            disabled={disabled}
            announce={setAnnouncement}
            toolbarAction={services.addButton}
            extraGroups={services.extraGroups}
            groupControls={services.controls}
            groupNotice={services.notice}
          >
            {services.serversQuery.isError && (
              <Callout variant="error">
                <CalloutDescription>{`Could not load the services: ${loadErrorText(services.serversQuery.error).replace(/\.+$/, '')}.`}</CalloutDescription>
                <CalloutActions>
                  <Button size="sm" variant="outline" onClick={() => void services.serversQuery.refetch()}>Retry</Button>
                </CalloutActions>
              </Callout>
            )}
          </PermissionCatalog>
        </TabsContent>
      </Tabs>
      <div role="status" aria-live="polite" className="sr-only">{announcement}</div>
      {browseOpen && <SkillBrowser open onOpenChange={setBrowseOpen} />}
      {services.dialogs}
      <Sheet open={webSettingsOpen} onOpenChange={setWebSettingsOpen}>
        <SheetContent className="overflow-y-auto">
          <SheetHeader>
            <SheetTitle>Web & Search</SheetTitle>
            <SheetDescription>The search services agents use for web search.</SheetDescription>
          </SheetHeader>
          {webSettingsOpen && <IntegrationsSection part="search" />}
        </SheetContent>
      </Sheet>
    </div>
  )
}
