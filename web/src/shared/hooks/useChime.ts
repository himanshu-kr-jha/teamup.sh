import { useCallback, useRef } from 'react';

/**
 * Short tones, synthesised rather than loaded from a file so there is no audio
 * asset to ship or fail to load.
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

  /** One soft note. Used when a message arrives. */
  const ping = useCallback(() => {
    play((ctx) => {
      const now = ctx.currentTime;
      const gain = ctx.createGain();
      gain.connect(ctx.destination);
      const osc = ctx.createOscillator();
      osc.type = 'sine';
      // A small rise reads as a notification rather than an alarm.
      osc.frequency.setValueAtTime(660, now);
      osc.frequency.exponentialRampToValueAtTime(880, now + 0.12);
      osc.connect(gain);
      gain.gain.setValueAtTime(0.0001, now);
      gain.gain.exponentialRampToValueAtTime(0.08, now + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.35);
      osc.start(now);
      osc.stop(now + 0.35);
    });
  }, [play]);

  /** Two bursts, the shape of a telephone ring. Used for an incoming call. */
  const ring = useCallback(() => {
    play((ctx) => {
      const now = ctx.currentTime;
      const gain = ctx.createGain();
      gain.connect(ctx.destination);
      [0, 0.4].forEach((offset) => {
        const osc = ctx.createOscillator();
        osc.type = 'sine';
        osc.frequency.value = 480;
        osc.connect(gain);
        osc.start(now + offset);
        osc.stop(now + offset + 0.3);
      });
      gain.gain.setValueAtTime(0.0001, now);
      gain.gain.exponentialRampToValueAtTime(0.15, now + 0.05);
      gain.gain.exponentialRampToValueAtTime(0.0001, now + 0.75);
    });
  }, [play]);

  const stop = useCallback(() => {
    ctxRef.current?.close().catch(() => {
      // Already closed; nothing to do.
    });
    ctxRef.current = null;
  }, []);

  return { ping, ring, joined, knock, stop };
}
