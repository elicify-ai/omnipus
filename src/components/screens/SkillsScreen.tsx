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
} from '@phosphor-icons/react'
import { SkeletonList, EmptyState, ErrorState } from '@/components/shared/ListStates'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
  isApiError,
} from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SkillBrowser } from '@/components/skills/SkillBrowser'
import { McpServerModal } from '@/components/skills/McpServerModal'

export function SkillsScreen() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const [skillBrowserOpen, setSkillBrowserOpen] = useState(false)
  const [mcpModalOpen, setMcpModalOpen] = useState(false)
  const [confirmDeleteSkill, setConfirmDeleteSkill] = useState<string | null>(null)
  const [confirmDeleteMcp, setConfirmDeleteMcp] = useState<string | null>(null)
  const [expandedMcp, setExpandedMcp] = useState<string | null>(null)
  const [expandedTool, setExpandedTool] = useState<string | null>(null)

  const { data: rawSkills = [], isLoading: skillsLoading, isError: skillsError } = useQuery({
    queryKey: ['skills'],
    queryFn: fetchSkills,
  })

  const skills = rawSkills

  const { data: mcpServers = [], isLoading: mcpLoading, isError: mcpError } = useQuery({
    queryKey: ['mcp-servers'],
    queryFn: fetchMcpServers,
  })

  const { data: tools = [], isLoading: toolsLoading, isError: toolsError } = useQuery({
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

  const { mutate: doDeleteMcp } = useMutation({
    mutationFn: (id: string) => deleteMcpServer(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      addToast({ message: 'MCP server removed', variant: 'success' })
      setConfirmDeleteMcp(null)
    },
    onError: (err: Error) => {
      addToast({ message: isApiError(err) ? err.userMessage : err.message, variant: 'error' })
      setConfirmDeleteMcp(null)
    },
  })

  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
    <div className="max-w-4xl mx-auto px-4 py-6">
      <div className="mb-6">
        <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Skills & Tools</h1>
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
          <div className="flex justify-end mb-3">
            <Button size="sm" className="gap-1.5" onClick={() => setSkillBrowserOpen(true)}>
              <MagnifyingGlass size={13} /> Browse Skills
            </Button>
          </div>
          {skillsError ? (
            <ErrorState message="Could not load skills." />
          ) : skillsLoading ? (
            <SkeletonList />
          ) : skills.length === 0 ? (
            <EmptyState icon={<PuzzlePiece size={40} weight="thin" />} message="No skills installed." />
          ) : (
            <div className="space-y-2">
              {skills.map((skill) => (
                <div
                  key={skill.id}
                  className="flex items-start gap-3 p-4 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)]"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-medium text-sm text-[var(--color-secondary)]">{skill.name}</span>
                      <span className="font-mono text-[10px] text-[var(--color-muted)]">v{skill.version}</span>
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
                      <span>by {skill.author}</span>
                      {skill.agent_assignment && <span>→ {skill.agent_assignment}</span>}
                    </div>
                  </div>
                  <button
                    type="button"
                    onClick={() => setConfirmDeleteSkill(skill.name)}
                    className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors p-1 rounded shrink-0"
                    aria-label={`Remove ${skill.name}`}
                  >
                    <Trash size={14} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </TabsContent>

        {/* MCP Servers */}
        <TabsContent value="mcp">
          <div className="flex justify-end mb-3">
            <Button size="sm" className="gap-1.5" onClick={() => setMcpModalOpen(true)}>
              <Plus size={13} /> Add Server
            </Button>
          </div>
          {mcpError ? (
            <ErrorState message="Could not load MCP servers." />
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
                        <button
                          type="button"
                          onClick={() => setExpandedMcp(isExpanded ? null : server.id)}
                          className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors p-1 rounded"
                          aria-label="Toggle tool list"
                        >
                          {isExpanded ? <CaretUp size={13} /> : <CaretDown size={13} />}
                        </button>
                        <button
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
                      <div className="px-4 pb-4 border-t border-[var(--color-border)]">
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

        {/* Built-in tools */}
        <TabsContent value="builtins">
          {toolsError ? (
            <ErrorState message="Could not load tools." />
          ) : toolsLoading ? (
            <SkeletonList />
          ) : tools.length === 0 ? (
            <EmptyState icon={<Wrench size={40} weight="thin" />} message="No tools available." />
          ) : (
            <div className="space-y-1.5">
              {tools.map((tool) => {
                const isExpanded = expandedTool === tool.name
                return (
                  <div
                    key={tool.name}
                    className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] overflow-hidden"
                  >
                    <button
                      type="button"
                      onClick={() => setExpandedTool(isExpanded ? null : tool.name)}
                      className="flex items-center gap-3 px-4 py-3 w-full text-left hover:bg-[var(--color-surface-2)] transition-colors"
                    >
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <span className="font-mono text-xs text-[var(--color-secondary)]">{tool.name}</span>
                          <Badge variant="muted" className="text-[10px]">{tool.category}</Badge>
                        </div>
                      </div>
                      {isExpanded ? (
                        <CaretUp size={12} className="text-[var(--color-muted)] shrink-0" />
                      ) : (
                        <CaretDown size={12} className="text-[var(--color-muted)] shrink-0" />
                      )}
                    </button>
                    {isExpanded && (
                      <div className="px-4 pb-3 border-t border-[var(--color-border)]">
                        <p className="text-xs text-[var(--color-muted)] mt-2">{tool.description}</p>
                        <div className="flex items-center gap-2 mt-2">
                          <span className="text-[10px] text-[var(--color-muted)]">Status:</span>
                          <Badge variant="success" className="text-[10px]">enabled</Badge>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </TabsContent>
      </Tabs>

      {/* Skill browser modal */}
      <SkillBrowser open={skillBrowserOpen} onOpenChange={setSkillBrowserOpen} />

      {/* MCP server add modal */}
      <McpServerModal open={mcpModalOpen} onOpenChange={setMcpModalOpen} />

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
  )
}
