# Build stage
#
# Pinned to the build machine's own architecture and cross-compiled with
# GOARCH. Building `--platform linux/arm64` the ordinary way would run this
# stage under QEMU, which is slow, and the hardcoded `GOARCH=amd64` it used to
# carry would then have put an amd64 binary inside an arm64 image.
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

# Supplied by buildx, one value per requested platform.
ARG TARGETARCH

# Stamped into the binary by the release workflow; `dev` when built by hand.
ARG VERSION=dev
ARG COMMIT=dev
ARG BUILD_DATE=unknown

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev

# Download Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code. This must already contain cmd/teamup/web/dist — the SPA is
# gitignored, so build it (`make build-frontend`) before building the image.
COPY . .

# Build Go binary
WORKDIR /app
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /teamup ./cmd/teamup

# Runtime stage (distroless for minimal attack surface)
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /teamup /teamup
COPY --from=builder /app/migrations /migrations

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/teamup", "server"]
