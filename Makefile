.PHONY: build build-frontend test lint docker helm migrate-up migrate-down migrate-version migrate-create run dev help

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
	@echo "  migrate-up      Apply every pending migration"
	@echo "  migrate-down    Roll back the last migration (one step)"
	@echo "  migrate-version Show the recorded schema version"
	@echo "  migrate-create  Create new migration files"
	@echo "                  (rolling back everything is \`teamup migrate down-all\`,"
	@echo "                   deliberately not a make target)"
	@echo "  run             Run server locally"
	@echo "  dev             Run development servers (frontend + backend)"
	@echo "  clean           Clean build artifacts"
	@echo "  help            Show this help"

# Build frontend
#
# Dependencies are installed only when they are missing. Reinstalling on every
# build is slow, and worse, the two package managers disagree about the shape
# of node_modules: npm cannot install over a tree pnpm laid out, and pnpm wants
# to delete one npm made. Whichever put it there, either can run the build.
#
# pnpm is preferred when it is genuinely on PATH — it owns the lockfile — but it
# is usually installed through nvm, which the shell make uses does not see.
build-frontend:
	@echo "Building frontend..."
	@cd web && \
	if [ ! -d node_modules ]; then \
		if command -v pnpm >/dev/null 2>&1; then \
			echo "  installing with pnpm"; CI=true pnpm install --frozen-lockfile; \
		elif command -v npm >/dev/null 2>&1; then \
			echo "  pnpm is not on PATH, installing with npm"; npm install --no-audit --no-fund; \
		else \
			echo "Neither pnpm nor npm is on PATH. Install Node.js and try again." >&2; exit 1; \
		fi; \
	fi && \
	if command -v pnpm >/dev/null 2>&1; then pnpm run build; \
	elif command -v npm >/dev/null 2>&1; then npm run build; \
	else echo "Neither pnpm nor npm is on PATH." >&2; exit 1; fi

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

# Database-backed tests need one migrated database to clone per package.
# Point TEAMUP_TEST_DATABASE_URL at it; tests skip when it is unset.
TEST_DATABASE_URL ?= postgres://teamup:teamup@localhost:5432/teamup_test?sslmode=disable

# Create and migrate the template database the test harness clones from.
#
# The database to recreate and the maintenance connection to do it over are both
# read out of TEST_DATABASE_URL. The previous version substituted a literal
# "teamup_test", so overriding TEST_DATABASE_URL pointed the migration at one
# database while dropping another — passing a scratch URL destroyed the shared
# test database instead of leaving it alone.
test-db:
	@echo "Preparing test database..."
	@db=$$(printf '%s' "$(TEST_DATABASE_URL)" | sed -E 's|^[^?]*/([^/?]+)(\?.*)?$$|\1|'); \
	admin=$$(printf '%s' "$(TEST_DATABASE_URL)" | sed -E 's|^([^?]*/)[^/?]+|\1postgres|'); \
	psql "$$admin" -v ON_ERROR_STOP=1 \
		-c "DROP DATABASE IF EXISTS \"$$db\" WITH (FORCE);" \
		-c "CREATE DATABASE \"$$db\";"
	@TEAMUP_DATABASE_URL="$(TEST_DATABASE_URL)" $(BUILD_DIR)/$(BINARY_NAME) migrate up
	@echo "Test database ready: $(TEST_DATABASE_URL)"

# Run tests
test: test-unit test-integration

test-unit:
	@echo "Running unit tests..."
	TEAMUP_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
		go test -race -coverprofile=coverage.out -covermode=atomic ./internal/... ./pkg/...

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
docker:
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

# One step, matching the name. `teamup migrate down-all` rolls back everything;
# it is deliberately not given a make target, because the whole point of the
# separate name is that it is not reachable by habit.
migrate-down:
	@echo "Rolling back the last migration..."
	go run ./cmd/teamup migrate down

migrate-version:
	@go run ./cmd/teamup migrate version

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
