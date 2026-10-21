# Omnipus CI / test-pipeline worker image.
# Toolchain baked in; the repo is pulled at runtime so the worker always tests the latest ref.
# Includes baked-in Chromium/Firefox/WebKit + Playwright so the `e2e` gate can run the matrix
# (chat, subagent, handoff, …) without re-downloading ~500 MB of browser binaries on every run.
FROM golang:1.26-bookworm

ENV DEBIAN_FRONTEND=noninteractive \
    GOTOOLCHAIN=auto \
    NODE_MAJOR=22

# Node 22 + build deps (gcc for the CGO=1 build gate, ripgrep/jq/git/make for scripts) +
# python3 + python-is-python3: the document-runtime probe (pkg/documentruntime.ProbeArgv)
# runs `python -m ...`; without it pkg/agent TestNewAgentInstanceWiresDocumentRuntime... sees
# exit 127. runci.sh also installs them at run time for machines on an older image.
# Playwright Chromium system deps (libnss3/libnspr4/libgbm1/libasound2/… — the metapackage the
# `playwright install --with-deps` resolver would install; we pin it here to make the image
# build deterministic and to keep the system-dep surface auditable in source review).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        curl ca-certificates gnupg \
        libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 \
        libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 \
        libgbm1 libasound2 libpango-1.0-0 libcairo2 \
        python3 python-is-python3; \
    mkdir -p /etc/apt/keyrings; \
    curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" > /etc/apt/sources.list.d/nodesource.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends nodejs build-essential git make ripgrep jq openssh-client fonts-liberation; \
    rm -rf /var/lib/apt/lists/*; \
    node -v; npm -v; go version; gcc --version | head -1

# Go codegen tool needed by scripts/gen-contracts.sh (Step 3/5). Baked into a stable GOBIN on PATH.
ENV GOBIN=/usr/local/gobin
ENV PATH=$GOBIN:$PATH
# Pin to the exact version the committed pkg/api/generated/ was produced with (the version is stamped
# into the file header → @latest causes spurious verify-contracts drift).
RUN go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.0 && oapi-codegen --version

# Playwright + browsers, baked into the image. Version pinned to match the repo's
# @playwright/test — keep these two in sync. The browsers live at /opt/ms-playwright (in the
# image, not on the /cache volume) so the first e2e run is immediate on a freshly-deployed
# machine.
#
# ADR-067: THREE engines, not one. The preview-isolation specs run on chromium, firefox and
# webkit — containment differs between engines, which is why the measurement covered all three.
# With only chromium baked in, the firefox and webkit projects fail at launch with
# `browserType.launch: Executable doesn't exist` — the exact phantom-failure signature runci.sh
# documents from 2026-07-26, where 48 "failures" each took 4-6ms because no browser ever started.
#
# `--with-deps` rather than the pinned apt list above: that list is Chromium's, and WebKit needs
# a substantially different set (gstreamer, libwoff, libenchant, libmanette, …) that varies by
# Debian release and Playwright version. Hand-pinning it would be a list nobody can audit against
# the version actually installed — the resolver knows, and being wrong here is a silent
# launch failure rather than a build error.
#
# Version bumped 1.49.0 -> 1.61.1 to match the repo (package.json: "@playwright/test": "^1.61.1").
# They had drifted, which is what the "keep these two in sync" note above was there to prevent:
# a mismatched image ships browser revisions the runner does not want, and runci.sh then has to
# download the right ones at runtime on every single run.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
RUN npm install -g @playwright/test@1.61.1 playwright@1.61.1 \
    && playwright install --with-deps chromium firefox webkit \
    && rm -rf /root/.npm /tmp/*

# The non-root user runci.sh re-execs the Go/node test gates as (see ci_prepare there). It is
# created here so a fresh image needs no run-time useradd; NO `USER` instruction on purpose: the
# machine's SSH shell and every root-requiring step (git, apt, chown, Xvfb for e2e) stay root,
# and runci.sh drops to `ci` only for the gates, via runuser.
RUN useradd -m -s /bin/bash ci

# Persistent caches live on the mounted volume (/cache) so repeat runs are fast.
ENV GOPATH=/cache/go \
    GOCACHE=/cache/go-build \
    GOMODCACHE=/cache/go/pkg/mod \
    npm_config_cache=/cache/npm

WORKDIR /work
COPY runci.sh /usr/local/bin/runci.sh
COPY idle-watchdog.sh /usr/local/bin/idle-watchdog.sh
RUN chmod +x /usr/local/bin/runci.sh /usr/local/bin/idle-watchdog.sh

# Every boot starts the idle-watchdog backstop (2026-10-21, founder: the self-stop timer must
# run on every machine, not only on dispatcher-driven boots — a machine started by hand, or by
# an SSH console auto-start, otherwise has no watchdog and bills until someone notices). A
# watchdog that fails to start must not keep the box from booting: `;`, not `&&`.
#
# How the watchdog stops the machine: NOT `poweroff`. Here poweroff is a symlink to systemctl,
# there is no systemd under /fly/init, and it returns 1 — measured live 2026-10-21, the watchdog
# logged "POWERING OFF" every poll and the machine stayed up. Fly stops a machine when its main
# process exits; exit 0 is not a failure, so the on-failure restart policy leaves it stopped.
# The watchdog's poweroff command therefore only drops a halt marker in /tmp, and the main
# process below (instead of `sleep infinity`) exits 0 as soon as it appears. /tmp is wiped on
# every stop, so a marker can never outlive the machine life that wrote it.
# Stays alive otherwise so the orchestrator can `fly ssh console` / `fly machine exec` gates.
RUN printf '#!/bin/sh\n# idle-watchdog poweroff hook: ask the main process to exit 0 (see Dockerfile)\n: > /tmp/ci-machine.halt\n' \
        > /usr/local/bin/ci-machine-halt \
    && chmod +x /usr/local/bin/ci-machine-halt
ENV IDLE_WATCHDOG_POWEROFF_CMD=/usr/local/bin/ci-machine-halt
CMD ["sh", "-c", "/usr/local/bin/idle-watchdog.sh start; while [ ! -e /tmp/ci-machine.halt ]; do sleep 2; done; echo 'ci-worker: idle-watchdog halt marker found, main process exits 0, Fly stops the machine'; exit 0"]
