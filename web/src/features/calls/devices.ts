// Companion mode: one person in a call from a laptop and a phone at once.
//
// Each browser joins as its own identity in the room, "<userId>:<deviceId>"
// (internal/calls/participant), so a second device no longer pushes the first
// one out. Everything that counts people goes by the person instead: the count
// in the call's header, whether you are alone, raised hands, the chat's "you".
//
// The device id is kept per browser (localStorage), so a reload, or a second tab
// of the same browser, is the same device: a reload goes back in as itself, and
// a second tab takes the call over from the first (DUPLICATE_IDENTITY), which
// says so, rather than counting as a second device that would echo the first.

// As CallStage.tsx and notetaker.tsx have them; repeated here, as those import this.
const GUEST_PREFIX = 'guest-';
const NOTETAKER_PREFIX = 'notes-';

/** The kinds of device a tile is labelled with (participant.DeviceKinds). */
export type DeviceKind = 'phone' | 'tablet' | 'laptop';

/** The participant attribute holding a member's device kind (participant.DeviceAttribute). */
export const DEVICE_ATTRIBUTE = 'device';

/** The LiveKit data topic a device uses to tell its person's other devices it took the call over. */
export const MOVED_TOPIC = 'teamup.device-moved';

const DEVICE_KEY = 'teamup.device.id';
const ID_PATTERN = /^[A-Za-z0-9_-]{1,32}$/;
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Sixteen random letters and digits, which is plenty to tell one person's browsers apart. */
export function newDeviceId(random: (n: number) => Uint8Array = randomBytes): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  return Array.from(random(16), (b) => alphabet[b % alphabet.length]).join('');
}

function randomBytes(n: number): Uint8Array {
  const out = new Uint8Array(n);
  crypto.getRandomValues(out);
  return out;
}

let memoryId: string | null = null;

/**
 * This browser's device id, made the first time it is asked for and kept. Where
 * storage is refused (private browsing in some browsers) it lasts as long as the
 * page, which still keeps two devices apart.
 */
export function deviceId(storage: Pick<Storage, 'getItem' | 'setItem'> | undefined = safeLocalStorage()): string {
  try {
    const kept = storage?.getItem(DEVICE_KEY);
    if (kept && ID_PATTERN.test(kept)) return kept;
  } catch {
    // Unreadable: made afresh below.
  }
  if (memoryId && !storage) return memoryId;
  const made = newDeviceId();
  try {
    storage?.setItem(DEVICE_KEY, made);
  } catch {
    // Not kept; this page keeps it.
  }
  memoryId = made;
  return made;
}

/** What kind of device a browser is on, from what it says of itself. */
export function deviceKind(userAgent: string, maxTouchPoints = 0): DeviceKind {
  if (/iPad|Tablet|PlayBook|Silk|Kindle/i.test(userAgent)) return 'tablet';
  // An iPad asking for the desktop site says it is a Mac, but a Mac has no touch screen.
  if (/Macintosh/.test(userAgent) && maxTouchPoints > 1) return 'tablet';
  if (/Android/i.test(userAgent) && !/Mobile/i.test(userAgent)) return 'tablet';
  if (/Mobi|iPhone|iPod|Android|Windows Phone/i.test(userAgent)) return 'phone';
  return 'laptop';
}

/** This browser's device, as the token request sends it. */
export function thisDevice(): { id: string; kind: DeviceKind } {
  const ua = typeof navigator === 'undefined' ? '' : navigator.userAgent;
  const touch = typeof navigator === 'undefined' ? 0 : navigator.maxTouchPoints ?? 0;
  return { id: deviceId(), kind: deviceKind(ua, touch) };
}

/**
 * Who an identity belongs to, the same for each of somebody's devices: a member's
 * user id, a guest's whole identity, and nothing for the note-taker.
 */
export function personOf(identity: string): string {
  if (!identity || identity.startsWith(NOTETAKER_PREFIX)) return '';
  if (identity.startsWith(GUEST_PREFIX)) return identity;
  const cut = identity.indexOf(':');
  const user = cut < 0 ? identity : identity.slice(0, cut);
  return UUID_PATTERN.test(user) ? user : identity;
}

export function samePerson(a: string, b: string): boolean {
  const p = personOf(a);
  return p !== '' && p === personOf(b);
}

/** How many people some identities are: each person once, however many devices. */
export function countPeople(identities: Iterable<string>): number {
  const seen = new Set<string>();
  for (const id of identities) {
    const who = personOf(id);
    if (who) seen.add(who);
  }
  return seen.size;
}

/** The kind of device a participant is in from, if its token said. */
export function kindOf(p: { attributes?: Readonly<Record<string, string>> }): DeviceKind | undefined {
  const kind = p.attributes?.[DEVICE_ATTRIBUTE];
  return kind === 'phone' || kind === 'tablet' || kind === 'laptop' ? kind : undefined;
}

/**
 * A tile's name: "Asha (phone)" while Asha is in from more than one device, so
 * the two tiles can be told apart, and plain "Asha" otherwise.
 */
export function deviceLabel(name: string, kind: DeviceKind | undefined, devicesOfPerson: number): string {
  if (devicesOfPerson < 2 || !kind) return name;
  return `${name} (${kind})`;
}

/** "your phone", for "Moved to your phone". */
export function yourDevice(kind: DeviceKind | undefined): string {
  return kind ? `your ${kind}` : 'your other device';
}

/** What the device that was moved off says. */
export function movedMessage(kind: DeviceKind | undefined): string {
  return `Moved to ${yourDevice(kind)}. This device has left the call.`;
}

/** The word a device that took over sends its person's other devices. */
export function movedPacket(kind: DeviceKind) {
  return new TextEncoder().encode(JSON.stringify({ to: kind }));
}

/**
 * Whether a packet tells this device to step aside, and to what: only from
 * another device of the same person. Identities are the media server's, signed
 * into each token, so nobody else can send one in your name.
 */
export function readMoved(
  payload: Uint8Array,
  from: { identity: string } | undefined,
  topic: string | undefined,
  me: string
): { to: DeviceKind | undefined } | null {
  if (topic !== MOVED_TOPIC || !from || from.identity === me || !samePerson(from.identity, me)) return null;
  try {
    const body = JSON.parse(new TextDecoder().decode(payload)) as { to?: unknown };
    const to = body?.to;
    return { to: to === 'phone' || to === 'tablet' || to === 'laptop' ? to : undefined };
  } catch {
    return { to: undefined };
  }
}

function safeLocalStorage(): Storage | undefined {
  try {
    return typeof localStorage === 'undefined' ? undefined : localStorage;
  } catch {
    return undefined;
  }
}
