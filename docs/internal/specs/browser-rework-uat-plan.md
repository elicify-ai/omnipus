# UAT plan — the browser rework (ADR-072 D1 + D2)

**Audience:** a human tester sitting in front of the Omnipus web UI with a terminal open. No
test-framework knowledge is assumed and none is needed. Nothing in this plan is automated.

**Scope:** the two settled specifications for the browser rework —

| Document | Absolute path | What it settles |
|---|---|---|
| ADR-072 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md` | The decisions |
| Ownership spec (**D1**) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-workspace-ownership-spec.md` | Who owns which browser and which tabs |
| Capability spec (**D2**) | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/docs/internal/specs/browser-agent-capability-spec.md` | The six new browser actions, and how elements are found |

**Status:** draft for review. This plan does not decide anything; where the specs are silent, the
question is recorded in §8 rather than answered here.

---

## 1. Purpose, and what "pass" means

### 1.1 What is being tested, in plain terms

Today every agent shares one Chrome browser and one set of tabs, and handing a browsing task from
one agent to another loses the tab. The rework changes three things.

1. **One Chrome per workspace.** Each workspace gets its own Chrome process with its own profile —
   the folder Chrome keeps cookies and logins in. So a login made in workspace A is invisible in
   workspace B, and shared by every agent on workspace A.
2. **Tabs belong to the conversation, not the agent.** A tab opened during a chat stays with that
   chat whichever agent you switch to. A different chat in the same workspace does not see it. A tab
   *you* open in the live panel is the workspace's, and every agent on the workspace can see and
   drive it.
3. **Six new browser actions**, plus a better way of finding things on a page: an agent names an
   element by its *role* and its *visible name* ("the button called Submit") instead of by a CSS
   selector, and the system checks the element is genuinely clickable before clicking it.

Underneath all three sits **memory-based admission**: when the machine is short of memory, the
system closes an idle browser or refuses to start a new one, rather than driving the machine into
swap.

### 1.2 Why this plan exists at all

This project has repeatedly shipped changes that **reported success and changed nothing** — a
security control that said "saved" and altered no behaviour, a guard test that passed with the thing
it guarded deleted, a default-agent setting that returned `200 OK` and changed no routing. Both
specs are written against that failure mode, and so is this plan.

That is why **every test case carries a mandatory "what a silent failure looks like" field.** A case
is not finished when the expected result appears. It is finished when the tester has also confirmed
that the *specific wrong-but-plausible outcome* named in that field did not happen. If a tester
cannot tell the two apart from where they are sitting, that itself is the finding, and it goes in the
defect report.

### 1.3 What a tester is and is not asked to judge

| The tester **is** asked to | The tester is **not** asked to |
|---|---|
| Observe what the UI shows and what the agent says | Read Go or TypeScript source |
| Read `$OMNIPUS_HOME/logs/gateway.log` when a case says to | Interpret a stack trace |
| Notice when something is *slow* as well as when it is *wrong* | Measure milliseconds; "does this feel laggy" is the right resolution |
| Report a message that is technically correct but useless to a person | Decide whether the design is right |

### 1.4 Exit condition for the whole UAT

The UAT is **complete and passing** when all four hold:

1. **Every P0 case passes**, with its silent-failure check explicitly performed and recorded. A P0
   case that could not be run counts as a failure, not as "not applicable".
2. **Every P1 case passes, or has a filed defect with an agreed severity and a named owner.** A P1
   case may not be closed as "works on my machine" or "did not reproduce" without a second tester
   attempting it.
3. **No open defect at severity S1 or S2** (§6.2).
4. **The coverage map in §7 is filled in**, including its second half — the honest list of things
   the specs require that a human at the UI *cannot* observe. That list is expected to be non-empty.
   An empty one means somebody claimed coverage they do not have.

**The UAT fails outright**, regardless of case counts, if any of the following is observed at any
point:

- An agent reports that it did something to a page, and the page did not change (§5, N-1).
- A tool returns `deferred` and the agent treats that as done (§5, N-2).
- Two different chats can see each other's tabs (§5, N-3).
- The live browser panel is visibly slow but shows no error (§5, N-6) — the fast video path failed
  and something degraded silently, which this project deleted the old fallback specifically to
  prevent.

### 1.5 How to record a run

One row per case in a spreadsheet or a shared doc, with these columns:

`Case ID | Tester | Date | Result (Pass / Fail / Blocked) | Silent-failure check performed? (Y/N) | Defect ID | Notes`

The **"silent-failure check performed?"** column is not optional and not a formality. A `Pass` with
`N` in that column is treated as `Blocked`.

---

## 2. Test environment setup

Everything below is a literal command. Run them in the order given. Absolute paths throughout.

### 2.1 Prerequisites

| Requirement | Why | How to check |
|---|---|---|
| macOS or Linux | Windows is `degraded-unsupported` for the browser pool (D1 US-15/AC18) | — |
| Go on the `PATH` | Building the binary | `go version` |
| Node and npm | Building the web UI | `node --version && npm --version` |
| Google Chrome or Chromium installed | The browser being pooled | Launch it once by hand |
| At least 8 GB RAM | §4.G needs headroom to take away | `top` / Activity Monitor |
| An LLM provider key you already hold | The agents need a model that supports tool use | See §3.2 — **you enter this once, in the app's own Settings, and nowhere else** |

### 2.2 Build the binary with the web UI embedded

The binary serves the web UI from a copy embedded at build time. If you skip the copy step you will
test a **stale** interface and every UI-facing case in this plan becomes meaningless.

```bash
cd /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf

# 1. Build the web UI
npm ci
npm run build                                    # writes dist/spa/

# 2. Sync it into the embed directory (do not skip)
rm -rf /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/pkg/gateway/spa
mkdir -p /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/pkg/gateway/spa
cp -r /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/dist/spa/* \
      /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/pkg/gateway/spa/

# 3. Build the binary
CGO_ENABLED=0 go build -tags goolm,stdjson \
  -o /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/omnipus-uat \
  ./cmd/omnipus/
```

**Verify the embed actually happened** before going further. Pick any string you know is new in this
build (a label from the browser panel, say) and confirm it is present:

```bash
grep -c "Retry" /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/pkg/gateway/spa/assets/index-*.js
```

A result of `0` means step 2 did not take. Stop and redo it.

> **Do not test against the Vite development server** (`npm run dev`). It proxies API calls to a
> separate process and does not exercise the embedded UI. Every case in this plan assumes the Go
> binary is serving both.

### 2.3 Start a clean gateway

```bash
export OMNIPUS_HOME=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home
rm -rf "$OMNIPUS_HOME"
mkdir -p "$OMNIPUS_HOME"

/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/omnipus-uat gateway
```

Leave that terminal running — it is your gateway. Open a **second** terminal for everything else.

Then open `http://localhost:5000/` in your own browser and complete onboarding.

**If the gateway will not start:**

| Symptom | Cause | Fix |
|---|---|---|
| Exits immediately, nothing on screen | Startup panic | Read `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/logs/gateway_panic.log` |
| "address already in use" | Port 5000 taken | Edit `gateway.port` in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/config.json` and restart |
| Every agent turn 404s | The chosen model does not support tool use | Switch to a tool-capable model (§3.2) |

Runtime log for the whole run: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/logs/gateway.log`

### 2.4 The fixture the whole plan is built on

Build this once. Every case refers back to it by name.

| Thing to create | Name to use | Where in the UI |
|---|---|---|
| Workspace 1 | **Alpha** | Workspaces → New |
| Workspace 2 | **Bravo** | Workspaces → New |
| Agents on Alpha's team | **Jim** and **Ray** (both seeded; both have browser tools) | Alpha → Team |
| Agents on Bravo's team | **Jim** and **Ray** | Bravo → Team |
| Chat A1 | a chat in workspace **Alpha** | Alpha → Chat → New |
| Chat A2 | a **second, separate** chat in workspace **Alpha** | Alpha → Chat → New |
| Chat B1 | a chat in workspace **Bravo** | Bravo → Chat → New |

> **Why Jim and Ray specifically.** On a fresh install, Jim and Ray resolve `allow` for the browser
> tools; **Mia and Ava resolve `deny` for all six** (D2 US-12/AC2, AC3). That is deliberate and is
> itself tested (UAT-08). Do not "fix" it by changing policy — several cases depend on it.

### 2.5 The scheduled / background run

Several cases need work that runs with **nobody watching**, because that is when the interesting
failures happen (a tab it should not have inherited; an approval nobody can answer).

1. Go to **Alpha → Calendar**.
2. Create a recurring entry, every 5 minutes, assigned to **Ray**.
3. Give it this instruction, verbatim:
   > *List the browser tabs you can currently see, then open `https://example.com/scheduled-marker`
   > and report the page title.*
4. Save it. Note the **local time of the next run** — you will need to be watching.

> Scheduling in this product is done through the workspace **Calendar**. There is no Command Center
> and no cron entry field anywhere in the UI; if you find one, that is a defect in its own right.

### 2.6 Where to look when a case says "check the log"

```bash
tail -f /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/logs/gateway.log
```

Useful greps, all one line:

```bash
# Chrome processes currently running, one line each
ps ax | grep -i "user-data-dir" | grep -v grep

# The per-workspace profile directories
ls -la /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/browser/profiles/

# Disk used per workspace profile
du -sh /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/browser/profiles/*
```

> **`ps ax | grep` only.** Do not use `pkill -f`, and do not pipe `lsof` into `kill`. Several cases
> ask you to kill a *specific* Chrome; they give you the exact, safe command at the point of use.

---

## 3. Test data and accounts

### 3.1 The hard rule

> ## ⛔ NO REAL CREDENTIALS. EVER.
>
> **A tester must never type a real password, a real API key, a real payment card, a real personal
> email account, or any other genuine credential into any page driven by this build, or into any
> page opened in the live browser panel.**
>
> This is not a preference and it is not about tidiness. Under this rework a login established in a
> workspace becomes usable by **every agent on that workspace**, **including on turns nobody is
> watching** (D1 US-21). A snapshot of a signed-in page **deliberately includes field values,
> passwords among them** (D2 US-10/AC3). A real credential entered during this UAT is a real
> credential handed to an unattended process and written into a profile directory on disk.
>
> **If a case seems to require a real account, it does not. Stop and raise it in §8.**

### 3.2 The one exception, and its boundary

You will need **one LLM provider API key** so the agents can think at all. That key is entered
**once**, in the app's own **Settings → Providers** screen, and **nowhere else** — never into a web
page inside the browser panel, never into a test site, never into a chat message.

Use a key you can revoke, and revoke it when the UAT is over.

Pick a model that supports tool use. Known-good: `z-ai/glm-5-turbo`, `google/gemini-2.5-flash`,
`anthropic/claude-3.5-haiku`. A model without tool support returns 404 on every turn and every case
in this plan will fail for the wrong reason.

### 3.3 The throwaway login site

Profile isolation (§4.B) can only be proven by logging in somewhere and checking the login did or did
not carry over. Use a public practice site that **publishes its own dummy account on the login page**.

| | |
|---|---|
| **Site** | `https://the-internet.herokuapp.com/login` |
| **Username** | `tomsmith` |
| **Password** | `SuperSecretPassword!` |
| **Why it is safe** | Both values are printed on the login page itself, for anyone, deliberately. The account owns nothing and protects nothing. |
| **How you know you are logged in** | The page shows "You logged into a secure area!" and `/secure` is reachable |
| **How you know you are logged out** | `/secure` bounces back to `/login` |

If that host is down, an equivalent substitute must meet **all three** of these, and the substitute
must be recorded in the run log:

1. Its credentials are published publicly on the site itself.
2. The account holds no data belonging to anyone.
3. Signing in there cannot affect anything outside that site.

### 3.4 The rest of the test pages

The same host carries a page for each of the six new actions, so the whole of §4.D runs against one
domain:

| What you need | Page |
|---|---|
| A real `<select>` dropdown | `https://the-internet.herokuapp.com/dropdown` |
| A keyboard-only flow | `https://the-internet.herokuapp.com/key_presses` |
| A menu that only appears on hover | `https://the-internet.herokuapp.com/hovers` |
| A file upload form | `https://the-internet.herokuapp.com/upload` |
| A page that raises `alert` / `confirm` / `prompt` | `https://the-internet.herokuapp.com/javascript_alerts` |
| A form with several labelled fields, for the snapshot | `https://the-internet.herokuapp.com/login` |
| Generated / unstable class names, for role+name targeting | any of the above — none of them expose stable, meaningful class names |

**The file to upload** — create it yourself, and make it obviously worthless:

```bash
mkdir -p /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/uat-files
echo "omnipus uat upload fixture, no real content" > \
  /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/uat-files/uat-upload.txt
```

Never upload a real document.

### 3.5 A second file, deliberately out of bounds

One case (UAT-24) checks that an agent cannot upload a file from outside the area it is allowed to
read. Create a harmless decoy somewhere the agent has no business reaching:

```bash
echo "out of bounds decoy, no real content" > /tmp/uat-out-of-bounds.txt
```

This file must also contain nothing real. The point of the case is the refusal, not the file.

---
