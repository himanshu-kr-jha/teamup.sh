import { describe, expect, it } from 'vitest';
import { SILENT, bandsFromLevel, bandsFromSpectrum, ease, type Bands } from './speakingMeter';

describe('the speaking bars', () => {
  const RATE = 48_000;
  const FFT = 256;
  const spectrum = (fill: (hz: number) => number) =>
    Array.from({ length: FFT / 2 }, (_, i) => fill((i * RATE) / FFT));

  it('rest when nothing is heard', () => {
    expect(bandsFromSpectrum(spectrum(() => 0), RATE, FFT)).toEqual([0, 0, 0]);
    expect(bandsFromSpectrum(spectrum(() => 20), RATE, FFT)).toEqual([0, 0, 0]);
    expect(bandsFromLevel(0, 1234)).toEqual([0, 0, 0]);
  });

  it('follow where a voice is loudest', () => {
    const low = bandsFromSpectrum(spectrum((hz) => (hz < 500 ? 220 : 0)), RATE, FFT);
    expect(low[0]).toBeGreaterThan(0.8);
    expect(low[2]).toBe(0);
    const high = bandsFromSpectrum(spectrum((hz) => (hz > 1500 && hz < 4000 ? 220 : 0)), RATE, FFT);
    expect(high[2]).toBeGreaterThan(0.8);
    expect(high[0]).toBe(0);
  });

  it('stay within their bounds however loud it gets', () => {
    for (const b of [...bandsFromSpectrum(spectrum(() => 255), RATE, FFT), ...bandsFromLevel(5, 99)]) {
      expect(b).toBeGreaterThanOrEqual(0);
      expect(b).toBeLessThanOrEqual(1);
    }
  });

  it('from LiveKit level alone, the middle bar is the tallest', () => {
    for (const now of [0, 100, 450, 1000]) {
      const [a, b, c] = bandsFromLevel(0.3, now);
      expect(b).toBeGreaterThan(a);
      expect(b).toBeGreaterThan(c);
    }
  });

  it('rise quickly and fall gently', () => {
    const loud: Bands = [1, 1, 1];
    const up = ease(SILENT, loud);
    const down = ease(loud, SILENT);
    expect(up[0]).toBeGreaterThan(1 - down[0]);
    let shown: Bands = loud;
    for (let i = 0; i < 40; i++) shown = ease(shown, SILENT);
    expect(shown[1]).toBeLessThan(0.01);
  });
});
