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

# Local builds only: user VERSION override > non-empty release.txt > devel commit id.
VERSION ?= $(shell v=; [ -r release.txt ] && v=$$(tr -d '[:space:]' < release.txt); if [ -n "$$v" ]; then echo "$$v"; else git rev-parse --short=7 HEAD 2>/dev/null || echo devel; fi)
BUILD_EPOCH := $(shell date -u +%s)
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +%Y-%m-%dT%H:%M:%SZ)
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo N/A)
OFFICIAL_SITE := $(shell if [ -s site.txt ]; then cat site.txt; else printf '%s' "$(OFFICIAL_SITE)"; fi)

BINDIR ?= binaries
RELDIR ?= releases
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64,freebsd/amd64,freebsd/arm64
REGISTRY ?= ghcr.io/$(PROJECT_ORG)/$(PROJECT_NAME)
GO_CACHE ?= $(HOME)/go/pkg/mod
GO_BUILD ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)
DOCKER_MEM ?= 4g
DOCKER_CPUS ?= 2
DOCKER_CPU_SHARES ?= 256
DOCKER_TIMEOUT ?= 3600

LDFLAGS := -s -w -X 'main.Version=$(VERSION)' -X 'main.CommitID=$(COMMIT_ID)' -X 'main.BuildEpoch=$(BUILD_EPOCH)' -X 'main.OfficialSite=$(OFFICIAL_SITE)'
DEBUG_LDFLAGS := -X 'main.Version=$(VERSION)' -X 'main.CommitID=$(COMMIT_ID)' -X 'main.BuildEpoch=$(BUILD_EPOCH)' -X 'main.OfficialSite=$(OFFICIAL_SITE)'
DEBUG_GCFLAGS := all=-N -l
BUILD_OUTPUT_MOUNTS = $(if $(filter /%,$(BINDIR)),-v $(BINDIR):$(BINDIR)) $(if $(filter /%,$(RELDIR)),-v $(RELDIR):$(RELDIR))

GO_DOCKER_RUN := timeout -k 30 $(DOCKER_TIMEOUT) docker run --rm --no-healthcheck \
	--name $(PROJECT_NAME)-$$(head -c6 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) --cpu-shares=$(DOCKER_CPU_SHARES) \
	-v $(PWD):/app $(BUILD_OUTPUT_MOUNTS) \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false
GO_DOCKER := $(GO_DOCKER_RUN) casjaysdev/go:latest

.DEFAULT_GOAL := help
.PHONY: build test release docker clean help

# Build debug binaries for the local platform and all eight supported platforms.
build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER_RUN) -e BUILD_LDFLAGS="$(DEBUG_LDFLAGS)" -e BUILD_GCFLAGS="$(DEBUG_GCFLAGS)" casjaysdev/go:latest sh -c '\
	set -e; \
	go mod tidy; \
	go mod download; \
	go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME) ./src; \
	if [ -d src/client ]; then go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client; fi; \
	for platform in $$(echo "$(PLATFORMS)" | tr "," " "); do \
	  os=$${platform%/*}; arch=$${platform#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	  GOOS=$$os GOARCH=$$arch go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-$$os-$$arch$$suffix ./src; \
	  if [ -d src/client ]; then GOOS=$$os GOARCH=$$arch go build -buildvcs=false -gcflags "$$BUILD_GCFLAGS" -ldflags "$$BUILD_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-$$os-$$arch$$suffix ./src/client; fi; \
	done'
	@echo "Build complete: $(BINDIR)/"

# Run analysis, vulnerability checks, Go tests and the basic binary tier.
test:
	@mkdir -p $(GO_CACHE) $(GO_BUILD) "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"
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
	sh -c 'go mod download && go vet ./... && GOTOOLCHAIN=go1.26.4 staticcheck -checks=all ./... && govulncheck ./... && if ls tests/*.sh >/dev/null 2>&1; then shellcheck tests/*.sh && shfmt -d -i 2 -ci tests/*.sh; fi && mkdir -p /coverage/data && TMPDIR=/coverage go test -v -cover ./... -args -test.gocoverdir=/coverage/data && go tool covdata textfmt -i=/coverage/data -o=/coverage/coverage.out && go tool cover -func=/coverage/coverage.out && coverage=$$(go tool cover -func=/coverage/coverage.out | awk "/^total:/ {print \$$3}") && awk -v coverage="$$coverage" "BEGIN { if (coverage+0 < 60) exit 1 }"' && \
	echo "Running the binary checks (start, --help, --version, CLI)..." && \
	./tests/run_tests.sh

# Build stripped, production binaries and checksums without publishing a release.
release:
	@mkdir -p $(RELDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER_RUN) -e BUILD_LDFLAGS="$(LDFLAGS)" casjaysdev/go:latest sh -c '\
	set -e; go mod tidy; go mod download; \
	for platform in $$(echo "$(PLATFORMS)" | tr "," " "); do \
	  os=$${platform%/*}; arch=$${platform#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	  GOOS=$$os GOARCH=$$arch go build -buildvcs=false -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(RELDIR)/$(PROJECT_NAME)-$$os-$$arch$$suffix ./src; \
	  if [ -d src/client ]; then GOOS=$$os GOARCH=$$arch go build -buildvcs=false -trimpath -ldflags "$$BUILD_LDFLAGS" -o $(RELDIR)/$(PROJECT_NAME)-cli-$$os-$$arch$$suffix ./src/client; fi; \
	done'
	@printf '%s\n' "$(VERSION)" > $(RELDIR)/version.txt
	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' --exclude='.forgejo' --exclude='binaries' --exclude='releases' --exclude='*.tar.gz' -czf $(RELDIR)/$(PROJECT_NAME)-$(VERSION)-source.tar.gz .
	@cd $(RELDIR) && sha256sum $(PROJECT_NAME)-* version.txt > sha256.txt && sha512sum $(PROJECT_NAME)-* version.txt > sha512.txt
	@echo "Release build complete: $(RELDIR)/"

# Build and verify one host-platform image locally; publishing is CI/CD's job.
docker:
	@echo "Building Docker image $(VERSION)..."
	@docker buildx version >/dev/null 2>&1 || { echo "docker buildx required" >&2; exit 1; }
	@docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
	@docker buildx build -f docker/Dockerfile --load \
		--build-arg VERSION="$(VERSION)" --build-arg BUILD_DATE="$(BUILD_DATE)" \
		--build-arg BUILD_EPOCH="$(BUILD_EPOCH)" --build-arg COMMIT_ID="$(COMMIT_ID)" \
		--build-arg OFFICIAL_SITE="$(OFFICIAL_SITE)" \
		-t $(REGISTRY):$(VERSION) -t $(REGISTRY):latest .
	@bash .github/scripts/verify-container-image.sh $(REGISTRY):$(VERSION)

# Remove local build and release artifacts.
clean:
	@rm -rf -- $(BINDIR) $(RELDIR)

# List the only supported Makefile targets.
help:
	@echo "Targets:"
	@echo "  build    Debug binaries for all platforms to $(BINDIR)/"
	@echo "  test     Lint, vulnerability scan, unit tests, then the basic binary checks"
	@echo "  release  Production binaries, source archive and checksums to $(RELDIR)/"
	@echo "  docker   Container image for the host platform (no push)"
	@echo "  clean    Remove $(BINDIR)/ and $(RELDIR)/"
	@echo "  help     This list"
