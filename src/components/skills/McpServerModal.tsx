import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { SmartSelect } from '@/components/ui/smart-select'
import { addMcpServer, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface McpServerModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function McpServerModal({ open, onOpenChange }: McpServerModalProps) {
  const queryClient = useQueryClient()
  const { addToast } = useUiStore()

  const [name, setName] = useState('')
  const [command, setCommand] = useState('')
  const [args, setArgs] = useState('')
  const [transport, setTransport] = useState<'stdio' | 'sse' | 'http'>('stdio')

  const { mutate: doAdd, isPending } = useMutation({
    mutationFn: () =>
      addMcpServer({
        name: name.trim(),
        command: command.trim(),
        args: args.trim() ? args.split(',').map((a) => a.trim()).filter(Boolean) : undefined,
        transport,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['mcp-servers'] })
      addToast({ message: 'MCP server added', variant: 'success' })
      handleClose()
    },
    onError: (err: unknown) => addToast({ message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to add MCP server', variant: 'error' }),
  })

  function handleClose() {
    setName('')
    setCommand('')
    setArgs('')
    setTransport('stdio' as const)
    onOpenChange(false)
  }

  const canSubmit = name.trim().length > 0 && command.trim().length > 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add MCP Server</DialogTitle>
          <DialogDescription>Connect a Model Context Protocol server to extend agent capabilities</DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          <div className="space-y-1">
            <label className="text-xs text-[var(--color-muted)]">Name</label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-mcp-server"
              className="text-sm"
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-[var(--color-muted)]">Command</label>
            <Input
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="npx @example/mcp-server"
              className="text-sm font-mono"
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-[var(--color-muted)]">Args (comma-separated, optional)</label>
            <Input
              value={args}
              onChange={(e) => setArgs(e.target.value)}
              placeholder="--port, 3000, --verbose"
              className="text-sm font-mono"
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-[var(--color-muted)]">Transport</label>
            <SmartSelect
              value={transport}
              onValueChange={(v) => setTransport(v as typeof transport)}
              items={[
                { value: 'stdio', label: 'stdio' },
                { value: 'sse', label: 'SSE' },
                { value: 'http', label: 'HTTP' },
              ]}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={handleClose} disabled={isPending}>
            Cancel
          </Button>
          <Button size="sm" onClick={() => doAdd()} disabled={!canSubmit || isPending}>
            {isPending ? 'Adding...' : 'Add server'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
