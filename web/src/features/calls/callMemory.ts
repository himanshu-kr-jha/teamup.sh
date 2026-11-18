// The call this tab is in, kept across a reload so the reload does not drop it.
//
// A refresh, by accident or to get past a stuck page, used to put you out of the
// call and in front of the screen before it, with the others watching you leave.
// The call is written down here, per tab, while you are in it, and a reload soon
// after goes straight back in with the microphone and camera as they were. The
// LiveKit token is not kept: coming back asks for a new one, which is also how
// a call that ended meanwhile, or that you were removed from, says so.

import type { BackgroundChoice } from './background';

/** How long after the page went away a reload still goes back into the call. */
export const REJOIN_WITHIN_MS = 2 * 60_000;

/** How often the time on it is brought up to date while in the call. */
export const REMEMBER_EVERY_MS = 10_000;

export interface CallMemory {
  /** Whose call it is: somebody else signing in on this tab does not join it. */
  userId: string;
  callId: string;
  /** A day of a standing meeting, whose room nobody answers knocks from. */
  standing: boolean;
  mic: boolean;
  camera: boolean;
  audioInput?: string;
  videoInput?: string;
  audioOutput?: string;
  background?: BackgroundChoice;
  /** When it was last written, in milliseconds since 1970. */
  at: number;
}

const KEY = 'teamup.call.current';

const text = (value: unknown, max = 256): string | undefined =>
  typeof value === 'string' && value ? value.slice(0, max) : undefined;

/** Only the shapes background.ts writes; it checks the rest (a picture that exists) itself. */
function background(value: unknown): BackgroundChoice | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const v = value as { kind?: unknown; strength?: unknown; id?: unknown; theme?: unknown };
  if (v.kind === 'none' || v.kind === 'custom') return { kind: v.kind } as BackgroundChoice;
  if (v.kind === 'blur' && (v.strength === 'light' || v.strength === 'strong')) return { kind: 'blur', strength: v.strength };
  if (v.kind === 'image' && text(v.id, 64)) return { kind: 'image', id: text(v.id, 64) } as BackgroundChoice;
  if (v.kind === 'company' && (v.theme === 'dark' || v.theme === 'light')) return { kind: 'company', theme: v.theme };
  return undefined;
}

/** What was stored, made safe: anything not as written reads as nothing. */
export function parseCallMemory(raw: string | null): CallMemory | null {
  if (!raw) return null;
  try {
    const m = JSON.parse(raw) as Record<string, unknown> | null;
    if (!m || typeof m !== 'object') return null;
    const userId = text(m.userId, 64);
    const callId = text(m.callId, 64);
    if (!userId || !callId || typeof m.at !== 'number' || !Number.isFinite(m.at)) return null;
    return {
      userId,
      callId,
      standing: m.standing === true,
      mic: m.mic === true,
      camera: m.camera === true,
      audioInput: text(m.audioInput),
      videoInput: text(m.videoInput),
      audioOutput: text(m.audioOutput),
      background: background(m.background),
      at: m.at,
    };
  } catch {
    return null;
  }
}

/**
 * The call to go back into, if this page is a reload of one you were in moments
 * ago as this person. Anything else, a new tab, a duplicated one, a link
 * followed, or a reload long after, starts afresh.
 */
export function recallCall(
  userId: string | undefined,
  now: number,
  options: { reloaded?: boolean; storage?: Pick<Storage, 'getItem'> } = {}
): CallMemory | null {
  const { reloaded = wasReloaded(), storage = safeStorage() } = options;
  if (!userId || !reloaded || !storage) return null;
  let memory: CallMemory | null;
  try {
    memory = parseCallMemory(storage.getItem(KEY));
  } catch {
    return null;
  }
  if (!memory || memory.userId !== userId) return null;
  // A clock set back since is no reason to trust it.
  if (now < memory.at - 5_000 || now - memory.at > REJOIN_WITHIN_MS) return null;
  return memory;
}

/** Writes down the call this tab is in. Not kept where storage is refused; then a reload simply starts afresh. */
export function rememberCall(memory: CallMemory, storage: Pick<Storage, 'setItem'> | undefined = safeStorage()): void {
  if (!storage) return;
  try {
    storage.setItem(KEY, JSON.stringify(memory));
  } catch {
    // Private browsing, or storage full.
  }
}

/** Forgets it: you left, the call ended, you were removed, or coming back failed. */
export function forgetCall(storage: Pick<Storage, 'removeItem'> | undefined = safeStorage()): void {
  if (!storage) return;
  try {
    storage.removeItem(KEY);
  } catch {
    // Nothing kept to forget.
  }
}

/**
 * Whether this page was loaded by a reload. A duplicated tab copies this tab's
 * storage too, and it must not take the call over from the tab it came from.
 */
export function wasReloaded(): boolean {
  try {
    const entry = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined;
    return entry?.type === 'reload';
  } catch {
    return false;
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof sessionStorage === 'undefined' ? undefined : sessionStorage;
  } catch {
    return undefined;
  }
}

// ── Somebody else reloading ──────────────────────────────────────────────

/**
 * How long somebody who dropped out without saying they were leaving may still be
 * on their way back: a reload, or a phone moving between networks. LiveKit itself
 * closes a room about this long after its last person goes (its departure timeout).
 */
export const DROP_GRACE_MS = 20_000;

/** The data topic a deliberate leave is said on, just before disconnecting. */
export const LEAVING_TOPIC = 'teamup.leaving';

/**
 * Tells a leave from a drop, for whoever is left behind. Being last out of a call
 * ends it for good, so somebody who is only reloading must not be taken for gone:
 * the call is then left open, and the room closes itself if nobody comes back.
 */
export function createDropWatch() {
  const leaving = new Set<string>();
  const dropped = new Map<string, number>();
  return {
    /** They said they were leaving. */
    saidLeaving(identity: string) {
      leaving.add(identity);
    },
    left(identity: string, now: number) {
      if (leaving.delete(identity)) dropped.delete(identity);
      else dropped.set(identity, now);
    },
    joined(identity: string) {
      leaving.delete(identity);
      dropped.delete(identity);
    },
    /** Whether anybody dropped out moments ago and may yet come back. */
    awaited(now: number): boolean {
      for (const at of dropped.values()) if (now - at < DROP_GRACE_MS) return true;
      return false;
    },
  };
}
