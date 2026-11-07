import { describe, it, expect, afterEach } from 'vitest';
import { newId } from './id';

const real = globalThis.crypto;
afterEach(() => {
  Object.defineProperty(globalThis, 'crypto', { value: real, configurable: true, writable: true });
});

function withCrypto(value: unknown) {
  Object.defineProperty(globalThis, 'crypto', { value, configurable: true, writable: true });
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe('newId', () => {
  it('uses the platform generator when there is one', () => {
    withCrypto({ randomUUID: () => '11111111-2222-4333-8444-555555555555' });
    expect(newId()).toBe('11111111-2222-4333-8444-555555555555');
  });

  // The reason this function exists.
  //
  // crypto.randomUUID is exposed only to a secure context. On the machine
  // running TeamUp the origin is localhost, which is exempt, so it is always
  // there and every id worked. Opened from a phone on the same Wi-Fi the
  // origin is http://192.168.x.x and it is simply not defined — so the call
  // threw inside the socket's onopen and onmessage handlers. conn.init was
  // never sent, so the server never acknowledged and everything the client
  // tried to send queued for ever; conn.pong was never sent either, so no
  // frame ever arrived from that client and the server reaped the connection
  // after 95 seconds, again and again. Live chat died a few seconds after
  // every page load and only a refresh brought it back.
  it('still returns an id where randomUUID does not exist', () => {
    withCrypto({ getRandomValues: (a: Uint8Array) => real.getRandomValues(a) });
    expect(() => newId()).not.toThrow();
    expect(newId()).toMatch(UUID);
  });

  it('still returns an id where there is no crypto at all', () => {
    withCrypto(undefined);
    expect(() => newId()).not.toThrow();
    expect(newId()).toMatch(UUID);
  });

  it('does not repeat itself', () => {
    withCrypto({ getRandomValues: (a: Uint8Array) => real.getRandomValues(a) });
    const seen = new Set(Array.from({ length: 2000 }, () => newId()));
    expect(seen.size).toBe(2000);
  });
});
