# Add GOPATH/bin to PATH for this Makefile
export PATH := $(shell go env GOPATH)/bin:$(PATH)

.PHONY: test lint vuln-check build

test:
	go test ./... -coverprofile coverage.out

lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	@echo "Running golangci-lint..."
	@golangci-lint run --timeout=2m --max-same-issues=10 --max-issues-per-linter=20

vuln-check:
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "Installing govulncheck..."; \
		go install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	@echo "Running vulnerability check..."
	@govulncheck ./...

build:
	@echo "Building cos-tool..."
	@go build -o bin/cos-tool ./cmd/root
	@chmod +x bin/cos-tool

build-all:
	@echo "Building cos-tool for all architectures..."
	@echo "Building for linux/amd64..."
	GOOS=linux GOARCH=amd64 go build -o bin/cos-tool-linux-amd64 ./cmd/root
	@echo "Building for linux/arm64..."
	GOOS=linux GOARCH=arm64 go build -o bin/cos-tool-linux-arm64 ./cmd/root
	@echo "Building for linux/ppc64le..."
	GOOS=linux GOARCH=ppc64le go build -o bin/cos-tool-linux-ppc64le ./cmd/root
	@echo "Building for linux/s390x..."
	GOOS=linux GOARCH=s390x go build -o bin/cos-tool-linux-s390x ./cmd/root
	@echo "All builds completed in bin/ directory"

clean:
	@echo "Cleaning build artifacts..."
	@rm -rf bin/
	@go clean -cache

fmt:
	go fmt ./...


deps:
	go mod tidy
	go mod download

help:
	@echo "Available commands:"
	@echo "  test             - Run tests with verbose output"
	@echo "  lint             - Run linter (auto-installs if needed)"
	@echo "  vuln-check       - Check for security vulnerabilities"
	@echo "  build            - Build cos-tool for current platform"
	@echo "  build-all        - Build cos-tool for all supported architectures"
	@echo "  clean            - Clean build artifacts and cache"
	@echo "  fmt              - Format code"
	@echo "  deps             - Download and tidy dependencies"
	@echo "  help             - Show this help message"
