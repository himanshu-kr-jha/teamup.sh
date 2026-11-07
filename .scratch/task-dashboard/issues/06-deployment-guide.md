# Deployment guide for a 50 person team on one VM

Status: resolved
Type: task
Blocked by: 05

Verify docker-compose.prod.yml really comes up, then document VM deployment first and the alternatives, with sizing, TLS, LiveKit ports, backups and upgrades.

## Answer

`docs/DEPLOYMENT.md` is rewritten, leading with one VM on Docker Compose: 4 vCPU, 8 GB, 100 GB SSD.
It was proved first: the secrets, start, backup, restore and upgrade commands were extracted from the guide and run from an empty state, with the wizard, sign-in, `/tasks`, uploads and a two-person call driven from a real browser through nginx over TLS.

The shipped stack could not start on a new machine at all.
Fixed in `Dockerfile`, `.dockerignore`, `docker-compose.prod.yml`, `nginx.conf`, `cmd/teamup/healthcheck.go` and the two workflows:

- The app image on GHCR does not exist, and building one needed Node on the machine. The Dockerfile now builds the SPA itself.
- `minio/minio` no longer exists anywhere upstream. Pinned `cgr.dev/chainguard/minio` by digest.
- Nothing ran migrations, so the server exited on a missing table. Added a `migrate` service that the server waits for.
- `docker compose run teamup migrate up` started the server, because `server` was in the entrypoint.
- The healthcheck called `wget`, which a distroless image does not have. Added `teamup healthcheck`, with a test.
- No service had a restart policy, and nginx refused to start, and kept stale addresses, because it resolved upstreams once.
- Postgres, Redis, MinIO, the app and LiveKit's API were published on all interfaces, past `ufw`.
- `/metrics` answered 200 through nginx, because the connection arrived from a private Docker address. Now 404, along with the readiness endpoints.
- 50000-60000/udp started one proxy per port and nearly exhausted a 15 GB machine. Now one UDP port, 7882.
- The Postgres healthcheck could pass during first-start initialisation. It now checks over TCP.
- All six images are pinned. `TEAMUP_DOMAIN` replaces editing three files.
- `nginx.conf` now limits login attempts, and that was verified.

Measured with 50 simulated people and 2000 tasks: dashboard p50 14.8 ms, p95 22.8 ms, about 300 MiB in total, 26 database connections and no errors.
Real TLS, real renewal, a real Ubuntu VM and calls across networks remain unverified, and the guide says so.

Follow-ups are in ticket 53 (dev setup and CI pull the removed MinIO images) and ticket 54 (no way to recover a forgotten password).
