#!/usr/bin/env bash
#
# One-time setup for a TeamUp development machine.
#
# Checks that the tools TeamUp needs are present and new enough, finds ports
# that are actually free, writes a .env with real generated secrets, starts the
# four backing services, builds the app and applies the schema.
#
# It is safe to run again. An existing .env is kept as-is unless you pass
# --force, because the auth secret in it cannot be regenerated without signing
# everybody out.
#
#   ./setup.sh            full setup
#   ./setup.sh --check    only report what is missing, change nothing
#   ./setup.sh --force    rewrite .env from scratch (destroys the auth secret)
#
set -euo pipefail

cd "$(dirname "$0")"

ENV_FILE=".env"
WEB_ENV_FILE="web/.env"
MODE="setup"

for arg in "$@"; do
  case "$arg" in
    --check) MODE="check" ;;
    --force) MODE="force" ;;
    # Print the header comment block as the help text: skip the shebang, strip
    # the leading '#', and stop at the first line that is not a comment, so
    # this keeps working when the block above changes length.
    -h|--help) awk 'NR==1{next} /^#/{sub(/^#[[:space:]]?/,""); print; next} {exit}' "$0"; exit 0 ;;
    *) echo "Unknown option: $arg (try --help)" >&2; exit 2 ;;
  esac
done

# ── output helpers ────────────────────────────────────────────────────────────
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  BOLD=$'\033[1m'; DIM=$'\033[2m'; RED=$'\033[31m'; GRN=$'\033[32m'
  YLW=$'\033[33m'; BLU=$'\033[36m'; RST=$'\033[0m'
else
  BOLD=""; DIM=""; RED=""; GRN=""; YLW=""; BLU=""; RST=""
fi

step()  { printf '\n%s▸ %s%s\n' "$BOLD" "$1" "$RST"; }
ok()    { printf '  %s✓%s %s\n' "$GRN" "$RST" "$1"; }
warn()  { printf '  %s!%s %s\n' "$YLW" "$RST" "$1"; }
info()  { printf '    %s%s%s\n' "$DIM" "$1" "$RST"; }
fail()  { printf '  %s✗%s %s\n' "$RED" "$RST" "$1"; PROBLEMS=$((PROBLEMS + 1)); }
die()   { printf '\n%s✗ %s%s\n\n' "$RED" "$1" "$RST" >&2; exit 1; }

PROBLEMS=0

printf '\n%s┌────────────────────────────────────────────────┐%s\n' "$BLU" "$RST"
printf '%s│%s  %sTeamUp%s — self-hosted team collaboration       %s│%s\n' "$BLU" "$RST" "$BOLD" "$RST" "$BLU" "$RST"
printf '%s└────────────────────────────────────────────────┘%s\n' "$BLU" "$RST"

# ── 1. prerequisites ──────────────────────────────────────────────────────────
# Versions are compared with sort -V so that, say, 1.10 is correctly newer than
# 1.9 — a plain string comparison gets that backwards.
version_at_least() {
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" = "$2" ]
}

step "Checking prerequisites"

if command -v go >/dev/null 2>&1; then
  GO_VER="$(go env GOVERSION 2>/dev/null | sed 's/^go//')"
  if version_at_least "$GO_VER" "1.25"; then
    ok "Go $GO_VER"
  else
    fail "Go $GO_VER is too old — 1.25 or newer is required"
    info "https://go.dev/dl/"
  fi
else
  fail "Go is not installed (need 1.25+)"
  info "https://go.dev/dl/"
fi

if command -v node >/dev/null 2>&1; then
  NODE_VER="$(node --version | sed 's/^v//')"
  if version_at_least "$NODE_VER" "20.0.0"; then
    ok "Node.js $NODE_VER"
  else
    fail "Node.js $NODE_VER is too old — 20 or newer is required"
    info "https://nodejs.org/"
  fi
else
  fail "Node.js is not installed (need 20+)"
  info "https://nodejs.org/"
fi

# Either package manager can build the web app. pnpm owns the lockfile and is
# preferred, but it is usually installed through nvm, which a non-login shell
# does not always see — so npm is a first-class fallback, not a downgrade.
if command -v pnpm >/dev/null 2>&1; then
  ok "pnpm $(pnpm --version) (owns the lockfile)"
elif command -v npm >/dev/null 2>&1; then
  ok "npm $(npm --version)"
  info "pnpm is not on PATH; npm will be used instead"
else
  fail "Neither pnpm nor npm is on PATH"
fi

if command -v docker >/dev/null 2>&1; then
  if docker info >/dev/null 2>&1; then
    ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '')"
  else
    fail "Docker is installed but the daemon is not reachable"
    info "Start Docker Desktop, or: sudo systemctl start docker"
  fi
else
  fail "Docker is not installed"
  info "https://docs.docker.com/get-docker/"
fi

COMPOSE="docker compose"
if ! docker compose version >/dev/null 2>&1; then
  if command -v docker-compose >/dev/null 2>&1; then
    COMPOSE="docker-compose"
    ok "docker-compose (v1)"
  else
    fail "Docker Compose is not available"
    info "Compose v2 ships with Docker Desktop and recent Docker Engine"
  fi
else
  ok "Docker Compose $(docker compose version --short 2>/dev/null || echo v2)"
fi

if [ "$PROBLEMS" -gt 0 ]; then
  die "$PROBLEMS prerequisite(s) missing. Install them and run ./setup.sh again."
fi

# ── 2. ports ──────────────────────────────────────────────────────────────────
# A port held by something else is the single most common way this setup fails,
# and it fails confusingly: compose reports a bind error for a container the
# reader did not know existed. So find the clashes up front and route around
# them rather than letting Docker discover them.
port_busy() {
  local p="$1"
  if command -v ss >/dev/null 2>&1; then
    ss -ltnH "sport = :$p" 2>/dev/null | grep -q . && return 0 || return 1
  elif command -v lsof >/dev/null 2>&1; then
    lsof -iTCP:"$p" -sTCP:LISTEN -n -P >/dev/null 2>&1 && return 0 || return 1
  else
    (exec 3<>"/dev/tcp/127.0.0.1/$p") >/dev/null 2>&1 && { exec 3>&-; return 0; } || return 1
  fi
}

# Walk upward from the default until something is free, so the assigned port
# stays recognisably close to the conventional one.
#
# The result comes back in FOUND_PORT rather than on stdout. Returning it by
# echo would mean calling this inside $( ), and a subshell swallows both the
# warning about a moved port and any `die` — the caller would carry on with an
# empty value instead of stopping.
#
# CLAIMED remembers what has already been handed out in this run. Nothing is
# bound until compose starts, so without it two services asked for adjacent
# defaults can both be given the same free port and only collide later.
#
# 7882 starts out taken because docker-compose.yml always publishes it for
# LiveKit's media, whichever port the signalling moves to.
FOUND_PORT=""
CLAIMED="7882"
find_free_port() {
  local p="$1" limit=$(( $1 + 200 ))
  while [ "$p" -lt "$limit" ]; do
    case " $CLAIMED " in
      *" $p "*) p=$((p + 1)); continue ;;
    esac
    if ! port_busy "$p"; then
      FOUND_PORT="$p"
      CLAIMED="$CLAIMED $p"
      return 0
    fi
    p=$((p + 1))
  done
  die "Could not find a free port near $1"
}

step "Checking ports"

if [ -f "$ENV_FILE" ] && [ "$MODE" != "force" ]; then
  # Reuse whatever the existing .env already settled on. Re-detecting would see
  # TeamUp's own running containers as a conflict and pointlessly move ports.
  set -a; . "./$ENV_FILE"; set +a
  APP_PORT="${TEAMUP_PORT:-8080}"
  PG_PORT="${TEAMUP_POSTGRES_PORT:-5432}"
  REDIS_PORT="${TEAMUP_REDIS_PORT:-6379}"
  MINIO_PORT="${TEAMUP_MINIO_PORT:-9000}"
  MINIO_CONSOLE_PORT="${TEAMUP_MINIO_CONSOLE_PORT:-9001}"
  LIVEKIT_PORT="${TEAMUP_LIVEKIT_PORT:-7880}"
  LIVEKIT_TCP_PORT="${TEAMUP_LIVEKIT_TCP_PORT:-7881}"
  ok "Using the ports already recorded in .env"
else
  MOVED=0
  assign() { # assign <varname> <label> <default>
    local __var="$1" label="$2" want="$3"
    find_free_port "$want"
    if [ "$FOUND_PORT" != "$want" ]; then
      warn "$label: port $want is in use, using $FOUND_PORT instead"
      MOVED=$((MOVED + 1))
    fi
    printf -v "$__var" '%s' "$FOUND_PORT"
  }
  assign APP_PORT            'TeamUp'        8080
  assign PG_PORT             'PostgreSQL'    5432
  assign REDIS_PORT          'Redis'         6379
  assign MINIO_PORT          'MinIO'         9000
  assign MINIO_CONSOLE_PORT  'MinIO console' 9001
  assign LIVEKIT_PORT        'LiveKit'       7880
  assign LIVEKIT_TCP_PORT    'LiveKit RTC'   7881
  if [ "$MOVED" -eq 0 ]; then
    ok "All default ports are free"
  else
    info "Only the host side moves; the containers keep their standard ports."
  fi
fi

if [ "$MODE" = "check" ]; then
  printf '\n%s✓ Everything TeamUp needs is present.%s\n' "$GRN" "$RST"
  printf '  Run %s./setup.sh%s to finish setting up.\n\n' "$BOLD" "$RST"
  exit 0
fi

# ── 3. configuration ──────────────────────────────────────────────────────────
step "Writing configuration"

# Replace a KEY=... line in place, or append it when the key is new. & and | are
# escaped because sed treats them specially in a replacement.
set_env() {
  local key="$1" val="$2" esc
  esc="$(printf '%s' "$val" | sed -e 's/[&|\\]/\\&/g')"
  if grep -qE "^${key}=" "$ENV_FILE"; then
    sed -i.tmp -E "s|^${key}=.*|${key}=${esc}|" "$ENV_FILE" && rm -f "$ENV_FILE.tmp"
  else
    printf '%s=%s\n' "$key" "$val" >> "$ENV_FILE"
  fi
}

random_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

if [ -f "$ENV_FILE" ] && [ "$MODE" != "force" ]; then
  ok ".env already exists — keeping it"
  info "The auth secret in it cannot be regenerated without signing everyone out."
  info "Pass --force to start over from .env.example."
else
  if [ ! -f .env.example ]; then
    die ".env.example is missing — cannot generate a .env from it.
   If you cloned this repository and it is not there, the file was probably
   excluded by a global gitignore rule such as '.env.*' when it was committed."
  fi
  if [ -f "$ENV_FILE" ]; then
    BACKUP=".env.backup.$(date +%s)"
    cp "$ENV_FILE" "$BACKUP"
    warn "Existing .env saved as $BACKUP"
  fi
  cp .env.example "$ENV_FILE"

  # The example ships a placeholder auth secret. It encrypts the key TeamUp
  # signs tokens with, so leaving the published default in place would let
  # anyone who has read this repository mint valid tokens.
  set_env TEAMUP_AUTH_SECRET "$(random_secret)"
  ok "Generated a unique TEAMUP_AUTH_SECRET"

  set_env TEAMUP_PORT                "$APP_PORT"
  set_env TEAMUP_POSTGRES_PORT       "$PG_PORT"
  set_env TEAMUP_REDIS_PORT          "$REDIS_PORT"
  set_env TEAMUP_MINIO_PORT          "$MINIO_PORT"
  set_env TEAMUP_MINIO_CONSOLE_PORT  "$MINIO_CONSOLE_PORT"
  set_env TEAMUP_LIVEKIT_PORT        "$LIVEKIT_PORT"
  set_env TEAMUP_LIVEKIT_TCP_PORT    "$LIVEKIT_TCP_PORT"

  # The connection strings have to follow the ports, or the server dials a
  # container that is listening somewhere else.
  set_env TEAMUP_BASE_URL      "http://localhost:$APP_PORT"
  set_env TEAMUP_DATABASE_URL  "postgres://teamup:teamup@localhost:$PG_PORT/teamup?sslmode=disable"
  set_env TEAMUP_REDIS_URL     "redis://localhost:$REDIS_PORT"
  set_env TEAMUP_MINIO_ENDPOINT "localhost:$MINIO_PORT"
  set_env TEAMUP_LIVEKIT_URL   "http://localhost:$LIVEKIT_PORT"
  ok "Wrote .env with matching connection strings"
fi

set -a; . "./$ENV_FILE"; set +a

# The browser reaches LiveKit's signalling socket directly over plain HTTP, and
# the frontend falls back to port 7880 when it is not told otherwise. If LiveKit
# had to move, that fallback would dial a closed port and calls would fail with
# nothing in the server log to explain it.
if [ "${TEAMUP_LIVEKIT_PORT:-7880}" != "7880" ]; then
  printf 'VITE_LIVEKIT_URL=ws://localhost:%s\n' "$TEAMUP_LIVEKIT_PORT" > "$WEB_ENV_FILE"
  warn "LiveKit moved to $TEAMUP_LIVEKIT_PORT — wrote $WEB_ENV_FILE so calls still connect"
fi

# ── 4. backing services ───────────────────────────────────────────────────────
step "Starting backing services"
info "postgres · redis · minio · livekit"

$COMPOSE up -d postgres redis minio livekit

printf '  waiting for them to report healthy'
deadline=$((SECONDS + 180))
while true; do
  unhealthy=""
  for svc in postgres redis minio livekit; do
    cid="$($COMPOSE ps -q "$svc" 2>/dev/null || true)"
    if [ -z "$cid" ]; then unhealthy="$unhealthy $svc(missing)"; continue; fi
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$cid" 2>/dev/null || echo unknown)"
    case "$status" in
      healthy|running) ;;
      *) unhealthy="$unhealthy $svc($status)" ;;
    esac
  done
  [ -z "$unhealthy" ] && { printf '\n'; ok "All four are up"; break; }
  if [ "$SECONDS" -ge "$deadline" ]; then
    printf '\n'
    $COMPOSE ps
    die "Timed out waiting for:$unhealthy"
  fi
  printf '.'
  sleep 3
done

# ── 4b. object storage bucket ─────────────────────────────────────────────────
# Nothing in the server's boot path creates this bucket, and neither does the
# setup wizard's finalize step — only the wizard's "Test connection" button
# does. So an instance provisioned any other way comes up with /readyz failing
# on MinIO and every upload broken, for a reason nothing announces. Create it
# here instead of leaving people to discover it.
step "Creating the object storage bucket"
BUCKET="${TEAMUP_MINIO_BUCKET:-teamup-files}"
# The server image has no shell or mc, so a one-shot client shares its network.
# It retries because MinIO has no healthcheck to wait for, so it may still be
# starting.
MINIO_CLIENT="cgr.dev/chainguard/minio-client@sha256:8dc1dceb7371c9091b17b450e4da67a492f6de3a22d716af14888a7d8597457e"
MINIO_CID="$($COMPOSE ps -q minio 2>/dev/null || true)"
bucket_ready=""
for _ in $(seq 1 30); do
  if [ -n "$MINIO_CID" ] && docker run --rm --network "container:$MINIO_CID" \
      -e "MC_HOST_local=http://${TEAMUP_MINIO_ACCESS_KEY:-minioadmin}:${TEAMUP_MINIO_SECRET_KEY:-minioadmin}@localhost:9000" \
      "$MINIO_CLIENT" mb --ignore-existing "local/$BUCKET" >/dev/null 2>&1; then
    bucket_ready=1
    break
  fi
  sleep 1
done
if [ -n "$bucket_ready" ]; then
  ok "Bucket '$BUCKET' is ready"
else
  warn "Could not create the '$BUCKET' bucket automatically"
  info "Create it at http://localhost:${MINIO_CONSOLE_PORT:-9001} (minioadmin / minioadmin),"
  info "or uploads will fail and /readyz will report minio as unhealthy."
fi

# ── 5. build ──────────────────────────────────────────────────────────────────
step "Building TeamUp"
info "first run downloads Go modules and npm packages — a few minutes"
make build
ok "Built ./bin/teamup with the web app embedded"

# ── 6. schema ─────────────────────────────────────────────────────────────────
step "Applying database migrations"
./bin/teamup migrate up
ok "Schema is up to date"

# ── done ──────────────────────────────────────────────────────────────────────
cat <<DONE

${GRN}${BOLD}Setup complete.${RST}

  Start TeamUp:   ${BOLD}./start.sh${RST}
  Then open:      ${BOLD}http://localhost:${TEAMUP_PORT:-8080}${RST}

The first page is a setup wizard. It asks for an organisation name and creates
your owner account — there are no default credentials.

  ${DIM}MinIO console   http://localhost:${TEAMUP_MINIO_CONSOLE_PORT:-9001}  (minioadmin / minioadmin)${RST}
  ${DIM}Stop everything ./stop.sh${RST}
  ${DIM}Start over      ./reset.sh --yes${RST}

DONE
