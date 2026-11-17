// The organization's call backgrounds, each made personal: an administrator
// puts in one template for a dark look and one for a light one (Organization >
// Appearance), and this browser writes the signed-in person's own name and
// company role on it before it goes behind their camera. One upload, and
// everybody has a background of their own.
//
// The text is drawn on the background itself, which is put in the video before
// it is sent, so others read it the right way round. Your own preview is not
// mirrored in TeamUp (CallStage.tsx), so you read it the same way they do.

import { useQuery } from '@tanstack/react-query';
import { orgApi, type CallBackgrounds } from '@/shared/api/client';
import { useAuthStore } from '@/shared/store/authStore';
import { drawNameplate, nameplateLayout, type CompanyTheme } from './nameplate';

export type { CompanyTheme } from './nameplate';

export const COMPANY_THEMES: { theme: CompanyTheme; label: string }[] = [
  { theme: 'dark', label: 'Company (dark)' },
  { theme: 'light', label: 'Company (light)' },
];

export const CALL_BACKGROUNDS_QUERY_KEY = ['call-backgrounds'] as const;

/**
 * The organization's templates, by address. Undefined while they are being
 * found out, and none for somebody who is not signed in, such as a guest.
 */
export function useCompanyTemplates(): CallBackgrounds | undefined {
  const signedIn = useAuthStore((s) => s.isAuthenticated);
  const { data, isError } = useQuery({
    queryKey: CALL_BACKGROUNDS_QUERY_KEY,
    queryFn: async () => (await orgApi.callBackgrounds()).data,
    enabled: signedIn,
    staleTime: 5 * 60 * 1000,
  });
  if (!signedIn || isError) return { dark: '', light: '' };
  return data;
}

/** What one person's background is made from. */
export interface CompanySource {
  template: string;
  theme: CompanyTheme;
  name: string;
  role: string;
}

/**
 * What your background for a theme is made from: the template and your own
 * name and role. Null when the organization has no template for it, and
 * undefined while that is still being found out.
 */
export function useCompanySource(theme: CompanyTheme | null): CompanySource | null | undefined {
  const templates = useCompanyTemplates();
  const name = useAuthStore((s) => s.user?.name ?? '');
  const role = useAuthStore((s) => s.user?.jobTitle ?? '');
  if (!theme) return null;
  if (!templates) return undefined;
  const template = templates[theme];
  return template ? { template, theme, name, role } : null;
}

// Each picture is made once: the camera and the menu ask for the same one many
// times. A handful is plenty: two themes, at the camera's size and as a tile.
const made = new Map<string, Promise<string>>();
const KEEP = 8;

/**
 * The template with the name and role written on it, at the given size, as a
 * picture the camera's processor can load.
 */
export function composeCompanyBackground(source: CompanySource, width: number, height: number): Promise<string> {
  const key = JSON.stringify([source.template, source.theme, source.name, source.role, width, height]);
  let picture = made.get(key);
  if (!picture) {
    picture = draw(source, width, height);
    // A failure is not kept, so the next try starts afresh.
    picture.catch(() => made.delete(key));
    made.set(key, picture);
    while (made.size > KEEP) made.delete(made.keys().next().value!);
  }
  return picture;
}

/** The nameplate's fonts, served by TeamUp itself: the Latin letters, in the weights used. */
const FONTS = [
  { family: 'Manrope', url: '/fonts/manrope-latin.woff2', weight: '400 600' },
  { family: 'Space Grotesk', url: '/fonts/space-grotesk-latin.woff2', weight: '400 500' },
];
let fontsLoaded: Promise<void> | null = null;

/**
 * Loads the nameplate's fonts once, before the first picture is drawn: a canvas
 * does not wait for a font, and would draw in the fallback. One that fails to
 * load is left out, and the name is written in the system's sans-serif instead.
 */
function loadFonts(): Promise<void> {
  fontsLoaded ??= Promise.all(
    FONTS.map(async (f) => {
      try {
        const face = new FontFace(f.family, `url(${f.url}) format("woff2")`, { weight: f.weight });
        await face.load();
        document.fonts.add(face);
      } catch {
        // The system font stands in.
      }
    })
  ).then(() => undefined);
  return fontsLoaded;
}

async function draw(source: CompanySource, width: number, height: number): Promise<string> {
  await loadFonts();
  const template = await new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error('The company background could not be loaded.'));
    img.src = source.template;
  });
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new Error('This browser cannot draw the company background.');
  // Covering the picture from its middle, as the processor itself would.
  const scale = Math.max(width / template.naturalWidth, height / template.naturalHeight);
  const w = template.naturalWidth * scale;
  const h = template.naturalHeight * scale;
  ctx.imageSmoothingQuality = 'high';
  ctx.drawImage(template, (width - w) / 2, (height - h) / 2, w, h);
  const measure = (text: string, font: string) => {
    ctx.font = font;
    return ctx.measureText(text).width;
  };
  drawNameplate(ctx, nameplateLayout(width, height, source.name, source.role, source.theme, measure));
  return canvas.toDataURL('image/jpeg', 0.92);
}
