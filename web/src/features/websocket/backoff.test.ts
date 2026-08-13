import { describe, it, expect } from 'vitest';
import { reconnectDelay, shouldReconnect, RECONNECT_CEILING_MS } from './backoff';

// No jitter, so the ladder itself is what is under test.
const noJitter = () => 0.5;

describe('reconnectDelay', () => {
  it('climbs the ladder for the first few attempts', () => {
    expect(reconnectDelay(0, noJitter)).toBe(1000);
    expect(reconnectDelay(1, noJitter)).toBe(2000);
    expect(reconnectDelay(2, noJitter)).toBe(5000);
    expect(reconnectDelay(3, noJitter)).toBe(10000);
    expect(reconnectDelay(4, noJitter)).toBe(30000);
  });

  // The defect: past the fifth attempt the client stopped scheduling
  // altogether, so a backend away for longer than the ladder — every rolling
  // deploy — left the tab dead until somebody reloaded it.
  it('never gives up, however long the backend is away', () => {
    for (const attempt of [5, 6, 20, 500, 100000]) {
      expect(reconnectDelay(attempt, noJitter)).toBe(RECONNECT_CEILING_MS);
    }
  });

  it('holds at the ceiling rather than backing off forever', () => {
    expect(RECONNECT_CEILING_MS).toBe(30000);
    expect(reconnectDelay(9999, noJitter)).toBeLessThanOrEqual(RECONNECT_CEILING_MS * 1.2);
  });

  // Fifty-odd tabs reconnecting on the same tick is a thundering herd against
  // a backend that has only just come up.
  it('spreads attempts either side of the base delay', () => {
    expect(reconnectDelay(0, () => 0)).toBe(800);
    expect(reconnectDelay(0, () => 1)).toBe(1200);
    expect(reconnectDelay(4, () => 0)).toBe(24000);
  });

  it('defaults to real randomness within the jitter band', () => {
    for (let i = 0; i < 50; i++) {
      const d = reconnectDelay(1);
      expect(d).toBeGreaterThanOrEqual(1600);
      expect(d).toBeLessThanOrEqual(2400);
    }
  });
});

describe('shouldReconnect', () => {
  it('does not reconnect when this tab closed the socket itself', () => {
    expect(shouldReconnect({ code: 1000, wasClean: true }, true)).toBe(false);
    expect(shouldReconnect({ code: 1006, wasClean: false }, true)).toBe(false);
  });

  it('reconnects after a socket that dropped', () => {
    expect(shouldReconnect({ code: 1006, wasClean: false }, false)).toBe(true);
  });

  // The server drains on shutdown and closes with 1000, which is a *clean*
  // close. Treating clean as "do not reconnect" meant a graceful rolling
  // deploy produced no reconnect attempt at all.
  it('reconnects after the server closes cleanly on shutdown', () => {
    expect(shouldReconnect({ code: 1000, wasClean: true }, false)).toBe(true);
    expect(shouldReconnect({ code: 1001, wasClean: true }, false)).toBe(true);
  });

  // The hub closes with 1008 when the token has expired. Worth retrying: the
  // cookie is refreshed out of band, and the ceiling keeps it to one attempt
  // every thirty seconds until it is.
  it('keeps retrying a policy close, slowly', () => {
    expect(shouldReconnect({ code: 1008, wasClean: true }, false)).toBe(true);
  });
});
