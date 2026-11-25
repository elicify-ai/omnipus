"""Build the formal as-is architecture document (Markdown + SVG files, and an HTML edition) from flows/*.yaml.
Usage: build_formal.py <source_dir> <md_out> <svg_dir_rel_to_md> <html_out or ->
Example (repo root): python3 docs/internal/architecture/as-is/source/tools/build_formal.py docs/internal/architecture/as-is/source docs/internal/architecture/AS-IS-architecture.md as-is -"""
import yaml, html, sys, os, re
W = sys.argv[1]; MD = sys.argv[2]; SVGREL = sys.argv[3]; HT = sys.argv[4]
sys.path.insert(0, os.path.join(W, "tools"))
import seq, runtime_map
CODE = "release/v0.1.1 @ 6fbf4b43c"
DATE = "2026-11-25"
ORDER = ["web-chat-turn", "connector-message", "tool-call", "delegation-and-stop",
         "scheduled-work", "plans-and-goals", "boot-and-recovery", "prompt-and-memory", "calendar-task"]
SHORT = {"web-chat-turn": "Web chat message to answer", "connector-message": "Chat-app message to answer",
         "tool-call": "One tool call", "delegation-and-stop": "Helpers and Stop",
         "scheduled-work": "Work that starts on its own", "plans-and-goals": "Plans, tasks and goals",
         "boot-and-recovery": "Start-up and restart", "prompt-and-memory": "What the model is sent",
         "calendar-task": "A Calendar task, end to end"}
flows = {k: yaml.safe_load(open(f"{W}/flows/{k}.yaml")) for k in ORDER}
extra = yaml.safe_load(open(f"{W}/extra.yaml")) if os.path.exists(f"{W}/extra.yaml") else {}
BG = '<rect x="-10000" y="-10000" width="40000" height="40000" fill="#0A0A0B"/>'
def with_bg(svg):  # dark background so light text stays readable on GitHub's light theme
    i = svg.index(">", svg.index("<svg")) + 1
    return svg[:i] + BG + svg[i:]
def ncalls(d): return sum(1 for s in d["steps"] if "from" in s)
def unverified(d): return sum(1 for s in d["steps"] if ("from" in s or "over" in s) and not s.get("verified", False))
def mdcell(s): return str(s).replace("|", "\\|").replace("\n", " ")
def mdtable(rows, heads, keys):
    if not rows: return ""
    out = ["| " + " | ".join(heads) + " |", "|" + "---|" * len(heads)]
    for r in rows:
        out.append("| " + " | ".join((f"`{mdcell(r.get(k,''))}`" if k == "ref" and r.get(k) else mdcell(r.get(k, ""))) for k in keys) + " |")
    return "\n".join(out) + "\n"

svgdir = os.path.join(os.path.dirname(MD), SVGREL); os.makedirs(svgdir, exist_ok=True)
open(os.path.join(svgdir, "00-running-server.svg"), "w").write(with_bg(runtime_map.svg()))
svgs = {}
for i, k in enumerate(ORDER, 1):
    s = seq.render(flows[k], f"f{i}")
    s = s.replace('<svg ', '<svg ', 1)
    if 'xmlns=' not in s[:400]: s = s.replace('<svg ', '<svg xmlns="http://www.w3.org/2000/svg" ', 1)
    svgs[k] = with_bg(s)
    open(os.path.join(svgdir, f"{i:02d}-{k}.svg"), "w").write(svgs[k])

INTRO = extra.get("intro", "")
import glob as _g0
def _slug(t): return re.sub(r"[^a-z0-9 -]", "", t.lower()).strip().replace(" ", "-")
SECTION_TOC = [(m, _slug(m)) for fn in sorted(_g0.glob(os.path.join(W, "sections", "*.md"))) for m in re.findall(r"^## (.+)$", open(fn).read(), re.M)]
LEGEND = ("**How to read a flow diagram:** lanes are the parts that act; time runs downward. Solid arrow = a call that waits for its answer; "
          "dashed blue = handed over without waiting (a queue or a frame); grey dashed = an answer coming back; teal = written to disk; "
          "gold box = what happens inside a part; purple frame = repeats; red frame = only if the condition holds. "
          "Each step shows its source file and function; an amber \"not verified\" badge marks a step that was inferred, not opened in the code.")
tot_calls = sum(ncalls(flows[k]) for k in ORDER)

md = [f"# Omnipus — As-Is Architecture\n",
      f"| | |\n|---|---|\n| Status | Formal current-state document of the Omnipus runtime |\n| Code described | `{CODE}` |\n| Date | {DATE} |\n| Owner | Chief Architect |\n| Rule | The code wins. Where this document and the code disagree, the code is right and this document must be corrected. |\n",
      "## Bottom line\n", INTRO.strip() + "\n" if INTRO else "",
      f"Omnipus is one main Go server process (plus child programs such as the browser and tool servers). Work enters through one HTTP port (web chat, REST, chat-app webhooks) or starts on its own from one of five primary work producers (three copies of the schedule engine, the task poller and the plan engine tick). "
      f"Chat messages wait in a 64-place in-memory queue; each conversation gets its own worker, which takes the ordinary queued messages of that conversation one at a time; scheduled runs, task runs and helper launches enter through their own admission paths. "
      f"A turn builds the prompt, calls the model, and runs the tools it asks for one after another through policy, approval and sandbox gates. "
      f"Most state is saved under one data folder as JSON or JSONL files; the credential store is encrypted, some indexes are databases, and queues and approvals live only in memory (see \"Where data lives\"). This document shows that picture once, then {len(ORDER)} flows traced step by step in the code "
      f"({tot_calls} calls, each with its source reference), each with what is saved, what happens on failure, and the key facts that are easy to get wrong.\n",
      "## Contents\n", "1. [The server while it runs](#the-server-while-it-runs)\n" +
      "".join(f"{i+1}. [Flow {i}: {SHORT[k]}](#flow-{i})\n" for i, k in enumerate(ORDER, 1)) +
      "".join(f"- [{t}](#{a})\n" for t, a in SECTION_TOC) + "".join(f"- [{t}](#{a})\n" for t, a in extra.get("toc_tail", [])),
      "## The server while it runs\n",
      "One program. Work comes in through one HTTP port or starts from one of five primary work producers. Chat messages wait in a 64-place in-memory queue; "
      "each conversation gets its own worker for ordinary queued messages; other entry paths have their own admission. A turn calls the model and runs tools one after another. Selected state is saved on disk (see \"Where data lives\").\n",
      f"![The server while it runs]({SVGREL}/00-running-server.svg)\n",
      "Numbers 1–8 follow one web chat message. Amber = work that starts on its own; it skips the queue and the agent-picking step. "
      "Red = outside the server or able to block. Teal = files on disk.\n", LEGEND + "\n"]
for i, k in enumerate(ORDER, 1):
    d = flows[k]
    md.append(f'<a id="flow-{i}"></a>\n\n## Flow {i}: {SHORT[k]}\n\n**{d["title"]}**\n')
    md.append(re.sub(r"\s+", " ", d.get("summary", "")).strip() + "\n")
    uv = unverified(d)
    md.append(f"![{d['title']}]({SVGREL}/{i:02d}-{k}.svg)\n\n_{ncalls(d)} calls; {uv} step(s) not verified in the code._\n")
    for key, title in (("task_state_machine", "Task states"), ("plan_state_machine", "Plan states")):
        sm = d.get(key)
        if sm:
            md.append(f"### {title}\n\n`" + " → ".join(sm["states"]) + "`\n")
            for kk, vv in sm.items():
                if kk not in ("states", "transitions", "ref"): md.append(f"- {kk.replace('_',' ')}: {vv}\n")
            md.append("\n" + mdtable(sm.get("transitions", []), ["From", "To", "When", "Code"], ["from", "to", "by", "ref"]))
    md.append("### What is saved, where, when\n\n" + mdtable(d.get("stores", []), ["What", "Where", "When", "Code"], ["what", "where", "when", "ref"]))
    md.append("### When it goes wrong\n\n" + mdtable(d.get("failures", []), ["Case", "What happens", "Code"], ["case", "effect", "ref"]))
    md.append("### Key facts\n\n" + mdtable(d.get("facts", []), ["Fact", "Code"], ["fact", "ref"]))
import glob as _g
for fn in sorted(_g.glob(os.path.join(W, "sections", "*.md"))):
    md.append(open(fn).read().strip() + "\n")
for sec in extra.get("sections", []):
    md.append(f'<a id="{sec["anchor"]}"></a>\n\n## {sec["title"]}\n\n{sec["body"].strip()}\n')
open(MD, "w").write("\n".join(x for x in md if x))

if HT == "-":
    print("built", MD); sys.exit(0)
# HTML edition: same content, rendered from the Markdown body with inline SVG.
CSS = open(os.path.join(W, "tools", "page.css")).read()
try:
    import markdown
    body = markdown.markdown(open(MD).read(), extensions=["tables", "toc"])
except ImportError:
    sys.exit("python markdown package missing")
for fn in os.listdir(svgdir):
    body = re.sub(rf'<img alt="[^"]*" src="{re.escape(SVGREL)}/{re.escape(fn)}" ?/?>', lambda m: '<div class="fig">' + open(os.path.join(svgdir, fn)).read() + '</div>', body)
open(HT, "w").write(f'<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Omnipus — As-Is Architecture</title><style>{CSS}\nmain{{max-width:1500px;margin:0 auto;padding:24px}} table{{border-collapse:collapse;margin:8px 0 18px}} td,th{{border:1px solid #2a2f3a;padding:6px 8px;vertical-align:top;font-size:13px}} code{{font-size:12px}} .fig{{overflow-x:auto}}</style></head><body><main>{body}</main></body></html>')
print("built", MD, HT)
