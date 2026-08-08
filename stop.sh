#!/bin/bash
set -uo pipefail

cd "$(dirname "$0")"

COMPOSE="docker compose"
if ! docker compose version >/dev/null 2>&1; then
  COMPOSE="docker-compose"
fi

echo "🛑 Stopping TeamUp..."
pkill -f "teamup server" 2>/dev/null || true

$COMPOSE stop postgres redis minio livekit 2>/dev/null || true

# Earlier versions of start.sh created these by hand with `docker run`. Remove
# them so they can't hold ports 5432/6379/9000/7880 against the compose stack.
for name in teamup-postgres teamup-redis teamup-minio teamup-livekit; do
  if docker ps -a --format '{{.Names}}' | grep -qx "$name"; then
    echo "🧹 Removing legacy container $name (named volumes are kept)"
    docker rm -f "$name" >/dev/null 2>&1 || true
  fi
done

echo "✅ Stopped"
