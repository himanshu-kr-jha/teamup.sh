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

  /** One soft note. Used when a message arrives. */
  const ping = useCallback(() => {
    try {
      const ctx = context();
      if (!ctx) return;
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
    } catch {
      // No audio available; the message still arrives.
    }
  }, [context]);

  /** Two bursts, the shape of a telephone ring. Used for an incoming call. */
  const ring = useCallback(() => {
    try {
      const ctx = context();
      if (!ctx) return;
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
    } catch {
      // No audio available; the dialog still shows.
    }
  }, [context]);

  const stop = useCallback(() => {
    ctxRef.current?.close().catch(() => {
      // Already closed; nothing to do.
    });
    ctxRef.current = null;
  }, []);

  return { ping, ring, stop };
}
