// Readable command preview for `bash` tool-approval requests. Ported
// (verbatim in spirit) from the retired ExecApprovalBlock — see the ADR-036
// note atop ToolApprovalModal.tsx. Registered as an ADDITIVE registry entry
// (registry.ts): this renders ABOVE the generic Arguments JSON dump, it does
// not replace it — approving a shell command benefits from seeing both the
// highlighted command AND the raw args (e.g. run_in_background).

import type { ToolApprovalPreviewContext } from './types'

/**
 * Formats a `bash` command string for display: separates any leading
 * `KEY=value` env-var assignments from the binary name and highlights the
 * binary.
 */
function formatBashCommand(command: string): { envPrefix: string; binary: string; args: string } {
  const parts = command.split(' ')
  let binaryIndex = 0
  for (let i = 0; i < parts.length; i++) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*=/.test(parts[i])) {
      binaryIndex = i
      break
    }
  }
  const envPrefix = parts.slice(0, binaryIndex).join(' ')
  const afterEnv = envPrefix ? command.slice(envPrefix.length + 1) : command
  const firstSpace = afterEnv.indexOf(' ')
  const binary = firstSpace === -1 ? afterEnv : afterEnv.slice(0, firstSpace)
  const args = firstSpace === -1 ? '' : afterEnv.slice(firstSpace)
  return { envPrefix, binary, args }
}

export function BashApprovalPreview({ args }: ToolApprovalPreviewContext) {
  const command = typeof args.command === 'string' && args.command.length > 0 ? args.command : null
  if (!command) return null

  const preview = formatBashCommand(command)
  const cwd = typeof args.cwd === 'string' && args.cwd.length > 0 ? args.cwd : undefined

  return (
    <div>
      <p className="text-xs text-[var(--color-muted)] mb-1">Command</p>
      <pre className="font-mono text-xs bg-[var(--color-surface-2)] rounded-lg px-3 py-2 whitespace-pre-wrap break-all text-[var(--color-secondary)]">
        {preview.envPrefix && (
          <span className="text-[var(--color-muted)]">{preview.envPrefix} </span>
        )}
        <span className="text-[var(--color-accent)] font-semibold">{preview.binary}</span>
        <span>{preview.args}</span>
      </pre>
      {cwd && (
        <p className="mt-1 text-[10px] text-[var(--color-muted)]">
          <span className="text-[var(--color-border)]">dir: </span>
          <span className="font-mono">{cwd}</span>
        </p>
      )}
    </div>
  )
}
