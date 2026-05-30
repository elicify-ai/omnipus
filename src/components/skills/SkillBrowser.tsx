import { useRef, useState } from 'react'
import { CloudSlash, UploadSimple } from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { installSkillFromFile } from '@/lib/api'

interface SkillBrowserProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface HashMismatchError {
  expected?: string
  got?: string
}

export function SkillBrowser({ open, onOpenChange }: SkillBrowserProps) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [hashMismatch, setHashMismatch] = useState<HashMismatchError | null>(null)
  const [isInstalling, setIsInstalling] = useState(false)

  async function handleFileSelected(file: File) {
    setIsInstalling(true)
    try {
      const text = await file.text()
      await installSkillFromFile(text, file.name)
    } catch (err: unknown) {
      // Check for hash mismatch error (409 or error message containing "hash mismatch")
      const msg = err instanceof Error ? err.message : String(err)
      if (msg.includes('hash mismatch') || msg.includes('409')) {
        let expected: string | undefined
        let got: string | undefined
        try {
          // Try to parse JSON from the error message (format: "409: {...}")
          const jsonStart = msg.indexOf('{')
          if (jsonStart !== -1) {
            const parsed = JSON.parse(msg.slice(jsonStart)) as { expected?: string; got?: string }
            expected = parsed.expected
            got = parsed.got
          }
        } catch {
          // ignore parse failures
        }
        setHashMismatch({ expected, got })
      }
      // For other errors, silently ignore in this minimal implementation
    } finally {
      setIsInstalling(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
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
              Install skills manually by placing a <span className="font-mono">SKILL.md</span> file in your skills directory.
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

      {/* Hash mismatch dialog (#109) */}
      <Dialog open={hashMismatch !== null} onOpenChange={() => setHashMismatch(null)}>
        <DialogContent data-testid="skill-hash-mismatch-dialog" className="max-w-md">
          <DialogHeader>
            <DialogTitle>Integrity check failed</DialogTitle>
            <DialogDescription>
              The skill file could not be installed because its hash does not match the expected value.
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
