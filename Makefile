.PHONY: build build-frontend test lint docker helm migrate-up migrate-down migrate-create run dev help

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

BINARY_NAME := teamup
BUILD_DIR := bin

# Default target
help:
	@echo "TeamUp - Self-hosted Collaboration Platform"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  build           Build Go binary (embeds frontend)"
	@echo "  build-frontend  Build React frontend"
	@echo "  test            Run all tests"
	@echo "  test-unit       Run unit tests only"
	@echo "  test-integration Run integration tests"
	@echo "  test-e2e        Run E2E tests"
	@echo "  lint            Run linters (golangci-lint, eslint)"
	@echo "  docker          Build Docker image"
	@echo "  docker-push     Build and push Docker image to GHCR"
	@echo "  helm            Package Helm chart"
	@echo "  migrate-up      Run database migrations up"
	@echo "  migrate-down    Rollback last migration"
	@echo "  migrate-create  Create new migration files"
	@echo "  run             Run server locally"
	@echo "  dev             Run development servers (frontend + backend)"
	@echo "  clean           Clean build artifacts"
	@echo "  help            Show this help"

# Build frontend
build-frontend:
	@echo "Building frontend..."
	cd web && pnpm install --frozen-lockfile && pnpm run build

# Build Go binary with embedded frontend
build: build-frontend
	@echo "Building $(BINARY_NAME) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/teamup

# Build for multiple architectures
build-all: build-frontend
	@echo "Building multi-arch binaries..."
	@mkdir -p $(BUILD_DIR)
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch \
		go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-linux-$$arch ./cmd/teamup; \
	done

# Run tests
test: test-unit test-integration

test-unit:
	@echo "Running unit tests..."
	go test -v -race -coverprofile=coverage.out -covermode=atomic ./internal/... ./pkg/...

test-integration:
	@echo "Running integration tests..."
	go test -v -tags=integration ./internal/...

test-e2e:
	@echo "Running E2E tests..."
	cd web && pnpm playwright install --with-deps && pnpm test:e2e

# Linting
lint:
	@echo "Running Go linter..."
	golangci-lint run ./...
	@echo "Running frontend linter..."
	cd web && pnpm lint

# Docker
docker: build-all
	@echo "Building Docker image..."
	docker build -t teamup:$(VERSION) -t teamup:latest .

docker-push: docker
	@echo "Pushing to GHCR..."
	docker push ghcr.io/$(GITHUB_REPOSITORY):$(VERSION)
	docker push ghcr.io/$(GITHUB_REPOSITORY):latest

# Helm
helm:
	@echo "Packaging Helm chart..."
	helm package helm/teamup -d $(BUILD_DIR)/helm

# Migrations
migrate-up:
	@echo "Running migrations up..."
	go run ./cmd/teamup migrate up

migrate-down:
	@echo "Rolling back last migration..."
	go run ./cmd/teamup migrate down

migrate-create:
	@read -p "Migration name: " name; \
	go run ./cmd/teamup migrate create $$name

# Run server
run: build
	@echo "Starting server..."
	./bin/$(BINARY_NAME) server

# Development
dev:
	@echo "Starting development servers..."
	@echo "Frontend: http://localhost:3000"
	@echo "Backend:  http://localhost:8080"
	@echo ""
	@echo "Run in separate terminals:"
	@echo "  Terminal 1: cd web && pnpm dev"
	@echo "  Terminal 2: make run-dev"

run-dev:
	@go run ./cmd/teamup server

# Clean
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	rm -f coverage.out
	cd web && rm -rf dist node_modules/.cache

# Generate code (if using sqlc, etc.)
generate:
	@echo "Generating code..."
	go generate ./...

# Install tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
