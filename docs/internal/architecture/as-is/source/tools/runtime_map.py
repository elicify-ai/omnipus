"""Hand-laid SVG: Omnipus while it runs (processes, queues, workers, stores)."""
import html
F = "Inter,Helvetica,Arial,sans-serif"; M = "'JetBrains Mono',ui-monospace,monospace"
def e(s): return html.escape(s)

def box(x, y, w, h, title, lines, col, sub=None, rx=12, fs=13.5):
    out = [f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}" fill="{col}" fill-opacity=".13" stroke="{col}" stroke-width="1.6"/>',
           f'<text x="{x+12}" y="{y+22}" font-family="{F}" font-size="{fs}" font-weight="700" fill="#F8FAFC">{e(title)}</text>']
    for i, ln in enumerate(lines):
        out.append(f'<text x="{x+12}" y="{y+42+i*16}" font-family="{F}" font-size="12" fill="#CBD5E1">{e(ln)}</text>')
    if sub:
        out.append(f'<text x="{x+12}" y="{y+h-9}" font-family="{M}" font-size="9.5" fill="#8B93A1">{e(sub)}</text>')
    return "".join(out)

def arrow(pts, col, label=None, lx=None, ly=None, dash=False, num=None, anchor="start"):
    d = "M" + " L".join(f"{x} {y}" for x, y in pts)
    da = ' stroke-dasharray="7 5"' if dash else ""
    out = [f'<path d="{d}" fill="none" stroke="{col}" stroke-width="2.2"{da} marker-end="url(#rm{col[1:]})"/>']
    if label:
        tx = lx if lx is not None else (pts[0][0] + pts[-1][0]) / 2
        ty = ly if ly is not None else (pts[0][1] + pts[-1][1]) / 2 - 6
        if num:
            out.append(f'<circle cx="{tx-12 if anchor=="start" else tx+12}" cy="{ty-4}" r="9" fill="{col}"/><text x="{tx-12 if anchor=="start" else tx+12}" y="{ty}" text-anchor="middle" font-family="{F}" font-size="10.5" font-weight="700" fill="#0A0A0B">{num}</text>')
            tx = tx + 2 if anchor == "start" else tx - 2
        for i, ln in enumerate(label.split("\n")):
            out.append(f'<text x="{tx}" y="{ty+i*14}" text-anchor="{anchor}" font-family="{F}" font-size="11.5" fill="{col}">{e(ln)}</text>')
    return "".join(out)

GOLD, BLUE, PURP, TEAL, RED, AMB, GREY, PINK = "#D4AF37", "#93C5FD", "#C084FC", "#2DD4BF", "#F87171", "#FBBF24", "#94A3B8", "#F472B6"

def svg():
    W, H = 1460, 920
    p = []
    # outside: ways in
    p.append(f'<text x="20" y="30" font-family="{F}" font-size="12" letter-spacing="2" fill="#64748B">OUTSIDE</text>')
    p.append(box(20, 50, 210, 70, "Browser tab (web app)", ["chat socket + REST"], BLUE))
    p.append(box(20, 300, 210, 70, "Chat apps", ["Telegram, Slack, Discord, …"], BLUE))
    p.append(box(20, 470, 210, 70, "Command line", ["REST with CLI token"], BLUE))
    # server process frame
    p.append(f'<rect x="270" y="40" width="880" height="700" rx="22" fill="#D4AF37" fill-opacity=".035" stroke="#D4AF37" stroke-opacity=".55" stroke-dasharray="3 6"/>')
    p.append(f'<text x="290" y="64" font-family="{F}" font-size="12" letter-spacing="2" fill="{GOLD}">THE OMNIPUS SERVER: ONE MAIN PROCESS, MANY GOROUTINES (LIGHTWEIGHT THREADS) + CHILD PROGRAMS, WEBRTC SOCKETS</text>')
    p.append(box(290, 80, 380, 112, "HTTP server, one port", ["owned and started by the chat-app manager:", "if every chat app fails to start, so does the web app", "sign-in checked per route class (some routes are open)"], GOLD, "pkg/channels/manager.go::StartAll"))
    p.append(box(700, 80, 430, 112, "Chat socket + session hub", ["one hub per conversation numbers and keeps every", "frame; the server pushes it over the browser's own", "WebSocket to each tab attached to that session;", "a reopened tab catches up from the hub"], GOLD, "pkg/gateway/websocket*.go, ws_session_hub.go"))
    p.append(box(290, 250, 380, 92, "Chat-app connectors", ["text + media send workers per app; typing and", "placeholder before routing; unbound app = dropped"], GOLD, "pkg/channels/base.go::HandleMessage; routing/route.go"))
    p.append(box(700, 250, 430, 92, "Message bus: in-memory queue, 64 inbound places", ["web chat, chat apps, helper results, plan wake-ups", "lost if the process dies; web chat waits up to 5 s"], PINK, "pkg/bus/bus.go"))
    p.append(box(700, 392, 430, 100, "Agent loop: one reader, one worker per conversation", ["queued messages of one conversation run in order;", "at the limit a NEW conversation is refused", "(\"I'm at capacity\"), not queued"], PURP, "pkg/agent/loop.go::Run; session_worker.go"))
    p.append(box(290, 392, 380, 100, "Background-work producers (5 primary)", ["3 copies of the schedule engine: heartbeats,", "Calendar, chat loops · task poller every 1 min", "· plan engine tick every 30 s"], AMB, "pkg/cron (x3); heartbeat/task_drain.go; plan_engine.go"))
    p.append(box(290, 530, 380, 100, "Task executor", ["one atomic claim; slot limit for non-MAIN runs", "(MAIN-mode runs: launcher admission first);", "retries until Judge accepts (attempt budget, default 3)"], AMB, "pkg/task/claim.go; agent/task_run_loop.go"))
    p.append(box(700, 530, 430, 92, "Turn engine", ["prepare -> [ call model -> run tools ]* -> finish", "goal check after the turn; Judge in background"], PURP, "pkg/agent/loop.go::runTurn; goal_loop.go"))
    p.append(box(925, 645, 205, 86, "Tool gates", ["policy, ask, limits;", "one tool after another"], PURP, "loop_run_turn_tools.go", fs=13))
    p.append(box(700, 645, 205, 86, "Approval wait", ["in memory, default 600 s", "(configurable); gone after restart"], RED, "gateway/approvals.go", fs=13))
    p.append(box(290, 645, 380, 86, "Helpers (delegation)", ["own goroutine; parent does not wait;", "result returns via inbox or the bus"], PURP, "pkg/agent/steer_*.go", fs=13))
    # outside right
    p.append(f'<text x="1190" y="30" font-family="{F}" font-size="12" letter-spacing="2" fill="#64748B">OUTSIDE</text>')
    p.append(box(1190, 520, 250, 92, "Model providers", ["fallback chain; stops once text", "has streamed or the user cancelled"], RED, "pkg/providers/fallback.go"))
    p.append(box(1190, 640, 250, 116, "Child programs", ["shell + background shells; stdio MCP", "servers (long-lived); Chromium for the", "browser tool. Program starts use the", "kernel sandbox policy"], RED, "pkg/sandbox/hardened_exec.go"))
    p.append(box(1190, 392, 250, 100, "Third-party agents", ["outside agent programs", "driven by a helper in the", "agent's real workspace"], RED, "pkg/agent/external_dispatch.go"))
    # disk
    p.append(f'<text x="20" y="790" font-family="{F}" font-size="12" letter-spacing="2" fill="{TEAL}">ON DISK ($OMNIPUS_HOME): SELECTED STORES</text>')
    stores = [("sessions/", ["one store for ALL agents:", "what the user sees + the", "model's working window"]),
              ("agents/<id>/SOUL.md", ["SOUL.md = the agent's prompt;", "the workspace's AGENT.md =", "project instructions"]),
              ("memory notes", ["newest 20 notes packed", "into every prompt (8,000", "character budget)"]),
              ("tasks/  plans/", ["task + run history,", "plan records with", "state + phase"]),
              ("3x jobs.json", ["heartbeats, Calendar", "triggers, chat loops", "(one file each)"]),
              ("system/audit.jsonl", ["security activity: rows", "written at policy, approval", "and tool steps"]),
              ("credentials.json", ["encrypted; provider keys", "copied into the process", "environment at boot + reload"])]
    for i, (t, ls) in enumerate(stores):
        p.append(box(20 + i * 205, 805, 192, 100, t, ls, TEAL, rx=18, fs=13))
    # arrows
    p.append(arrow([(230, 85), (290, 120)], BLUE, "1 message frame", 236, 78))
    p.append(arrow([(670, 120), (700, 120)], BLUE))
    p.append(arrow([(915, 192), (915, 250)], BLUE, "2 save user line,\nthen publish", 925, 214))
    p.append(arrow([(230, 335), (290, 300)], BLUE, "1", 250, 334))
    p.append(arrow([(670, 296), (700, 296)], BLUE))
    p.append(arrow([(915, 342), (915, 392)], PINK, "3 one reader takes it", 925, 370))
    p.append(arrow([(915, 492), (915, 530)], PURP, "4 run the turn", 925, 515))
    p.append(arrow([(480, 492), (480, 530)], AMB, "Calendar, plans,\npoller -> task", 470, 506, anchor="end"))
    p.append(arrow([(670, 576), (700, 576)], AMB, None))
    p.append(arrow([(670, 430), (685, 430), (685, 560), (700, 560)], AMB, "heartbeats + loops: DIRECT call\n(no bus, no routing)", 695, 506, dash=True))
    p.append(arrow([(1130, 560), (1190, 560)], RED, "5 model", 1136, 552))
    p.append(arrow([(1027, 622), (1027, 645)], PURP))
    p.append(arrow([(925, 688), (905, 688)], RED, "ask", 915, 680, anchor="middle"))
    p.append(arrow([(1130, 688), (1190, 688)], RED, "6 start", 1136, 680))
    p.append(arrow([(700, 600), (670, 660)], PURP, "delegate", 640, 640, anchor="end"))
    p.append(arrow([(480, 731), (480, 755), (1168, 755), (1168, 450), (1190, 450)], RED, "a helper may drive a third-party agent", 1000, 769, dash=True))
    p.append(arrow([(1100, 530), (1100, 192)], GOLD, "7 live tokens + tool events", 1106, 510, dash=True))
    p.append(arrow([(700, 605), (686, 605), (686, 800)], TEAL, "8 save window + transcript", 696, 785))
    p.append(arrow([(230, 505), (252, 505), (252, 175), (290, 175)], BLUE, "REST", 236, 498))
    defs = "".join(f'<marker id="rm{c[1:]}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0 0 L10 5 L0 10 z" fill="{c}"/></marker>'
                   for c in (GOLD, BLUE, PURP, TEAL, RED, AMB, GREY, PINK))
    return (f'<svg viewBox="0 0 {W} {H}" width="{W}" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Omnipus while it runs" style="max-width:100%;height:auto">'
            f'<defs>{defs}</defs>{"".join(p)}</svg>')
