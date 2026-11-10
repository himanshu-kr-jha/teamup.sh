#!/usr/bin/env bash
# Runs the Playwright suite against a real server and a brand-new database.
#
#   web/e2e/run.sh [playwright args...]      e.g.  web/e2e/run.sh -g "Mia"
#
# Needs PostgreSQL, Redis, MinIO and LiveKit already running (docker compose up
# -d postgres redis minio livekit), and a built binary and frontend:
#   (cd web && pnpm build) && go build -o bin/teamup ./cmd/teamup
#
# Everything is overridable from the environment. The defaults match the
# throwaway containers used while the suite was written.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PG_HOST="${E2E_PG_HOST:-localhost}"
PG_PORT="${E2E_PG_PORT:-55450}"
PG_USER="${E2E_PG_USER:-teamup}"
PG_PASSWORD="${E2E_PG_PASSWORD:-teamup}"
DB_NAME="${E2E_DB_NAME:-teamup_e2e}"
PORT="${E2E_PORT:-18321}"
REDIS_URL="${E2E_REDIS_URL:-redis://localhost:6379/9}"
LOG="${E2E_SERVER_LOG:-${TMPDIR:-/tmp}/teamup-e2e-server.log}"

case "$DB_NAME" in
  *e2e*) ;;
  *) echo "Refusing to drop '$DB_NAME': the database name must contain 'e2e'." >&2; exit 1 ;;
esac

export PGPASSWORD="$PG_PASSWORD"
psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d postgres -v ON_ERROR_STOP=1 -q \
  -c "DROP DATABASE IF EXISTS \"$DB_NAME\" WITH (FORCE)" -c "CREATE DATABASE \"$DB_NAME\""

# For specs that need to look at or move rows the UI cannot (psql is already required above).
export E2E_DATABASE_URL="postgres://$PG_USER:$PG_PASSWORD@$PG_HOST:$PG_PORT/$DB_NAME?sslmode=disable"
export E2E_PORT="$PORT"
export TEAMUP_PORT="$PORT" TEAMUP_BASE_URL="http://localhost:$PORT" TEAMUP_ENV=development TEAMUP_LOG_LEVEL=${E2E_LOG_LEVEL:-info}
export TEAMUP_DATABASE_URL="postgres://$PG_USER:$PG_PASSWORD@$PG_HOST:$PG_PORT/$DB_NAME?sslmode=disable"
export TEAMUP_REDIS_URL="$REDIS_URL"
export TEAMUP_MINIO_ENDPOINT="${E2E_MINIO_ENDPOINT:-localhost:9000}" TEAMUP_MINIO_ACCESS_KEY=minioadmin TEAMUP_MINIO_SECRET_KEY=minioadmin
export TEAMUP_MINIO_BUCKET="${E2E_MINIO_BUCKET:-teamup}" TEAMUP_MINIO_SECURE=false
export TEAMUP_LIVEKIT_URL="${E2E_LIVEKIT_URL:-http://localhost:7880}" TEAMUP_LIVEKIT_API_KEY=devkey TEAMUP_LIVEKIT_API_SECRET=devsecret
export TEAMUP_AUTH_SECRET="e2e-secret-e2e-secret-e2e-secret-0123456789" TEAMUP_INSECURE_COOKIES=true
export TEAMUP_AUTO_SETUP=true TEAMUP_SETUP_ORG_NAME="E2E Org" TEAMUP_SETUP_ORG_SUBDOMAIN=e2e
export TEAMUP_SETUP_ADMIN_NAME="Olivia Owner" TEAMUP_SETUP_ADMIN_EMAIL=owner@e2e.test TEAMUP_SETUP_ADMIN_PASSWORD="Owner-pass-12345"
# Google Calendar sync, against the fake Google that meetings.y-gcal.spec.ts runs on this port.
export E2E_GOOGLE_PORT="${E2E_GOOGLE_PORT:-18091}"
export TEAMUP_GCAL_CLIENT_ID=e2e-gcal TEAMUP_GCAL_CLIENT_SECRET=e2e-gcal-secret TEAMUP_GCAL_TOKEN_KEY=e2e-gcal-token-key-0123456789
export TEAMUP_GCAL_ENDPOINT="http://127.0.0.1:$E2E_GOOGLE_PORT"
# GitHub, faked by meetings.y-github.spec.ts on this port.
export E2E_GITHUB_PORT="${E2E_GITHUB_PORT:-18092}"
export TEAMUP_GITHUB_WEB_URL="http://127.0.0.1:$E2E_GITHUB_PORT" TEAMUP_GITHUB_API_URL="http://127.0.0.1:$E2E_GITHUB_PORT"
export TEAMUP_SETUP_OIDC_PROVIDER=google TEAMUP_SETUP_GOOGLE_CLIENT_ID=e2e.apps.googleusercontent.com TEAMUP_SETUP_GOOGLE_CLIENT_SECRET=unused

cd "$ROOT"
"$ROOT/bin/teamup" migrate up
"$ROOT/bin/teamup" server >"$LOG" 2>&1 &
SERVER=$!
trap 'kill "$SERVER" 2>/dev/null || true; wait "$SERVER" 2>/dev/null || true' EXIT

for _ in $(seq 1 60); do
  curl -fsS "http://localhost:$PORT/readyz" >/dev/null 2>&1 && break
  kill -0 "$SERVER" 2>/dev/null || { echo "server exited; see $LOG" >&2; exit 1; }
  sleep 1
done
curl -fsS "http://localhost:$PORT/readyz" >/dev/null || { echo "server never became ready; see $LOG" >&2; exit 1; }

cd "$ROOT/web"
E2E_BASE_URL="http://localhost:$PORT" npx playwright test "$@"
