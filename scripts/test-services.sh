#!/usr/bin/env bash
#
# The throwaway services the Go tests and the browser tests run against.
#
#   scripts/test-services.sh up       start whatever is not running, then prepare the test database
#   scripts/test-services.sh status   say what is running and on which port
#   scripts/test-services.sh migrate  bring the test database up to the newest migration
#   scripts/test-services.sh down     stop and remove them (their data goes with them)
#
# They are plain containers named teamup-cal-test-*, bound to 127.0.0.1 only,
# on the ports web/e2e/test-services.env names. They are separate from the
# development stack ./setup.sh starts, so a test run never touches your data.
#
# LiveKit posts its webhooks to the port the server under test listens on
# (E2E_PORT, 55470). That is fixed when the container is created, so if you
# change E2E_PORT, run `down` and `up` again.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck disable=SC1091
. "$ROOT/web/e2e/test-services.env"

PREFIX=teamup-cal-test
# The same versions production runs, so a test cannot pass on something production does not have.
PG_IMAGE=postgres:16-alpine
REDIS_IMAGE=redis:7-alpine
MINIO_IMAGE=cgr.dev/chainguard/minio@sha256:cee9a8e35885e93f14b3cb32960e5017aff74e2adfb52ba87c65003b0163886b
LIVEKIT_IMAGE=livekit/livekit-server:v1.13.11
MAILPIT_IMAGE=axllent/mailpit:v1.21.8

say() { printf '%s\n' "$*"; }
running() { [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = "true" ]; }
exists() { docker inspect "$1" >/dev/null 2>&1; }

# Starts a stopped container, or creates it with the arguments given.
ensure() {
  local name="$1"; shift
  if running "$name"; then
    say "  $name already running"
  elif exists "$name"; then
    docker start "$name" >/dev/null && say "  $name started"
  else
    docker run -d --name "$name" "$@" >/dev/null && say "  $name created"
  fi
}

livekit_config() {
  cat <<EOF
port: 7880
rtc:
  tcp_port: 55465
  udp_port: 55466
  use_external_ip: false
  node_ip: 127.0.0.1
keys:
  "devkey": "devsecret"
webhook:
  api_key: "devkey"
  urls:
    - http://host.docker.internal:${E2E_PORT}/api/livekit/webhook
EOF
}

# Over TCP, not the socket: on first boot Postgres runs a temporary server on the
# socket only while it initializes, then restarts, and a check against that one
# says ready a moment too early.
wait_for_postgres() {
  for _ in $(seq 1 60); do
    docker exec "$PREFIX-pg" pg_isready -h 127.0.0.1 -U teamup -q 2>/dev/null &&
      docker exec "$PREFIX-pg" psql -h 127.0.0.1 -U teamup -d postgres -Atqc 'SELECT 1' >/dev/null 2>&1 && return 0
    sleep 1
  done
  say "Postgres did not become ready; see: docker logs $PREFIX-pg" >&2
  exit 1
}

# go test clones the database TEAMUP_TEST_DATABASE_URL names, so it has to exist
# and carry every migration. The migrate command lives in the binary.
migrate() {
  wait_for_postgres
  docker exec "$PREFIX-pg" psql -h 127.0.0.1 -U teamup -d postgres -Atqc "SELECT 1 FROM pg_database WHERE datname = 'teamup_test'" | grep -q 1 \
    || docker exec "$PREFIX-pg" psql -h 127.0.0.1 -U teamup -d postgres -qc 'CREATE DATABASE teamup_test' >/dev/null
  say "  migrating teamup_test"
  (cd "$ROOT" && TEAMUP_DATABASE_URL="$TEAMUP_TEST_DATABASE_URL" go run ./cmd/teamup migrate up 2>&1 | tail -1)
}

up() {
  command -v docker >/dev/null || { say "Docker is needed: https://docs.docker.com/engine/install/" >&2; exit 1; }
  say "Starting the test services"
  ensure "$PREFIX-pg" -p 127.0.0.1:55461:5432 \
    -e POSTGRES_USER=teamup -e POSTGRES_PASSWORD=teamup -e POSTGRES_DB=postgres "$PG_IMAGE"
  ensure "$PREFIX-redis" -p 127.0.0.1:55462:6379 "$REDIS_IMAGE"
  ensure "$PREFIX-minio" -p 127.0.0.1:55463:9000 \
    -e MINIO_ROOT_USER=minioadmin -e MINIO_ROOT_PASSWORD=minioadmin "$MINIO_IMAGE" server /data
  # One TCP and one UDP port for media. Never publish a UDP range: Docker makes a
  # process per port, and a range of thousands takes minutes and gigabytes.
  ensure "$PREFIX-livekit" --add-host host.docker.internal:host-gateway \
    -p 127.0.0.1:55464:7880 -p 127.0.0.1:55465:55465/tcp -p 127.0.0.1:55466:55466/udp \
    -e LIVEKIT_CONFIG="$(livekit_config)" "$LIVEKIT_IMAGE"
  ensure "$PREFIX-mailpit" -p 127.0.0.1:55480:1025 -p 127.0.0.1:55481:8025 \
    "$MAILPIT_IMAGE" --smtp-auth-accept-any --smtp-auth-allow-insecure
  if [ ! -f "$ROOT/cmd/teamup/web/dist/index.html" ]; then
    # The binary embeds the web app and will not compile without it.
    say "  building the web app once, because the server embeds it"
    (cd "$ROOT" && make build-frontend >/dev/null)
  fi
  migrate
  say ""
  status
}

status() {
  for s in pg:55461 redis:55462 minio:55463 livekit:55464 mailpit:55481; do
    name="$PREFIX-${s%%:*}"
    if running "$name"; then state="running"; else state="not running"; fi
    printf '  %-26s %-12s 127.0.0.1:%s\n' "$name" "$state" "${s##*:}"
  done
}

down() {
  say "Removing the test services"
  for s in pg redis minio livekit mailpit; do
    docker rm -f "$PREFIX-$s" >/dev/null 2>&1 && say "  $PREFIX-$s removed" || true
  done
}

case "${1:-}" in
  up) up ;;
  status) status ;;
  migrate) migrate ;;
  down) down ;;
  *) awk 'NR==1{next} /^#/{sub(/^#[[:space:]]?/,""); print; next} {exit}' "$0"; exit 2 ;;
esac
