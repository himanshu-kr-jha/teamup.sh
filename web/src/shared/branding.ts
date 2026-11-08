/**
 * The organization's brand colour, worn by everything that is "primary":
 * buttons, the selected place in the navigation, badges, focus rings.
 *
 * No colour means TeamUp's own indigo, which is what globals.css already says,
 * so applying nothing is the default rather than a colour of its own.
 *
 * The tokens are remembered in this browser too, so the inline script in
 * index.html can put them on screen before the first paint instead of flashing
 * indigo first. That script only copies what is saved; branding.test.ts keeps
 * the two in step.
 */

export const BRANDING_STORAGE_KEY = 'branding-tokens';

/** Colours an administrator can pick with one click. The first is the default. */
export const BRAND_PRESETS: Array<{ name: string; color: string }> = [
  { name: 'Indigo (default)', color: '' },
  { name: 'Blue', color: '#2563eb' },
  { name: 'Sky', color: '#0284c7' },
  { name: 'Teal', color: '#0d9488' },
  { name: 'Green', color: '#16a34a' },
  { name: 'Amber', color: '#d97706' },
  { name: 'Orange', color: '#ea580c' },
  { name: 'Red', color: '#dc2626' },
  { name: 'Rose', color: '#e11d48' },
  { name: 'Purple', color: '#9333ea' },
  { name: 'Slate', color: '#475569' },
];

/** What TeamUp's indigo looks like as a hex colour, for colour pickers. */
export const DEFAULT_BRAND_HEX = '#6366f1';

export function isHexColor(value: string): boolean {
  return /^#[0-9a-fA-F]{6}$/.test(value);
}

function channels(hex: string): [number, number, number] {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** "#6366f1" as the "H S% L%" the CSS variables hold. */
export function hexToHslTokens(hex: string): string {
  const [r, g, b] = channels(hex).map((c) => c / 255);
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let h = 0;
  let s = 0;
  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
    else if (max === g) h = (b - r) / d + 2;
    else h = (r - g) / d + 4;
    h *= 60;
  }
  const round = (x: number) => Math.round(x * 10) / 10;
  return `${round(h)} ${round(s * 100)}% ${round(l * 100)}%`;
}

function luminance(rgb: [number, number, number]): number {
  const [r, g, b] = rgb.map((c) => {
    const v = c / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

const LIGHT_TEXT = '210 40% 98%';
const DARK_TEXT = '222.2 47.4% 11.2%';
const LIGHT_TEXT_LUMINANCE = luminance([248, 250, 252]);
const DARK_TEXT_LUMINANCE = luminance([15, 23, 42]);

/** Whichever of the light and dark text reads better on the colour. */
export function textOn(hex: string): 'light' | 'dark' {
  const bg = luminance(channels(hex));
  const onLight = (LIGHT_TEXT_LUMINANCE + 0.05) / (bg + 0.05);
  const onDark = (bg + 0.05) / (DARK_TEXT_LUMINANCE + 0.05);
  return onLight >= onDark ? 'light' : 'dark';
}

export interface BrandTokens {
  primary: string;
  primaryForeground: string;
}

export function tokensFor(hex: string): BrandTokens | null {
  if (!isHexColor(hex)) return null;
  return {
    primary: hexToHslTokens(hex),
    primaryForeground: textOn(hex) === 'light' ? LIGHT_TEXT : DARK_TEXT,
  };
}

/**
 * Puts the tokens on the page. Set on the root element itself, they win over
 * both the light and the dark palettes in globals.css. index.html does the same
 * thing before the bundle loads.
 */
export function setTokens(tokens: BrandTokens | null, root: HTMLElement = document.documentElement) {
  if (!tokens) {
    for (const name of ['--primary', '--ring', '--primary-foreground']) root.style.removeProperty(name);
    return;
  }
  root.style.setProperty('--primary', tokens.primary);
  root.style.setProperty('--ring', tokens.primary);
  root.style.setProperty('--primary-foreground', tokens.primaryForeground);
}

/** Applies a brand colour ("" for the default) and remembers it for the next load. */
export function applyBrandColor(hex: string) {
  const tokens = tokensFor(hex);
  setTokens(tokens);
  try {
    if (tokens) localStorage.setItem(BRANDING_STORAGE_KEY, JSON.stringify(tokens));
    else localStorage.removeItem(BRANDING_STORAGE_KEY);
  } catch {
    // Private windows: it is still applied, just not remembered.
  }
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', tokens ? hex : '#4f46e5');
}
