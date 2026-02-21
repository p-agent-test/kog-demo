.PHONY: build test lint run clean

BINARY=platform-agent
BUILD_DIR=bin

build:
	@echo "Building $(BINARY)..."
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/agent/

test:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	go vet ./...
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

run:
	@test -f .env && export $$(grep -v '^#' .env | xargs) && go run ./cmd/agent/ || echo "Create .env from .env.example first"

clean:
	rm -rf $(BUILD_DIR) coverage.out

docker-build:
	docker build -t platform-agent:latest .

fmt:
	gofmt -w .
	goimports -w . 2>/dev/null || true
