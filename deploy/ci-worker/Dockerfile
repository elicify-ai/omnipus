# Omnipus CI / test-pipeline worker image.
# Toolchain baked in; the repo is pulled at runtime so the worker always tests the latest ref.
# Includes a baked-in Chromium + Playwright so the `e2e` gate can run the Playwright matrix
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

# Playwright + Chromium browser, baked into the image. Version pinned to match the repo's
# @playwright/test (^1.49.0) — keep these two in sync. The browser lives at
# /opt/ms-playwright (in the image, not on the /cache volume) so the first e2e run is
# immediate on a freshly-deployed machine. Image grows by ~500 MB; acceptable for a CI box.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
RUN npm install -g @playwright/test@1.49.0 playwright@1.49.0 \
    && playwright install chromium \
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
