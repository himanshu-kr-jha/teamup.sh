import { createECDH, randomBytes } from 'node:crypto';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { test, expect } from './support/fixtures';
import { apiAs, idOf } from './support/meetings';

/**
 * A phone's push service, as far as TeamUp can tell: it is where the server
 * sends a push for the phone, and this one writes down what arrived.
 */
async function phonePushService() {
  const arrived: { at: number; urgency?: string; ttl?: string }[] = [];
  const server: Server = createServer((req, res) => {
    req.resume();
    req.on('end', () => {
      arrived.push({ at: Date.now(), urgency: req.headers['urgency'] as string, ttl: req.headers['ttl'] as string });
      res.writeHead(201).end();
    });
  });
  await new Promise<void>((done) => server.listen(0, '127.0.0.1', done));
  const { port } = server.address() as AddressInfo;
  return { endpoint: `http://127.0.0.1:${port}/phone`, arrived, close: () => new Promise((done) => server.close(done)) };
}

/** The keys a browser makes when it subscribes, so the server can encrypt for it. */
function browserKeys() {
  const ecdh = createECDH('prime256v1');
  ecdh.generateKeys();
  return { p256dh: ecdh.getPublicKey().toString('base64url'), auth: randomBytes(16).toString('base64url') };
}

// Reported from Android: with TeamUp installed on the phone, nothing rang and
// nothing was shown while the app was not open. The server skipped anybody
// with TeamUp open anywhere, so a laptop left open silenced the phone; and a
// call's notification sounded once rather than ringing.
test('the phone rings and shows messages while TeamUp is open on the laptop', async ({ as }) => {
  test.setTimeout(90_000);
  const phone = await phonePushService();
  const alice = await apiAs('alice');
  const sam = await apiAs('sam');
  let callId: string | undefined;
  try {
    await alice.post('/notifications/subscribe', { endpoint: phone.endpoint, ...browserKeys(), deviceName: 'Android phone' });

    // The laptop: open, signed in, connected.
    const laptop = await as('alice');
    // Signed in is on the chat page; its socket is up a moment later.
    await laptop.page.waitForTimeout(1_500);

    // A message.
    const dm = await sam.post('/channels/direct', { userId: idOf('alice') });
    await sam.post(`/channels/${dm.id}/messages`, { content: 'Are you there?', tempId: `push-${Date.now()}` });
    await expect.poll(() => phone.arrived.length, { message: 'the message never reached the phone', timeout: 15_000 }).toBeGreaterThan(0);
    // An Android phone asleep holds back anything that is not urgent.
    expect(phone.arrived[0].urgency).toBe('high');

    // A call rings, again and again, until it is dealt with.
    const before = phone.arrived.length;
    const call = await sam.post('/calls', { callType: 'audio', invitees: [idOf('alice')] });
    callId = call.id;
    await expect
      .poll(() => phone.arrived.length - before, { message: 'the call did not keep ringing the phone', timeout: 20_000 })
      .toBeGreaterThanOrEqual(3);
    const ring = phone.arrived[before];
    expect(ring.urgency).toBe('high');
    // Not delivered to a phone that comes back on after the call has stopped ringing.
    expect(Number(ring.ttl)).toBeLessThanOrEqual(60);

    // The caller gives up: the phone is told to stop, and then it stops.
    // Pushes are told apart by how long they wait for a phone that is off: a ring
    // as long as it rings, "Call ended" a minute, and a message, such as the
    // missed call written into the chat, an hour. Counting every push failed on a
    // slow machine, where those last two landed after a fixed pause.
    const ended = Date.now();
    await sam.post(`/calls/${callId}/end`);
    callId = undefined;
    const stopped = () => phone.arrived.find((p) => p.at >= ended && p.ttl === '60');
    await expect.poll(() => !!stopped(), { message: 'the phone was never told the call was over', timeout: 15_000 }).toBe(true);
    await laptop.page.waitForTimeout(7_000);
    const ringsAfter = phone.arrived.filter((p) => p.ttl === ring.ttl && p.at > stopped()!.at);
    expect(ringsAfter, 'the phone went on ringing after the call was over').toEqual([]);
  } finally {
    if (callId) await sam.post(`/calls/${callId}/end`).catch(() => {});
    await alice.call('delete', '/notifications/subscribe', { endpoint: phone.endpoint }).catch(() => {});
    await Promise.all([alice.dispose(), sam.dispose(), phone.close()]);
  }
});
