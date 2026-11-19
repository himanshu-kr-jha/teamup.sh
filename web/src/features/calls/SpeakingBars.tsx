import { useEffect, useRef, useState } from 'react';
import { Track, type Participant } from 'livekit-client';
import { MicOff } from 'lucide-react';
import { cn } from '@/shared/utils/cn';
import { SILENT, bandsFromLevel, ease, everyFrame, meterFor, type Bands, type Meter } from './speakingMeter';

/** Whether the person has asked their system for less movement. */
export function useReducedMotion(): boolean {
  const query = '(prefers-reduced-motion: reduce)';
  const [reduced, setReduced] = useState(() => typeof window !== 'undefined' && !!window.matchMedia?.(query).matches);
  useEffect(() => {
    const list = window.matchMedia?.(query);
    if (!list) return;
    const changed = () => setReduced(list.matches);
    list.addEventListener('change', changed);
    return () => list.removeEventListener('change', changed);
  }, []);
  return reduced;
}

const SIZES = {
  // On a tile's name bar.
  md: { circle: 'h-5 w-5', bar: 3, gap: 2, rest: 3, max: 12, icon: 'h-3.5 w-3.5' },
  // On a small tile, and in the corner window.
  sm: { circle: 'h-4 w-4', bar: 2, gap: 1.5, rest: 2, max: 9, icon: 'h-3 w-3' },
};

/** What reduced motion shows while somebody speaks: the shape of a voice, still. */
const STILL_SPEAKING: Bands = [0.5, 1, 0.5];

/**
 * A microphone that moves while somebody talks, as in Google Meet: three rounded
 * bars in a small circle, their heights following how loud the person is. Silent
 * but listening, the bars rest as dots; muted, it is the crossed-out microphone.
 *
 * `bare` drops the circle, for inside a button that has its own; `always` follows
 * every sound rather than only what LiveKit counts as speaking, so your own
 * button shows that your microphone is hearing you.
 */
export function SpeakingBars({
  participant,
  speaking,
  muted,
  size = 'md',
  bare = false,
  always = false,
  className,
}: {
  participant: Participant;
  speaking: boolean;
  muted: boolean;
  size?: keyof typeof SIZES;
  bare?: boolean;
  always?: boolean;
  className?: string;
}) {
  const bars = useRef<(HTMLSpanElement | null)[]>([]);
  const reduced = useReducedMotion();
  const s = SIZES[size];

  const draw = (bands: Bands) => {
    bands.forEach((v, i) => {
      const el = bars.current[i];
      if (el) el.style.height = `${(s.rest + (s.max - s.rest) * Math.min(1, Math.max(0, v))).toFixed(1)}px`;
    });
  };

  // Still: a fixed shape while speaking, dots otherwise.
  useEffect(() => {
    if (muted || !reduced) return;
    draw(speaking ? STILL_SPEAKING : SILENT);
    // draw only reads refs and the size.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [muted, reduced, speaking, size]);

  // Moving: the shared loop reads the microphone and writes the heights itself.
  useEffect(() => {
    if (muted || reduced) return;
    let shown: Bands = SILENT;
    let meter: Meter | null = null;
    let heard: MediaStreamTrack | undefined;
    const stop = everyFrame((now) => {
      const track = participant.getTrackPublication(Track.Source.Microphone)?.track?.mediaStreamTrack;
      if (track !== heard) {
        meter?.release();
        meter = track ? meterFor(track) : null;
        heard = track;
      }
      const listening = always || participant.isSpeaking;
      const target = listening ? meter?.read() ?? bandsFromLevel(participant.audioLevel, now) : SILENT;
      const next = ease(shown, target);
      // Nothing moved: leave the page alone.
      if (next.every((v, i) => Math.abs(v - shown[i]) < 0.004)) return;
      shown = next;
      draw(shown);
    });
    return () => {
      stop();
      meter?.release();
    };
    // draw only reads refs and the size.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [participant, muted, reduced, always, size]);

  if (muted) {
    if (bare) return null;
    return (
      <span className={cn('inline-flex shrink-0 items-center justify-center', s.circle, className)}>
        <MicOff className={cn(s.icon, 'text-red-400')} aria-label="Microphone off" />
      </span>
    );
  }

  return (
    <span
      role={bare ? undefined : 'img'}
      aria-label={bare ? undefined : speaking ? 'Speaking' : 'Microphone on'}
      aria-hidden={bare ? true : undefined}
      data-testid="speaking-bars"
      data-speaking={speaking ? 'true' : 'false'}
      data-motion={reduced ? 'still' : 'moving'}
      className={cn(
        'inline-flex shrink-0 items-center justify-center [contain:layout_paint]',
        !bare && cn('rounded-full transition-colors duration-200', s.circle, speaking ? 'bg-green-500' : 'bg-black/45'),
        className
      )}
      style={{ gap: s.gap }}
    >
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          ref={(el) => {
            bars.current[i] = el;
          }}
          className={cn('block rounded-full', bare ? 'bg-current' : 'bg-white')}
          style={{ width: s.bar, height: s.rest }}
        />
      ))}
    </span>
  );
}
