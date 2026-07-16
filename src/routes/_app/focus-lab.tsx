import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { Slider } from '@/components/ui/slider'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { MagnifyingGlass, Plus } from '@phosphor-icons/react'

export const Route = createFileRoute('/_app/focus-lab')({
  component: FocusLab,
})

/**
 * FOCUS LAB — throwaway visual QA page for the centralized focus ring.
 * Every focusable component in the kit, side by side, so the ring can be
 * compared in one place. Left column = resting; right column = the same
 * control with focus FORCED via the `focus-lab-force` class (see the style
 * block below), which mirrors the central :focus-visible rule.
 *
 * Not linked from any navigation — reach it at /#/focus-lab.
 * DELETE THIS FILE once the audit is done.
 */
function FocusLab() {
  const [sliderVal, setSliderVal] = useState([40])

  return (
    <div className="absolute inset-0 overflow-y-auto overscroll-contain">
      {/* Mirror the central rule so "forced" examples render identically to
          real focus without needing JS focus juggling. */}
      <style>{`
        .focus-lab-force { outline: 2px solid var(--color-accent) !important; outline-offset: 2px; }
      `}</style>
      <div className="max-w-3xl mx-auto px-6 py-8 flex flex-col gap-8">
        <div>
          <h1 className="font-headline text-xl font-bold text-[var(--color-secondary)]">Focus Lab</h1>
          <p className="text-sm text-[var(--color-muted)] mt-1">
            Central ring: 2px solid Forge Gold, 2px offset (WCAG 2.4.13). Left = rest, right = focus (forced).
            Tab through the left column to verify REAL focus matches the forced column.
          </p>
        </div>

        <Row label="Input">
          <Input placeholder="Resting input" className="max-w-xs" />
          <Input placeholder="Focused input" className="max-w-xs focus-lab-force border-[var(--color-accent)]" />
        </Row>

        <Row label="Textarea">
          <Textarea placeholder="Resting textarea" className="max-w-xs" rows={2} />
          <Textarea placeholder="Focused textarea" className="max-w-xs focus-lab-force border-[var(--color-accent)]" rows={2} />
        </Row>

        <Row label="Button (default)">
          <Button>Resting</Button>
          <Button className="focus-lab-force">Focused</Button>
        </Row>

        <Row label="Button (ghost sm — AgentPicker tier)">
          <Button variant="ghost" size="sm">Resting</Button>
          <Button variant="ghost" size="sm" className="focus-lab-force">Focused</Button>
        </Row>

        <Row label="Icon button (attach tier)">
          <button type="button" className="h-7 w-7 rounded-full flex items-center justify-center text-[var(--color-muted)] hover:bg-[var(--color-surface-3)]" aria-label="Resting icon button">
            <Plus size={16} />
          </button>
          <button type="button" className="focus-lab-force h-7 w-7 rounded-full flex items-center justify-center text-[var(--color-muted)]" aria-label="Focused icon button">
            <Plus size={16} />
          </button>
        </Row>

        <Row label="Icon button (sidebar tier)">
          <button type="button" className="rounded p-1.5 text-[var(--color-muted)] hover:bg-[var(--color-surface-2)]" aria-label="Resting sidebar icon">
            <MagnifyingGlass size={16} />
          </button>
          <button type="button" className="focus-lab-force rounded p-1.5 text-[var(--color-muted)]" aria-label="Focused sidebar icon">
            <MagnifyingGlass size={16} />
          </button>
        </Row>

        <Row label="Select">
          <Select>
            <SelectTrigger className="max-w-xs"><SelectValue placeholder="Resting select" /></SelectTrigger>
            <SelectContent><SelectItem value="a">Option A</SelectItem></SelectContent>
          </Select>
          <Select>
            <SelectTrigger className="max-w-xs focus-lab-force border-[var(--color-accent)]"><SelectValue placeholder="Focused select" /></SelectTrigger>
            <SelectContent><SelectItem value="a">Option A</SelectItem></SelectContent>
          </Select>
        </Row>

        <Row label="Checkbox">
          <label className="flex items-center gap-2 text-sm text-[var(--color-secondary)]"><Checkbox /> Resting</label>
          <label className="flex items-center gap-2 text-sm text-[var(--color-secondary)]"><Checkbox className="focus-lab-force" /> Focused</label>
        </Row>

        <Row label="Switch">
          <Switch aria-label="Resting switch" />
          <Switch className="focus-lab-force" aria-label="Focused switch" />
        </Row>

        <Row label="Slider (thumb focus)">
          <Slider value={sliderVal} onValueChange={setSliderVal} max={100} className="max-w-xs" aria-label="Resting slider" />
          <span className="text-xs text-[var(--color-muted)]">Tab to the thumb to verify the ring live</span>
        </Row>

        <Row label="Tabs">
          <Tabs defaultValue="one" className="max-w-xs">
            <TabsList>
              <TabsTrigger value="one">Rest</TabsTrigger>
              <TabsTrigger value="two" className="focus-lab-force">Focused</TabsTrigger>
            </TabsList>
            <TabsContent value="one" />
          </Tabs>
        </Row>

        <Row label="Link / nav item (sidebar tier)">
          <a href="#/" className="text-sm text-[var(--color-secondary)] px-3 py-2 rounded-lg hover:bg-[var(--color-surface-2)]">Resting link</a>
          <a href="#/" className="focus-lab-force text-sm text-[var(--color-secondary)] px-3 py-2 rounded-lg">Focused link</a>
        </Row>

        <p className="text-xs text-[var(--color-muted)] pb-8">
          Throwaway page — delete src/routes/_app/focus-lab.tsx after the audit.
        </p>
      </div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <span className="text-[10px] font-semibold uppercase tracking-widest text-[var(--color-muted)]">{label}</span>
      <div className="grid grid-cols-2 gap-6 items-center">{children}</div>
    </div>
  )
}
