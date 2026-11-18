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
import { RNNOISE, applyNoiseCancellation } from './noiseApply';
import {
  UNAVAILABLE_TEXT,
  markNoiseUnavailable,
  noiseCancellationPossible,
  noiseStatus,
  setNoiseChoice,
  useNoiseCancellationPossible,
  useNoiseChoice,
  type NoiseStatus,
} from './noiseCancellation';

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
  const [waitingForTap, setWaitingForTap] = useState(false);
  // The listener waiting for that tap, while there is one.
  const armed = useRef<(() => void) | null>(null);
  useEffect(
    () => () => {
      if (!armed.current) return;
      window.removeEventListener('pointerdown', armed.current, true);
      window.removeEventListener('keydown', armed.current, true);
    },
    []
  );

  useEffect(() => {
    if (!track) return;
    const want = wanted && possible;
    // Each change counts only until the next, or until the call ends.
    let current = true;
    queue.current = queue.current.then(async () => {
      const outcome = await applyNoiseCancellation(track, want, () => current);
      if (!current) return;
      setWaitingForTap(outcome === 'suspended');
      if (outcome === 'suspended') {
        // Not the device: the page has not been touched since it loaded (a reload
        // straight back into the call), and audio waits for that. Asked again on
        // the next tap or key, listened for once however often this happens.
        if (armed.current) return;
        const again = () => {
          window.removeEventListener('pointerdown', again, true);
          window.removeEventListener('keydown', again, true);
          armed.current = null;
          setRetry((n) => n + 1);
        };
        armed.current = again;
        window.addEventListener('pointerdown', again, true);
        window.addEventListener('keydown', again, true);
      } else if (outcome === 'failed') {
        if (noiseCancellationPossible()) toastRef.current(UNAVAILABLE_TEXT);
        markNoiseUnavailable();
      }
    });
    return () => {
      current = false;
    };
  }, [track, wanted, possible, retry]);

  if (!waitingForTap) return null;
  return (
    <div
      role="status"
      data-testid="noise-tap-hint"
      className="pointer-events-none fixed left-1/2 top-[calc(env(safe-area-inset-top)+0.75rem)] z-[60] max-w-[calc(100vw-2rem)] -translate-x-1/2 rounded-full border border-border bg-background px-3 py-1.5 text-center text-xs text-foreground shadow-lg"
    >
      Tap anywhere to turn noise cancellation back on
    </div>
  );
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
