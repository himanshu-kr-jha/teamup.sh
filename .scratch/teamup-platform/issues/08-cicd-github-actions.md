## Question

Design GitHub Actions CI/CD pipeline: build Go binary (multi-arch: linux/amd64, linux/arm64), build React frontend (Vite), embed frontend in binary (or serve separately), build Docker image (multi-stage, distroless/base), push to GHCR, generate Helm chart (values.yaml for config), create GitHub Release with artifacts, and versioning (semver from tags). Include test stages (unit, integration, e2e).

Status: claimed

## Answer

**Decision: CI/CD pipeline architecture defined below.**

### 1. Repository Structure for CI/CD

```
teamup/
├── .github/
│   ├── workflows/
│   │   ├── ci.yml           # PR checks (lint, test, build)
│   │   ├── release.yml      # Tag push → release artifacts
│   │   ├── docker.yml       # Daily/weekly Docker builds
│   │   └── helm.yml         # Helm chart publishing
│   └── dependabot.yml
├── cmd/teamup/              # Main binary
├── web/                     # React frontend (Vite)
├── helm/teamup/             # Helm chart
├── Dockerfile
├── docker-compose.yml
├── go.mod / go.sum
├── package.json / pnpm-lock.yaml
├── Makefile
└── .golangci.yml
```

---

### 2. CI Workflow (`.github/workflows/ci.yml`)

```yaml
name: CI

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main, develop]

env:
  GO_VERSION: '1.22'
  NODE_VERSION: '20'
  REGISTRY: ghcr.io
  IMAGE_NAME: ${{ github.repository }}

jobs:
  # ──────────────────────────────────────────────
  # Lint & Type Check
  # ──────────────────────────────────────────────
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with: { go-version: ${{ env.GO_VERSION }} }

      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with: { version: latest }

      - name: Setup Node
        uses: actions/setup-node@v4
        with: { node-version: ${{ env.NODE_VERSION }}, cache: 'pnpm' }

      - run: pnpm install --frozen-lockfile
      - run: pnpm lint          # eslint + prettier --check
      - run: pnpm typecheck     # tsc --noEmit

  # ──────────────────────────────────────────────
  # Unit Tests (Go + Frontend)
  # ──────────────────────────────────────────────
  unit-test:
    name: Unit Tests
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env: { POSTGRES_PASSWORD: test, POSTGRES_DB: teamup_test }
        ports: [5432:5432]
        options: >-
          --health-cmd="pg_isready -U postgres"
          --health-interval=10s --health-timeout=5s --health-retries=5
      redis:
        image: redis:7-alpine
        ports: [6379:6379]
        options: --health-cmd="redis-cli ping" --health-interval=10s --health-timeout=5s --health-retries=5
      minio:
        image: minio/minio:latest
        command: server /data --console-address ":9001"
        env: { MINIO_ROOT_USER: test, MINIO_ROOT_PASSWORD: testtest }
        ports: [9000:9000]
        options: --health-cmd="mc ready local" --health-interval=10s --health-timeout=5s --health-retries=5

    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with: { go-version: ${{ env.GO_VERSION }} }

      - name: Run Go tests
        run: |
          go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
        env:
          TEST_DATABASE_URL: postgres://postgres:test@localhost:5432/teamup_test?sslmode=disable
          TEST_REDIS_URL: redis://localhost:6379
          TEST_MINIO_ENDPOINT: localhost:9000

      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with: { files: coverage.out, flags: unittests }

      - name: Setup Node
        uses: actions/setup-node@v4
        with: { node-version: ${{ env.NODE_VERSION }}, cache: 'pnpm' }

      - run: pnpm install --frozen-lockfile
      - run: pnpm test -- --coverage
      - name: Upload frontend coverage
        uses: codecov/codecov-action@v4
        with: { files: coverage/lcov.info, flags: frontend }

  # ──────────────────────────────────────────────
  # Integration Tests (require LiveKit, full stack)
  # ──────────────────────────────────────────────
  integration-test:
    name: Integration Tests
    runs-on: ubuntu-latest
    needs: lint
    services:
      postgres: { ... same as above ... }
      redis: { ... }
      minio: { ... }
      livekit:
        image: livekit/livekit-server:latest
        env:
          LIVEKIT_CONFIG: |
            port: 7880
            keys: { "test-key": "test-secret" }
        ports: [7880:7880, 7881:7881, 7882:7882]

    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: ${{ env.GO_VERSION }} }

      - name: Build binary
        run: make build

      - name: Start services
        run: |
          docker compose -f docker-compose.test.yml up -d
          sleep 10 # wait for healthchecks

      - name: Run integration tests
        run: go test -v -tags=integration ./internal/...
        env:
          LIVEKIT_URL: http://localhost:7880
          LIVEKIT_API_KEY: test-key
          LIVEKIT_API_SECRET: test-secret

      - name: Upload integration test results
        if: always()
        uses: actions/upload-artifact@v4
        with: { name: integration-results, path: test-results/ }

  # ──────────────────────────────────────────────
  # Build (multi-arch Go binary + frontend embed)
  # ──────────────────────────────────────────────
  build:
    name: Build (${{ matrix.os }}/${{ matrix.arch }})
    runs-on: ubuntu-latest
    needs: [lint, unit-test]
    strategy:
      matrix:
        include:
          - { os: linux, arch: amd64, goos: linux, goarch: amd64 }
          - { os: linux, arch: arm64, goos: linux, goarch: arm64 }
    steps:
      - uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with: { go-version: ${{ env.GO_VERSION }} }

      - name: Setup Node
        uses: actions/setup-node@v4
        with: { node-version: ${{ env.NODE_VERSION }}, cache: 'pnpm' }

      - name: Build frontend
        working-directory: ./web
        run: |
          pnpm install --frozen-lockfile
          pnpm run build
          # Output to ../cmd/teamup/web/dist (for go:embed)

      - name: Build Go binary
        run: |
          CGO_ENABLED=0 GOOS=${{ matrix.goos }} GOARCH=${{ matrix.goarch }} \
          go build -ldflags="-s -w -X main.version=${{ github.sha }} -X main.commit=${{ github.sha }} -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
          -o teamup-${{ matrix.os }}-${{ matrix.arch }} ./cmd/teamup

      - name: Test binary
        run: |
          ./teamup-${{ matrix.os }}-${{ matrix.arch }} version
          ./teamup-${{ matrix.os }}-${{ matrix.arch }} migrate --help

      - name: Upload artifacts
        uses: actions/upload-artifact@v4
        with:
          name: teamup-${{ matrix.os }}-${{ matrix.arch }}
          path: teamup-${{ matrix.os }}-${{ matrix.arch }}
          retention-days: 7

  # ──────────────────────────────────────────────
  # E2E Tests (Playwright, against built binary)
  # ──────────────────────────────────────────────
  e2e-test:
    name: E2E Tests
    runs-on: ubuntu-latest
    needs: build
    steps:
      - uses: actions/checkout@v4
      - uses: actions/download-artifact@v4
        with: { name: teamup-linux-amd64, path: ./bin }
      - run: chmod +x ./bin/teamup-linux-amd64

      - name: Start full stack
        run: |
          docker compose -f docker-compose.e2e.yml up -d
          ./bin/teamup-linux-amd64 migrate up
          ./bin/teamup-linux-amd64 server &
          sleep 15

      - name: Setup Node
        uses: actions/setup-node@v4
        with: { node-version: ${{ env.NODE_VERSION }}, cache: 'pnpm' }
      - run: pnpm install --frozen-lockfile

      - name: Run Playwright tests
        working-directory: ./web
        run: pnpm playwright install --with-deps && pnpm test:e2e
        env:
          PLAYWRIGHT_BASE_URL: http://localhost:8080

      - name: Upload E2E artifacts
        if: always()
        uses: actions/upload-artifact@v4
        with: { name: e2e-results, path: web/test-results/ }

  # ──────────────────────────────────────────────
  # Security Scan
  # ──────────────────────────────────────────────
  security:
    name: Security Scan
    runs-on: ubuntu-latest
    needs: lint
    steps:
      - uses: actions/checkout@v4
      - name: Run Trivy (Go vuln scan)
        uses: aquasecurity/trivy-action@master
        with: { scan-type: 'fs', scan-ref: '.', format: 'sarif', output: 'trivy.sarif' }
      - name: Upload Trivy results
        uses: github/codeql-action/upload-sarif@v3
        with: { sarif_file: trivy.sarif }
      - name: Gosec
        run: |
          go install github.com/securego/gosec/v2/cmd/gosec@latest
          gosec ./...
```

---

### 3. Release Workflow (`.github/workflows/release.yml`)

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'  # v1.0.0, v1.0.0-rc.1, etc.

permissions:
  contents: write
  packages: write
  id-token: write

jobs:
  # ──────────────────────────────────────────────
  # Build Release Artifacts
  # ──────────────────────────────────────────────
  build-release:
    name: Build Release Artifacts
    runs-on: ubuntu-latest
    outputs:
      version: ${{ steps.version.outputs.version }}
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 } # For git describe

      - name: Setup Go
        uses: actions/setup-go@v5
        with: { go-version: '1.22' }

      - name: Setup Node
        uses: actions/setup-node@v4
        with: { node-version: '20', cache: 'pnpm' }

      - name: Determine version
        id: version
        run: |
          VERSION=${GITHUB_REF#refs/tags/v}
          echo "version=$VERSION" >> $GITHUB_OUTPUT
          echo "VERSION=$VERSION" >> $GITHUB_ENV

      - name: Build frontend
        working-directory: ./web
        run: |
          pnpm install --frozen-lockfile
          pnpm run build

      - name: Build multi-arch binaries
        run: |
          for arch in amd64 arm64; do
            CGO_ENABLED=0 GOOS=linux GOARCH=$arch \
            go build -ldflags="-s -w -X main.version=$VERSION -X main.commit=$(git rev-parse HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
            -o dist/teamup-linux-$arch ./cmd/teamup
          done

      - name: Create checksums
        run: |
          cd dist
          sha256sum teamup-linux-* > checksums.txt
          cat checksums.txt

      - name: Upload release artifacts
        uses: actions/upload-artifact@v4
        with:
          name: release-artifacts
          path: dist/
          retention-days: 30

  # ──────────────────────────────────────────────
  # Docker Multi-arch Build & Push to GHCR
  # ──────────────────────────────────────────────
  docker:
    name: Docker Build & Push
    needs: build-release
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up QEMU
        uses: docker/setup-qemu-action@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Login to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Download release artifacts
        uses: actions/download-artifact@v4
        with: { name: release-artifacts, path: dist }

      - name: Build and push
        uses: docker/build-push-action@v5
        with:
          context: .
          push: true
          platforms: linux/amd64,linux/arm64
          tags: |
            ghcr.io/${{ github.repository }}:${{ needs.build-release.outputs.version }}
            ghcr.io/${{ github.repository }}:latest
          labels: |
            org.opencontainers.image.version=${{ needs.build-release.outputs.version }}
            org.opencontainers.image.source=${{ github.server_url }}/${{ github.repository }}
          cache-from: type=gha
          cache-to: type=gha,mode=max

  # ──────────────────────────────────────────────
  # Helm Chart Package & Publish
  # ──────────────────────────────────────────────
  helm:
    name: Helm Chart
    needs: build-release
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Setup Helm
        uses: azure/setup-helm@v4
        with: { version: '3.14' }

      - name: Update chart version
        run: |
          cd helm/teamup
          sed -i "s/^version: .*/version: ${{ needs.build-release.outputs.version }}/" Chart.yaml
          sed -i "s/^appVersion: .*/appVersion: ${{ needs.build-release.outputs.version }}/" Chart.yaml

      - name: Lint chart
        run: helm lint helm/teamup

      - name: Package chart
        run: helm package helm/teamup -d dist/helm

      - name: Upload Helm chart
        uses: actions/upload-artifact@v4
        with: { name: helm-chart, path: dist/helm/, retention-days: 30 }

      # Optional: Push to GHCR as OCI chart
      - name: Push to GHCR (OCI)
        if: env.HELM_REGISTRY == '1'
        run: |
          helm registry login ghcr.io -u ${{ github.actor }} -p ${{ secrets.GITHUB_TOKEN }}
          helm push dist/teamup-${{ needs.build-release.outputs.version }}.tgz oci://ghcr.io/${{ github.repository }}/charts

  # ──────────────────────────────────────────────
  # Create GitHub Release
  # ──────────────────────────────────────────────
  github-release:
    name: GitHub Release
    needs: [build-release, docker, helm]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with: { name: release-artifacts, path: dist }
      - uses: actions/download-artifact@v4
        with: { name: helm-chart, path: dist/helm }

      - name: Generate changelog
        id: changelog
        uses: triangleroot/changelog-action@v2
        with:
          token: ${{ secrets.GITHUB_TOKEN }}
          tag: ${{ github.ref_name }}

      - name: Create release
        uses: softprops/action-gh-release@v2
        with:
          tag_name: ${{ github.ref_name }}
          name: TeamUp ${{ github.ref_name }}
          body: ${{ steps.changelog.outputs.changelog }}
          files: |
            dist/teamup-linux-amd64
            dist/teamup-linux-arm64
            dist/checksums.txt
            dist/helm/teamup-${{ needs.build-release.outputs.version }}.tgz
          draft: false
          prerelease: ${{ contains(github.ref_name, '-rc') || contains(github.ref_name, '-beta') || contains(github.ref_name, '-alpha') }}
          generate_release_notes: false
```

---

### 4. Dockerfile (Multi-stage, Distroless)

```dockerfile
# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
RUN apk add --no-cache git make gcc musl-dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make build-frontend  # builds web/ → cmd/teamup/web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /teamup ./cmd/teamup

# Runtime stage (distroless - minimal, secure)
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /teamup /teamup
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/teamup"]
```

**Alternative (with glibc for CGO deps):**
```dockerfile
FROM gcr.io/distroless/cc-debian12:nonroot
```

---

### 5. Helm Chart Structure (`helm/teamup/`)

```
helm/teamup/
├── Chart.yaml
├── values.yaml
├── values.schema.json
├── templates/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── ingress.yaml
│   ├── configmap.yaml
│   ├── secret.yaml
│   ├── servicemonitor.yaml    # Prometheus
│   ├── pvc.yaml               # MinIO, LiveKit data
│   └── _helpers.tpl
└── charts/
    ├── postgresql-12.x.tgz    # Dependency
    ├── redis-18.x.tgz
    ├── minio-12.x.tgz
    └── livekit-0.x.tgz
```

**values.yaml (key sections):**
```yaml
image:
  repository: ghcr.io/yourorg/teamup
  tag: "1.0.0"
  pullPolicy: IfNotPresent

replicaCount: 1

resources:
  limits: { cpu: 1000m, memory: 1Gi }
  requests: { cpu: 250m, memory: 512Mi }

config:
  env: production
  logLevel: info
  port: 8080
  database:
    host: postgresql
    port: 5432
    name: teamup
  redis:
    host: redis-master
    port: 6379
  minio:
    endpoint: minio:9000
    accessKey: minioadmin
    secretKey: minioadmin
    bucket: teamup-files
  livekit:
    url: http://livekit:7880
    apiKey: changeme
    apiSecret: changeme

ingress:
  enabled: true
  className: nginx
  host: teamup.example.com
  tls: true

postgresql:
  enabled: true
  auth: { postgresPassword: changeme, database: teamup }

redis:
  enabled: true
  architecture: standalone

minio:
  enabled: true
  mode: standalone
  rootUser: minioadmin
  rootPassword: minioadmin
  buckets: [{ name: teamup-files, policy: none }]

livekit:
  enabled: true
  replicaCount: 1
  config:
    keys: { "changeme": "changeme" }
```

---

### 6. Versioning Strategy

| Tag Format | Version | Release Type |
|------------|---------|--------------|
| `v1.0.0` | 1.0.0 | Stable |
| `v1.1.0-rc.1` | 1.1.0-rc.1 | Release Candidate |
| `v1.1.0-beta.2` | 1.1.0-beta.2 | Beta |
| `v1.1.0-alpha.1` | 1.1.0-alpha.1 | Alpha |

**SemVer enforcement:** `git tag -a v1.0.0 -m "Release v1.0.0"` → triggers release workflow.

**Changelog:** Auto-generated from conventional commits (`feat:`, `fix:`, `BREAKING CHANGE:`).

---

### 7. Makefile Targets

```makefile
.PHONY: build build-frontend test lint docker helm release

VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT := $(shell git rev-parse --short HEAD)
DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build: build-frontend
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/teamup ./cmd/teamup

build-frontend:
	cd web && pnpm install --frozen-lockfile && pnpm run build

test:
	go test -v -race -coverprofile=coverage.out ./...

lint:
	golangci-lint run
	cd web && pnpm lint && pnpm typecheck

docker:
	docker build -t teamup:$(VERSION) .

helm:
	helm package helm/teamup -d dist/helm

release:
	git tag -a v$(VERSION) -m "Release v$(VERSION)"
	git push origin v$(VERSION)
```

---

### 8. Open Questions (for later)

- **Auto-update mechanism** for self-hosted instances (check GitHub Releases, download binary) — v2
- **SBOM generation** (Syft) — compliance
- **Cosign signing** for binaries/images — supply chain security
- **Canary/blue-green deployment** via Helm — v2
- **Multi-region Docker builds** — for faster arm64 builds

Status: resolved
