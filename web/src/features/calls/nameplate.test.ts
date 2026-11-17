import { describe, expect, it } from 'vitest';
import { companyCanvasSize, nameplateLayout, type Measure } from './nameplate';

// A stand-in for the canvas's measureText: every letter half as wide as the
// font is tall, which is near enough a real sans-serif for the arithmetic.
const measure: Measure = (text, font) => text.length * 0.5 * Number(/([\d.]+)px/.exec(font)![1]);

describe('the name and role on a company background', () => {
  it('sit in the top right as in the sample: right edge 5% in, the name large and the role smaller below it', () => {
    const [name, role] = nameplateLayout(1280, 720, 'Himanshu Kumar Jha', 'AI/ML Engineer', 'dark', measure);
    expect(name.text).toBe('Himanshu Kumar Jha');
    expect(name.x).toBeCloseTo(1280 * 0.95);
    expect(name.y / 720).toBeCloseTo(0.113, 2);
    expect(name.size / 720).toBeCloseTo(0.03, 3);
    expect(name.font).toMatch(/^600 21\.6px /);
    expect(role.text).toBe('AI/ML Engineer');
    expect(role.x).toBe(name.x);
    expect(role.y).toBeGreaterThan(name.y);
    expect(role.y / 720).toBeCloseTo(0.146, 2);
    expect(role.size).toBeLessThan(name.size * 0.6);
    expect(role.font).toMatch(/^400 /);
  });

  it('scale with the picture', () => {
    const small = nameplateLayout(320, 180, 'Alice Adams', 'Designer', 'dark', measure);
    const large = nameplateLayout(1280, 720, 'Alice Adams', 'Designer', 'dark', measure);
    small.forEach((line, i) => {
      expect(line.x * 4).toBeCloseTo(large[i].x);
      expect(line.y * 4).toBeCloseTo(large[i].y);
      expect(line.size * 4).toBeCloseTo(large[i].size);
    });
  });

  it('leave the role out when there is none, and write nothing for nobody', () => {
    const lines = nameplateLayout(1280, 720, 'Alice Adams', '  ', 'light', measure);
    expect(lines.map((l) => l.text)).toEqual(['Alice Adams']);
    expect(nameplateLayout(1280, 720, '', '', 'light', measure)).toEqual([]);
  });

  it('are light on the dark template and dark on the light one', () => {
    const [darkName, darkRole] = nameplateLayout(1280, 720, 'A', 'B', 'dark', measure);
    const [lightName, lightRole] = nameplateLayout(1280, 720, 'A', 'B', 'light', measure);
    const lum = (hex: string) => parseInt(hex.slice(1, 3), 16) + parseInt(hex.slice(3, 5), 16) + parseInt(hex.slice(5, 7), 16);
    expect(lum(darkName.colour)).toBeGreaterThan(600);
    expect(lum(darkRole.colour)).toBeGreaterThan(400);
    expect(lum(lightName.colour)).toBeLessThan(100);
    expect(lum(lightRole.colour)).toBeLessThan(300);
  });

  it("are in the samples' fonts, set a little tight, with the system font to fall back on", () => {
    const [darkName, darkRole] = nameplateLayout(1280, 720, 'A', 'B', 'dark', measure);
    const [lightName] = nameplateLayout(1280, 720, 'A', 'B', 'light', measure);
    expect(darkName.font).toMatch(/^600 21\.6px "Manrope", ui-sans-serif, system-ui/);
    expect(lightName.font).toMatch(/^500 21\.6px "Space Grotesk", ui-sans-serif/);
    expect(darkName.tracking).toBeCloseTo(-0.43, 2);
    expect(lightName.tracking).toBeLessThan(0);
    expect(darkRole.tracking).toBe(0);
  });

  it('make a very long name smaller, so it never runs past the middle', () => {
    const long = 'Maximilian Alexander Konstantin von Habsburg-Lothringen';
    const [name] = nameplateLayout(1280, 720, long, '', 'dark', measure);
    expect(measure(long, name.font)).toBeLessThanOrEqual(1280 * 0.45 + 0.01);
    expect(name.size).toBeLessThan(21.6);
    expect(name.x - measure(long, name.font)).toBeGreaterThan(1280 * 0.45);
  });

  it('stay in the corner and the same size on a squarer camera, and shrink on an upright one', () => {
    const square = nameplateLayout(1280, 960, 'Alice Adams', 'Designer', 'dark', measure);
    const wide = nameplateLayout(1280, 720, 'Alice Adams', 'Designer', 'dark', measure);
    expect(square[0].size).toBeCloseTo(wide[0].size);
    expect(square[0].x).toBeCloseTo(wide[0].x);
    expect(square[0].y).toBeCloseTo(wide[0].y);
    const upright = nameplateLayout(720, 1280, 'Alice Adams', 'Designer', 'dark', measure);
    expect(upright[0].x).toBeLessThan(720);
    expect(upright[0].size).toBeCloseTo(21.6 * (720 / 1280));
  });
});

describe('the size a company background is drawn at', () => {
  it("is 1280 wide at the camera's own shape, and 1280 x 720 when that is not known", () => {
    expect(companyCanvasSize(1280, 720)).toEqual({ width: 1280, height: 720 });
    expect(companyCanvasSize(640, 480)).toEqual({ width: 1280, height: 960 });
    expect(companyCanvasSize(1920, 1080)).toEqual({ width: 1280, height: 720 });
    expect(companyCanvasSize(undefined, undefined)).toEqual({ width: 1280, height: 720 });
    expect(companyCanvasSize(0, 720)).toEqual({ width: 1280, height: 720 });
  });
});
