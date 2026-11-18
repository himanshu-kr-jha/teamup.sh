import { useEffect, useId, useRef, useState } from 'react';
import { useLocalParticipant, useMediaDeviceSelect, useRoomContext } from '@livekit/components-react';
import { TrackEvent, type LocalAudioTrack } from 'livekit-client';
import { Check } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import { Switch } from '@/shared/components/ui/switch';
import { useAuth } from '@/features/auth/AuthProvider';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import { deviceLabel } from './prejoin';
import {
  UNAVAILABLE_TEXT,
  captureFor,
  markNoiseUnavailable,
  noiseCancellationPossible,
  noiseStatus,
  setNoiseChoice,
  useNoiseCancellationPossible,
  useNoiseChoice,
  type NoiseStatus,
} from './noiseCancellation';

/** The processor's name (rnnoise.ts), here so this file does not load it. */
const RNNOISE = 'teamup-rnnoise';

/** Whether a track has RNNoise on it now, following it as that changes. */
function useAttached(track: LocalAudioTrack | undefined): boolean {
  const [attached, setAttached] = useState(() => track?.getProcessor()?.name === RNNOISE);
  useEffect(() => {
    const update = () => setAttached(track?.getProcessor()?.name === RNNOISE);
    update();
    track?.on(TrackEvent.TrackProcessorUpdate, update);
    return () => {
      track?.off(TrackEvent.TrackProcessorUpdate, update);
    };
  }, [track]);
  return attached;
}

/**
 * Asks the microphone again, with the browser's own noise suppression on or off.
 * Always with the device it is on: LiveKit drops the other settings when asked
 * again without one.
 */
async function recapture(track: LocalAudioTrack, rnnoise: boolean) {
  const settings = track.mediaStreamTrack.getSettings();
  if (settings.noiseSuppression === !rnnoise) return;
  const deviceId = settings.deviceId || (track.constraints.deviceId as string | undefined) || 'default';
  await track.restartTrack({ deviceId, ...captureFor(rnnoise) });
}

/** The call's microphone track, while it has one. */
function useMicTrack(): LocalAudioTrack | undefined {
  const { microphoneTrack } = useLocalParticipant();
  return microphoneTrack?.track as LocalAudioTrack | undefined;
}

/** What noise cancellation is doing for the call's microphone. */
function useCallNoiseStatus(): NoiseStatus {
  const { user } = useAuth();
  const wanted = useNoiseChoice(user?.id);
  const possible = useNoiseCancellationPossible();
  const attached = useAttached(useMicTrack());
  return noiseStatus(wanted, possible, attached);
}

/**
 * Keeps RNNoise on your microphone for as long as you are in the call and want
 * it, through switching microphones, muting and unmuting. Renders nothing; it
 * sits inside the room, full size or in the corner.
 */
export function NoiseCancellation() {
  const track = useMicTrack();
  const { user } = useAuth();
  const wanted = useNoiseChoice(user?.id);
  const possible = useNoiseCancellationPossible();
  const toast = useToast();
  const toastRef = useRef(toast.toast);
  toastRef.current = toast.toast;
  // One change at a time, as with the camera's effects.
  const queue = useRef(Promise.resolve());
  // Asked again once you next press or type, where the browser would not start audio yet.
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    if (!track) return;
    const want = wanted && possible;
    queue.current = queue.current.then(async () => {
      const has = track.getProcessor()?.name === RNNOISE;
      try {
        if (want && !has) {
          const { createRnnoiseProcessor } = await import('./rnnoise');
          await recapture(track, true);
          await track.setProcessor(createRnnoiseProcessor());
        } else if (!want) {
          if (has) await track.stopProcessor();
          await recapture(track, false);
        }
      } catch (err) {
        // The microphone let go of meanwhile, as on leaving, is no reason to give up on it.
        if (track.mediaStreamTrack?.readyState === 'ended') return;
        await track.stopProcessor().catch(() => undefined);
        await recapture(track, false).catch(() => undefined);
        if ((err as Error)?.name === 'NotAllowedError' && (err as Error).message === 'suspended') {
          // Not the device: the page has not been touched since it loaded (a reload
          // straight back into the call), and audio waits for that.
          const again = () => setRetry((n) => n + 1);
          window.addEventListener('pointerdown', again, { once: true, capture: true });
          window.addEventListener('keydown', again, { once: true, capture: true });
          return;
        }
        if (noiseCancellationPossible()) toastRef.current(UNAVAILABLE_TEXT);
        markNoiseUnavailable();
      }
    });
  }, [track, wanted, possible, retry]);

  return null;
}

/** "Noise cancellation" and its switch, on the screen before a call and in the call's microphone menu. */
export function NoiseSwitch({ status, className }: { status: NoiseStatus; className?: string }) {
  const { user } = useAuth();
  const wanted = useNoiseChoice(user?.id);
  const id = useId();
  return (
    <div className={className}>
      <div className="flex items-center justify-between gap-3">
        <label htmlFor={id} className="min-w-0 text-sm">
          Noise cancellation
        </label>
        <Switch
          id={id}
          checked={wanted}
          onCheckedChange={(on) => setNoiseChoice(user?.id, on)}
          data-testid="noise-cancellation"
          data-status={status}
        />
      </div>
      {status === 'unavailable' && <p className="mt-1 text-xs text-muted-foreground">{UNAVAILABLE_TEXT}.</p>}
    </div>
  );
}

/**
 * The menu beside the microphone button in a call: which microphone, and noise
 * cancellation. LiveKit's own device menu has no room for anything else.
 */
export function MicrophoneMenu({ label }: { label: string }) {
  const room = useRoomContext();
  const track = useMicTrack();
  const status = useCallNoiseStatus();
  const [open, setOpen] = useState(false);
  const { devices, activeDeviceId, setActiveMediaDevice } = useMediaDeviceSelect({
    kind: 'audioinput',
    room,
    track,
    // Names are only known once the microphone has been allowed, which in a call it has.
    requestPermissions: open,
  });
  const active = activeDeviceId === 'default' ? devices[0]?.deviceId : activeDeviceId;

  return (
    <DropdownMenu modal={false} open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="lk-button lk-button-menu"
          aria-label={label}
          title={label}
          data-noise-cancellation={status}
        />
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="end" sideOffset={8} className="w-72 max-w-[calc(100vw-1rem)] p-1">
        <DropdownMenuLabel className="px-2 pb-1 pt-1.5 text-xs font-medium text-muted-foreground">Microphone</DropdownMenuLabel>
        {devices.map((device, i) => {
          const selected = device.deviceId === active;
          return (
            <DropdownMenuItem
              key={device.deviceId || i}
              onSelect={() => void setActiveMediaDevice(device.deviceId)}
              className="gap-2"
              aria-checked={selected}
              role="menuitemradio"
            >
              <Check className={cn('h-4 w-4 shrink-0', !selected && 'invisible')} aria-hidden="true" />
              <span className="min-w-0 truncate">{deviceLabel(device, i, 'audioinput')}</span>
            </DropdownMenuItem>
          );
        })}
        <DropdownMenuSeparator />
        {/* A switch rather than an item, so pressing it leaves the menu open to see it change. */}
        <NoiseSwitch status={status} className="px-2 py-1.5" />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
