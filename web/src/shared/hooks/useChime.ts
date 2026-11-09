import { useCallback, useRef } from 'react';
import { playSound } from '@/shared/sounds';

/**
 * Short tones for what happens in a call (somebody came in, somebody knocked),
 * synthesised rather than loaded from a file; and the recorded message sound
 * from shared/sounds, which also has the rings and the end of a call.
 *
 * Best effort by design: a browser refuses to play sound until the page has
 * been interacted with, so anything that relies on this must still work
 * silently. Nothing here throws.
 */
export function useChime() {
  const ctxRef = useRef<AudioContext | null>(null);

  const context = useCallback(() => {
    const Ctor = window.AudioContext ?? (window as any).webkitAudioContext;
    if (!Ctor) return null;
    if (!ctxRef.current) ctxRef.current = new Ctor();
    const ctx = ctxRef.current;
    // Suspended until the page has been clicked; resuming is what makes the
    // first sound after an interaction audible.
    if (ctx.state === 'suspended') void ctx.resume();
    return ctx;
  }, []);

  /**
   * Runs a sound now or not at all. A context still suspended by the browser's
   * autoplay rule resumes later, on some later click, and anything already queued
   * would then all play at once; so a sound that could not play straight away is
   * dropped, and whatever went with it (a toast, a prompt) is what tells you.
   */
  const play = useCallback(
    (draw: (ctx: AudioContext) => void) => {
      try {
        const ctx = context();
        if (!ctx) return;
        if (ctx.state === 'running') {
          draw(ctx);
          return;
        }
        const asked = Date.now();
        void ctx
          .resume()
          .then(() => {
            if (Date.now() - asked < 300) draw(ctx);
          })
          .catch(() => {
            // Still not allowed; the page says what happened anyway.
          });
      } catch {
        // No audio available.
      }
    },
    [context]
  );

  /** A note at a time, for the short sounds below. */
  const note = (ctx: AudioContext, frequency: number, at: number, length: number, volume: number) => {
    const gain = ctx.createGain();
    gain.connect(ctx.destination);
    const osc = ctx.createOscillator();
    osc.type = 'sine';
    osc.frequency.value = frequency;
    osc.connect(gain);
    gain.gain.setValueAtTime(0.0001, at);
    gain.gain.exponentialRampToValueAtTime(volume, at + 0.015);
    gain.gain.exponentialRampToValueAtTime(0.0001, at + length);
    osc.start(at);
    osc.stop(at + length);
  };

  /** Two rising notes: somebody came into the call. */
  const joined = useCallback(() => {
    play((ctx) => {
      const now = ctx.currentTime;
      note(ctx, 587, now, 0.18, 0.09);
      note(ctx, 880, now + 0.14, 0.28, 0.09);
    });
  }, [play]);

  /** Two low raps: somebody is at the door and is waiting for an answer. */
  const knock = useCallback(() => {
    play((ctx) => {
      const now = ctx.currentTime;
      note(ctx, 330, now, 0.12, 0.2);
      note(ctx, 330, now + 0.2, 0.12, 0.2);
    });
  }, [play]);

  /** A new message: the recorded notification sound. */
  const ping = useCallback(() => {
    playSound('notification');
  }, []);

  const stop = useCallback(() => {
    ctxRef.current?.close().catch(() => {
      // Already closed; nothing to do.
    });
    ctxRef.current = null;
  }, []);

  return { ping, joined, knock, stop };
}
