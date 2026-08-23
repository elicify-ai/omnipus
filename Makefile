# =============================================================================
# CANONICAL BUILD PATHS — there are exactly three. Use one of them.
#
#   make build          Local / dev / deploy-from-source. THE default.
#   make release-build  Real release (goreleaser). Normally only CI runs this.
#   make docker-build   Container image.
#
# Do NOT run a bare `go build ./cmd/omnipus/`. It skips this file's LDFLAGS,
# which means:
#   - no Version / GitCommit / BuildTime baked in — the binary cannot be
#     traced back to a commit, and `omnipus doctor` will flag it as
#     improperly built;
#   - no `-s -w`, so the binary is ~48 MB LARGER for no benefit
#     (measured 2026-07-21: 102 MB via make vs 150 MB via bare go build).
#
# Build tags are NOT optional. `goolm,stdjson` (GO_BUILD_TAGS below) are
# required — without them pkg/channels/matrix is excluded and the build dies
# with a misleading "build constraints exclude all Go files" error. Every
# target here injects them for you; that is a reason to use the targets.
#
# Discretionary tags (append to GO_BUILD_TAGS, none are on by default):
#   lite       drops WhatsApp native AND WebRTC live-browser video
#   nogodmode  compiles out the sandbox-off ("god mode") toggle; for hosted
#   bedrock    compiles in the real AWS Bedrock provider (stub without it)
# =============================================================================

.PHONY: all build install uninstall clean help test gen-contracts verify-contracts lint-wire-types lint-tool-error-status lint-no-jpeg-screencast spa-embed release-snapshot release-build golangci-lint-version-check

# Build variables
BINARY_NAME=omnipus
BUILD_DIR=build
CMD_DIR=cmd/$(BINARY_NAME)
MAIN_GO=$(CMD_DIR)/main.go

# Version
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short=8 HEAD 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date +%FT%T%z)
GO_VERSION=$(shell $(GO) version | awk '{print $$3}')
# Build-time vars live at pkg/config: Version, GitCommit, BuildTime, GoVersion.
# pkg/config/version.go documents this path explicitly. The CLI reads Version
# via config.GetVersion(). (pkg/gateway has its own unrelated Version var.)
CONFIG_PKG=github.com/elicify-ai/omnipus/pkg/config
LDFLAGS=-X $(CONFIG_PKG).Version=$(VERSION) -X $(CONFIG_PKG).GitCommit=$(GIT_COMMIT) -X $(CONFIG_PKG).BuildTime=$(BUILD_TIME) -X $(CONFIG_PKG).GoVersion=$(GO_VERSION) -s -w

# Go variables
GO?=CGO_ENABLED=0 go
WEB_GO?=$(GO)
GO_BUILD_TAGS?=goolm,stdjson
GOFLAGS?=-v -tags $(GO_BUILD_TAGS)
# GOFLAGS_NO_GOOLM / GO_BUILD_TAGS_NO_GOOLM / the comma-empty-space helpers,
# and the PATCH_MIPS_FLAGS / PTY_PATCH_LOONG64 macros below, were removed
# 2026-08-23 (ADR-067 §6.2/§10 step 9): they existed solely to serve
# linux/mipsle and linux/loong64, both dropped from `build-all` (no evidence
# of users; mipsle alone forced this Matrix-less, goolm-less build path).
# `git log -p` on this file has the originals if a mipsle/loong64 build is
# ever revisited as its own decision.

# Golangci-lint
GOLANGCI_LINT?=golangci-lint

# Quality-gate tool versions (see the `tools` target).
# oapi-codegen's pin is READ FROM scripts/gen-contracts.sh rather than repeated
# here: that script hard-fails on a mismatch, so two hand-maintained copies of
# the version would eventually disagree and the failure would surface as a
# confusing verify-contracts diff. One source of truth, derived.
OAPI_CODEGEN_VERSION := $(shell sed -n 's/^REQUIRED_OAPI_CODEGEN_VERSION="\(.*\)"/\1/p' scripts/gen-contracts.sh)
# govulncheck is installed @latest by CI (.github/workflows/pr.yml), so local
# installs track the same moving target rather than pinning behind it.
GOVULNCHECK_VERSION?=latest
# golangci-lint MUST match the version CI gates with (.github/workflows/pr.yml's
# `golangci-lint-action` step: `version: v2.10.1`). ADR-067 §5: two different
# golangci-lint versions disagree on findings (measured: G115 differed between
# 2.10.1 and 2.12.2 in both directions) — a local "0 issues" run with the wrong
# version is not a green, it's an unmeasured claim. Bump this the moment
# pr.yml's pin changes; `make lint`/`make fix` refuse to run on a mismatch
# rather than silently trusting whatever is on PATH (see
# golangci-lint-version-check below).
GOLANGCI_LINT_VERSION := v2.10.1

# Installation
INSTALL_PREFIX?=$(HOME)/.local
INSTALL_BIN_DIR=$(INSTALL_PREFIX)/bin
INSTALL_MAN_DIR=$(INSTALL_PREFIX)/share/man/man1
INSTALL_TMP_SUFFIX=.new

# Workspace and Skills
OMNIPUS_HOME?=$(HOME)/.omnipus
WORKSPACE_DIR?=$(OMNIPUS_HOME)/workspace
WORKSPACE_SKILLS_DIR=$(WORKSPACE_DIR)/skills
BUILTIN_SKILLS_DIR=$(CURDIR)/skills

# OS detection
UNAME_S:=$(shell uname -s)
UNAME_M:=$(shell uname -m)

# Platform-specific settings
ifeq ($(UNAME_S),Linux)
	PLATFORM=linux
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),aarch64)
		ARCH=arm64
	else ifeq ($(UNAME_M),armv81)
		ARCH=arm64
	else ifeq ($(UNAME_M),loongarch64)
		ARCH=loong64
	else ifeq ($(UNAME_M),riscv64)
		ARCH=riscv64
	else ifeq ($(UNAME_M),mipsel)
		ARCH=mipsle
	else
		ARCH=$(UNAME_M)
	endif
else ifeq ($(UNAME_S),Darwin)
	PLATFORM=darwin
	WEB_GO=CGO_ENABLED=1 go
	ifeq ($(UNAME_M),x86_64)
		ARCH=amd64
	else ifeq ($(UNAME_M),arm64)
		ARCH=arm64
	else
		ARCH=$(UNAME_M)
	endif
else
	PLATFORM=$(UNAME_S)
	ARCH=$(UNAME_M)
endif

BINARY_PATH=$(BUILD_DIR)/$(BINARY_NAME)-$(PLATFORM)-$(ARCH)

# Default target
all: build

## generate: Run generate
generate:
	@echo "Run generate..."
	@rm -r ./$(CMD_DIR)/workspace 2>/dev/null || true
	@$(GO) generate ./...
	@echo "Run generate complete"

## spa-embed: Build the SPA and mirror it into pkg/gateway/spa/ for //go:embed.
## pkg/gateway/embed.go has //go:embed all:spa and pkg/gateway/spa/ is gitignored.
## Vite outputs to dist/spa; we copy it into the embed target before any go build.
spa-embed:
	@echo "Building SPA and embedding into pkg/gateway/spa/..."
	@npm ci
	@npm run build
	@rm -rf pkg/gateway/spa
	@cp -r dist/spa pkg/gateway/spa
	@echo "SPA embed complete"

## build: Build the omnipus binary for current platform
build: spa-embed generate
	@echo "Building $(BINARY_NAME) for $(PLATFORM)/$(ARCH)..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_PATH) ./$(CMD_DIR)
	@echo "Build complete: $(BINARY_PATH)"
	@ln -sf $(BINARY_NAME)-$(PLATFORM)-$(ARCH) $(BUILD_DIR)/$(BINARY_NAME)

## build-launcher: Build the omnipus-launcher (web console) binary
build-launcher:
	@echo "Building omnipus-launcher for $(PLATFORM)/$(ARCH)..."
	@mkdir -p $(BUILD_DIR)
	@if [ ! -f web/backend/dist/index.html ]; then \
		echo "Building frontend..."; \
		cd web/frontend && pnpm install && pnpm build:backend; \
	fi
	@$(WEB_GO) build $(GOFLAGS) -o $(BUILD_DIR)/omnipus-launcher-$(PLATFORM)-$(ARCH) ./web/backend
	@ln -sf omnipus-launcher-$(PLATFORM)-$(ARCH) $(BUILD_DIR)/omnipus-launcher
	@echo "Build complete: $(BUILD_DIR)/omnipus-launcher"

## build-launcher-tui: Build the omnipus-launcher TUI binary
build-launcher-tui:
	@echo "Building omnipus-launcher-tui for $(PLATFORM)/$(ARCH)..."
	@mkdir -p $(BUILD_DIR)
	@$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/omnipus-launcher-tui-$(PLATFORM)-$(ARCH) ./cmd/omnipus-launcher-tui
	@ln -sf omnipus-launcher-tui-$(PLATFORM)-$(ARCH) $(BUILD_DIR)/omnipus-launcher-tui
	@echo "Build complete: $(BUILD_DIR)/omnipus-launcher-tui"

# build-lite (ADR-067 §6.2 / §10 step 10) was removed 2026-08-23: the lite
# variant saved ~58 MB by dropping whatsmeow, was never published, and forced
# `//go:build !lite` across 36 Go source files for a saving that reached no
# one. Go source build tags are Wave 2's follow-up (ADR-067 §10 step 14,
# `!lite`/`!mipsle`/`!netbsd`/`!(freebsd && arm)`), not removed here.
# `lite-build-weekly.yml` is likewise out of this file's scope.

# Single-platform convenience targets (build-linux-arm, build-linux-arm64,
# build-linux-mipsle, build-pi-zero) were removed 2026-07-21: every platform
# they produced is already built by `build-all` below, they had zero
# references anywhere in the repo, CI, or docs, and each extra entry point
# was one more way for a build to diverge from the canonical one. To build a
# single platform ad hoc, set GOOS/GOARCH on `build-all`'s recipe line, or
# just run `make build-all` and take the artifact you need.

## build-all: Build omnipus for all shipped platforms (ADR-067 §6.1/§6.2/§10 step 9)
## Exactly four targets: linux/amd64, linux/arm64, darwin/arm64, darwin/amd64.
## NOTE: this is NOT the set .goreleaser.yaml releases — it explicitly ignores
## darwin/amd64 (goos: darwin / goarch: amd64), and ADR-067 §6.3.1 keeps it
## excluded because no Intel Mac runner is obtainable. darwin/amd64 is built
## here anyway because it is the primary development host: developers must be
## able to cross-compile it locally even while it ships to nobody. It is the
## one deliberate exception to the rule below. Also built: the container image
## (separately, via `make docker-build`). Windows, linux/arm(v6),
## linux/armv7, linux/loong64, linux/riscv64, and linux/mipsle were built here but never
## released; see ADR-067 §6.2 for why each was dropped. Do not re-add a target without
## also adding it to .goreleaser.yaml and a CI leg that exercises it (ADR-067 §6.3).
build-all: spa-embed generate
	@echo "Building for multiple platforms..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./$(CMD_DIR)
	GOOS=linux GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./$(CMD_DIR)
	GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./$(CMD_DIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./$(CMD_DIR)
	# NetBSD targets removed 2026-05-30. They have never built in this repo:
	#   (a) pkg/sandbox/hardened_exec.go references applyPlatformHardening,
	#       applyPostStartHardening, memoryLimitSupported — defined only in
	#       _linux.go / _darwin.go / _windows.go. A _netbsd.go shim would
	#       need to land before NetBSD compiles cleanly.
	#   (b) modernc.org/sqlite (vendored transitively via whatsmeow) ships a
	#       sqlite_netbsd_amd64.go with its own type errors upstream.
	# docs/operations/platform-support.md lists only Linux x86_64, Linux
	# aarch64, and macOS arm64 as supported, so the NetBSD lines were aspirational
	# rather than load-bearing — the `build` workflow on main has been red on
	# every push since 2026-04-26 purely because of these two lines.
	# Re-add once both upstream issues are resolved.
	@echo "All builds complete"

## release-snapshot: Run goreleaser locally without publishing (produces dist/ artifacts).
## Useful for verifying the release pipeline before tagging.
release-snapshot:
	@echo "Running goreleaser snapshot..."
	@goreleaser release --snapshot --clean --skip=publish,sign
	@echo "Snapshot artifacts in dist/"

## release-build: Run goreleaser to produce a real release.
## Requires GITHUB_TOKEN and a pushed tag matching the current HEAD.
## Normally invoked by .github/workflows/release.yml, not manually.
release-build:
	@echo "Running goreleaser release..."
	@goreleaser release --clean
	@echo "Release complete"

## install: Install omnipus to system and copy builtin skills
install: build
	@echo "Installing $(BINARY_NAME)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	# Copy binary with temporary suffix to ensure atomic update
	@cp $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_BIN_DIR)/$(BINARY_NAME)$(INSTALL_TMP_SUFFIX)
	@chmod +x $(INSTALL_BIN_DIR)/$(BINARY_NAME)$(INSTALL_TMP_SUFFIX)
	@mv -f $(INSTALL_BIN_DIR)/$(BINARY_NAME)$(INSTALL_TMP_SUFFIX) $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Installed binary to $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Installation complete!"

## uninstall: Remove omnipus from system
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@rm -f $(INSTALL_BIN_DIR)/$(BINARY_NAME)
	@echo "Removed binary from $(INSTALL_BIN_DIR)/$(BINARY_NAME)"
	@echo "Note: Only the executable file has been deleted."
	@echo "If you need to delete all configurations (config.json, workspace, etc.), run 'make uninstall-all'"

## uninstall-all: Remove omnipus and all data
uninstall-all:
	@echo "Removing workspace and skills..."
	@rm -rf $(OMNIPUS_HOME)
	@echo "Removed workspace: $(OMNIPUS_HOME)"
	@echo "Complete uninstallation done!"

## clean: Remove build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR)
	@echo "Clean complete"

## vet: Run go vet for static analysis
vet: generate
	@$(GO) vet $(GOFLAGS) ./...

## test: Test Go code
test: generate
	@$(GO) test $(GOFLAGS) ./...

## golangci-lint-version-check: Fail loudly if $(GOLANGCI_LINT) isn't the version CI gates with.
## ADR-067 §5: a green measured with a different instrument is not a green — this refuses
## to run lint/fix at all rather than silently trusting whatever golangci-lint is on PATH.
golangci-lint-version-check:
	@if ! command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		echo "ERROR: golangci-lint not found on PATH (looked for '$(GOLANGCI_LINT)')."; \
		echo "  Install the pinned version: make tools"; \
		exit 1; \
	fi; \
	found=$$($(GOLANGCI_LINT) version --short 2>/dev/null); \
	want=$(GOLANGCI_LINT_VERSION:v%=%); \
	if [ "$$found" != "$$want" ]; then \
		echo "ERROR: golangci-lint version mismatch."; \
		echo "  found:  $$found"; \
		echo "  wanted: $$want  (pinned in Makefile::GOLANGCI_LINT_VERSION, matching .github/workflows/pr.yml)"; \
		echo "  A different golangci-lint version can disagree on findings (ADR-067 §5) —"; \
		echo "  a local run with the wrong version is not a trustworthy green."; \
		echo "  Fix: make tools   (installs golangci-lint@$(GOLANGCI_LINT_VERSION) into \$$(go env GOPATH)/bin)"; \
		exit 1; \
	fi

## fmt: Format Go code
fmt: golangci-lint-version-check
	@$(GOLANGCI_LINT) fmt

## lint: Run linters
lint: golangci-lint-version-check
	@$(GOLANGCI_LINT) run --build-tags $(GO_BUILD_TAGS)

## fix: Fix linting issues
fix: golangci-lint-version-check
	@$(GOLANGCI_LINT) run --fix --build-tags $(GO_BUILD_TAGS)

## deps: Download dependencies
deps:
	@$(GO) mod download
	@$(GO) mod verify

## tools: Install the Go CLI tools the quality gates need (contracts + vuln scan)
## Versions are PINNED to what CI installs (.github/workflows/pr.yml). oapi-codegen
## in particular must match exactly: scripts/gen-contracts.sh hard-fails on a
## version mismatch because its generated output is version-dependent in ways
## that are not confined to comments, so a different version produces a spurious
## verify-contracts diff.
##
## The remaining contract tooling (@redocly/cli, openapi-typescript,
## openapi-zod-client) are npm devDependencies resolved via `npx --no-install`,
## so `npm ci` covers them — they are deliberately NOT installed here.
tools:
	@echo "Installing pinned Go tools into $$($(GO) env GOPATH)/bin ..."
	@CGO_ENABLED=0 go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION)
	@CGO_ENABLED=0 go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@CGO_ENABLED=0 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@echo "Done. Ensure $$($(GO) env GOPATH)/bin is on PATH."

## tools-check: Report whether each quality-gate tool is present and correctly versioned
tools-check:
	@fail=0; \
	if command -v oapi-codegen >/dev/null 2>&1; then \
		v=$$(oapi-codegen --version 2>/dev/null | tail -1 | tr -d '[:space:]'); \
		if [ "$$v" = "$(OAPI_CODEGEN_VERSION)" ]; then echo "  oapi-codegen   $$v (pinned OK)"; \
		else echo "  oapi-codegen   $$v (WRONG, want $(OAPI_CODEGEN_VERSION)) -> make tools"; fail=1; fi; \
	else echo "  oapi-codegen   MISSING -> make tools"; fail=1; fi; \
	if command -v govulncheck >/dev/null 2>&1; then echo "  govulncheck    present"; \
	else echo "  govulncheck    MISSING -> make tools"; fail=1; fi; \
	if command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		glv=$$($(GOLANGCI_LINT) version --short 2>/dev/null); \
		gwant=$(GOLANGCI_LINT_VERSION:v%=%); \
		if [ "$$glv" = "$$gwant" ]; then echo "  golangci-lint  $$glv (pinned OK)"; \
		else echo "  golangci-lint  $$glv (WRONG, want $$gwant) -> make tools"; fail=1; fi; \
	else echo "  golangci-lint  MISSING -> make tools"; fail=1; fi; \
	if [ -d node_modules ]; then echo "  node_modules   present (redocly/openapi-* resolve via npx)"; \
	else echo "  node_modules   MISSING -> npm ci"; fail=1; fi; \
	exit $$fail

## update-deps: Update dependencies
update-deps:
	@$(GO) get -u ./...
	@$(GO) mod tidy

## check: Run vet, fmt, and verify dependencies
check: deps fmt vet test

## run: Build and run omnipus
run: build
	@$(BUILD_DIR)/$(BINARY_NAME) $(ARGS)

DOCKER_IMAGE ?= omnipus:latest

## docker-build: Build the Omnipus container image
docker-build:
	@echo "Building Omnipus container image (docker/Dockerfile.heavy)..."
	docker build -f docker/Dockerfile.heavy -t $(DOCKER_IMAGE) .

# Was `docker compose -f docker/docker-compose.yml build ...`, which exited 0
# while building NOTHING ("No services to build", verified 2026-08-23): after
# the ADR-067 image consolidation that compose file is pull-only — both
# services name a published ghcr.io tag and neither has a build section. A
# target that prints "Building..." and silently builds nothing is exactly the
# false green docs/internal/false-green-patterns.md exists to stop, so this
# now invokes the one real Dockerfile directly.

# docker-build-full / docker-run-full / docker-run-agent-full (ADR-067 §1/§6.2)
# were removed 2026-08-23: the project ships ONE container image now
# (docker/Dockerfile.heavy, which already carries Node.js 24 for MCP servers
# and the browser dependencies — what "full" used to mean). The "minimal"
# vs "full" split, docker/docker-compose.full.yml, and the other four
# Dockerfiles were removed by the sibling pass on docker/ (ADR-067 §10 step
# 11); this file only removes the Makefile targets that pointed at them.

## docker-test: Smoke-test the runtime tooling in the container image
docker-test:
	@echo "Testing runtime tooling in the container image..."
	@chmod +x scripts/test-docker-mcp.sh
	@OMNIPUS_TEST_IMAGE=$(DOCKER_IMAGE) ./scripts/test-docker-mcp.sh

## docker-run: Run omnipus gateway in Docker
docker-run:
	docker compose -f docker/docker-compose.yml --profile gateway up

## docker-run-agent: Run omnipus agent in Docker (interactive)
docker-run-agent:
	docker compose -f docker/docker-compose.yml run --rm omnipus-agent

## docker-clean: Clean Docker images and volumes
docker-clean:
	docker compose -f docker/docker-compose.yml down -v
	-docker rmi $(DOCKER_IMAGE) 2>/dev/null
	docker rmi omnipus:latest 2>/dev/null || true


## build-macos-app: Build Omnipus macOS .app bundle (no terminal window)
build-macos-app:
	@echo "Building macOS .app bundle..."
	@if [ "$(UNAME_S)" != "Darwin" ]; then \
		echo "Error: This target is only available on macOS"; \
		exit 1; \
	fi
	@cd web && $(MAKE) build && cd ..
	@./scripts/build-macos-app.sh $(BINARY_NAME)-$(PLATFORM)-$(ARCH)
	@echo "macOS .app bundle created: $(BUILD_DIR)/Omnipus.app"

## gen-contracts: Regenerate all contract artifacts (TS types, zod schemas, Go types)
gen-contracts:
	./scripts/gen-contracts.sh

## lint-wire-types: Fail if hand-written wire-format types exist outside generated directories
## Enforces hard-constraint #8 (CLAUDE.md contract-first rule).
## Add `// not-wire-format` on the struct/interface declaration line to suppress a false positive.
lint-wire-types:
	bash scripts/check-no-handwritten-wire-types.sh

## lint-tool-error-status: Fail if any SPA component derives tool-call error state from status.type==='incomplete'
## Regression guard for issue #617 — see scripts/check-no-tool-error-from-status.sh's header comment.
lint-tool-error-status:
	bash scripts/check-no-tool-error-from-status.sh

## lint-no-jpeg-screencast: Fail if the deleted JPEG live-browser screencast path reappears
## Regression guard for ADR-061 — see scripts/check-no-jpeg-screencast.sh's header comment.
lint-no-jpeg-screencast:
	bash scripts/check-no-jpeg-screencast.sh

## verify-contracts: Regenerate contracts, run wire-type lint, typecheck TS, fail if anything has drifted
# Note: `tsc --noEmit` (without -b) is a silent no-op on a project-references
# root. Always use `tsc -b --noEmit` here and in CI. See F6 / npm run typecheck.
verify-contracts: gen-contracts lint-wire-types
	npx tsc -b --noEmit
	git diff --exit-code -- contracts/ pkg/api/generated/ src/lib/api/generated/ pkg/gateway/inboundschemas/

## verify-asyncapi-drift: Run the AsyncAPI Go generator and fail if the output differs from the committed file
# Standalone drift gate for pkg/api/generated/asyncapi_types.gen.go. The
# generator's matchingNamedInlineGoType / sameSchemaShape mechanism (added
# alongside the inline-mirror hand-fix retirement) can silently fall back to
# an anonymous inline-struct emit if a future schema author's inline mirror
# drifts from its sibling named schema. A drift here means the previously
# hand-adjusted `*ErrorPayload` / `*ReplayErrorPayload` pointers revert to
# anonymous structs and the Zod/JSON contract breaks.
#
# CI hook: also reached via `make verify-contracts` (which runs gen-contracts
# first, then a git diff --exit-code over the whole pkg/api/generated/ tree).
# This target exists as a focused, runnable-by-itself check for local dev
# and as documentation of the drift surface. Exit 0 = committed file is
# generator-idempotent; non-zero = regenerate and commit the diff.
verify-asyncapi-drift:
	@if [ ! -f contracts/asyncapi.yaml ]; then \
		echo "contracts/asyncapi.yaml not found; cannot verify AsyncAPI drift"; \
		exit 1; \
	fi
	CGO_ENABLED=0 $(GO) run ./scripts/gen-asyncapi-go/ \
		contracts/asyncapi.yaml \
		pkg/api/generated/asyncapi_types.gen.go
	git diff --exit-code -- pkg/api/generated/asyncapi_types.gen.go

## help: Show this help message
help:
	@echo "omnipus Makefile"
	@echo ""
	@echo "Usage:"
	@echo "  make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sort | awk -F': ' '{printf "  %-16s %s\n", substr($$1, 4), $$2}'
	@echo ""
	@echo "Examples:"
	@echo "  make build              # Build for current platform"
	@echo "  make install            # Install to ~/.local/bin"
	@echo "  make uninstall          # Remove from /usr/local/bin"
	@echo "  make install-skills     # Install skills to workspace"
	@echo "  make docker-build       # Build Docker image"
	@echo "  make docker-test        # Test MCP tools in Docker"
	@echo ""
	@echo "Environment Variables:"
	@echo "  INSTALL_PREFIX          # Installation prefix (default: ~/.local)"
	@echo "  WORKSPACE_DIR           # Workspace directory (default: ~/.omnipus/workspace)"
	@echo "  VERSION                 # Version string (default: git describe)"
	@echo ""
	@echo "Current Configuration:"
	@echo "  Platform: $(PLATFORM)/$(ARCH)"
	@echo "  Binary: $(BINARY_PATH)"
	@echo "  Install Prefix: $(INSTALL_PREFIX)"
	@echo "  Workspace: $(WORKSPACE_DIR)"
