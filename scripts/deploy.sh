#!/usr/bin/env bash
#
# Deploys a commit to a TeamUp server that was set up as docs/DEPLOYMENT.md says.
#
#   scripts/deploy.sh                      deploy the latest main to the server in .deploy.env
#   scripts/deploy.sh --ref 56c6ac1        deploy an earlier commit of main, to roll back
#   scripts/deploy.sh --domain new.host    also move to a new address (new IP, new DNS name)
#   scripts/deploy.sh --check              only check the server and the commit, change nothing
#   scripts/deploy.sh --env .deploy.acme.env   deploy to another server, such as one
#                                          scripts/new-server.sh set up
#
# Options, each of which can also be set in .deploy.env (never committed):
#   --host user@host     TEAMUP_DEPLOY_HOST   who to ssh in as, and where
#   --key path           TEAMUP_DEPLOY_KEY    the ssh key, if not your default one
#   --dir path           TEAMUP_DEPLOY_DIR    where TeamUp lives there (default /opt/teamup)
#   --ref ref            what to deploy (default: main as it is on GitHub)
#
# Only main is deployed. Whatever is asked for has to be a commit on main as it
# is on GitHub, so what runs is always something everybody can see and that
# went through review; anything else is refused. Merge to main first.
#   --no-backup          skip ./backup.sh before upgrading (it runs when it exists)
#   --ipv4               TEAMUP_DEPLOY_IPV4=1 ssh over IPv4 only, for networks whose IPv6 route is unreliable
#   --domain name        point TeamUp at a new address and get it a certificate
#   --email address      for the new certificate (default: none registered)
#   --now                deploy now, outside the deploy window (an urgent fix)
#   --during-calls       do not wait for calls to end (an urgent fix that cannot wait)
#   --wait minutes       TEAMUP_DEPLOY_WAIT: how long to wait for calls to end (default 30)
#
# When it runs. Deploys go out in the evening, TEAMUP_DEPLOY_WINDOW (default
# 20:00-08:00, India time, TEAMUP_DEPLOY_TZ). One asked for during the day is
# copied to the server now and booked there for the start of the window, so
# nobody has to keep a terminal open; --now sends it at once. Either way the
# server first waits, up to --wait minutes, until nobody is in a call, and gives
# up rather than cut one short: a deploy restarts the app, which makes everybody
# in a call reconnect.
#
# The image. main's checks build it on GitHub, which publishes it as
# ghcr.io/<repository>:sha-<commit> (.github/workflows/image.yml). The server
# downloads that instead of building, which took four minutes of both its CPUs
# from the call server. If it is not there (the checks have not finished, or
# the image is private), the server builds it as before.
#
# What it does, in order: copies the committed tree of the ref (never your
# uncommitted changes) to the server, keeps .env, certs/, backups/ and
# backup.sh as they are, backs up, builds an image tagged with the commit,
# starts it (migrations run first, and the server starts only if they pass),
# then checks the site answers. The previous image stays, for rolling back.
# The long part runs on the server under nohup, so a dropped ssh connection
# does not stop it halfway.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
# Which server: .deploy.env, or the file --env names. Read before the other
# options, so any of them still overrides it.
DEPLOY_ENV=.deploy.env
prev=""
for arg in "$@"; do
  [ "$prev" = --env ] && DEPLOY_ENV="$arg"
  prev="$arg"
done
if [ "$DEPLOY_ENV" != .deploy.env ] && [ ! -f "$DEPLOY_ENV" ]; then
  echo "deploy: no such file: $DEPLOY_ENV" >&2
  exit 1
fi
case "$DEPLOY_ENV" in /*) ;; *) DEPLOY_ENV="./${DEPLOY_ENV#./}" ;; esac
# shellcheck source=/dev/null
[ -f "$DEPLOY_ENV" ] && . "$DEPLOY_ENV"

HOST="${TEAMUP_DEPLOY_HOST:-}"
KEY="${TEAMUP_DEPLOY_KEY:-}"
DIR="${TEAMUP_DEPLOY_DIR:-/opt/teamup}"
REF=origin/main
BACKUP=1
IPV4="${TEAMUP_DEPLOY_IPV4:-0}"
DOMAIN=""
EMAIL=""
CHECK=0
DURING_CALLS=0
WAIT_MINUTES="${TEAMUP_DEPLOY_WAIT:-30}"
NOW=0
WINDOW="${TEAMUP_DEPLOY_WINDOW:-20:00-08:00}"
WINDOW_TZ="${TEAMUP_DEPLOY_TZ:-Asia/Kolkata}"
IMAGE_REPO="${TEAMUP_DEPLOY_IMAGE_REPO:-ghcr.io/himanshu-kr-jha/teamup.sh}"

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --key) KEY="$2"; shift 2 ;;
    --dir) DIR="$2"; shift 2 ;;
    --ref) REF="$2"; shift 2 ;;
    --no-backup) BACKUP=0; shift ;;
    --ipv4) IPV4=1; shift ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --email) EMAIL="$2"; shift 2 ;;
    --check) CHECK=1; shift ;;
    --during-calls) DURING_CALLS=1; shift ;;
    --now) NOW=1; shift ;;
    --wait) WAIT_MINUTES="$2"; shift 2 ;;
    --env) shift 2 ;;
    -h|--help) awk 'NR==1{next} /^#/{sub(/^#[[:space:]]?/,""); print; next} {exit}' "$0"; exit 0 ;;
    *) echo "Unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
done

die() { echo "deploy: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }

[ -n "$HOST" ] || die "say where to deploy: --host user@host, or TEAMUP_DEPLOY_HOST in .deploy.env"

SSH=(ssh -o ConnectTimeout=20 -o ServerAliveInterval=15)
SCP=(scp -q -o ConnectTimeout=20)
if [ "$IPV4" = 1 ]; then SSH+=(-4); SCP+=(-4); fi
if [ -n "$KEY" ]; then SSH+=(-i "$KEY"); SCP+=(-i "$KEY"); fi

# ── what is being deployed ────────────────────────────────────────────────────
git fetch --quiet origin main || die "could not fetch main from GitHub"
COMMIT="$(git rev-parse --verify --quiet "$REF^{commit}")" || die "no such commit: $REF"
TAG="$(git rev-parse --short "$COMMIT")"
git merge-base --is-ancestor "$COMMIT" origin/main \
  || die "$TAG is not on main. Only main is deployed: merge it to main and push, then deploy."
step "Deploying $TAG from main ($(git log -1 --format=%s "$COMMIT"))"
if [ "$COMMIT" != "$(git rev-parse origin/main)" ]; then
  echo "note: this is an earlier commit of main than the latest, $(git rev-parse --short origin/main): a rollback."
fi

# ── the server ────────────────────────────────────────────────────────────────
step "Checking the server"
"${SSH[@]}" "$HOST" "test -f '$DIR/.env' && test -f '$DIR/docker-compose.yml' && { docker compose version >/dev/null 2>&1 || sudo docker compose version >/dev/null; }" \
  || die "$HOST:$DIR does not look like a TeamUp install (no .env or compose file, or no Docker). Set it up with docs/DEPLOYMENT.md first."
# The compose file hands Redis its password through `configs: content:`, which
# Docker Compose understands from 2.23.1; an older one would stop mid-deploy.
COMPOSE_VERSION="$("${SSH[@]}" "$HOST" "docker compose version --short 2>/dev/null || sudo docker compose version --short" | tr -d 'v\r')"
if [ "$(printf '%s\n%s\n' 2.23.1 "$COMPOSE_VERSION" | sort -V | head -1)" != 2.23.1 ]; then
  die "the server has Docker Compose $COMPOSE_VERSION; this release needs 2.23.1 or later (sudo apt-get install --only-upgrade docker-compose-plugin)"
fi
echo "  Docker Compose $COMPOSE_VERSION"
# The Redis password is written into a config file, where spaces or quotes would split it.
"${SSH[@]}" "$HOST" "grep -qE '^REDIS_PASSWORD=[A-Za-z0-9_-]+$' '$DIR/.env'" \
  || die "REDIS_PASSWORD in $DIR/.env must be letters, digits, - and _ only (docs/DEPLOYMENT.md makes one with openssl rand -hex 24)"
# The server refuses to start in production on a short or example call secret.
"${SSH[@]}" "$HOST" "grep -E '^LIVEKIT_API_SECRET=' '$DIR/.env' | grep -qE '^LIVEKIT_API_SECRET=.{32,}$' && ! grep -qiE '^LIVEKIT_API_SECRET=.*(devsecret|changeme)' '$DIR/.env'" \
  || die "LIVEKIT_API_SECRET in $DIR/.env must be at least 32 characters and not an example (openssl rand -hex 32); the new server will not start without it"
CURRENT="$("${SSH[@]}" "$HOST" "grep -E '^TEAMUP_(IMAGE|DOMAIN)=' '$DIR/.env'" || true)"
while IFS= read -r line; do echo "  now: $line"; done <<<"$CURRENT"
if [ "$CHECK" = 1 ]; then
  echo "Check only: nothing changed."
  exit 0
fi

# ── when ────────────────────────────────────────────────────────────────────
# Minutes since midnight in the window's zone, and whether that is inside it.
minutes_of() { local h="${1%%:*}" m="${1##*:}"; echo $(( 10#$h * 60 + 10#$m )); }
OPENS="${WINDOW%%-*}"; CLOSES="${WINDOW##*-}"
now_min=$(minutes_of "$(TZ="$WINDOW_TZ" date +%H:%M)")
open_min=$(minutes_of "$OPENS"); close_min=$(minutes_of "$CLOSES")
if [ "$open_min" -le "$close_min" ]; then
  in_window=$(( now_min >= open_min && now_min < close_min ))
else
  in_window=$(( now_min >= open_min || now_min < close_min ))
fi
WHEN=""
if [ "$in_window" = 0 ] && [ "$NOW" = 0 ]; then
  # Today's opening, in the window's zone, as systemd reads it.
  WHEN="$(TZ="$WINDOW_TZ" date +%F) $OPENS:00 $WINDOW_TZ"
  echo "note: it is $(TZ="$WINDOW_TZ" date +%H:%M) in $WINDOW_TZ, outside the deploy window $WINDOW; booking it for $OPENS (or deploy with --now)."
fi

# ── copy ──────────────────────────────────────────────────────────────────────
step "Copying $TAG"
SRC="/tmp/teamup-new-$TAG"
git archive "$COMMIT" | "${SSH[@]}" "$HOST" "rm -rf '$SRC' && mkdir -p '$SRC' && tar -x -C '$SRC'"

# ── the part that runs on the server ─────────────────────────────────────────
REMOTE=$(cat <<'REMOTE_SCRIPT'
#!/bin/bash
set -euo pipefail
trap 'rc=$?; echo "== exit $rc"; echo $rc > /tmp/teamup-deploy.done' EXIT
TAG="$1"; DIR="$2"; BACKUP="$3"; DOMAIN="$4"; EMAIL="$5"
SRC="$6"; COMMIT="$7"; IMAGE_REPO="$8"; WAIT_MINUTES="$9"; DURING_CALLS="${10}"
cd "$DIR"
dc() { sudo docker compose "$@"; }
echo "== deploy $TAG, $(date '+%F %H:%M %Z')"

# The image GitHub built, fetched before anything else: it takes a few seconds
# and little CPU, where building here took minutes of all of it.
PULLED=0
if sudo docker pull -q "$IMAGE_REPO:sha-$COMMIT" >/dev/null 2>&1; then
  sudo docker tag "$IMAGE_REPO:sha-$COMMIT" "teamup:$TAG"
  PULLED=1
  echo "== image $IMAGE_REPO:sha-$COMMIT"
else
  echo "== no published image for $COMMIT; it will be built here"
fi

# Nobody in a call: the app's record of who joined and has not left. A session
# older than half a day is a leave that never arrived, not somebody talking.
in_calls() {
  dc exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SELECT count(DISTINCT s.user_id) FROM call_sessions s JOIN calls c ON c.id = s.call_id WHERE s.left_at IS NULL AND c.status = '"'"'active'"'"' AND s.joined_at > now() - interval '"'"'12 hours'"'"'"' 2>/dev/null | tr -d '[:space:]'
}
if [ "$DURING_CALLS" = 0 ]; then
  deadline=$(( $(date +%s) + WAIT_MINUTES * 60 ))
  while :; do
    n="$(in_calls || true)"
    case "$n" in
      ''|*[!0-9]*) echo "could not tell whether anybody is in a call (is the database up?); deploy with --during-calls to go anyway"; exit 3 ;;
    esac
    [ "$n" = 0 ] && { echo "== nobody is in a call"; break; }
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "$n still in calls after $WAIT_MINUTES minutes; nothing was changed. Deploy later, or with --during-calls if it cannot wait"
      exit 4
    fi
    echo "== $n in calls at $(date +%H:%M); checking again in a minute"
    sleep 60
  done
fi

if [ "$BACKUP" = 1 ] && [ -x ./backup.sh ]; then
  echo "== backup"
  sudo ./backup.sh
fi

echo "== copy tree"
sudo rsync -a --delete --exclude=/.env --exclude=/certs --exclude=/backups --exclude=/backup.sh "$SRC/" "$DIR/"
sudo sed -i "s/^TEAMUP_IMAGE=.*/TEAMUP_IMAGE=teamup:$TAG/; s/^TEAMUP_VERSION=.*/TEAMUP_VERSION=$TAG/" .env

if [ "$PULLED" = 0 ]; then
  echo "== build $(date -u +%T)"
  dc build teamup 2>&1 | tail -3
fi

if [ -n "$DOMAIN" ]; then
  OLD="$(grep '^TEAMUP_DOMAIN=' .env | cut -d= -f2)"
  echo "== certificate for $DOMAIN"
  dc stop nginx
  if [ -n "$EMAIL" ]; then reg=(-m "$EMAIL"); else reg=(--register-unsafely-without-email); fi
  sudo certbot certonly --standalone -d "$DOMAIN" --agree-tos -n "${reg[@]}" 2>&1 | tail -3
  sudo install -m 600 "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" certs/tls.crt
  sudo install -m 600 "/etc/letsencrypt/live/$DOMAIN/privkey.pem" certs/tls.key
  sudo sed -i "s/^TEAMUP_DOMAIN=.*/TEAMUP_DOMAIN=$DOMAIN/" .env
fi

echo "== start $(date -u +%T)"
dc up -d 2>&1 | tail -4
# nginx reads its config once, so it is told to read it again. A reload lets
# the connections it already has finish, where a restart dropped every call's
# signalling at once; it falls back to a restart if nginx is not running.
dc exec -T nginx nginx -s reload >/dev/null 2>&1 || dc restart nginx >/dev/null 2>&1
# LiveKit learns a new public address on restart.
if [ -n "$DOMAIN" ]; then
  dc restart livekit >/dev/null 2>&1
  if [ -n "$OLD" ] && [ "$OLD" != "$DOMAIN" ]; then sudo certbot delete --cert-name "$OLD" -n >/dev/null 2>&1 || true; fi
fi

echo "== migrations"
dc logs migrate --no-log-prefix 2>&1 | tail -2
echo "== waiting for healthy"
for _ in $(seq 1 60); do
  state="$(dc ps teamup --format '{{.Status}}')"
  case "$state" in *"(healthy)"*) break ;; esac
  sleep 3
done
dc ps --format '{{.Service}}\t{{.Image}}\t{{.Status}}'

# Each build leaves about 1.5 GB of build cache and a 100 MB image behind, so
# a server deployed a few times a day fills its disk in weeks. Only once the
# new one is healthy: keep the three newest images (this one and two to roll
# back to) and 4 GB of cache, which is what makes the next build quick.
case "$(dc ps teamup --format '{{.Status}}')" in *"(healthy)"*)
  echo "== tidy"
  sudo docker images teamup --format '{{.CreatedAt}}\t{{.Repository}}:{{.Tag}}' | sort -r | tail -n +4 | cut -f2 \
    | xargs -r sudo docker rmi >/dev/null 2>&1 || true
  sudo docker builder prune -f --max-used-space 4gb >/dev/null 2>&1 \
    || sudo docker builder prune -f --keep-storage 4gb >/dev/null 2>&1 || true
  sudo docker image prune -f >/dev/null 2>&1 || true
  # Pulled images are kept as teamup:<commit>; the registry's name for them goes.
  sudo docker images "$IMAGE_REPO" --format '{{.Repository}}:{{.Tag}}' | xargs -r sudo docker rmi >/dev/null 2>&1 || true
  sudo rm -rf "$SRC"
  df -h / | awk 'NR==2 {print "disk: " $3 " used of " $2 " (" $5 ")"}'
;; esac
REMOTE_SCRIPT
)
printf '%s\n' "$REMOTE" | "${SSH[@]}" "$HOST" 'cat > /tmp/teamup-deploy.sh && rm -f /tmp/teamup-deploy.done'
ARGS_Q="'$TAG' '$DIR' '$BACKUP' '$DOMAIN' '$EMAIL' '$SRC' '$COMMIT' '$IMAGE_REPO' '$WAIT_MINUTES' '$DURING_CALLS'"

if [ -n "$WHEN" ]; then
  # Booked on the server, replacing any deploy booked earlier: the newest
  # commit of main is the one to ship. The script is copied per commit for the
  # same reason the tree is.
  "${SSH[@]}" "$HOST" "cp /tmp/teamup-deploy.sh /tmp/teamup-deploy-$TAG.sh \
    && for u in \$(systemctl list-units --all --plain --no-legend 'teamup-deploy-*' | awk '{print \$1}'); do sudo systemctl stop \"\$u\" >/dev/null 2>&1 || true; done \
    && sudo systemd-run --quiet --unit='teamup-deploy-$TAG' --on-calendar='$WHEN' --timer-property=AccuracySec=1s \
         /bin/bash -c \"bash /tmp/teamup-deploy-$TAG.sh $ARGS_Q > /tmp/teamup-deploy.log 2>&1 < /dev/null\"" \
    || die "could not book the deploy on the server"
  step "Booked $TAG for $WHEN"
  echo "  It waits for calls to end first. Its log will be /tmp/teamup-deploy.log on the server;"
  echo "  see what is booked with: ssh $HOST systemctl list-timers 'teamup-deploy-*'"
  exit 0
fi

"${SSH[@]}" "$HOST" "nohup bash /tmp/teamup-deploy.sh $ARGS_Q > /tmp/teamup-deploy.log 2>&1 < /dev/null &"

step "Waiting for calls to end, then starting on the server (safe to lose this connection)"
shown=0
# Up to the call wait plus a build, checking every ten seconds.
for _ in $(seq 1 $(( (WAIT_MINUTES + 20) * 6 ))); do
  sleep 10
  lines="$("${SSH[@]}" "$HOST" "cat /tmp/teamup-deploy.log 2>/dev/null" || true)"
  total="$(printf '%s\n' "$lines" | wc -l)"
  if [ "$total" -gt "$shown" ]; then printf '%s\n' "$lines" | tail -n +"$((shown + 1))"; shown="$total"; fi
  if "${SSH[@]}" "$HOST" "test -f /tmp/teamup-deploy.done" 2>/dev/null; then break; fi
done
rc="$("${SSH[@]}" "$HOST" "cat /tmp/teamup-deploy.done 2>/dev/null" || echo unknown)"
[ "$rc" = 0 ] || die "the server side ended with $rc; read /tmp/teamup-deploy.log there. The old image is still there to roll back to (docs/BUILD-AND-DEPLOY.md)."

# ── is it answering? ──────────────────────────────────────────────────────────
SITE="${DOMAIN:-$(printf '%s\n' "$CURRENT" | sed -n 's/^TEAMUP_DOMAIN=//p')}"
if [ -n "$SITE" ]; then
  step "Checking https://$SITE"
  ok=1
  for path in / /healthz /api/status; do
    code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "https://$SITE$path" || echo 000)"
    printf '  %-12s %s\n' "$path" "$code"
    [ "$code" = 200 ] || ok=0
  done
  curl -s --max-time 20 "https://$SITE/api/status" | grep -o '"overall":"[a-z]*"' | sed 's/^/  /' || true
  [ "$ok" = 1 ] || die "the site is not answering as it should. See docs/BUILD-AND-DEPLOY.md, Rolling back."
fi
step "Deployed $TAG"
