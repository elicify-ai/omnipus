// standins.tsx — throwaway Mail / Tasks / Team / Calendar stand-in contents
// for the wave-0 demo (§9: "stand-in stories ... throwaway, don't
// over-invest"). Static fixture lists, no behavior beyond being visible;
// wave 1+ replaces each with the real panel.

function StandinFrame(props: { testId: string; title: string; note: string }) {
  return (
    <div data-testid={props.testId} className="flex h-full min-h-0 flex-col gap-[var(--space-2)] p-[var(--space-3)]">
      <p className="text-[length:var(--type-body-compact-size)] font-medium">{props.title}</p>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{props.note}</p>
    </div>
  )
}

export function MailPanelStandin() {
  return (
    <div data-testid="panel-content-mail" className="flex h-full min-h-0 flex-col gap-[var(--space-2)] p-[var(--space-3)]">
      <p className="text-[length:var(--type-body-compact-size)] font-medium">Mail</p>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Stand-in content — the real Mail panel lands with its own wave.</p>
      {/* A horizontally scrollable strip — SP-26's row 15 exercises a swipe
          STARTING here: it must scroll the strip, never close the panel.
          Keyboard accessible (axe scrollable-region-focusable): focusable
          region with its own name. */}
      <div
        data-testid="mail-carousel"
        role="region"
        aria-label="Message carousel"
        tabIndex={0}
        className="flex gap-[var(--space-2)] overflow-x-auto pb-[var(--space-1)]"
      >
        {['One', 'Two', 'Three', 'Four', 'Five', 'Six', 'Seven'].map((label, i) => (
          <div
            key={label}
            className="flex h-16 w-40 shrink-0 items-center justify-center rounded-md bg-[var(--color-surface-2)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]"
          >
            Message {i + 1} — {label}
          </div>
        ))}
      </div>
      {['Welcome to the fixture mailbox', 'Demo message from Alpha', 'Notes about the panel shell'].map((subject) => (
        <div key={subject} className="rounded-md bg-[var(--color-surface-2)] p-[var(--space-2)] text-[length:var(--type-utility-xs-size)]">
          {subject}
        </div>
      ))}
    </div>
  )
}

export function TasksPanelStandin() {
  return (
    <StandinFrame
      testId="panel-content-tasks"
      title="Tasks"
      note="Stand-in content — list/kanban views arrive with the real Tasks panel."
    />
  )
}

export function TeamPanelStandin() {
  return (
    <StandinFrame
      testId="panel-content-team"
      title="Team"
      note="Stand-in content — the agent graph arrives with the real Team panel."
    />
  )
}

export function CalendarPanelStandin() {
  return (
    <StandinFrame
      testId="panel-content-calendar"
      title="Calendar"
      note="Stand-in content — day/week views arrive with the real Calendar panel."
    />
  )
}
