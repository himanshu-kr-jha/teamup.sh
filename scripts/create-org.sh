#!/usr/bin/env bash
#
# Creates the organization and its owner on a TeamUp server that has been
# installed but not set up yet: a fresh install, or one whose data was wiped.
# It answers the setup wizard from inside the server, with the storage and call
# keys the server already has in its .env, and keeps the website off until the
# organization exists, so a stranger can never reach the setup page and claim it.
#
#   scripts/create-org.sh                       the server in .deploy.env
#   scripts/create-org.sh --env .deploy.acme.env
#
# It asks for the organization's name and the owner's name, email and password
# (hidden), so nothing secret is typed on the command line or kept anywhere.
# Options, all optional:
#   --org-name "Acme Ltd"   --owner-name "Asha Rao"   --owner-email asha@acme.com
#
# It refuses a server that is already set up.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

ENV_FILE=".deploy.env" ORG_NAME="" OWNER_NAME="" OWNER_EMAIL=""
while [ $# -gt 0 ]; do
  case "$1" in
    --env) ENV_FILE="$2"; shift 2 ;;
    --org-name) ORG_NAME="$2"; shift 2 ;;
    --owner-name) OWNER_NAME="$2"; shift 2 ;;
    --owner-email) OWNER_EMAIL="$2"; shift 2 ;;
    -h|--help) awk 'NR==1{next} /^#/{sub(/^#[[:space:]]?/,""); print; next} {exit}' "$0"; exit 0 ;;
    *) echo "Unknown option: $1 (try --help)" >&2; exit 2 ;;
  esac
done

die() { echo "create-org: $*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }

[ -f "$ENV_FILE" ] || die "no such file: $ENV_FILE"
# shellcheck source=/dev/null
. "./${ENV_FILE#./}"
HOST="${TEAMUP_DEPLOY_HOST:-}"
KEY="${TEAMUP_DEPLOY_KEY:-}"
DIR="${TEAMUP_DEPLOY_DIR:-/opt/teamup}"
[ -n "$HOST" ] || die "$ENV_FILE does not say TEAMUP_DEPLOY_HOST"

SSH=(ssh -o ConnectTimeout=20 -o ServerAliveInterval=15)
if [ "${TEAMUP_DEPLOY_IPV4:-0}" = 1 ]; then SSH+=(-4); fi
if [ -n "$KEY" ]; then SSH+=(-i "$KEY"); fi
remote() { "${SSH[@]}" "$HOST" "$@"; }

step "Checking $HOST"
remote "test -f '$DIR/.env'" || die "$HOST has no TeamUp in $DIR; set one up with scripts/new-server.sh"
DOMAIN="$(remote "grep -m1 '^TEAMUP_DOMAIN=' '$DIR/.env' | cut -d= -f2-")"
echo "  TeamUp for $DOMAIN"
# The app answers inside the server even with the website off. Its image has
# no shell tools, so a curl container on the same network asks it.
STATUS="$(remote "cd '$DIR' && n=\$(sudo docker inspect -f '{{range \$k, \$v := .NetworkSettings.Networks}}{{\$k}}{{end}}' \"\$(sudo docker compose ps -q teamup)\") && sudo docker run --rm --network \"\$n\" curlimages/curl:8.11.1 -fs http://teamup:8080/api/wizard/status" 2>/dev/null || true)"
case "$STATUS" in
  *'"complete":false'*) ;;
  *'"complete":true'*) die "$DOMAIN is already set up; this only sets up an empty server" ;;
  *) die "could not ask the app on $HOST whether it is set up; is it running? ($STATUS)" ;;
esac

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
SHORT="$(printf '%s' "$ORG_NAME" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9\n' '-' | sed 's/--*/-/g; s/^-//; s/-$//' | cut -c1-40)"
[ ${#SHORT} -ge 2 ] || SHORT="team"

step "Creating it"
# Over ssh's own encryption into a file only its owner can read, deleted as
# soon as the setup is done. Through the environment, not arguments, which
# anybody on this machine can list.
ORG_NAME="$ORG_NAME" SHORT="$SHORT" OWNER_NAME="$OWNER_NAME" OWNER_EMAIL="$OWNER_EMAIL" OWNER_PASSWORD="$OWNER_PASSWORD" \
  python3 -c 'import json, os; e = os.environ; print(json.dumps({"orgName": e["ORG_NAME"], "orgSubdomain": e["SHORT"], "adminName": e["OWNER_NAME"], "adminEmail": e["OWNER_EMAIL"], "adminPassword": e["OWNER_PASSWORD"]}))' \
  | remote 'umask 077; cat > /tmp/teamup-owner.json'
unset OWNER_PASSWORD again

remote bash -s -- "$DIR" <<'REMOTE_SCRIPT'
set -euo pipefail
trap 'rm -f /tmp/teamup-owner.json; rm -rf /tmp/teamup-setup' EXIT
DIR="$1"
cd "$DIR"
dc() { sudo docker compose "$@"; }

# The website stays off until the organization exists.
dc stop nginx >/dev/null 2>&1 || true
dc up -d --wait postgres redis minio livekit >/dev/null
# Not --wait for the app: until it is set up it has no storage bucket, so it
# rightly says it is not ready. The setup page answering is what counts here.
dc up -d teamup >/dev/null

umask 077
mkdir -p /tmp/teamup-setup
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
chmod 755 /tmp/teamup-setup && chmod 644 /tmp/teamup-setup/*.json
NETWORK="$(sudo docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{end}}' "$(dc ps -q teamup)")"
# Setup asks for the code the server made at start, which it keeps in Redis.
for i in $(seq 1 30); do
  SETUP_CODE="$(dc exec -T redis sh -c 'redis-cli --no-auth-warning GET wizard:setup-token' 2>/dev/null | tr -d '\r')"
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
    echo "  $s done"
  done
  code=$(curl -s -o answer -w "%{http_code}" -b jar -c jar -H "X-Setup-Token: $SETUP_CODE" -X POST http://teamup:8080/api/wizard/step5)
  [ "$code" = 200 ] || { echo "setup step5 answered $code: $(cat answer)"; exit 1; }
  echo "  finished"
'
dc up -d nginx >/dev/null
REMOTE_SCRIPT

step "Checking https://$DOMAIN"
for _ in $(seq 1 30); do
  if curl -fs -o /dev/null --max-time 5 "https://$DOMAIN/healthz"; then break; fi
  sleep 2
done
overall="$(curl -s --max-time 10 "https://$DOMAIN/api/status" | python3 -c 'import json, sys; print(json.load(sys.stdin)["overall"])' 2>/dev/null || echo unknown)"
echo "  status: $overall"
cat <<EOF

Done. Sign in at https://$DOMAIN as $OWNER_EMAIL.
Then, under Settings, add the meeting note-taker's keys and the email settings if you use them.
EOF
