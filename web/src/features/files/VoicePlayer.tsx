import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { Download, Loader2, Pause, Play } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { BARS, clock, madeUpPeaks, nextSpeed, peaksOf, whenDurationKnown } from './media';

/** Waveforms already worked out, so scrolling back does not decode again. */
const waveforms = new Map<string, { peaks: number[]; seconds: number }>();

/**
 * Reads a recording for its waveform and length. When it cannot be read (the
 * storage does not allow it, or the browser cannot decode it) the waveform is
 * made up from its name, and the length comes from the player.
 */
function useWaveform(key: string, url: string) {
  const [shape, setShape] = useState(() => waveforms.get(key) ?? null);
  useEffect(() => {
    if (shape || !url) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch(url);
        if (!res.ok) throw new Error(String(res.status));
        const data = await res.arrayBuffer();
        const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext;
        const ctx = new Ctx();
        try {
          const audio = await ctx.decodeAudioData(data);
          const found = { peaks: peaksOf(audio.getChannelData(0)), seconds: audio.duration };
          waveforms.set(key, found);
          if (!cancelled) setShape(found);
        } finally {
          void ctx.close();
        }
      } catch {
        const made = { peaks: madeUpPeaks(key), seconds: NaN };
        if (!cancelled) setShape(made);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [key, url, shape]);
  return shape;
}

/**
 * A voice message: a round play button in the organization's colour, the
 * waveform filling as it plays (click or drag along it to jump), the time, the
 * speed, and a download.
 */
export function VoicePlayer({ src, id, filename, onDownload }: { src: string; id: string; filename: string; onDownload: () => void }) {
  const audio = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const [waiting, setWaiting] = useState(false);
  const [position, setPosition] = useState(0);
  const [duration, setDuration] = useState(NaN);
  const [speed, setSpeed] = useState(1);
  const shape = useWaveform(id, src);
  const peaks = shape?.peaks ?? madeUpPeaks(id);
  const length = Number.isFinite(shape?.seconds) ? shape!.seconds : duration;

  useEffect(() => {
    const el = audio.current;
    if (!el) return;
    const stopWatching = whenDurationKnown(el, setDuration);
    const onTime = () => setPosition(el.currentTime);
    const onPlay = () => setPlaying(true);
    const onPause = () => setPlaying(false);
    const onEnd = () => {
      setPlaying(false);
      setPosition(0);
      el.currentTime = 0;
    };
    const onWaiting = () => setWaiting(true);
    const onPlaying = () => setWaiting(false);
    el.addEventListener('timeupdate', onTime);
    el.addEventListener('play', onPlay);
    el.addEventListener('pause', onPause);
    el.addEventListener('ended', onEnd);
    el.addEventListener('waiting', onWaiting);
    el.addEventListener('playing', onPlaying);
    return () => {
      stopWatching();
      el.removeEventListener('timeupdate', onTime);
      el.removeEventListener('play', onPlay);
      el.removeEventListener('pause', onPause);
      el.removeEventListener('ended', onEnd);
      el.removeEventListener('waiting', onWaiting);
      el.removeEventListener('playing', onPlaying);
    };
  }, [src]);

  useEffect(() => {
    if (audio.current) audio.current.playbackRate = speed;
  }, [speed]);

  const toggle = () => {
    const el = audio.current;
    if (!el) return;
    if (el.paused) {
      // One voice message at a time, as people listen to them.
      document.querySelectorAll<HTMLAudioElement>('audio[data-voice]').forEach((other) => other !== el && other.pause());
      void el.play().catch(() => setPlaying(false));
    } else el.pause();
  };

  const seekTo = (fraction: number) => {
    const el = audio.current;
    if (!el || !Number.isFinite(length) || length <= 0) return;
    el.currentTime = Math.max(0, Math.min(1, fraction)) * length;
    setPosition(el.currentTime);
  };
  const seekFromPointer = (e: PointerEvent<HTMLDivElement>) => {
    const box = e.currentTarget.getBoundingClientRect();
    seekTo((e.clientX - box.left) / box.width);
  };
  const seekByKeys = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!Number.isFinite(length) || length <= 0) return;
    const step = e.shiftKey ? 10 : 5;
    if (e.key === 'ArrowRight') seekTo((position + step) / length);
    else if (e.key === 'ArrowLeft') seekTo((position - step) / length);
    else return;
    e.preventDefault();
  };

  const played = Number.isFinite(length) && length > 0 ? position / length : 0;
  const shown = playing || position > 0 ? position : length;

  return (
    <span
      className="flex w-[17rem] max-w-full items-center gap-2.5 rounded-2xl border border-border/70 bg-background py-2 pl-2 pr-1.5 text-foreground shadow-sm"
      data-testid="voice-message"
    >
      <audio ref={audio} src={src} preload="metadata" data-voice aria-label={filename} />
      <button
        type="button"
        onClick={toggle}
        aria-label={playing ? 'Pause the voice message' : 'Play the voice message'}
        className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-sm transition-transform hover:scale-105 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background active:scale-95"
      >
        {waiting ? (
          <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
        ) : playing ? (
          <Pause className="h-4 w-4 fill-current" aria-hidden="true" />
        ) : (
          <Play className="ml-0.5 h-4 w-4 fill-current" aria-hidden="true" />
        )}
      </button>

      <span className="flex min-w-0 flex-1 flex-col gap-1">
        <div
          role="slider"
          tabIndex={0}
          aria-label="Position in the voice message"
          aria-valuemin={0}
          aria-valuemax={Math.round(Number.isFinite(length) ? length : 0)}
          aria-valuenow={Math.round(position)}
          aria-valuetext={`${clock(position)} of ${clock(length)}`}
          onPointerDown={(e) => {
            e.currentTarget.setPointerCapture(e.pointerId);
            seekFromPointer(e);
          }}
          onPointerMove={(e) => e.buttons === 1 && seekFromPointer(e)}
          onKeyDown={seekByKeys}
          className="flex h-7 cursor-pointer touch-none items-center gap-[2px] rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          data-testid="voice-waveform"
        >
          {peaks.slice(0, BARS).map((p, i) => (
            <span
              key={i}
              className={cn(
                'w-full min-w-[2px] rounded-full transition-colors duration-150',
                (i + 0.5) / BARS <= played ? 'bg-primary' : 'bg-muted-foreground/35'
              )}
              style={{ height: `${Math.round(p * 100)}%` }}
            />
          ))}
        </div>
        <span className="flex items-center justify-between text-[11px] leading-none text-muted-foreground tabular-nums">
          <span data-testid="voice-time">{clock(shown)}</span>
        </span>
      </span>

      <span className="flex shrink-0 flex-col items-center gap-0.5">
        <button
          type="button"
          onClick={() => setSpeed((s) => nextSpeed(s))}
          aria-label={`Playback speed ${speed} times; change it`}
          title="Playback speed"
          className="min-w-[2.25rem] rounded-full bg-muted px-1.5 py-0.5 text-[11px] font-semibold tabular-nums text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          {speed}×
        </button>
        <button
          type="button"
          onClick={onDownload}
          aria-label={`Download ${filename}`}
          title="Download"
          className="flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <Download className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
      </span>
    </span>
  );
}
