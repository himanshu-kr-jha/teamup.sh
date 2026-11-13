#!/usr/bin/env bash
# Fills a TEST server with video meetings: creates each one as a TeamUp call,
# so the server makes the room and takes LiveKit's webhooks as it would for
# people, then puts simulated participants into it with LiveKit's own load
# tester (lk, https://github.com/livekit/livekit-cli).
#
#   tools/loadtest/calls.sh ROOMS PEOPLE DURATION [QUALITY]
#   e.g.  tools/loadtest/calls.sh 6 8 2m medium
#
# Each meeting has PEOPLE publishing camera and microphone and PEOPLE watching
# a 3x3 gallery, as everybody in a real meeting both sends and watches.
#
# Needs, in the environment: BASE (https://server), LOADTEST_PASSWORD (the
# seeded users' password), LIVEKIT_API_KEY and LIVEKIT_API_SECRET (the test
# server's), and lk on the PATH. Meeting n is created by user load<n>.
set -euo pipefail
ROOMS="$1" PEOPLE="$2" DURATION="$3" QUALITY="${4:-medium}"
DOMAIN="${DOMAIN:-loadtest.example}"
OUT="${OUT:-./calls-$ROOMS-$PEOPLE}"
mkdir -p "$OUT"
: "${BASE:?}" "${LOADTEST_PASSWORD:?}" "${LIVEKIT_API_KEY:?}" "${LIVEKIT_API_SECRET:?}"

rooms=()
for n in $(seq 1 "$ROOMS"); do
  jar="$OUT/jar-$n"
  curl -ks -c "$jar" -H 'Content-Type: application/json' -H "X-Forwarded-For: 10.250.0.$n" \
    --data "$(printf '{"email":"load%d@%s","password":"%s"}' "$n" "$DOMAIN" "$LOADTEST_PASSWORD")" \
    "$BASE/api/auth/login" -o /dev/null
  room="$(curl -ks -b "$jar" -H 'Content-Type: application/json' -H "X-Forwarded-For: 10.250.0.$n" \
    --data "{\"callType\":\"video\",\"title\":\"Load meeting $n\"}" "$BASE/api/calls" \
    | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("call",d); print(d["roomName"])')"
  rooms+=("$room")
done
echo "created ${#rooms[@]} meetings"

ws="wss://${BASE#https://}/livekit"
pids=()
for i in "${!rooms[@]}"; do
  lk --url "$ws" load-test --room "${rooms[$i]}" --duration "$DURATION" \
    --video-publishers "$PEOPLE" --audio-publishers "$PEOPLE" --subscribers "$PEOPLE" \
    --layout 3x3 --video-resolution "$QUALITY" --num-per-second 5 \
    >"$OUT/room-$i.txt" 2>&1 &
  pids+=($!)
done
for p in "${pids[@]}"; do wait "$p" || true; done

# End the meetings, as the last person leaving would.
for n in $(seq 1 "$ROOMS"); do
  id="$(curl -ks -b "$OUT/jar-$n" "$BASE/api/calls" -H "X-Forwarded-For: 10.250.0.$n" \
    | python3 -c 'import json,sys
d=json.load(sys.stdin); c=d.get("calls",d)
print(next((x["id"] for x in c if x.get("status") in ("active","ringing","ongoing")), ""))' 2>/dev/null || true)"
  [ -n "$id" ] && curl -ks -b "$OUT/jar-$n" -X POST -H "X-Forwarded-For: 10.250.0.$n" "$BASE/api/calls/$id/end" -o /dev/null || true
done
echo "results in $OUT"
