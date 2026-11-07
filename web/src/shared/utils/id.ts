/**
 * A random id, on every origin TeamUp is served from.
 *
 * `crypto.randomUUID` is restricted to a secure context — HTTPS, or localhost.
 * That makes it present on the machine running the server and absent the
 * moment the same page is opened from a phone at http://192.168.x.x, where
 * calling it throws `crypto.randomUUID is not a function`. The socket called
 * it from inside `onopen` and from the ping handler, so on a local network the
 * connection handshake and the heartbeat both died and live chat stopped
 * working a minute and a half after each page load.
 *
 * `crypto.getRandomValues` is not restricted the same way, so where the
 * platform will not mint a UUID we assemble one from random bytes instead. The
 * last resort exists so this can never be the thing that throws: an id here
 * only has to be unique among one browser tab's in-flight frames, and a socket
 * that cannot say hello is a worse outcome than a weakly random id.
 */
export function newId(): string {
  const c: Crypto | undefined = globalThis.crypto;

  if (typeof c?.randomUUID === 'function') return c.randomUUID();

  const bytes = new Uint8Array(16);
  if (typeof c?.getRandomValues === 'function') {
    c.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256);
  }

  // Version 4, variant 1 — the shape anything parsing these will expect.
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;

  const hex: string[] = [];
  for (let i = 0; i < bytes.length; i++) hex.push(bytes[i].toString(16).padStart(2, '0'));
  return (
    hex.slice(0, 4).join('') + '-' +
    hex.slice(4, 6).join('') + '-' +
    hex.slice(6, 8).join('') + '-' +
    hex.slice(8, 10).join('') + '-' +
    hex.slice(10, 16).join('')
  );
}
