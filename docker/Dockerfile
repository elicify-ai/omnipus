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
FROM golang:1.26-alpine AS go-builder

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

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -fsS http://localhost:5000/health || exit 1

COPY --from=go-builder /out/omnipus /usr/local/bin/omnipus

# Create non-root user.
RUN addgroup -g 1000 omnipus && \
    adduser -D -u 1000 -G omnipus omnipus

USER omnipus

ENTRYPOINT ["omnipus"]
CMD ["gateway"]
