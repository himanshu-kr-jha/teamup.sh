#!/usr/bin/env bash
#
# Deploys a commit to a TeamUp server that was set up as docs/DEPLOYMENT.md says.
#
#   scripts/deploy.sh                      deploy the latest main to the server in .deploy.env
#   scripts/deploy.sh --ref 56c6ac1        deploy an earlier commit of main, to roll back
#   scripts/deploy.sh --domain new.host    also move to a new address (new IP, new DNS name)
#   scripts/deploy.sh --check              only check the server and the commit, change nothing
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
[ -f .deploy.env ] && . ./.deploy.env

HOST="${TEAMUP_DEPLOY_HOST:-}"
KEY="${TEAMUP_DEPLOY_KEY:-}"
DIR="${TEAMUP_DEPLOY_DIR:-/opt/teamup}"
REF=origin/main
BACKUP=1
IPV4="${TEAMUP_DEPLOY_IPV4:-0}"
DOMAIN=""
EMAIL=""
CHECK=0

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
CURRENT="$("${SSH[@]}" "$HOST" "grep -E '^TEAMUP_(IMAGE|DOMAIN)=' '$DIR/.env'" || true)"
echo "$CURRENT" | sed 's/^/  now: /'
if [ "$CHECK" = 1 ]; then
  echo "Check only: nothing changed."
  exit 0
fi

# ── copy ──────────────────────────────────────────────────────────────────────
step "Copying $TAG"
git archive "$COMMIT" | "${SSH[@]}" "$HOST" 'rm -rf /tmp/teamup-new && mkdir -p /tmp/teamup-new && tar -x -C /tmp/teamup-new'

# ── the part that runs on the server ─────────────────────────────────────────
REMOTE=$(cat <<'REMOTE_SCRIPT'
#!/bin/bash
set -euo pipefail
trap 'rc=$?; echo "== exit $rc"; echo $rc > /tmp/teamup-deploy.done' EXIT
TAG="$1"; DIR="$2"; BACKUP="$3"; DOMAIN="$4"; EMAIL="$5"
cd "$DIR"
dc() { sudo docker compose "$@"; }

if [ "$BACKUP" = 1 ] && [ -x ./backup.sh ]; then
  echo "== backup"
  sudo ./backup.sh
fi

echo "== copy tree"
sudo rsync -a --delete --exclude=/.env --exclude=/certs --exclude=/backups --exclude=/backup.sh /tmp/teamup-new/ "$DIR/"
sudo sed -i "s/^TEAMUP_IMAGE=.*/TEAMUP_IMAGE=teamup:$TAG/; s/^TEAMUP_VERSION=.*/TEAMUP_VERSION=$TAG/" .env

echo "== build $(date -u +%T)"
dc build teamup 2>&1 | tail -3

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
# nginx reads its config once; LiveKit learns a new public address on restart.
dc restart nginx >/dev/null 2>&1
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
REMOTE_SCRIPT
)
printf '%s\n' "$REMOTE" | "${SSH[@]}" "$HOST" 'cat > /tmp/teamup-deploy.sh && rm -f /tmp/teamup-deploy.done'
"${SSH[@]}" "$HOST" "nohup bash /tmp/teamup-deploy.sh '$TAG' '$DIR' '$BACKUP' '$DOMAIN' '$EMAIL' > /tmp/teamup-deploy.log 2>&1 < /dev/null &"

step "Building and starting on the server (several minutes; safe to lose this connection)"
shown=0
for _ in $(seq 1 120); do
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
