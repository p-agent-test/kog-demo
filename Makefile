.PHONY: build test lint run dev clean docker-build fmt

BINARY=platform-agent
BUILD_DIR=bin
ENV_FILE=.env

# Load .env if exists
ifneq (,$(wildcard $(ENV_FILE)))
  include $(ENV_FILE)
  export
endif

## build: Compile the binary
build:
	@echo "Building $(BINARY)..."
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/agent/

## run: Build and run with .env
run: build
	./$(BUILD_DIR)/$(BINARY)

## dev: Run with go run (auto-reload friendly)
dev:
	go run ./cmd/agent/

## test: Run all tests with race detector
test:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

## test-short: Quick test without verbose
test-short:
	go test -race ./...

## lint: Vet + golangci-lint
lint:
	go vet ./...
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

## fmt: Format code
fmt:
	gofmt -w .
	goimports -w . 2>/dev/null || true

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR) coverage.out

## docker-build: Build Docker image
docker-build:
	docker build -t platform-agent:latest .

## help: Show this help
help:
	@grep -E '^## ' Makefile | sed 's/## //' | column -t -s ':'
