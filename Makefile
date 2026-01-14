.PHONY: build test clean install lint fmt deps

# Build variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"
BINARY := lnpm
BUILD_DIR := bin

# Default target
all: build

# Build the binary
build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/lnpm

# Build for release (multiple platforms)
release:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/lnpm
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/lnpm
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/lnpm
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/lnpm
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/lnpm

# Install dependencies
deps:
	go mod download
	go mod tidy

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run linter
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed, running go vet"; \
		go vet ./...; \
	fi

# Format code
fmt:
	go fmt ./...
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w .; \
	fi

# Install binary to GOPATH/bin
install: build
	go install $(LDFLAGS) ./cmd/lnpm

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

# Run the CLI (for testing)
run: build
	./$(BINARY) $(ARGS)

# Development: watch and rebuild (requires entr)
watch:
	@if command -v entr >/dev/null 2>&1; then \
		find . -name '*.go' | entr -r make build; \
	else \
		echo "entr not installed. Install it for file watching."; \
	fi

# Show help
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  release       - Build for all platforms"
	@echo "  deps          - Download and tidy dependencies"
	@echo "  test          - Run tests"
	@echo "  test-coverage - Run tests with coverage report"
	@echo "  lint          - Run linter"
	@echo "  fmt           - Format code"
	@echo "  install       - Install to GOPATH/bin"
	@echo "  clean         - Remove build artifacts"
	@echo "  run ARGS=...  - Build and run with arguments"
	@echo "  watch         - Watch for changes and rebuild"
