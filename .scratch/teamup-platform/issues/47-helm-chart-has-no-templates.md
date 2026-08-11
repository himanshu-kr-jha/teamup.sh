# 47 — Give the Helm chart the templates it needs to deploy anything

**What to build:** `helm/teamup` renders zero Kubernetes resources. Add the templates so `helm install` actually deploys TeamUp.

**Blocked by:** None — can start immediately

**Status:** completed

- [x] Deployment for the TeamUp binary (image, replicas, probes against `/healthz` and `/readyz`, resources)
- [x] Service + optional Ingress
- [x] ConfigMap / Secret wiring for every `TEAMUP_*` variable the server requires at boot
- [x] `_helpers.tpl` with the usual name/label/selector helpers
- [x] NOTES.txt
- [x] Postgres / Redis / MinIO / LiveKit as dependencies or documented prerequisites — **documented prerequisites**, justified in the comments below
- [x] `helm template` renders valid manifests; `helm lint --strict` passes
- [x] `helm template` output validated against a real Kubernetes schema (kubeconform 0.6.7)

**Type:** task

## Evidence

The chart directory contains exactly two files and no `templates/` directory:

```
$ find helm -type f
helm/teamup/Chart.yaml
helm/teamup/values.yaml
$ ls helm/teamup/templates/
ls: cannot access 'helm/teamup/templates/': No such file or directory
```

Helm treats a chart with no `templates/` as valid, so this **passes `helm lint` and packages cleanly** — `make helm` succeeds and produces a `.tgz`. That is why it went unnoticed. Installing it creates no Deployment, no Service, no ConfigMap: nothing runs.

This contradicts the destination in `map.md` ("deployable as single Go binary, Docker Compose, **or Helm chart**") and decision 08, whose ticket `08-cicd-github-actions.md` specifies seven templates plus subcharts. `docs/DEPLOYMENT.md` should be checked for instructions that cannot work.

Scale of the gap: `values.yaml` exists and describes configuration that nothing consumes, so the chart currently reads as complete to anyone who opens it.

### Failing check to write first

```
helm template teamup helm/teamup | grep -c 'kind:'
```

Returns 0 today. Any real chart returns several. Wire this into CI alongside `helm lint` — lint alone is what let an empty chart look healthy.

### Note for whoever picks this up

The required boot configuration is not guesswork — the server hard-fails at startup listing every missing variable. Run it with an empty environment to get the authoritative list. Ticket 35 also makes `TEAMUP_AUTH_SECRET` load-bearing (it wraps the persisted JWT signing key), so the chart must surface it as a Secret and document that losing it invalidates every session.

## Comments

### RED, reproduced before anything was written

```
$ helm template teamup helm/teamup | grep -c 'kind:'
0
$ helm lint --strict helm/teamup
==> Linting helm/teamup
[INFO] Chart.yaml: icon is recommended

1 chart(s) linted, 0 chart(s) failed
```

Both halves of the ticket confirmed: zero resources, and `--strict` lint clean on a chart that deploys nothing.

GREEN: `helm template teamup helm/teamup | grep -c 'kind:'` now returns **6** with defaults (ServiceAccount, Secret, ConfigMap, Service, Deployment, migration Job) and **10** with everything enabled.

### The authoritative required-variable list

From the binary, not from `values.yaml`:

```
$ env -i ./teamup server
Error: config parse: env: required environment variable "TEAMUP_DATABASE_URL" is not set;
  ... "TEAMUP_REDIS_URL" ... "TEAMUP_MINIO_ENDPOINT" ... "TEAMUP_MINIO_ACCESS_KEY" ...
  "TEAMUP_MINIO_SECRET_KEY" ... "TEAMUP_MINIO_BUCKET" ... "TEAMUP_LIVEKIT_URL" ...
  "TEAMUP_LIVEKIT_API_KEY" ... "TEAMUP_LIVEKIT_API_SECRET" is not set
```

Nine required variables. The old `values.yaml` did not describe them: it modelled the database as `config.database.{host,port,name,user,sslmode,maxConns}`, but `cmd/teamup/main.go:118` reads a single `TEAMUP_DATABASE_URL` DSN, and `maxConns`/`sslmode` are read by nothing at all. Same for Redis. Every key in the rewritten `values.yaml` is consumed by a template.

### Empty is not the same as unset, and that decided the Secret design

`cmd/teamup/main.go` parses config with `caarlos0/env`, whose `required` tag checks only whether the variable is *present*. Verified:

```
$ env -i TEAMUP_DATABASE_URL= TEAMUP_REDIS_URL= ./teamup server
Error: config parse: env: required environment variable "TEAMUP_MINIO_ENDPOINT" is not set; ...
```

The two empty-but-set variables pass the check and the server goes on to fail later, obscurely, on the first query. So `templates/secret.yaml` **omits** keys whose value is empty rather than writing `""`. A misconfigured release then produces the binary's own diagnostic in `kubectl logs`, naming exactly what is missing.

Verified end to end: rendering the chart with defaults, extracting the `envFrom` ConfigMap + Secret exactly as kubelet would, and running the binary with that environment reproduces the boot error listing precisely the six variables NOTES.txt warns about.

### Position on Postgres / Redis / MinIO / LiveKit: prerequisites, not subcharts

Ticket 08 sketches four vendored subcharts. Not done, deliberately, and `values.yaml` no longer pretends otherwise (the `postgresql:`/`redis:`/`minio:`/`livekit:` blocks with `enabled: true` and `changeme` passwords are gone — they configured nothing).

Reasons, in order of weight:

1. **LiveKit is the one that would actually break.** It needs a wide UDP port range and a routable external address to carry media. A generic subchart gets that wrong more often than right, and the failure mode is the worst kind: calls connect, then carry no audio. Getting it right is cluster-specific work no chart default can do.
2. **A bundled database is the thing nobody can safely `helm uninstall`.** The server takes DSNs and endpoints, so pointing at RDS/ElastiCache/S3 or an operator-managed instance is a one-line value either way.
3. **Vendored subcharts make `helm lint`, `helm template` and CI depend on upstream chart repositories.** Bitnami's chart/image catalog has moved under them once already; a chart that cannot render offline cannot be schema-checked in CI, which is how this ticket happened.

The prerequisites are spelled out at the bottom of `values.yaml` and repeated in NOTES.txt. Worth knowing: **the MinIO bucket must already exist.** The server does not create it at boot — only the setup wizard does (`internal/wizard/wizard.go:315`).

Schema creation *is* handled: `templates/migration-job.yaml` runs `teamup migrate up` as a `pre-install,pre-upgrade` hook. Without it a fresh install starts a server against an empty database.

### Probes, and the readiness signal we know is incomplete

`/healthz` → liveness, `/readyz` → readiness, plus a `startupProbe` on `/healthz` (30 × 2s) so a slow boot is not killed by liveness.

`observability.ReadyHandler` pings PostgreSQL and Redis only, and applies a 2s internal timeout — readiness `timeoutSeconds` is set to 3 so the probe can actually observe the negative answer rather than timing out first.

**Interaction with ticket 39 (`/readyz` must cover MinIO and LiveKit).** Until 39 lands, a pod whose object storage or LiveKit is unreachable reports ready and takes traffic: uploads and calls fail while a rollout goes green. The chart is therefore built *not* to lean on readiness as a dependency gate — there is no init container waiting on `/readyz`, and no PDB or rollout gate keyed to it. NOTES.txt states the limitation to the operator in as many words. When 39 lands, no chart change is needed; readiness simply becomes truthful.

Liveness deliberately stays on `/healthz`. Pointing liveness at `/readyz` would restart every pod in the fleet during a Postgres blip instead of just removing them from the Service.

### TEAMUP_AUTH_SECRET

- Lives in a Secret (`secrets.authSecret`, or any key in a `secrets.existingSecret` you manage), never the ConfigMap.
- No default, and no `randAlphaNum` generation. A generated value looks convenient and is a trap here: GitOps tools render with `helm template`, where `lookup` returns nil, so the secret would rotate on every sync and log the entire company out each time. Better to make the operator produce one.
- NOTES.txt says plainly that losing *or changing* it invalidates every session and refresh token, that it must be backed up, and that all replicas must carry the identical value.
- When it is unset, NOTES.txt prints a standalone warning telling you to set it *before anyone signs in*.

`replicaCount > 1` is a supported path: no local state, no leader election, no `ReadWriteOnce` volume; the only shared secret is delivered by the one Secret every replica reads. Rendered and schema-checked at `replicaCount=3` and with the HPA enabled.

### Two things found while wiring this up, not fixed here

1. **`/metrics` is unauthenticated** (`cmd/teamup/main.go:457`, and it is on the auth-skip list at :562). With `ingress.enabled=true` and the default `path: /`, it is served on the public host. Confirmed against the running binary: `GET /metrics` → 200 with no credentials. The chart cannot fix this without controller-specific snippets, so NOTES.txt warns whenever an ingress path of `/` is published. Worth its own ticket.
2. **WebSocket idle timeouts.** The default ingress annotations now include `proxy-read-timeout`/`proxy-send-timeout` of 3600s. nginx's 60s default silently tears down TeamUp's long-lived WebSocket once a minute, which presents as "the app randomly reconnects".

### CI step (describe-only — `.github/workflows/ci.yml` is not in this worktree)

Add to the chart job, after `helm lint`:

```yaml
      - name: Helm chart renders resources
        run: |
          set -euo pipefail
          helm lint --strict helm/teamup
          # An empty chart lints clean. Count what it actually produces.
          for args in \
            "" \
            "--set replicaCount=3" \
            "--set ingress.enabled=true" \
            "--set ingress.enabled=false" \
            "--set autoscaling.enabled=true" \
            "--set monitoring.serviceMonitor.enabled=true" \
            "--set secrets.existingSecret=external"
          do
            n=$(helm template teamup helm/teamup $args | grep -c '^kind:')
            echo "$n resources [$args]"
            [ "$n" -gt 0 ] || { echo "chart rendered nothing"; exit 1; }
          done

      - name: Validate rendered manifests against Kubernetes schemas
        run: |
          curl -sSL https://github.com/yannh/kubeconform/releases/download/v0.6.7/kubeconform-linux-amd64.tar.gz \
            | tar -xz -C /usr/local/bin kubeconform
          for v in 1.27.0 1.29.0 1.31.0; do
            helm template teamup helm/teamup --set ingress.enabled=true --set autoscaling.enabled=true \
                                             --set monitoring.serviceMonitor.enabled=true \
              | kubeconform -strict -summary -kubernetes-version "$v" \
                  -schema-location default \
                  -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' -
          done
```

The second step is the one that matters. `helm lint` passing is exactly what let an empty chart look healthy for this long.

### Verification performed

- `helm lint --strict helm/teamup` — clean (only the pre-existing `[INFO] icon is recommended`), with defaults and with a full override file.
- `helm template` × 17 value combinations × 3 Kubernetes versions (1.27, 1.29, 1.31) = **51 renders, all valid** under `kubeconform -strict`, ServiceMonitor included (real CRD schema from the datree catalog, not `-ignore-missing-schemas`). Combinations covered: defaults, `replicaCount=3`, ingress on/off/no-className/with-TLS, `existingSecret`, migrations off, `serviceAccount.create=false`, autoscaling, ServiceMonitor, name/fullname overrides, probes nulled, resources nulled, non-default ports, and an everything-on file.
- `helm package` → renders 6 resources from the packaged `.tgz` (so `.helmignore` is not eating `templates/`).
- The binary booted successfully against the environment the chart produces, extracted from the rendered ConfigMap and Secret the way kubelet builds `envFrom`, pointed at local Postgres/Redis/MinIO/LiveKit: `GET /healthz` → 200, `GET /readyz` → `{"status":"ready"}` 200.
- NOTES.txt was rendered (via `tpl` on a copy, since `helm install --dry-run` needs a cluster) with defaults and with overrides; the missing-variable list it prints matches the binary's own error exactly.

**No live install was performed — there is no Kubernetes cluster in this environment.** Everything above is rendering, schema validation, and running the real binary on the chart's own environment. Scheduling, image pull, probe behaviour under kubelet, hook ordering and the Ingress controller are unverified.

### Left alone

- `docs/DEPLOYMENT.md` — checked as the ticket asks. It never mentions Helm or Kubernetes (binary, Docker Compose and EC2 only), so there are no broken instructions to correct. It has no Helm section either; adding one is worth doing but is not this ticket.
- `values.schema.json` (ticket 08 lists it) — not added. It would catch typo'd keys, but it is a second source of truth for the value shape and belongs in its own change.
- `PodDisruptionBudget`, `NetworkPolicy`, `PrometheusRule` — out of scope. `monitoring.prometheusRule` was dropped from `values.yaml` rather than left describing rules that do not exist.
- No `preStop` hook. The distroless image has no shell for the usual `sleep`, and `lifecycle.sleep` needs Kubernetes 1.30+. `terminationGracePeriodSeconds: 40` covers the server's 30s drain.
- Nothing under `internal/`, `cmd/` or `pkg/` was touched.
