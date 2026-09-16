# Settings & notifications — 2026-05

## Status

Draft, decisions logged 2026-05-03. Specifies the two new operator-facing settings tabs (Memory, Dreamcatcher) introduced by `memory-redesign-2026-05.md`, and the platform-wide notification system that surfaces Dreamcatcher and other events. Designed to extend cleanly to future notification sources (security, tasks, channels).

Companion documents:

- `docs/internal/design/memory-redesign-2026-05.md` — defines the memory tiers, Dreamcatcher consolidation pass, and the values the Memory and Dreamcatcher settings tabs control.
- `docs/internal/design/projects-ui-2026-05.md` — Command Center, session creation, and history surfaces; notification badges integrate with Command Center project rows.
- `docs/internal/design/sandbox-redesign-2026-05.md` — sandbox events that the notification system will surface in a later phase.

## Settings — Memory tab

```
Settings > Memory

  Memory retention
    Session transcripts            [▾ 90 days        ]
    Learnings                      [▾ Forever        ]
    Memories                       [▾ Forever, archive when stale ]

  Memory size
    Soft warning threshold         [ 8 KB    ]
    Hard cap                       [ 64 KB   ]

  Auto-archive
    Confidence threshold           [ 0.2     ]
    Days without access            [ 90      ]
    Sweep schedule                 [▾ Weekly ]

  Confidence drift weights         [advanced ▾]
    On successful action           [ +0.05   ]
    On contradicted (supersedes)   [ -0.20   ]

  User-imperative auto-extract
    [✓] Enable regex auto-remember
    Patterns                       [▾ View / edit 4 patterns]

  Index
    Last rebuilt: 2 hours ago      [ Rebuild now ]
    Storage: 12.4 MB across 247 memories, 38 learnings
```

Each control maps to a configuration value:

| Control | Config key | Default |
|---|---|---|
| Session transcripts retention | `memory.sessions.retention_days` | 90 |
| Learnings retention | `memory.learnings.retention` | `forever` |
| Memories retention | `memory.memories.retention` | `archive_when_stale` |
| Soft warning threshold | `memory.size.soft_kb` | 8 |
| Hard cap | `memory.size.hard_kb` | 64 |
| Confidence threshold | `memory.archive.confidence_threshold` | 0.2 |
| Days without access | `memory.archive.idle_days` | 90 |
| Sweep schedule | `memory.archive.sweep_cron` | `weekly` |
| Confidence on success | `memory.confidence.on_success` | +0.05 |
| Confidence on contradicted | `memory.confidence.on_contradicted` | -0.20 |
| User-imperative regex enabled | `memory.imperative_regex.enabled` | true |

Advanced sections collapse the tuning numbers (drift weights, cluster thresholds) so casual operators don't see them by default.

## Settings — Dreamcatcher tab

```
Settings > Dreamcatcher

  Schedule — project rooms
    [✓] Run nightly
    Time                           [▾ 03:00 local ]
    Per-project enable             [▾ Default on; override per project ]

  Schedule — private rooms
    [ ] Run on schedule            (default off — operator-driven only)
    [ Run now for selected agent... ]

  LLM budget
    Max tokens per run             [ 5000   ]
    Model                          [▾ Same as session recap ]

  Proposals
    Pending proposal expiry        [ 30 days ]
    [✓] Notify when new proposals appear
    [✓] Notify before expiry (3-day warning)

  Cluster detection (auto-emerge MOCs)
    Min memories per emerging MOC  [ 3      ]
    Tag-overlap threshold          [ 0.6    ]

  Recent runs
    2026-05-03 03:00  website-foo  → 5 proposals (3 accepted, 2 pending)
    2026-05-02 03:00  api-rebuild  → 0 proposals
    2026-05-01 03:00  website-foo  → 2 proposals (2 accepted)
    ...
```

Config keys:

| Control | Config key | Default |
|---|---|---|
| Project rooms scheduled | `dreamcatcher.project.scheduled` | true |
| Project rooms run time | `dreamcatcher.project.run_time` | `03:00` |
| Per-project override | `dreamcatcher.project.<id>.enabled` | (inherits) |
| Private rooms scheduled | `dreamcatcher.private.scheduled` | false |
| Max tokens per run | `dreamcatcher.budget.max_tokens` | 5000 |
| Model override | `dreamcatcher.model` | `recap_model` |
| Proposal expiry days | `dreamcatcher.proposals.expiry_days` | 30 |
| Notify on new proposals | `dreamcatcher.notify.new_proposals` | true |
| Notify before expiry | `dreamcatcher.notify.expiry_warning` | true |
| Cluster size threshold | `dreamcatcher.cluster.min_memories` | 3 |
| Tag-overlap threshold | `dreamcatcher.cluster.tag_overlap` | 0.6 |

The "Recent runs" section is a read-only audit view sourced from `<room>/.omnipus/.system.jsonl` events tagged `memory.dreamcatcher.run`.

## Notification system

Cross-cutting platform feature. Phase 1 sources are Dreamcatcher and memory events; designed to extend to sandbox alerts, task due dates, and channel events in later phases.

### Trigger inventory (phase 1)

| Event | Severity | Source | Surfaces |
|---|---|---|---|
| Dreamcatcher generated N proposals | Info | `pkg/memory/dreamcatcher` | Bell badge + dropdown entry + Command Center project row badge |
| Proposals expiring in 3 days | Warning | sweep job over `proposals/` | Bell badge + dropdown entry |
| Dreamcatcher run failed | Error | dreamcatcher pass exit | Bell (red) + persistent banner until acknowledged |
| Auto-archive sweep ran (N memories archived) | Info | sweep job | Dropdown entry only (no badge count) |
| Memory size hard cap exceeded on write | Warning | `remember()` validator | Inline error in chat (caller saw it) + dropdown entry |
| Task reassigned in a project room | Info | `task_update` audit | Dropdown entry only |
| Agent added to project | Info | project membership API | Dropdown entry only |
| Agent removed from project | Info | project membership API | Dropdown entry only |

### Future trigger sources (phase 2+)

Reserved namespaces in the notification schema; not yet wired:

- **Sandbox**: kernel policy denials, audit log anomalies, suspicious binding attempts.
- **Tasks**: due date approaching, blocked task unblocked, project task reassigned.
- **Channels**: new message in a channel, channel disconnect, rate limit reached.
- **Updates**: new gateway version available (Open Source / Desktop only).

### UI surface — top bar bell

```
Top bar:    [Omnipus 🐙]    [agent: Jim]                 [🔔 3]    [⚙ Settings]
                                                          ↓
                                              ┌───────────────────────────┐
                                              │ 5 new Dreamcatcher        │
                                              │ proposals on website-foo  │
                                              │ 2h ago         [Review →] │
                                              ├───────────────────────────┤
                                              │ 2 proposals expiring      │
                                              │ Wed on api-rebuild        │
                                              │ 1d ago         [Review →] │
                                              ├───────────────────────────┤
                                              │ Auto-archived 3 stale     │
                                              │ memories on website-foo   │
                                              │ 4d ago                    │
                                              └───────────────────────────┘
                                              [ Mark all read ]
```

Behavior:

- Badge count = unread Info + Warning + Error notifications.
- Errors stay in the dropdown until manually dismissed.
- Info entries auto-fade after 7 days unless interacted with.
- Each entry has an action link (e.g., `[Review →]`) that deep-links to the relevant view (proposals queue, project memory view, etc.).

### Inline badges

Notifications also surface contextually where the operator naturally lands:

- Command Center project rows: `"website-foo · 5 proposals to review"` chip.
- Memory section nav item: small dot if any project has unreviewed proposals.
- Per-project memory view: header banner if proposals are pending for that project.

### Notification storage

Notifications persist server-side in `<OMNIPUS_HOME>/notifications.jsonl` (append-only, per-operator if multi-user). Read state stored alongside. Phase 1 keeps the SQLite query-by-time simple; phase 2 may move to a structured table when filter complexity grows.

### Delivery channels

| Channel | Phase 1 | Phase 2+ |
|---|---|---|
| In-app (SPA bell + inline badges) | yes | extended sources |
| Desktop notification (Electron) | no — not in OSS scope | yes (Desktop variant) |
| Email | no | yes (SaaS variant) |
| Webhook | no | possibly |

Phase 1 ships in-app only. The notification schema is designed to be channel-agnostic so adding desktop/email/webhook later is wiring, not redesign.

### REST endpoints (informal)

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/notifications?limit=20&unread_only=false` | list notifications, newest first |
| `POST /api/v1/notifications/{id}/ack` | mark one read |
| `POST /api/v1/notifications/ack-all` | mark all read |
| `GET /api/v1/notifications/badge` | unread count + max severity for the top-bar bell |

The badge endpoint is polled (low-frequency, ~30s) initially; WebSocket push for notifications is a phase 2 optimization.

## Cross-cutting decisions

| # | Decision | Rationale |
|---|---|---|
| SN1 | Two settings tabs added: Memory, Dreamcatcher | Each tab maps to one domain; tuning knobs colocated with the feature they affect |
| SN2 | Notifications are a platform feature, not memory-specific | Phase 1 sources are memory; future phases add sandbox, tasks, channels — same UI surface |
| SN3 | Notification storage is JSONL append-only at gateway scope | Simple, queryable, survives restarts; consistent with the project's "JSONL only for Omnipus data" principle (no SQLite anywhere) |
| SN4 | Phase 1 in-app only; desktop/email/webhook reserved as channels for later variants | Scope discipline — ship the OSS surface first |
| SN5 | "Advanced" sections collapse drift weights, cluster thresholds | Operators shouldn't have to see tuning numbers unless they need to |
| SN6 | Notification retention is tier-based: errors forever, warnings 90d, info 30d. All configurable | Q13 — operational forensics need permanence; routine info shouldn't accumulate forever |
| SN7 | Mute Dreamcatcher per project supported via Dreamcatcher settings tab | Q-followon — operators with many projects need to suppress noise from inactive ones |

### Tier-based notification retention (SN6)

| Severity | Default retention | Configurable | Rationale |
|---|---|---|---|
| Error | forever (until manually purged) | yes | Operational evidence; a 6-month-old failed Dreamcatcher run is still relevant |
| Warning | 90 days | yes | Resolves itself or escalates; 3-month window covers typical sprint cadence |
| Info | 30 days | yes | Routine; older info is noise |

Auto-prune sweep runs daily. Pruned entries move to `<OMNIPUS_HOME>/notifications-archived.jsonl.gz` (gzipped) before deletion if the operator wants compliance retention; otherwise discarded.

### Mute Dreamcatcher per project (SN7)

Added to the Dreamcatcher settings tab:

```
Per-project notification mute
  ☐ website-foo                   (mute Dreamcatcher proposal notifications)
  ☐ api-rebuild                   (mute Dreamcatcher proposal notifications)
  ☐ research-agent                (mute Dreamcatcher proposal notifications)
```

Muting a project suppresses *all* Dreamcatcher-source notifications for that project — proposal-generated, expiry-warning, run-failed. Other notification sources (auto-archive, memory size violations) are not affected by the mute.

## Remaining open questions

1. **Per-operator notification preferences** in multi-user (SaaS) deployments — out of scope for OSS phase 1, but reserve a `mute_categories` field in the schema for forward-compatibility.
2. **Quiet hours** — should the bell badge be suppressed during 22:00-06:00 local? Deferred to phase 2; not in OSS v1.
3. **Per-source mute granularity** — phase 1 ships project-level mute for Dreamcatcher only. Memory-source mute (auto-archive notifications), task-source mute (when phase 2 lands) — all defer to later UX iteration.

## Sequencing

After memory-redesign and projects-ui land. Within this work:

1. Settings tabs (read-only first, mapped to existing config keys).
2. Notification storage (JSONL writer + REST endpoints).
3. SPA top-bar bell + dropdown.
4. Inline badges (Command Center project rows, memory nav item).
5. Notification triggers wired into Dreamcatcher and memory subsystems.

## References

- `docs/internal/design/memory-redesign-2026-05.md` — Memory and Dreamcatcher mechanics that the settings tabs configure.
- `docs/internal/design/projects-ui-2026-05.md` — UI surfaces the bell badge and inline badges integrate with.
- `docs/internal/design/sandbox-redesign-2026-05.md` — phase 2 notification source.
- `docs/internal/design/tasks-redesign-2026-05.md` — phase 2 notification source (task due dates, reassignment).
