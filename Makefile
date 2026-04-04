# atlax - Build System
# Custom reverse TLS tunnel with TCP stream multiplexing

# Version information
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE       ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# Go settings
GO         := go
GOFLAGS    ?=
LDFLAGS    := -s -w \
	-X github.com/atlasshare/atlax/pkg/config.Version=$(VERSION) \
	-X github.com/atlasshare/atlax/pkg/config.Commit=$(COMMIT) \
	-X github.com/atlasshare/atlax/pkg/config.Date=$(DATE)

# Output
BIN_DIR    := bin
RELAY_BIN  := $(BIN_DIR)/atlax-relay
AGENT_BIN  := $(BIN_DIR)/atlax-agent

# Tool versions
GOLANGCI_LINT_VERSION ?= v1.62.2

# Platforms
GOOS       ?= $(shell $(GO) env GOOS)
GOARCH     ?= $(shell $(GO) env GOARCH)

.PHONY: all build build-relay build-agent test lint fmt vet docker-build certs-dev clean install coverage help

## all: Build both binaries (default target)
all: build

## build: Build both relay and agent binaries to bin/
build: build-relay build-agent

## build-relay: Build the relay binary
build-relay:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(RELAY_BIN) ./cmd/relay/

## build-agent: Build the agent binary
build-agent:
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(AGENT_BIN) ./cmd/agent/

## test: Run tests with race detector and coverage
test:
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...

## lint: Run golangci-lint
lint:
	golangci-lint run --config .golangci.yml ./...

## fmt: Format Go source files
fmt:
	gofmt -w .
	goimports -w .

## vet: Run go vet and staticcheck
vet:
	$(GO) vet ./...
	staticcheck ./...

## docker-build: Build Docker images for relay and agent
docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-f deployments/docker/Dockerfile.relay \
		-t atlax-relay:$(VERSION) .
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		-f deployments/docker/Dockerfile.agent \
		-t atlax-agent:$(VERSION) .

## certs-dev: Generate development certificates
certs-dev:
	@bash scripts/gen-certs.sh

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR)
	rm -f coverage.out coverage.html
	rm -rf dist/

## install: Install binaries to GOPATH/bin
install:
	$(GO) install $(GOFLAGS) -ldflags '$(LDFLAGS)' ./cmd/relay/
	$(GO) install $(GOFLAGS) -ldflags '$(LDFLAGS)' ./cmd/agent/

## coverage: Generate and display coverage report
coverage: test
	$(GO) tool cover -html=coverage.out -o coverage.html
	$(GO) tool cover -func=coverage.out

## help: Show this help message
help:
	@echo "atlax - Custom reverse TLS tunnel with TCP stream multiplexing"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
