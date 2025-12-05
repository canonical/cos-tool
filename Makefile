# Add GOPATH/bin to PATH for this Makefile
export PATH := $(shell go env GOPATH)/bin:$(PATH)

.PHONY: test lint

test:
	go test ./... -coverprofile coverage.out

lint:
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8; \
	fi
	@echo "Running golangci-lint..."
	@golangci-lint run --timeout=2m --max-same-issues=10 --max-issues-per-linter=20

fmt:
	go fmt ./...


deps:
	go mod tidy
	go mod download

help:
	@echo "Available commands:"
	@echo "  test             - Run tests with verbose output"
	@echo "  lint             - Run linter (auto-installs if needed)"
	@echo "  fmt              - Format code"
	@echo "  deps             - Download and tidy dependencies"
	@echo "  help             - Show this help message"
