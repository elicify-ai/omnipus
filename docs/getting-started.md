# Getting started

This page takes you from an empty machine to your first conversation with an agent. You install Omnipus, create the one admin account, and start chatting in the workspace that is created for you.

## What it is

Omnipus is a self-hosted app: you run it on your own machine or server, and everything it stores stays in one directory there. A single binary — or a single container — serves the web interface and the API on one port, `5000`. Nothing else is exposed.

The first run asks you for two things: the admin account that will own this install, and one AI provider whose models power the agents. You bring the provider. A key from OpenAI or OpenRouter works, and a provider that runs models on your own machine, such as Ollama, needs no key at all. Omnipus creates everything else: a workspace named **My Workspace**, and a first agent, **Mia**, ready to talk.

## When you would use it

Use this page once, on a fresh install or a new machine. If Omnipus is already running, sign in at the same address and go straight to your [workspaces](workspaces.md). If it will not start, begin with [troubleshooting](troubleshooting.md).

## How to go from nothing to your first conversation

1. **Get a provider key.** Sign up with OpenAI or OpenRouter and copy the API key. The setup screen suggests both. Running models locally with Ollama also works and needs no key.
2. **Install Omnipus.** Pick one of the three methods in the table below and run its command.
3. **Start it.** With a native install, run `omnipus start` in a terminal. With Docker, the container from step 2 is already running.
4. **Open `http://localhost:5000`.** On a fresh install the app takes you straight to the setup wizard. On a Mac, the first start may ask you to approve the binary under System Settings, then Privacy & Security.
5. **Work through the wizard's three screens.** The wizard table below describes each one. **Finish** creates your account and saves the provider in one step, then logs you in.
6. **Select Start chatting** on the "Mia — Assistant" screen. The app opens **My Workspace** on its **Chat** tab.
7. **Type a message and send it.** Mia answers. Use the agent picker next to the message box to talk to a different agent. The workspace's other tabs — Tasks, Calendar, Library, Team — sit at the top of the screen.

Three ways to install, and what each is good for:

| Method | Good for | Command |
|---|---|---|
| One-line installer | Linux (x86-64 or ARM64) and Apple-Silicon Macs | below |
| Docker, published image | Windows, Intel Macs, and any machine with Docker | below |
| Build from source | Developers contributing to Omnipus | [project README](../README.md) |

Native install:

```bash
curl -sSL https://raw.githubusercontent.com/elicify-ai/omnipus/main/scripts/install.sh | sh
```

Docker:

```bash
mkdir -p ./data
docker run -d \
  -p 127.0.0.1:5000:5000 \
  -e OMNIPUS_GATEWAY_HOST=0.0.0.0 \
  -v "$PWD/data:/root/.omnipus" \
  ghcr.io/elicify-ai/omnipus:latest
```

`OMNIPUS_GATEWAY_HOST=0.0.0.0` is required in Docker. By default Omnipus listens on the loopback address of its own machine, and inside a container that is the container itself, not yours. Every Docker option, including a larger image with browser support, is in [docker.md](docker.md).

The wizard is three numbered screens plus a closing screen:

| Screen | What you do | What Omnipus does |
|---|---|---|
| 1 — What should I call you? | Type the admin username | Accepts any name that is not empty |
| 2 — Set your password | Choose a password of at least 8 characters, twice | Shows how strong it is |
| 3 — Add a model key | Pick a provider, paste the key, pick "Model for your first agent" | Checks the connection for the model you picked |
| Last — Mia, Assistant | Select Start chatting | Logs you in and opens the Chat tab |

The check on screen 3 runs when you pick a model, or when you press its Check connection button. **Finish** stays locked until that check passes for the model you selected, so a working key with a broken model cannot get through setup.

## Limits and things to watch

- **One port, not two.** The web interface, the API, and agent-built previews all arrive through port `5000`. There is no second port `5001`; an older guide that opens one is out of date.
- **Port 5000 is a popular port.** The macOS AirPlay receiver and many dev tools use it. If it is taken, pick another port with `gateway.port` in the config, or the `OMNIPUS_GATEWAY_PORT` variable, and open that instead. [troubleshooting](troubleshooting.md) walks through it.
- **Keep the address private until setup is done.** Whoever completes the wizard owns the install. The default binding, and the Docker command above, keep it reachable only from your own machine.
- **The installer covers three platforms.** Linux x86-64, Linux ARM64, and Apple-Silicon Macs. Windows and Intel Macs use the Docker image instead.
- **Back up the master key.** Omnipus encrypts every secret it stores. The encryption key is `master.key`, inside the data directory: `~/.omnipus/` natively, the mounted `./data` folder with Docker. Lose it and every stored credential is unrecoverable.
- **The published Docker image has no browser.** Agents that browse the web need the larger image you build yourself — see [docker.md](docker.md).

## Related pages

- [workspaces](workspaces.md) — what the container you just landed in holds, and how to run more than one
- [agents](agents.md) — the four agents that ship with Omnipus, and how to add your own
- [concepts](concepts.md) — the mental model behind agents, workspaces, and connectors
- [settings](settings.md) — changing providers, models, and limits after the install
- [connectors](connectors.md) — reaching your agents from Telegram, Discord, and other apps
- [troubleshooting](troubleshooting.md) — the first stop when something does not start
