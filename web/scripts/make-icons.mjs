// Draws TeamUp's app icons in public/ from one picture: the mark the loading
// screen shows (index.html), white on the brand colour. Each icon is drawn at
// its own size by a browser, so the strokes stay crisp rather than scaled.
// Run from web/: node scripts/make-icons.mjs

import { writeFileSync } from 'node:fs';
import { chromium } from '@playwright/test';

const BRAND_TOP = '#6366f1';
const BRAND_BOTTOM = '#4338ca';
// The # of the loading screen, lucide's "hash", on a 24 unit grid.
const MARK = '<line x1="4" x2="20" y1="9" y2="9"/><line x1="4" x2="20" y1="15" y2="15"/><line x1="10" x2="8" y1="3" y2="21"/><line x1="16" x2="14" y1="3" y2="21"/>';

/**
 * The icon as SVG. `radius` rounds the tile (0 is full bleed, for systems that
 * cut their own shape); `mark` is how much of the tile the # spans.
 */
function svg(size, { radius, mark }) {
  const m = size * mark;
  const at = (size - m) / 2;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}">
  <defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${BRAND_TOP}"/><stop offset="1" stop-color="${BRAND_BOTTOM}"/></linearGradient></defs>
  <rect width="${size}" height="${size}" rx="${size * radius}" fill="url(#g)"/>
  <svg x="${at}" y="${at}" width="${m}" height="${m}" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.4" stroke-linecap="round">${MARK}</svg>
</svg>`;
}

const ICONS = [
  // Browsers and Android's launcher where it does not cut a shape: a rounded tile.
  { file: 'public/icons/icon-192.png', size: 192, radius: 0.22, mark: 0.56 },
  { file: 'public/icons/icon-512.png', size: 512, radius: 0.22, mark: 0.56 },
  // Android cuts its own shape and keeps only the middle 80%: full bleed, the mark well inside.
  { file: 'public/icons/icon-maskable-512.png', size: 512, radius: 0, mark: 0.48 },
  // The iPhone home screen rounds the corners itself and wants no transparency.
  { file: 'public/apple-touch-icon.png', size: 180, radius: 0, mark: 0.56 },
  { file: 'public/favicon-32.png', size: 32, radius: 0.22, mark: 0.62 },
];

writeFileSync('public/favicon.svg', svg(64, { radius: 0.22, mark: 0.62 }) + '\n');

const browser = await chromium.launch();
const page = await browser.newPage({ deviceScaleFactor: 1 });
for (const icon of ICONS) {
  await page.setViewportSize({ width: icon.size, height: icon.size });
  await page.setContent(`<html><body style="margin:0;background:transparent">${svg(icon.size, icon)}</body></html>`);
  await page.locator('svg').first().screenshot({ path: icon.file, omitBackground: true });
  console.log(icon.file);
}
await browser.close();
