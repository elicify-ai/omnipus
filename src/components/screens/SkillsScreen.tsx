import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  PuzzlePiece,
  HardDrives,
  Wrench,
  Shield,
  Trash,
  Plus,
  MagnifyingGlass,
  CaretDown,
  CaretUp,
  ArrowRight,
  CaretRight,
  PencilSimple,
  CircleNotch,
  PlugsConnected,
  Clock,
} from '@phosphor-icons/react'
import { SkeletonList, EmptyState, ErrorState } from '@/components/shared/ListStates'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  fetchSkills,
  fetchMcpServers,
  fetchTools,
  deleteSkill,
  deleteMcpServer,
  testMcpServer,
  updateMcpServer,
  isApiError,
  skillLastInvoked,
  type McpServer,
  type ToolRegistryEntry,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SkillBrowser } from '@/components/skills/SkillBrowser'
import { McpServerModal } from '@/components/skills/McpServerModal'
import { ScreenHeader } from '@/components/layout/ScreenHeader'
import { CATEGORY_LABELS } from '@/lib/toolCategories'
import { formatDateTime } from '@/lib/dateFormat'

export function SkillsScreen() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [skillBrowserOpen, setSkillBrowserOpen] = useState(false)
  const [mcpModalOpen, setMcpModalOpen] = useState(false)
  const [mcpEditTarget, setMcpEditTarget] = useState<McpServer | null>(null)
  const [confirmDeleteSkill, setConfirmDeleteSkill] = useState<string | null>(null)
  const [confirmDeleteMcp, setConfirmDeleteMcp] = useState<string | null>(null)
  const [expandedMcp, setExpandedMcp] = useState<string | null>(null)
  const [testingMcp, setTestingMcp] = useState<string | null>(null)

  const { data: rawSkills = [], isLoading: skillsLoading, isError: skillsError, refetch: refetchSkills } = useQuery({
    queryKey: ['skills'],
    queryFn: fetchSkills,
  })

  const skills = rawSkills

  const { data: mcpServers = [], isLoading: mcpLoading, isError: mcpError, refetch: refetchMcpServers } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: fetchMcpServers,
  })

  const { data: tools = [], isLoading: toolsLoading, isError: toolsError, refetch: refetchTools } = useQuery({
    queryKey: ['tools'],
    queryFn: fetchTools,
  })

  const { mutate: doDeleteSkill } = useMutation({
    mutationFn: (name: string) => deleteSkill(name),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['skills'] })
      addToast({ message: 'Skill removed', variant: 'success' })
      setConfirmDeleteSkill(null)
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setConfirmDeleteSkill(null)
    },
  })

  /**
   * Invalidate every cache an MCP server mutation can affect, beyond the
   * server list itself. Delete/toggle/test all reconcile the live MCP
   * manager server-side (connect/disconnect + tool registration), which
   * changes what the central tool registry and per-agent tool lists report —
   * so the policy editors (Settings → Security, ToolsAndPermissions) and
   * this screen's own tools tab must refetch too, or they keep showing
   * stale (pre-reconcile) tool counts / MCP sections. Mirrors the
   * equivalent helper in McpServerModal.tsx (add/edit paths).
   */
  function invalidateMcpToolCaches() {
    queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
    queryClient.invalidateQueries({ queryKey: ['tools-builtin'] }) // SecuritySection global editor
    queryClient.invalidateQueries({ queryKey: ['registry-tools'] }) // ToolsAndPermissions per-agent editor
    queryClient.invalidateQueries({ queryKey: ['agent-tools'] }) // prefix match — every agent
    queryClient.invalidateQueries({ queryKey: ['tools'] }) // this screen's own tools tab
  }

  const { mutate: doDeleteMcp } = useMutation({
    mutationFn: (id: string) => deleteMcpServer(id),
    onSuccess: () => {
      invalidateMcpToolCaches()
      addToast({ message: 'MCP server removed', variant: 'success' })
      setConfirmDeleteMcp(null)
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setConfirmDeleteMcp(null)
    },
  })

  const { mutate: doToggleEnabled } = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      updateMcpServer(id, { enabled }),
    onSuccess: () => {
      invalidateMcpToolCaches()
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
    },
  })

  async function handleTestMcp(id: string) {
    setTestingMcp(id)
    try {
      const result = await testMcpServer(id)
      if (result.success) {
        invalidateMcpToolCaches()
        addToast({
          message: `Connected — ${result.tool_count ?? 0} tool${(result.tool_count ?? 0) !== 1 ? 's' : ''}`,
          variant: 'success',
        })
      } else {
        addToast({ message: result.message, variant: 'error' })
      }
    } catch (err) {
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Test failed',
        variant: 'error',
      })
    } finally {
      setTestingMcp(null)
    }
  }

  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Skills & Tools" />
    <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
    <div className="max-w-4xl mx-auto px-4 py-6">
      <div className="mb-6">
        <p className="text-sm text-[var(--color-muted)] mt-0.5">
          Manage agent capabilities — skills, MCP servers, and built-in tools.
        </p>
      </div>

      <Tabs defaultValue="skills">
        <TabsList className="mb-6">
          <TabsTrigger value="skills" className="gap-1.5">
            <PuzzlePiece size={13} /> Installed Skills
          </TabsTrigger>
          <TabsTrigger value="mcp" className="gap-1.5">
            <HardDrives size={13} /> MCP Servers
          </TabsTrigger>
          <TabsTrigger value="builtins" className="gap-1.5">
            <Wrench size={13} /> Built-in Tools
          </TabsTrigger>
        </TabsList>

        {/* Installed Skills */}
        <TabsContent value="skills">
          {skillsError ? (
            <ErrorState message="Could not load skills." onRetry={() => refetchSkills()} />
          ) : skillsLoading ? (
            <SkeletonList />
          ) : skills.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 gap-4 text-center">
              <div className="text-[var(--color-border)]">
                <PuzzlePiece size={40} weight="thin" />
              </div>
              <div className="space-y-1">
                <p className="text-sm text-[var(--color-muted)]">No skills installed.</p>
                <p className="text-xs text-[var(--color-muted)]/70">Browse the skill registry to add capabilities to your agents.</p>
              </div>
              <Button size="sm" className="gap-1.5 mt-1" onClick={() => setSkillBrowserOpen(true)}>
                <MagnifyingGlass size={13} /> Browse Skills
              </Button>
            </div>
          ) : (
            <>
              <div className="flex justify-end mb-3">
                <Button size="sm" className="gap-1.5" onClick={() => setSkillBrowserOpen(true)}>
                  <MagnifyingGlass size={13} /> Browse Skills
                </Button>
              </div>
            <div className="space-y-2">
              {skills.map((skill) => (
                <div
                  key={skill.id}
                  className="flex items-start gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-medium text-sm text-[var(--color-secondary)]">{skill.name}</span>
                      {skill.version && skill.version !== '0.0.0' && (
                        <span className="font-mono text-[10px] text-[var(--color-muted)]">v{skill.version}</span>
                      )}
                      {skill.source === 'builtin' && (
                        <Badge variant="secondary" className="text-[10px]">
                          Built-in
                        </Badge>
                      )}
                      {(skill.source === 'global' || skill.source === 'workspace') && (
                        <Badge variant="secondary" className="text-[10px]">
                          Community
                        </Badge>
                      )}
                      {skill.verified && (
                        <Badge variant="success" className="gap-1 text-[10px]">
                          <Shield size={9} weight="fill" /> Verified
                        </Badge>
                      )}
                      <Badge
                        variant={skill.status === 'active' ? 'success' : skill.status === 'error' ? 'error' : 'muted'}
                        className="text-[10px]"
                      >
                        {skill.status}
                      </Badge>
                    </div>
                    <p className="text-xs text-[var(--color-muted)] mt-1">{skill.description}</p>
                    <div className="flex items-center gap-3 mt-1.5 text-[10px] text-[var(--color-muted)]">
                      {skill.author && <span>by {skill.author}</span>}
                      {skill.agent_assignment && <span>→ {skill.agent_assignment}</span>}
                    </div>
                    {/* ADR-072 D3.1: a granted skill's last-invocation
                        timestamp — the cheapest observable signal for
                        whether D2 (description-driven activation) is
                        actually working. A skill that has never been called
                        must render VISIBLY as unused, not look identical to
                        an actively-used one — so this always renders a row,
                        distinguishing "Never used" from a real timestamp
                        rather than omitting the line entirely when there is
                        no timestamp. */}
                    <div
                      className="flex items-center gap-1 mt-1.5 text-[10px] text-[var(--color-muted)]"
                      data-testid={`skill-last-invoked-${skill.id}`}
                    >
                      <Clock size={11} />
                      {skillLastInvoked(skill) ? (
                        <span>Last used {formatDateTime(skillLastInvoked(skill))}</span>
                      ) : (
                        <span className="italic">Never used</span>
                      )}
                    </div>
                  </div>
                  {skill.source !== 'builtin' && (
                    <button tabIndex={0}
                      type="button"
                      onClick={() => setConfirmDeleteSkill(skill.name)}
                      className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors p-1 rounded shrink-0"
                      aria-label={`Remove ${skill.name}`}
                    >
                      <Trash size={14} />
                    </button>
                  )}
                </div>
              ))}
            </div>
            </>
          )}
        </TabsContent>

        {/* MCP Servers */}
        <TabsContent value="mcp">
          <div className="flex justify-end mb-3">
            <Button size="sm" className="gap-1.5" onClick={() => { setMcpEditTarget(null); setMcpModalOpen(true) }}>
              <Plus size={13} /> Add Server
            </Button>
          </div>
          {mcpError ? (
            <ErrorState message="Could not load MCP servers." onRetry={() => refetchMcpServers()} />
          ) : mcpLoading ? (
            <SkeletonList />
          ) : mcpServers.length === 0 ? (
            <EmptyState icon={<HardDrives size={40} weight="thin" />} message="No MCP servers connected." />
          ) : (
            <div className="space-y-2">
              {mcpServers.map((server) => {
                const isExpanded = expandedMcp === server.id
                return (
                  <div
                    key={server.id}
                    className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
                  >
                    <div className="flex items-center gap-3 p-4">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-medium text-sm text-[var(--color-secondary)]">{server.name}</span>
                          <Badge variant="outline" className="text-[10px] font-mono">{server.transport}</Badge>
                          <Badge
                            variant={server.status === 'connected' ? 'success' : 'error'}
                            className="text-[10px]"
                          >
                            {server.status}
                          </Badge>
                        </div>
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        <span className="text-xs text-[var(--color-muted)]">
                          {server.tool_count} tools
                        </span>
                        {/* Enable/disable toggle (G8) */}
                        <Switch
                          checked={server.enabled !== false}
                          onCheckedChange={(checked) => doToggleEnabled({ id: server.id, enabled: checked })}
                          aria-label={server.enabled !== false ? `Disable ${server.name}` : `Enable ${server.name}`}
                          data-testid={`toggle-enabled-${server.id}`}
                        />
                        {/* Test button (G7) */}
                        <button tabIndex={0}
                          type="button"
                          onClick={() => handleTestMcp(server.id)}
                          disabled={testingMcp === server.id}
                          className="text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors p-1 rounded disabled:opacity-50"
                          aria-label={`Test ${server.name}`}
                          data-testid={`test-mcp-${server.id}`}
                        >
                          {testingMcp === server.id
                            ? <CircleNotch size={14} className="animate-spin" />
                            : <PlugsConnected size={14} />
                          }
                        </button>
                        {/* Edit button (G8) */}
                        <button tabIndex={0}
                          type="button"
                          onClick={() => { setMcpEditTarget(server); setMcpModalOpen(true) }}
                          className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors p-1 rounded"
                          aria-label={`Edit ${server.name}`}
                          data-testid={`edit-mcp-${server.id}`}
                        >
                          <PencilSimple size={14} />
                        </button>
                        <button tabIndex={0}
                          type="button"
                          onClick={() => setExpandedMcp(isExpanded ? null : server.id)}
                          className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors p-1 rounded"
                          aria-expanded={isExpanded}
                          aria-controls={isExpanded ? `mcp-tools-${server.id}` : undefined}
                          aria-label={`${isExpanded ? 'Hide' : 'Show'} tools for ${server.name}`}
                        >
                          {isExpanded ? <CaretUp size={13} /> : <CaretDown size={13} />}
                        </button>
                        <button tabIndex={0}
                          type="button"
                          onClick={() => setConfirmDeleteMcp(server.id)}
                          className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors p-1 rounded"
                          aria-label={`Remove ${server.name}`}
                        >
                          <Trash size={14} />
                        </button>
                      </div>
                    </div>
                    {isExpanded && (
                      <div id={`mcp-tools-${server.id}`} className="px-4 pb-4 border-t border-[var(--color-border)]">
                        {server.tools && server.tools.length > 0 ? (
                          <div className="pt-3 flex flex-wrap gap-1.5">
                            {server.tools.map((tool) => (
                              <span
                                key={tool}
                                className="font-mono text-[10px] px-2 py-0.5 rounded bg-[var(--color-surface-2)] border border-[var(--color-border)] text-[var(--color-muted)]"
                              >
                                {tool}
                              </span>
                            ))}
                          </div>
                        ) : (
                          <p className="pt-3 text-xs text-[var(--color-muted)]">No tool details available.</p>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </TabsContent>

        {/* Built-in tools — read-only by-category overview (US-E2 / #338) */}
        <TabsContent value="builtins">
          {toolsError ? (
            <ErrorState message="Could not load tools." onRetry={() => refetchTools()} />
          ) : toolsLoading ? (
            <SkeletonList />
          ) : tools.length === 0 ? (
            <EmptyState icon={<Wrench size={40} weight="thin" />} message="No tools available." />
          ) : (
            <ToolsOverview tools={tools} />
          )}
        </TabsContent>
      </Tabs>

      {/* Skill browser modal */}
      <SkillBrowser open={skillBrowserOpen} onOpenChange={setSkillBrowserOpen} />

      {/* MCP server add/edit modal */}
      <McpServerModal
        open={mcpModalOpen}
        onOpenChange={(o) => {
          setMcpModalOpen(o)
          if (!o) setMcpEditTarget(null)
        }}
        initialServer={mcpEditTarget ?? undefined}
      />

      {/* Skill delete confirmation */}

      <AlertDialog
        open={confirmDeleteSkill !== null}
        onOpenChange={(o) => !o && setConfirmDeleteSkill(null)}
      >
        <AlertDialogContent className="max-w-sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Remove skill</AlertDialogTitle>
            <AlertDialogDescription>
              Remove <span className="font-medium text-[var(--color-secondary)]">{confirmDeleteSkill}</span>? This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel size="sm">Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              size="sm"
              onClick={() => {
                if (confirmDeleteSkill) doDeleteSkill(confirmDeleteSkill)
                setConfirmDeleteSkill(null)
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* MCP server delete confirmation */}
      <AlertDialog
        open={confirmDeleteMcp !== null}
        onOpenChange={(o) => !o && setConfirmDeleteMcp(null)}
      >
        <AlertDialogContent className="max-w-sm">
          <AlertDialogHeader>
            <AlertDialogTitle>Remove MCP server</AlertDialogTitle>
            <AlertDialogDescription>
              Remove this MCP server? Any agents using it will lose access to its tools.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel size="sm">Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              size="sm"
              onClick={() => {
                if (confirmDeleteMcp) doDeleteMcp(confirmDeleteMcp)
                setConfirmDeleteMcp(null)
              }}
            >
              Remove
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

    </div>
    </div>
    </div>
  )
}

/**
 * Category display names for the by-category overview.
 * All builtin tools are shown grouped by domain category.
 * Editing happens in Settings → Security.
 */
const CATEGORY_DESCRIPTIONS: Record<string, string> = {
  filesystem: 'Read, write, and search files in the workspace',
  shell: 'Run commands in agent workspaces',
  browser: 'Navigate, click, and screenshot web pages in a headless browser',
  web: 'Search the web and fetch content from URLs',
  platform: 'Access system-level information and gateway management',
  tasks: 'Create, update, and list tasks in the task board',
  memory: 'Store and recall information across sessions',
  communication: 'Send messages and notifications to users',
  delegation: 'Spawn subagents and hand off conversations',
  agents: 'Create and manage agents',
  workspaces: 'Create and manage workspaces',
  channels: 'Enable and configure messaging channels',
  providers: 'Configure AI model providers',
  skills: 'Install and manage skills',
  tool_discovery: 'Search and discover available tools',
  mcp: 'Manage MCP servers',
  general: 'General-purpose tools',
  // Legacy fallback values
  workspace: 'Read, write, and search files; run commands in agent workspaces',
  system: 'System management tools',
}

function ToolsOverview({ tools }: { tools: ToolRegistryEntry[] }) {
  // Show all tools (scope is now only 'core' or 'general')
  const visible = tools

  // Group by category
  const grouped = visible.reduce<Record<string, ToolRegistryEntry[]>>((acc, tool) => {
    const cat = tool.category || 'general'
    if (!acc[cat]) acc[cat] = []
    acc[cat].push(tool)
    return acc
  }, {})

  const categories = Object.keys(grouped).sort()

  // Track expanded categories for drill-down (fix #6)
  const [expandedCategories, setExpandedCategories] = useState<Set<string>>(new Set())

  function toggleCategory(cat: string) {
    setExpandedCategories((prev) => {
      const next = new Set(prev)
      if (next.has(cat)) {
        next.delete(cat)
      } else {
        next.add(cat)
      }
      return next
    })
  }

  return (
    <div className="space-y-4">
      <p className="text-xs text-[var(--color-muted)]">
        What your agents can do — grouped by capability area. Click a category to see the individual tools.
        Permissions are managed in{' '}
        <a tabIndex={0}
          href="/settings?tab=security"
          className="inline-flex items-center gap-0.5 text-[var(--color-accent)] hover:opacity-80 underline"
          data-testid="manage-permissions-link"
        >
          Settings → Security
          <ArrowRight size={11} />
        </a>
        .
      </p>

      {categories.length === 0 ? (
        <EmptyState
          icon={<Wrench size={40} weight="thin" />}
          message="No tools available."
        />
      ) : (
        <div className="space-y-2">
          {categories.map((cat) => {
            const catTools = grouped[cat]
            const description =
              CATEGORY_DESCRIPTIONS[cat] ??
              `${catTools.length} tool${catTools.length !== 1 ? 's' : ''} in this category`
            const isExpanded = expandedCategories.has(cat)
            return (
              <div
                key={cat}
                className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
                data-testid={`tool-category-${cat}`}
              >
                {/* Category header row — clickable to expand/collapse */}
                <button tabIndex={0}
                  type="button"
                  onClick={() => toggleCategory(cat)}
                  className="flex items-center gap-3 w-full px-4 py-3 text-left hover:bg-[var(--color-surface-2)] transition-colors"
                  aria-expanded={isExpanded}
                  aria-controls={isExpanded ? `tool-category-tools-${cat}` : undefined}
                  data-testid={`tool-category-toggle-${cat}`}
                >
                  <div className="flex-1 min-w-0">
                    <span className="text-sm font-medium text-[var(--color-secondary)]">
                      {CATEGORY_LABELS[cat] ?? cat}
                    </span>
                    <p className="text-xs text-[var(--color-muted)] mt-0.5">
                      {description}
                    </p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <Badge variant="muted" className="text-[10px]">
                      {catTools.length} tool{catTools.length !== 1 ? 's' : ''}
                    </Badge>
                    {isExpanded
                      ? <CaretUp size={13} className="text-[var(--color-muted)]" />
                      : <CaretRight size={13} className="text-[var(--color-muted)]" />
                    }
                  </div>
                </button>
                {/* Expanded tool list */}
                {isExpanded && (
                  <div
                    id={`tool-category-tools-${cat}`}
                    className="px-4 pb-3 border-t border-[var(--color-border)] pt-2"
                    data-testid={`tool-category-tools-${cat}`}
                  >
                    <div className="flex flex-wrap gap-1.5">
                      {catTools.map((tool) => (
                        <span
                          key={tool.name}
                          className="font-mono text-[10px] px-2 py-0.5 rounded bg-[var(--color-surface-2)] border border-[var(--color-border)] text-[var(--color-muted)]"
                          title={tool.description ?? tool.name}
                          aria-label={tool.description ? `${tool.name} — ${tool.description}` : tool.name}
                        >
                          {tool.name}
                        </span>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
