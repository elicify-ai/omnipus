"""Render a flow YAML (see flow-format.md) into an SVG sequence diagram."""
import html, textwrap

KIND = {  # lane colours
    "person": "#F5D77A", "browser": "#93C5FD", "server": "#D4AF37", "worker": "#C084FC",
    "store": "#2DD4BF", "external": "#F87171",
}
ARROW = {"call": "#E2E8F0", "async": "#93C5FD", "return": "#94A3B8", "store": "#2DD4BF", "wait": "#FBBF24"}
LANE_W, TOP, HDR_H = 178, 20, 58
FONT = "Inter,Helvetica,Arial,sans-serif"
MONO = "'JetBrains Mono',ui-monospace,monospace"

def esc(s): return html.escape(str(s), quote=True)

def wrap(s, chars):
    out = []
    for para in str(s).split("\n"):
        out += textwrap.wrap(para, max(chars, 8)) or [""]
    return out

def render(flow, uid):
    parts = flow["participants"]
    idx = {p["id"]: i for i, p in enumerate(parts)}
    n = len(parts)
    W = 40 + n * LANE_W
    cx = lambda i: 20 + LANE_W * i + LANE_W / 2
    body, frames, stack = [], [], []
    y = TOP + HDR_H + 22
    stepno = 0
    for st in flow.get("steps", []):
        if "phase" in st:
            y += 8
            body.append(f'<rect x="12" y="{y}" width="{W-24}" height="26" rx="7" fill="#D4AF37" fill-opacity=".10" stroke="#D4AF37" stroke-opacity=".35"/>'
                        f'<text x="24" y="{y+17.5}" font-family="{FONT}" font-size="13" font-weight="600" fill="#F5D77A">{esc(st["phase"])}</text>')
            y += 40; continue
        if "loop" in st or "alt" in st:
            kind = "loop" if "loop" in st else "alt"
            stack.append({"kind": kind, "label": st.get(kind), "y": y, "elses": []})
            y += 30; continue
        if "else" in st:
            if stack: stack[-1]["elses"].append((y, st["else"]))
            y += 28; continue
        if "end" in st:
            if stack:
                f = stack.pop(); depth = len(stack)
                frames.append((f, y + 4, depth))
            y += 16; continue
        if "over" in st:
            ids = st["over"] if isinstance(st["over"], list) else [st["over"]]
            ii = [idx[i] for i in ids if i in idx]
            if not ii: continue
            x1 = cx(min(ii)) - 80; x2 = cx(max(ii)) + 80
            if x2 - x1 < 380:
                c0 = (x1 + x2) / 2; x1, x2 = c0 - 190, c0 + 190
            if x1 < 10: x2 += 10 - x1; x1 = 10
            if x2 > W - 10: x1 -= x2 - (W - 10); x2 = W - 10
            lines = wrap(st.get("note", ""), int((x2 - x1 - 20) / 6.4))
            ref = st.get("ref"); rl = wrap(ref, int((x2 - x1 - 20) / 6.2)) if ref else []
            h = 12 + 16 * len(lines) + 13 * len(rl)
            mark = "" if st.get("verified", True) else ' stroke-dasharray="4 3"'
            body.append(f'<rect x="{x1}" y="{y}" width="{x2-x1}" height="{h}" rx="8" fill="#1f1a0b" stroke="#a8862a"{mark}/>')
            for k, ln in enumerate(lines):
                body.append(f'<text x="{x1+10}" y="{y+19+16*k}" font-family="{FONT}" font-size="12.5" fill="#F1E3B0">{esc(ln)}</text>')
            for k, rr in enumerate(rl):
                body.append(f'<text x="{x1+10}" y="{y+18+16*len(lines)+13*k}" font-family="{MONO}" font-size="10" fill="#8B93A1">{esc(rr)}</text>')
            y += h + 12; continue
        a, b = st.get("from"), st.get("to", st.get("from"))
        if a not in idx or b not in idx: continue
        stepno += 1
        kind = st.get("kind", "call"); col = ARROW.get(kind, "#E2E8F0")
        ia, ib = idx[a], idx[b]
        verified = st.get("verified", True)
        if ia == ib or kind == "self":
            x = cx(ia); lines = wrap(st.get("label", ""), 30)
            h = 16 * len(lines) + 30
            body.append(f'<path d="M{x} {y+6} h44 v{h-14} h-40" fill="none" stroke="{col}" stroke-width="1.6" marker-end="url(#ah{uid}{kind})"/>')
            for k, ln in enumerate(lines):
                body.append(f'<text x="{x+52}" y="{y+16+16*k}" font-family="{FONT}" font-size="12.5" fill="#E5E7EB">{esc(ln)}</text>')
            if st.get("ref"):
                body.append(f'<text x="{x+52}" y="{y+16+16*len(lines)}" font-family="{MONO}" font-size="10" fill="#8B93A1">{esc(st["ref"])}</text>')
            lx = x - 30
        else:
            x1, x2 = cx(ia), cx(ib)
            span = abs(x2 - x1)
            lines = wrap(st.get("label", ""), int(max(span, 220) / 6.7) - 2)
            rl = wrap(st.get("ref", ""), int(max(span, 260) / 6.2)) if st.get("ref") else []
            h = 16 * len(lines) + 13 * len(rl) + 16
            ay = y + h - 4
            mid = min(max((x1 + x2) / 2, 150), W - 150)
            dash = ' stroke-dasharray="6 4"' if kind in ("async", "return") else ""
            body.append(f'<line x1="{x1}" y1="{ay}" x2="{x2 - (6 if x2>x1 else -6)}" y2="{ay}" stroke="{col}" stroke-width="1.8"{dash} marker-end="url(#ah{uid}{kind})"/>')
            for k, ln in enumerate(lines):
                body.append(f'<text x="{mid}" y="{y+10+16*k}" text-anchor="middle" font-family="{FONT}" font-size="12.5" fill="#F1F5F9">{esc(ln)}</text>')
            mid = min(max(mid, 150), W - 150)
            for k, rr in enumerate(rl):
                body.append(f'<text x="{mid}" y="{y+7+16*len(lines)+13*k}" text-anchor="middle" font-family="{MONO}" font-size="10" fill="#8B93A1">{esc(rr)}</text>')
            lx = min(x1, x2) - 30
        badge = "#D4AF37" if verified else "#FBBF24"
        bx = 26 if lx < 26 else lx
        body.append(f'<circle cx="{bx}" cy="{y+8}" r="10" fill="{badge}"/><text x="{bx}" y="{y+12}" text-anchor="middle" font-family="{FONT}" font-size="10.5" font-weight="700" fill="#0A0A0B">{stepno}</text>')
        if not verified:
            body.append(f'<text x="{bx+14}" y="{y+12}" font-family="{FONT}" font-size="10" fill="#FBBF24">not verified</text>')
        y += h + 14
    while stack:
        f = stack.pop(); frames.append((f, y, len(stack)))
    H = y + 20
    fr = []
    for f, yend, depth in frames:
        x1 = 8 + depth * 6; w = W - 16 - depth * 12
        col = "#C084FC" if f["kind"] == "loop" else "#F87171"
        fr.append(f'<rect x="{x1}" y="{f["y"]}" width="{w}" height="{yend-f["y"]}" rx="10" fill="{col}" fill-opacity=".04" stroke="{col}" stroke-opacity=".7" stroke-dasharray="5 4"/>')
        lab = ("repeat: " if f["kind"] == "loop" else "if: ") + str(f["label"])
        tw = 12 + 6.8 * len(lab)
        fr.append(f'<path d="M{x1} {f["y"]} h{tw} l-0 18 l-8 6 h-{tw-8} z" fill="{col}" fill-opacity=".85"/><text x="{x1+8}" y="{f["y"]+16}" font-family="{FONT}" font-size="12" font-weight="600" fill="#0A0A0B">{esc(lab)}</text>')
        for ey, el in f["elses"]:
            fr.append(f'<line x1="{x1}" y1="{ey}" x2="{x1+w}" y2="{ey}" stroke="{col}" stroke-opacity=".6" stroke-dasharray="5 4"/>'
                      f'<text x="{x1+10}" y="{ey+17}" font-family="{FONT}" font-size="12" font-style="italic" fill="{col}">else: {esc(el)}</text>')
    hdr = []
    for i, p in enumerate(parts):
        c = KIND.get(p.get("kind", "server"), "#D4AF37"); x = cx(i)
        hdr.append(f'<line x1="{x}" y1="{TOP+HDR_H}" x2="{x}" y2="{H-8}" stroke="{c}" stroke-opacity=".35" stroke-width="1.5" stroke-dasharray="2 5"/>')
        lines = wrap(p["label"], 22)[:3]
        hy = TOP + (HDR_H - 16 * len(lines)) / 2 + 12
        shape = 'rx="22"' if p.get("kind") in ("store",) else 'rx="10"'
        hdr.append(f'<rect x="{x-LANE_W/2+8}" y="{TOP}" width="{LANE_W-16}" height="{HDR_H}" {shape} fill="{c}" fill-opacity=".16" stroke="{c}" stroke-width="1.5"/>')
        for k, ln in enumerate(lines):
            hdr.append(f'<text x="{x}" y="{hy+16*k}" text-anchor="middle" font-family="{FONT}" font-size="12.5" font-weight="600" fill="#F8FAFC">{esc(ln)}</text>')
    defs = "".join(
        f'<marker id="ah{uid}{k}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">'
        + (f'<path d="M0 0 L10 5 L0 10 z" fill="{c}"/>' if k not in ("async",) else f'<path d="M0 0 L10 5 L0 10" fill="none" stroke="{c}" stroke-width="1.6"/>')
        + '</marker>' for k, c in ARROW.items())
    return (f'<svg viewBox="0 0 {W} {H}" width="{W}" role="img" aria-label="{esc(flow["title"])}" xmlns="http://www.w3.org/2000/svg" style="width:100%;max-width:{W}px;min-width:{int(W*0.88)}px;height:auto">'
            f'<defs>{defs}</defs>{"".join(fr)}{"".join(hdr)}{"".join(body)}</svg>')
