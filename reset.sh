#!/bin/bash
# Wipe every trace of a running TeamUp and leave it ready for the setup wizard:
# the database, the uploaded files, and the cached sessions and presence.
#
# The infrastructure containers keep running; only their contents go.
set -euo pipefail

cd "$(dirname "$0")"

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

# Find a service's container. Compose only knows the ones it started itself,
# and these are often left over from an earlier run under a different project
# name, so fall back to the fixed teamup-* name before giving up.
container_for() {
  local service="$1" cid
  cid="$($COMPOSE ps -q "$service" 2>/dev/null || true)"
  if [ -z "$cid" ] && docker ps --format '{{.Names}}' | grep -qx "teamup-$service"; then
    cid="teamup-$service"
  fi
  if [ -z "$cid" ]; then
    echo "❌ The $service container is not running. Start it with ./start.sh first." >&2
    exit 1
  fi
  printf '%s' "$cid"
}

# Parsed out of the connection string so this follows .env rather than
# hard-coding names that could drift apart from it.
DB_NAME="$(printf '%s' "${TEAMUP_DATABASE_URL}" | sed -E 's#.*/([^/?]+).*#\1#')"
DB_USER="$(printf '%s' "${TEAMUP_DATABASE_URL}" | sed -E 's#.*://([^:]+):.*#\1#')"
BUCKET="${TEAMUP_MINIO_BUCKET:-teamup-files}"

if [ "${1:-}" != "--yes" ]; then
  cat <<WARNING
This deletes everything in TeamUp and cannot be undone:

  database   ${DB_NAME}      every organization, account, message and task
  files      ${BUCKET}       every uploaded attachment and recording
  cache      redis           every signed-in session and presence record

You will sign in again through the setup wizard with a new account.

Re-run with --yes to go ahead:  ./reset.sh --yes
WARNING
  exit 1
fi

echo "🛑 Stopping the TeamUp server (the infrastructure stays up)..."
pkill -f "bin/teamup server" 2>/dev/null || true
sleep 1

PG="$(container_for postgres)"
MINIO="$(container_for minio)"
REDIS="$(container_for redis)"

echo "🗄️  Recreating the ${DB_NAME} database..."
# Terminate live connections first: DROP DATABASE fails while the pool is
# still attached, and the server may only just have gone.
docker exec "$PG" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 -c \
  "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${DB_NAME}' AND pid <> pg_backend_pid();" >/dev/null
docker exec "$PG" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 -c \
  "DROP DATABASE IF EXISTS ${DB_NAME};" >/dev/null
docker exec "$PG" psql -U "$DB_USER" -d postgres -v ON_ERROR_STOP=1 -c \
  "CREATE DATABASE ${DB_NAME} OWNER ${DB_USER};" >/dev/null

echo "🧹 Emptying the ${BUCKET} bucket..."
# The MinIO image has no shell or mc, so a one-shot client shares its network.
docker run --rm --network "container:$MINIO" \
  -e "MC_HOST_local=http://${TEAMUP_MINIO_ACCESS_KEY}:${TEAMUP_MINIO_SECRET_KEY}@localhost:9000" \
  cgr.dev/chainguard/minio-client@sha256:8dc1dceb7371c9091b17b450e4da67a492f6de3a22d716af14888a7d8597457e \
  rb --force "local/${BUCKET}" >/dev/null 2>&1 \
  || echo "   (bucket was already empty or absent - the server recreates it)"

echo "🧼 Flushing redis..."
docker exec "$REDIS" redis-cli FLUSHALL >/dev/null

echo "🔧 Applying migrations to the fresh database..."
if [ ! -x ./bin/teamup ]; then
  make build
fi
./bin/teamup migrate up

cat <<DONE

✅ Reset. TeamUp is back to a blank installation.

   Start it with:   ./start.sh
   Then open:       ${TEAMUP_BASE_URL:-http://localhost:8080}

   The setup wizard runs again and creates your owner account.
DONE
