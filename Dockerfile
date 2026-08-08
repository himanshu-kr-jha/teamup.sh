# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev

# Download Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Copy pre-built frontend
COPY cmd/teamup/web/dist ./web/dist

# Build Go binary
WORKDIR /app
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.version=dev -X main.commit=dev -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /teamup ./cmd/teamup

# Runtime stage (distroless for minimal attack surface)
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /teamup /teamup
COPY --from=builder /app/migrations /migrations

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/teamup", "server"]
