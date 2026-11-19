// What the screen before a call needs that is not drawing: who is already in it, in
// words, and the microphone, camera and devices you chose last time.

/** Somebody on a call's list, as the call's details give them. */
export interface CallPerson {
  userId: string;
  userName?: string;
  joinedAt?: string | null;
  leftAt?: string | null;
}

/**
 * Who is in the call right now: came in and has not left. Everybody invited is on
 * the list from the start, with no joinedAt until they come. Yourself is left out,
 * and said separately (you may be in it on another device).
 */
export function presentPeople(people: CallPerson[] | undefined, myId: string | undefined): { others: CallPerson[]; me: boolean } {
  const here = (people ?? []).filter((p) => !!p.joinedAt && !p.leftAt);
  return { others: here.filter((p) => p.userId !== myId), me: here.some((p) => p.userId === myId) };
}

/** The first word of a name, which is how people are spoken of in a sentence. */
function firstName(name: string | undefined): string {
  const word = (name ?? '').trim().split(/\s+/)[0];
  return word || 'Somebody';
}

/**
 * "Nobody is here yet", "Asha is here", "Asha and Ravi are here", "Asha, Ravi and
 * Mohan are here", and past three, "Asha, Ravi and 3 more are here".
 */
export function whoIsHere(names: (string | undefined)[]): string {
  const first = names.map(firstName);
  if (first.length === 0) return 'Nobody is here yet';
  if (first.length === 1) return `${first[0]} is here`;
  if (first.length === 2) return `${first[0]} and ${first[1]} are here`;
  if (first.length === 3) return `${first[0]}, ${first[1]} and ${first[2]} are here`;
  return `${first[0]}, ${first[1]} and ${first.length - 2} more are here`;
}

/** How many faces are drawn before the rest are counted as "+3". */
export const FACES_SHOWN = 4;

/** The faces to draw and how many are left over for "+N". */
export function facesAndRest<T>(people: T[], shown = FACES_SHOWN): { faces: T[]; rest: number } {
  // Five people are five faces; a "+1" chip would take a face's room to say less.
  if (people.length <= shown + 1) return { faces: people, rest: 0 };
  return { faces: people.slice(0, shown), rest: people.length - shown };
}

// ── What you chose last time ──────────────────────────────────────────────

/** The microphone and camera you joined with last, and the devices. */
export interface JoinChoice {
  mic: boolean;
  camera: boolean;
  audioInput?: string;
  videoInput?: string;
  audioOutput?: string;
}

/** Somebody who has never joined starts as today: both off, until they turn them on. */
export const FIRST_JOIN: JoinChoice = { mic: false, camera: false };

const keyFor = (userId: string) => `teamup.call.join.${userId}`;

const deviceId = (value: unknown): string | undefined => (typeof value === 'string' && value ? value.slice(0, 256) : undefined);

/** What was stored, made safe: anything that is not what was written reads as a first join. */
export function parseJoinChoice(raw: string | null): JoinChoice {
  if (!raw) return { ...FIRST_JOIN };
  try {
    const parsed = JSON.parse(raw) as Record<string, unknown> | null;
    if (!parsed || typeof parsed !== 'object') return { ...FIRST_JOIN };
    return {
      mic: parsed.mic === true,
      camera: parsed.camera === true,
      audioInput: deviceId(parsed.audioInput),
      videoInput: deviceId(parsed.videoInput),
      audioOutput: deviceId(parsed.audioOutput),
    };
  } catch {
    return { ...FIRST_JOIN };
  }
}

/** Your last choice in this browser. Per person, since a computer can be shared. */
export function readJoinChoice(userId: string | undefined, storage: Pick<Storage, 'getItem'> | undefined = safeStorage()): JoinChoice {
  if (!userId || !storage) return { ...FIRST_JOIN };
  try {
    return parseJoinChoice(storage.getItem(keyFor(userId)));
  } catch {
    return { ...FIRST_JOIN };
  }
}

/** Keeps a choice, or part of one, for next time. Not kept where storage is refused; nothing else depends on it. */
export function saveJoinChoice(
  userId: string | undefined,
  change: Partial<JoinChoice>,
  storage: Pick<Storage, 'getItem' | 'setItem'> | undefined = safeStorage()
): void {
  if (!userId || !storage) return;
  try {
    const next = { ...readJoinChoice(userId, storage), ...change };
    storage.setItem(keyFor(userId), JSON.stringify(next));
  } catch {
    // Private browsing, or storage full: it is simply not remembered.
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof localStorage === 'undefined' ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

// ── Devices ───────────────────────────────────────────────────────────────

/** Why a camera or microphone cannot be used, in the terms the screen speaks. */
export type DeviceProblem = 'denied' | 'missing' | 'busy' | 'failed';

/** What getUserMedia's error means for somebody choosing what to join with. */
export function deviceProblem(error: unknown): DeviceProblem {
  const name = (error as { name?: string } | null)?.name ?? '';
  if (name === 'NotAllowedError' || name === 'SecurityError' || name === 'PermissionDeniedError') return 'denied';
  if (name === 'NotFoundError' || name === 'OverconstrainedError' || name === 'DevicesNotFoundError') return 'missing';
  if (name === 'NotReadableError' || name === 'TrackStartError' || name === 'AbortError') return 'busy';
  return 'failed';
}

/** The sentence for a problem with the camera or the microphone. */
export function problemText(problem: DeviceProblem, what: 'camera' | 'microphone'): string {
  switch (problem) {
    case 'denied':
      return `Your browser is not letting TeamUp use your ${what}. Allow it from the address bar, then try again. You can still join without it.`;
    case 'missing':
      return `No ${what} was found. Plug one in, or join without it.`;
    case 'busy':
      return `Your ${what} is in use by another app, or could not start. Close that app and try again, or join without it.`;
    default:
      return `Your ${what} could not be started. You can still join without it.`;
  }
}

/** What a device list shows for a device whose name the browser keeps back until access is given. */
export function deviceLabel(device: { label: string }, index: number, kind: MediaDeviceKind): string {
  if (device.label) return device.label;
  const noun = kind === 'audioinput' ? 'Microphone' : kind === 'videoinput' ? 'Camera' : 'Speaker';
  return `${noun} ${index + 1}`;
}
