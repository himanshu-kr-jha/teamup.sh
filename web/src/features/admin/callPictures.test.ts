import { describe, expect, it } from 'vitest';
import { coverCrop, nameFromFile, sizeProblem } from './callPictures';

describe('a call picture before it is sent', () => {
  it('is named from its file', () => {
    expect(nameFromFile('harbour-at-dusk.jpg')).toBe('Harbour at dusk');
    expect(nameFromFile('IMG_2041.JPEG')).toBe('IMG 2041');
    expect(nameFromFile('  office   wall.webp')).toBe('Office wall');
    expect(nameFromFile('.png')).toBe('Background');
    expect(nameFromFile('a'.repeat(80) + '.png')).toHaveLength(60);
  });

  it('is cut to 16:9 from its middle, as the server cuts it', () => {
    expect(coverCrop(1920, 1080)).toEqual({ x: 0, y: 0, width: 1920, height: 1080 });
    expect(coverCrop(3200, 1200)).toEqual({ x: 533, y: 0, width: 2133, height: 1200 });
    expect(coverCrop(1600, 1200)).toEqual({ x: 0, y: 150, width: 1600, height: 900 });
  });

  it('must be at least the camera’s size once cut', () => {
    expect(sizeProblem(1280, 720)).toBe('');
    expect(sizeProblem(4000, 3000)).toBe('');
    expect(sizeProblem(640, 360)).toMatch(/1280 x 720/);
    // Wide enough, but too short once cut to 16:9.
    expect(sizeProblem(1600, 700)).toMatch(/1280 x 720/);
    // A phone held upright.
    expect(sizeProblem(1200, 1600)).toMatch(/1280 x 720/);
  });
});
