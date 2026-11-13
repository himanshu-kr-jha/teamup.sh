#!/usr/bin/env bash
#
# Sets up TeamUp for a new organization on a fresh Ubuntu server, from your
# machine, in one go: docs/DEPLOYMENT.md's walkthrough, steps 3 to 10.
#
#   scripts/new-server.sh --host ubuntu@203.0.113.10 --key ~/keys/acme.pem --domain team.acme.com
#
# Before running it:
#   - the server is Ubuntu 22.04 or 24.04, and you can ssh in as a user with sudo
#   - its cloud firewall allows TCP 22, 80, 443 and 7881, and UDP 7882
#   - the domain resolves to the server (with no domain, use <a>-<b>-<c>-<d>.sslip.io
#     for the address a.b.c.d)
#
# It asks for the organization's name and the owner's name, email and password,
# so nothing secret is typed on the command line. Options, all optional:
#   --org-name "Acme Ltd"   --owner-name "Asha Rao"   --owner-email asha@acme.com
#   --email you@acme.com    for the certificate's expiry notices
#   --name acme             what to call this server here (default: from the domain)
#   --ipv4                  ssh over IPv4 only
#
# What it does, in order: checks the server and the address; installs Docker and
# certbot; copies main as it is on GitHub (never your uncommitted changes); writes
# .env with new random secrets; gets the certificate and sets up its renewal;
# builds and starts everything except the website; creates the organization and
# its owner through the setup API from inside the server, so there is never a
# moment when a stranger could reach the setup page and claim it; starts the
# website; sets up nightly backups; checks the site; and keeps, on this machine,
# a copy of the server's .env and a deploy file so `make deploy` can update it.
#
# It refuses a server that already has TeamUp, so it cannot overwrite anybody's
# data. The long part runs on the server under nohup, so a dropped connection
# does not stop it halfway.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

HOST="" KEY="" DOMAIN="" EMAIL="" NAME="" IPV4=0 DIR=/opt/teamup
ORG_NAME="" OWNER_NAME="" OWNER_EMAIL=""
while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="$2"; shift 2 ;;
    --key) KEY="$2"; shift 2 ;;
    --domain) DOMAIN="$2"; shift 2 ;;
    --email) EMAIL="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --ipv4) IPV4=1; shift ;;
    --org-name) ORG_NAME="$2"; shift 2 ;;
    --owner-name) OWNER_NAME="$2"; shift 2 ;;
    --owner-email) OWNER_EMAIL="$2"; shift 2 ;;
    -h|--help) awk 'NR==1{next} /^#/{sub(/^#[[:space:]]?/,""); print; next} {exit}' "$0"; exit 0 ;;
    *) echo "Unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
done

die() { echo "new-server: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }

[ -n "$HOST" ] || die "say which server: --host user@address"
[ -n "$DOMAIN" ] || die "say its address: --domain team.example.com (or <a>-<b>-<c>-<d>.sslip.io)"
case "$DOMAIN" in *[!a-zA-Z0-9.-]* | .* | *. ) die "$DOMAIN is not a domain name" ;; esac
NAME="${NAME:-$(printf '%s' "$DOMAIN" | cut -d. -f1 | tr -c 'a-zA-Z0-9-\n' '-' | tr '[:upper:]' '[:lower:]')}"
DEPLOY_FILE=".deploy.$NAME.env"
SAVED_ENV="$HOME/.teamup-servers/$DOMAIN.env"
[ ! -e "$DEPLOY_FILE" ] || die "$DEPLOY_FILE already exists: is $NAME set up already? Choose another --name."

SSH=(ssh -o ConnectTimeout=20 -o ServerAliveInterval=15)
if [ "$IPV4" = 1 ]; then SSH+=(-4); fi
if [ -n "$KEY" ]; then SSH+=(-i "$KEY"); fi
remote() { "${SSH[@]}" "$HOST" "$@"; }

# ── who and what ─────────────────────────────────────────────────────────────
step "The organization"
ask() { local v; read -r -p "$1: " v; printf '%s' "$v"; }
[ -n "$ORG_NAME" ] || ORG_NAME="$(ask "Organization name")"
[ ${#ORG_NAME} -ge 2 ] || die "the organization needs a name of at least 2 characters"
[ -n "$OWNER_NAME" ] || OWNER_NAME="$(ask "Owner's full name")"
[ -n "$OWNER_NAME" ] || die "the owner needs a name"
[ -n "$OWNER_EMAIL" ] || OWNER_EMAIL="$(ask "Owner's email")"
case "$OWNER_EMAIL" in *@*.*) ;; *) die "$OWNER_EMAIL is not an email address" ;; esac
while :; do
  read -r -s -p "Owner's password (12 characters or more): " OWNER_PASSWORD; echo
  if [ ${#OWNER_PASSWORD} -lt 12 ]; then echo "  At least 12 characters."; continue; fi
  read -r -s -p "The same again: " again; echo
  [ "$OWNER_PASSWORD" = "$again" ] && break
  echo "  They differ; try again."
done
# The organization's short name, as the setup wizard asks for it.
SHORT="$(printf '%s' "$ORG_NAME" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-' | sed 's/--*/-/g; s/^-//; s/-$//' | cut -c1-40)"
[ -n "$SHORT" ] || SHORT="$NAME"

# ── what is being installed ─────────────────────────────────────────────────
step "Checking"
git fetch --quiet origin main || die "could not fetch main from GitHub"
COMMIT="$(git rev-parse origin/main)"
TAG="$(git rev-parse --short "$COMMIT")"
echo "  installs main as it is on GitHub: $TAG ($(git log -1 --format=%s "$COMMIT"))"

remote 'true' || die "cannot ssh to $HOST"
remote "test ! -e '$DIR/.env'" || die "$HOST already has TeamUp in $DIR; this script only sets up empty servers. To update it, use make deploy."
remote 'sudo -n true' || die "$HOST: the ssh user needs sudo without a password"
# shellcheck disable=SC2016 # expands on the server
remote '. /etc/os-release && [ "$ID" = ubuntu ]' || die "$HOST is not Ubuntu"
SERVER_IP="$(remote 'curl -4 -s --max-time 10 https://checkip.amazonaws.com || true' | tr -d '[:space:]')"
RESOLVED="$(python3 -c 'import socket, sys; print(socket.gethostbyname(sys.argv[1]))' "$DOMAIN" 2>/dev/null || true)"
echo "  server's public address: ${SERVER_IP:-unknown}; $DOMAIN resolves to: ${RESOLVED:-nothing}"
if [ -n "$SERVER_IP" ] && [ "$RESOLVED" != "$SERVER_IP" ]; then
  die "$DOMAIN does not point at this server yet. Point it at $SERVER_IP and wait until it resolves, because the certificate needs it."
fi

# ── the part that runs on the server ────────────────────────────────────────
step "Copying $TAG"
git archive "$COMMIT" | remote 'rm -rf /tmp/teamup-new && mkdir -p /tmp/teamup-new && tar -x -C /tmp/teamup-new'

# The owner's details go over ssh's own encryption into a file only root can
# read, and are deleted as soon as the organization exists.
# Through the environment, not arguments, which anybody on this machine can list.
ORG_NAME="$ORG_NAME" SHORT="$SHORT" OWNER_NAME="$OWNER_NAME" OWNER_EMAIL="$OWNER_EMAIL" OWNER_PASSWORD="$OWNER_PASSWORD" \
  python3 -c 'import json, os; e = os.environ; print(json.dumps({"orgName": e["ORG_NAME"], "orgSubdomain": e["SHORT"], "adminName": e["OWNER_NAME"], "adminEmail": e["OWNER_EMAIL"], "adminPassword": e["OWNER_PASSWORD"]}))' \
  | remote 'umask 077; cat > /tmp/teamup-owner.json'
unset OWNER_PASSWORD again

REMOTE=$(cat <<'REMOTE_SCRIPT'
#!/bin/bash
set -euo pipefail
trap 'rc=$?; rm -f /tmp/teamup-owner.json; rm -rf /tmp/teamup-setup; echo "== exit $rc"; echo $rc > /tmp/teamup-new-server.done' EXIT
TAG="$1"; DIR="$2"; DOMAIN="$3"; EMAIL="$4"
export DEBIAN_FRONTEND=noninteractive

echo "== install Docker and certbot $(date -u +%T)"
if ! command -v docker >/dev/null 2>&1; then curl -fsSL https://get.docker.com | sudo sh >/dev/null; fi
sudo apt-get update -qq && sudo apt-get install -y -qq certbot rsync python3 openssl >/dev/null
sudo usermod -aG docker "$USER" || true

echo "== code and secrets"
sudo mkdir -p "$DIR" && sudo chown "$USER" "$DIR"
rsync -a /tmp/teamup-new/ "$DIR/"
cd "$DIR"
umask 077
cat > .env <<EOF
COMPOSE_FILE=docker-compose.prod.yml
COMPOSE_PROJECT_NAME=teamup
TEAMUP_DOMAIN=$DOMAIN
TEAMUP_IMAGE=teamup:$TAG
TEAMUP_VERSION=$TAG
POSTGRES_PASSWORD=$(openssl rand -hex 24)
REDIS_PASSWORD=$(openssl rand -hex 24)
MINIO_ACCESS_KEY=$(openssl rand -hex 12)
MINIO_SECRET_KEY=$(openssl rand -hex 24)
LIVEKIT_API_KEY=API$(openssl rand -hex 8)
LIVEKIT_API_SECRET=$(openssl rand -hex 32)
AUTH_SECRET=$(openssl rand -hex 32)
EOF
umask 022
dc() { sudo docker compose "$@"; }

echo "== certificate for $DOMAIN"
if [ -n "$EMAIL" ]; then reg=(-m "$EMAIL"); else reg=(--register-unsafely-without-email); fi
sudo certbot certonly --standalone -d "$DOMAIN" --agree-tos -n "${reg[@]}" 2>&1 | tail -2
mkdir -p certs
sudo install -m 600 "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" certs/tls.crt
sudo install -m 600 "/etc/letsencrypt/live/$DOMAIN/privkey.pem" certs/tls.key
sudo mkdir -p /etc/letsencrypt/renewal-hooks/pre /etc/letsencrypt/renewal-hooks/deploy /etc/letsencrypt/renewal-hooks/post
printf '#!/bin/sh\ncd %s && docker compose stop nginx\n' "$DIR" | sudo tee /etc/letsencrypt/renewal-hooks/pre/teamup.sh >/dev/null
printf '#!/bin/sh\ninstall -m 600 "$RENEWED_LINEAGE/fullchain.pem" %s/certs/tls.crt\ninstall -m 600 "$RENEWED_LINEAGE/privkey.pem" %s/certs/tls.key\n' "$DIR" "$DIR" | sudo tee /etc/letsencrypt/renewal-hooks/deploy/teamup.sh >/dev/null
printf '#!/bin/sh\ncd %s && docker compose start nginx\n' "$DIR" | sudo tee /etc/letsencrypt/renewal-hooks/post/teamup.sh >/dev/null
sudo chmod +x /etc/letsencrypt/renewal-hooks/*/teamup.sh

echo "== build $(date -u +%T)"
dc build teamup 2>&1 | tail -2

# Everything but the website, so the setup page is never reachable from outside.
echo "== start, without the website $(date -u +%T)"
dc up -d postgres redis minio livekit migrate teamup 2>&1 | tail -3
dc logs migrate --no-log-prefix 2>&1 | tail -1

echo "== create the organization and its owner"
mkdir -p /tmp/teamup-setup && chmod 700 /tmp/teamup-setup
set -a; . ./.env; set +a
python3 - /tmp/teamup-owner.json /tmp/teamup-setup <<'PY'
import json, os, sys
owner = json.load(open(sys.argv[1]))
out = sys.argv[2]
steps = {
    "step1": {"orgName": owner["orgName"], "orgSubdomain": owner["orgSubdomain"]},
    "step2": {k: owner[k] for k in ("adminName", "adminEmail", "adminPassword")},
    # Password sign-in is the real path; the provider is a required field.
    "step3": {"oidcProvider": "custom"},
    "step4": {
        "minioEndpoint": "minio:9000", "minioBucket": "teamup-files",
        "minioAccessKey": os.environ["MINIO_ACCESS_KEY"], "minioSecretKey": os.environ["MINIO_SECRET_KEY"],
        "livekitUrl": "http://livekit:7880",
        "livekitApiKey": os.environ["LIVEKIT_API_KEY"], "livekitApiSecret": os.environ["LIVEKIT_API_SECRET"],
    },
}
for name, body in steps.items():
    with open(os.path.join(out, name + ".json"), "w") as f:
        json.dump(body, f)
PY
rm -f /tmp/teamup-owner.json
# The curl image runs as its own user, so it has to be able to read these; the
# directory is removed as soon as setup is done.
chmod 755 /tmp/teamup-setup && chmod 644 /tmp/teamup-setup/*.json
NETWORK="$(sudo docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$(dc ps -q teamup)")"
# Setup asks for the code the server made at start, which it keeps in Redis.
# exec reads stdin unless told otherwise, and stdin is the rest of this script.
SETUP_CODE=""
for _ in $(seq 1 30); do
  SETUP_CODE="$(dc exec -T redis sh -c 'redis-cli --no-auth-warning GET wizard:setup-token' </dev/null 2>/dev/null | tr -d '\r')"
  [ -n "$SETUP_CODE" ] && break
  sleep 2
done
[ -n "$SETUP_CODE" ] || { echo "could not read the setup code from Redis"; exit 1; }
sudo docker run --rm --network "$NETWORK" -e SETUP_CODE="$SETUP_CODE" -v /tmp/teamup-setup:/w:ro curlimages/curl:8.11.1 sh -c '
  set -e
  cd /tmp
  for i in $(seq 1 60); do curl -fs -o /dev/null http://teamup:8080/api/wizard/status && break; sleep 2; done
  for s in step1 step2 step3 step4; do
    code=$(curl -s -o answer -w "%{http_code}" -b jar -c jar -H "X-Setup-Token: $SETUP_CODE" -H "Content-Type: application/json" --data @/w/$s.json http://teamup:8080/api/wizard/$s)
    [ "$code" = 200 ] || { echo "setup $s answered $code: $(cat answer)"; exit 1; }
  done
  code=$(curl -s -o answer -w "%{http_code}" -b jar -c jar -H "X-Setup-Token: $SETUP_CODE" -X POST http://teamup:8080/api/wizard/step5)
  [ "$code" = 200 ] || { echo "setup step5 answered $code: $(cat answer)"; exit 1; }
  curl -fs http://teamup:8080/api/wizard/status
  echo
'
rm -rf /tmp/teamup-setup

echo "== start the website"
dc up -d 2>&1 | tail -2
for _ in $(seq 1 60); do
  case "$(dc ps teamup --format '{{.Status}}')" in *"(healthy)"*) break ;; esac
  sleep 3
done
dc ps --format '{{.Service}}\t{{.Status}}'

echo "== nightly backups"
cat > backup.sh <<'EOF'
#!/bin/sh
set -e
cd /opt/teamup
mkdir -p backups
D=$(date +%F)
docker compose exec -T postgres pg_dump -U teamup -Fc teamup > "backups/postgres-$D.dump"
docker compose stop minio
docker run --rm -v teamup_minio-data:/data:ro -v "$PWD/backups":/backup alpine:3.20 \
  tar czf "/backup/minio-$D.tar.gz" -C /data .
docker compose start minio
cp .env "backups/env-$D"
find backups -type f -mtime +14 -delete
EOF
sed -i "s#/opt/teamup#$DIR#" backup.sh
chmod +x backup.sh
mkdir -p backups
# A new server has no crontab, and grep finds nothing to keep: both exit 1,
# which under pipefail stopped the setup here and left backups unscheduled.
{ sudo crontab -l 2>/dev/null | grep -v "$DIR/backup.sh" || true; echo "0 3 * * * $DIR/backup.sh >> $DIR/backups/backup.log 2>&1"; } | sudo crontab -
sudo certbot renew --dry-run >/dev/null 2>&1 && echo "certificate renewal: works" || echo "certificate renewal: the dry run failed; check with sudo certbot renew --dry-run"
REMOTE_SCRIPT
)
printf '%s\n' "$REMOTE" | remote 'cat > /tmp/teamup-new-server.sh && rm -f /tmp/teamup-new-server.done'
remote "nohup bash /tmp/teamup-new-server.sh '$TAG' '$DIR' '$DOMAIN' '$EMAIL' > /tmp/teamup-new-server.log 2>&1 < /dev/null &"

step "Setting up on the server (10 minutes or so; safe to lose this connection)"
shown=0
for _ in $(seq 1 180); do
  sleep 10
  lines="$(remote "cat /tmp/teamup-new-server.log 2>/dev/null" || true)"
  total="$(printf '%s\n' "$lines" | wc -l)"
  if [ "$total" -gt "$shown" ]; then printf '%s\n' "$lines" | tail -n +"$((shown + 1))"; shown="$total"; fi
  if remote "test -f /tmp/teamup-new-server.done" 2>/dev/null; then break; fi
done
rc="$(remote "cat /tmp/teamup-new-server.done 2>/dev/null" || echo unknown)"
[ "$rc" = 0 ] || die "the server side ended with $rc; read /tmp/teamup-new-server.log there."

# ── keep what is needed to look after it ─────────────────────────────────────
step "Keeping a copy of the server's secrets, and how to deploy to it"
mkdir -p "$(dirname "$SAVED_ENV")" && chmod 700 "$(dirname "$SAVED_ENV")"
remote "cat '$DIR/.env'" > "$SAVED_ENV"
chmod 600 "$SAVED_ENV"
{
  echo "# Where scripts/deploy.sh deploys $ORG_NAME. Never committed."
  echo "TEAMUP_DEPLOY_HOST=$HOST"
  [ -n "$KEY" ] && echo "TEAMUP_DEPLOY_KEY=$KEY"
  [ "$IPV4" = 1 ] && echo "TEAMUP_DEPLOY_IPV4=1"
  echo "TEAMUP_DEPLOY_DIR=$DIR"
} > "$DEPLOY_FILE"
chmod 600 "$DEPLOY_FILE"
echo "  the server's .env: $SAVED_ENV (a backup cannot be restored without it; keep it safe)"
echo "  deploy settings:   $DEPLOY_FILE"

step "Checking https://$DOMAIN"
ok=1
for path in / /healthz /api/status; do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 "https://$DOMAIN$path" || echo 000)"
  printf '  %-12s %s\n' "$path" "$code"
  [ "$code" = 200 ] || ok=0
done
[ "$ok" = 1 ] || die "the site is not answering as it should; read /tmp/teamup-new-server.log on the server."

step "$ORG_NAME is ready at https://$DOMAIN"
cat <<EOF
  Sign in as $OWNER_EMAIL, then under Organization:
    Email          the mail server, so members and guests are emailed
    Appearance     the logo and colour
    Members, Teams the people, by hand or from a CSV
    Meeting notes  keys for the note-taker, if wanted
  Copy $DIR/backups off the server regularly: a backup on the same disk does not survive losing it.
  To update it later: make deploy ARGS="--env $DEPLOY_FILE"
EOF
