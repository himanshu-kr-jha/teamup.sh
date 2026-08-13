# 43 — Add the GitHub Actions pipeline the CI/CD decision specifies

**What to build:** The CI/CD decision records a full GitHub Actions pipeline — lint, unit/integration/e2e tests, multi-arch binaries, distroless Docker to GHCR, Helm chart, GitHub Release. None of it exists in the repository. Build the workflows.

**Blocked by:** 33, 34, 35 — a pipeline that goes green over the current WebSocket and auth defects would certify a broken build

**Status:** completed — but see "What is unproven", the workflows have never been executed by GitHub

- [x] `.github/workflows/ci.yml`: lint → tests with a Postgres service container → integration-tagged tests
- [x] Fail CI when database-backed tests skip, so an unset `TEAMUP_TEST_DATABASE_URL` cannot turn into a green run
- [x] Include `./cmd/...` in the tested packages (see ticket 45)
- [x] `.github/workflows/release.yml`: multi-arch amd64/arm64 build, distroless image to GHCR, Helm package, GitHub Release on semver tags
- [x] Run `go test -race` in CI — it is what catches ticket 34's class of bug

**Type:** task

## Evidence

There is no `.github` directory:

```
$ ls -la .github/workflows/
ls: cannot access '.github/workflows/': No such file or directory

$ find . -maxdepth 2 -name ".github" -not -path "*/node_modules/*"
(no output)

$ find . -path ./web/node_modules -prune -o \( -name "*.yml" -o -name "*.yaml" \) -print | grep -iE "github|ci|workflow|action"
(no output)
```

The build targets the pipeline would call do exist and work — `Makefile` defines `build`, `build-all` (amd64/arm64 at `Makefile:66-73`), `test-unit`, `test-integration`, `test-e2e`, `lint`, `docker`, `docker-push`, `helm` — and there is a `Dockerfile` and a `helm/teamup` chart. So this is genuinely just the missing workflow layer, not missing build machinery.

Verified locally, so CI has a known-good baseline to reproduce:

```
go build ./...                      -> clean
go vet ./...                        -> clean
go test -race ./internal/... ./pkg/... ./cmd/...
   -> 286 passed, 0 failed, 0 skipped   (with TEAMUP_TEST_DATABASE_URL set)
```

### The skip trap to guard against

`internal/testsupport/db.go` skips database-backed tests when `TEAMUP_TEST_DATABASE_URL` is unset. Without it the suite still exits 0 while testing almost nothing. A workflow that forgets the service container would be permanently, silently green. Assert a minimum test count or fail on any skip.

### Note on ordering

Listed as blocked deliberately. Standing up CI first would produce a green badge on a build whose WebSocket protocol drops every inbound message (33) and whose hub can be crashed by an ordinary call (34). Land those, then let CI hold the line.

## Comments

### What was added

| File | Purpose |
|---|---|
| `.github/workflows/ci.yml` | `frontend` → `lint` / `test` → `build` / `docker`, plus `helm`. Push to main and PRs to main. Publishes nothing. |
| `.github/workflows/release.yml` | `build-release` → `docker` (GHCR) / `helm` → `github-release`. Triggered only by a `v*` tag. |
| `.github/dependabot.yml` | Weekly gomod / npm / github-actions / docker updates. |
| `.golangci.yml` | Pins the linter set so an upgrade cannot silently change what CI enforces. |
| `.dockerignore` | New. Keeps `web/node_modules`, `.git` and the scratch trees out of the build context. |
| `Dockerfile` | Modified — see "The Dockerfile was not multi-arch". |

### `go build ./...` does not work on a fresh checkout

The single most important thing a first-time CI author needs to know here, and it is not written down anywhere else:

```
$ go build ./...
cmd/teamup/main.go:49:12: pattern all:web/dist: no matching files found
```

`cmd/teamup/main.go:49` has `//go:embed all:web/dist`, resolving to `cmd/teamup/web/dist`, and `.gitignore:44` ignores that directory. So `go build`, `go vet`, `go run ./cmd/teamup migrate` and `go test ./cmd/...` **all fail on a clean clone until the SPA has been built**. A pipeline that runs `go vet` before the frontend job dies on line one.

That is why `ci.yml` starts with a `frontend` job that builds the SPA and uploads `cmd/teamup/web/dist` as an artifact, which every Go job downloads. It costs one job and removes a failure mode that looks like a Go problem and is not.

### The skip trap, and the guard — demonstrated, not assumed

`test` runs a `postgres:16-alpine` service container, migrates it, then runs the suite with `-v` through `tee`, and two guard steps read the log:

- **any `--- SKIP` fails the build**
- **fewer than `MIN_EXPECTED_TESTS` (280) top-level passes fails the build**

Proven by running the trap deliberately, with `TEAMUP_TEST_DATABASE_URL` unset:

```
go test exit=0 (note: green despite testing nothing)
::error::tests were skipped — CI must run the database-backed suite, not step over it
--- SKIP: TestUpdateTaskRejectsValuesTheColumnCannotHold (0.00s)
--- SKIP: TestUpdateTaskAcceptsWellFormedValues (0.00s)
--- SKIP: TestUpdateTaskRejectsTheWholeBodyWhenOneFieldIsBad (0.00s)
skip-guard would exit 1  <-- correct
0 top-level tests passed (floor: 280)
count-guard would exit 1  <-- correct
```

And with the database present, the same steps run verbatim:

```
289 top-level tests passed (floor: 280)
skip-guard exit=0
count-guard exit=0
```

### Only Postgres is needed

Grepping every `_test.go` in the repository, `TEAMUP_TEST_DATABASE_URL` is the only environment variable any test reads. Redis, MinIO and LiveKit service containers — which decision 08 specifies — would be dead weight, so they are not in the workflow. Add them when a test actually needs one.

### `make test-db` is not called, on purpose

`Makefile:81-87` drops and recreates a hardcoded `teamup_test` regardless of the `TEST_DATABASE_URL` it is handed. The workflow migrates the service container's own database with `go run ./cmd/teamup migrate up` instead. If that Makefile bug is fixed on another branch, switching the step over is a one-line change — but the workflow does not depend on it landing.

### `teamup migrate` demands config it does not use (issue 46)

```
$ TEAMUP_DATABASE_URL=... ./bin/teamup migrate up
Error: config parse: env: required environment variable "TEAMUP_REDIS_URL" is not set; ...
  "TEAMUP_MINIO_ENDPOINT" ... "TEAMUP_MINIO_ACCESS_KEY" ... "TEAMUP_MINIO_SECRET_KEY" ...
  "TEAMUP_MINIO_BUCKET" ... "TEAMUP_LIVEKIT_URL" ... "TEAMUP_LIVEKIT_API_KEY" ...
  "TEAMUP_LIVEKIT_API_SECRET" is not set
```

Migrating touches none of those. The migrate step therefore carries obviously-fake placeholders (`unused-by-migrate`, `127.0.0.1` endpoints) purely to satisfy the config parser, with a comment pointing at issue 46. No secrets are involved. Delete the placeholders once 46 makes config validation per-command.

### `make test-integration` is folded into the main run, not a second step

`go test -tags=integration ./internal/...` does not run *only* integration tests — a build tag adds files, it does not filter them — so the target re-runs the entire suite. No file in the repository carries `//go:build integration` today, so a separate step would have doubled a three-minute suite for zero extra coverage. The single test run in `ci.yml` carries `-tags=integration` instead: same effect today, and the first integration test somebody writes is picked up automatically. Verified locally: `make test-integration` passes and runs the whole ordinary suite.

### Coverage and the missing `covdata` tool

`make test-unit` **exits 1 on this machine**, and it is nothing to do with the tests:

```
$ make test-unit TEST_DATABASE_URL=.../teamup_test_c
# github.com/teamup/teamup/internal/httpx
go: no such tool "covdata"
...
ok  github.com/teamup/teamup/internal/tasks  17.685s  coverage: 52.7% of statements
make: *** [Makefile:94: test-unit] Error 1
```

Every real package passes; the five failures are the packages with *no test files*, where `-coverprofile` needs `covdata` to synthesise an empty profile. Cause:

```
$ go version                 go version go1.25.0 linux/amd64
$ go env GOTOOLDIR           ~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64/pkg/tool/linux_amd64
$ ls $(go env GOTOOLDIR)     asm cgo compile cover link preprofile vet
```

This toolchain was fetched as a module (`GOTOOLCHAIN=auto`) and ships a trimmed `pkg/tool` with no `covdata`. `actions/setup-go` installs the full distribution, which has it — **but I could not execute a GitHub runner to confirm that**, so the workflow does not assume it. A probe step runs `go tool covdata help`; if it fails, the coverage flags are dropped and a `::warning::` is emitted rather than the build dying with a message nobody would connect to coverage. Locally the probe correctly reports `available=false` and the suite runs without a profile.

Nothing is uploaded to Codecov: that needs a token, and this ticket adds no secrets.

### Lint blocks on new issues, not on the backlog

`golangci-lint run ./...` (v2.6.2, matching the Go 1.25 toolchain — v1.64.8 refuses outright with *"the Go language version (go1.24) used to build golangci-lint is lower than the targeted Go version (1.25.0)"*) reports **26 pre-existing issues**:

```
26 issues:
* errcheck: 20
* ineffassign: 1
* staticcheck: 4
* unused: 1
```

They sit in `cmd/teamup/main.go`, `internal/auth`, `internal/chat`, `internal/mail`, `internal/notifications`, `internal/org`, `internal/presence`, `internal/tasks`, `internal/testsupport`, `internal/websocket` and `internal/wizard` — mostly unchecked `defer x.Close()`. None are from this ticket's changes.

Blocking on all 26 would make every PR red for debt it did not create; excluding the linters entirely would make the job theatre. So the `golangci-lint` step runs on pull requests with `only-new-issues: true` — a PR is judged on what it adds. It is skipped on push, where `only-new-issues` has no diff to work from. `gofmt` and `go vet` block unconditionally on both events and are clean today.

**Follow-up:** clear those 26 and drop `only-new-issues`.

### The Dockerfile was not multi-arch

It hardcoded `GOARCH=amd64`, so `buildx --platform linux/amd64,linux/arm64` would have produced an arm64 image containing an x86-64 binary — a "multi-arch" release that crashes on arm64. Changed to:

```dockerfile
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=dev
ARG BUILD_DATE=unknown
...
CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="... -X main.version=${VERSION} ..."
```

`--platform=$BUILDPLATFORM` also means the arm64 build cross-compiles natively instead of crawling under QEMU. Verified locally:

```
#20 [linux/amd64->arm64 builder 8/8] RUN ... GOARCH=arm64 go build ...   DONE 79.1s
#23 [linux/amd64 builder 8/8]        RUN ... GOARCH=amd64 go build ...   DONE 79.1s
buildx multi-arch exit=0

$ file <arm64 image>/teamup
ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped
```

Also removed `COPY cmd/teamup/web/dist ./web/dist`: it copied the SPA to `/app/web/dist`, which nothing embeds (the embed resolves to `cmd/teamup/web/dist`, already present via `COPY . .`).

Left alone: `RUN go mod tidy` inside the build stage. It mutates `go.mod`/`go.sum` during an image build and needs the network. Wrong, but out of scope here.

### Publishing is gated

`ci.yml` never logs in to a registry and never pushes: its `docker` job builds both platforms with `outputs: type=cacheonly` purely to prove the Dockerfile still works on a PR. All publishing — GHCR push, `latest` tag, Helm package, GitHub Release — lives in `release.yml` behind `on: push: tags: ['v*']`. `packages: write` is scoped to the one job that needs it; `contents: write` to the release job only. No secret values were added; only the automatic `secrets.GITHUB_TOKEN` is referenced.

The tag is validated before anything is built, and a prerelease never claims `:latest`:

```
v1.0.0        -> version=1.0.0        prerelease=false (:latest tagged)
v1.1.0-rc.1   -> version=1.1.0-rc.1   prerelease=true  (no :latest tag)
v1.0 / vfoo / v1.0.0.0 / nightly -> rejected
```

### The Helm chart packages, but it is empty

`helm lint` and `helm package` both succeed:

```
$ helm lint helm/teamup
[INFO] Chart.yaml: icon is recommended
1 chart(s) linted, 0 chart(s) failed

$ helm package helm/teamup -d dist/helm
Successfully packaged chart and saved it to: dist/helm/teamup-1.0.0.tgz
```

But `helm/teamup` contains only `Chart.yaml` and `values.yaml` — **no `templates/` directory**. Decision 08 specifies deployment, service, ingress, configmap, secret, servicemonitor and pvc templates plus subchart dependencies. `helm install` of this chart would create nothing. The workflow packages and attaches it because that is what the ticket asks for; writing the templates is a separate piece of work and should have its own ticket.

### Not built

- **`docker.yml` (scheduled builds) and `helm.yml` (chart publishing)** from decision 08's file listing. Not in this ticket's checklist and neither has a consumer yet. The optional OCI chart push to GHCR is also omitted.
- **An e2e job.** `make test-e2e` runs `pnpm playwright install && pnpm test:e2e`, but there is no `playwright.config.*` and no e2e spec anywhere in `web/`. A job for it would fail immediately or, worse, be made to pass by ignoring the error.
- **Trivy / gosec security scanning** from decision 08 §2. Deferred — neither tool is configured in the repository and a first run on this codebase would need triage that does not belong in this ticket.

## What was verified locally, and what was not

Everything below was executed on this machine against the live Postgres.

| Step in the workflow | Result |
|---|---|
| `actionlint` + `shellcheck` over both workflow files | exit 0, no findings |
| YAML parse, job graph, `needs` targets, step shapes | all valid |
| `gofmt -l ./cmd ./internal ./pkg` | clean |
| `go vet ./...` | clean |
| `golangci-lint run ./...` (v2.6.2, with `.golangci.yml`) | 26 pre-existing issues, itemised above; `config verify` passes |
| `go run ./cmd/teamup migrate up` against a scratch database | `Migrations applied successfully` |
| covdata probe | correctly reports `available=false` on this toolchain |
| full suite, `-race -v`, with the database | 289 passed / 0 failed / 0 skipped |
| skip guard, with the database removed | fires, exit 1 — output above |
| minimum-count guard | fires at 0 passes, passes at 289 |
| `make test-integration` | exit 0 (runs the whole ordinary suite) |
| cross-compile amd64 + arm64 | both `ELF 64-bit`, correct machine type each |
| `teamup version`, `teamup migrate --help` smoke tests | exit 0 |
| `docker buildx build --platform linux/amd64,linux/arm64` | exit 0; exported arm64 image contains an aarch64 binary |
| `helm lint` / `helm package` (v3.16.3) | exit 0 |
| frontend: install, `eslint`, `vitest run`, `tsc && vite build` | all exit 0; 8 vitest tests pass; SPA written to `cmd/teamup/web/dist` |

### What is unproven

- **No workflow has been run by GitHub.** Everything above is the same *commands* run locally; the runner environment, the action versions and the artifact hand-off between jobs have not been exercised. Expect the first run to need a fixup.
- **`covdata` on the runner.** Reasoned, not observed. The probe step exists precisely because I could not check.
- **`only-new-issues: true`** behaviour on pull requests is taken from the action's documentation. The step is confined to `pull_request` events so that a push cannot hit an untested path.
- **Frontend commands were run with `npm`, not `pnpm`** — pnpm is not on this machine's PATH. The scripts are identical, but dependency resolution from `pnpm-lock.yaml` may differ from what npm chose. The workflow uses `pnpm/action-setup@v4` with pnpm 9, matching `lockfileVersion: '9.0'`.
- **The GHCR push and the GitHub Release have never executed.** By design — they only run on a `v*` tag, and no tag was created.
- **The `postgres:16-alpine` service container** was not started; the local Postgres 16 stood in. The harness needs `CREATE DATABASE` to clone its per-package databases, which the container's `POSTGRES_USER` superuser provides.
