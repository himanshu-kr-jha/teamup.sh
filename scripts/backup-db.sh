#!/bin/sh
#
# The nightly backup, run on the server from its TeamUp directory: by cron
# (through ./backup.sh, which scripts/new-server.sh writes) and by
# scripts/deploy.sh before every upgrade.
#
# On the server's disk, in backups/: the database, the .env it needs (its
# AUTH_SECRET decrypts what the database holds), and the MinIO files when the
# files live on this server. Kept 14 days.
#
# When the files live in an outside bucket (FILES_BUCKET in .env), the database
# also goes to that bucket as one object, _backups/teamup-db.dump, written over
# each night. With versioning on, the bucket keeps every night's copy behind
# the latest one, and its lifecycle rule decides how long; scripts/db-backups.sh
# lists them and fetches any of them.
set -eu
# Everything here holds every message and the server's secrets.
umask 077
cd "$(dirname "$0")/.."
set -a; . ./.env; set +a

mkdir -p backups
D=$(date +%F)
DUMP="backups/postgres-$D.dump"
docker compose exec -T postgres pg_dump -U teamup -Fc teamup > "$DUMP.part" </dev/null
mv "$DUMP.part" "$DUMP"
echo "$(date '+%F %T %Z') database: $DUMP ($(du -h "$DUMP" | cut -f1))"

if [ "${MINIO_REPLICAS:-1}" != 0 ]; then
  docker compose stop minio
  docker run --rm -v teamup_minio-data:/data:ro -v "$PWD/backups":/backup alpine:3.20 \
    tar czf "/backup/minio-$D.tar.gz" -C /data .
  docker compose start minio
fi
cp .env "backups/env-$D"
find backups -type f -mtime +14 -delete

if [ -n "${FILES_BUCKET:-}" ]; then
  URL="https://$FILES_ENDPOINT/$FILES_BUCKET/_backups/teamup-db.dump"
  # The key goes to curl on stdin, not as an argument anybody could list.
  # S3 checks the upload against this checksum and refuses a damaged one.
  code=$(printf 'user = "%s:%s"\n' "$FILES_ACCESS_KEY" "$FILES_SECRET_KEY" |
    curl -K - -s --max-time 900 -o /tmp/teamup-backup-upload.out -w '%{http_code}' \
      --aws-sigv4 "aws:amz:$FILES_REGION:s3" \
      -H "x-amz-content-sha256: $(sha256sum "$DUMP" | cut -d' ' -f1)" \
      -H 'Content-Type: application/octet-stream' \
      -T "$DUMP" "$URL" || true)
  if [ "$code" = 200 ]; then
    echo "$(date '+%F %T %Z') bucket: $FILES_BUCKET/_backups/teamup-db.dump"
  else
    echo "$(date '+%F %T %Z') bucket upload FAILED (answered ${code:-nothing}):" >&2
    cat /tmp/teamup-backup-upload.out >&2 2>/dev/null || true
    rm -f /tmp/teamup-backup-upload.out
    exit 1
  fi
  rm -f /tmp/teamup-backup-upload.out
fi
