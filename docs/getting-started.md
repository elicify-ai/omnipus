# Getting started — your first 10 minutes

Welcome! This is the fastest path from nothing to your first real conversation with
your AI team. Every step has something concrete to do. Let's go.

## 1. What you need

You need a machine or Docker — a Linux or macOS computer, or anywhere you can run Docker.

You also need one LLM API key. This is the AI "brain" behind your agents. The easiest first
pick is **[OpenRouter](providers.md)** or **OpenAI** — sign up and copy an API key.
Prefer fully local and free? Use **[Ollama](providers.md)**, which runs models on
your own machine with no key at all.

That's it. See **[providers.md](providers.md)** for the full list of 35+ supported providers.

## 2. Install in one line

**Native install** (Linux/macOS):

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
```

**Or with Docker** (one line):

```bash
docker run -d -p 127.0.0.1:5000:5000 -p 127.0.0.1:5001:5001 -v "$PWD/data:/root/.omnipus" ghcr.io/elicify-ai/omnipus:latest
```

These are the quick paths. For every install option (Docker variants,
build-from-source, and more), see the **Install section of the
[project README](../README.md)**.

If you installed natively, start the app:

```bash
omnipus start
```

Then open **http://localhost:5000** in your browser. (With Docker, it's already
running — just open that address.)

## 3. First boot: the setup wizard

The first time you open the web app, a short wizard walks you through setup. It only
takes a minute:

1. **Welcome** — a quick hello. Click to begin.
2. **Provider + API key** — pick your provider (e.g. OpenRouter), paste your API key,
   and hit **Test connection** to confirm it works.
3. **Admin account** — choose a username and password (at least 8 characters). This is
   your login.
4. **Model** — pick which model your agents should use, from the provider you just tested.
5. **Complete** — done! The wizard logs you straight in.

> 📸 **Screenshot needed:** the onboarding wizard mid-flow — the Provider step with an
> API key pasted and the "Test connection" button visible.

![The Omnipus login screen](marketing/screenshots/01-login.png)
*After setup, this is where you sign back in.*

## 4. Say hi to Mia

Open **Chat** from the sidebar. By default, you're already talking to **Mia**, your
Coach & Guide. Type this and send it:

> **Hi — what can this do?**

Mia will introduce the team and suggest what to try next.

![The empty chat with Mia](marketing/screenshots/02-chat-empty-mia.png)
*Mia greets you on first open — just start typing.*

## 5. Watch a handoff

Now ask for something specific. Try:

> **I need help building a small website.**

Mia recognizes this is a building task and **hands you off to Jim**, the general-purpose
agent — all in the same conversation. You'll see a small handoff card in the chat noting
the switch, and Jim picks up the thread without you repeating yourself.

![Mia handing off to Jim](marketing/screenshots/16-handoff-mia-to-jim.png)
*Control passes to a teammate in the same chat — no copy-paste.*

## 6. Try the specialists

You can also work with a specialist directly.

### Ask Ray to research something

> **Ray, what are the top 3 open-source note-taking apps right now? Include links.**

Ray searches the web and answers with citations — he won't bluff.

![Ray running a research task](marketing/screenshots/14-ray-research-demo.png)
*Ray returns answers backed by sources.*

### Ask Jim to create a file or scope a task

> **Jim, create a file called notes.md with a short to-do list for launching a blog.**

**Want to pick an agent on purpose?** Use the **agent picker** at the top of the chat
to switch teammates mid-conversation, any time.

![The agent picker menu](marketing/screenshots/12-agent-picker-menu.png)
*Switch agents whenever you like.*

## 7. Build your own agent with Ava

Need a teammate the built-ins don't cover? Ask **Ava**:

> **Ava, I want an agent that helps me plan weekly meals.**

Ava interviews you with a few questions, then builds a brand-new custom agent for you.
When she's done, it shows up in your roster alongside the others.

![Ava building a new agent](marketing/screenshots/15-ava-build-agent.png)
*Ava turns a short interview into a custom agent.*

## 8. Where to go next

**Connect a chat channel** (talk to your agents from Telegram, Discord, and more): **[chat-apps.md](chat-apps.md)**

**Prefer the terminal?** **[Using the CLI](using-omnipus-cli.md)**

**Full tour of the web app:** **[Using the web app](using-omnipus-ui.md)**

**The mental model behind it all:** **[How Omnipus works](concepts.md)**

**Something not working?** **[Troubleshooting](troubleshooting.md)**
