import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { useLocalParticipant } from '@livekit/components-react';
import type { LocalVideoTrack } from 'livekit-client';
import { Ban, Check, ImagePlus, Loader2, Sparkles } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import { useToast } from '@/shared/hooks/useToast';
import { cn } from '@/shared/utils/cn';
import {
  LOOKS,
  PICTURES,
  applyCameraEffects,
  customPicture,
  lookFilter,
  lookWash,
  setLook,
  supportsLooks,
  useLook,
  sameChoice,
  saveCustomPicture,
  setBackgroundChoice,
  thumbURL,
  useBackgroundChoice,
  useBackgroundsSupported,
  type BackgroundChoice,
} from './background';

// Whether an effect is being put on the camera right now. The first one fetches
// MediaPipe (about 3 MB), which can take a few seconds; the button says so.
let applying = 0;
const applyingListeners = new Set<() => void>();
const setApplying = (delta: number) => {
  applying = Math.max(0, applying + delta);
  applyingListeners.forEach((l) => l());
};
function useApplying(): boolean {
  return useSyncExternalStore(
    (l) => {
      applyingListeners.add(l);
      return () => applyingListeners.delete(l);
    },
    () => applying > 0,
    () => false
  );
}

/** How long one change may take before the next is let through anyway. */
const APPLY_TIMEOUT_MS = 20_000;

/**
 * Keeps your chosen background and colour on your camera for as long as you are
 * in the call. Renders nothing; it sits inside the room wherever the call is,
 * full size or in the corner, so the effect does not depend on the controls.
 *
 * It applies a change straight away while the camera is on, and when the camera
 * comes back on after being off. Not while it is off: a processor set on a camera
 * with no picture waits for one, and every change after it waited behind it, which
 * is why a background used to appear only after the camera was turned off and on.
 */
export function CameraBackground() {
  const { cameraTrack } = useLocalParticipant();
  const track = cameraTrack?.track as LocalVideoTrack | undefined;
  const live = useCameraLive(track);
  const choice = useBackgroundChoice();
  const look = useLook();
  const toast = useToast();
  const toastRef = useRef(toast.error);
  toastRef.current = toast.error;
  // One change at a time: a processor half set up when the next choice arrives
  // would otherwise be replaced under itself.
  const queue = useRef(Promise.resolve());

  useEffect(() => {
    if (!track || !live) return;
    setApplying(1);
    queue.current = queue.current.then(async () => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          applyCameraEffects(track, choice, look),
          new Promise<void>((resolve) => (timer = setTimeout(resolve, APPLY_TIMEOUT_MS))),
        ]);
      } catch {
        toastRef.current('Your camera effects could not be applied on this device.');
        setBackgroundChoice({ kind: 'none' });
        setLook('natural');
      } finally {
        clearTimeout(timer);
        setApplying(-1);
      }
    });
  }, [track, live, choice, look]);

  return null;
}

/** Whether the camera is on and sending a picture, following it as it turns off and on. */
function useCameraLive(track: LocalVideoTrack | undefined): boolean {
  const [live, setLive] = useState(() => !!track && !track.isMuted);
  useEffect(() => {
    if (!track) {
      setLive(false);
      return;
    }
    const update = () => setLive(!track.isMuted && track.mediaStreamTrack?.readyState === 'live');
    update();
    track.on('muted', update);
    track.on('unmuted', update);
    track.on('restarted', update);
    return () => {
      track.off('muted', update);
      track.off('unmuted', update);
      track.off('restarted', update);
    };
  }, [track]);
  return live;
}

/** The button beside the camera's, and its menu of backgrounds. */
export function BackgroundPicker() {
  const supported = useBackgroundsSupported();
  const choice = useBackgroundChoice();
  const look = useLook();
  const busy = useApplying();
  const [looks] = useState(supportsLooks);
  const toast = useToast();
  const [custom, setCustom] = useState(customPicture);
  const [saving, setSaving] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  if (supported === null) return null;
  const on = choice.kind !== 'none' || look !== 'natural';
  const label = supported ? 'Background effects' : 'Background effects are not available in this browser';

  const choose = (next: BackgroundChoice) => setBackgroundChoice(next);

  const addPicture = async (file: File | undefined) => {
    if (!file) return;
    setSaving(true);
    try {
      await saveCustomPicture(file);
      setCustom(customPicture());
      // A new choice object even when it was already yours, so the camera
      // picks up the new picture.
      choose({ kind: 'custom' });
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'That picture could not be used.');
    } finally {
      setSaving(false);
      if (fileInput.current) fileInput.current.value = '';
    }
  };

  return (
    <>
      <DropdownMenu modal={false}>
        <DropdownMenuTrigger asChild disabled={!supported}>
          <button
            type="button"
            className="lk-button"
            aria-label={label}
            title={label}
            aria-pressed={on}
            data-lk-enabled={on}
            data-testid="background-effects"
          >
            {saving || busy ? <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" /> : <Sparkles className="h-5 w-5" aria-hidden="true" />}
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          side="top"
          align="center"
          sideOffset={8}
          className="max-h-[min(40rem,calc(100dvh-6rem))] w-[18.5rem] overflow-y-auto p-2"
          aria-label="Background effects"
        >
          <DropdownMenuLabel className="px-1 pb-2 pt-0.5 text-xs font-medium text-muted-foreground">Background</DropdownMenuLabel>
          <div className="grid grid-cols-3 gap-2">
            <Tile label="None" selected={choice.kind === 'none'} onSelect={() => choose({ kind: 'none' })}>
              <span className="flex h-full w-full items-center justify-center bg-muted text-muted-foreground">
                <Ban className="h-5 w-5" aria-hidden="true" />
              </span>
            </Tile>
            <Tile
              label="Slight blur"
              selected={sameChoice(choice, { kind: 'blur', strength: 'light' })}
              onSelect={() => choose({ kind: 'blur', strength: 'light' })}
            >
              <RoomSample blur={2} />
            </Tile>
            <Tile
              label="Blur"
              selected={sameChoice(choice, { kind: 'blur', strength: 'strong' })}
              onSelect={() => choose({ kind: 'blur', strength: 'strong' })}
            >
              <RoomSample blur={5} />
            </Tile>
            {PICTURES.map((p) => (
              <Tile
                key={p.id}
                label={p.label}
                selected={sameChoice(choice, { kind: 'image', id: p.id })}
                onSelect={() => choose({ kind: 'image', id: p.id })}
              >
                <img src={thumbURL(p.id)} alt="" className="h-full w-full object-cover" draggable={false} />
              </Tile>
            ))}
            {custom && (
              <Tile label="Your picture" selected={choice.kind === 'custom'} onSelect={() => choose({ kind: 'custom' })}>
                <img src={custom} alt="" className="h-full w-full object-cover" draggable={false} />
              </Tile>
            )}
            <Tile label={custom ? 'Change your picture' : 'Add a picture'} selected={false} onSelect={() => fileInput.current?.click()}>
              <span className="flex h-full w-full items-center justify-center border border-dashed border-border bg-background text-muted-foreground">
                <ImagePlus className="h-5 w-5" aria-hidden="true" />
              </span>
            </Tile>
          </div>
          {looks && (
            <>
              <DropdownMenuLabel className="px-1 pb-2 pt-3 text-xs font-medium text-muted-foreground">Colour</DropdownMenuLabel>
              <div className="grid grid-cols-3 gap-2" role="group" aria-label="Colour">
                {LOOKS.map((l) => (
                  <Tile key={l.id} label={l.label} selected={look === l.id} onSelect={() => setLook(l.id)}>
                    <span className="relative block h-full w-full" style={{ filter: lookFilter(l.id) || undefined }}>
                      <RoomSample blur={0} />
                      {lookWash(l.id) && (
                        <span className="absolute inset-0 mix-blend-soft-light" style={{ backgroundColor: lookWash(l.id) }} />
                      )}
                    </span>
                  </Tile>
                ))}
              </div>
            </>
          )}
          <p className="px-1 pt-2 text-[11px] leading-snug text-muted-foreground">
            Done on this device, before your video is sent. Others see the result.
          </p>
        </DropdownMenuContent>
      </DropdownMenu>
      <input
        ref={fileInput}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        className="hidden"
        aria-hidden="true"
        tabIndex={-1}
        onChange={(e) => void addPicture(e.target.files?.[0])}
        data-testid="background-picture-input"
      />
    </>
  );
}

function Tile({ label, selected, onSelect, children }: { label: string; selected: boolean; onSelect: () => void; children: ReactNode }) {
  return (
    <DropdownMenuItem
      onSelect={onSelect}
      aria-label={label}
      aria-checked={selected}
      role="menuitemradio"
      title={label}
      className={cn(
        'relative flex cursor-pointer flex-col items-stretch gap-1 rounded-md p-0 focus:bg-transparent data-[highlighted]:bg-transparent',
        'group/tile'
      )}
    >
      <span
        className={cn(
          'relative block aspect-video overflow-hidden rounded-md ring-offset-2 ring-offset-popover transition-shadow',
          selected ? 'ring-2 ring-primary' : 'ring-1 ring-border group-data-[highlighted]/tile:ring-2 group-data-[highlighted]/tile:ring-ring/60'
        )}
      >
        {children}
        {selected && (
          <span className="absolute right-1 top-1 flex h-4 w-4 items-center justify-center rounded-full bg-primary text-primary-foreground shadow">
            <Check className="h-3 w-3" aria-hidden="true" />
          </span>
        )}
      </span>
      <span className="truncate px-0.5 text-center text-[11px] leading-tight text-foreground">{label}</span>
    </DropdownMenuItem>
  );
}

/**
 * A little room behind a person, its background blurred by the given amount: the
 * two blurs show their difference at a glance, and the colours show each look.
 * Drawn, rather than a photograph.
 */
function RoomSample({ blur: amount }: { blur: number }) {
  return (
    <span className="relative block h-full w-full overflow-hidden bg-[#d8cec2]">
      <span className="absolute inset-[-10%]" style={{ filter: amount ? `blur(${amount}px)` : undefined }}>
        <span className="absolute left-[14%] top-[16%] h-[42%] w-[26%] border-2 border-white bg-[#bcd6ea]" />
        <span className="absolute right-[12%] top-[14%] h-[58%] w-[24%] bg-[#8a6a4f]">
          <span className="absolute inset-x-0 top-1/3 h-[2px] bg-[#5d4634]" />
          <span className="absolute inset-x-0 top-2/3 h-[2px] bg-[#5d4634]" />
          <span className="absolute left-[15%] top-[8%] h-[22%] w-[18%] bg-[#c0583f]" />
          <span className="absolute left-[45%] top-[42%] h-[20%] w-[30%] bg-[#3f6f8a]" />
        </span>
        <span className="absolute bottom-0 inset-x-0 h-[22%] bg-[#a8927c]" />
      </span>
      <span className="absolute bottom-0 left-1/2 h-[48%] w-[40%] -translate-x-1/2 rounded-t-[45%] bg-[#3b4252]" />
      <span className="absolute bottom-[44%] left-1/2 h-[30%] w-[19%] -translate-x-1/2 rounded-full bg-[#c99a7c]" />
    </span>
  );
}
