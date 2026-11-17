// Where a person's name and company role are written on the organization's
// call background, and how. Measured from the owner's Cognecto samples
// (docs/branding/): right-aligned in the top right corner, the name large and
// the role smaller below it, light on the dark template and dark on the light.
//
// Kept apart from the drawing so the arithmetic can be tested without a canvas.

export type CompanyTheme = 'dark' | 'light';

/** The canvas the camera's background is drawn at: the camera's own size. */
export const COMPANY_WIDTH = 1280;
const BASE_HEIGHT = 720;

/** The sample's proportions, on a 1280 x 720 picture. */
const RIGHT_MARGIN = 64; // 5% of the width
const NAME_SIZE = 21.6; // 3% of the height
const NAME_BASELINE = 81.4; // 11.3% of the height
const ROLE_SIZE = 11.6; // 1.6% of the height
const ROLE_BASELINE = 105.1; // 14.6% of the height
/** A name longer than this, as a share of the width, is made smaller to fit. */
const MAX_TEXT_WIDTH = 0.45;

/** The same sans-serif the rest of TeamUp is written in (index.html). */
export const NAMEPLATE_FONT = 'ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif';

export const NAMEPLATE_STYLE: Record<CompanyTheme, { name: string; role: string; nameWeight: number; roleWeight: number }> = {
  dark: { name: '#f2f5fb', role: '#9aa8c3', nameWeight: 600, roleWeight: 400 },
  light: { name: '#0b1220', role: '#4a5565', nameWeight: 500, roleWeight: 400 },
};

export interface TextLine {
  text: string;
  /** A canvas font, such as '600 21.6px system-ui'. */
  font: string;
  size: number;
  colour: string;
  /** The right edge of the text, and its baseline. */
  x: number;
  y: number;
}

/** How wide a piece of text is in a font: the canvas's measureText, or a stand-in in tests. */
export type Measure = (text: string, font: string) => number;

/**
 * The size of the picture for a camera of the given size: the camera's width
 * brought to 1280, at the camera's own shape. The processor stretches the
 * background to cover the camera's picture, so a 16:9 picture behind a 4:3
 * camera would lose its sides, and the name with them; drawn at the camera's
 * shape, the name is always in its corner.
 */
export function companyCanvasSize(cameraWidth?: number, cameraHeight?: number): { width: number; height: number } {
  if (!cameraWidth || !cameraHeight) return { width: COMPANY_WIDTH, height: BASE_HEIGHT };
  return { width: COMPANY_WIDTH, height: Math.round((COMPANY_WIDTH * cameraHeight) / cameraWidth) };
}

/**
 * Where the name and the role go on a picture of this size. The role is left
 * out when there is none. Everything scales with the largest 16:9 area that
 * fits the picture, so the text is the sample's size on a 16:9 camera and no
 * bigger on a squarer one.
 */
export function nameplateLayout(
  width: number,
  height: number,
  name: string,
  role: string,
  theme: CompanyTheme,
  measure: Measure
): TextLine[] {
  const scale = Math.min(width, (height * 16) / 9) / COMPANY_WIDTH;
  const style = NAMEPLATE_STYLE[theme];
  const x = width - RIGHT_MARGIN * scale;
  const maxWidth = width * MAX_TEXT_WIDTH;
  const line = (text: string, size: number, weight: number, colour: string, baseline: number): TextLine => {
    let fitted = size * scale;
    const natural = measure(text, fontOf(weight, fitted));
    // Rounded down, as the font is written to a hundredth of a pixel: never a little over.
    if (natural > maxWidth) fitted = Math.floor(((fitted * maxWidth) / natural) * 100) / 100;
    return { text, font: fontOf(weight, fitted), size: fitted, colour, x, y: baseline * scale };
  };
  const lines: TextLine[] = [];
  const cleanName = name.trim();
  const cleanRole = role.trim();
  if (cleanName) lines.push(line(cleanName, NAME_SIZE, style.nameWeight, style.name, NAME_BASELINE));
  if (cleanRole) lines.push(line(cleanRole, ROLE_SIZE, style.roleWeight, style.role, ROLE_BASELINE));
  return lines;
}

function fontOf(weight: number, size: number): string {
  return `${weight} ${Math.round(size * 100) / 100}px ${NAMEPLATE_FONT}`;
}

/** Writes the lines on a canvas, as laid out. */
export function drawNameplate(ctx: CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D, lines: TextLine[]) {
  ctx.save();
  ctx.textAlign = 'right';
  ctx.textBaseline = 'alphabetic';
  for (const l of lines) {
    ctx.font = l.font;
    ctx.fillStyle = l.colour;
    ctx.fillText(l.text, l.x, l.y);
  }
  ctx.restore();
}
