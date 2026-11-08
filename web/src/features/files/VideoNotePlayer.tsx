import { useEffect, useRef, useState } from 'react';
import { Download, Loader2, Maximize2, Pause, Play, Volume2, VolumeX } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { clock, whenDurationKnown } from './media';

/**
 * A video message: a rounded square of the person talking, its own controls
 * over it in the organization's colour rather than the browser's. Tap to play
 * or pause; a progress line along the bottom; the length, sound, full screen
 * and download in the corners.
 */
export function VideoNotePlayer({
  src,
  filename,
  onDownload,
  className,
  testId = 'video-message',
}: {
  src: string;
  filename: string;
  onDownload?: () => void;
  className?: string;
  testId?: string;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const [playing, setPlaying] = useState(false);
  const [ready, setReady] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [muted, setMuted] = useState(false);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(NaN);

  useEffect(() => {
    const el = video.current;
    if (!el) return;
    setReady(false);
    const stopWatching = whenDurationKnown(el, setDuration);
    const onTime = () => setPosition(el.currentTime);
    const onPlay = () => setPlaying(true);
    const onPause = () => setPlaying(false);
    const onEnd = () => {
      setPlaying(false);
      el.currentTime = 0;
      setPosition(0);
    };
    const onReady = () => setReady(true);
    const onWaiting = () => setWaiting(true);
    const onPlaying = () => setWaiting(false);
    el.addEventListener('timeupdate', onTime);
    el.addEventListener('play', onPlay);
    el.addEventListener('pause', onPause);
    el.addEventListener('ended', onEnd);
    el.addEventListener('loadeddata', onReady);
    el.addEventListener('waiting', onWaiting);
    el.addEventListener('playing', onPlaying);
    if (el.readyState >= 2) setReady(true);
    return () => {
      stopWatching();
      el.removeEventListener('timeupdate', onTime);
      el.removeEventListener('play', onPlay);
      el.removeEventListener('pause', onPause);
      el.removeEventListener('ended', onEnd);
      el.removeEventListener('loadeddata', onReady);
      el.removeEventListener('waiting', onWaiting);
      el.removeEventListener('playing', onPlaying);
    };
  }, [src]);

  const toggle = () => {
    const el = video.current;
    if (!el) return;
    if (el.paused) void el.play().catch(() => setPlaying(false));
    else el.pause();
  };

  const fullscreen = () => {
    const el = video.current as (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null;
    if (!el) return;
    if (el.requestFullscreen) void el.requestFullscreen().catch(() => undefined);
    else el.webkitEnterFullscreen?.();
  };

  const played = Number.isFinite(duration) && duration > 0 ? Math.min(1, position / duration) : 0;
  const corner =
    'flex h-7 w-7 items-center justify-center rounded-full bg-black/45 text-white backdrop-blur-sm transition-colors hover:bg-black/65 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white';

  return (
    <span
      className={cn(
        'group/video relative block aspect-square w-60 max-w-full overflow-hidden rounded-2xl border border-border/70 bg-muted shadow-sm',
        className
      )}
      data-testid={testId}
    >
      <video
        ref={video}
        src={src}
        playsInline
        preload="metadata"
        muted={muted}
        onClick={toggle}
        className={cn('h-full w-full cursor-pointer object-cover transition-opacity duration-300', ready ? 'opacity-100' : 'opacity-0')}
        aria-label={filename}
      />

      {/* Until the first frame is in, a themed placeholder rather than black. */}
      {!ready && (
        <span className="pointer-events-none absolute inset-0 flex items-center justify-center" aria-hidden="true">
          <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
        </span>
      )}

      {!playing && ready && (
        <button
          type="button"
          onClick={toggle}
          aria-label="Play the video message"
          className="absolute left-1/2 top-1/2 flex h-14 w-14 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-lg transition-transform hover:scale-105 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 active:scale-95"
        >
          <Play className="ml-1 h-6 w-6 fill-current" aria-hidden="true" />
        </button>
      )}
      {waiting && playing && (
        <Loader2 className="pointer-events-none absolute left-1/2 top-1/2 h-8 w-8 -translate-x-1/2 -translate-y-1/2 animate-spin text-white drop-shadow" aria-hidden="true" />
      )}

      <span className="absolute left-2 top-2 rounded-full bg-black/45 px-2 py-0.5 text-[11px] font-medium tabular-nums text-white backdrop-blur-sm" data-testid="video-time">
        {clock(playing || position > 0 ? position : duration)}
      </span>
      <span className="absolute right-2 top-2 flex gap-1 opacity-100 transition-opacity [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-hover/video:opacity-100 [@media(hover:hover)]:group-focus-within/video:opacity-100">
        {playing && (
          <button type="button" onClick={toggle} aria-label="Pause the video message" className={corner}>
            <Pause className="h-3.5 w-3.5 fill-current" aria-hidden="true" />
          </button>
        )}
        <button type="button" onClick={() => setMuted((m) => !m)} aria-label={muted ? 'Turn the sound on' : 'Turn the sound off'} className={corner}>
          {muted ? <VolumeX className="h-3.5 w-3.5" aria-hidden="true" /> : <Volume2 className="h-3.5 w-3.5" aria-hidden="true" />}
        </button>
        <button type="button" onClick={fullscreen} aria-label="Full screen" className={corner}>
          <Maximize2 className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
        {onDownload && (
          <button type="button" onClick={onDownload} aria-label={`Download ${filename}`} title="Download" className={corner}>
            <Download className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        )}
      </span>

      <span className="pointer-events-none absolute inset-x-0 bottom-0 h-1 bg-black/25" aria-hidden="true">
        <span className="block h-full bg-primary transition-[width] duration-200 ease-linear" style={{ width: `${played * 100}%` }} />
      </span>
    </span>
  );
}
