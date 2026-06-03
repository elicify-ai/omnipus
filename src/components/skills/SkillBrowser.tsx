/**
 * SkillBrowser — Install-from-file modal for SKILL.md packages.
 *
 * Changes from the original:
 *  - Install flow now shows a capabilities + "unverified" notice confirm step
 *    before actually installing (US-E4 / #340).
 *  - Non-hash errors are surfaced as a toast instead of being silently swallowed.
 *  - Hash-mismatch still shows the dedicated inline dialog (unchanged).
 */

import { useRef, useState } from 'react'
import { CloudSlash, UploadSimple, Warning, ShieldWarning } from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { installSkillFromFile, isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface SkillBrowserProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface HashMismatchError {
  expected?: string
  got?: string
}

interface PendingInstall {
  file: File
  text: string
  /** Detected capabilities extracted from SKILL.md frontmatter (best-effort). */
  capabilities: string[]
}

/**
 * Best-effort capability extraction from SKILL.md content.
 * Looks for a `capabilities:` YAML list in the frontmatter block.
 * Returns an empty array if nothing is found — the confirm step still shows.
 */
function extractCapabilities(text: string): string[] {
  // Match capabilities list in YAML frontmatter (--- ... ---)
  const frontmatter = text.match(/^---\n([\s\S]*?)\n---/)
  if (!frontmatter) return []
  const block = frontmatter[1]
  // Match `capabilities:` followed by list items
  const capMatch = block.match(/capabilities:\s*\n((?:\s*-[^\n]+\n?)*)/)
  if (!capMatch) return []
  return capMatch[1]
    .split('\n')
    .map((l) => l.replace(/^\s*-\s*/, '').trim())
    .filter(Boolean)
}

export function SkillBrowser({ open, onOpenChange }: SkillBrowserProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const { addToast } = useUiStore()

  const [hashMismatch, setHashMismatch] = useState<HashMismatchError | null>(null)
  const [isInstalling, setIsInstalling] = useState(false)
  const [pendingInstall, setPendingInstall] = useState<PendingInstall | null>(null)

  async function handleFileSelected(file: File) {
    // Read the file content first, then show the confirm step
    let text: string
    try {
      text = await file.text()
    } catch {
      addToast({ message: 'Could not read the selected file.', variant: 'error' })
      if (fileInputRef.current) fileInputRef.current.value = ''
      return
    }
    const capabilities = extractCapabilities(text)
    setPendingInstall({ file, text, capabilities })
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  async function handleConfirmInstall() {
    if (!pendingInstall) return
    const { text, file } = pendingInstall
    setPendingInstall(null)
    setIsInstalling(true)
    try {
      await installSkillFromFile(text, file.name)
      addToast({ message: `Skill "${file.name}" installed successfully.`, variant: 'success' })
    } catch (err: unknown) {
      // Hash-mismatch: the backend returns a 409 whose body carries
      // {"expected": "<sha>", "got": "<sha>"}. Branch on the ApiError type
      // (status === 409) and parse err.body — NOT err.message, which only
      // contains the generic userMessage ("This conflicts…").
      if (isApiError(err) && err.status === 409) {
        let expected: string | undefined
        let got: string | undefined
        try {
          if (err.body) {
            const parsed = JSON.parse(err.body) as {
              expected?: string
              got?: string
            }
            expected = parsed.expected
            got = parsed.got
          }
        } catch {
          // Body wasn't JSON — show the dialog with blank hashes rather than
          // misclassifying as a generic error.
        }
        setHashMismatch({ expected, got })
      } else {
        // Surface all other errors as a toast (no more silent swallow)
        const msg = isApiError(err)
          ? err.userMessage
          : err instanceof Error
          ? err.message
          : String(err)
        addToast({
          message: `Failed to install skill: ${msg || 'Unknown error'}`,
          variant: 'error',
        })
      }
    } finally {
      setIsInstalling(false)
    }
  }

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Browse Skills</DialogTitle>
            <DialogDescription>Install skills from the ClawHub registry</DialogDescription>
          </DialogHeader>

          <div className="flex flex-col items-center justify-center py-10 gap-3 text-center">
            <CloudSlash size={40} weight="thin" className="text-[var(--color-border)]" />
            <p className="text-sm text-[var(--color-muted)]">ClawHub registry not yet available</p>
            <p className="text-xs text-[var(--color-muted)]">
              Install skills manually by placing a{' '}
              <span className="font-mono">SKILL.md</span> file in your skills directory.
            </p>

            {/* Install from file */}
            <Button
              size="sm"
              variant="outline"
              disabled={isInstalling}
              onClick={() => fileInputRef.current?.click()}
              className="mt-2 gap-2"
            >
              <UploadSimple size={14} />
              {isInstalling ? 'Installing...' : 'Install from file'}
            </Button>
            <input
              ref={fileInputRef}
              type="file"
              accept=".zip,.json,.md"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0]
                if (file) void handleFileSelected(file)
              }}
            />
          </div>
        </DialogContent>
      </Dialog>

      {/* Install confirmation dialog — shows capabilities + unverified notice */}
      <Dialog
        open={pendingInstall !== null}
        onOpenChange={(o) => {
          if (!o) setPendingInstall(null)
        }}
      >
        <DialogContent data-testid="skill-install-confirm-dialog" className="max-w-md">
          <DialogHeader>
            <DialogTitle>Install skill</DialogTitle>
            <DialogDescription>
              Review this skill before installing. Unverified skills run on your server
              and can access tools allowed by your agent policy.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            {/* Unverified notice */}
            <div
              className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2.5"
              data-testid="unverified-notice"
            >
              <ShieldWarning size={15} weight="fill" className="text-amber-400 mt-0.5 shrink-0" />
              <div className="text-xs text-amber-300 leading-relaxed">
                <span className="font-semibold">Unverified skill.</span> This skill has not been
                reviewed or signed by the Omnipus team. Only install skills you trust.
              </div>
            </div>

            {/* File name */}
            <div className="text-xs text-[var(--color-muted)]">
              File:{' '}
              <span className="font-mono text-[var(--color-secondary)]">
                {pendingInstall?.file.name}
              </span>
            </div>

            {/* Capabilities */}
            {pendingInstall && pendingInstall.capabilities.length > 0 ? (
              <div className="space-y-1">
                <p className="text-xs font-medium text-[var(--color-secondary)]">
                  Declared capabilities
                </p>
                <ul className="space-y-0.5">
                  {pendingInstall.capabilities.map((cap) => (
                    <li
                      key={cap}
                      className="flex items-center gap-1.5 text-xs text-[var(--color-muted)]"
                    >
                      <Warning size={11} className="text-amber-400 shrink-0" />
                      {cap}
                    </li>
                  ))}
                </ul>
              </div>
            ) : (
              <p className="text-xs text-[var(--color-muted)]">
                No capabilities declared in this skill file.
              </p>
            )}
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPendingInstall(null)}
              disabled={isInstalling}
            >
              Cancel
            </Button>
            <Button
              size="sm"
              data-testid="confirm-install-btn"
              onClick={() => void handleConfirmInstall()}
              disabled={isInstalling}
            >
              {isInstalling ? 'Installing...' : 'Install skill'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Hash mismatch dialog (#109) */}
      <Dialog open={hashMismatch !== null} onOpenChange={() => setHashMismatch(null)}>
        <DialogContent data-testid="skill-hash-mismatch-dialog" className="max-w-md">
          <DialogHeader>
            <DialogTitle>Integrity check failed</DialogTitle>
            <DialogDescription>
              The skill file could not be installed because its hash does not match the expected
              value.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2 text-sm">
            <p className="text-[var(--color-error)]">
              Hash mismatch — the skill file may have been tampered with or corrupted.
            </p>
            {hashMismatch?.expected && (
              <p className="text-xs text-[var(--color-muted)]">
                Expected: <span className="font-mono">{hashMismatch.expected}</span>
              </p>
            )}
            {hashMismatch?.got && (
              <p className="text-xs text-[var(--color-muted)]">
                Got: <span className="font-mono">{hashMismatch.got}</span>
              </p>
            )}
          </div>
          <div className="flex justify-end">
            <Button size="sm" variant="outline" onClick={() => setHashMismatch(null)}>
              Dismiss
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}
