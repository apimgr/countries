# Go project build targets. Toolchain commands run in the CasjaysDev Go image.
PROJECT_NAME := $(shell sed -n 's/^project_name:[[:space:]]*//p' IDEA.md 2>/dev/null | head -n1 | tr -d '[:space:]')
ifeq ($(PROJECT_NAME),)
PROJECT_NAME := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)(\.git)?$$|\1|' || basename "$$(pwd)")
endif
PROJECT_ORG := $(shell sed -n 's/^project_org:[[:space:]]*//p' IDEA.md 2>/dev/null | head -n1 | tr -d '[:space:]')
ifeq ($(PROJECT_ORG),)
PROJECT_ORG := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)/[^/]+(\.git)?$$|\1|' || basename "$$(dirname "$$(pwd)")")
endif
INTERNAL_NAME := $(shell sed -n 's/^internal_name:[[:space:]]*//p' IDEA.md 2>/dev/null | head -n1 | tr -d '[:space:]')
ifeq ($(INTERNAL_NAME),)
INTERNAL_NAME := $(PROJECT_NAME)
endif
PROJECTNAME := $(PROJECT_NAME)
PROJECTORG := $(PROJECT_ORG)
VERSION ?= $(shell v=; [ -r release.txt ] && v=$$(tr -d '[:space:]' < release.txt); if [ -n "$$v" ]; then echo "$$v"; else git rev-parse --short=7 HEAD 2>/dev/null || echo devel; fi)
BUILD_EPOCH := $(shell date -u +%s)
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +%Y-%m-%dT%H:%M:%SZ)
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo N/A)
OFFICIAL_SITE := $(shell if [ -s site.txt ]; then cat site.txt; else printf '%s' "$(OFFICIAL_SITE)"; fi)
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64,freebsd/amd64,freebsd/arm64
GUI_PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64
BUILD_GUI ?= 0
BINDIR ?= binaries
RELDIR ?= releases
REGISTRY ?= ghcr.io/$(PROJECTORG)/$(PROJECTNAME)

GO_CACHE ?= $(HOME)/go/pkg/mod
GO_BUILD ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)
DOCKER_MEM ?= 4g
DOCKER_CPUS ?= 2
DOCKER_CPU_SHARES ?= 256
DOCKER_TIMEOUT ?= 3600
BUILD_OUTPUT_MOUNT = $(if $(filter /%,$(BINDIR)),-v $(BINDIR):$(BINDIR))

LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)
CLIENT_LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)

GO_DOCKER_RUN := timeout -k 30 $(DOCKER_TIMEOUT) docker run --rm --no-healthcheck \
	--name $(PROJECT_NAME)-$$(tr -dc 'a-z0-9' </dev/urandom | head -c8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) --cpu-shares=$(DOCKER_CPU_SHARES) \
	-v $(PWD):/app \
	$(BUILD_OUTPUT_MOUNT) \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
	-e BUILD_LDFLAGS="$(LDFLAGS)" -e CLIENT_LDFLAGS="$(CLIENT_LDFLAGS)"
GO_DOCKER := $(GO_DOCKER_RUN) casjaysdev/go:latest

.PHONY: dev local build test test-docker test-e2e test-incus release-check release beta docker clean
.DEFAULT_GOAL := dev

# Build server and CLI for quick local development into a temporary directory.
dev:
	@mkdir -p $(GO_CACHE) $(GO_BUILD) "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"
	@BUILD_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX") && \
	$(GO_DOCKER_RUN) -v "$$BUILD_DIR:/build" casjaysdev/go:latest sh -c 'set -eu; go mod tidy; go generate ./src/graphql; go build -trimpath -o /build/$(PROJECT_NAME) ./src; go build -trimpath -o /build/$(PROJECT_NAME)-cli ./src/client; if [ "$(BUILD_GUI)" = 1 ]; then os=$$(go env GOOS); case "$$os" in linux|darwin|windows) go build -tags gui -trimpath -o /build/$(PROJECT_NAME)-cli-gui ./src/client ;; esac; fi' && \
	echo "Development binaries: $$BUILD_DIR" && \
	echo "Cleanup when finished: rm -rf \"$$BUILD_DIR\""

# Build the server and CLI for the local machine into binaries/.
local: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER_RUN) casjaysdev/go:latest sh -c 'set -eu; go mod tidy; go mod download; go generate ./src/graphql; go build -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME) ./src; go build -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client; if [ "$(BUILD_GUI)" = 1 ]; then os=$$(go env GOOS); case "$$os" in linux|darwin|windows) go build -tags gui -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-gui ./src/client ;; esac; fi'

# Cross-compile server and CLI for all eight supported platforms.
build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER_RUN) casjaysdev/go:latest sh -c '\
	set -eu; \
	go mod tidy; \
	go mod download; \
	go generate ./src/graphql; \
	echo "Building local $(PROJECT_NAME) and CLI"; \
	go build -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME) ./src; \
	go build -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client; \
	for target in $$(echo "$(PLATFORMS)" | tr , " "); do \
	  os=$${target%/*}; arch=$${target#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	  echo "Building $$os/$$arch server and CLI"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-$$os-$$arch$$suffix ./src; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-$$os-$$arch$$suffix ./src/client; \
	done; \
	if [ "$(BUILD_GUI)" = 1 ]; then \
	  for target in $$(echo "$(GUI_PLATFORMS)" | tr , " "); do \
	    os=$${target%/*}; arch=$${target#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	    echo "Building optional GUI client $$os/$$arch"; \
	    GOOS=$$os GOARCH=$$arch go build -tags gui -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-gui-$$os-$$arch$$suffix ./src/client; \
	  done; \
	fi'

# Run static analysis, vulnerability scanning, and the Go test suite.
test:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@mkdir -p "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"
	TEST_SOURCE="" COVERAGE_DIR="" TEST_NETWORK="" && \
	trap 'if [ -n "$$TEST_NETWORK" ]; then docker network rm "$$TEST_NETWORK" >/dev/null 2>&1 || true; fi; if [ -n "$$TEST_SOURCE" ]; then rm -rf "$$TEST_SOURCE"; fi; if [ -n "$$COVERAGE_DIR" ]; then rm -rf "$$COVERAGE_DIR"; fi' EXIT && \
	TEST_SOURCE=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX") && \
	COVERAGE_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX") && \
	TEST_NETWORK="$(PROJECT_NAME)-$$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')" && \
	docker network create --driver bridge "$$TEST_NETWORK" >/dev/null && \
	tar --exclude=.git --exclude=binaries --exclude=releases -cf - . | tar -xf - -C "$$TEST_SOURCE" && \
	timeout -k 30 $(DOCKER_TIMEOUT) docker run --rm --no-healthcheck --network "$$TEST_NETWORK" \
	--name $(PROJECT_NAME)-$$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n') \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) --cpu-shares=$(DOCKER_CPU_SHARES) \
	-v "$$TEST_SOURCE:/app:ro" -v "$$COVERAGE_DIR:/coverage" \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod -v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false casjaysdev/go:latest \
	sh -c 'go vet ./... && govulncheck ./... && go test -v -coverprofile=/coverage/coverage.out ./... && go tool cover -func=/coverage/coverage.out && coverage=$$(go tool cover -func=/coverage/coverage.out | awk "/^total:/ {print \$$3}") && awk -v coverage="$$coverage" "BEGIN { if (coverage+0 < 60) exit 1 }"'

# Run the Docker-based server and CLI binary integration suite.
test-docker:
	@bash ./tests/run_tests.sh

# Run the browser-based application flow in isolated Docker containers.
test-e2e:
	@bash ./tests/e2e.sh

# Run service installation and lifecycle checks in Debian and Alpine Incus containers.
test-incus:
	@bash ./tests/incus.sh

# Validate the stable semantic version required for a manual release.
release-check:
	@printf '%s\n' "$(VERSION)" | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$$' >/dev/null || { echo "ERROR: make release needs a stable SemVer X.Y.Z in release.txt (got '$(VERSION)')" >&2; exit 78; }

# Publish a beta from the current, clean main commit.
beta:
	@[ "$$(git branch --show-current)" = main ] || { echo "beta: run it on main" >&2; exit 1; }
	@[ -z "$$(git status --porcelain)" ] || { echo "beta: the working tree is not clean" >&2; exit 1; }
	@TAG=$$(date -u +%Y%m%d%H%M%S)-beta; git tag "$$TAG" && git push origin "refs/tags/$$TAG" && echo "Pushed $$TAG"

# Create local release archives from the cross-platform binaries.
release: release-check build
	@mkdir -p $(RELDIR)
	@printf '%s\n' "$(VERSION)" > $(RELDIR)/version.txt
	@for file in $(BINDIR)/$(PROJECTNAME)-*; do \
		[ -f "$$file" ] || continue; \
		strip "$$file" 2>/dev/null || true; \
		cp "$$file" $(RELDIR)/; \
	done
	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' --exclude='.forgejo' --exclude='binaries' --exclude='releases' --exclude='*.tar.gz' -czf $(RELDIR)/$(PROJECTNAME)-$(VERSION)-source.tar.gz .
	@cd $(RELDIR) && sha256sum $(PROJECTNAME)-* version.txt > sha256.txt && sha512sum $(PROJECTNAME)-* version.txt > sha512.txt
	@gh release delete v$(VERSION) --yes 2>/dev/null || true
	@git tag -d v$(VERSION) 2>/dev/null || true
	@git push origin :refs/tags/v$(VERSION) 2>/dev/null || true
	@gh release create v$(VERSION) $(RELDIR)/* --title "v$(VERSION)" --notes "Release v$(VERSION)" --latest

# Build the host-platform Docker image locally; CI builds and publishes multi-platform images.
docker:
	@echo "Building Docker image $(VERSION)..."
	@docker buildx version >/dev/null 2>&1 || { echo "docker buildx required" >&2; exit 1; }
	@docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
	@docker buildx build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT_ID=$(COMMIT_ID) \
		--build-arg BUILD_EPOCH=$(BUILD_EPOCH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg OFFICIAL_SITE='$(OFFICIAL_SITE)' \
		-f docker/Dockerfile \
		-t $(REGISTRY):$(VERSION) \
		-t $(REGISTRY):latest \
		--load .
	@bash .github/scripts/verify-container-image.sh $(REGISTRY):$(VERSION)

# Remove build and release artifacts.
clean:
	@rm -rf $(BINDIR) $(RELDIR)
