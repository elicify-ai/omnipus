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
 *
 * Exported for EnvironmentSetupApprovalPreview (generic-install lane): an
 * environment_setup command/script is displayed with the same formatting
 * pattern — the approver reads the agent-supplied text exactly as it will
 * run, multiline scripts included (`whitespace-pre-wrap` in the caller).
 */
export function formatBashCommand(command: string): { envPrefix: string; binary: string; args: string } {
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

/**
 * Splits `commandText` around the first occurrence of `binary` so a caller
 * can render the resolved binary highlighted, matching formatBashCommand's
 * existing highlight treatment. Used for ADR-092 D4's per-segment approval
 * display (ToolApprovalModal.tsx), where each CommandSegmentInfo carries its
 * own `resolved_binary` rather than reusing the whole-command env/binary
 * split above. Falls back to putting the whole text in `after` (nothing
 * highlighted) when there is no binary to find, or it does not literally
 * appear in the text — never throws, never guesses.
 */
export function highlightBinaryInText(
  commandText: string,
  binary?: string,
): { before: string; binary: string; after: string } {
  if (!binary) return { before: '', binary: '', after: commandText }
  const idx = commandText.indexOf(binary)
  if (idx === -1) return { before: '', binary: '', after: commandText }
  return {
    before: commandText.slice(0, idx),
    binary: commandText.slice(idx, idx + binary.length),
    after: commandText.slice(idx + binary.length),
  }
}

export function BashApprovalPreview({ args }: ToolApprovalPreviewContext) {
  const command = typeof args.command === 'string' && args.command.length > 0 ? args.command : null
  if (!command) return null

  const preview = formatBashCommand(command)
  const cwd = typeof args.cwd === 'string' && args.cwd.length > 0 ? args.cwd : undefined

  return (
    <div>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mb-[var(--space-1)]">Command</p>
      <pre className="font-mono text-[length:var(--type-utility-xs-size)] bg-[var(--color-surface-2)] rounded-lg px-[var(--space-2-5)] py-[var(--space-2)] whitespace-pre-wrap break-all text-[var(--color-secondary)]">
        {preview.envPrefix && (
          <span className="text-[var(--color-muted)]">{preview.envPrefix} </span>
        )}
        <span className="text-[var(--color-accent)] font-semibold">{preview.binary}</span>
        <span>{preview.args}</span>
      </pre>
      {cwd && (
        <p className="mt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          <span className="text-[var(--color-border)]">dir: </span>
          <span className="font-mono">{cwd}</span>
        </p>
      )}
    </div>
  )
}
