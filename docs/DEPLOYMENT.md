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

For the HTTPS half, the least painful route is a tunnel, which hands you a real
certificate without touching your network:

```bash
cloudflared tunnel --url http://localhost:8080
```

It prints an `https://something.trycloudflare.com` address. Set
`TEAMUP_BASE_URL` to it, leave `TEAMUP_INSECURE_COOKIES` **off**, and open that
address on the phone. Everything works, including calls and notifications,
because the browser sees a proper certificate.

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

# Change every one of these from the defaults.
TEAMUP_DATABASE_URL=postgres://teamup:<a real password>@postgres:5432/teamup?sslmode=disable
TEAMUP_MINIO_ACCESS_KEY=<not minioadmin>
TEAMUP_MINIO_SECRET_KEY=<not minioadmin>
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

Replace `teamup.example.com` with your own name in `nginx.conf` — it appears
twice, once in the redirect and once in the TLS server.

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
| New members get no email | No SMTP is configured. The screen says so, and offers the credentials as a file instead. |
