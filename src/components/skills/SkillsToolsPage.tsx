// SkillsToolsPage — Skills & Tools: the one place for the global answer to
// "may this run, and what does the model see about it" (skills-tools-redesign
// spec). Three tabs — Skills (trust block first), Omnipus tools, External
// tools — each one list with search, two filters and collapsed groups; every
// change saves on its own through the app-level queue (C-16).

import { useCallback, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { GearSix } from '@phosphor-icons/react'
import { fetchRegistryTools, fetchSkills, type Skill } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { usePermissionQueue } from '@/lib/permissionSaveQueue'
import { Button } from '@/components/ui/button'
import { Callout, CalloutActions, CalloutDescription } from '@/components/ui/callout'
import { PageHeader } from '@/components/ui/page-header'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { IntegrationsSection } from '@/components/settings/IntegrationsSection'
import { InstallReview } from './InstallReview'
import { NewSkillMenu } from './NewSkillMenu'
import { PermissionCatalog } from './PermissionCatalog'
import { SkillBrowser } from './SkillBrowser'
import { TrustBlock } from './TrustBlock'
import { skillItems, toolItems } from './skillsToolsModel'
import { useSkillInstall } from './useSkillInstall'

const WEB_GROUP_ID = 'web & search'

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

  const failures = [
    skillsQuery.isError ? skillsQuery.error : undefined,
    toolsQuery.isError ? toolsQuery.error : undefined,
    queue.stored ? undefined : queue.loadError,
  ].filter((err) => err !== undefined)
  const retryLoads = () => {
    if (skillsQuery.isError) void skillsQuery.refetch()
    if (toolsQuery.isError) void toolsQuery.refetch()
    if (!queue.stored) void queue.load()
  }
  const disabled = failures.length > 0 || !queue.stored || skillsQuery.isPending || toolsQuery.isPending
  const trust = queue.valueOf('skill_trust')

  return (
    <div className="flex h-full min-h-0 flex-col gap-[var(--space-3)] overflow-y-auto p-[var(--space-4)]">
      <PageHeader
        title="Skills & Tools"
        description="What agents may run, and what the model sees about each tool and skill."
        primaryAction={(
          <NewSkillMenu onUpload={() => fileInput.current?.click()} onBrowse={() => setBrowseOpen(true)} />
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
          event.target.value = ''
          if (file) void install.upload(file)
        }}
      />
      {install.stageError && (
        <Callout variant="error"><CalloutDescription>{install.stageError}</CalloutDescription></Callout>
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
            trust: trust === 'allow' || trust === 'deny' ? trust : 'ask',
          }}
          outcome={install.outcome}
          installing={install.busy}
          onInstall={(category) => void install.install(category)}
          onRetry={install.retry}
          onClose={install.close}
        />
      )}
      {failures.length > 0 && (
        <Callout variant="error">
          <CalloutDescription>{loadErrorText(failures[0])}</CalloutDescription>
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
            External tools
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
          >
            <TrustBlock queue={queue} disabled={disabled} />
          </PermissionCatalog>
        </TabsContent>
        <TabsContent value="tools">
          <PermissionCatalog
            items={builtinTools}
            queue={queue}
            noun="tools"
            testIdPrefix="tool"
            loading={toolsQuery.isPending}
            disabled={disabled}
            announce={setAnnouncement}
            groupActions={(groupId) => groupId === WEB_GROUP_ID && (
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
            loading={toolsQuery.isPending}
            disabled={disabled}
            announce={setAnnouncement}
          />
        </TabsContent>
      </Tabs>
      <div role="status" aria-live="polite" className="sr-only">{announcement}</div>
      {browseOpen && <SkillBrowser open onOpenChange={setBrowseOpen} />}
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
