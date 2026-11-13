# Load test

Measures how many people one TeamUp server carries before it slows down.
Never point it at a server with real people on it: it signs in thousands of test users and writes messages and tasks.

## What a virtual user does

Each one signs in, keeps a live connection open as a browser tab does, and then does something every 3 seconds on average:

| Share | Action |
|---|---|
| 35% | read a conversation, then mark it read |
| 15% | send a message (nine in ten to their team of 25, one in ten to everybody) |
| 10% | list conversations |
| 10% | unread count |
| 10% | list tasks |
| 5% | task dashboard |
| 4% each | create a task, search, calendar, calls, who is online |
| the rest | who am I |

That is about five times busier than a person at work, so 100 virtual users stand for roughly 500 people.
Each sent message carries the time it was sent, so the other members' live connections measure how long it took to reach them.

## Running it

You need two machines in the same region: the server under test, set up with `scripts/new-server.sh` as a real one would be, and a second one to send the load from, so the generator never competes with the server for CPU.

1. On the test server, seed users and conversations (as many users as the largest step):

   ```
   docker compose exec -T postgres psql -U teamup -d teamup \
     -v n=2000 -v domain=loadtest.example -v owner=OWNER_EMAIL -f - < tools/loadtest/seed.sql
   ```

   The users are `load1@loadtest.example` and on, with the owner's password.

2. Tell the test server's nginx to trust the generator for the user's address, inside `http {`:

   ```
   set_real_ip_from GENERATOR_IP;
   real_ip_header X-Forwarded-For;
   ```

   Each virtual user then has its own address, as people do.
   Without it the whole test comes from one address and meets the per-address limits, which are there for strangers.

3. Build and copy the tool, then run steps of increasing size from the generator:

   ```
   CGO_ENABLED=0 GOOS=linux go build -o loadtest ./tools/loadtest
   export LOADTEST_PASSWORD=...   # the owner's password
   ./loadtest -base https://TEST_SERVER -users 250 -ramp 20s -duration 2m -json step-250.json
   ```

   Watch `docker stats` on the server while it runs.
   `-actions=false -sockets 5` only holds connections open, for testing how many live connections fit.
`-upload 0.03 -download 0.06` adds file sharing: three actions in a hundred upload a file (mostly pictures, some documents, one in twenty 10 to 15 MB) through the same signed links the app uses, and six open a file somebody shared.

## Meetings

`calls.sh ROOMS PEOPLE DURATION [QUALITY]` creates each meeting as a TeamUp call, then fills it with LiveKit's own load tester, [`lk load-test`](https://github.com/livekit/livekit-cli), at the given video quality.
Each meeting has PEOPLE sending camera and microphone and PEOPLE watching a 3x3 gallery.
It needs the test server's LiveKit key and secret in the environment, and the server's firewall open on TCP 7881 and UDP 7882.
Run it beside `loadtest` to see chat, files and meetings together, and watch the server's network as well as its CPU: video is mostly bandwidth.

The summary gives, for each kind of request, how many per second and the p50, p95 and p99 times, the error codes, and how long a message took to reach the other members.
