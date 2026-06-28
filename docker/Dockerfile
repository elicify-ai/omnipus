# ============================================================
# Stage 1: Build the SPA (frontend) into dist/spa/
# ============================================================
FROM node:24-alpine AS spa-builder

WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

# ============================================================
# Stage 2: Build the Go binary with the SPA embedded
# ============================================================
FROM golang:1.26.3-alpine AS go-builder

RUN apk add --no-cache git make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# pkg/gateway/embed.go has //go:embed all:spa and pkg/gateway/spa/ is
# gitignored. Mirror the Vite output from stage 1 into the embed target
# before `go build` reads the directive.
COPY --from=spa-builder /src/dist/spa /src/pkg/gateway/spa

RUN CGO_ENABLED=0 go build -tags goolm,stdjson \
    -ldflags "-s -w" \
    -o /out/omnipus ./cmd/omnipus

# ============================================================
# Stage 3: Minimal runtime image
# ============================================================
FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata curl

# Gateway defaults: 5000 = SPA + API, 5001 = preview iframes.
# See gateway.preview_port / preview_listener_enabled in pkg/config.
EXPOSE 5000 5001

# Inside a container the gateway must bind 0.0.0.0 so the published port
# mapping is reachable from the host. The on-disk config seed
# (pkg/datamodel/init.go) defaults to "localhost" because that is the
# right default for a local-dev install (network-private by default), so
# we override here rather than at the seed layer.
# pkg/config GatewayConfig already supports OMNIPUS_GATEWAY_HOST via env
# tag + env.Parse in loadConfigInternal — no code changes needed.
ENV OMNIPUS_GATEWAY_HOST=0.0.0.0

# Default sandbox mode = permissive inside Docker. The default unprivileged
# container seccomp profile blocks syscalls the hardened-exec path requires,
# causing "fork/exec /bin/sh: permission denied" with sandbox=enforce.
# Override with OMNIPUS_SANDBOX_MODE=enforce if you have configured custom
# capabilities. See pkg/gateway/sandbox_apply.go for the full rationale.
ENV OMNIPUS_SANDBOX_MODE=permissive

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -fsS http://localhost:5000/health || exit 1

COPY --from=go-builder /out/omnipus /usr/local/bin/omnipus

# Create non-root user.
RUN addgroup -g 1000 omnipus && \
    adduser -D -u 1000 -G omnipus omnipus

USER omnipus

# --allow-empty (-E) is required on first boot — the seed config has no
# providers configured yet (the SPA onboarding wizard adds the first
# one), and without -E the gateway aborts with
# "error creating provider: no providers configured" before the SPA is
# reachable. Once onboarding completes the providers list is populated
# and the flag is a no-op, so it is safe to leave in the default CMD.
# Operators can override with `docker run ... omnipus:tag gateway` (no
# args) if they want the strict-boot behavior for production where the
# config is fully provisioned ahead of time.
ENTRYPOINT ["omnipus"]
CMD ["start", "--allow-empty"]
