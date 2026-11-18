// Putting RNNoise on the call's microphone, or taking it off, one change at a time
// and safely against the call ending in the middle: every step waits on something
// (loading the processor, asking for the microphone again, starting the audio),
// and leaving the call meanwhile must not leave a microphone open or an audio
// context running. The React side is NoiseCancellation in NoiseControls.tsx.

import type { LocalAudioTrack, TrackProcessor, Track, AudioProcessorOptions } from 'livekit-client';
import { captureFor } from './noiseCancellation';

/** The processor's name (rnnoise.ts), here so this file does not load it. */
export const RNNOISE = 'teamup-rnnoise';

/** The part of a LiveKit microphone track this needs, so tests can stand in for it. */
export type NoiseTrack = Pick<LocalAudioTrack, 'getProcessor' | 'setProcessor' | 'stopProcessor' | 'restartTrack' | 'constraints'> & {
  mediaStreamTrack: Pick<MediaStreamTrack, 'readyState' | 'getSettings'>;
};

type Loader = () => Promise<{ createRnnoiseProcessor: () => TrackProcessor<Track.Kind.Audio, AudioProcessorOptions> }>;

/**
 * 'done'; 'cancelled' when the call ended or the choice changed meanwhile, with
 * nothing left behind; 'suspended' when the page has not been touched since it
 * loaded, so audio cannot start yet; 'failed' when this device cannot.
 */
export type NoiseOutcome = 'done' | 'cancelled' | 'suspended' | 'failed';

/** What the processor throws when the page's audio cannot start until it is touched (rnnoise.ts). */
export const isSuspended = (err: unknown) => (err as Error)?.name === 'NotAllowedError' && (err as Error)?.message === 'suspended';

/**
 * Asks the microphone again, with the browser's own noise suppression on or off.
 * Always with the device it is on: LiveKit drops the other settings when asked
 * again without one.
 */
export async function recapture(track: NoiseTrack, rnnoise: boolean) {
  const settings = track.mediaStreamTrack.getSettings();
  if (settings.noiseSuppression === !rnnoise) return;
  const deviceId = settings.deviceId || (track.constraints.deviceId as string | undefined) || 'default';
  await track.restartTrack({ deviceId, ...captureFor(rnnoise) });
}

/**
 * Brings the track to what is wanted. `alive` says whether this change still
 * matters: false once the call has ended, or a newer change has been asked for.
 */
export async function applyNoiseCancellation(
  track: NoiseTrack,
  want: boolean,
  alive: () => boolean,
  load: Loader = () => import('./rnnoise')
): Promise<NoiseOutcome> {
  const live = () => alive() && track.mediaStreamTrack.readyState === 'live';
  // Takes it off a track nobody is using any more, which closes its audio context.
  const undo = async () => {
    if (track.getProcessor()?.name === RNNOISE) await track.stopProcessor().catch(() => undefined);
    return 'cancelled' as const;
  };
  if (!live()) return 'cancelled';
  const has = track.getProcessor()?.name === RNNOISE;
  try {
    if (want && !has) {
      const { createRnnoiseProcessor } = await load();
      if (!live()) return 'cancelled';
      await recapture(track, true);
      if (!live()) return 'cancelled';
      await track.setProcessor(createRnnoiseProcessor());
      if (!live()) return undo();
    } else if (!want) {
      if (has) await track.stopProcessor();
      if (!live()) return 'cancelled';
      await recapture(track, false);
    }
    return 'done';
  } catch (err) {
    if (!live()) return undo();
    await track.stopProcessor().catch(() => undefined);
    // Never asks for the microphone again once the call is over.
    if (live()) await recapture(track, false).catch(() => undefined);
    return isSuspended(err) ? 'suspended' : 'failed';
  }
}
