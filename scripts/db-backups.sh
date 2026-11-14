#!/usr/bin/env bash
#
# Lists and fetches the nightly database backups a server keeps in its files
# bucket (scripts/backup-db.sh writes them), from this machine.
#
#   scripts/db-backups.sh list                      every night's copy, newest first
#   scripts/db-backups.sh get                       the latest
#   scripts/db-backups.sh get --days-ago 7          the last one made 7 days ago
#   scripts/db-backups.sh get --date 2026-11-14     the last one made that day
#   scripts/db-backups.sh get --version <id>        exactly that copy (from list)
#
# Options: --env FILE names the server (default .deploy.env); -o FILE is where
# get saves (default teamup-db-<date>.dump here). Days are the server's own.
#
# The bucket keeps one object, _backups/teamup-db.dump, and versioning keeps
# each night's copy behind the latest, for as long as its lifecycle rule says.
# The bucket's key never leaves the server: the server asks the bucket, and the
# copy comes back over ssh.
#
# Restoring one: docs/DEPLOYMENT.md, under "Backups".
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

die() { echo "db-backups: $*" >&2; exit 1; }

CMD="${1:-}"; [ $# -gt 0 ] && shift
ENV_FILE=".deploy.env" PICK="latest" VALUE="" OUT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --env) ENV_FILE="$2"; shift 2 ;;
    --days-ago) PICK=days; VALUE="$2"; shift 2 ;;
    --date) PICK=date; VALUE="$2"; shift 2 ;;
    --version) PICK=version; VALUE="$2"; shift 2 ;;
    -o) OUT="$2"; shift 2 ;;
    -h|--help) awk 'NR==1{next} /^#/{sub(/^#[[:space:]]?/,""); print; next} {exit}' "$0"; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
done
case "$CMD" in list|get) ;; *) die "say list or get (try --help)" ;; esac
case "$PICK" in
  days) case "$VALUE" in ''|*[!0-9]*) die "--days-ago takes a number of days" ;; esac ;;
  date) case "$VALUE" in [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]) ;; *) die "--date takes YYYY-MM-DD" ;; esac ;;
  version) case "$VALUE" in ''|*[!A-Za-z0-9._-]*) die "that is not a version id from list" ;; esac ;;
esac

[ -f "$ENV_FILE" ] || die "no such file: $ENV_FILE"
# shellcheck source=/dev/null
. "./${ENV_FILE#./}"
HOST="${TEAMUP_DEPLOY_HOST:?$ENV_FILE does not say TEAMUP_DEPLOY_HOST}"
DIR="${TEAMUP_DEPLOY_DIR:-/opt/teamup}"
SSH=(ssh -o ConnectTimeout=20)
if [ "${TEAMUP_DEPLOY_IPV4:-0}" = 1 ]; then SSH+=(-4); fi
if [ -n "${TEAMUP_DEPLOY_KEY:-}" ]; then SSH+=(-i "$TEAMUP_DEPLOY_KEY"); fi

# Runs on the server: lists the versions, picks one, and for get writes its
# date on stderr's first line and its bytes on stdout.
SERVER=$(cat <<'SERVER_SCRIPT'
set -eu
cd "$1"; CMD="$2"; PICK="$3"; VALUE="$4"
set -a; . ./.env; set +a
[ -n "${FILES_BUCKET:-}" ] || { echo "this server keeps its files on its own disk, so its backups are in $1/backups" >&2; exit 3; }
s3() { printf "user = \"%s:%s\"\n" "$FILES_ACCESS_KEY" "$FILES_SECRET_KEY" | curl -K - -sS --aws-sigv4 "aws:amz:$FILES_REGION:s3" "$@"; }
BASE="https://$FILES_ENDPOINT/$FILES_BUCKET"
s3 "$BASE/?versions&prefix=_backups/teamup-db.dump" > /tmp/teamup-backups.xml
CHOSEN=$(python3 - "$CMD" "$PICK" "$VALUE" /tmp/teamup-backups.xml <<"PY"
import sys, datetime, xml.etree.ElementTree as ET
cmd, pick, value, path = sys.argv[1:]
root = ET.parse(path).getroot()
ns = root.tag.split("}")[0] + "}" if root.tag.startswith("{") else ""
if root.tag.endswith("Error"):
    sys.exit("the bucket refused: " + (root.findtext("Code") or "") + ": " + (root.findtext("Message") or ""))
rows = []
for v in root.iter(ns + "Version"):
    if v.findtext(ns + "Key") != "_backups/teamup-db.dump":
        continue
    when = datetime.datetime.fromisoformat(v.findtext(ns + "LastModified").replace("Z", "+00:00")).astimezone()
    rows.append((when, v.findtext(ns + "VersionId"), int(v.findtext(ns + "Size")), v.findtext(ns + "IsLatest") == "true"))
rows.sort(reverse=True)
if not rows:
    sys.exit("no backups in the bucket yet")
if cmd == "list":
    print(f"{'made':<22} {'size':>9}  version", file=sys.stderr)
    for when, vid, size, latest in rows:
        print(f"{when:%Y-%m-%d %H:%M %Z}  {size/1e6:7.1f} MB  {vid}{'  (latest)' if latest else ''}", file=sys.stderr)
    print(f"{len(rows)} copies, from {rows[-1][0]:%Y-%m-%d} to {rows[0][0]:%Y-%m-%d}", file=sys.stderr)
    sys.exit(0)
if pick == "latest":
    hit = rows[0]
elif pick == "version":
    hit = next((r for r in rows if r[1] == value), None)
else:
    day = (datetime.date.today() - datetime.timedelta(days=int(value))) if pick == "days" else datetime.date.fromisoformat(value)
    hit = next((r for r in rows if r[0].date() == day), None)
    if hit is None:
        sys.exit(f"no backup made on {day}; the copies run from {rows[-1][0]:%Y-%m-%d} to {rows[0][0]:%Y-%m-%d} (see list)")
if hit is None:
    sys.exit("no such version (see list)")
print(f"{hit[0]:%Y-%m-%d} {hit[1]} {hit[2]} {hit[0]:%Y-%m-%d %H:%M %Z}")
PY
)
rm -f /tmp/teamup-backups.xml
[ "$CMD" = get ] || exit 0
echo "$CHOSEN" >&2
s3 -f "$BASE/_backups/teamup-db.dump?versionId=$(echo "$CHOSEN" | cut -d" " -f2)"
SERVER_SCRIPT
)

if [ "$CMD" = list ]; then
  "${SSH[@]}" "$HOST" "bash -s -- $(printf '%q ' "$DIR" list "$PICK" "$VALUE")" <<<"$SERVER"
  exit
fi

TMP="$(mktemp "${TMPDIR:-/tmp}/teamup-db.XXXXXX")"
INFO="$(mktemp "${TMPDIR:-/tmp}/teamup-db-info.XXXXXX")"
trap 'rm -f "$TMP" "$INFO"' EXIT
chmod 600 "$TMP"
"${SSH[@]}" "$HOST" "bash -s -- $(printf '%q ' "$DIR" get "$PICK" "$VALUE")" <<<"$SERVER" >"$TMP" 2>"$INFO" \
  || { cat "$INFO" >&2; die "could not fetch the backup"; }
read -r DAY VERSION SIZE MADE < <(tail -1 "$INFO")
[ "$(stat -c %s "$TMP" 2>/dev/null || stat -f %z "$TMP")" = "$SIZE" ] || die "the copy arrived incomplete; try again"
OUT="${OUT:-teamup-db-$DAY.dump}"
[ ! -e "$OUT" ] || die "$OUT exists already; choose another with -o"
(umask 077; mv "$TMP" "$OUT")
echo "Saved $OUT: the backup made $MADE ($((SIZE / 1000)) kB, version $VERSION)."
echo "It holds every message and account in the organization; keep it somewhere safe."
