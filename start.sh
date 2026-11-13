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
  echo "❌ .env not found — this machine has not been set up yet." >&2
  echo "   Run ./setup.sh first. It checks prerequisites, finds free ports," >&2
  echo "   and generates the secrets that .env.example only has placeholders for." >&2
  exit 1
fi

COMPOSE="docker compose"
if ! docker compose version >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

# --lan makes TeamUp reachable from other machines on this network. Three
# things have to be set together for that to work, and getting one of them
# wrong fails quietly, so they are set here rather than left to be remembered.
if [ "${1:-}" = "--lan" ]; then
  LAN_IP="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{print $7; exit}')"
  if [ -z "$LAN_IP" ]; then
    LAN_IP="$(ipconfig getifaddr en0 2>/dev/null || true)"
  fi
  if [ -z "$LAN_IP" ]; then
    echo "❌ Could not work out this machine's address on the network." >&2
    echo "   Find it and set TEAMUP_BASE_URL and TEAMUP_INSECURE_COOKIES by hand;" >&2
    echo "   docs/DEPLOYMENT.md has the details." >&2
    exit 1
  fi

  export TEAMUP_BASE_URL="http://${LAN_IP}:${TEAMUP_PORT:-8080}"
  # A browser discards a Secure cookie that arrives over plain HTTP, so
  # without this, signing in from another machine fails with no error.
  export TEAMUP_INSECURE_COOKIES=true
  # Presigned URLs are opened by the browser, not by the server, so the host
  # in them has to be one the other machine can reach. Left at localhost every
  # upload and every download resolves, on that machine, to that machine.
  export TEAMUP_MINIO_ENDPOINT="${LAN_IP}:${TEAMUP_MINIO_PORT:-9000}"
  # The server's own calls to storage stay local: they must not depend on the
  # LAN address still being routable from here.
  export TEAMUP_MINIO_INTERNAL_ENDPOINT="localhost:${TEAMUP_MINIO_PORT:-9000}"
  # LiveKit otherwise asks a public STUN server where it is, and is told the
  # router's public address — which nothing on this network can route to.
  export TEAMUP_LIVEKIT_USE_EXTERNAL_IP=false
  export TEAMUP_LIVEKIT_NODE_IP="$LAN_IP"

  echo ""
  echo "🌐 Local network mode"
  echo "   Open ${TEAMUP_BASE_URL} on any device on this Wi-Fi."
  echo ""
  echo "   The session cookie travels unencrypted, because a browser will not"
  echo "   keep a secure one over plain HTTP. Use this on a network you trust."
  echo ""
  echo "   Calls and desktop notifications will not work: a browser only allows"
  echo "   a camera, a microphone or a notification on HTTPS or localhost."
  echo "   docs/DEPLOYMENT.md explains how to get those working too."
  echo ""
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

APP_URL="${TEAMUP_BASE_URL:-http://localhost:${TEAMUP_PORT:-8080}}"

# Printed before the server starts rather than after, because `exec` below
# replaces this shell — once the server is running, nothing here runs again.
cat <<SUMMARY

  ┌──────────────────────────────────────────────────────────────┐
  │  TeamUp is starting                                          │
  └──────────────────────────────────────────────────────────────┘

    App              ${APP_URL}
    MinIO console    http://localhost:${TEAMUP_MINIO_CONSOLE_PORT:-9001}   (minioadmin / minioadmin)
    Metrics          ${APP_URL}/metrics
    Health           ${APP_URL}/healthz   ${APP_URL}/readyz

    Postgres         localhost:${TEAMUP_POSTGRES_PORT:-5432}      Redis      localhost:${TEAMUP_REDIS_PORT:-6379}
    LiveKit          localhost:${TEAMUP_LIVEKIT_PORT:-7880}

  Open ${APP_URL} — the first visit runs the setup wizard,
  which creates your organisation and owner account. There are no default
  credentials. The wizard asks for a setup code: the server prints it below
  as setup_code when it starts, so only whoever runs it can set it up.

  Stop with Ctrl-C, then ./stop.sh to shut the containers down too.

SUMMARY

if [ -n "${TEAMUP_LIVEKIT_NODE_IP:-}" ]; then
  echo "  LiveKit is advertising ${TEAMUP_LIVEKIT_NODE_IP}"
  echo
fi

exec ./bin/teamup server
