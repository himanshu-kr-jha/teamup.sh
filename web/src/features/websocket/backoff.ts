/**
 * How long to wait before trying the socket again.
 *
 * The ladder used to stop after five rungs, which meant a backend away for
 * more than about forty-eight seconds — every rolling deploy — left the tab
 * connected to nothing, for ever, with no way back but a reload. A client
 * that has given up is worse than a slow one: nothing arrives and nothing
 * says so.
 *
 * So the ladder climbs to a ceiling and then stays there. Thirty seconds is
 * short enough that a deploy is over almost as soon as it is over, and long
 * enough that a backend that is genuinely gone is not being hammered.
 */
const CONNECT_DELAYS = [1000, 2000, 5000, 10000, 30000];

export const RECONNECT_CEILING_MS = 30000;

/**
 * Twenty per cent either side. Every tab in the company loses its socket on
 * the same tick of a deploy, and would otherwise come back on the same tick
 * too — fifty simultaneous handshakes against a process still warming up.
 */
const JITTER = 0.2;

export function reconnectDelay(attempt: number, jitter: () => number = Math.random): number {
  const base = attempt < CONNECT_DELAYS.length ? CONNECT_DELAYS[attempt] : RECONNECT_CEILING_MS;
  // jitter() is 0..1; map it onto [1 - JITTER, 1 + JITTER].
  return Math.round(base * (1 - JITTER + jitter() * JITTER * 2));
}

/**
 * Whether a closed socket should be opened again.
 *
 * Only this tab deciding to close counts as a reason to stop. Everything else
 * is reconnected, including a *clean* close: the server drains on shutdown and
 * closes with 1000, so treating "clean" as "do not reconnect" meant a graceful
 * deploy — the well-behaved case — was the one that killed every tab.
 */
export function shouldReconnect(_close: { code: number; wasClean: boolean }, intentional: boolean): boolean {
  return !intentional;
}
