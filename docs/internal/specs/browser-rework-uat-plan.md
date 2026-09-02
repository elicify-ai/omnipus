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
| Agents on Bravo's team | **Jim** only — **not Ray** | Bravo → Team |
| Chat A1 | a chat in workspace **Alpha** | Alpha → Chat → New |
| Chat A2 | a **second, separate** chat in workspace **Alpha** | Alpha → Chat → New |
| Chat B1 | a chat in workspace **Bravo** | Bravo → Chat → New |

> **Why Ray is on Alpha only and Jim is on both.** Two cases need this exact shape and would be
> impossible otherwise. A background run needs an agent that belongs to **exactly one** workspace, so
> the system can work out which browser it means (UAT-05). And a background run for an agent on
> **two** workspaces must be *refused as ambiguous* rather than quietly given one of them (UAT-07,
> D1 FR-033). Jim gives you the second case; Ray gives you the first. Do not "tidy up" the rosters.

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

## 4. Test cases

### 4.0 How to read a case

Every case has the same seven parts. Two of them are the point of the whole plan:

- **Expected observable result** — what you should see if the build is correct.
- **What a silent failure looks like** — the specific wrong outcome that *resembles* success. You
  must actively check for this. If you cannot tell it apart from the expected result from where you
  are sitting, write that down: an unobservable difference is itself a defect (§5, N-4).

Priorities: **P0** must pass to ship. **P1** must pass or carry an agreed, owned defect. **P2** is
information — a failure is recorded, not blocking.

"Traces to" cites the source document: **D1** = ownership spec, **D2** = capability spec, **ADR** =
ADR-072, **H-n** = that document's numbered holdout scenario in its §13.

### 4.1 Case index

| Group | Cases | Theme |
|---|---|---|
| **A** | UAT-01 … UAT-08 | Ownership and handover |
| **B** | UAT-09 … UAT-12 | Workspace isolation |
| **C** | UAT-13 … UAT-18 | The live browser panel |
| **D** | UAT-19 … UAT-27 | The six new actions and element targeting |
| **E** | UAT-28 … UAT-30 | Two turns writing at once |
| **F** | UAT-31 … UAT-32 | Human control of the wheel |
| **G** | UAT-33 … UAT-37 | Memory pressure |
| **H** | UAT-38 … UAT-42 | Crash, restart and deletion |
| **I** | UAT-43 … UAT-45 | Idle behaviour |
| **J** | UAT-46 … UAT-48 | Audit trail and disclosure |

---

### Group A — Ownership and handover

#### UAT-01 — The reported bug: a human browses, then a second agent takes over

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-2, US-22/AC7; ADR §1.1; D1 H-1 |
| **Preconditions** | Gateway running; workspace **Alpha**; chat **A1** open |

**Steps**

1. In chat **A1**, open the **live browser panel**.
2. In the panel, drive the browser yourself to `https://the-internet.herokuapp.com/login` and sign in
   with the published dummy account from §3.3.
3. Confirm you can see "You logged into a secure area!" in the panel.
4. **Release control** of the panel (hand the wheel back).
5. In chat **A1**, address **Jim** and ask: *"What browser tabs can you see right now?"*
6. Then ask Jim: *"On that page, click Logout."*

**Expected observable result**

At step 5 Jim names the secure-area page you opened, and identifies it as the workspace's tab (a tab
*you* opened, not one of his). At step 6 the panel visibly navigates — the page changes under Jim's
action, without you having to hand anything over, run a command, or re-navigate.

**What a silent failure looks like**

- Jim answers *"I don't see any tabs"* or *"there is nothing open"* and sounds perfectly confident.
  **This is the original defect.** It reads as an honest answer and is not.
- Jim *describes* the page correctly but the panel does not change at step 6 — he narrated an action
  he did not perform. Watch the panel, not the chat.
- Jim says he clicked Logout and reports success, but the page still shows the secure area. Refresh
  the panel before believing either of them.

---

#### UAT-02 — A tab follows the chat when you switch agents

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-1/AC1–AC3, US-22/AC6; D1 H-19 |
| **Preconditions** | Chat **A1**, fresh; no tabs open |

**Steps**

1. In chat **A1**, ask **Jim**: *"Open `https://the-internet.herokuapp.com/dropdown` in the browser."*
2. Confirm in the live panel that the page is open.
3. **Switch the chat to Ray** — same chat A1, different agent. Do not open a new chat.
4. Ask **Ray**: *"What tabs can you see?"*
5. Ask **Ray**: *"On that page, select the option 'Option 2'."*

**Expected observable result**

Ray lists the dropdown page at step 4 and changes it at step 5. He can list it, switch to it, drive
it and close it. No handover step exists and none is needed.

**What a silent failure looks like**

- Ray reports no tabs. Under the earlier, superseded design this was the *correct* answer, so a
  tester working from an older document — or an implementation that stopped halfway — will read this
  as a pass. **It is a failure.**
- Ray "sees" the tab because he opened his own copy of the same URL. Check the tab count in the
  panel: there must be **one** tab, not two, and it must still be showing whatever state the page was
  left in (the dropdown selection, the scroll position) rather than a fresh load.

---

#### UAT-03 — Two chats in one workspace do not see each other's tabs

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-22/AC8; D1 H-19 (second half) |
| **Preconditions** | Chats **A1** and **A2**, both in workspace Alpha |

**Steps**

1. In chat **A1**, ask Jim to open `https://the-internet.herokuapp.com/hovers`.
2. Switch to chat **A2** (same workspace, different conversation).
3. Ask Jim in **A2**: *"What tabs can you see?"*
4. Ask Jim in **A2**: *"Close the hovers tab."*
5. Return to chat **A1** and ask Jim: *"What tabs can you see?"*

**Expected observable result**

At step 3 chat A2 does **not** list the hovers page. At step 4 Jim cannot close it — he has nothing
to close and should say so. At step 5 chat A1 still has its hovers tab, untouched.

**What a silent failure looks like**

- A2 lists A1's tab. That is a **privacy failure**, not a convenience win: it means every
  conversation in a workspace can read what every other one is browsing. It looks like the feature
  working.
- A2 cannot *list* the tab but *can* close or drive it when named directly. Step 4 exists for that;
  do not skip it because step 3 passed.
- A1's tab is gone at step 5 with no explanation from either chat.

---

#### UAT-04 — The tab *you* open is the whole team's

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-22/AC7; D1 §4 contract 2 |
| **Preconditions** | Chats **A1** and **A2** both exist and are otherwise empty of tabs |

**Steps**

1. In chat **A1**, open the live panel and navigate **yourself** to
   `https://the-internet.herokuapp.com/key_presses`. Release control.
2. In chat **A1**, ask Jim what tabs he can see.
3. Switch to chat **A2** and ask Jim there what tabs he can see.
4. In chat **A2**, ask Jim to read the text on that page.

**Expected observable result**

Both chats list the page you opened, and both label it as the **workspace's** tab rather than
something that chat opened. Jim can read it from A2.

**What a silent failure looks like**

- Only A1 sees it. The tab got filed under the chat instead of the workspace, which quietly removes
  the "ask any agent to take over what I'm looking at" behaviour while UAT-02 and UAT-03 still pass.
- Both see it but neither says whose it is — the answer is right and the label is missing, which
  matters because UAT-03 depends on a person being able to tell the two kinds apart.

---

#### UAT-05 — A background run gets its own tabs

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-22/AC8, US-6/AC1; D1 §4 contract 6 |
| **Preconditions** | The scheduled entry from §2.5, assigned to **Ray** (Alpha only). Ray is not on Bravo. |

**Steps**

1. In chat **A1**, ask Jim to open `https://the-internet.herokuapp.com/dropdown`. Leave it open.
2. Separately, in the live panel, sign in to the dummy account (§3.3) so the workspace has a login.
3. Wait for the scheduled run to fire (§2.5 told you the time). Do not touch anything while it runs.
4. Read the run's output — Workspaces → Alpha → Calendar → the entry's last run, and/or
   `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/logs/gateway.log`.
5. Return to chat **A1** and confirm the dropdown tab is still there and still on the same page.

**Expected observable result**

The scheduled run reports a tab list that does **not** contain chat A1's dropdown tab. It opens its
own tab for `https://example.com/scheduled-marker` and reports that page's title. Chat A1 is
undisturbed. The run resolves to workspace Alpha's browser rather than failing.

**What a silent failure looks like**

- The scheduled run lists chat A1's tab. Tabs leaked from an attended conversation into an
  unattended one; the run looks successful and it read something it should not have.
- The run reports the marker page title without ever opening it — check the panel tab count during
  the run, or the log, for evidence a tab was actually created.
- The run silently does nothing and is recorded as successful. An empty tab list plus no marker page
  is a failure, not a quiet pass.

> **Open question, flagged not answered.** Whether the background run should be **signed out** of the
> workspace's sites is not settled between the brief for this plan and the specs. The specs say a
> background run **shares the workspace browser and therefore its logins** (D1 US-5, D1 §4 contract 5,
> D1 H-6). This case therefore does **not** assert a logged-out state. See §8, Q-1 — and **record what
> you actually observed** either way, because that observation is the input the question needs.

---

#### UAT-06 — A background run reaches the same workspace as its files

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-6/AC0, AC1 |
| **Preconditions** | The §2.5 schedule; the workspace Alpha browser has a live login (§3.3) |

**Steps**

1. Ensure workspace Alpha is signed in to the dummy site via the live panel, then release control.
2. Edit the §2.5 schedule's instruction to: *"Open `https://the-internet.herokuapp.com/secure` and
   report exactly what the page says."*
3. Wait for it to run. Read the output.

**Expected observable result**

The run reports the secure-area content — it reached workspace Alpha's Chrome, and inherited Alpha's
login, because it is the same browser and the same profile.

**What a silent failure looks like**

- The run reports the login page instead and calls that a success ("the page says: Login"). Reaching
  a *different* browser and reporting whatever it found there is indistinguishable from working
  unless you read the actual text.
- The run reports the secure area because it silently re-authenticated by typing credentials from
  somewhere. There should be **no** login step in the trace. If there is one, ask where it got the
  password — that is an S1 defect.

---

#### UAT-07 — An agent on two workspaces, with no workspace named, is refused

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-6/AC3 (FR-033), US-11/AC2; D1 H-11 |
| **Preconditions** | **Jim** is on both Alpha and Bravo (per §2.4) |

**Steps**

1. Create a second scheduled entry in **Alpha → Calendar**, assigned to **Jim**, instructing him to
   list browser tabs.
2. Let it run. Read the output and the gateway log around that timestamp.

**Expected observable result**

The run is **refused**, and the refusal *names the ambiguity* — Jim is on more than one workspace and
this run does not say which. Both candidate workspaces appear in the log at WARN level. No browser
launches for this run.

**What a silent failure looks like**

- The run succeeds and lists Alpha's tabs. The system picked a workspace by sorting or by luck. It
  looks entirely correct until the day it picks the other client's browser. **Check the log for a
  WARN naming both workspaces** — its absence, combined with a success, is the failure.
- The run fails with a generic error ("browser unavailable") that does not mention the ambiguity. The
  behaviour is right and unactionable; file it as S3.

---

#### UAT-08 — A denied agent cannot reach the browser, and does not lie about why

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-8/AC1; D1 H-4 (**required UAT**) |
| **Preconditions** | Workspace Alpha has a browser with at least one tab open. **Mia** is on the Alpha team. |

**Steps**

1. Ensure a page is open in Alpha's browser (any of the §3.4 pages).
2. In chat **A1**, address **Mia** and ask: *"What browser tabs are open?"*
3. **Capture the full transcript of her answer** and attach it to the run log.
4. Read her answer closely for one specific claim.

**Steps 2–4 are mandatory even though this case is expected to reveal an unsatisfying answer.**

**Expected observable result**

Mia has no browser tools — she was never shown any — so she answers **from absence**: she reports
nothing is open, or that she cannot check. **This is the recorded, accepted outcome**, not a pass
condition being fudged. The spec is explicit that she cannot tell "I may not" from "there is
nothing", and the transcript is the evidence the gap is still there.

**What a silent failure looks like — this is the actual check**

Mia must **not** claim that *the browser is shared across the workspace*, or otherwise describe a
browser she cannot see. That specific sentence is one she can still emit, and it would be a
confident description of something she has no access to. If she says anything of that shape, **file
it as a defect and quote it verbatim.**

---

### Group B — Workspace isolation

#### UAT-09 — A login in one workspace does not exist in another

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-3/AC1; D1 §4 contract 3; D1 H-3 |
| **Preconditions** | Workspaces Alpha and Bravo both exist; neither has browsed yet |

**Steps**

1. In chat **A1** (Alpha), open the live panel and sign in to the dummy site (§3.3). Confirm the
   secure area. Release control.
2. In chat **B1** (Bravo), ask Jim to open `https://the-internet.herokuapp.com/secure`.
3. Read what Jim reports.
4. In a terminal, run:
   `ps ax | grep "user-data-dir" | grep -v grep`
5. In a terminal, run:
   `ls -la /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/browser/profiles/`

**Expected observable result**

At step 3 Bravo is **logged out** — it lands on the login page. At step 4 you see **two separate
Chrome processes with two different process IDs and two different `--user-data-dir` paths**. At step
5 there are **two directories**, one per workspace.

**What a silent failure looks like**

- Bravo shows the secure area. Two clients are sharing one login.
- Bravo is logged out but step 4 shows **one** Chrome. The isolation was done some other way, inside
  a single browser — which is the mechanism this rework deliberately replaced, and which does not
  survive a crash (UAT-38) or a restart (UAT-40). **The process count is the real assertion here**,
  not the logged-out page.
- Only one profile directory exists at step 5, or both workspaces point at the same one.

---

#### UAT-10 — A second agent on the same workspace *is* signed in

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-3, US-4; D1 §4 contract 4 |
| **Preconditions** | Continue directly from UAT-09 — Alpha is signed in |

**Steps**

1. In chat **A1**, switch the agent to **Ray**.
2. Ask Ray to open `https://the-internet.herokuapp.com/secure` and report what it says.
3. Now open a **brand-new chat** in Alpha (call it A3) and ask Jim there to do the same.

**Expected observable result**

Both Ray (step 2) and the new chat (step 3) see the **secure area** — they share Alpha's browser and
therefore Alpha's login. Isolation is between workspaces, never between agents or chats on one
workspace.

**What a silent failure looks like**

- Ray or the new chat is logged out. The isolation went one level too deep. This will read to a
  tester as "isolation working" — it is the opposite of what the design says, and it means every new
  conversation costs the operator a re-login.
- Both see the secure area but a `ps ax | grep user-data-dir` now shows a **third** Chrome. Something
  is launching a browser per chat and copying the profile; that is a memory and disk problem waiting
  to happen even though the page looks right.

---

#### UAT-11 — An agent on no workspace is told the truth

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-14/AC1, AC2 |
| **Preconditions** | Create a **custom agent** (Agents → New). Grant it the browser tools in its Tools tab. **Do not add it to any workspace team.** |

**Steps**

1. Start a chat with that custom agent.
2. Ask it to open `https://example.com`.
3. Read the error it reports.
4. Open the live browser panel for that agent and read what the panel says.

**Expected observable result**

The tool fails with a message that says this agent is **not on a workspace**, and names the remedy —
add it to a workspace's team. The panel shows a reason that distinguishes *"this agent is not on a
workspace"* from *"browser tools are not registered for this agent"*.

**What a silent failure looks like**

- The agent reaches **some** workspace's browser. That is a boundary breach dressed as convenience.
  Check `ps ax | grep user-data-dir` — a browser being driven by an agent on no team is the failure.
- The tool returns an empty success — no tabs, no error, nothing happened. An agent given nothing and
  told nothing will simply report that the page was empty.
- The panel and the tool disagree, or the panel gives the generic "not registered" reason for an
  agent whose problem is actually the missing team. Both render the same today; the change is
  supposed to separate them.

---

#### UAT-12 — Upgrading does not hand anyone someone else's session

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-23/AC1–AC3; D1 H-21 |
| **Preconditions** | An `$OMNIPUS_HOME` from a **pre-rework** build that has a populated `browser/profiles/default/` directory and a live login. If you do not have one, mark this case **Blocked** — do not fabricate the directory. |

**Steps**

1. Point the new binary at that older home directory and start it.
2. In every workspace, ask an agent to open the site the old install was signed in to.
3. Run `ls -la <that home>/browser/profiles/`.
4. Read the release note for this version.

**Expected observable result**

Every workspace is **logged out** — nobody inherited the old session. The old `default/` directory is
**still on disk and untouched**. The release note states plainly that agents need to sign in again,
per workspace, after the upgrade.

**What a silent failure looks like**

- One workspace is mysteriously already signed in. Some workspace adopted the shared profile, and
  which one is arbitrary. It looks like a smooth upgrade.
- The `default/` directory is **gone**. Logins the operator may still want were destroyed to make the
  upgrade look clean.
- Everything is logged out and the release note does not mention it — correct behaviour, no warning,
  and every operator files a bug on day one.

---

### Group C — The live browser panel

> **Read this before running Group C.** The old slow video path was **deleted on purpose**. There is
> now one video path (WebRTC) and no fallback. That means: a panel that *works but feels slow* is
> not "fine, just a bit laggy" — it is a defect, and it is exactly the defect the deletion was meant
> to expose. Report sluggishness with the same seriousness as a blank panel.

#### UAT-13 — Video is smooth

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | ADR-061 (no JPEG fallback); D1 US-2/AC2 |
| **Preconditions** | Chat A1, live panel open |

**Steps**

1. Open the live panel and navigate to a page with motion — a video, or
   `https://the-internet.herokuapp.com/dynamic_content` reloaded repeatedly.
2. Watch for 30 seconds.
3. Scroll the page up and down in the panel, continuously, for 10 seconds.

**Expected observable result**

Motion is continuous and smooth. Scrolling tracks your input without visible stepping. It should feel
like watching a screen share, not like a slideshow.

**What a silent failure looks like**

- The panel updates in visible steps — recognisably a sequence of stills rather than video — while
  showing **no error at all**. This is the deleted fallback having come back, or the video path having
  failed into something that still paints pixels. It looks completely normal to anyone not watching
  for it. **This is a P0 defect, and it is the single most likely thing this group will catch.**
- Motion is smooth but lags your scroll by a second or more. Same class of problem, same severity.

---

#### UAT-14 — Clicking in the panel responds without lag

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | ADR-061; ADR-038 D6 (human control) |
| **Preconditions** | Live panel open, you hold control |

**Steps**

1. Take control of the panel.
2. Navigate to `https://the-internet.herokuapp.com/dropdown`.
3. Click the dropdown, pick an option, click elsewhere. Repeat five times.
4. Type into a text field on `https://the-internet.herokuapp.com/login` and watch the characters
   appear.

**Expected observable result**

Clicks land where you clicked and the page responds immediately. Typed characters appear as you type
them, not in a burst afterwards.

**What a silent failure looks like**

- Clicks land in the **wrong place** — the picture you are clicking on is behind the real page, so
  your click hits whatever is now under that coordinate. This shows up as "the page did something
  unexpected" and gets blamed on the site.
- Typing appears in bursts. The input is queued somewhere. Nothing errors.

---

#### UAT-15 — Taking the wheel and handing it back

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | ADR-038 D6; D1 US-22/AC5, H-27 |
| **Preconditions** | Chat A1, live panel open, a page loaded |

**Steps**

1. **Take control** in the panel. Confirm the UI shows that you hold it.
2. Navigate somewhere and interact.
3. **Release control.** Confirm the UI shows you no longer hold it.
4. Ask Jim in chat A1 to fill a field on the current page.
5. Take control again.
6. Confirm you can still drive the page.

**Expected observable result**

The control state is visible at every step and matches reality. After step 3, Jim acts on the page at
step 4 **with no "take control" step and no permission prompt** — asking him to do it *is* the
handover. After step 5, you are driving again.

**What a silent failure looks like**

- The UI shows you have released control but Jim's action at step 4 does nothing, or defers. The
  displayed state and the real state disagree; the displayed one is the lie.
- Jim's action succeeds at step 4 and the result *announces a transfer of ownership* ("I have taken
  control of the tab"). There is no such thing to report in this design; a message describing one
  means something else is going on.
- After step 5 you cannot type, and nothing says why.

---

#### UAT-16 — A video failure is visible, with a retry

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | ADR-061 (visible failure, no silent degrade) |
| **Preconditions** | Live panel open and working. You will deliberately break it. |

**Steps**

1. With the panel streaming, **disconnect the machine's network** for about 20 seconds (turn Wi-Fi
   off), then turn it back on. If the panel recovers on its own, note that and move to step 2
   anyway.
2. Reload the browser page showing the Omnipus UI while the panel is open.
3. If neither step produces a failure, ask the developer running this build for a supported way to
   force the video path to fail, and record which method you used.

**Expected observable result**

When the video path fails, the panel shows a **persistent, readable error naming the actual reason**,
with a **Retry** control. Retry either restores video or fails again with a reason. At no point does
the panel show a picture that is not live.

**What a silent failure looks like**

- **The panel goes blank with no message.** The operator has no idea whether the browser died, the
  page is white, or the video stopped.
- The panel keeps showing the **last frame** it received, indefinitely. It looks like a page that is
  simply not doing anything. An operator will sit and wait, or worse, ask an agent to act on what
  they think they are seeing.
- An error appears but says something generic ("connection issue"). The whole point of removing the
  fallback was that the real reason reaches the operator; a generic string re-hides it.

---

#### UAT-17 — The panel shows the right workspace's browser

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-11/AC1 |
| **Preconditions** | **Jim** is on both Alpha and Bravo. Alpha and Bravo each have a distinct page open. |

**Steps**

1. In chat **A1** (Alpha), open the panel with Jim selected. Note what page it shows.
2. In chat **B1** (Bravo), open the panel with Jim selected. Note what page it shows.
3. In each chat, ask Jim what tabs he sees.

**Expected observable result**

The panel in A1 shows Alpha's page; the panel in B1 shows Bravo's. In each chat, Jim's tab list
matches what the panel is showing. The panel and the agent never disagree.

**What a silent failure looks like**

- Both panels show the same workspace's browser. Everything else in Group B still passes; only this
  case catches it, and an operator who trusts the panel will believe they are looking at the wrong
  client's screen.
- The panel shows Alpha while Jim in that chat lists Bravo's tabs. Both surfaces are individually
  plausible; only comparing them exposes it.

---

#### UAT-18 — A dialog on a tab you are not watching

| | |
|---|---|
| **Priority** | P2 |
| **Traces to** | D2 H-8 (a **measurement**, not a pass/fail) |
| **Preconditions** | Two tabs open in Alpha; the panel is watching the first |

**Steps**

1. In chat A1, ask Jim to open `https://the-internet.herokuapp.com/javascript_alerts` in a **second**
   tab, while the panel stays on the first.
2. Ask Jim to click "Click for JS Alert" on that second tab and then handle the dialog.
3. **Write down what you, the human, could see of any of this in the panel.**

**Expected observable result**

Jim recovers the second tab. **This case is scored on what you observed, not on pass/fail** — the
specs record this as a known gap and want a human's account of how bad it is.

**What a silent failure looks like**

Not applicable — record the observation. If you saw **nothing at all** while an agent answered a
dialog on a signed-in site, say so plainly; that sentence is the deliverable.

---

### Group D — The six new actions

> Each of the six is exercised through a real page, not a fixture, because the point is whether an
> agent can drive the web as it actually is. Where a case says "ask Jim to…", phrase it naturally —
> do not name the tool. If Jim cannot work out which action to use, **that is a finding** and belongs
> in the notes even when the case otherwise passes.

#### UAT-19 — Choose from a dropdown

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D2 US-5/AC1–AC3 |
| **Preconditions** | Chat A1, Jim |

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/dropdown`.
2. Ask Jim: *"Set that dropdown to Option 2."*
3. Look at the panel.
4. Ask Jim to set it to an option that does not exist, e.g. *"Set it to Option 9."*

**Expected observable result**

Step 2: the dropdown visibly shows Option 2 in the panel, and the page reacts as it would to a human
selection. Step 4: Jim reports a clear failure naming the option he could not find, and the dropdown
is **unchanged**.

**What a silent failure looks like**

- The dropdown's displayed value changes but the page behaves as if nothing was selected. Some pages
  only react to a real selection event; a value written directly looks identical and does nothing.
  Where the page has a visible reaction to selection, watch for it.
- Step 4 leaves the dropdown on a *different* wrong option, or clears it. A failed selection must
  change nothing at all.
- Jim reports success at step 4.

---

#### UAT-20 — A keyboard-only flow

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D2 US-6/AC1–AC3 |
| **Preconditions** | Chat A1, Jim |

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/key_presses`.
2. Ask Jim to press **Enter**, then **Tab**, then **Escape**, one at a time. The page reports each
   key it receives.
3. Ask Jim to open `https://the-internet.herokuapp.com/login`, put the cursor in the username field,
   type the dummy username, press **Tab**, type the dummy password, and press **Enter** — using keys
   only, never clicking the Login button.
4. Ask Jim to press a key that does not exist: *"Press Ctrl+Banana."*

**Expected observable result**

Step 2: the page displays "You entered: ENTER", then TAB, then ESCAPE. Step 3: the form submits and
the secure area appears. Step 4: Jim reports an error that **lists the key names that are accepted**.

**What a silent failure looks like**

- At step 4, the literal text `Ctrl+Banana` is **typed into the page** as characters. The tool
  treated an unknown key as text. On a login form that means a malformed password attempt and a page
  that just says "invalid" — completely opaque.
- At step 2 the page shows nothing but Jim reports the keys were pressed. Read the page, not the
  chat.
- Step 3 succeeds because Jim clicked the button anyway. Ask him to describe what he did, and check
  the trace for a click.

---

#### UAT-21 — A menu that only appears on hover

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D2 US-7/AC1 |
| **Preconditions** | Chat A1, Jim |

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/hovers`.
2. Ask Jim: *"Hover over the first image and tell me the name that appears."*
3. Watch the panel while he does it.

**Expected observable result**

The hidden caption appears under the first image, and Jim reads out the name. **No click occurs** —
the page does not navigate.

**What a silent failure looks like**

- Jim **clicks** the image instead of hovering. The caption's link fires and the page navigates. He
  will still be able to report a name (from the new page), so the answer looks right. Watch for
  navigation.
- Jim reports the name without the caption ever appearing in the panel — he read it out of the page
  source rather than revealing it. The distinction matters on real sites where the hover *loads* the
  menu rather than just showing it.

---

#### UAT-22 — Take a structured snapshot of a page

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D2 US-10/AC1–AC3; D2 H-1 |
| **Preconditions** | Chat A1, Jim, on the login page |

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/login`.
2. Ask Jim: *"Take a structured snapshot of this page and tell me every field and button on it."*
3. **Record whether Jim reached for a snapshot on his own, or reached for a screenshot instead.**
4. If he did not use a snapshot, ask explicitly: *"Search your tools for a page-snapshot action and
   use it."* Run steps 2–3 again with that prompt.
5. From the snapshot output, ask Jim to act on one of the elements it named — *"type into the field
   the snapshot called Username."*

**Expected observable result**

The snapshot names the username field, the password field and the Login button, each with what kind
of thing it is and what it is called. At step 5, a handle from the snapshot works immediately in the
next action.

**Step 3 is a required measurement, not a formality.** This action is deliberately *not* offered to
the agent up front — it has to search for it. Whether an agent finds it unaided is a recorded number,
and "he screenshotted instead" is a legitimate, expected result to write down rather than a bug in
the snapshot itself.

**What a silent failure looks like**

- The snapshot returns something that *looks* structured but names nothing you can act on, and step 5
  fails with "element not found". The output was decorative.
- The output is silently cut short — Jim summarises three fields on a page with thirty. On a big page
  the output is capped, and the cap is supposed to be **announced** in the output with a count of what
  was left out. A truncation with no marker reads as a complete page.

---

#### UAT-23 — A snapshot of a signed-in page shows field values

| | |
|---|---|
| **Priority** | P1 — **security-relevant, read the note** |
| **Traces to** | D2 US-10/AC3, AC4; D2 H-11 |
| **Preconditions** | Signed in on Alpha with the dummy account (§3.3) |

**Steps**

1. In the live panel, go to `https://the-internet.herokuapp.com/login` and type the dummy username
   and the dummy password into the fields — **but do not submit**.
2. Release control.
3. Ask Jim to take a structured snapshot of the page.
4. Read the snapshot output in the chat.
5. Open **Settings → Security → Audit Log** and find the snapshot record.

**Expected observable result**

The snapshot **does** contain the typed values, including what is in the password field. **That is
the ruling, not a bug** — the design decided the agent needs to see what is on the form. The audit
record, by contrast, records only *that* a snapshot happened, its page and its size — **never the
values**.

**What a silent failure looks like**

- The **audit record contains the captured values.** The values are meant to live only in the
  conversation, not in a permanent log. Read the audit entry, not just the chat.
- The Audit Log screen is **completely blank**. That is a known separate defect (a legacy log entry
  with a dot in its name blanks the whole viewer). Confirm by also fetching the log another way —
  `curl` against `/api/v1/audit-log`, or read
  `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/system/audit.jsonl` — and **record
  which install you were on**. A blank viewer here is not this rework's defect, but an unexamined
  blank viewer hides everything else in this case.
- A value you registered as secret elsewhere in the product appears in plaintext instead of being
  replaced by a `[FILTERED]` marker.

---

#### UAT-24 — Upload a file, and refuse one from out of bounds

| | |
|---|---|
| **Priority** | P1 — **may be Blocked; see the note** |
| **Traces to** | D2 US-8/AC1–AC5; D2 H-2 |
| **Preconditions** | The two files from §3.4 and §3.5 exist |

> **This case may legitimately be blocked.** The upload action is deliberately **not registered**
> until a separate issue lands, because on an unattended turn its approval prompt has nobody to answer
> it. If Jim reports the tool does not exist, record the case as **Blocked**, not as passing and not
> as failing. Then still run step 4.

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/upload`.
2. Ask Jim to attach
   `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/uat-files/uat-upload.txt`
   and submit it.
3. When an approval prompt appears, approve it — **exactly one prompt**, and read what it says before
   approving.
4. Ask Jim to attach `/tmp/uat-out-of-bounds.txt` instead.
5. Check **Settings → Security → Audit Log** for both attempts.

**Expected observable result**

Step 2–3: one approval prompt naming the file and the site; on approval, the page reports the file
was received. Step 4: **refused**, naming the path, without the file ever reaching the browser. Step
5: an audit record for **both** the allowed and the denied attempt.

**What a silent failure looks like**

- **No approval prompt at all**, and the upload just happens. An agent quietly sending a file from
  your disk to a website is the single worst outcome in this plan.
- Step 4 is refused with a message that does not name the path — the operator cannot tell which file
  was blocked or why.
- Step 4 is refused *by the website* rather than by the product (the file arrived and was rejected
  there). Check the audit record: if it shows the file was handed to the browser, the boundary did
  not hold.
- Only the successful upload is audited. A denied attempt that leaves no trace is the one you most
  want a record of.

---

#### UAT-25 — A page that raises a browser popup

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D2 US-9/AC1–AC5, AC9; D2 H-3 |
| **Preconditions** | Chat A1, Jim |

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/javascript_alerts`.
2. Ask Jim to click **"Click for JS Alert"**.
3. Ask Jim to **dismiss the popup**.
4. Ask Jim to read the page's result message.
5. Repeat with **"Click for JS Confirm"**, dismissing it (choosing Cancel).
6. Repeat with **"Click for JS Prompt"**, answering it with the text `uat`.
7. Now do it once more but **do not** ask him to dismiss it: click "Click for JS Alert", then
   immediately ask him to read some text on the page.

**Expected observable result**

Steps 2–6: each popup is answered and the page's own result line confirms which answer it got
(`You successfully clicked an alert`, `You clicked: Cancel`, `You entered: uat`). Step 7: the next
action **times out with an error that says a dialog is pending and names the action that clears it** —
and Jim can then recover unaided.

**What a silent failure looks like**

- **The tab wedges permanently.** Every later action on it times out with a bare "timeout" or
  "context deadline exceeded" that mentions no dialog. The agent has no idea what happened, retries,
  and burns the rest of the turn. **This is the worst case the design names for itself.**
- Jim reports he dismissed the popup and the page's result line says something else — or says
  nothing, meaning no popup was ever answered.
- Jim answers a `confirm()` with **OK** on an unattended run. On a turn where nobody can be asked, he
  is allowed to **dismiss** a popup but not to **accept** one. If a scheduled run ever reports
  clicking "OK" on a confirmation, that is an S1 defect.

---

#### UAT-26 — Naming an element by what it is and what it says

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D2 US-1/AC1–AC2, US-2/AC1–AC2; D2 H-1 |
| **Preconditions** | Chat A1, Jim |

**Steps**

1. Ask Jim to open `https://the-internet.herokuapp.com/login`.
2. Ask Jim: *"Click the button called Login"* — describing it by its visible name, not by any code.
3. Ask Jim to open a page with several identically-named controls
   (`https://the-internet.herokuapp.com/challenging_dom` has several buttons of the same kind), and
   ask him to *"click the button"* without saying which.
4. Then ask him to click **the second one**.

**Expected observable result**

Step 2 works from the visible name alone. Step 3 **fails with an error naming how many candidates it
found** and listing them. Step 4 clicks the second one in page order.

**What a silent failure looks like**

- Step 3 **picks the first one silently** and reports success. On a page of "Delete" buttons, that is
  the difference between deleting the row you meant and deleting the top one. It looks exactly like
  success and is the most consequential silent failure in Group D.
- Step 3 errors but the message does not say **how many** it found, so the agent cannot ask for the
  second.
- Step 2 works because Jim fell back to a code-level selector. Ask him what he used; if the answer
  contains CSS, the new targeting is not being reached.

---

#### UAT-27 — A click that cannot land says why

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D2 US-3/AC1–AC2, AC6; D2 H-4 |
| **Preconditions** | Chat A1, Jim, on a page with a **disabled** control and, separately, a page with a cookie banner or overlay covering something |

**Steps**

1. Ask Jim to open a page with a disabled button
   (`https://the-internet.herokuapp.com/dynamic_controls` — disable the input first).
2. Ask Jim to type into the disabled field.
3. Find a real site with a cookie banner covering a button. Ask Jim to open it and click the covered
   button.
4. Read the error.
5. Ask Jim to dismiss the banner and retry.

**Expected observable result**

Step 2: the failure says the element is **not enabled** — not "timeout". Step 4: the failure says the
element is **not hit-testable** *and names what is on top of it*. Step 5: Jim dismisses the banner
himself and the retry works, without you telling him what the obstacle was.

**What a silent failure looks like**

- Either failure reports as a plain **timeout**. The agent then waits, retries and eventually gives
  up with no idea what was wrong — and neither do you, from the transcript.
- Step 4 names *hit-testable* but not the covering element. The agent cannot work out what to
  dismiss, and step 5 will not happen unaided.
- The click "succeeds" through the overlay — the banner is still there and the page did something.
  The agent reports success; on a real site that is a click on the banner, not on the button.

---

### Group E — Two turns writing at once

> **Where contention is even possible.** Two different chats have two different sets of tabs, so they
> cannot collide (that is UAT-03). Collisions happen in exactly two places: on a tab **you** opened,
> which the whole workspace shares, and between two turns running in **one** conversation. Group E
> exercises both, and also checks that the *non*-colliding case does not block for no reason.

#### UAT-28 — Two agents told to act on the same tab at once

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-9/AC1, AC7; ADR criterion 16; D1 H-8 |
| **Preconditions** | You have opened a tab yourself in the panel on workspace Alpha (so it is the workspace's tab) and released control. Two chats, A1 and A2, both on Alpha. |

**Steps**

1. In chat **A1**, ask Jim: *"On the tab I opened, navigate to
   `https://the-internet.herokuapp.com/dropdown`."*
2. **Within a second or two**, in chat **A2**, ask Jim: *"On the tab I opened, navigate to
   `https://the-internet.herokuapp.com/hovers`."* Getting these close together is the point; have
   both messages typed and ready.
3. Watch the panel throughout.
4. Read both transcripts to the end.

**Expected observable result**

**Both requests eventually complete.** One may report that it waited or was deferred and then went
ahead — at most one deferral each. The panel shows one navigation, then the other. It never shows a
half-loaded mixture of the two pages.

**What a silent failure looks like**

- One agent reports success and **nothing happened for it** — the page went to the other agent's
  destination and the first agent narrated a navigation it lost. Read both transcripts against what
  the panel actually showed, in order.
- One agent **returns an error**. Contention is meant to make a caller wait, never fail.
- One agent returns `deferred` and **stops there, treating it as finished**. A deferral means "not
  yet"; an agent that reports it as done has silently dropped the task. **This is a defect even
  though nothing crashed** — see §5, N-2.
- The two turns **deadlock**: both sit there and neither ever completes. Give it two minutes before
  calling it.

---

#### UAT-29 — Two conversations that do not share a tab do not block each other

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-9/AC7 (second half) |
| **Preconditions** | Chats A1 and A2 on Alpha, each with **its own** tab open (from UAT-03) |

**Steps**

1. In chat **A1**, start something slow on A1's own tab — ask Jim to open a heavy page and read all
   its text.
2. **While that is running**, in chat **A2**, ask Jim to do something quick on A2's own tab.
3. Time how long A2 waits before it starts doing anything.

**Expected observable result**

A2 proceeds immediately. Neither waits on the other, and neither reports a deferral — they are
working on separate tab sets and have nothing to contend over.

**What a silent failure looks like**

- A2 reports a **deferral** and waits for A1. Everything still "works", just slowly and mysteriously.
  On a busy install this turns into every conversation queueing behind every other one, and no error
  is ever produced to explain it. A safety mechanism that serialises work it never needed to
  serialise is a performance defect, not a conservative choice.

---

#### UAT-30 — A deferral is legible to the agent

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D2 H-12 |
| **Preconditions** | Set up a deferral deliberately — repeat UAT-28, or hold the wheel (UAT-31) |

**Steps**

1. Cause a deferral by any of the routes above.
2. Read **exactly what the agent was told**, and **what it then did**.

**Expected observable result**

The agent can tell "not right now, try again" apart from "this failed". It waits and retries, or it
says plainly that it is waiting. The message it received says which of the two situations it is in.

**What a silent failure looks like**

- The agent reads the deferral as a failure and **gives up on the task**, reporting to you that it
  could not do the thing. The mechanism worked perfectly; the outcome for the user is a dropped
  request.
- The agent reads the deferral as **success** and moves on to the next step of a multi-step task,
  building on a page state that never happened.

---

### Group F — Human control of the wheel

#### UAT-31 — While you hold the wheel, an agent stands down

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-9/AC2, US-22/AC6; ADR-038 D6; D1 H-27 (second half) |
| **Preconditions** | Chat A1, live panel open on a page you can see clearly |

**Steps**

1. **Take control** of the panel and keep holding it. Do not release.
2. Put the page in a state you will recognise — scroll to a specific spot, type something in a field.
3. While still holding control, ask Jim in chat A1: *"Click the Login button on this page."*
4. Watch the page for five seconds.
5. Read Jim's reply.
6. Release control and ask him again.

**Expected observable result**

At step 4 **the page does not move under your hands.** At step 5 Jim reports that he is standing down
because a human holds control — a *deferral*, not an error, with a reason that says so. At step 6 the
same request works.

**What a silent failure looks like**

- **The page changes anyway.** Everything else about the interaction looks fine; the operator's
  control was decorative. This is the case an implementation loses most easily, because the *allowed*
  direction (step 6) still passes on a build with no lock at all.
- Jim reports success at step 3 and the page did not change. He was refused and did not notice.
- Jim reports a hard **error** rather than a deferral. Behaviour is right, the agent's next move is
  wrong — it will give up instead of retrying after you release.

---

#### UAT-32 — Two actions are still allowed while you hold the wheel

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D2 US-9/AC6, US-10/AC5; D2 H-12 |
| **Preconditions** | Continue from UAT-31 — you are holding control |

**Steps**

1. Still holding control, ask Jim to **take a structured snapshot** of the page.
2. Navigate the page yourself to `https://the-internet.herokuapp.com/javascript_alerts` and click
   "Click for JS Alert" so a popup is open, **while still holding control**.
3. Ask Jim to **dismiss the popup**.

**Expected observable result**

Both work. A snapshot only reads; it is never deferred. Dismissing a popup is a **recovery** action —
it must work even when a human holds the wheel, because a stuck popup is exactly the state you would
need an agent to get you out of.

**What a silent failure looks like**

- The snapshot is **deferred**. Reading a page is now blocked by a lock that exists to stop *writing*
  to it, and an agent asked "what's on screen?" while you drive answers "I can't look".
- Dismissing the popup is deferred. **The tab is now stuck and the one thing that could unstick it is
  refusing to run.** Nothing errors; the browser is simply dead until someone restarts something.
- Dismissing the popup works and the result claims the agent has **taken over the tab**. Answering a
  popup changes nothing about who owns the tab; a message saying otherwise means ownership moved when
  it should not have.

---

### Group G — Memory pressure

> ## ⚠️ How to constrain memory safely
>
> **Do NOT use a `MemoryMax` cgroup cap with swap enabled.** On this project's machines that has
> repeatedly produced processes that thrash and then cannot be killed at all, taking the host with
> them. This is not a style preference; it has happened.
>
> Use one of these instead, in order of preference:
>
> 1. **Use a genuinely small machine.** A 4 GB VM or a small cloud box gives you real pressure with
>    no tricks. This is the best option and the one the specs were reasoned against.
> 2. **Fill the memory with ordinary applications.** Start a large video call, open a browser with
>    many tabs, run a couple of heavy builds. Watch `top` or Activity Monitor until free memory is
>    genuinely low. Crude, safe, and closest to what an operator will actually hit.
> 3. **If you must cap on Linux**, cap swap to zero at the same time so a runaway dies instantly
>    instead of thrashing:
>    `systemd-run --scope -p MemoryMax=3G -p MemorySwapMax=0 /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf/omnipus-uat gateway`
>    Never set `MemoryMax` without `MemorySwapMax=0`.
>
> Record which method you used on every Group G case. The results are not comparable across methods.

#### UAT-33 — Idle browsers are closed rather than the machine swapping

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-15/AC1, AC2; D1 H-12 |
| **Preconditions** | A constrained host per the box above. Create as many workspaces as it takes to fill it (Charlie, Delta, Echo…). |

**Steps**

1. Browse a page in each workspace in turn until the machine has as many browsers as it has room
   for. **Leave them idle** — close no panels, but stop interacting.
2. Note which workspace you browsed **first** (the least recently used one).
3. Log in to the dummy site (§3.3) in that first workspace before leaving it idle.
4. Create one **more** workspace and browse a page in it.
5. Watch `ps ax | grep user-data-dir | grep -v grep` before and after step 4.
6. Now return to the first workspace and ask an agent to open
   `https://the-internet.herokuapp.com/secure`.

**Expected observable result**

Step 4 **just works.** Nothing is refused, no error appears, and nothing is said to you. The Chrome
count does not keep climbing — the oldest idle browser was closed to make room. At step 6 the first
workspace **reopens after a visible pause and is still logged in**.

**That pause is the entire cost of the mechanism, and confirming it is a pause and not a logout is
the point of this case.**

**What a silent failure looks like**

- Step 6 comes back **logged out**. The browser was not closed and reopened, it was thrown away. This
  is not a capacity policy, it is data loss — and it presents to the operator as "the site logged me
  out again", which they will blame on the site.
- The Chrome count keeps climbing at step 4 and the machine starts swapping (everything, including
  the UI, becomes treacle). Nothing was closed and nothing was refused. Watch the process count, not
  just whether step 4 succeeded.
- Step 4 is **refused** even though the browsers were idle. Idle browsers are supposed to be closed
  silently; a refusal here means the eviction path is not running and only the refusal is.

---

#### UAT-34 — When nothing can be closed, the refusal names memory

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-15/AC6, AC15; D1 H-12a |
| **Preconditions** | Same constrained host. This time **keep a tab and a live panel open in every workspace**, so none of them is idle. |

**Steps**

1. With every workspace pinned (a tab open and a panel attached in each), ask an agent in **one more**
   workspace to browse.
2. Wait. Read the error when it arrives.
3. Check the panels and tabs you had open.
4. Close **one** panel, wait a few seconds, and retry step 1.

**Expected observable result**

Step 2: after a pause, the request **fails with a message saying the host is out of memory**, and
suggesting something you can actually do — *close a browser or a panel you are finished with, or
wait*. Step 3: **nothing you had open closed.** Step 4: it now works.

**What a silent failure looks like**

- The message **names a setting to raise** — `max_browsers`, `max_tabs`, a "limit" of any kind. There
  is no such setting any more, so the message is telling an operator to do something impossible. They
  will go looking, find nothing, and file a bug against the wrong thing. **Read the message word for
  word.**
- The message is generic — "could not be adopted", "browser unavailable". An agent that cannot tell
  "out of memory" from "something broke" **retries immediately**, straight back into the pressure.
- One of your open browsers **was** closed to make room. Pinned means pinned; a panel you were
  watching must not be taken away from you.
- It succeeds and the machine starts swapping. Confirm with `top` before recording a pass.

---

#### UAT-35 — The first browser always launches

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-15/AC17 (the floor); D1 H-26 |
| **Preconditions** | A host where memory **cannot be measured** — Windows, or Linux inside a sandbox with no readable `/proc` (gVisor). If you cannot produce one, mark **Blocked** and say so; do not simulate it. |

**Steps**

1. Start the gateway on that host with nothing configured.
2. Send an ordinary chat message. **It must be answered.**
3. Ask an agent to open a browser and browse a page.
4. Then try to open a **second** workspace's browser.
5. Check the log for how many times the "cannot determine memory" reason is written.

**Expected observable result**

Steps 2 and 3 **succeed** — one browser, one tab. Step 4 is **refused**, and the refusal names
memory. The reason is logged **once**, not on every call.

**What a silent failure looks like**

- **Nothing is ever refused** and browsers keep launching. The gate had nothing to read and answered
  "there's room". This is the exact false-green shape the design was written against, and from the
  outside it looks like a machine with generous capacity.
- **The first message at step 2 is refused.** Someone read "refuse to grow" as "refuse to run". Every
  refusal test passes perfectly and the product is dead on that platform.
- The log fills with the same line on every request, burying everything else.

---

#### UAT-36 — Moving between more workspaces than fit

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-15/AC8; D1 H-12b |
| **Preconditions** | A host with room for roughly three browsers, and four workspaces |

**Steps**

1. Browse in each of the four workspaces in turn, about a minute each, going round twice.
2. Watch `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/logs/gateway.log` throughout.
3. Count the warnings about contention.

**Expected observable result**

Everything works; each switch costs a visible pause. **Exactly one** warning appears, naming the
workspaces that are contending, naming **memory** as the constraint, and naming a remedy that exists
— add memory, or browse fewer workspaces at once.

**What a silent failure looks like**

- **A warning on every switch.** A line that appears constantly is filtered out by every operator
  before the day it means something.
- **No warning at all.** The operator experiences unexplained pauses and has nothing to search for.
- A warning that names a **config key**. There is no key; see UAT-34's note.

---

#### UAT-37 — There is no limit setting, and freeing memory takes effect immediately

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-15/AC7, AC22; D1 H-13, H-25 |
| **Preconditions** | Continue from UAT-36 |

**Steps**

1. Go to **Settings** and look for a browser or tab limit — anything called max browsers, max tabs, or
   a total tab budget. Search the browser section thoroughly.
2. Go to **Settings → Performance** and read what is shown for parallel agents on a fresh install
   where you have never set a value.
3. Set the parallel-agents value explicitly to `12`. Confirm the screen shows `12`.
4. Free real memory on the host — quit an unrelated application.
5. Without restarting the gateway, retry the browse that was refused in UAT-34.
6. Read the release notes for this version.

**Expected observable result**

Step 1: **you find nothing.** No such setting exists and no help text claims one does. Step 2: the
value reads as **automatic, bounded by available memory** — it does **not** display a large number,
and certainly not under a heading like "Live system recommendation". Step 3: an explicitly set value
is honoured. Step 5: the pool grows, with **no restart**, without disturbing anything already open.
Step 6: the release note says there is **no longer a computed default**.

**What a silent failure looks like**

- Step 2 shows a big number (`2000` is the specific one to watch for) presented as a recommendation.
  An operator who never asked for a number is being handed one and will treat it as the capacity.
- Step 1 finds a setting that **loads and does nothing**. An operator who sets it believes they have
  a limit they do not have — worse than no setting at all.
- Step 6's release note contains a sentence about a default "changing from 2 to 2000". That sentence
  was written down before it was withdrawn and is one copy-paste away from shipping.
- Step 5 requires a restart, and nobody says so.

---

### Group H — Crash, restart and deletion

#### UAT-38 — Killing one workspace's browser affects only that workspace

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-16/AC1–AC3; D1 H-14 |
| **Preconditions** | Alpha and Bravo both have live browsers, both with panels attached. Alpha is signed in to the dummy site (§3.3). |

**Steps**

1. Identify the two Chrome processes and which is which:
   `ps ax | grep "user-data-dir" | grep -v grep`
   The `--user-data-dir` path contains the workspace id.
2. Kill **only Alpha's**, by its exact process id: `kill -9 <that one pid>`.
   *(Do not use `pkill`. Do not match on a pattern. One pid, deliberately chosen.)*
3. Immediately look at **Bravo's** panel and ask an agent in chat B1 to read the page.
4. Then go back to chat A1 and ask Jim to open `https://the-internet.herokuapp.com/secure`.

**Expected observable result**

Step 3: **Bravo is completely unaffected** — its panel keeps streaming, its tabs are intact, its
agent works. Step 4: Alpha's panel showed an error, and Alpha's browser **relaunches from its own
profile and is still signed in**.

**What a silent failure looks like**

- Bravo's panel goes blank, or Bravo's agent starts failing too. One workspace's crash took the
  others down; from Bravo's side it looks like an unrelated fault.
- Alpha relaunches **logged out**. The login was in memory, not on disk, and every crash costs every
  workspace its sessions — presenting as "the site keeps logging me out".
- Alpha's panel shows the **last frame from before the crash** and no error. You would happily ask an
  agent to click something on a browser that no longer exists.

---

#### UAT-39 — Killing the gateway leaves nothing behind

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-19/AC1, AC2, AC2a, AC3; D1 H-16 |
| **Preconditions** | Three workspaces with live browsers, all signed in |

**Steps**

1. Note the gateway's process id and all Chrome process ids
   (`ps ax | grep user-data-dir | grep -v grep`).
2. Kill the **gateway** with `kill -9 <gateway pid>`.
3. Immediately check for surviving Chrome processes from step 1.
4. Start the gateway again (§2.3, **without** deleting `$OMNIPUS_HOME`).
5. Read the startup lines in
   `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/logs/gateway.log`.
6. In each of the three workspaces, ask an agent to open the signed-in page.

**Expected observable result**

Step 5: startup reports what it cleaned up, with a count. **On Linux**, leftover Chromes from the
previous run are terminated. **On macOS**, they are *not* terminated — instead a warning names the
surviving process id, because the product cannot safely confirm a process is one of its own there.
Either way, step 6 works in all three workspaces and **all three are still signed in**, with no
"profile in use" failure.

**What a silent failure looks like**

- A workspace fails to relaunch with a **"profile in use"** style error, because a leftover lock file
  was not cleared. It looks like a corrupt profile and the obvious remedy — delete the profile — is
  the one that loses the login.
- Startup says it cleaned up and orphan Chromes are still running. Check with `ps ax`, do not take the
  log's word for it.
- On macOS, orphans survive and **nothing is logged about them**. They consume memory outside
  everything Group G measures, forever.

---

#### UAT-40 — Restarting the gateway keeps every login

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-12/AC4a, US-19/AC3 |
| **Preconditions** | Alpha and Bravo both signed in (Alpha to the dummy site; Bravo can be signed in as the same dummy user — they are separate profiles) |

**Steps**

1. Stop the gateway **cleanly** (Ctrl-C in its terminal, once, and let it finish).
2. Start it again (§2.3, same `$OMNIPUS_HOME`).
3. In each workspace, ask an agent to open `https://the-internet.herokuapp.com/secure`.

**Expected observable result**

Both workspaces are **still signed in**. Neither is signed in *as the other*.

**What a silent failure looks like**

- Everything is logged out. The profiles were not being persisted at all, and nobody noticed because
  during a single session it makes no difference.
- Both are signed in but **as the same account**, because the profiles were merged on restart. On a
  dummy account this is invisible; with two real clients it is a breach.

---

#### UAT-41 — Deleting a workspace deletes its logins

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-20/AC1–AC3; D1 H-17 |
| **Preconditions** | Alpha and Bravo both have browsers with tabs open and a live login |

**Steps**

1. List the profile directories and record them:
   `ls -la /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/browser/profiles/`
2. Delete workspace **Bravo** in the UI.
3. List the profile directories again.
4. Check `ps ax | grep user-data-dir | grep -v grep`.
5. Confirm Alpha still works and is still signed in.

**Expected observable result**

Bravo's directory is **gone from disk**. Bravo's Chrome has closed. Alpha's directory and Chrome are
untouched, and Alpha is still signed in. You can answer a client's *"is my data deleted?"* with yes.

**What a silent failure looks like**

- The workspace disappears from the UI and its profile directory **remains on disk**, cookies and all.
  The deletion looked complete and the client's session tokens are still there.
- Alpha's directory disappears too, or Alpha is logged out. The wrong thing was deleted and the UI
  shows nothing about it.
- Bravo's Chrome is still running against a directory that has been removed (step 4). It will
  misbehave in ways that get attributed to something else entirely.

---

#### UAT-42 — Saving an unrelated setting does not disturb browsing

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-17/AC1, AC2; D1 H-9 |
| **Preconditions** | Alpha and Bravo browsing, panels streaming, both signed in |

**Steps**

1. Record both Chrome process ids.
2. Go to **Settings** and change something unrelated — a display preference. Save.
3. Immediately re-check the process ids, the panels and the logins.

**Expected observable result**

Both process ids are **unchanged**. Both panels keep streaming without a flicker. Both logins survive.

**What a silent failure looks like**

- The process ids change. Every browser was torn down and rebuilt on a settings save. If the profiles
  persist, the logins survive and you would never notice except for the pause — and the pause gets
  blamed on the network.
- One panel stops streaming and shows a still frame with no error. See UAT-16.

---

### Group I — Idle behaviour

#### UAT-43 — An untouched browser closes, and comes back signed in

| | |
|---|---|
| **Priority** | **P0** |
| **Traces to** | D1 US-12/AC4, AC4a; D1 H-15 |
| **Preconditions** | Alpha has a browser, a tab, and a live login. Default timings: a tab is reaped after about **5 minutes** idle; the whole browser closes after about **15 minutes** with nothing open and nobody watching. |

**Steps**

1. Sign in to the dummy site in Alpha. Confirm the secure area.
2. **Close the live panel** — an attached panel deliberately keeps the browser alive, so leaving it
   open invalidates this case.
3. Leave everything alone for **25 minutes**. Do not chat, do not open the panel.
4. Check `ps ax | grep user-data-dir | grep -v grep`.
5. Check the profile directory still exists:
   `ls -la /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/browser/profiles/`
6. Ask Jim in chat A1 to open `https://the-internet.herokuapp.com/secure`.

**Expected observable result**

Step 4: **Alpha's Chrome is gone.** Step 5: **Alpha's profile directory is still there.** Step 6:
Chrome relaunches after a pause and the page shows the **secure area** — still signed in. No error,
no second browser, nothing to re-configure.

**What a silent failure looks like**

- Step 4 shows Chrome still running. The idle close never fires. On a long-lived install every
  workspace ever browsed accumulates a browser, and the machine slowly fills. It is invisible for
  days.
- Step 5 shows the profile directory **gone** along with the process. Step 6 will then work but
  logged out — and it will look like a normal session expiry.
- Step 6 fails, or launches a **second** browser for the same workspace (check the process count).

---

#### UAT-44 — Disk does not fill, and the logins survive the clean-up

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-25/AC1–AC3, AC7; D1 H-28 |
| **Preconditions** | Five workspaces, each browsed enough to build up a cache (load a few image-heavy pages in each) |

**Steps**

1. Record the size of each profile:
   `du -sh /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/browser/profiles/*`
2. Sign in to the dummy site in **one** of them first.
3. Leave the host idle for **25 minutes** with all panels closed.
4. Re-measure with the same command.
5. Ask an agent in the signed-in workspace to open the secure page.
6. Separately, drive **one** workspace continuously for an hour and measure it again.
7. Look for a statement about step 6's behaviour in the browser section of the configuration
   documentation.

**Expected observable result**

Step 4: each profile has **shrunk** to roughly its login-bearing size. Step 5: **still signed in** —
the first page load is slower because cached assets were discarded, and that is the accepted cost.
Step 6: the continuously driven one has **not** shrunk. Step 7: that is **stated in the
documentation**, so an operator can predict their own disk use rather than having to work it out.

**What a silent failure looks like**

- Step 5 comes back **logged out.** The clean-up removed the whole profile instead of just the cache.
  This passes any check that only looks at whether disk was reclaimed.
- Nothing shrinks and nobody says anything. Disk fills over weeks and presents as an unrelated
  outage — this project has filled its root volume twice already.
- Step 6 does not shrink **and** the documentation is silent. The behaviour is correct and
  unpredictable, which for a disk-space question is nearly as bad as being wrong.

---

#### UAT-45 — Starting the gateway warms one browser, not one per workspace

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-24/AC1, AC2 |
| **Preconditions** | Five or more workspaces exist and have all been browsed at some point |

**Steps**

1. Stop the gateway cleanly. Confirm no Chrome processes remain.
2. Start it again.
3. Wait one minute without touching anything.
4. `ps ax | grep user-data-dir | grep -v grep`

**Expected observable result**

**Exactly one** Chrome is running — the one belonging to the default agent's workspace. Not five.

**What a silent failure looks like**

- Five Chromes start. Every workspace is now "busy" from the moment of boot, which erases the
  distinction the whole memory design rests on, and multiplies the background video encoding by five
  on a machine that has nothing to display. Startup will just feel slow, and nobody will connect the
  two.
- Zero start and nothing is logged. That is *probably* fine — a missed optimisation, and the log
  should say so at INFO — but confirm the log mentions it rather than assuming.

---

### Group J — Audit trail and disclosure

#### UAT-46 — Every action on a signed-in site is recorded

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-13/AC2–AC4; D1 H-22 |
| **Preconditions** | Alpha signed in to the dummy site |

**Steps**

1. Ask Jim to perform **ten** distinct actions on the signed-in site — navigate, click, type, select,
   press a key, and so on. Count them.
2. Open **Settings → Security → Audit Log**.
3. Count the browser entries and read what each one names.
4. If the screen is blank, read the log another way:
   `tail -50 /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat-home/system/audit.jsonl`

**Expected observable result**

**The screen renders**, and there are **ten** entries, each naming the agent, the action and the site.
You can answer *"which agent did that, on which site"* for any one of them.

**What a silent failure looks like**

- **One** entry, recorded the first time the agent touched the browser. It looks like auditing is on.
  It cannot tell you who made the tenth action — which is the one that matters when the tenth action
  was a purchase.
- The Audit Log screen is **completely blank**. A single malformed entry blanks the whole viewer. An
  audit trail nobody can read is not a mitigation, and this is easy to mistake for "nothing happened".
  Always do step 4 before recording a result.
- Read-only actions flood the log and bury the ones that changed something. Reading a page is not
  supposed to be audited per call.

---

#### UAT-47 — Adding an agent to a team says what that grants

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D1 US-21/AC1–AC3; D1 H-18 |
| **Preconditions** | Workspace Alpha is signed in to the dummy site |

**Steps**

1. Go to **Alpha → Team** and open the control for adding an agent.
2. **Before confirming**, read what the screen tells you.
3. Confirm, then read the release note for this change.

**Expected observable result**

Before you confirm, the UI states the concrete consequence in plain words: **this agent will be able
to act as whoever this workspace is signed in as, on any site it is signed into, including on turns
nobody is watching.** Not a mechanism description. Not a tooltip you have to hover for. Not only in
the release note — though it is in the release note too.

**What a silent failure looks like**

- The disclosure appears **after** you confirm, or in a tooltip, or only in the release note. The
  operator learns what they granted after granting it.
- The text describes the *mechanism* ("agents on a workspace share a browser profile") rather than
  the *consequence*. Technically accurate, and it does not tell a non-engineer that they just handed
  an unattended process a live login.
- There is no disclosure at all.

---

#### UAT-48 — What a delegated agent does, and where you can see it

| | |
|---|---|
| **Priority** | P1 |
| **Traces to** | D2 US-10/AC6, US-18/AC2; D2 H-13 |
| **Preconditions** | Alpha signed in. Default chat settings — **verbose chat off**. |

**Steps**

1. In chat A1, ask Jim to **delegate** to Ray a task that involves taking a snapshot of the
   signed-in page.
2. Read the chat thread. Look for the snapshot and its output.
3. Open the **Activity panel** and expand Ray's span. Look for it there.
4. Record your own reaction to step 2 in one sentence.

**Expected observable result**

The snapshot **does not appear in the chat thread** — that is the stated behaviour, not a bug. It
**does** appear in the Activity panel when you expand the delegated span.

**Step 4 is the deliverable of this case.** If an operator watching a delegated agent read a
signed-in page finds no trace of it in the conversation, their reaction is the input the open design
question needs, and no automated test can produce it.

**What a silent failure looks like**

- It is missing from the Activity panel **too**. Then there is no surface at all, and the mitigation
  the design relies on does not exist.
- It appears in the chat thread. Different from what the spec says; record it, because a doc and a
  build disagreeing is a defect in one of them and you cannot tell which from here.

---
