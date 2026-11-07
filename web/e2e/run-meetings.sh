#!/usr/bin/env bash
# The whole Playwright suite, including meetings.spec.ts, against the real
# stack: real Postgres, Redis, MinIO, LiveKit and a real server.
#
#   web/e2e/run-meetings.sh [playwright args...]     e.g.  -g "Knock"
#
# Differences from run.sh: it sources test-services.env (ports of the
# teamup-cal-test-* containers) and checks, before starting, that LiveKit will
# post its webhooks to the port the server will listen on. Without that the
# attendance tests cannot pass, and they say "WEBHOOKS NEVER ARRIVED" rather
# than blaming the product. Needs a rebuilt frontend and binary:
#   make build
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/test-services.env"

if command -v docker >/dev/null 2>&1 && docker inspect "$E2E_LIVEKIT_CONTAINER" >/dev/null 2>&1; then
  cfg="$(docker inspect "$E2E_LIVEKIT_CONTAINER" --format '{{range .Config.Env}}{{println .}}{{end}}')"
  want=":${E2E_PORT}/api/livekit/webhook"
  if ! grep -q -- "$want" <<<"$cfg"; then
    echo "LiveKit container $E2E_LIVEKIT_CONTAINER does not post webhooks to *$want." >&2
    echo "Its webhook config is:" >&2
    grep -A3 'webhook:' <<<"$cfg" >&2 || echo "  (none)" >&2
    echo "Recreate it as in web/e2e/README-services.md, or set E2E_PORT to the port in that URL." >&2
    exit 1
  fi
else
  echo "note: cannot inspect LiveKit container '$E2E_LIVEKIT_CONTAINER'; assuming its webhook posts to :$E2E_PORT." >&2
fi

ROOT="$(cd "$HERE/../.." && pwd)"
if [ ! -x "$ROOT/bin/teamup" ]; then echo "no bin/teamup: run  . web/e2e/test-services.env && make build" >&2; exit 1; fi
# The frontend is embedded in the binary and learns where LiveKit is when it is BUILT.
if ! grep -rqF -- "$VITE_LIVEKIT_URL" "$ROOT/cmd/teamup/web/dist/assets" 2>/dev/null; then
  echo "The embedded frontend does not point at $VITE_LIVEKIT_URL, so browsers cannot reach LiveKit." >&2
  echo "Rebuild it:  . web/e2e/test-services.env && make build" >&2
  exit 1
fi
stale="$(find "$ROOT/web/src" "$ROOT/internal" "$ROOT/cmd" "$ROOT/pkg" "$ROOT/migrations" -type f \( -name '*.go' -o -name '*.ts' -o -name '*.tsx' -o -name '*.css' -o -name '*.sql' \) -not -path '*/web/dist/*' -newer "$ROOT/bin/teamup" -print -quit)"
if [ -n "$stale" ]; then
  echo "bin/teamup is older than $stale: you would be testing an old build." >&2
  echo "Rebuild it:  . web/e2e/test-services.env && make build" >&2
  exit 1
fi

exec "$HERE/run.sh" "$@"
