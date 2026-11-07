# Services for the real-stack browser specs

`meetings.spec.ts` needs a real LiveKit that calls the server's webhook, and the Go integration tests need the same containers.
`test-services.env` holds the ports, `run-meetings.sh` runs the suite.

```bash
# The frontend is embedded in the binary and fixes LiveKit's address at build time, so source the env first.
. web/e2e/test-services.env && make build
web/e2e/run-meetings.sh          # everything
web/e2e/run-meetings.sh e2e/meetings.spec.ts -g "Knock"
. web/e2e/test-services.env && go test ./...      # same services for Go
```

`go test` clones the database named by `TEAMUP_TEST_DATABASE_URL`, so after adding a migration bring that template up to date, or every database test fails on a missing table:

```bash
. web/e2e/test-services.env && TEAMUP_DATABASE_URL="$TEAMUP_TEST_DATABASE_URL" go run ./cmd/teamup migrate up
```

## The containers

Names are `teamup-cal-test-{pg,redis,minio,livekit}`; do not stop ones you did not start.
Postgres on 55461, Redis 55462, MinIO 55463, LiveKit signalling 55464, ICE over one TCP port (55465) and one UDP port (55466).
Never publish a UDP range.

```bash
docker run -d --name teamup-cal-test-livekit \
  --add-host host.docker.internal:host-gateway \
  -p 127.0.0.1:55464:7880 -p 127.0.0.1:55465:55465/tcp -p 127.0.0.1:55466:55466/udp \
  -e LIVEKIT_CONFIG='
port: 7880
rtc:
  tcp_port: 55465
  udp_port: 55466
  use_external_ip: false
  node_ip: 127.0.0.1
keys:
  "devkey": "devsecret"
webhook:
  api_key: "devkey"
  urls:
    - http://host.docker.internal:55470/api/livekit/webhook
' livekit/livekit-server:latest
```

The webhook URL is the one thing that ties LiveKit to the server under test: the container posts to port **55470** on the host, so the server must listen there (`E2E_PORT`, default 55470 in `test-services.env`).
`run-meetings.sh` refuses to start if the container's config names another port.
If it is wrong anyway, the attendance specs fail with `WEBHOOKS NEVER ARRIVED`, which is a setup fault and not a product bug.
On Linux the container reaches the host through `host.docker.internal:host-gateway`.
