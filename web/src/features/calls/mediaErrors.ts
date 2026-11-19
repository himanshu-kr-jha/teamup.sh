// What went wrong with your camera or microphone, in words, wherever a call
// meets it: the banner over a call now, and the screen before a call later.
// Pure, so it is tested without a browser.

/** Why a camera or microphone cannot be used. */
export type DeviceProblem = 'denied' | 'missing' | 'busy' | 'failed';

/** The two devices a call takes from you. A speaker or a shared screen is neither. */
export type InputDevice = 'camera' | 'microphone';

/** What getUserMedia's error means, by the name browsers give it (old Chrome names too). */
export function deviceProblem(error: unknown): DeviceProblem {
  const name = (error as { name?: string } | null)?.name ?? '';
  if (name === 'NotAllowedError' || name === 'SecurityError' || name === 'PermissionDeniedError') return 'denied';
  // A device id remembered from last time that is no longer plugged in fails as Overconstrained.
  if (name === 'NotFoundError' || name === 'OverconstrainedError' || name === 'DevicesNotFoundError') return 'missing';
  if (name === 'NotReadableError' || name === 'TrackStartError' || name === 'AbortError') return 'busy';
  return 'failed';
}

/**
 * Which device a LiveKit MediaDevicesError is about. LiveKit gives the kind for
 * the camera and the microphone and none for a shared screen, whose picker
 * being closed is changing your mind, not a broken device.
 */
export function inputDevice(kind: MediaDeviceKind | undefined): InputDevice | null {
  if (kind === 'videoinput') return 'camera';
  if (kind === 'audioinput') return 'microphone';
  return null;
}

/** The sentence for a problem with your camera or microphone during a call. */
export function inCallText(problem: DeviceProblem, device: InputDevice): string {
  switch (problem) {
    case 'denied':
      return `Your browser blocked the ${device}. Allow it in the address bar, then turn the ${device} on again.`;
    case 'missing':
      return `No ${device} was found. Plug one in or choose another, then turn the ${device} on again.`;
    case 'busy':
      return `Your ${device} is in use by another app, or could not start. Close that app, then turn the ${device} on again.`;
    default:
      return `Your ${device} could not be started. Turn it on again to try once more.`;
  }
}

/** One device that is not working, as the banner over a call shows it. */
export interface MediaFailure {
  device: InputDevice;
  problem: DeviceProblem;
  text: string;
}

/** A failure worth telling you about, or null for one that does not stop your camera or microphone. */
export function mediaFailure(error: unknown, kind: MediaDeviceKind | undefined): MediaFailure | null {
  const device = inputDevice(kind);
  if (!device) return null;
  const problem = deviceProblem(error);
  return { device, problem, text: inCallText(problem, device) };
}

// The same failure reaches the call twice: LiveKit announces it to the room,
// which the banner shows, and then the button or shortcut that asked for the
// device is told it failed. Marking what the banner has said keeps the second
// from saying it again in a toast.
const reported = new WeakSet<object>();

export function markReported(error: unknown): void {
  if (error && typeof error === 'object') reported.add(error);
}

export function wasReported(error: unknown): boolean {
  return !!error && typeof error === 'object' && reported.has(error);
}
