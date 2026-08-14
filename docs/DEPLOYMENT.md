# Running TeamUp somewhere other than your own machine

There is one thing to understand before anything else, because it decides how
much work each option is.

## Why HTTPS decides what works

Browsers grant some abilities only to a "secure context": HTTPS, or `localhost`.
Three things TeamUp does fall under that rule.

| Needs a secure context | What happens without one |
| --- | --- |
| Cookies marked `Secure` | The browser silently discards the session cookie. Sign-in appears to succeed and you land back on the login page. |
| Camera and microphone | Calls cannot start; `getUserMedia` is refused outright. |
| Desktop notifications | The Notification API is unavailable. |

`localhost` is exempt, which is why everything works on the machine running it
and stops working the moment you open `http://192.168.x.x:8080` on a phone.

So there are two shapes of deployment:

- **Plain HTTP on a local network** — quick, no certificates, but no calls and no
  desktop notifications. Fine for testing chat, tasks, files, presence and
  search.
- **HTTPS** — everything works. Needed on EC2 anyway.

---

## On your own network, for a phone or a second laptop

The short way — one command, which works out the address and sets everything
that has to be set together:

```bash
./start.sh --lan
```

It prints the address to open on the phone, and says plainly what will not work.
The rest of this section is what that command does, for when you want to do it
by hand.

Find this machine's address on the network:

```bash
ip -4 route get 1.1.1.1 | awk '{print $7; exit}'    # Linux
ipconfig getifaddr en0                              # macOS
```

Say it is `192.168.0.121`. Start the infrastructure, then the server:

```bash
./start.sh    # once, to bring up postgres, redis, minio, livekit
```

Stop it (Ctrl-C leaves the containers running) and start the server with three
settings:

```bash
set -a; . ./.env; set +a

export TEAMUP_BASE_URL="http://192.168.0.121:8080"
export TEAMUP_INSECURE_COOKIES=true      # see the warning below

./bin/teamup server
```

Then open `http://192.168.0.121:8080` on the phone. It must be on the same
network — the same Wi-Fi, not mobile data.

**`TEAMUP_INSECURE_COOKIES=true` drops the `Secure` flag from the session
cookie.** That is the only way a browser will keep it over plain HTTP. It also
means the session token travels in clear text over your network, so use it on a
network you trust and nowhere else. It defaults to off; nothing turns it on for
you.

If the firewall blocks the port:

```bash
sudo ufw allow 8080/tcp        # Ubuntu
```

### If you want calls to work on the local network too

Calls need HTTPS, and they need LiveKit to advertise an address the phone can
reach. By default LiveKit asks a public STUN server for its address and gets the
router's public IP, which nothing on the LAN can route to. Point it at the LAN
address instead:

```bash
export TEAMUP_LIVEKIT_USE_EXTERNAL_IP=false
export TEAMUP_LIVEKIT_NODE_IP=192.168.0.121
docker compose up -d livekit
```

For the HTTPS half, the least painful route is a tunnel. It hands you a real
certificate without touching your network, your router, or DNS.

```bash
# Once, to install it
curl -fL -o cloudflared \
  https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64
chmod +x cloudflared && sudo mv cloudflared /usr/local/bin/
```

On macOS, `brew install cloudflared` instead.

Then, with TeamUp already running on port 8080, in a second terminal:

```bash
cloudflared tunnel --url http://localhost:8080
```

It prints an address like `https://some-words-here.trycloudflare.com`. No
Cloudflare account is needed for this; the address is temporary and changes each
time you start it.

Restart TeamUp pointed at that address:

```bash
set -a; . ./.env; set +a
export TEAMUP_BASE_URL="https://some-words-here.trycloudflare.com"
./bin/teamup server
```

Leave `TEAMUP_INSECURE_COOKIES` **off** — the tunnel is HTTPS, so the cookie
should stay Secure. Then open that address on the phone. It does not need to be
on your Wi-Fi any more; the tunnel reaches it from anywhere.

Everything works this way, calls included: the app proxies LiveKit's signalling
socket at `/livekit`, so it travels through the same tunnel and inherits its
certificate.

Two things to know. The address is public while the tunnel is open — anybody
who has it can reach your login page — so stop the tunnel when you are done. And
media still needs a route between the two devices; if a call connects but nobody
can hear anything, `TEAMUP_LIVEKIT_NODE_IP` is likely still pointing somewhere
the other side cannot reach.

---

## On EC2

### The instance

- **Type**: `t3.small` is enough for a handful of people. Postgres, Redis, MinIO
  and LiveKit all run alongside the app, and the frontend build wants about 2 GB
  of memory. `t3.micro` will run out during `npm run build`.
- **Disk**: 20 GB is comfortable; attachments and recordings land in MinIO on the
  same disk.
- **Security group**:

  | Port | Protocol | Why |
  | --- | --- | --- |
  | 22 | TCP | ssh |
  | 80 | TCP | certificate issue and the redirect to HTTPS |
  | 443 | TCP | the app |
  | 7881 | TCP | LiveKit media fallback when UDP is blocked |
  | 50000–60000 | UDP | LiveKit media |

  The UDP range is what makes calls work. Without it a call connects and then
  nobody can hear anybody.

### Setting it up

```bash
sudo apt update && sudo apt install -y docker.io docker-compose-plugin git
sudo usermod -aG docker $USER && newgrp docker

git clone <your repository> teamup && cd teamup
cp .env.example .env
```

Edit `.env`. What matters:

```ini
TEAMUP_BASE_URL=https://teamup.yourdomain.com
TEAMUP_ENV=production

# Encrypts the key TeamUp signs tokens with. The value in .env.example is a
# published placeholder — anyone who has read the repository could mint valid
# tokens against it. Generate your own:  openssl rand -hex 32
TEAMUP_AUTH_SECRET=<64 random hex characters>

# Change every one of these from the defaults.
TEAMUP_DATABASE_URL=postgres://teamup:<a real password>@postgres:5432/teamup?sslmode=disable
TEAMUP_MINIO_ACCESS_KEY=<not minioadmin>
TEAMUP_MINIO_SECRET_KEY=<not minioadmin>
# The address the BROWSER uses for object storage — see below. Not minio:9000.
TEAMUP_MINIO_ENDPOINT=teamup.yourdomain.com
TEAMUP_LIVEKIT_API_KEY=<not devkey>
TEAMUP_LIVEKIT_API_SECRET=<not devsecret>

# So new members are actually sent their password.
TEAMUP_SMTP_HOST=...
TEAMUP_SMTP_PORT=587
TEAMUP_SMTP_USER=...
TEAMUP_SMTP_PASSWORD=...
TEAMUP_SMTP_FROM=teamup@yourdomain.com
```

Leave `TEAMUP_INSECURE_COOKIES` unset. On HTTPS it is not needed and turning it
on would send session tokens in clear.

**Back up `TEAMUP_AUTH_SECRET` somewhere other than the server.** It decrypts
the signing key held in the database; if you lose it, that key cannot be read
back, every session ends at once, and a new key has to be generated. Restoring
a database backup without the matching secret does not get you back.

**Create the object storage bucket before first boot.** Nothing in the server's
startup path creates it, so without it `/readyz` reports MinIO unhealthy and
every upload fails. `./setup.sh` does this for a local install; for a production
one, create `TEAMUP_MINIO_BUCKET` by hand in your object store.

### The certificate

Point an A record at the instance's public IP, then:

```bash
sudo apt install -y certbot
sudo certbot certonly --standalone -d teamup.yourdomain.com
mkdir -p certs
sudo cp /etc/letsencrypt/live/teamup.yourdomain.com/fullchain.pem certs/tls.crt
sudo cp /etc/letsencrypt/live/teamup.yourdomain.com/privkey.pem  certs/tls.key
sudo chown $USER certs/*
```

Replace `teamup.example.com` with your own name in three places:

| File | What |
| --- | --- |
| `nginx.conf` | `server_name`, twice — the redirect and the TLS server |
| `docker-compose.prod.yml` | `TEAMUP_MINIO_ENDPOINT`, `TEAMUP_BASE_URL`, and the nginx service's network `aliases` entry |

The alias is what lets the other containers resolve your public name from
inside the compose network, so LiveKit's recording upload does not have to
leave the host and come back through NAT to reach object storage. Use a real
certificate rather than a self-signed one: those containers verify it.

### Object storage has to be reachable from the browser

Uploads and inline images do not pass through the app. The server signs a URL
and the browser opens it directly, which means the address in
`TEAMUP_MINIO_ENDPOINT` has to be one a *browser* can resolve and load over
TLS. `minio:9000` is neither: the name does not exist outside Docker, and an
`http://` URL on an HTTPS page is blocked as mixed content before it is even
tried. The shipped configuration therefore looks like this:

```ini
TEAMUP_MINIO_ENDPOINT=teamup.yourdomain.com     # what the browser opens
TEAMUP_MINIO_SECURE=true
TEAMUP_MINIO_INTERNAL_ENDPOINT=minio:9000       # what this server dials
TEAMUP_MINIO_INTERNAL_SECURE=false
TEAMUP_MINIO_BUCKET=teamup-files
TEAMUP_MINIO_REGION=us-east-1
```

and `nginx.conf` carries a matching `location /teamup-files/` block. Three
things about that block are load-bearing, because an S3 signature covers the
request path, the query string and the `Host` header, and MinIO recomputes it
from whatever actually arrives:

- **The location prefix is the bucket name.** URLs are path-style —
  `https://teamup.yourdomain.com/<bucket>/<object key>`. Rename the bucket and
  you must rename the location.
- **Nothing may rewrite the path.** No `rewrite`, and `proxy_pass` without a
  URI part so nginx forwards the request line as the browser sent it.
- **`proxy_set_header Host $http_host;`.** Not `$host`, which drops a
  non-default port, and not nginx's default, which substitutes the upstream
  name. Either one produces `SignatureDoesNotMatch` on every upload.

If you put object storage on its own hostname instead — `files.yourdomain.com`
with its own certificate and server block — set `TEAMUP_MINIO_ENDPOINT` to that
name and keep the bucket-named location. Everything above still applies.

### Upload limits, and the two upload paths

Two settings, and they do different jobs.

`TEAMUP_MAX_UPLOAD_BYTES` (default 5 GiB) is the largest file the server will
accept by either path. It is not advisory: the exact content length is signed
into every URL, so object storage rejects a body of any other size, and the
attachment row records the size storage measured rather than the one the
browser claimed. The browser is told the limit too, so an over-sized file is
refused before anything is sent.

`TEAMUP_MULTIPART_THRESHOLD_BYTES` (default 16 MiB) is where a file stops going
up in one PUT and starts going up in resumable parts:

- **At or below it** — one presigned PUT, as before.
- **Above it** — a real S3 multipart upload. The server fixes the plan (how
  many parts, and each part's exact byte count) when the upload starts, signs
  one URL per part, and assembles the object itself. A connection that dies at
  80% resumes: the browser asks the server which parts object storage already
  holds and sends only what is missing. That survives a page reload as well,
  provided the same file is chosen again — the browser cannot re-open a file
  handle across a reload, so the user has to re-select it, and the parts
  already uploaded are then kept.

A single PUT above the threshold is **refused**, not merely discouraged. That
is what guarantees no request ever carries more than one part.

Which is why raising `TEAMUP_MAX_UPLOAD_BYTES` no longer means touching nginx.
`client_max_body_size` on the `/teamup-files/` location has to cover the
largest single *request*, and above the threshold that is one part — so it
needs to be at least `TEAMUP_MULTIPART_THRESHOLD_BYTES` plus a little slack,
not the size of the largest file. The shipped `100m` covers the default 16 MiB
threshold with room to spare. Raise `client_max_body_size` only if you raise
the threshold.

The threshold must not exceed the ceiling — the server refuses to start with a
configuration where nothing could ever use multipart.

**Reclaiming abandoned uploads.** An interrupted multipart upload holds its
parts in object storage without ever appearing as an object, so nothing that
walks the bucket finds them. The hourly sweep that removes staged-but-never-sent
single uploads also aborts multipart uploads nobody came back to after 24
hours. A day is deliberate: it is long enough to resume a paused 2 GB upload
the next morning. No bucket lifecycle rule is required, and none is configured.

Multipart was previously removed from this codebase because it was a lie:
`initiate` handed back an ordinary single-PUT URL and `complete` wrote no
record at all. What is there now does the work — the size a client declares is
what it is authorised for and all it can deliver, and completing writes a real
attachment row measured from storage.

`TEAMUP_MINIO_REGION` is part of the signing key. Leave it at `us-east-1`
unless you set `MINIO_REGION` on the minio service, in which case set both to
the same value. A mismatch shows up in the readiness probe at start-up rather
than as a user's failed upload.

### Running it

```bash
docker compose -f docker-compose.prod.yml up -d
```

nginx terminates TLS and proxies the app, the WebSocket, and LiveKit's
signalling socket at `/livekit`. The browser works that out for itself: on
HTTPS it connects to `wss://<your host>/livekit` rather than to port 7880, which
a secure page is not allowed to do.

Open `https://teamup.yourdomain.com` and the setup wizard runs.

### Afterwards

- **Renewal**: `certbot renew` writes new files; copy them into `certs/` again
  and `docker compose -f docker-compose.prod.yml restart nginx`. Worth putting
  in a cron job.
- **Backups**: everything durable is in the `postgres-data` and `minio-data`
  volumes. `docker exec teamup-postgres pg_dump -U teamup teamup` for the
  database.
- **Logs**: `docker compose -f docker-compose.prod.yml logs -f teamup`.

---

## When something does not work

| Symptom | Cause |
| --- | --- |
| Sign-in returns to the login page | The session cookie was discarded. Either you are on plain HTTP without `TEAMUP_INSECURE_COOKIES`, or on HTTPS with it set when it should not be. |
| Nothing arrives without a refresh | The WebSocket is not connecting. Check that the proxy passes `Upgrade` and `Connection` headers on `/ws`. |
| A call connects but nobody hears anything | UDP 50000–60000 is not open, or LiveKit is advertising an address the other side cannot reach. `docker logs teamup-livekit | grep nodeIP` shows what it settled on. |
| The camera never starts | The page is not on HTTPS or localhost. |
| Every upload and every inline image fails | `TEAMUP_MINIO_ENDPOINT` is not an address the browser can reach over TLS, or nginx has no `location` matching the bucket name. Open one of the failing URLs directly: a DNS error or a mixed-content block in the console says which. |
| Uploads fail with 403 `SignatureDoesNotMatch` | The proxy altered something the signature covers. Check that the `/teamup-files/` location sets `Host $http_host` and does not rewrite the path, and that `TEAMUP_MINIO_REGION` matches the object store. |
| Uploads fail with 413 | `client_max_body_size` on the `/teamup-files/` location is smaller than `TEAMUP_MULTIPART_THRESHOLD_BYTES`, which is the largest single request either path ever makes. |
| A large file is refused before anything is sent | It is over `TEAMUP_MAX_UPLOAD_BYTES`. The message says what the limit is. |
| A large file is refused with "has to go up as a resumable multipart upload" | A client asked for a single PUT above `TEAMUP_MULTIPART_THRESHOLD_BYTES`. The bundled web client never does; an old cached build or a third-party client might. |
| A big upload restarts from zero after a reload | The same file has to be re-selected — the browser cannot re-open a file handle across a reload. A different file, or the same file re-saved, is treated as a new upload on purpose: the parts already in storage are not re-read, so a wrong match would silently splice two files together. |
| Object storage fills with nothing visible in the bucket | Abandoned multipart parts. They are not objects, so listing the bucket does not show them. The hourly sweep aborts them after 24 hours; check the logs for "could not sweep abandoned uploads". |
| New members get no email | No SMTP is configured. The screen says so, and offers the credentials as a file instead. |
