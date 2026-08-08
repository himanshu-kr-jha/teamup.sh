#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"

# Load environment variables (ignores comments and blank lines, keeps values
# containing spaces intact — the previous `export $(cat .env | xargs)` split on
# every space and silently mangled them).
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
else
  echo "❌ .env not found. Copy .env.example to .env first." >&2
  exit 1
fi

COMPOSE="docker compose"
if ! docker compose version >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

echo "🚀 Starting TeamUp stack..."

# Compose owns the container lifecycle. The old script ran `docker run ...
# 2>/dev/null || docker start <name>`, which hid failures: when a port was
# briefly taken, the run failed, the fallback started a container that had
# never been attached to a network, and Postgres came up unreachable with no
# error printed.
echo "📦 Starting infrastructure (postgres, redis, minio, livekit)..."
$COMPOSE up -d postgres redis minio livekit

echo "⏳ Waiting for services to be healthy..."
deadline=$((SECONDS + 120))
while true; do
  unhealthy=""
  for svc in postgres redis minio livekit; do
    cid="$($COMPOSE ps -q "$svc" 2>/dev/null || true)"
    if [ -z "$cid" ]; then
      unhealthy="$unhealthy $svc(missing)"
      continue
    fi
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid" 2>/dev/null || echo unknown)"
    case "$status" in
      healthy|running) ;;
      *) unhealthy="$unhealthy $svc($status)" ;;
    esac
  done

  if [ -z "$unhealthy" ]; then
    echo "✅ Infrastructure ready"
    break
  fi
  if [ "$SECONDS" -ge "$deadline" ]; then
    echo "❌ Timed out waiting for:$unhealthy" >&2
    $COMPOSE ps
    exit 1
  fi
  sleep 3
done

echo "🔨 Building TeamUp..."
make build

echo "🔧 Running database migrations..."
./bin/teamup migrate up

echo "🌐 Starting TeamUp server on ${TEAMUP_BASE_URL:-http://localhost:8080}"
exec ./bin/teamup server
