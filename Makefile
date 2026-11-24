# Go project build targets. Toolchain commands run in the CasjaysDev Go image.
PROJECTNAME := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)(\.git)?$$|\1|' || basename "$$(pwd)")
PROJECTORG := $(shell git remote get-url origin 2>/dev/null | sed -E 's|.*/([^/]+)/[^/]+(\.git)?$$|\1|' || basename "$$(dirname "$$(pwd)")")
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
GO_BUILD ?= $(HOME)/.cache/go-build/$(PROJECTNAME)
DOCKER_MEM ?= 4g
DOCKER_CPUS ?= 2
BUILD_OUTPUT_MOUNT = $(if $(filter /%,$(BINDIR)),-v $(BINDIR):$(BINDIR))

LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)
CLIENT_LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.CommitID=$(COMMIT_ID) -X main.BuildEpoch=$(BUILD_EPOCH) -X main.OfficialSite=$(OFFICIAL_SITE)

GO_DOCKER := docker run --rm \
	--name $(PROJECTNAME)-$$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n') \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v $(PWD):/app \
	$(BUILD_OUTPUT_MOUNT) \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod \
	-v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false \
	casjaysdev/go:latest

.PHONY: dev local build test license-check release docker clean
.DEFAULT_GOAL := dev

# Build server and CLI for quick local development into a temporary directory.
dev:
	@mkdir -p $(GO_CACHE) $(GO_BUILD) "$${TMPDIR:-/tmp}/$(PROJECTORG)"
	@$(GO_DOCKER) go mod tidy
	@BUILD_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECTORG)/$(PROJECTNAME)-XXXXXX") && \
	docker run --rm --name $(PROJECTNAME)-$$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n') \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v $(PWD):/app -v "$$BUILD_DIR:/build" \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod -v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false casjaysdev/go:latest \
	sh -c 'set -eu; go generate ./src/graphql; go build -trimpath -o /build/$(PROJECTNAME) ./src; go build -trimpath -o /build/$(PROJECTNAME)-cli ./src/client; if [ "$(BUILD_GUI)" = 1 ]; then os=$$(go env GOOS); case "$$os" in linux|darwin|windows) go build -tags gui -trimpath -o /build/$(PROJECTNAME)-cli-gui ./src/client ;; esac; fi' && \
	echo "Development binaries: $$BUILD_DIR"

# Build the server and CLI for the local machine into binaries/.
local: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) go mod tidy
	@$(GO_DOCKER) go mod download
	@$(GO_DOCKER) sh -c 'set -eu; go generate ./src/graphql; go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME) ./src; go build -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli ./src/client; if [ "$(BUILD_GUI)" = 1 ]; then os=$$(go env GOOS); case "$$os" in linux|darwin|windows) go build -tags gui -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli-gui ./src/client ;; esac; fi'

# Cross-compile server and CLI for all eight supported platforms.
build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) go mod tidy
	@$(GO_DOCKER) go mod download
	@$(GO_DOCKER) sh -c '\
	set -eu; \
	go generate ./src/graphql; \
	for target in $$(echo "$(PLATFORMS)" | tr , " "); do \
	  os=$${target%/*}; arch=$${target#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	  echo "Building $$os/$$arch server and CLI"; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-$$os-$$arch$$suffix ./src; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli-$$os-$$arch$$suffix ./src/client; \
	done; \
	if [ "$(BUILD_GUI)" = 1 ]; then \
	  for target in $$(echo "$(GUI_PLATFORMS)" | tr , " "); do \
	    os=$${target%/*}; arch=$${target#*/}; suffix=""; [ "$$os" != windows ] || suffix=.exe; \
	    echo "Building optional GUI client $$os/$$arch"; \
	    GOOS=$$os GOARCH=$$arch go build -tags gui -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli-gui-$$os-$$arch$$suffix ./src/client; \
	  done; \
	fi; \
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME) ./src; \
	go build -trimpath -ldflags "$(CLIENT_LDFLAGS)" -o $(BINDIR)/$(PROJECTNAME)-cli ./src/client'

# Run static analysis, vulnerability scanning, and the Go test suite.
test:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@mkdir -p "$${TMPDIR:-/tmp}/$(PROJECTORG)"
	@TEST_SOURCE=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECTORG)/$(PROJECTNAME)-XXXXXX") && \
	COVERAGE_DIR=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECTORG)/$(PROJECTNAME)-XXXXXX") && \
	TEST_NETWORK="$(PROJECTNAME)-test-net" && \
	docker network create --driver bridge "$$TEST_NETWORK" >/dev/null && \
	trap 'docker network rm "$$TEST_NETWORK" >/dev/null 2>&1 || true; rm -rf "$$TEST_SOURCE" "$$COVERAGE_DIR"' EXIT && \
	tar --exclude=.git --exclude=binaries --exclude=releases -cf - . | tar -xf - -C "$$TEST_SOURCE" && \
	docker run --rm --name $(PROJECTNAME)-test-$$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n') \
	--network "$$TEST_NETWORK" \
	--memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) \
	-v "$$TEST_SOURCE:/app:ro" -v "$$COVERAGE_DIR:/coverage" \
	-v $(GO_CACHE):/usr/local/share/go/pkg/mod -v $(GO_BUILD):/usr/local/share/go/cache \
	-w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false casjaysdev/go:latest \
	sh -c 'go vet ./... && govulncheck ./... && go test -v -coverprofile=/coverage/coverage.out ./... && go tool cover -func=/coverage/coverage.out && coverage=$$(go tool cover -func=/coverage/coverage.out | awk "/^total:/ {print \$$3}") && awk -v coverage="$$coverage" "BEGIN { if (coverage+0 < 60) exit 1 }"'

# Check dependency licenses and save the current third-party license report.
license-check:
	@mkdir -p $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER) bash ./scripts/verify-licenses.sh

# Create local release archives from the cross-platform binaries.
release: build
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

# Build and publish multi-platform Docker images.
docker:
	@docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT_ID=$(COMMIT_ID) \
		--build-arg BUILD_EPOCH=$(BUILD_EPOCH) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg OFFICIAL_SITE='$(OFFICIAL_SITE)' \
		--label maintainer=apimgr \
		--label org.opencontainers.image.vendor=$(PROJECTORG) \
		--label org.opencontainers.image.authors=$(PROJECTORG) \
		--label org.opencontainers.image.title=$(PROJECTNAME) \
		--label org.opencontainers.image.base.name=$(PROJECTNAME) \
		--label 'org.opencontainers.image.description=$(PROJECTNAME) - Countries API' \
		--label org.opencontainers.image.licenses=MIT \
		--label org.opencontainers.image.created=$(BUILD_DATE) \
		--label org.opencontainers.image.version=$(VERSION) \
		--label org.opencontainers.image.schema-version=$(VERSION) \
		--label org.opencontainers.image.revision=$(COMMIT_ID) \
		--label org.opencontainers.image.url=https://github.com/$(PROJECTORG)/$(PROJECTNAME) \
		--label org.opencontainers.image.source=https://github.com/$(PROJECTORG)/$(PROJECTNAME) \
		--label org.opencontainers.image.documentation=https://github.com/$(PROJECTORG)/$(PROJECTNAME) \
		--label org.opencontainers.image.vcs-type=Git \
		--label com.github.containers.toolbox=false \
		--annotation maintainer=apimgr \
		--annotation org.opencontainers.image.vendor=$(PROJECTORG) \
		--annotation org.opencontainers.image.authors=$(PROJECTORG) \
		--annotation org.opencontainers.image.title=$(PROJECTNAME) \
		--annotation org.opencontainers.image.base.name=$(PROJECTNAME) \
		--annotation 'org.opencontainers.image.description=$(PROJECTNAME) - Countries API' \
		--annotation org.opencontainers.image.licenses=MIT \
		--annotation org.opencontainers.image.created=$(BUILD_DATE) \
		--annotation org.opencontainers.image.version=$(VERSION) \
		--annotation org.opencontainers.image.schema-version=$(VERSION) \
		--annotation org.opencontainers.image.revision=$(COMMIT_ID) \
		--annotation org.opencontainers.image.url=https://github.com/$(PROJECTORG)/$(PROJECTNAME) \
		--annotation org.opencontainers.image.source=https://github.com/$(PROJECTORG)/$(PROJECTNAME) \
		--annotation org.opencontainers.image.documentation=https://github.com/$(PROJECTORG)/$(PROJECTNAME) \
		--annotation org.opencontainers.image.vcs-type=Git \
		--annotation com.github.containers.toolbox=false \
		-f docker/Dockerfile -t $(REGISTRY):$(VERSION) -t $(REGISTRY):latest --push .

# Remove build and release artifacts.
clean:
	@rm -rf $(BINDIR) $(RELDIR)
