# Build variables
BINARY_NAME=ds-s3
BINARY := bin/$(BINARY_NAME)
VERSION?=dev
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE?=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Build directories
BUILD_DIR=bin
CMD_DIR=cmd/s3

# Go commands
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet

.PHONY: build build-all clean test tidy fmt

# Target platforms
PLATFORMS=linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

build:
	mkdir -p bin
	$(GOBUILD) $(LDFLAGS) -o $(BINARY) ./cmd/s3

release-prepare: ## Prepare for release
	@echo "Preparing release $(VERSION)"
	@mkdir -p $(BUILD_DIR)

release-build-all: release-prepare ## Build multi-platform binaries for release
	@echo "Building release artifacts for $(VERSION)"
	# Iterate through PLATFORMS to build per-platform artifacts into dedicated directories
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*}; \
		GOARCH=$${platform#*/}; \
		OUTPUT_DIR="$(BUILD_DIR)/$$platform"; \
		mkdir -p "$$OUTPUT_DIR"; \
		OUTPUT_FILE="$$OUTPUT_DIR/$(BINARY_NAME)"; \
		if [ "$$GOOS" = "windows" ]; then \
			OUTPUT_FILE="$$OUTPUT_FILE.exe"; \
		fi; \
		echo "  > $$platform"; \
		GOOS=$$GOOS GOARCH=$$GOARCH $(GOBUILD) $(LDFLAGS) -o "$$OUTPUT_FILE" ./$(CMD_DIR); \
	done
	@echo "✓ All platform binaries built successfully"

clean:
	rm -rf bin

fmt:
	$(GOFMT) ./...

lint:
	$(GOVET) ./...

test:
	$(GOTEST) ./...

tidy:
	$(GOMOD) tidy
