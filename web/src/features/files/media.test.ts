import { describe, expect, it } from 'vitest';
import { BARS, clock, madeUpPeaks, nextSpeed, peaksOf } from './media';

describe('voice and video players', () => {
  it('say the time as minutes and seconds, and nothing silly for an unknown length', () => {
    expect(clock(0)).toBe('0:00');
    expect(clock(7.9)).toBe('0:07');
    expect(clock(750)).toBe('12:30');
    expect(clock(Infinity)).toBe('0:00');
    expect(clock(NaN)).toBe('0:00');
  });

  it('draw a waveform from the loudness of each stretch, the loudest full height', () => {
    const samples = new Float32Array(4000);
    for (let i = 2000; i < 3000; i++) samples[i] = Math.sin(i) * 0.5;
    const peaks = peaksOf(samples, 4);
    expect(peaks).toHaveLength(4);
    expect(peaks[2]).toBe(1);
    expect(peaks[0]).toBeCloseTo(0.08); // silence is a sliver, not nothing
  });

  it('make up the same waveform for the same message when it cannot be read', () => {
    const a = madeUpPeaks('chat/abc.webm');
    expect(a).toHaveLength(BARS);
    expect(madeUpPeaks('chat/abc.webm')).toEqual(a);
    expect(madeUpPeaks('chat/xyz.webm')).not.toEqual(a);
    expect(a.every((p) => p >= 0.12 && p <= 1)).toBe(true);
  });

  it('cycle the speed 1, 1.5, 2 and back', () => {
    expect([nextSpeed(1), nextSpeed(1.5), nextSpeed(2)]).toEqual([1.5, 2, 1]);
  });
});
