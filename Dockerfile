# Every base image is pinned by digest, with its tag kept beside it for the
# reader and for Dependabot, so a rebuild of the same commit starts from the
# same bytes and a tag that is moved or hijacked upstream cannot change it.

# SPA stage
#
# Node runs on the build machine's architecture: the output is the same files
# whichever image they end up in.
FROM --platform=$BUILDPLATFORM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS web

# The exact version web/package.json names in packageManager, so the image
# resolves the lockfile the same way CI and every developer do.
RUN npm install -g pnpm@9.15.9

WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# Vite writes to ../cmd/teamup/web/dist, which is what the Go binary embeds.
RUN pnpm build

# Build stage
#
# Pinned to the build machine's own architecture and cross-compiled with
# GOARCH. Building `--platform linux/arm64` the ordinary way would run this
# stage under QEMU, which is slow, and the hardcoded `GOARCH=amd64` it used to
# carry would then have put an amd64 binary inside an arm64 image.
FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder

# Supplied by buildx, one value per requested platform.
ARG TARGETARCH

# Stamped into the binary by the release workflow; `dev` when built by hand.
ARG VERSION=dev
ARG COMMIT=dev
ARG BUILD_DATE=unknown

WORKDIR /app

# No extra packages: the build is CGO_ENABLED=0, so no C toolchain; the
# version comes in as build arguments, not from git (.git is not even in the
# build context); and modules come from the Go module proxy.

# Download Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code, and the SPA built above.
COPY . .
COPY --from=web /src/cmd/teamup/web/dist cmd/teamup/web/dist

# Build Go binary
WORKDIR /app
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /teamup ./cmd/teamup

# Runtime stage (distroless for minimal attack surface)
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=builder /teamup /teamup
COPY --from=builder /app/migrations /migrations

EXPOSE 8080

USER nonroot:nonroot

# Split so that `docker compose run teamup migrate up` runs the migrate command.
# With "server" in the entrypoint it ran the server, which then fails on the
# schema the command was meant to create.
ENTRYPOINT ["/teamup"]
CMD ["server"]
