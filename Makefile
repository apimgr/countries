# Go project build targets. Toolchain commands run in the CasjaysDev Go image.
PROJECTNAME := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)(\.git)?$$|\1|' || basename "$$(pwd)")
PROJECTORG := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)/[^/]+(\.git)?$$|\1|' || basename "$$(dirname "$$(pwd)")")
VERSION := $(shell cat release.txt 2>/dev/null || echo "0.1.0")
BUILD_EPOCH := $(shell date -u +%s)
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +%Y-%m-%dT%H:%M:%SZ)
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo N/A)
PLATFORMS ?= linux/amd64,linux/arm64
BINDIR ?= binaries
RELDIR ?= releases
REGISTRY := ghcr.io/$(PROJECTORG)/$(PROJECTNAME)

GO_CACHE ?= $(HOME)/go/pkg/mod
GO_BUILD ?= $(HOME)/.cache/go-build/$(PROJECTNAME)
DOCKER_MEM ?= 4g
DOCKER_CPUS ?= 2

LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildDate=$(BUILD_DATE) -X main.BuildEpoch=$(BUILD_EPOCH)
CLIENT_LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildDate=$(BUILD_DATE) -X main.BuildEpoch=$(BUILD_EPOCH)

GO_DOCKER := docker run --rm \
	--name $(PROJECTNAME)-$$(tr -dc 'a-z0-9' </dev/urandom | head -c8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v $(PWD):/app \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
	casjaysdev/go:latest

.PHONY: dev local build test release docker clean
.DEFAULT_GOAL := dev

# Build server and CLI for quick local development into a temporary directory.
dev:
	@mkdir -p $(GO_CACHE) $(GO_BUILD) "$${TMPDIR:-/tmp}/$(PROJECTORG)"
	@BUILD_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECTORG)/$(PROJECTNAME)-XXXXXX") && \
	docker run --rm --name $(PROJECTNAME)-$$(tr -dc 'a-z0-9' </dev/urandom | head -c8) \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v $(PWD):/app -v "$$BUILD_DIR:/build" \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod -v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false casjaysdev/go:latest \
	sh -c 'go build -trimpath -o /build/$(PROJECTNAME) ./src && go build -trimpath -o /build/$(PROJECTNAME)-cli ./src/client' && \
	echo "Development binaries: $$BUILD_DIR"

# Build the server and CLI for the local machine into binaries/.
local:
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) sh -c 'go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME) ./src && go build -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli ./src/client'

# Cross-compile server and CLI for all eight supported platforms.
build:
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) sh -c '\
	set -eu; \
	for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64 freebsd/amd64 freebsd/arm64; do \
	  os=$${target%/*}; arch=$${target#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	  echo "Building $$os/$$arch server and CLI"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-$$os-$$arch$$suffix ./src; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli-$$os-$$arch$$suffix ./src/client; \
	done; \
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME) ./src; \
	go build -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli ./src/client'

# Run static analysis, vulnerability scanning, and the Go test suite.
test:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) sh -c 'mkdir -p "$${TMPDIR:-/tmp}/$(PROJECTORG)" && coverage_dir=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECTORG)/$(PROJECTNAME)-XXXXXX") && trap "rm -rf $$coverage_dir" EXIT && go vet ./... && govulncheck ./... && go test -v -coverprofile="$$coverage_dir/coverage.out" ./... && go tool cover -func="$$coverage_dir/coverage.out" && coverage=$$(go tool cover -func="$$coverage_dir/coverage.out" | awk "/^total:/ {print \$$3}") && awk -v coverage="$$coverage" "BEGIN { if (coverage+0 < 60) exit 1 }"'

# Create local release archives from the cross-platform binaries.
release: build
	@mkdir -p $(RELDIR)
	@cp $(BINDIR)/$(PROJECTNAME)-* $(RELDIR)/
	@git archive --format=tar.gz --prefix=$(PROJECTNAME)-$(VERSION)/ HEAD -o $(RELDIR)/$(PROJECTNAME)-$(VERSION)-src.tar.gz
	@git archive --format=zip --prefix=$(PROJECTNAME)-$(VERSION)/ HEAD -o $(RELDIR)/$(PROJECTNAME)-$(VERSION)-src.zip
	@echo "Release artifacts prepared in $(RELDIR)/"

# Build and publish multi-platform Docker images.
docker:
	@docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) --build-arg COMMIT_ID=$(COMMIT_ID) --build-arg BUILD_EPOCH=$(BUILD_EPOCH) --build-arg BUILD_DATE=$(BUILD_DATE) -f docker/Dockerfile -t $(REGISTRY):$(VERSION) -t $(REGISTRY):latest --push .

# Remove build and release artifacts.
clean:
	@rm -rf $(BINDIR) $(RELDIR)
