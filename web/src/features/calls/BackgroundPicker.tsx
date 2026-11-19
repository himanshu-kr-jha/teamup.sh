import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useLocalParticipant } from '@livekit/components-react';
import { TrackEvent, type LocalVideoTrack } from 'livekit-client';
import { Ban, Check, ImagePlus, Loader2, Sparkles } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/shared/components/ui/dropdown-menu';
import { useToast } from '@/shared/hooks/useToast';
import { PERMS, can } from '@/shared/permissions';
import { useAuthStore } from '@/shared/store/authStore';
import { cn } from '@/shared/utils/cn';
import {
  LOOKS,
  applyCameraEffects,
  preloadCameraEffects,
  lookFilter,
  lookWash,
  setLook,
  stillAllowed,
  supportsLooks,
  useLook,
  sameChoice,
  setBackgroundChoice,
  useBackgroundChoice,
  useBackgroundsSupported,
  type BackgroundChoice,
} from './background';
import {
  COMPANY_THEMES,
  composeCompanyBackground,
  useCallBackgrounds,
  useCompanySource,
  useKnownCallBackgrounds,
  type CompanySource,
  type CompanyTheme,
} from './companyBackground';
import { companyCanvasSize } from './nameplate';

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
  const live = useCameraEffects(cameraTrack?.track as LocalVideoTrack | undefined);
  // With the camera on, what a background needs is fetched while nothing else is
  // going on, so the first one chosen is quick.
  useEffect(() => (live ? preloadCameraEffects(true) : undefined), [live]);
  return null;
}

/**
 * Keeps your chosen background and colour on a camera track: the one in the call
 * (CameraBackground), or the preview before you join it (PreJoinScreen).
 */
export function useCameraEffects(track: LocalVideoTrack | undefined): boolean {
  const live = useCameraLive(track);
  const choice = useBackgroundChoice();
  const look = useLook();
  useForgetStaleBackground();
  const backgrounds = useCallBackgrounds();
  // The company background is made from your name and role as well as the
  // template, so a change to any of them makes it again.
  const company = useCompanySource(choice.kind === 'company' ? choice.theme : null);
  // One of the organization's pictures, by its address: undefined while the
  // pictures are being found out, null when it is not among them.
  const picture =
    choice.kind !== 'picture' ? null : backgrounds ? (backgrounds.pictures?.find((p) => p.id === choice.id)?.url ?? null) : undefined;
  const sourceKey = company === undefined || picture === undefined ? undefined : JSON.stringify({ company, picture });
  const toast = useToast();
  const toastRef = useRef(toast.error);
  toastRef.current = toast.error;
  // One change at a time: a processor half set up when the next choice arrives
  // would otherwise be replaced under itself.
  const queue = useRef(Promise.resolve());

  useEffect(() => {
    // Not until it is known which backgrounds the organization has: taking the
    // effect off meanwhile would only flash the room behind you.
    if (!track || !live || sourceKey === undefined) return;
    const source = JSON.parse(sourceKey) as { company: CompanySource | null; picture: string | null };
    setApplying(1);
    queue.current = queue.current.then(async () => {
      let timer: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          (async () => {
            let picture = source.picture;
            if (source.company) {
              // The camera's own size, not that of the picture an effect already on it puts out.
              const settings = track.getSourceTrackSettings();
              const size = companyCanvasSize(settings?.width, settings?.height);
              picture = await composeCompanyBackground(source.company, size.width, size.height);
            }
            await applyCameraEffects(track, choice, look, picture);
          })(),
          new Promise<void>((resolve) => (timer = setTimeout(resolve, APPLY_TIMEOUT_MS))),
        ]);
      } catch {
        // A camera let go of meanwhile, as the preview's is on joining, is no
        // reason to forget the background chosen for it.
        if (track.mediaStreamTrack?.readyState === 'ended') return;
        toastRef.current('Your camera effects could not be applied on this device.');
        setBackgroundChoice({ kind: 'none' });
        setLook('natural');
      } finally {
        clearTimeout(timer);
        setApplying(-1);
      }
    });
  }, [track, live, choice, look, sourceKey]);
  return live;
}

/**
 * Forgets a background the organization no longer offers, such as a picture an
 * administrator has since removed, and goes back to none, quietly. Only once the
 * server has said what there is, so a failed request forgets nothing.
 */
function useForgetStaleBackground() {
  const known = useKnownCallBackgrounds();
  const choice = useBackgroundChoice();
  useEffect(() => {
    if (known && !stillAllowed(choice, known)) setBackgroundChoice({ kind: 'none' });
  }, [known, choice]);
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

/**
 * The button beside the camera's, and its menu of backgrounds. In the call it is
 * one of the call's own buttons; on the screen before it, a round button laid over
 * your preview, beside the microphone and camera.
 */
export function BackgroundPicker({ variant = 'call' }: { variant?: 'call' | 'preview' }) {
  const supported = useBackgroundsSupported();
  const choice = useBackgroundChoice();
  const look = useLook();
  const busy = useApplying();
  const [looks] = useState(supportsLooks);
  const backgrounds = useCallBackgrounds();
  const signedIn = useAuthStore((s) => s.isAuthenticated);
  const mayManage = useAuthStore((s) => can(s.user, PERMS.orgSettings));
  useForgetStaleBackground();
  const companyThemes = COMPANY_THEMES.filter((t) => backgrounds?.[t.theme]);
  const pictures = backgrounds?.pictures ?? [];
  const [open, setOpen] = useState(false);
  // Whatever is chosen next should be quick, the first time too.
  useEffect(() => {
    if (open && supported) preloadCameraEffects();
  }, [open, supported]);

  if (supported === null) return null;
  // A background the organization has since taken away is no background.
  const gone = !!backgrounds && !stillAllowed(choice, backgrounds);
  const on = (choice.kind !== 'none' && !gone) || look !== 'natural';
  const label = supported ? 'Background effects' : 'Background effects are not available in this browser';

  const choose = (next: BackgroundChoice) => setBackgroundChoice(next);

  return (
    <DropdownMenu modal={false} open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild disabled={!supported}>
        <button
          type="button"
          className={variant === 'call' ? 'lk-button' : previewButtonClass(on)}
          aria-label={label}
          title={label}
          aria-pressed={on}
          data-lk-enabled={on}
          data-testid="background-effects"
        >
          {busy ? <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" /> : <Sparkles className="h-5 w-5" aria-hidden="true" />}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        side="top"
        align="center"
        sideOffset={8}
        className={cn(
          'w-[18.5rem] overflow-y-auto p-2',
          // Over the preview it may open downwards, into whatever room is left below.
          variant === 'call'
            ? 'max-h-[min(40rem,calc(100dvh-6rem))]'
            : 'max-h-[min(40rem,calc(var(--radix-dropdown-menu-content-available-height)-0.5rem))]'
        )}
        aria-label="Background effects"
      >
        {variant === 'call' && open && <CallPreview />}
        <DropdownMenuLabel className="px-1 pb-2 pt-0.5 text-xs font-medium text-muted-foreground">Background</DropdownMenuLabel>
        <div className="grid grid-cols-3 gap-2">
          <Tile label="None" selected={choice.kind === 'none' || gone} onSelect={() => choose({ kind: 'none' })}>
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
        </div>
        {/* A guest is not one of the organization, so has its blurs but not its pictures. */}
        {signedIn && (
          <>
            <DropdownMenuLabel className="px-1 pb-2 pt-3 text-xs font-medium text-muted-foreground">Pictures</DropdownMenuLabel>
            {!backgrounds ? (
              <div className="grid grid-cols-3 gap-2" aria-hidden="true">
                {[0, 1, 2].map((i) => (
                  <span key={i} className="block aspect-video animate-pulse rounded-md bg-muted" />
                ))}
              </div>
            ) : companyThemes.length + pictures.length === 0 ? (
              <div className="rounded-md border border-dashed border-border px-3 py-3 text-center" data-testid="background-pictures-empty">
                <p className="text-xs leading-snug text-muted-foreground">Your organization has not added any background pictures yet.</p>
                {mayManage && (
                  <DropdownMenuItem asChild className="mx-auto mt-2 w-fit cursor-pointer justify-center gap-1.5 text-xs font-medium text-primary">
                    <Link to="/organization?tab=appearance">
                      <ImagePlus className="h-3.5 w-3.5" aria-hidden="true" />
                      Add pictures
                    </Link>
                  </DropdownMenuItem>
                )}
              </div>
            ) : (
              <div className="grid grid-cols-3 gap-2" role="group" aria-label="Pictures">
                {companyThemes.map((t) => (
                  <Tile
                    key={t.theme}
                    label={t.label}
                    selected={sameChoice(choice, { kind: 'company', theme: t.theme })}
                    onSelect={() => choose({ kind: 'company', theme: t.theme })}
                  >
                    <CompanyThumb theme={t.theme} />
                  </Tile>
                ))}
                {pictures.map((p) => (
                  <Tile
                    key={p.id}
                    label={p.name}
                    selected={sameChoice(choice, { kind: 'picture', id: p.id })}
                    onSelect={() => choose({ kind: 'picture', id: p.id })}
                  >
                    <img src={p.thumbUrl} alt="" className="h-full w-full object-cover" draggable={false} loading="lazy" />
                  </Tile>
                ))}
              </div>
            )}
          </>
        )}
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
  );
}

/**
 * Your camera at the top of the menu, with what you choose on it, so you see it
 * come on before everybody else does.
 *
 * While the camera has no processor, it shows a copy of the camera (never the one
 * sent) through the processor being made ready, over the camera as it is sent;
 * the camera takes that processor once it shows the choice there
 * (attachCameraProcessor), and the copy goes once the camera's own picture shows
 * it too. While the camera has one, a change is made to it in place, at once, so
 * the camera as sent is the preview.
 */
function CallPreview() {
  const { cameraTrack } = useLocalParticipant();
  const track = cameraTrack?.track as LocalVideoTrack | undefined;
  const live = useCameraLive(track);
  if (!track || !live) return null;
  return <EffectsPreview track={track} />;
}

function EffectsPreview({ track }: { track: LocalVideoTrack }) {
  const busy = useApplying();
  const sent = useRef<HTMLVideoElement>(null);
  const copy = useRef<HTMLVideoElement>(null);
  // Counts the processor coming on and off, which changes what is shown.
  const [round, setRound] = useState(0);

  useEffect(() => {
    const el = sent.current;
    if (!el) return;
    track.attach(el);
    return () => {
      track.detach(el);
    };
  }, [track]);

  useEffect(() => {
    const el = copy.current;
    if (!el) return;
    const show = (shown: boolean) => (el.dataset.shown = String(shown));
    if (track.getProcessor()) {
      const changed = () => !track.getProcessor() && setRound((r) => r + 1);
      track.on(TrackEvent.TrackProcessorUpdate, changed);
      return () => {
        track.off(TrackEvent.TrackProcessorUpdate, changed);
      };
    }
    const camera = track.mediaStreamTrack.clone();
    const settings = track.getSourceTrackSettings();
    let stopped = false;
    let preview: import('./cameraProcessor').Preview | null = null;
    void import('./cameraProcessor').then(async (m) => {
      if (stopped) return;
      preview = m.openPreview(camera, { width: settings.width, height: settings.height });
      const out = await preview.output;
      if (!out || stopped) return;
      el.srcObject = new MediaStream([out]);
      void el.play().catch(() => undefined);
      afterFrame(el, () => !stopped && show(true));
    });
    const changed = () => {
      const shownSent = sent.current;
      if (!track.getProcessor() || !shownSent) return;
      // The camera has taken the processor: once its own picture shows it, the copy goes.
      afterFrame(shownSent, () => {
        if (stopped) return;
        show(false);
        setRound((r) => r + 1);
      });
    };
    track.on(TrackEvent.TrackProcessorUpdate, changed);
    return () => {
      stopped = true;
      track.off(TrackEvent.TrackProcessorUpdate, changed);
      show(false);
      el.srcObject = null;
      preview?.close();
      camera.stop();
    };
  }, [track, round]);

  return (
    <div
      className="relative mb-2 aspect-video overflow-hidden rounded-md bg-slate-900 ring-1 ring-border"
      data-testid="background-preview"
      role="img"
      aria-label="Preview of your camera"
    >
      <video ref={sent} muted playsInline autoPlay className="absolute inset-0 h-full w-full object-cover" />
      <video
        ref={copy}
        muted
        playsInline
        autoPlay
        data-shown="false"
        data-testid="background-preview-copy"
        className="absolute inset-0 h-full w-full object-cover opacity-0 data-[shown=true]:opacity-100"
      />
      {busy && (
        <span className="absolute bottom-1.5 left-1.5 inline-flex items-center gap-1.5 rounded-full bg-black/60 px-2 py-0.5 text-[11px] font-medium text-white backdrop-blur">
          <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
          Getting it ready…
        </span>
      )}
    </div>
  );
}

/** Calls back once the video has shown its next frame. */
function afterFrame(video: HTMLVideoElement, then: () => void) {
  if (typeof video.requestVideoFrameCallback === 'function') video.requestVideoFrameCallback(() => then());
  else setTimeout(then, 100);
}

/**
 * A round button laid over your camera's preview, before a call. The picture
 * under it is dark whatever the page's theme, so the button is too; one that is
 * on turns light, and one whose device is off turns red, as in the call.
 */
export function previewButtonClass(on: boolean, alarm = false): string {
  return cn(
    'inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-full border transition-colors',
    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white focus-visible:ring-offset-2 focus-visible:ring-offset-black/40',
    'disabled:cursor-not-allowed disabled:opacity-50',
    alarm
      ? 'border-transparent bg-red-600 text-white hover:bg-red-700'
      : on
        ? 'border-transparent bg-white text-slate-900 hover:bg-white/90'
        : 'border-white/25 bg-black/55 text-white backdrop-blur hover:bg-black/70'
  );
}

/** The company background as you would have it, your name included, in small. */
function CompanyThumb({ theme }: { theme: CompanyTheme }) {
  const source = useCompanySource(theme);
  const key = source ? JSON.stringify(source) : '';
  const [picture, setPicture] = useState<{ key: string; src: string } | null>(null);
  useEffect(() => {
    if (!key) return;
    let live = true;
    composeCompanyBackground(JSON.parse(key) as CompanySource, 320, 180)
      .then((src) => live && setPicture({ key, src }))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [key]);
  if (!picture || picture.key !== key) return <span className={cn('block h-full w-full', theme === 'dark' ? 'bg-slate-900' : 'bg-slate-100')} />;
  return <img src={picture.src} alt="" className="h-full w-full object-cover" draggable={false} />;
}

/**
 * One choice in the menu. Choosing keeps the menu open, so the preview shows it
 * coming on and the next can be tried.
 */
function Tile({
  label,
  selected,
  onSelect,
  children,
}: {
  label: string;
  selected: boolean;
  onSelect: () => void;
  children: ReactNode;
}) {
  return (
    <DropdownMenuItem
      onSelect={(e) => {
        e.preventDefault();
        onSelect();
      }}
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
