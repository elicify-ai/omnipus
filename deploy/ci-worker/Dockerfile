# Omnipus CI / test-pipeline worker image.
# Toolchain baked in; the repo is pulled at runtime so the worker always tests the latest ref.
# Includes baked-in Chromium/Firefox/WebKit + Playwright so the `e2e` gate can run the matrix
# (chat, subagent, handoff, …) without re-downloading ~500 MB of browser binaries on every run.
FROM golang:1.26-bookworm

ENV DEBIAN_FRONTEND=noninteractive \
    GOTOOLCHAIN=auto \
    NODE_MAJOR=22

# Node 22 + build deps (gcc for the CGO=1 build gate, ripgrep/jq/git/make for scripts) +
# Playwright Chromium system deps (libnss3/libnspr4/libgbm1/libasound2/… — the metapackage the
# `playwright install --with-deps` resolver would install; we pin it here to make the image
# build deterministic and to keep the system-dep surface auditable in source review).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        curl ca-certificates gnupg \
        libnss3 libnspr4 libatk1.0-0 libatk-bridge2.0-0 libcups2 libdrm2 \
        libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 \
        libgbm1 libasound2 libpango-1.0-0 libcairo2; \
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

# Persistent caches live on the mounted volume (/cache) so repeat runs are fast.
ENV GOPATH=/cache/go \
    GOCACHE=/cache/go-build \
    GOMODCACHE=/cache/go/pkg/mod \
    npm_config_cache=/cache/npm

WORKDIR /work
COPY runci.sh /usr/local/bin/runci.sh
RUN chmod +x /usr/local/bin/runci.sh

# Stay alive so the orchestrator can `fly ssh console` / `fly machine exec` gate commands.
CMD ["sleep", "infinity"]
