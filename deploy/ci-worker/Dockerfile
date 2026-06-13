# Omnipus CI / test-pipeline worker image.
# Toolchain baked in; the repo is pulled at runtime so the worker always tests the latest ref.
FROM golang:1.26-bookworm

ENV DEBIAN_FRONTEND=noninteractive \
    GOTOOLCHAIN=auto \
    NODE_MAJOR=22

# Node 22 + build deps (gcc for the CGO=1 build gate, ripgrep/jq/git/make for scripts).
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gnupg; \
    mkdir -p /etc/apt/keyrings; \
    curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key | gpg --dearmor -o /etc/apt/keyrings/nodesource.gpg; \
    echo "deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_${NODE_MAJOR}.x nodistro main" > /etc/apt/sources.list.d/nodesource.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends nodejs build-essential git make ripgrep jq openssh-client; \
    rm -rf /var/lib/apt/lists/*; \
    node -v; npm -v; go version; gcc --version | head -1

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
