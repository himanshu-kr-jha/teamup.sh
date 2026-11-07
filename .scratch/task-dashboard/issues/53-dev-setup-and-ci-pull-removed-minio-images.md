# Dev setup and CI pull MinIO images that no longer exist

Status: resolved
Type: bug

MinIO archived the open-source project and removed its images.
Checked on 2026-11-06: `docker pull minio/minio:latest` fails with "pull access denied", `quay.io/minio/minio` answers 401, and `dl.min.io` answers 410 Gone.

Machines that already have the image cached keep working, which hides this.
A fresh clone fails at `./setup.sh`, because `docker-compose.yml` uses `minio/minio:latest`.
`.github/workflows/ci.yml` runs `minio/minio:latest` and `minio/mc:latest`, so a CI run on a clean runner should fail the same way.
This was not run on a clean machine or on GitHub.

`docker-compose.prod.yml` was fixed under ticket 06 by pinning `cgr.dev/chainguard/minio` by digest.
Dev is not a one-line swap, because that image has no shell and no `mc`:

- The compose healthcheck calls `mc ready local`.
- `setup.sh` runs `mc mb` inside the container to create the bucket. `cgr.dev/chainguard/minio-client` exists and could do it from a second container.
- CI runs `minio/mc:latest` for the same reason.

Longer term, MinIO no longer gets upstream security fixes, so whether to stay on it at all is a product decision. See "Object storage" in `docs/DEPLOYMENT.md`.

## Answer

`docker-compose.yml`, `setup.sh`, `reset.sh` and `.github/workflows/ci.yml` use the same `cgr.dev/chainguard/minio` digest as `docker-compose.prod.yml`.
`reset.sh` was not on the ticket, but it ran `mc` inside the container and would have stopped working.

Health check: removed, the same as production, since the image has nothing to run one with.
Services that waited for `service_healthy` now wait for `service_started`, and `setup.sh` and `start.sh` already accept a running container that has no health check.
Bucket: `setup.sh`, `reset.sh` and CI run a one-shot `cgr.dev/chainguard/minio-client` container, pinned by digest, configured by `MC_HOST_<alias>` because it has no shell either.
`setup.sh` and `reset.sh` share MinIO's network with `--network container:<id>`.
`setup.sh` retries for 30 seconds, because there is no health check to wait for.
With `TEAMUP_AUTO_SETUP`, the server still creates the bucket itself at boot.

Two other defects stood in the way and are fixed, one line each:
- `setup.sh` could hand LiveKit's TCP port 7882, which compose always publishes, so setup failed with "port is already allocated" on any machine with 7880 and 7881 taken. 7882 now starts out claimed.
- The dev compose admin password `admin123456` was 11 characters, the minimum is 12, so the `teamup` service died at boot with "Auto-setup failed".

Run for real, in a temporary copy of the tree with no `.env`, compose project `teamup53test`, and the ports `setup.sh` chose (8081, 5433, 6380, 9001, 9002, 7881, 7883):
- `./setup.sh` exited 0, created the bucket, built, and migrated, and `/readyz` then answered 200 with `minio: pass`.
- `docker compose up -d --build teamup` with MinIO stopped first, so both started together: the bucket was created by auto-setup and `/readyz` answered 200.
- `./reset.sh --yes` removed a bucket with a file in it.
- The CI commands against a scratch container on another port: `mb` succeeded, and the live object storage tests in `internal/files` passed against this image.
- The repo's `.env` was not touched.
Everything started was removed, and the `teamup-dev-test-*` and unrelated containers stayed up.

Not verified:
- CI itself cannot run here.
`.github/workflows/ci.yml` was reviewed by hand, and only its commands were run locally.
- `livekit-egress` was not started.
Compose validated it, but its image was not pulled and recording was not tested.

Left alone:
- `pnpm` on this machine resolves to 11.20.0, and with it `make build` fails in `web/` with `ERR_PNPM_IGNORED_BUILDS` for esbuild, and it writes a `web/pnpm-workspace.yaml`.
The setup run above used pnpm 9.
A new machine with pnpm 11 should hit this at `./setup.sh`.
Nothing pins the version: `web/package.json` has no `packageManager` field.
- The dev compose `teamup` service does not migrate, so `docker compose up teamup` on an empty database fails with `relation "signing_keys" does not exist`.
It works after `./setup.sh`, which migrates from the host.
- Whether to stay on MinIO at all, which the ticket calls a product decision.
