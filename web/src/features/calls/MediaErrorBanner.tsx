import { useEffect, useRef, useState } from 'react';
import { useRoomContext } from '@livekit/components-react';
import { RoomEvent, Track, type Participant, type TrackPublication } from 'livekit-client';
import { AlertTriangle, X } from 'lucide-react';
import { useToast } from '@/shared/hooks/useToast';
import { markReported, mediaFailure, type InputDevice, type MediaFailure } from './mediaErrors';

const DEVICE_OF: Partial<Record<Track.Source, InputDevice>> = {
  [Track.Source.Camera]: 'camera',
  [Track.Source.Microphone]: 'microphone',
};

/**
 * Says over a call that your camera or microphone could not be used, which one
 * and why, until that device works or you dismiss it. An empty tile with no
 * explanation is the worst answer to a refused camera; a warning that stays up
 * once the camera works (18 November) is the next worst.
 *
 * Only a failure that leaves the device off is said: a closed screen picker, a
 * speaker that cannot be chosen or a background that falls back to the plain
 * camera do not stop your camera or microphone. Where there is no room for the
 * banner, in the corner window, it is a toast instead.
 */
export function MediaErrorBanner({ hidden }: { hidden: boolean }) {
  const room = useRoomContext();
  const [failures, setFailures] = useState<Partial<Record<InputDevice, MediaFailure>>>({});
  const toast = useToast();
  const quiet = useRef({ hidden, toast: toast.error });
  quiet.current = { hidden, toast: toast.error };

  useEffect(() => {
    const onFailure = (error: Error, kind?: MediaDeviceKind) => {
      const failure = mediaFailure(error, kind);
      if (!failure) return;
      markReported(error);
      if (quiet.current.hidden) quiet.current.toast(failure.text);
      setFailures((now) => ({ ...now, [failure.device]: failure }));
    };
    // The device working is the answer to it not working.
    const onWorking = (publication: TrackPublication, participant: Participant) => {
      const device = DEVICE_OF[publication.source];
      if (!device || participant !== room.localParticipant) return;
      setFailures((now) => {
        if (!now[device]) return now;
        const { [device]: _gone, ...rest } = now;
        return rest;
      });
    };
    room.on(RoomEvent.MediaDevicesError, onFailure);
    room.on(RoomEvent.LocalTrackPublished, onWorking);
    room.on(RoomEvent.TrackUnmuted, onWorking);
    return () => {
      room.off(RoomEvent.MediaDevicesError, onFailure);
      room.off(RoomEvent.LocalTrackPublished, onWorking);
      room.off(RoomEvent.TrackUnmuted, onWorking);
    };
  }, [room]);

  const shown = (['microphone', 'camera'] as const).flatMap((d) => (failures[d] ? [failures[d]] : []));
  if (hidden || shown.length === 0) return null;
  return (
    <div
      role="alert"
      data-testid="media-error"
      className="flex items-start gap-3 border-b border-destructive/30 bg-destructive/10 px-3 py-2.5 text-sm text-destructive dark:border-red-500/30 dark:bg-red-500/15 dark:text-red-300"
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden="true" />
      <div className="min-w-0 flex-1 space-y-1 leading-snug">
        {shown.map((f) => (
          <p key={f.device}>{f.text}</p>
        ))}
      </div>
      <button
        type="button"
        aria-label="Dismiss"
        title="Dismiss"
        onClick={() => setFailures({})}
        className="-my-1 -mr-1 inline-flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-md hover:bg-destructive/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring dark:hover:bg-red-500/20"
      >
        <X className="h-4 w-4" aria-hidden="true" />
      </button>
    </div>
  );
}
